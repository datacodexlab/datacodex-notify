// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/rules"
	"datacodex-notify/internal/store"
)

type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

type rulesFixture struct {
	svc   *Service
	store *store.Store
	clock *fakeClock
	rid   string
}

func newRulesFixture(t *testing.T) *rulesFixture {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "rules.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	r, err := s.AddRecipient(context.Background(), "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	svc := NewService(s, locale.NewManager("en"))
	clock := &fakeClock{t: time.Now()}
	svc.now = clock.now
	svc.loc = time.UTC
	return &rulesFixture{svc: svc, store: s, clock: clock, rid: r.ID}
}

func (f *rulesFixture) send(t *testing.T, level store.Level, text string) *store.Message {
	t.Helper()
	msg, err := f.svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: level, Text: text, Source: "agent"})
	if err != nil {
		t.Fatalf("ValidateAndEnqueue(%q) failed: %v", text, err)
	}
	return msg
}

func (f *rulesFixture) deliveries(t *testing.T, id string) int {
	t.Helper()
	d, err := f.store.GetDeliveriesForMessage(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return len(d)
}

func (f *rulesFixture) settings(t *testing.T, mutate func(*rules.Settings)) {
	t.Helper()
	s, err := f.svc.RulesSettings(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mutate(&s)
	if err := f.svc.SaveRulesSettings(context.Background(), s); err != nil {
		t.Fatalf("SaveRulesSettings failed: %v", err)
	}
}

func atClock(hh, mm int) time.Time {
	now := time.Now().UTC()
	return time.Date(now.Year(), now.Month(), now.Day(), hh, mm, 0, 0, time.UTC)
}

func TestDuplicatesAreRecordedButNotDelivered(t *testing.T) {
	f := newRulesFixture(t)

	first := f.send(t, store.LevelInfo, "build finished")
	second := f.send(t, store.LevelInfo, "build finished")
	other := f.send(t, store.LevelInfo, "different text")

	if first.Status != store.StatusQueued || other.Status != store.StatusQueued {
		t.Errorf("first=%s other=%s, want queued", first.Status, other.Status)
	}
	if second.Status != store.StatusDeduplicated || f.deliveries(t, second.ID) != 0 {
		t.Errorf("duplicate: status=%s deliveries=%d", second.Status, f.deliveries(t, second.ID))
	}

	// A duplicate does not extend the window: once the original ages out, the text goes again.
	f.clock.advance(11 * time.Minute)
	again := f.send(t, store.LevelInfo, "build finished")
	if again.Status != store.StatusQueued {
		t.Errorf("after the window: %s, want queued", again.Status)
	}
}

func TestDuplicateKeyAndLevels(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	send := func(level store.Level, text, key string) *store.Message {
		msg, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: level, Text: text, Source: "a", DedupeKey: key})
		if err != nil {
			t.Fatal(err)
		}
		return msg
	}

	if send(store.LevelWarning, "one", "k1").Status != store.StatusQueued {
		t.Fatal("first must queue")
	}
	if send(store.LevelWarning, "totally different text", "k1").Status != store.StatusDeduplicated {
		t.Error("the same explicit key is a duplicate even with different text")
	}
	if send(store.LevelCritical, "one", "k2").Status != store.StatusQueued {
		t.Error("another key is not a duplicate")
	}
	if send(store.LevelCritical, "one", "k2").Status != store.StatusDeduplicated {
		t.Error("critical messages are deduplicated too")
	}
	if got := f.send(t, store.LevelInfo, "same text"); got.Status != store.StatusQueued {
		t.Fatal("first must queue")
	}
	other, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Text: "same text", Source: "other-agent"})
	if other.Status != store.StatusQueued {
		t.Error("the same text from another source is not a duplicate")
	}
}

func TestDedupeCanBeTurnedOff(t *testing.T) {
	f := newRulesFixture(t)
	f.settings(t, func(s *rules.Settings) { s.DedupeMinutes = 0 })

	f.send(t, store.LevelInfo, "repeat")
	if got := f.send(t, store.LevelInfo, "repeat"); got.Status != store.StatusQueued {
		t.Errorf("status = %s, want queued with dedupe off", got.Status)
	}
}

func TestRateLimitPerRecipient(t *testing.T) {
	f := newRulesFixture(t)
	f.settings(t, func(s *rules.Settings) { s.RateLimitPerHour = 3 })
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		if got := f.send(t, store.LevelInfo, fmt.Sprintf("message %d", i)); got.Status != store.StatusQueued {
			t.Fatalf("message %d: %s", i, got.Status)
		}
	}
	limited := f.send(t, store.LevelWarning, "message 4")
	if limited.Status != store.StatusRateLimited || f.deliveries(t, limited.ID) != 0 {
		t.Errorf("over the limit: status=%s deliveries=%d", limited.Status, f.deliveries(t, limited.ID))
	}
	if got := f.send(t, store.LevelCritical, "message 5"); got.Status != store.StatusQueued {
		t.Errorf("critical bypasses the limit, got %s", got.Status)
	}

	// A second recipient is unaffected; only the one over the limit is skipped.
	b, err := f.store.AddRecipient(ctx, "Second", "+201000000002", "201000000002@s.whatsapp.net", 7)
	if err != nil {
		t.Fatal(err)
	}
	mixed := f.send(t, store.LevelInfo, "message 6")
	deliveries, _ := f.store.GetDeliveriesForMessage(ctx, mixed.ID)
	if mixed.Status != store.StatusQueued || len(deliveries) != 1 || deliveries[0].RecipientID != b.ID {
		t.Errorf("only the recipient under the limit should get it: %s %+v", mixed.Status, deliveries)
	}

	// An hour later the window has moved on.
	f.clock.advance(61 * time.Minute)
	if got := f.send(t, store.LevelInfo, "message 7"); got.Status != store.StatusQueued {
		t.Errorf("after an hour: %s", got.Status)
	}
}

func TestPauseHoldsNonCriticalAndReleasesOnResume(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()

	if err := f.svc.Pause(ctx, time.Hour); err != nil {
		t.Fatal(err)
	}
	state, _ := f.svc.PauseState(ctx)
	if !state.Paused || state.Indefinite || !state.Until.After(f.clock.now()) {
		t.Fatalf("unexpected pause state: %+v", state)
	}

	info := f.send(t, store.LevelInfo, "held info")
	warning := f.send(t, store.LevelWarning, "held warning")
	critical := f.send(t, store.LevelCritical, "urgent")
	if info.Status != store.StatusHeldPaused || warning.Status != store.StatusHeldPaused || critical.Status != store.StatusQueued {
		t.Fatalf("statuses: %s %s %s", info.Status, warning.Status, critical.Status)
	}

	if n, err := f.svc.ReleaseHeld(ctx); err != nil || n != 0 {
		t.Errorf("nothing is released while paused: n=%d err=%v", n, err)
	}

	f.clock.advance(61 * time.Minute)
	n, err := f.svc.ReleaseHeld(ctx)
	if err != nil || n != 2 {
		t.Fatalf("released %d (err=%v), want 2", n, err)
	}
	for _, id := range []string{info.ID, warning.ID} {
		if got, _ := f.store.GetMessage(ctx, id); got.Status != store.StatusQueued {
			t.Errorf("message %s is %s after release", id, got.Status)
		}
	}
	if n, _ := f.svc.ReleaseHeld(ctx); n != 0 {
		t.Errorf("a second pass must release nothing, got %d", n)
	}
}

func TestIndefinitePauseEndsWithResume(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	_ = f.svc.Pause(ctx, 0)

	held := f.send(t, store.LevelInfo, "waiting")
	f.clock.advance(1000 * time.Hour)
	if n, _ := f.svc.ReleaseHeld(ctx); n != 0 {
		t.Fatal("an indefinite pause never expires")
	}

	if err := f.svc.Resume(ctx); err != nil {
		t.Fatal(err)
	}
	if state, _ := f.svc.PauseState(ctx); state.Paused {
		t.Error("still paused after Resume")
	}
	if n, _ := f.svc.ReleaseHeld(ctx); n != 1 {
		t.Errorf("released %d, want 1", n)
	}
	if got, _ := f.store.GetMessage(ctx, held.ID); got.Status != store.StatusQueued {
		t.Errorf("status = %s", got.Status)
	}
}

func enableQuietHours(t *testing.T, f *rulesFixture) {
	t.Helper()
	f.settings(t, func(s *rules.Settings) {
		s.QuietEnabled = true
		s.QuietStart = 22 * 60
		s.QuietEnd = 7 * 60
	})
}

func TestQuietHoursHoldInfoAndReleaseOneDigest(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	enableQuietHours(t, f)

	f.clock.t = atClock(23, 0)
	info1 := f.send(t, store.LevelInfo, "first quiet note")
	info2 := f.send(t, store.LevelInfo, "second quiet note")
	warning := f.send(t, store.LevelWarning, "warning passes")
	critical := f.send(t, store.LevelCritical, "critical passes")

	if info1.Status != store.StatusHeldQuietHours || info2.Status != store.StatusHeldQuietHours {
		t.Fatalf("info must be held: %s %s", info1.Status, info2.Status)
	}
	if warning.Status != store.StatusQueued || critical.Status != store.StatusQueued {
		t.Fatalf("warning and critical must pass: %s %s", warning.Status, critical.Status)
	}

	if n, _ := f.svc.ReleaseHeld(ctx); n != 0 {
		t.Errorf("nothing is released during quiet hours, got %d", n)
	}

	f.clock.t = atClock(7, 30)
	n, err := f.svc.ReleaseHeld(ctx)
	if err != nil || n != 1 {
		t.Fatalf("released %d (err=%v), want one digest", n, err)
	}

	queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
	var digest *store.Message
	for _, m := range queued {
		if m.Source == "digest" {
			digest = m
		}
	}
	if digest == nil || digest.Level != store.LevelInfo || digest.Mode != store.ModeText {
		t.Fatalf("no info digest queued: %+v", queued)
	}
	for _, want := range []string{"2 message(s)", "first quiet note", "second quiet note"} {
		if !strings.Contains(digest.Text, want) {
			t.Errorf("digest is missing %q:\n%s", want, digest.Text)
		}
	}
	if f.deliveries(t, digest.ID) != 1 {
		t.Errorf("digest deliveries = %d, want 1", f.deliveries(t, digest.ID))
	}
	for _, id := range []string{info1.ID, info2.ID} {
		if got, _ := f.store.GetMessage(ctx, id); got.Status != store.StatusDigested {
			t.Errorf("original %s is %s, want digested", id, got.Status)
		}
	}
	if n, _ := f.svc.ReleaseHeld(ctx); n != 0 {
		t.Errorf("a second pass must not make another digest, got %d", n)
	}
}

func TestDigestIsCappedAndSkipsDisabledRecipients(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	enableQuietHours(t, f)
	f.settings(t, func(s *rules.Settings) { s.RateLimitPerHour = 0 })

	f.clock.t = atClock(23, 0)
	for i := 0; i < 60; i++ {
		f.send(t, store.LevelInfo, fmt.Sprintf("note %02d %s", i, strings.Repeat("x", 100)))
	}
	f.clock.t = atClock(8, 0)
	if n, err := f.svc.ReleaseHeld(ctx); err != nil || n != 1 {
		t.Fatalf("released %d err=%v", n, err)
	}

	queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
	digest := queued[len(queued)-1]
	if got := len([]rune(digest.Text)); got > maxDigestRunes+300 {
		t.Errorf("digest has %d characters, over the cap", got)
	}
	if !strings.Contains(digest.Text, "more message(s) not shown") {
		t.Errorf("a capped digest must say how many were left out:\n%s", digest.Text[len(digest.Text)-200:])
	}

	// With no enabled recipient left, held messages are closed out without a digest.
	f.clock.t = atClock(23, 0)
	held := f.send(t, store.LevelInfo, "late note")
	r, _ := f.store.GetRecipient(ctx, f.rid)
	_ = f.store.UpdateRecipient(ctx, r.ID, r.Name, r.PhoneE164, r.JID, false, r.Levels)
	f.clock.t = atClock(8, 0)
	if n, _ := f.svc.ReleaseHeld(ctx); n != 0 {
		t.Errorf("no digest without recipients, got %d", n)
	}
	if got, _ := f.store.GetMessage(ctx, held.ID); got.Status != store.StatusDigested {
		t.Errorf("status = %s", got.Status)
	}
}

func TestPausedInfoReleasedDuringQuietHoursWaitsForTheDigest(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	enableQuietHours(t, f)

	f.clock.t = atClock(12, 0)
	_ = f.svc.Pause(ctx, time.Hour)
	info := f.send(t, store.LevelInfo, "paused info")
	warning := f.send(t, store.LevelWarning, "paused warning")

	f.clock.t = atClock(22, 30) // pause over, quiet hours active
	n, err := f.svc.ReleaseHeld(ctx)
	if err != nil || n != 1 {
		t.Fatalf("released %d err=%v, want only the warning", n, err)
	}
	if got, _ := f.store.GetMessage(ctx, info.ID); got.Status != store.StatusHeldQuietHours {
		t.Errorf("paused info during quiet hours must wait for the digest, got %s", got.Status)
	}
	if got, _ := f.store.GetMessage(ctx, warning.ID); got.Status != store.StatusQueued {
		t.Errorf("warning status = %s", got.Status)
	}
}

func TestRulesSettingsRoundTripAndValidation(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()

	defaults, err := f.svc.RulesSettings(ctx)
	if err != nil || defaults.DedupeMinutes != 10 || defaults.RateLimitPerHour != 20 {
		t.Fatalf("defaults: %+v err=%v", defaults, err)
	}

	f.settings(t, func(s *rules.Settings) {
		s.QuietEnabled = true
		s.QuietStart = 21*60 + 15
		s.DedupeMinutes = 4
		s.HeartbeatIntervalS = 120
	})
	saved, _ := f.svc.RulesSettings(ctx)
	if !saved.QuietEnabled || saved.QuietStart != 21*60+15 || saved.DedupeMinutes != 4 || saved.HeartbeatIntervalS != 120 {
		t.Errorf("saved settings differ: %+v", saved)
	}

	bad := saved
	bad.RateLimitPerHour = -5
	if err := f.svc.SaveRulesSettings(ctx, bad); err == nil {
		t.Error("an invalid value must be rejected")
	}
	if again, _ := f.svc.RulesSettings(ctx); again.RateLimitPerHour != saved.RateLimitPerHour {
		t.Error("a rejected save must not change anything")
	}
}
