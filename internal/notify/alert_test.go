// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/store"
)

func TestHeartbeatLostAlertUsesItsMarkerAndIsNeverDeduplicated(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	alert := Alert{Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "claude:core", Text: "Reviewer stopped responding"}

	first, err := f.svc.Alert(ctx, alert)
	if err != nil {
		t.Fatalf("Alert failed: %v", err)
	}
	second, err := f.svc.Alert(ctx, alert)
	if err != nil {
		t.Fatalf("Alert failed: %v", err)
	}

	skull := string(rune(0x1F480))
	if !strings.HasPrefix(first.Text, skull+" *Heartbeat Lost*") || !strings.Contains(first.Text, "claude:core") {
		t.Errorf("alert text = %q", first.Text)
	}
	if first.Level != store.LevelCritical || first.Status != store.StatusQueued || second.Status != store.StatusQueued {
		t.Errorf("alerts must be queued and never deduplicated: %s %s", first.Status, second.Status)
	}
	if f.deliveries(t, first.ID) != 1 {
		t.Errorf("deliveries = %d", f.deliveries(t, first.ID))
	}
}

func TestPlainAlertUsesTheLevelMarker(t *testing.T) {
	f := newRulesFixture(t)
	msg, err := f.svc.Alert(context.Background(), Alert{Level: store.LevelInfo, Source: "heartbeat", Text: "Reviewer is back"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(msg.Text, string(rune(0x1F7E2))+" *Info*") {
		t.Errorf("alert text = %q", msg.Text)
	}
}

func TestAlertsFollowQuietHoursAndPauseLikeAnyMessageOfTheirLevel(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	enableQuietHours(t, f)
	f.clock.t = atClock(23, 0)

	back, _ := f.svc.Alert(ctx, Alert{Level: store.LevelInfo, Source: "heartbeat", Text: "back"})
	lost, _ := f.svc.Alert(ctx, Alert{Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "heartbeat", Text: "lost"})
	if back.Status != store.StatusHeldQuietHours || lost.Status != store.StatusQueued {
		t.Errorf("info alert=%s critical alert=%s", back.Status, lost.Status)
	}

	f.clock.t = atClock(12, 0)
	_ = f.svc.Pause(ctx, time.Hour)
	paused, _ := f.svc.Alert(ctx, Alert{Level: store.LevelWarning, Source: "x", Text: "warn"})
	critical, _ := f.svc.Alert(ctx, Alert{Level: store.LevelCritical, Source: "x", Text: "crit"})
	if paused.Status != store.StatusHeldPaused || critical.Status != store.StatusQueued {
		t.Errorf("warning alert=%s critical alert=%s", paused.Status, critical.Status)
	}
}

func TestAlertErrors(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()

	if _, err := f.svc.Alert(ctx, Alert{Level: "loud", Text: "x"}); err == nil {
		t.Error("an invalid level must be rejected")
	}

	r, _ := f.store.GetRecipient(ctx, f.rid)
	_ = f.store.UpdateRecipient(ctx, r.ID, r.Name, r.PhoneE164, r.JID, false, r.Levels)
	if _, err := f.svc.Alert(ctx, Alert{Level: store.LevelCritical, Text: "x"}); err == nil {
		t.Error("an alert without any enabled recipient must report it")
	}
}
