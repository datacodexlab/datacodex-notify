// SPDX-License-Identifier: GPL-3.0-only

package tray

import (
	"bytes"
	"strings"
	"testing"

	"datacodex-notify/assets"
	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/wa"
)

func TestIconForState(t *testing.T) {
	tests := []struct {
		state wa.ConnectionState
		want  []byte
	}{
		{wa.StateConnected, assets.IconGreen},
		{wa.StateConnecting, assets.IconYellow},
		{wa.StateReconnecting, assets.IconYellow},
		{wa.StateUnlinked, assets.IconRed},
		{wa.StateLoggedOut, assets.IconRed},
	}
	for _, tc := range tests {
		if !bytes.Equal(iconFor(tc.state), tc.want) {
			t.Errorf("wrong icon for state %s", tc.state)
		}
	}
}

func TestTooltipHasVersionAndStateLabel(t *testing.T) {
	tr := New("1.2.3", locale.NewManager("en"), Actions{})
	got := tr.tooltip(wa.StateConnected)
	if !strings.Contains(got, "v1.2.3") || !strings.Contains(got, "Connected") {
		t.Errorf("unexpected tooltip %q", got)
	}
}

func TestSetStateBeforeReadyOnlyStoresState(t *testing.T) {
	tr := New("1", locale.NewManager("en"), Actions{})
	tr.SetState(wa.StateConnected)
	if tr.state != wa.StateConnected {
		t.Errorf("state = %s", tr.state)
	}
}

func TestPauseTitleFollowsTheState(t *testing.T) {
	loc := locale.NewManager("en")
	if got := pauseTitle(loc, false); got != "Pause for 1 hour" {
		t.Errorf("not paused: %q", got)
	}
	if got := pauseTitle(loc, true); got != "Resume Notifications" {
		t.Errorf("paused: %q", got)
	}
}

func TestPauseAndAutostartStateAreRememberedBeforeTheMenuExists(t *testing.T) {
	tr := New("1", locale.NewManager("en"), Actions{})
	tr.SetPaused(true)
	tr.SetAutostart(true)
	if !tr.paused || !tr.autostart {
		t.Errorf("paused=%v autostart=%v", tr.paused, tr.autostart)
	}
}
