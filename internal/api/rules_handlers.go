// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"encoding/json"
	"net/http"
	"time"

	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/rules"
	"datacodex-notify/internal/store"
)

// Autostarter turns "start with Windows" on and off; *autostart.Manager implements it.
type Autostarter interface {
	Enabled() (bool, error)
	Set(enabled bool) error
}

const maxPauseMinutes = 7 * 24 * 60

type rulesSettingsJSON struct {
	QuietEnabled       bool   `json:"quiet_enabled"`
	QuietStart         string `json:"quiet_start"`
	QuietEnd           string `json:"quiet_end"`
	DedupeMinutes      int    `json:"dedupe_minutes"`
	RateLimitPerHour   int    `json:"rate_limit_per_hour"`
	HeartbeatIntervalS int    `json:"heartbeat_interval_s"`
	HeartbeatGraceS    int    `json:"heartbeat_grace_s"`
}

func toRulesJSON(s rules.Settings) rulesSettingsJSON {
	return rulesSettingsJSON{
		QuietEnabled:       s.QuietEnabled,
		QuietStart:         rules.FormatClock(s.QuietStart),
		QuietEnd:           rules.FormatClock(s.QuietEnd),
		DedupeMinutes:      s.DedupeMinutes,
		RateLimitPerHour:   s.RateLimitPerHour,
		HeartbeatIntervalS: s.HeartbeatIntervalS,
		HeartbeatGraceS:    s.HeartbeatGraceS,
	}
}

type pauseJSON struct {
	Paused     bool       `json:"paused"`
	Indefinite bool       `json:"indefinite"`
	Until      *time.Time `json:"until,omitempty"`
}

func (s *Server) pauseJSON(r *http.Request) (pauseJSON, error) {
	state, err := s.notifySvc.PauseState(r.Context())
	if err != nil {
		return pauseJSON{}, err
	}
	out := pauseJSON{Paused: state.Paused, Indefinite: state.Indefinite}
	if state.Paused && !state.Indefinite {
		out.Until = &state.Until
	}
	return out, nil
}

func (s *Server) handleUIRules(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		settings, err := s.notifySvc.RulesSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to load the rules")
			return
		}
		pause, err := s.pauseJSON(r)
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to read the pause state")
			return
		}

		autostart := map[string]interface{}{"available": s.autostart != nil, "enabled": false}
		if s.autostart != nil {
			if enabled, err := s.autostart.Enabled(); err == nil {
				autostart["enabled"] = enabled
			}
		}

		heartbeats := []heartbeatJSON{}
		if s.heartbeats != nil {
			list, err := s.heartbeats.List(r.Context())
			if err != nil {
				WriteError(w, http.StatusInternalServerError, "Failed to list heartbeats")
				return
			}
			for _, hb := range list {
				heartbeats = append(heartbeats, toHeartbeatJSON(hb))
			}
		}

		WriteJSON(w, http.StatusOK, map[string]interface{}{
			"settings":   toRulesJSON(settings),
			"pause":      pause,
			"autostart":  autostart,
			"heartbeats": heartbeats,
		})

	case http.MethodPut:
		var req rulesSettingsJSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		start, errStart := rules.ParseClock(req.QuietStart)
		end, errEnd := rules.ParseClock(req.QuietEnd)
		if errStart != nil || errEnd != nil {
			WriteError(w, http.StatusBadRequest, "Quiet hours must be written as HH:MM")
			return
		}

		current, err := s.notifySvc.RulesSettings(r.Context())
		if err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to load the rules")
			return
		}
		current.QuietEnabled = req.QuietEnabled
		current.QuietStart, current.QuietEnd = start, end
		current.DedupeMinutes = req.DedupeMinutes
		current.RateLimitPerHour = req.RateLimitPerHour
		current.HeartbeatIntervalS = req.HeartbeatIntervalS
		current.HeartbeatGraceS = req.HeartbeatGraceS
		if err := s.notifySvc.SaveRulesSettings(r.Context(), current); err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, toRulesJSON(current))

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}

// handleUIPause serves POST /ui/pause (minutes, or 0 for until resumed) and DELETE (resume).
func (s *Server) handleUIPause(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodPost:
		var req struct {
			Minutes int `json:"minutes"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if req.Minutes < 0 || req.Minutes > maxPauseMinutes {
			WriteError(w, http.StatusBadRequest, "The pause must be between 0 (until resumed) and 10080 minutes")
			return
		}
		if err := s.notifySvc.Pause(r.Context(), time.Duration(req.Minutes)*time.Minute); err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to pause notifications")
			return
		}

	case http.MethodDelete:
		if err := s.notifySvc.Resume(r.Context()); err != nil {
			WriteError(w, http.StatusInternalServerError, "Failed to resume notifications")
			return
		}
		if released, err := s.notifySvc.ReleaseHeld(r.Context()); err == nil && released > 0 && s.dispatcher != nil {
			s.dispatcher.Wake()
		}

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}

	pause, err := s.pauseJSON(r)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read the pause state")
		return
	}
	WriteJSON(w, http.StatusOK, pause)
}

// handleUIAutostart serves PUT /ui/autostart.
func (s *Server) handleUIAutostart(w http.ResponseWriter, r *http.Request) {
	if s.autostart == nil {
		WriteError(w, http.StatusServiceUnavailable, "Autostart is not available")
		return
	}
	if r.Method != http.MethodPut {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Enabled bool `json:"enabled"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := s.autostart.Set(req.Enabled); err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to change the startup setting")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"enabled": req.Enabled})
}

type alertSettingJSON struct {
	Kind    notify.AlertType `json:"kind"`
	Enabled bool             `json:"enabled"`
	Mode    store.Mode       `json:"mode"`
}

func (s *Server) alertSettingsJSON(r *http.Request) []alertSettingJSON {
	all := s.notifySvc.AlertSettings(r.Context())
	out := make([]alertSettingJSON, 0, len(notify.AlertTypes))
	for _, kind := range notify.AlertTypes {
		out = append(out, alertSettingJSON{Kind: kind, Enabled: all[kind].Enabled, Mode: all[kind].Mode})
	}
	return out
}

// handleUIAlerts serves GET /ui/alerts (every app-generated alert kind with its
// switch and delivery mode) and PUT /ui/alerts (change one kind).
func (s *Server) handleUIAlerts(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		WriteJSON(w, http.StatusOK, map[string]interface{}{"alerts": s.alertSettingsJSON(r)})

	case http.MethodPut:
		var req alertSettingJSON
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			WriteError(w, http.StatusBadRequest, "Invalid request body")
			return
		}
		if err := s.notifySvc.SaveAlertSetting(r.Context(), req.Kind, notify.AlertSetting{Enabled: req.Enabled, Mode: req.Mode}); err != nil {
			WriteError(w, http.StatusBadRequest, err.Error())
			return
		}
		WriteJSON(w, http.StatusOK, map[string]interface{}{"alerts": s.alertSettingsJSON(r)})

	default:
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
	}
}
