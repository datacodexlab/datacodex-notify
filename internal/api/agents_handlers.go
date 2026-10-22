// SPDX-License-Identifier: GPL-3.0-only

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"datacodex-notify/internal/agents"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/skillinstall"
	"datacodex-notify/internal/store"
)

// AgentService is the agent registry; *agents.Service implements it.
type AgentService interface {
	Connect(ctx context.Context, req agents.ConnectRequest) (*store.AgentSession, error)
	Disconnect(ctx context.Context, name string) (found bool, err error)
	Gate(ctx context.Context, session string) error
	Touch(ctx context.Context, session string, message bool) error
	List(ctx context.Context) ([]*store.AgentSession, error)
	Session(ctx context.Context, id string) (*store.AgentSession, error)
	SetMuted(ctx context.Context, id string, muted bool) (found bool, err error)
	MuteAll(ctx context.Context) (bool, error)
	SetMuteAll(ctx context.Context, muted bool) error
	NotifyConnect(ctx context.Context) (bool, error)
	SetNotifyConnect(ctx context.Context, enabled bool) error
	Remove(ctx context.Context, id string) (found bool, err error)
}

// SkillManager installs and removes the agent skill; *skillinstall.Manager implements it.
type SkillManager interface {
	Status() []skillinstall.TargetStatus
	Install() error
	Remove() error
}

const (
	agentsPath        = "/api/v1/agents/"
	uiAgentsPath      = "/api/v1/ui/agents"
	statusMutedByUser = http.StatusLocked
)

func (s *Server) requireAgents(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.agents == nil {
			WriteError(w, http.StatusServiceUnavailable, "Agent management is not configured")
			return
		}
		next(w, r)
	}
}

// gateAgent rejects a request from a muted agent with HTTP 423. It returns true when the request may continue.
func (s *Server) gateAgent(w http.ResponseWriter, r *http.Request, session string) bool {
	if s.agents == nil {
		return true
	}
	err := s.agents.Gate(r.Context(), session)
	switch {
	case err == nil:
		return true
	case errors.Is(err, agents.ErrMuted):
		WriteError(w, statusMutedByUser, agents.ErrMuted.Error())
	default:
		WriteError(w, http.StatusInternalServerError, "Failed to check the agent mute state")
	}
	return false
}

// touchAgent records activity after a successful request; a failure is not worth failing the request.
func (s *Server) touchAgent(r *http.Request, session string, message bool) {
	if s.agents != nil {
		_ = s.agents.Touch(r.Context(), session, message)
	}
}

// handleAgentConnect serves POST /agents/connect.
func (s *Server) handleAgentConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Source     string   `json:"source"`
		Session    string   `json:"session"`
		Project    string   `json:"project"`
		RulesFiles []string `json:"rules_files"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	session, err := s.agents.Connect(r.Context(), agents.ConnectRequest{Source: req.Source, Session: req.Session, Project: req.Project, RulesFiles: req.RulesFiles})
	switch {
	case errors.Is(err, agents.ErrMuted):
		WriteError(w, statusMutedByUser, agents.ErrMuted.Error())
	case errors.Is(err, agents.ErrInvalid):
		WriteError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "Failed to register the agent session")
	default:
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "session": toSessionJSON(session)})
	}
}

// handleAgentDisconnect serves POST /agents/disconnect.
func (s *Server) handleAgentDisconnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Session string `json:"session"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid JSON payload: "+err.Error())
		return
	}

	found, err := s.agents.Disconnect(r.Context(), req.Session)
	switch {
	case errors.Is(err, agents.ErrInvalid):
		WriteError(w, http.StatusBadRequest, err.Error())
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "Failed to disconnect the agent session")
	default:
		WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "found": found})
	}
}

type sessionJSON struct {
	store.AgentSession
	Status string `json:"status"`
}

func toSessionJSON(a *store.AgentSession) sessionJSON {
	status := "connected"
	switch {
	case a.DisconnectedAt != nil:
		status = "disconnected"
	case a.Muted:
		status = "muted"
	}
	return sessionJSON{AgentSession: *a, Status: status}
}

// handleUIAgents serves GET /ui/agents: sessions, the global mute, and the skill copies.
func (s *Server) handleUIAgents(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	list, err := s.agents.List(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to list agent sessions")
		return
	}
	sessions := make([]sessionJSON, 0, len(list))
	for _, a := range list {
		sessions = append(sessions, toSessionJSON(a))
	}
	muteAll, err := s.agents.MuteAll(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read the global mute")
		return
	}

	notifyConnect, err := s.agents.NotifyConnect(r.Context())
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to read the connect notice setting")
		return
	}

	out := map[string]interface{}{"sessions": sessions, "mute_all": muteAll, "notify_connect": notifyConnect, "skill": []skillinstall.TargetStatus{}}
	if s.skill != nil {
		out["skill"] = s.skill.Status()
	}
	WriteJSON(w, http.StatusOK, out)
}

// handleUIAgentMuteAll serves POST /ui/agents/mute-all.
func (s *Server) handleUIAgentMuteAll(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Muted bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}
	if err := s.agents.SetMuteAll(r.Context(), req.Muted); err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to change the global mute")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"mute_all": req.Muted})
}

// handleUIAgentNotifyConnect serves POST /ui/agents/notify-connect.
func (s *Server) handleUIAgentNotifyConnect(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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
	if err := s.agents.SetNotifyConnect(r.Context(), req.Enabled); err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to change the connect notice setting")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]bool{"notify_connect": req.Enabled})
}

// handleUIAgentSession serves POST /ui/agents/sessions/{id}/mute and GET .../disconnect-prompt.
func (s *Server) handleUIAgentSession(w http.ResponseWriter, r *http.Request) {
	rest := strings.Trim(strings.TrimPrefix(r.URL.Path, uiAgentsPath+"/sessions/"), "/")
	id, action, hasAction := strings.Cut(rest, "/")
	if id != "" && !hasAction && r.Method == http.MethodDelete {
		s.handleUIAgentRemove(w, r, id)
		return
	}
	if id == "" || !hasAction {
		WriteError(w, http.StatusNotFound, "Unknown agent action")
		return
	}
	if action == "disconnect-prompt" {
		s.handleUIDisconnectPrompt(w, r, id)
		return
	}
	if action != "mute" {
		WriteError(w, http.StatusNotFound, "Unknown agent action")
		return
	}
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		Muted bool `json:"muted"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	found, err := s.agents.SetMuted(r.Context(), id, req.Muted)
	switch {
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "Failed to change the session mute")
	case !found:
		WriteError(w, http.StatusNotFound, "Session not found")
	default:
		WriteJSON(w, http.StatusOK, map[string]bool{"muted": req.Muted})
	}
}

// handleUIAgentRemove serves DELETE /ui/agents/sessions/{id}.
func (s *Server) handleUIAgentRemove(w http.ResponseWriter, r *http.Request, id string) {
	found, err := s.agents.Remove(r.Context(), id)
	switch {
	case err != nil:
		WriteError(w, http.StatusInternalServerError, "Failed to remove the session")
	case !found:
		WriteError(w, http.StatusNotFound, "Session not found")
	default:
		WriteJSON(w, http.StatusOK, map[string]bool{"removed": true})
	}
}

// handleUIRotateToken serves POST /ui/agents/rotate-token. The new token is
// stored and takes effect at once; it is never sent to the browser.
func (s *Server) handleUIRotateToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.tokens == nil {
		WriteError(w, http.StatusServiceUnavailable, "Token rotation is not available")
		return
	}

	token, err := secrets.GenerateSecureToken(32)
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to generate a token")
		return
	}
	if err := s.tokens.Set(secrets.TargetAPIToken, token); err != nil {
		WriteError(w, http.StatusInternalServerError, "Failed to store the new token")
		return
	}
	s.setToken(token)
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "rotated_at": time.Now().UTC()})
}

// handleUISkill serves POST /ui/agents/skill with {"action": "install" | "remove"}.
func (s *Server) handleUISkill(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		WriteError(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	if s.skill == nil {
		WriteError(w, http.StatusServiceUnavailable, "Skill management is not available")
		return
	}
	var req struct {
		Action string `json:"action"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		WriteError(w, http.StatusBadRequest, "Invalid request body")
		return
	}

	var err error
	switch req.Action {
	case "install":
		err = s.skill.Install()
	case "remove":
		err = s.skill.Remove()
	default:
		WriteError(w, http.StatusBadRequest, "The action must be install or remove")
		return
	}
	if err != nil {
		WriteError(w, http.StatusInternalServerError, "The skill could not be changed in every folder")
		return
	}
	WriteJSON(w, http.StatusOK, map[string]interface{}{"success": true, "skill": s.skill.Status()})
}
