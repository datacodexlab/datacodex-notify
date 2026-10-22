// SPDX-License-Identifier: GPL-3.0-only

package heartbeat

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
	st, err := store.Open(filepath.Join(t.TempDir(), "heartbeat.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	f := &fixture{store: st, alerts: &recordingAlerter{}, now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	f.svc = New(st, f.alerts, locale.NewManager("en"), func(context.Context) (int, int) { return 600, 300 })
	f.svc.now = func() time.Time { return f.now }
	f.svc.startedAt = f.now
	return f
}

func (f *fixture) advance(d time.Duration) { f.now = f.now.Add(d) }

func (f *fixture) beat(t *testing.T, req BeatRequest) (*store.Heartbeat, bool) {
	t.Helper()
	hb, recovered, err := f.svc.Beat(context.Background(), req)
	if err != nil {
		t.Fatalf("Beat failed: %v", err)
	}
	return hb, recovered
}

func (f *fixture) check(t *testing.T) {
	t.Helper()
	if err := f.svc.Check(context.Background()); err != nil {
		t.Fatalf("Check failed: %v", err)
	}
}

func TestTimeoutRaisesExactlyOneCriticalAlert(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "run-1", Label: "Plan 25", Source: "claude:core", EverySeconds: 60, GraceSeconds: 30})

	f.advance(90 * time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 0 {
		t.Fatal("the deadline is interval plus grace: no alert at exactly 90 s")
	}

	f.advance(time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("expected one alert after the deadline, got %d", len(f.alerts.alerts))
	}
	a := f.alerts.alerts[0]
	if a.Kind != notify.AlertHeartbeatLost || a.Level != store.LevelCritical || a.Source != "claude:core" {
		t.Errorf("unexpected alert: %+v", a)
	}
	for _, want := range []string{"Plan 25", "claude:core", "stopped responding"} {
		if !strings.Contains(a.Text, want) {
			t.Errorf("alert text %q is missing %q", a.Text, want)
		}
	}
	if hb, _ := f.store.GetHeartbeat(context.Background(), "run-1"); hb.State != StateAlerted {
		t.Errorf("state = %s, want alerted", hb.State)
	}

	f.advance(time.Hour)
	f.check(t)
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Errorf("an outage must be announced once, got %d alerts", len(f.alerts.alerts))
	}
}

func TestBeatsKeepTheHeartbeatAlive(t *testing.T) {
	f := newFixture(t)
	req := BeatRequest{ID: "run-1", EverySeconds: 60, GraceSeconds: 0}
	f.beat(t, req)

	for i := 0; i < 10; i++ {
		f.advance(50 * time.Second)
		f.beat(t, BeatRequest{ID: "run-1"})
		f.check(t)
	}
	if len(f.alerts.alerts) != 0 {
		t.Errorf("a beating heartbeat must not alert, got %d", len(f.alerts.alerts))
	}
}

func TestRecoveryRaisesOneInfoAlertAndRearms(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "run-1", Label: "Nightly", EverySeconds: 10, GraceSeconds: 0})

	f.advance(11 * time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Fatalf("expected the loss alert, got %d", len(f.alerts.alerts))
	}

	_, recovered := f.beat(t, BeatRequest{ID: "run-1"})
	if !recovered || len(f.alerts.alerts) != 2 {
		t.Fatalf("recovered=%v alerts=%d", recovered, len(f.alerts.alerts))
	}
	back := f.alerts.alerts[1]
	if back.Level != store.LevelInfo || back.Kind != notify.AlertNone || !strings.Contains(back.Text, "Nightly") || !strings.Contains(back.Text, "back") {
		t.Errorf("unexpected recovery alert: %+v", back)
	}

	if _, again := f.beat(t, BeatRequest{ID: "run-1"}); again || len(f.alerts.alerts) != 2 {
		t.Error("a normal beat after recovery must not alert")
	}

	// The next outage is announced again.
	f.advance(11 * time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 3 || f.alerts.alerts[2].Kind != notify.AlertHeartbeatLost {
		t.Errorf("a second outage must raise a new alert, got %d alerts", len(f.alerts.alerts))
	}
}

func TestStopRemovesFromMonitoring(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "run-1", EverySeconds: 10, GraceSeconds: 0})

	found, err := f.svc.Stop(context.Background(), "run-1")
	if err != nil || !found {
		t.Fatalf("Stop: found=%v err=%v", found, err)
	}
	f.advance(time.Hour)
	f.check(t)
	if len(f.alerts.alerts) != 0 {
		t.Error("a stopped heartbeat must never alert")
	}
	if list, _ := f.svc.List(context.Background()); len(list) != 0 {
		t.Errorf("stopped heartbeat still listed: %+v", list)
	}

	found, err = f.svc.Stop(context.Background(), "run-1")
	if err != nil || found {
		t.Errorf("stopping twice must be harmless: found=%v err=%v", found, err)
	}
}

func TestStopWhileAlertedIsSilent(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "run-1", EverySeconds: 10, GraceSeconds: 0})
	f.advance(11 * time.Second)
	f.check(t)

	_, _ = f.svc.Stop(context.Background(), "run-1")
	if len(f.alerts.alerts) != 1 {
		t.Errorf("stopping must not send a recovery message, got %d alerts", len(f.alerts.alerts))
	}
}

func TestNewHeartbeatsUseDefaultsAndExistingOnesKeepTheirValues(t *testing.T) {
	f := newFixture(t)

	hb, _ := f.beat(t, BeatRequest{ID: "defaults", GraceSeconds: -1})
	if hb.IntervalS != 600 || hb.GraceS != 300 {
		t.Errorf("new heartbeat should use the defaults, got %d/%d", hb.IntervalS, hb.GraceS)
	}

	hb, _ = f.beat(t, BeatRequest{ID: "custom", Label: "L", Source: "S", EverySeconds: 45, GraceSeconds: 5})
	if hb.IntervalS != 45 || hb.GraceS != 5 || hb.Label != "L" || hb.Source != "S" {
		t.Errorf("explicit values must be kept: %+v", hb)
	}

	hb, _ = f.beat(t, BeatRequest{ID: "custom", GraceSeconds: -1})
	if hb.IntervalS != 45 || hb.GraceS != 5 || hb.Label != "L" || hb.Source != "S" {
		t.Errorf("a plain beat must keep the stored values: %+v", hb)
	}

	hb, _ = f.beat(t, BeatRequest{ID: "custom", EverySeconds: 90, GraceSeconds: 0, Label: "New"})
	if hb.IntervalS != 90 || hb.GraceS != 0 || hb.Label != "New" {
		t.Errorf("new values must replace the old ones: %+v", hb)
	}
}

func TestBeatValidation(t *testing.T) {
	f := newFixture(t)
	bad := map[string]BeatRequest{
		"empty id":       {ID: ""},
		"spaces in id":   {ID: "has space"},
		"slash in id":    {ID: "a/b"},
		"long id":        {ID: strings.Repeat("a", 129)},
		"negative every": {ID: "x", EverySeconds: -1},
		"huge every":     {ID: "x", EverySeconds: 24*60*60 + 1},
		"huge grace":     {ID: "x", GraceSeconds: 24*60*60 + 1},
	}
	for name, req := range bad {
		if _, _, err := f.svc.Beat(context.Background(), req); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: got %v, want ErrInvalid", name, err)
		}
	}
	if list, _ := f.svc.List(context.Background()); len(list) != 0 {
		t.Error("invalid requests must not register anything")
	}
}

func TestRestartDoesNotAlertBeforeAFullIntervalHasPassed(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "run-1", EverySeconds: 60, GraceSeconds: 30})

	// The app was off for two hours; the heartbeat looks stale but must not alert yet.
	f.advance(2 * time.Hour)
	f.svc.startedAt = f.now
	f.check(t)
	if len(f.alerts.alerts) != 0 {
		t.Fatal("downtime of the app itself must not look like a missing agent")
	}

	f.advance(91 * time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Errorf("after interval plus grace from the restart the alert is due, got %d", len(f.alerts.alerts))
	}
}

func TestAlertFailureStillMarksTheOutage(t *testing.T) {
	f := newFixture(t)
	f.alerts.err = errors.New("no enabled recipients configured")
	f.beat(t, BeatRequest{ID: "run-1", EverySeconds: 10, GraceSeconds: 0})

	f.advance(11 * time.Second)
	f.check(t)
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Errorf("the failed alert must not be retried on every tick, got %d attempts", len(f.alerts.alerts))
	}
}

func TestCheckHandlesSeveralHeartbeatsIndependently(t *testing.T) {
	f := newFixture(t)
	f.beat(t, BeatRequest{ID: "fast", Label: "Fast", EverySeconds: 10, GraceSeconds: 0})
	f.beat(t, BeatRequest{ID: "slow", Label: "Slow", EverySeconds: 1000, GraceSeconds: 0})

	f.advance(20 * time.Second)
	f.check(t)
	if len(f.alerts.alerts) != 1 || !strings.Contains(f.alerts.alerts[0].Text, "Fast") {
		t.Errorf("only the fast heartbeat should alert: %+v", f.alerts.alerts)
	}
}

func TestSuppressedMonitorStaysSilent(t *testing.T) {
	f := newFixture(t)
	muted := true
	f.svc.SetSuppress(func(context.Context) bool { return muted })
	f.beat(t, BeatRequest{ID: "run-1", EverySeconds: 10, GraceSeconds: 0})

	f.advance(time.Hour)
	f.check(t)
	if len(f.alerts.alerts) != 0 {
		t.Fatalf("a suppressed monitor must not alert, got %d", len(f.alerts.alerts))
	}

	muted = false
	f.check(t)
	if len(f.alerts.alerts) != 1 {
		t.Errorf("once the suppression ends the overdue heartbeat alerts, got %d", len(f.alerts.alerts))
	}
}

func TestHeartbeatRemembersItsSession(t *testing.T) {
	f := newFixture(t)
	hb, _ := f.beat(t, BeatRequest{ID: "run-1", Session: "proj-1", EverySeconds: 60, GraceSeconds: 0})
	if hb.Session != "proj-1" {
		t.Errorf("session = %q", hb.Session)
	}
	hb, _ = f.beat(t, BeatRequest{ID: "run-1"})
	if hb.Session != "proj-1" {
		t.Errorf("a plain beat must keep the session, got %q", hb.Session)
	}
	if _, _, err := f.svc.Beat(context.Background(), BeatRequest{ID: "x", Session: strings.Repeat("a", 129)}); !errors.Is(err, ErrInvalid) {
		t.Errorf("a long session name must be rejected: %v", err)
	}
}

func TestAlertsNameTheAgentAndTheProjectOfTheSession(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.store.UpsertAgentSession(ctx, "claude-code", "acme-k7f2", "acme-shop", nil, f.now); err != nil {
		t.Fatal(err)
	}
	f.beat(t, BeatRequest{ID: "run-1", Label: "Plan 25", Source: "claude-code", Session: "acme-k7f2", EverySeconds: 60, GraceSeconds: 30})

	f.advance(2 * time.Minute)
	f.check(t)
	lost := f.alerts.alerts[0]
	if lost.Project != "acme-shop" || lost.Source != "claude-code" {
		t.Errorf("the alert carries the source and the project: %+v", lost)
	}
	if !strings.Contains(lost.Text, "Claude Code") || strings.Contains(lost.Text, "claude-code") {
		t.Errorf("the text names the agent, not the raw source: %q", lost.Text)
	}

	f.beat(t, BeatRequest{ID: "run-1", Session: "acme-k7f2"})
	if back := f.alerts.alerts[1]; back.Project != "acme-shop" {
		t.Errorf("the recovery alert carries the project: %+v", back)
	}
}
