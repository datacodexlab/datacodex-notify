// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/store"
	"github.com/google/uuid"
	"github.com/skip2/go-qrcode"
)

var e164Regex = regexp.MustCompile(`^\+[1-9]\d{7,14}$`)

func (s *Server) handleUISession(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var body struct {
		LaunchToken string `json:"launch_token"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	s.launchMu.Lock()
	validLaunch := false
	if s.currentLaunchToken != "" && subtle.ConstantTimeCompare([]byte(body.LaunchToken), []byte(s.currentLaunchToken)) == 1 {
		validLaunch = true
		s.currentLaunchToken = "" // One-time token consumed
	}
	s.launchMu.Unlock()

	if !validLaunch {
		WriteError(w, http.StatusUnauthorized, "Invalid or expired launch token")
		return
	}

	// Create session
	sessionID, err := secrets.GenerateSecureToken(32)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to create the session")
		return
	}
	csrfToken, err := secrets.GenerateSecureToken(32)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to create the session")
		return
	}

	s.sessionsMu.Lock()
	s.uiSessions[sessionID] = csrfToken
	s.sessionsMu.Unlock()

	http.SetCookie(w, &http.Cookie{
		Name:     "dcn_session",
		Value:    sessionID,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
	})

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success":    true,
		"csrf_token": csrfToken,
	})
}

func (s *Server) handleUIState(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	state := s.waSender.GetState()
	qr := s.waSender.GetLatestQR()
	activeHBs, _ := s.store.ListActiveHeartbeats(r.Context())
	hasQueued, err := s.store.HasQueuedMessages(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read queue state")
		return
	}

	pairing := s.waSender.PairingStatus()
	var expiresAt int64
	if !pairing.CodeExpiresAt.IsZero() {
		expiresAt = pairing.CodeExpiresAt.Unix()
	}

	home := s.homeSummary(r)

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"home":   home,
		"state":  state,
		"has_qr": qr != "",
		"pairing": map[string]interface{}{
			"active":          pairing.Active,
			"expired":         pairing.Expired,
			"started":         pairing.Started,
			"code_expires_at": expiresAt,
		},
		"has_queued":        hasQueued,
		"active_heartbeats": len(activeHBs),
		"app_version":       s.version,
		"language":          s.locale.GetLanguage(),
	})
}

// handleUIPair starts a fresh pairing on request (throttled and single in-flight in the client).
func (s *Server) handleUIPair(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	result, err := s.waSender.RequestPairing(r.Context())
	if err != nil {
		WriteError(w, http.StatusBadGateway, "Failed to start pairing: "+err.Error())
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"result": result})
}

func (s *Server) handleUIQR(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	qrString := s.waSender.GetLatestQR()
	if qrString == "" {
		WriteError(w, http.StatusNotFound, "No active pairing QR code")
		return
	}

	pngData, err := qrcode.Encode(qrString, qrcode.Medium, 256)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to render QR code: "+err.Error())
		return
	}

	w.Header().Set("Content-Type", "image/png")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(pngData)
}

func (s *Server) handleUIRecipients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		list, err := s.store.ListRecipients(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if list == nil {
			list = []*store.Recipient{}
		}
		WriteJSON(w, http.StatusOK, list)

	case http.MethodPost:
		var req struct {
			Name      string `json:"name"`
			PhoneE164 string `json:"phone_e164"`
			Levels    int    `json:"levels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		req.PhoneE164 = strings.TrimSpace(req.PhoneE164)
		if !e164Regex.MatchString(req.PhoneE164) {
			WriteError(w, http.StatusBadRequest, "Invalid phone number: must be E.164 format (e.g. +201000000001)")
			return
		}

		if req.Name == "" {
			req.Name = req.PhoneE164
		}
		if req.Levels <= 0 {
			req.Levels = 7 // all levels by default
		}

		// Calculate JID from phone
		jid := strings.TrimPrefix(req.PhoneE164, "+") + "@s.whatsapp.net"

		// If connected, check if on WhatsApp
		if s.waSender.GetState() == "connected" {
			isOnWA, realJID, err := s.waSender.IsOnWhatsApp(r.Context(), req.PhoneE164)
			if err == nil && !isOnWA {
				WriteError(w, http.StatusBadRequest, "Phone number is not registered on WhatsApp")
				return
			}
			if realJID != "" {
				jid = realJID
			}
		}

		created, err := s.store.AddRecipient(r.Context(), req.Name, req.PhoneE164, jid, req.Levels)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to add recipient: "+err.Error())
			return
		}
		WriteJSON(w, http.StatusCreated, created)

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleUIRecipientItem(w http.ResponseWriter, r *http.Request) {
	id := s.extractID(r.URL.Path, "/api/v1/ui/recipients/")
	if id == "" {
		WriteError(w, http.StatusBadRequest, "Missing recipient ID")
		return
	}

	switch r.Method {
	case http.MethodPut:
		var req struct {
			Name      string `json:"name"`
			PhoneE164 string `json:"phone_e164"`
			Enabled   bool   `json:"enabled"`
			Levels    int    `json:"levels"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}

		jid := strings.TrimPrefix(req.PhoneE164, "+") + "@s.whatsapp.net"
		err := s.store.UpdateRecipient(r.Context(), id, req.Name, req.PhoneE164, jid, req.Enabled, req.Levels)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	case http.MethodDelete:
		err := s.store.DeleteRecipient(r.Context(), id)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]bool{"success": true})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

func (s *Server) handleUILog(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	q := r.URL.Query()
	limit := 100
	if n, err := strconv.Atoi(q.Get("limit")); err == nil && n > 0 {
		limit = min(n, 200)
	}
	messages, err := s.store.ListRecentMessages(r.Context(), limit, q.Get("level"), q.Get("status"))
	if err != nil {
		WriteError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if messages == nil {
		messages = []*store.Message{}
	}
	WriteJSON(w, http.StatusOK, messages)
}

func (s *Server) handleUITest(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	// The body is optional: {"mode": "text" | "voice" | "both"}. Without it the level default applies.
	var req struct {
		Mode store.Mode `json:"mode"`
	}
	if r.ContentLength != 0 {
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
	}

	testText := s.locale.GetMessage("test_message", nil)
	msg, err := s.notifySvc.ValidateAndEnqueue(r.Context(), &notify.SendRequest{
		Level:     store.LevelInfo,
		Source:    "admin-ui",
		DedupeKey: uuid.New().String(),
		Text:      testText,
		Mode:      req.Mode,
	})
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"id":      msg.ID,
	})
}

func (s *Server) handleUIUnlink(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()

	err := s.waSender.Logout(ctx)
	if err != nil {
		s.waSender.Disconnect()
	}

	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}

// maskPhone shows the first digits and the last two of a phone number: +2010••••••78.
func maskPhone(digits string) string {
	if digits == "" {
		return ""
	}
	if len(digits) <= 5 {
		return "+" + digits
	}
	return "+" + digits[:3] + strings.Repeat("\u2022", len(digits)-5) + digits[len(digits)-2:]
}

// homeSummary gathers the facts the Home screen shows on top of the connection state.
func (s *Server) homeSummary(r *http.Request) map[string]interface{} {
	ctx := r.Context()
	out := map[string]interface{}{
		"linked_phone":     maskPhone(s.waSender.LinkedPhone()),
		"last_sent_at":     nil,
		"recipients":       0,
		"agents_total":     0,
		"agents_connected": 0,
		"paused":           false,
		"pause_until":      nil,
	}
	if at, err := s.store.LastSentAt(ctx); err == nil && at != nil {
		out["last_sent_at"] = at.UTC()
	}
	if list, err := s.store.ListRecipients(ctx); err == nil {
		out["recipients"] = len(list)
	}
	if s.agents != nil {
		if list, err := s.agents.List(ctx); err == nil {
			connected := 0
			for _, a := range list {
				if a.DisconnectedAt == nil {
					connected++
				}
			}
			out["agents_total"] = len(list)
			out["agents_connected"] = connected
		}
	}
	if pause, err := s.notifySvc.PauseState(ctx); err == nil && pause.Paused {
		out["paused"] = true
		if !pause.Indefinite {
			out["pause_until"] = pause.Until.UTC()
		}
	}
	return out
}
