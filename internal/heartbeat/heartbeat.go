// SPDX-License-Identifier: GPL-3.0-only

// Package heartbeat is the dead-man's switch: a long-running agent reports in
// regularly, and if it stops, one critical alert is raised; when it reports
// again, one "back" message is sent.
package heartbeat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"sync"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/store"
)

// States stored with a heartbeat.
const (
	StateActive  = "active"
	StateAlerted = "alerted"
)

const (
	maxSeconds = 24 * 60 * 60
	maxIDLen   = 128

	// DefaultTick is how often the monitor looks for missed heartbeats.
	DefaultTick = 15 * time.Second
)

// ErrInvalid wraps every validation problem with a request.
var ErrInvalid = errors.New("invalid heartbeat request")

var idPattern = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)

// Alerter raises system messages; *notify.Service implements it.
type Alerter interface {
	Alert(ctx context.Context, a notify.Alert) (*store.Message, error)
}

// DefaultsFunc returns the default interval and grace, in seconds, for new heartbeats.
type DefaultsFunc func(ctx context.Context) (intervalS, graceS int)

// Service tracks heartbeats and raises alerts. Safe for concurrent use.
type Service struct {
	store    *store.Store
	alerts   Alerter
	locale   *locale.Manager
	defaults DefaultsFunc
	now      func() time.Time

	// startedAt delays alerts after a restart: a heartbeat is judged from the
	// later of its last beat and the start of this run, so downtime of the app
	// itself never looks like a missing agent.
	startedAt time.Time
	mu        sync.Mutex // serializes Beat and Check

	// suppress, when it returns true, silences the monitor (all agents are muted).
	suppress func(ctx context.Context) bool
}

// New creates the service.
func New(st *store.Store, alerts Alerter, loc *locale.Manager, defaults DefaultsFunc) *Service {
	s := &Service{store: st, alerts: alerts, locale: loc, defaults: defaults, now: time.Now}
	s.startedAt = s.now()
	return s
}

// SetSuppress makes Check stay silent while fn returns true. Call it before Run.
func (s *Service) SetSuppress(fn func(ctx context.Context) bool) {
	s.suppress = fn
}

// BeatRequest registers a heartbeat or reports in. EverySeconds of 0 and a
// negative GraceSeconds mean "use the stored value, or the default for a new one".
type BeatRequest struct {
	ID           string
	Label        string
	Source       string
	Session      string // the agent session this heartbeat belongs to, if any
	EverySeconds int
	GraceSeconds int
}

func (r BeatRequest) validate() error {
	switch {
	case r.ID == "" || len(r.ID) > maxIDLen || !idPattern.MatchString(r.ID):
		return fmt.Errorf("%w: the id must be 1 to %d characters of letters, digits, and . _ : -", ErrInvalid, maxIDLen)
	case len(r.Session) > maxIDLen:
		return fmt.Errorf("%w: the session name must be at most %d characters", ErrInvalid, maxIDLen)
	case r.EverySeconds < 0 || r.EverySeconds > maxSeconds:
		return fmt.Errorf("%w: every must be between 1 and %d seconds", ErrInvalid, maxSeconds)
	case r.GraceSeconds > maxSeconds:
		return fmt.Errorf("%w: grace must be at most %d seconds", ErrInvalid, maxSeconds)
	}
	return nil
}

// Beat registers or refreshes a heartbeat. recovered is true when it ends an alert.
func (s *Service) Beat(ctx context.Context, req BeatRequest) (hb *store.Heartbeat, recovered bool, err error) {
	if err := req.validate(); err != nil {
		return nil, false, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	previous, err := s.store.GetHeartbeat(ctx, req.ID)
	if err != nil {
		return nil, false, err
	}
	every, grace := req.EverySeconds, req.GraceSeconds
	if previous == nil {
		defaultEvery, defaultGrace := s.defaults(ctx)
		if every == 0 {
			every = defaultEvery
		}
		if grace < 0 {
			grace = defaultGrace
		}
	}

	hb, err = s.store.RegisterOrBeatHeartbeat(ctx, req.ID, req.Label, req.Source, req.Session, every, grace, s.now())
	if err != nil {
		return nil, false, err
	}

	if previous != nil && previous.State == StateAlerted {
		recovered = true
		s.alert(ctx, notify.Alert{
			Type:    notify.TypeHeartbeatBack,
			Level:   store.LevelInfo,
			Source:  sourceOf(hb),
			Project: s.projectOf(ctx, hb),
			Text:    s.locale.GetMessage("heartbeat_recovered", map[string]string{"label": labelOf(hb)}),
		})
	}
	return hb, recovered, nil
}

// Stop removes a heartbeat from monitoring. Stopping an unknown id is not an error.
func (s *Service) Stop(ctx context.Context, id string) (found bool, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	hb, err := s.store.GetHeartbeat(ctx, id)
	if err != nil || hb == nil {
		return false, err
	}
	return true, s.store.DeleteHeartbeat(ctx, id)
}

// List returns the heartbeats that are being monitored.
func (s *Service) List(ctx context.Context) ([]*store.Heartbeat, error) {
	return s.store.ListActiveHeartbeats(ctx)
}

// Check raises one alert for every active heartbeat that missed its deadline.
func (s *Service) Check(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.suppress != nil && s.suppress(ctx) {
		return nil
	}
	list, err := s.store.ListActiveHeartbeats(ctx)
	if err != nil {
		return err
	}
	now := s.now()
	for _, hb := range list {
		if hb.State != StateActive {
			continue
		}
		last := hb.LastBeatAt
		if last.Before(s.startedAt) {
			last = s.startedAt
		}
		deadline := last.Add(time.Duration(hb.IntervalS+hb.GraceS) * time.Second)
		if !now.After(deadline) {
			continue
		}

		s.alert(ctx, notify.Alert{
			Type:    notify.TypeHeartbeatLost,
			Kind:    notify.AlertHeartbeatLost,
			Level:   store.LevelCritical,
			Source:  sourceOf(hb),
			Project: s.projectOf(ctx, hb),
			Text: s.locale.GetMessage("heartbeat_stopped", map[string]string{
				"label":  labelOf(hb),
				"source": s.agentName(sourceOf(hb)),
				"time":   hb.LastBeatAt.Local().Format("15:04"),
			}),
		})
		if err := s.store.UpdateHeartbeatState(ctx, hb.ID, StateAlerted); err != nil {
			return err
		}
	}
	return nil
}

// Run checks for missed heartbeats every tick until ctx ends.
func (s *Service) Run(ctx context.Context, tick time.Duration) {
	ticker := time.NewTicker(tick)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.Check(ctx); err != nil {
				slog.Error("Heartbeat check failed", "err", err)
			}
		}
	}
}

// alert queues a system message. A failure (for example, no recipient yet) is
// logged and does not stop the state change, so the same outage is not re-announced.
func (s *Service) alert(ctx context.Context, a notify.Alert) {
	if _, err := s.alerts.Alert(ctx, a); err != nil {
		slog.Warn("Heartbeat alert was not queued", "err", err)
	}
}

func labelOf(hb *store.Heartbeat) string {
	if hb.Label != "" {
		return hb.Label
	}
	return hb.ID
}

func sourceOf(hb *store.Heartbeat) string {
	if hb.Source != "" {
		return hb.Source
	}
	return "heartbeat"
}

// projectOf returns the project of the agent session a heartbeat belongs to, or "".
func (s *Service) projectOf(ctx context.Context, hb *store.Heartbeat) string {
	if hb.Session == "" {
		return ""
	}
	session, err := s.store.GetAgentSessionByName(ctx, hb.Session)
	if err != nil || session == nil {
		return ""
	}
	return session.Project
}

// agentName is the display name of an agent source (Claude Code, Codex, ...); other sources are shown as they are.
func (s *Service) agentName(source string) string {
	name := s.locale.GetMessage("agent_name_"+source, nil)
	if name == "agent_name_"+source {
		return source
	}
	return name
}
