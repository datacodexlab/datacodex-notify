// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"datacodex-notify/internal/store"
)

const settingLanguage = "language"

func (s *Server) handleUISettings(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		WriteJSON(w, http.StatusOK, map[string]string{"language": s.locale.GetLanguage()})

	case http.MethodPut:
		var req struct {
			Language string `json:"language"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if req.Language != "ar" && req.Language != "en" {
			WriteError(w, http.StatusBadRequest, "Unsupported language: must be ar or en")
			return
		}
		if err := s.store.SetSetting(r.Context(), settingLanguage, req.Language); err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to save language")
			return
		}
		s.locale.SetLanguage(req.Language)
		WriteJSON(w, http.StatusOK, map[string]string{"language": req.Language})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleUILogItem serves POST /ui/log/{id}/retry.
func (s *Server) handleUILogItem(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/v1/ui/log/"), "/")
	id, action, found := strings.Cut(rest, "/")
	if !found || id == "" || action != "retry" {
		WriteError(w, http.StatusNotFound, "Unknown log action")
		return
	}

	err := s.store.RetryMessage(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrMessageNotFound):
		WriteError(w, http.StatusNotFound, "Message not found")
		return
	case errors.Is(err, store.ErrMessageNotFailed):
		WriteError(w, http.StatusConflict, "Only failed messages can be retried")
		return
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "Failed to retry message")
		return
	}

	if s.dispatcher != nil {
		s.dispatcher.Wake()
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"success": true})
}
