// SPDX-License-Identifier: GPL-3.0-only

package rules

import (
	"testing"
	"time"
)

var cairo = time.FixedZone("test", 2*60*60)

// at builds a time on 2026-10-21 at hh:mm in the test zone.
func at(hh, mm int) time.Time {
	return time.Date(2026, 10, 21, hh, mm, 0, 0, cairo)
}

func quiet(start, end string) Settings {
	s := Defaults(cairo)
	s.QuietEnabled = true
	s.QuietStart, _ = ParseClock(start)
	s.QuietEnd, _ = ParseClock(end)
	return s
}

func TestInQuietHours(t *testing.T) {
	tests := []struct {
		name  string
		s     Settings
		now   time.Time
		quiet bool
	}{
		{"disabled", Defaults(cairo), at(23, 0), false},
		{"wrapping window: late evening", quiet("22:00", "07:00"), at(23, 30), true},
		{"wrapping window: just after midnight", quiet("22:00", "07:00"), at(0, 15), true},
		{"wrapping window: start is inside", quiet("22:00", "07:00"), at(22, 0), true},
		{"wrapping window: end is outside", quiet("22:00", "07:00"), at(7, 0), false},
		{"wrapping window: daytime", quiet("22:00", "07:00"), at(12, 0), false},
		{"same-day window inside", quiet("13:00", "15:00"), at(14, 0), true},
		{"same-day window before", quiet("13:00", "15:00"), at(12, 59), false},
		{"same-day window end is outside", quiet("13:00", "15:00"), at(15, 0), false},
		{"empty window is never quiet", quiet("08:00", "08:00"), at(8, 0), false},
		{"time zone conversion", quiet("22:00", "07:00"), time.Date(2026, 10, 21, 21, 30, 0, 0, time.UTC), true}, // 23:30 in the test zone
	}
	for _, tc := range tests {
		if got := tc.s.InQuietHours(tc.now); got != tc.quiet {
			t.Errorf("%s: got %v, want %v", tc.name, got, tc.quiet)
		}
	}
}

func TestPaused(t *testing.T) {
	now := at(12, 0)
	s := Defaults(cairo)
	if s.Paused(now) {
		t.Error("not paused by default")
	}
	s.PausedUntil = now.Add(time.Minute)
	if !s.Paused(now) || s.Paused(now.Add(time.Minute)) || s.Paused(now.Add(time.Hour)) {
		t.Error("a timed pause must end exactly at PausedUntil")
	}
	s = Defaults(cairo)
	s.PausedIndefinitely = true
	if !s.Paused(now.Add(1000 * time.Hour)) {
		t.Error("an indefinite pause never ends by itself")
	}
}

func TestDecide(t *testing.T) {
	night := at(23, 0)
	day := at(12, 0)

	quietSettings := quiet("22:00", "07:00")
	paused := Defaults(cairo)
	paused.PausedIndefinitely = true
	both := quiet("22:00", "07:00")
	both.PausedIndefinitely = true

	tests := []struct {
		name      string
		s         Settings
		level     string
		now       time.Time
		duplicate bool
		want      Decision
	}{
		{"plain info passes", Defaults(cairo), LevelInfo, day, false, Allow},
		{"info in quiet hours is held", quietSettings, LevelInfo, night, false, HeldQuietHours},
		{"warning passes quiet hours", quietSettings, LevelWarning, night, false, Allow},
		{"critical passes quiet hours", quietSettings, LevelCritical, night, false, Allow},
		{"info outside quiet hours passes", quietSettings, LevelInfo, day, false, Allow},
		{"pause holds info", paused, LevelInfo, day, false, HeldPaused},
		{"pause holds warning", paused, LevelWarning, day, false, HeldPaused},
		{"pause never holds critical", paused, LevelCritical, day, false, Allow},
		{"pause wins over quiet hours for info", both, LevelInfo, night, false, HeldPaused},
		{"duplicate info", Defaults(cairo), LevelInfo, day, true, Deduplicated},
		{"duplicate critical is also dropped", Defaults(cairo), LevelCritical, day, true, Deduplicated},
		{"duplicate wins over pause", paused, LevelInfo, day, true, Deduplicated},
	}
	for _, tc := range tests {
		if got := Decide(tc.s, tc.level, tc.now, tc.duplicate); got != tc.want {
			t.Errorf("%s: got %s, want %s", tc.name, got, tc.want)
		}
	}
}

func TestRateExceeded(t *testing.T) {
	s := Defaults(cairo) // 20 per hour
	tests := []struct {
		level  string
		recent int
		want   bool
	}{
		{LevelInfo, 19, false},
		{LevelInfo, 20, true},
		{LevelWarning, 25, true},
		{LevelCritical, 500, false},
	}
	for _, tc := range tests {
		if got := RateExceeded(s, tc.level, tc.recent); got != tc.want {
			t.Errorf("%s with %d recent: got %v, want %v", tc.level, tc.recent, got, tc.want)
		}
	}
	s.RateLimitPerHour = 0
	if RateExceeded(s, LevelInfo, 10000) {
		t.Error("a limit of 0 turns the check off")
	}
}

func TestDedupeSince(t *testing.T) {
	now := at(12, 0)
	s := Defaults(cairo)
	since, ok := s.DedupeSince(now)
	if !ok || !since.Equal(now.Add(-10*time.Minute)) {
		t.Errorf("got %v %v", since, ok)
	}
	s.DedupeMinutes = 0
	if _, ok := s.DedupeSince(now); ok {
		t.Error("0 minutes turns detection off")
	}
}

func TestFromMapAndToMapRoundTrip(t *testing.T) {
	s := FromMap(nil, cairo)
	if s.QuietEnabled || s.QuietStart != 22*60 || s.QuietEnd != 7*60 || s.DedupeMinutes != 10 || s.RateLimitPerHour != 20 ||
		s.HeartbeatIntervalS != 600 || s.HeartbeatGraceS != 300 {
		t.Errorf("defaults wrong: %+v", s)
	}

	saved := ToMap(Settings{
		QuietEnabled: true, QuietStart: 23*60 + 30, QuietEnd: 6*60 + 5,
		DedupeMinutes: 3, RateLimitPerHour: 5, HeartbeatIntervalS: 90, HeartbeatGraceS: 0,
	})
	loaded := FromMap(saved, cairo)
	if !loaded.QuietEnabled || loaded.QuietStart != 23*60+30 || loaded.QuietEnd != 6*60+5 ||
		loaded.DedupeMinutes != 3 || loaded.RateLimitPerHour != 5 || loaded.HeartbeatIntervalS != 90 || loaded.HeartbeatGraceS != 0 {
		t.Errorf("round trip changed values: %+v", loaded)
	}
}

func TestFromMapIgnoresInvalidValues(t *testing.T) {
	s := FromMap(map[string]string{
		KeyQuietStart:     "25:00",
		KeyQuietEnd:       "nope",
		KeyDedupeMinutes:  "-1",
		KeyRateLimit:      "x",
		KeyHeartbeatEvery: "0",
		KeyHeartbeatGrace: "999999999",
		KeyPausedUntil:    "tomorrow",
	}, cairo)
	want := Defaults(cairo)
	if s.QuietStart != want.QuietStart || s.QuietEnd != want.QuietEnd || s.DedupeMinutes != want.DedupeMinutes ||
		s.RateLimitPerHour != want.RateLimitPerHour || s.HeartbeatIntervalS != want.HeartbeatIntervalS ||
		s.HeartbeatGraceS != want.HeartbeatGraceS || s.Paused(at(12, 0)) {
		t.Errorf("invalid values must fall back to the defaults: %+v", s)
	}
}

func TestPauseValues(t *testing.T) {
	until := at(13, 0)
	s := FromMap(map[string]string{KeyPausedUntil: until.Format(time.RFC3339)}, cairo)
	if !s.Paused(at(12, 0)) || s.Paused(at(13, 0)) {
		t.Errorf("timed pause not applied: %+v", s)
	}
	s = FromMap(map[string]string{KeyPausedUntil: PausedIndefinite}, cairo)
	if !s.Paused(at(12, 0)) || !s.PausedIndefinitely {
		t.Errorf("indefinite pause not applied: %+v", s)
	}
}

func TestValidate(t *testing.T) {
	good := Defaults(cairo)
	if err := Validate(good); err != nil {
		t.Fatalf("defaults must be valid: %v", err)
	}
	mutations := map[string]func(*Settings){
		"quiet start too late": func(s *Settings) { s.QuietStart = 24 * 60 },
		"quiet end negative":   func(s *Settings) { s.QuietEnd = -1 },
		"dedupe negative":      func(s *Settings) { s.DedupeMinutes = -1 },
		"dedupe too long":      func(s *Settings) { s.DedupeMinutes = 24*60 + 1 },
		"limit negative":       func(s *Settings) { s.RateLimitPerHour = -1 },
		"limit too high":       func(s *Settings) { s.RateLimitPerHour = 1001 },
		"interval zero":        func(s *Settings) { s.HeartbeatIntervalS = 0 },
		"interval too long":    func(s *Settings) { s.HeartbeatIntervalS = 24*60*60 + 1 },
		"grace negative":       func(s *Settings) { s.HeartbeatGraceS = -1 },
		"grace too long":       func(s *Settings) { s.HeartbeatGraceS = 24*60*60 + 1 },
	}
	for name, mutate := range mutations {
		s := Defaults(cairo)
		mutate(&s)
		if err := Validate(s); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

func TestParseClock(t *testing.T) {
	for value, want := range map[string]int{"00:00": 0, "07:05": 425, "23:59": 1439, " 12:30 ": 750} {
		if got, err := ParseClock(value); err != nil || got != want {
			t.Errorf("%q: got %d err=%v, want %d", value, got, err, want)
		}
	}
	for _, bad := range []string{"", "7", "24:00", "12:60", "12:5", "ab:cd", "12-30"} {
		if _, err := ParseClock(bad); err == nil {
			t.Errorf("%q should be rejected", bad)
		}
	}
	if FormatClock(425) != "07:05" {
		t.Errorf("FormatClock = %s", FormatClock(425))
	}
}
