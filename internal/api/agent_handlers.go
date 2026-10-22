// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"time"

	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/store"
)

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok":      true,
		"version": s.version,
	})
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	state := s.waSender.GetState()
	activeHBs, _ := s.store.ListActiveHeartbeats(r.Context())

	// Count queued messages
	hasQueued, err := s.store.HasQueuedMessages(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read queue state")
		return
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"state":             state,
		"has_queued":        hasQueued,
		"active_heartbeats": len(activeHBs),
		"version":           s.version,
	})
}

func (s *Server) handleNotify(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	var req struct {
		notify.SendRequest
		Session string `json:"session"`
		Wait    int    `json:"wait,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}
	if !s.gateAgent(w, r, req.Session) {
		return
	}

	if req.Project == "" && req.Session != "" {
		if known, err := s.store.GetAgentSessionByName(r.Context(), req.Session); err == nil && known != nil {
			req.Project = known.Project
		}
	}

	msg, err := s.notifySvc.ValidateAndEnqueue(r.Context(), &req.SendRequest)
	if err != nil {
		WriteError(w, http.StatusBadRequest, err.Error())
		return
	}

	s.touchAgent(r, req.Session, true)

	// Wake dispatcher to process immediately
	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}

	status := msg.Status
	if req.Wait > 0 && status == store.StatusQueued {
		wait := time.Duration(min(req.Wait, MaxNotifyWait)) * time.Second
		// The server write timeout is shorter than a long wait; extend it for this response only.
		_ = http.NewResponseController(w).SetWriteDeadline(time.Now().Add(wait + 10*time.Second))
		status = s.waitForSend(r.Context(), msg.ID, wait, status)
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"id":     msg.ID,
		"status": status,
	})
}

// MaxNotifyWait is the longest a send request may wait for the message to be sent, in seconds.
const MaxNotifyWait = 60

var sendWaitPoll = 200 * time.Millisecond

// waitForSend polls until the message leaves the queue or the wait ends, and returns its last status.
func (s *Server) waitForSend(ctx context.Context, id string, wait time.Duration, last store.MessageStatus) store.MessageStatus {
	ctx, cancel := context.WithTimeout(ctx, wait)
	defer cancel()
	ticker := time.NewTicker(sendWaitPoll)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return last
		case <-ticker.C:
			msg, err := s.store.GetMessage(ctx, id)
			if err != nil || msg == nil {
				continue
			}
			last = msg.Status
			if last != store.StatusQueued && last != store.StatusSending {
				return last
			}
		}
	}
}

func (s *Server) handleUIOpen(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	if s.uiOpener != nil {
		go s.uiOpener()
	}

	WriteJSON(w, http.StatusOK, map[string]interface{}{
		"ok": true,
	})
}

func (s *Server) handleInboxStub(w http.ResponseWriter, r *http.Request) {
	WriteError(w, http.StatusNotImplemented, "Inbox API reserved for Phase 4")
}

// Router dispatcher for /api/v1/inbox
func (s *Server) routeInbox(w http.ResponseWriter, r *http.Request) {
	s.handleInboxStub(w, r)
}

func (s *Server) extractID(path, prefix string) string {
	trimmed := strings.TrimPrefix(path, prefix)
	return strings.Trim(trimmed, "/")
}
