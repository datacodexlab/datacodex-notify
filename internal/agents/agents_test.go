// SPDX-License-Identifier: GPL-3.0-only

package agents

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/store"
)

type recordingAlerter struct {
	alerts []notify.Alert
	err    error
}

func (r *recordingAlerter) Alert(_ context.Context, a notify.Alert) (*store.Message, error) {
	r.alerts = append(r.alerts, a)
	return &store.Message{}, r.err
}

type fixture struct {
	svc    *Service
	store  *store.Store
	alerts *recordingAlerter
	now    time.Time
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "agents.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	f := &fixture{store: st, alerts: &recordingAlerter{}, now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	f.svc = New(st, f.alerts, locale.NewManager("en"))
	f.svc.now = func() time.Time { return f.now }
	return f
}

func (f *fixture) connect(t *testing.T, session string) *store.AgentSession {
	t.Helper()
	s, err := f.svc.Connect(context.Background(), ConnectRequest{Source: SourceClaudeCode, Session: session, RulesFiles: []string{`C:\Users\x\.claude\CLAUDE.md`}})
	if err != nil {
		t.Fatalf("Connect failed: %v", err)
	}
	return s
}

func (f *fixture) heartbeat(t *testing.T, id, session string) {
	t.Helper()
	if _, err := f.store.RegisterOrBeatHeartbeat(context.Background(), id, "L", "src", session, 60, 0, f.now); err != nil {
		t.Fatal(err)
	}
}

func (f *fixture) heartbeatIDs(t *testing.T) []string {
	t.Helper()
	list, _ := f.store.ListActiveHeartbeats(context.Background())
	ids := make([]string, len(list))
	for i, hb := range list {
		ids[i] = hb.ID
	}
	return ids
}

func TestConnectRegistersASessionAndSendsNoNoticeByDefault(t *testing.T) {
	f := newFixture(t)
	s := f.connect(t, "proj-1")
	if len(f.alerts.alerts) != 0 {
		t.Fatalf("connect must not send a WhatsApp notice by default: %+v", f.alerts.alerts)
	}
	if on, err := f.svc.NotifyConnect(context.Background()); err != nil || on {
		t.Fatalf("the notice setting defaults to off: on=%v err=%v", on, err)
	}
	if s.Source != SourceClaudeCode || s.SessionName != "proj-1" {
		t.Errorf("unexpected session: %+v", s)
	}
}

func TestConnectSendsANoticeWhenTheUserEnabledIt(t *testing.T) {
	f := newFixture(t)
	if err := f.svc.SetNotifyConnect(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	s := f.connect(t, "proj-1")

	if s.Source != SourceClaudeCode || s.SessionName != "proj-1" || len(s.RulesFiles) != 1 || s.Muted || s.DisconnectedAt != nil || !s.ConnectedAt.Equal(f.now) {
		t.Errorf("unexpected session: %+v", s)
	}
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("expected one notice, got %d", len(f.alerts.alerts))
	}
	a := f.alerts.alerts[0]
	if a.Level != store.LevelInfo || a.Kind != notify.AlertNone || !strings.Contains(a.Text, "proj-1") || !strings.Contains(a.Text, "claude-code") {
		t.Errorf("unexpected notice: %+v", a)
	}
}

func TestReconnectUpdatesTheSessionAndKeepsTheMute(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	first := f.connect(t, "proj-1")
	if _, err := f.svc.SetMuted(ctx, first.ID, true); err != nil {
		t.Fatal(err)
	}
	_, _ = f.svc.Disconnect(ctx, "proj-1")

	f.now = f.now.Add(time.Hour)
	if _, err := f.svc.Connect(ctx, ConnectRequest{Source: SourceCodex, Session: "proj-1"}); !errors.Is(err, ErrMuted) {
		t.Fatalf("a muted session must not reconnect: %v", err)
	}

	if _, err := f.svc.SetMuted(ctx, first.ID, false); err != nil {
		t.Fatal(err)
	}
	again, err := f.svc.Connect(ctx, ConnectRequest{Source: SourceCodex, Session: "proj-1", RulesFiles: []string{`D:\p\AGENTS.md`}})
	if err != nil {
		t.Fatalf("reconnect failed: %v", err)
	}
	if again.ID != first.ID || again.Source != SourceCodex || again.DisconnectedAt != nil || again.RulesFiles[0] != `D:\p\AGENTS.md` || !again.ConnectedAt.Equal(f.now) {
		t.Errorf("reconnect must update the same session: %+v", again)
	}
	if list, _ := f.svc.List(ctx); len(list) != 1 {
		t.Errorf("a name is one session, got %d", len(list))
	}
}

func TestConnectValidation(t *testing.T) {
	f := newFixture(t)
	bad := map[string]ConnectRequest{
		"unknown source": {Source: "bing", Session: "x"},
		"empty source":   {Session: "x"},
		"empty name":     {Source: SourceCodex},
		"spaces":         {Source: SourceCodex, Session: "has space"},
		"long name":      {Source: SourceCodex, Session: strings.Repeat("a", 129)},
		"relative file":  {Source: SourceCodex, Session: "x", RulesFiles: []string{"AGENTS.md"}},
		"too many files": {Source: SourceCodex, Session: "x", RulesFiles: []string{`C:\1`, `C:\2`, `C:\3`, `C:\4`, `C:\5`, `C:\6`, `C:\7`, `C:\8`, `C:\9`}},
	}
	for name, req := range bad {
		if _, err := f.svc.Connect(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if list, _ := f.svc.List(context.Background()); len(list) != 0 || len(f.alerts.alerts) != 0 {
		t.Error("invalid requests must register nothing and notify nobody")
	}
}

func TestSessionMuteRejectsOnlyThatSessionAndStopsItsHeartbeats(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a := f.connect(t, "alpha")
	f.connect(t, "beta")
	f.heartbeat(t, "hb-a", "alpha")
	f.heartbeat(t, "hb-b", "beta")
	f.heartbeat(t, "hb-none", "")

	found, err := f.svc.SetMuted(ctx, a.ID, true)
	if err != nil || !found {
		t.Fatalf("SetMuted: found=%v err=%v", found, err)
	}

	if err := f.svc.Gate(ctx, "alpha"); !errors.Is(err, ErrMuted) {
		t.Errorf("the muted session must be rejected: %v", err)
	}
	if err := f.svc.Gate(ctx, "beta"); err != nil {
		t.Errorf("another session is unaffected: %v", err)
	}
	if err := f.svc.Gate(ctx, ""); err != nil {
		t.Errorf("a request without a session is unaffected: %v", err)
	}
	if ids := strings.Join(f.heartbeatIDs(t), ","); !strings.Contains(ids, "hb-b") || !strings.Contains(ids, "hb-none") || strings.Contains(ids, "hb-a") {
		t.Errorf("only the muted session's heartbeats go away: %s", ids)
	}

	if _, err := f.svc.SetMuted(ctx, a.ID, false); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Gate(ctx, "alpha"); err != nil {
		t.Errorf("unmuting lets the session through again: %v", err)
	}
	if found, _ := f.svc.SetMuted(ctx, "missing", true); found {
		t.Error("an unknown id must report not found")
	}
}

func TestGlobalMuteRejectsEveryoneAndRemovesAllHeartbeats(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "alpha")
	f.heartbeat(t, "hb-a", "alpha")
	f.heartbeat(t, "hb-none", "")

	if err := f.svc.SetMuteAll(ctx, true); err != nil {
		t.Fatal(err)
	}
	for _, session := range []string{"", "alpha", "unknown"} {
		if err := f.svc.Gate(ctx, session); !errors.Is(err, ErrMuted) {
			t.Errorf("session %q must be rejected while everything is muted: %v", session, err)
		}
	}
	if _, err := f.svc.Connect(ctx, ConnectRequest{Source: SourceCodex, Session: "fresh"}); !errors.Is(err, ErrMuted) {
		t.Errorf("connect must be rejected while everything is muted: %v", err)
	}
	if ids := f.heartbeatIDs(t); len(ids) != 0 {
		t.Errorf("all heartbeats must be removed: %v", ids)
	}
	if on, _ := f.svc.MuteAll(ctx); !on {
		t.Error("MuteAll must report true")
	}

	if err := f.svc.SetMuteAll(ctx, false); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Gate(ctx, "alpha"); err != nil {
		t.Errorf("turning the global mute off lets agents through: %v", err)
	}
}

func TestDisconnectMarksStopsHeartbeatsAndNotifies(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "alpha")
	_ = f.svc.SetNotifyConnect(ctx, true)
	f.heartbeat(t, "hb-a", "alpha")
	f.heartbeat(t, "hb-other", "beta")
	f.alerts.alerts = nil

	f.now = f.now.Add(time.Minute)
	found, err := f.svc.Disconnect(ctx, "alpha")
	if err != nil || !found {
		t.Fatalf("Disconnect: found=%v err=%v", found, err)
	}
	got, _ := f.store.GetAgentSessionByName(ctx, "alpha")
	if got.DisconnectedAt == nil || !got.DisconnectedAt.Equal(f.now) {
		t.Errorf("disconnect time not recorded: %+v", got)
	}
	if ids := f.heartbeatIDs(t); len(ids) != 1 || ids[0] != "hb-other" {
		t.Errorf("only the session's heartbeats are removed: %v", ids)
	}
	if len(f.alerts.alerts) != 1 || !strings.Contains(f.alerts.alerts[0].Text, "alpha") || !strings.Contains(strings.ToLower(f.alerts.alerts[0].Text), "disconnected") {
		t.Errorf("expected one disconnect notice: %+v", f.alerts.alerts)
	}

	found, err = f.svc.Disconnect(ctx, "never-connected")
	if err != nil || found {
		t.Errorf("an unknown session is not an error: found=%v err=%v", found, err)
	}
	if _, err := f.svc.Disconnect(ctx, "bad name"); !errors.Is(err, ErrInvalid) {
		t.Errorf("got %v, want ErrInvalid", err)
	}
}

func TestMutedSessionDisconnectsQuietly(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.connect(t, "alpha")
	_, _ = f.svc.SetMuted(ctx, s.ID, true)
	f.alerts.alerts = nil

	if found, err := f.svc.Disconnect(ctx, "alpha"); err != nil || !found {
		t.Fatalf("Disconnect: found=%v err=%v", found, err)
	}
	if len(f.alerts.alerts) != 0 {
		t.Errorf("a muted session must not send a notice: %+v", f.alerts.alerts)
	}
	if got, _ := f.store.GetAgentSessionByName(ctx, "alpha"); got.DisconnectedAt == nil {
		t.Error("it must still be marked disconnected")
	}
}

func TestTouchRecordsActivity(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.connect(t, "alpha")

	f.now = f.now.Add(time.Minute)
	if err := f.svc.Touch(ctx, "alpha", false); err != nil {
		t.Fatal(err)
	}
	got, _ := f.store.GetAgentSessionByName(ctx, "alpha")
	if got.LastSeenAt == nil || !got.LastSeenAt.Equal(f.now) || got.LastMessageAt != nil {
		t.Errorf("a plain touch updates last seen only: %+v", got)
	}

	f.now = f.now.Add(time.Minute)
	_ = f.svc.Touch(ctx, "alpha", true)
	got, _ = f.store.GetAgentSessionByName(ctx, "alpha")
	if got.LastMessageAt == nil || !got.LastMessageAt.Equal(f.now) {
		t.Errorf("a message touch updates last message: %+v", got)
	}

	if err := f.svc.Touch(ctx, "", true); err != nil {
		t.Errorf("an empty session is ignored: %v", err)
	}
	if err := f.svc.Touch(ctx, "unknown", true); err != nil {
		t.Errorf("an unknown session is ignored: %v", err)
	}
}

func TestDisconnectSendsNoNoticeByDefault(t *testing.T) {
	f := newFixture(t)
	f.connect(t, "alpha")
	if found, err := f.svc.Disconnect(context.Background(), "alpha"); err != nil || !found {
		t.Fatalf("Disconnect: found=%v err=%v", found, err)
	}
	if len(f.alerts.alerts) != 0 {
		t.Errorf("no notice expected: %+v", f.alerts.alerts)
	}
}

func TestRemoveDeletesTheSessionAndItsHeartbeats(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	s := f.connect(t, "alpha")
	f.connect(t, "beta")
	f.heartbeat(t, "hb-a", "alpha")
	f.heartbeat(t, "hb-b", "beta")

	found, err := f.svc.Remove(ctx, s.ID)
	if err != nil || !found {
		t.Fatalf("Remove: found=%v err=%v", found, err)
	}
	if got, _ := f.store.GetAgentSessionByName(ctx, "alpha"); got != nil {
		t.Error("the session row must be gone")
	}
	if got, _ := f.store.GetAgentSessionByName(ctx, "beta"); got == nil {
		t.Error("other sessions stay")
	}
	if ids := f.heartbeatIDs(t); len(ids) != 1 || ids[0] != "hb-b" {
		t.Errorf("only the removed session's heartbeats go: %v", ids)
	}
	if found, err := f.svc.Remove(ctx, s.ID); err != nil || found {
		t.Errorf("removing twice reports not found: found=%v err=%v", found, err)
	}
}

func TestNoticeFailureDoesNotFailConnect(t *testing.T) {
	f := newFixture(t)
	_ = f.svc.SetNotifyConnect(context.Background(), true)
	f.alerts.err = errors.New("no enabled recipients configured")
	if _, err := f.svc.Connect(context.Background(), ConnectRequest{Source: SourceGemini, Session: "x"}); err != nil {
		t.Errorf("a notice failure must not fail the connect: %v", err)
	}
}
