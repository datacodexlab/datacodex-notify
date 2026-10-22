// SPDX-License-Identifier: GPL-3.0-only

// Package agents keeps the registry of agent sessions that connected themselves
// through the CLI, and the mute switches that let the user silence them.
package agents

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path/filepath"
	"regexp"
	"strings"
	"time"
	"unicode"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/store"
)

const (
	settingMuteAll = "agents_muted_all"

	maxNameLen   = 128
	maxProject   = 80
	maxRulesFile = 8
	maxPathLen   = 500
)

// Agent sources a session may report.
const (
	SourceClaudeCode = "claude-code"
	SourceCodex      = "codex"
	SourceGemini     = "gemini"
	SourceOther      = "other"
)

var (
	// ErrMuted means the user disabled notifications for this session or for all agents.
	ErrMuted = errors.New("notifications disabled by the user")
	// ErrInvalid wraps every validation problem with a request.
	ErrInvalid = errors.New("invalid agent request")
)

var namePattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// Alerter raises the short notices the app writes itself; *notify.Service implements it.
type Alerter interface {
	Alert(ctx context.Context, a notify.Alert) (*store.Message, error)
}

// Service is the agent registry. Safe for concurrent use.
type Service struct {
	store  *store.Store
	alerts Alerter
	locale *locale.Manager
	now    func() time.Time
}

// New creates the service.
func New(st *store.Store, alerts Alerter, loc *locale.Manager) *Service {
	return &Service{store: st, alerts: alerts, locale: loc, now: time.Now}
}

// ConnectRequest registers or updates a session.
type ConnectRequest struct {
	Source     string
	Session    string
	Project    string
	RulesFiles []string
}

func validSource(source string) bool {
	switch source {
	case SourceClaudeCode, SourceCodex, SourceGemini, SourceOther:
		return true
	}
	return false
}

func validateSession(name string) error {
	if name == "" || len(name) > maxNameLen || !namePattern.MatchString(name) {
		return fmt.Errorf("%w: the session name must be 1 to %d characters of letters, digits, and . _ : -", ErrInvalid, maxNameLen)
	}
	return nil
}

// ValidateProject checks a project name: empty, or up to 80 characters without control characters.
func ValidateProject(project string) error {
	if len([]rune(project)) > maxProject {
		return fmt.Errorf("%w: the project name is limited to %d characters", ErrInvalid, maxProject)
	}
	for _, r := range project {
		if unicode.IsControl(r) {
			return fmt.Errorf("%w: the project name must not contain control characters", ErrInvalid)
		}
	}
	return nil
}

func (r ConnectRequest) validate() error {
	if !validSource(r.Source) {
		return fmt.Errorf("%w: the source must be claude-code, codex, gemini, or other", ErrInvalid)
	}
	if err := validateSession(r.Session); err != nil {
		return err
	}
	if err := ValidateProject(r.Project); err != nil {
		return err
	}
	if len(r.RulesFiles) > maxRulesFile {
		return fmt.Errorf("%w: at most %d rules files", ErrInvalid, maxRulesFile)
	}
	for _, file := range r.RulesFiles {
		if len(file) > maxPathLen || strings.ContainsRune(file, 0) || !filepath.IsAbs(file) {
			return fmt.Errorf("%w: rules files must be absolute paths", ErrInvalid)
		}
	}
	return nil
}

// MuteAll reports whether every agent is muted.
func (s *Service) MuteAll(ctx context.Context) (bool, error) {
	value, err := s.store.GetSetting(ctx, settingMuteAll)
	return value == "true", err
}

// SetMuteAll turns the global mute on or off. Turning it on removes every
// heartbeat, so a muted agent can never cause a false "agent lost" alert.
func (s *Service) SetMuteAll(ctx context.Context, muted bool) error {
	if err := s.store.SetSetting(ctx, settingMuteAll, fmt.Sprintf("%t", muted)); err != nil {
		return err
	}
	if muted {
		return s.store.DeleteAllHeartbeats(ctx)
	}
	return nil
}

// Gate rejects a request from a muted agent. An empty or unknown session is only
// subject to the global mute.
func (s *Service) Gate(ctx context.Context, session string) error {
	all, err := s.MuteAll(ctx)
	if err != nil {
		return err
	}
	if all {
		return ErrMuted
	}
	if session == "" {
		return nil
	}
	known, err := s.store.GetAgentSessionByName(ctx, session)
	if err != nil {
		return err
	}
	if known != nil && known.Muted {
		return ErrMuted
	}
	return nil
}

// Touch records that a session was seen, and that it sent a message when message is true.
func (s *Service) Touch(ctx context.Context, session string, message bool) error {
	if session == "" {
		return nil
	}
	return s.store.TouchAgentSession(ctx, session, s.now(), message)
}

// Connect registers or updates a session. The notice goes to the log, and to WhatsApp only when the user enabled it.
func (s *Service) Connect(ctx context.Context, req ConnectRequest) (*store.AgentSession, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	if err := s.Gate(ctx, req.Session); err != nil {
		return nil, err
	}

	session, err := s.store.UpsertAgentSession(ctx, req.Source, req.Session, strings.TrimSpace(req.Project), req.RulesFiles, s.now())
	if err != nil {
		return nil, err
	}
	s.notice(ctx, "agent_connected", session, true)
	return session, nil
}

// Disconnect marks a session disconnected and removes its heartbeats. The notice
// goes to the log, and to WhatsApp only when the user enabled it and the session is not muted. An unknown session is not an error.
func (s *Service) Disconnect(ctx context.Context, name string) (found bool, err error) {
	if err := validateSession(name); err != nil {
		return false, err
	}
	session, err := s.store.GetAgentSessionByName(ctx, name)
	if err != nil {
		return false, err
	}
	if session == nil {
		return false, nil
	}

	if _, err := s.store.MarkAgentSessionDisconnected(ctx, name, s.now()); err != nil {
		return true, err
	}
	if err := s.store.DeleteHeartbeatsBySession(ctx, name); err != nil {
		return true, err
	}
	s.notice(ctx, "agent_disconnected", session, s.Gate(ctx, name) == nil)
	return true, nil
}

// SetMuted mutes or unmutes one session. Muting also removes its heartbeats.
func (s *Service) SetMuted(ctx context.Context, id string, muted bool) (found bool, err error) {
	session, err := s.store.GetAgentSession(ctx, id)
	if err != nil || session == nil {
		return false, err
	}
	if _, err := s.store.SetAgentSessionMuted(ctx, id, muted); err != nil {
		return true, err
	}
	if muted {
		return true, s.store.DeleteHeartbeatsBySession(ctx, session.SessionName)
	}
	return true, nil
}

// List returns every known session, newest connection first.
func (s *Service) List(ctx context.Context) ([]*store.AgentSession, error) {
	return s.store.ListAgentSessions(ctx)
}

// Session returns one session by id, or nil.
func (s *Service) Session(ctx context.Context, id string) (*store.AgentSession, error) {
	return s.store.GetAgentSession(ctx, id)
}

// NotifyConnect reports whether connect and disconnect also send a WhatsApp notice (default off).
func (s *Service) NotifyConnect(ctx context.Context) (bool, error) {
	value, err := s.store.GetSetting(ctx, notify.AlertEnabledKey(notify.TypeAgentConnect))
	return value == "true", err
}

// SetNotifyConnect turns the WhatsApp notice for connect and disconnect on or off.
func (s *Service) SetNotifyConnect(ctx context.Context, enabled bool) error {
	return s.store.SetSetting(ctx, notify.AlertEnabledKey(notify.TypeAgentConnect), fmt.Sprintf("%t", enabled))
}

// Remove deletes a session from the list, together with its heartbeats.
func (s *Service) Remove(ctx context.Context, id string) (found bool, err error) {
	session, err := s.store.GetAgentSession(ctx, id)
	if err != nil || session == nil {
		return false, err
	}
	if err := s.store.DeleteHeartbeatsBySession(ctx, session.SessionName); err != nil {
		return true, err
	}
	return s.store.DeleteAgentSession(ctx, id)
}

// notice records a connect or disconnect in the app log and, when the user
// enabled it and send is true, sends a short info message in the app language.
// A failure (for example no recipient yet) is logged and never fails the call.
func (s *Service) notice(ctx context.Context, key string, session *store.AgentSession, send bool) {
	slog.Info("Agent session event", "event", key, "session", session.SessionName, "source", session.Source)
	if !send {
		return
	}
	if enabled, err := s.NotifyConnect(ctx); err != nil || !enabled {
		return
	}
	text := s.locale.GetMessage(key, map[string]string{"session": session.SessionName, "source": session.Source})
	if _, err := s.alerts.Alert(ctx, notify.Alert{Type: notify.TypeAgentConnect, Level: store.LevelInfo, Source: "agents", Text: text}); err != nil {
		slog.Warn("Agent notice was not queued", "err", err)
	}
}
