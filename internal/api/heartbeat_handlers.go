// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	"datacodex-notify/internal/heartbeat"
	"datacodex-notify/internal/store"
)

// HeartbeatService is what the heartbeat routes need; *heartbeat.Service implements it.
type HeartbeatService interface {
	Beat(ctx context.Context, req heartbeat.BeatRequest) (hb *store.Heartbeat, recovered bool, err error)
	Stop(ctx context.Context, id string) (found bool, err error)
	List(ctx context.Context) ([]*store.Heartbeat, error)
}

const (
	agentHeartbeatsPath = "/api/v1/heartbeats/"
	uiHeartbeatsPath    = "/api/v1/ui/heartbeats"
)

func (s *Server) requireHeartbeats(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.heartbeats == nil {
			WriteError(w, http.StatusServiceUnavailable, "Heartbeats are not configured")
			return
		}
		next(w, r)
	}
}

func heartbeatID(path, prefix string) (string, bool) {
	raw := strings.Trim(strings.TrimPrefix(path, prefix), "/")
	id, err := url.PathUnescape(raw)
	if err != nil || id == "" || strings.Contains(id, "/") {
		return "", false
	}
	return id, true
}

type heartbeatJSON struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Source     string    `json:"source"`
	Session    string    `json:"session"`
	IntervalS  int       `json:"interval_s"`
	GraceS     int       `json:"grace_s"`
	LastBeatAt time.Time `json:"last_beat_at"`
	State      string    `json:"state"`
}

func toHeartbeatJSON(hb *store.Heartbeat) heartbeatJSON {
	return heartbeatJSON{
		ID: hb.ID, Label: hb.Label, Source: hb.Source, Session: hb.Session, IntervalS: hb.IntervalS,
		GraceS: hb.GraceS, LastBeatAt: hb.LastBeatAt, State: hb.State,
	}
}

// handleAgentHeartbeat serves POST (register or beat) and DELETE (stop) on /heartbeats/{id}.
func (s *Server) handleAgentHeartbeat(w http.ResponseWriter, r *http.Request) {
	id, ok := heartbeatID(r.URL.Path, agentHeartbeatsPath)
	if !ok {
		WriteError(w, http.StatusBadRequest, "Missing or invalid heartbeat ID")
		return
	}

	switch r.Method {
	case http.MethodPost:
		req := struct {
			Label   string `json:"label"`
			Source  string `json:"source"`
			Session string `json:"session"`
			Every   int    `json:"every_s"`
			Grace   *int   `json:"grace_s"`
		}{}
		if r.ContentLength != 0 {
			if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
				WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
				return
			}
		}
		if !s.gateAgent(w, r, req.Session) {
			return
		}
		grace := -1
		if req.Grace != nil {
			grace = *req.Grace
		}

		hb, recovered, err := s.heartbeats.Beat(r.Context(), heartbeat.BeatRequest{
			ID: id, Label: req.Label, Source: req.Source, Session: req.Session, EverySeconds: req.Every, GraceSeconds: grace,
		})
		if errors.Is(err, heartbeat.ErrInvalid) {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to record the heartbeat")
			return
		}
		s.touchAgent(r, req.Session, false)
		out := map[string]interface{}{"heartbeat": toHeartbeatJSON(hb), "recovered": recovered, "status": hb.State}
		WriteJSON(w, http.StatusOK, out)

	case http.MethodDelete:
		s.stopHeartbeat(w, r, id)

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// stopHeartbeat removes a heartbeat; stopping one that does not exist is not an error.
func (s *Server) stopHeartbeat(w http.ResponseWriter, r *http.Request, id string) {
	found, err := s.heartbeats.Stop(r.Context(), id)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to stop the heartbeat")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "found": found})
}

// handleUIHeartbeats serves GET /ui/heartbeats.
func (s *Server) handleUIHeartbeats(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	list, err := s.heartbeats.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to list heartbeats")
		return
	}
	out := make([]heartbeatJSON, 0, len(list))
	for _, hb := range list {
		out = append(out, toHeartbeatJSON(hb))
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleUIHeartbeatItem serves DELETE /ui/heartbeats/{id}.
func (s *Server) handleUIHeartbeatItem(w http.ResponseWriter, r *http.Request) {
	id, ok := heartbeatID(r.URL.Path, uiHeartbeatsPath+"/")
	if !ok {
		WriteError(w, http.StatusBadRequest, "Missing or invalid heartbeat ID")
		return
	}
	if r.Method != http.MethodDelete {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	s.stopHeartbeat(w, r, id)
}
