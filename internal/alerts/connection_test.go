// SPDX-License-Identifier: GPL-3.0-only

package alerts

import (
	"testing"
	"time"

	"datacodex-notify/internal/wa"
)

type probe struct {
	now      time.Time
	windows  int
	warnings [][2]time.Time
	watcher  *Connection
}

func newProbe() *probe {
	p := &probe{now: time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)}
	p.watcher = NewConnection(Options{
		Now:            func() time.Time { return p.now },
		OpenWindow:     func() { p.windows++ },
		WarnDisconnect: func(from, to time.Time) { p.warnings = append(p.warnings, [2]time.Time{from, to}) },
	})
	return p
}

func (p *probe) at(d time.Duration, state wa.ConnectionState) {
	p.now = p.now.Add(d)
	p.watcher.OnState(state)
}

func TestShortOutageIsNotReported(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	p.at(time.Minute, wa.StateReconnecting)
	p.at(4*time.Minute, wa.StateConnected)

	if len(p.warnings) != 0 {
		t.Errorf("an outage under five minutes must not warn: %v", p.warnings)
	}
}

func TestLongOutageIsReportedOnceWithItsBounds(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	lost := p.now.Add(time.Minute)
	p.at(time.Minute, wa.StateReconnecting)
	p.at(2*time.Minute, wa.StateReconnecting) // repeated events keep the original start
	p.at(8*time.Minute, wa.StateConnected)

	if len(p.warnings) != 1 {
		t.Fatalf("expected one warning, got %d", len(p.warnings))
	}
	if !p.warnings[0][0].Equal(lost) || !p.warnings[0][1].Equal(p.now) {
		t.Errorf("window = %v to %v, want %v to %v", p.warnings[0][0], p.warnings[0][1], lost, p.now)
	}

	p.at(time.Minute, wa.StateConnected)
	if len(p.warnings) != 1 {
		t.Error("staying connected must not warn again")
	}
}

func TestExactlyFiveMinutesIsNotLongEnough(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	p.at(time.Minute, wa.StateReconnecting)
	p.at(LongDisconnect, wa.StateConnected)
	if len(p.warnings) != 0 {
		t.Error("the warning needs more than five minutes")
	}
}

func TestFirstConnectionAfterStartupIsNeverAnOutage(t *testing.T) {
	p := newProbe()
	p.at(time.Hour, wa.StateConnecting)
	p.at(time.Hour, wa.StateConnected)
	if len(p.warnings) != 0 || p.windows != 0 {
		t.Errorf("startup must be silent: warnings=%d windows=%d", len(p.warnings), p.windows)
	}
}

func TestSecondOutageIsReportedAgain(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	p.at(time.Minute, wa.StateReconnecting)
	p.at(10*time.Minute, wa.StateConnected)
	p.at(time.Hour, wa.StateReconnecting)
	p.at(10*time.Minute, wa.StateConnected)
	if len(p.warnings) != 2 {
		t.Errorf("each long outage is reported, got %d", len(p.warnings))
	}
}

func TestLogoutOpensTheWindowButNotMoreThanEveryTenMinutes(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	p.at(time.Minute, wa.StateLoggedOut)
	if p.windows != 1 {
		t.Fatalf("a logout opens the window, got %d", p.windows)
	}

	p.at(3*time.Minute, wa.StateLoggedOut)
	p.at(5*time.Minute, wa.StateLoggedOut)
	if p.windows != 1 {
		t.Errorf("repeated logouts inside ten minutes must not reopen the window, got %d", p.windows)
	}

	p.at(2*time.Minute, wa.StateLoggedOut) // exactly ten minutes after the first opening
	if p.windows != 2 {
		t.Errorf("after ten minutes the window may open again, got %d", p.windows)
	}
}

func TestLogoutDoesNotLeaveALongOutageBehind(t *testing.T) {
	p := newProbe()
	p.at(0, wa.StateConnected)
	p.at(time.Minute, wa.StateReconnecting)
	p.at(time.Minute, wa.StateLoggedOut)
	p.at(time.Hour, wa.StateUnlinked)
	p.at(time.Minute, wa.StateConnected) // linked again with a new QR scan

	if len(p.warnings) != 0 {
		t.Errorf("re-linking after a logout is not an outage: %v", p.warnings)
	}
}

func TestMissingCallbacksAreTolerated(t *testing.T) {
	watcher := NewConnection(Options{})
	watcher.OnState(wa.StateConnected)
	watcher.OnState(wa.StateLoggedOut)
}
