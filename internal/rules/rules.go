// SPDX-License-Identifier: GPL-3.0-only

// Package rules decides what happens to a new message: send it, drop it as a
// duplicate, hold it for quiet hours or a pause, or limit it by rate. It is
// pure logic: the caller passes in the settings, the time, and the facts it
// looked up, so nothing here reads a clock or touches storage.
package rules

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Level names match the message levels used elsewhere.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Decision is the outcome of Decide.
type Decision string

const (
	Allow          Decision = "allow"
	Deduplicated   Decision = "deduplicated"
	HeldPaused     Decision = "held_paused"
	HeldQuietHours Decision = "held_quiet_hours"
)

// Defaults used when a setting is missing or invalid.
const (
	DefaultQuietStart         = 22 * 60
	DefaultQuietEnd           = 7 * 60
	DefaultDedupeMinutes      = 10
	DefaultRateLimitPerHour   = 20
	DefaultHeartbeatInterval  = 600
	DefaultHeartbeatGraceSecs = 300

	maxDedupeMinutes   = 24 * 60
	maxRateLimit       = 1000
	maxHeartbeatSecond = 24 * 60 * 60
)

// Settings holds every rule parameter. Quiet hours are minutes after midnight in Location.
type Settings struct {
	QuietEnabled     bool
	QuietStart       int
	QuietEnd         int
	DedupeMinutes    int // 0 turns duplicate detection off
	RateLimitPerHour int // 0 turns the limit off

	PausedIndefinitely bool
	PausedUntil        time.Time // zero when not paused for a limited time

	HeartbeatIntervalS int
	HeartbeatGraceS    int

	Location *time.Location
}

// Paused reports whether non-critical notifications are paused at now.
func (s Settings) Paused(now time.Time) bool {
	return s.PausedIndefinitely || now.Before(s.PausedUntil)
}

// InQuietHours reports whether now falls inside the quiet window, which may wrap past midnight.
func (s Settings) InQuietHours(now time.Time) bool {
	if !s.QuietEnabled || s.QuietStart == s.QuietEnd {
		return false
	}
	loc := s.Location
	if loc == nil {
		loc = time.Local
	}
	local := now.In(loc)
	minute := local.Hour()*60 + local.Minute()
	if s.QuietStart < s.QuietEnd {
		return minute >= s.QuietStart && minute < s.QuietEnd
	}
	return minute >= s.QuietStart || minute < s.QuietEnd
}

// DedupeSince returns the start of the duplicate window, or false when detection is off.
func (s Settings) DedupeSince(now time.Time) (time.Time, bool) {
	if s.DedupeMinutes <= 0 {
		return time.Time{}, false
	}
	return now.Add(-time.Duration(s.DedupeMinutes) * time.Minute), true
}

// Decide applies the rules in order: duplicates are dropped at every level,
// then a pause holds non-critical messages, then quiet hours hold info messages.
// Warnings and critical messages always pass quiet hours; critical always passes a pause.
func Decide(s Settings, level string, now time.Time, duplicate bool) Decision {
	switch {
	case duplicate:
		return Deduplicated
	case level != LevelCritical && s.Paused(now):
		return HeldPaused
	case level == LevelInfo && s.InQuietHours(now):
		return HeldQuietHours
	default:
		return Allow
	}
}

// RateExceeded reports whether a recipient has already received the hourly
// limit of non-critical messages. Critical messages bypass the limit.
func RateExceeded(s Settings, level string, recent int) bool {
	if level == LevelCritical || s.RateLimitPerHour <= 0 {
		return false
	}
	return recent >= s.RateLimitPerHour
}

// Setting keys in the settings store.
const (
	KeyQuietEnabled   = "quiet_enabled"
	KeyQuietStart     = "quiet_start"
	KeyQuietEnd       = "quiet_end"
	KeyDedupeMinutes  = "dedupe_minutes"
	KeyRateLimit      = "rate_limit_per_hour"
	KeyPausedUntil    = "paused_until"
	KeyHeartbeatEvery = "heartbeat_interval_s"
	KeyHeartbeatGrace = "heartbeat_grace_s"

	// PausedIndefinite is the stored value for a pause that lasts until resumed.
	PausedIndefinite = "indefinite"
)

// Defaults returns the settings used before anything is saved.
func Defaults(loc *time.Location) Settings {
	return Settings{
		QuietStart:         DefaultQuietStart,
		QuietEnd:           DefaultQuietEnd,
		DedupeMinutes:      DefaultDedupeMinutes,
		RateLimitPerHour:   DefaultRateLimitPerHour,
		HeartbeatIntervalS: DefaultHeartbeatInterval,
		HeartbeatGraceS:    DefaultHeartbeatGraceSecs,
		Location:           loc,
	}
}

// FromMap reads settings from stored values; anything missing or invalid keeps its default.
func FromMap(values map[string]string, loc *time.Location) Settings {
	s := Defaults(loc)
	s.QuietEnabled = values[KeyQuietEnabled] == "true"
	if m, err := ParseClock(values[KeyQuietStart]); err == nil {
		s.QuietStart = m
	}
	if m, err := ParseClock(values[KeyQuietEnd]); err == nil {
		s.QuietEnd = m
	}
	if n, ok := parseInt(values[KeyDedupeMinutes], 0, maxDedupeMinutes); ok {
		s.DedupeMinutes = n
	}
	if n, ok := parseInt(values[KeyRateLimit], 0, maxRateLimit); ok {
		s.RateLimitPerHour = n
	}
	if n, ok := parseInt(values[KeyHeartbeatEvery], 1, maxHeartbeatSecond); ok {
		s.HeartbeatIntervalS = n
	}
	if n, ok := parseInt(values[KeyHeartbeatGrace], 0, maxHeartbeatSecond); ok {
		s.HeartbeatGraceS = n
	}

	switch paused := values[KeyPausedUntil]; paused {
	case "":
	case PausedIndefinite:
		s.PausedIndefinitely = true
	default:
		if until, err := time.Parse(time.RFC3339, paused); err == nil {
			s.PausedUntil = until
		}
	}
	return s
}

// ToMap returns the values to store for everything except the pause.
func ToMap(s Settings) map[string]string {
	return map[string]string{
		KeyQuietEnabled:   strconv.FormatBool(s.QuietEnabled),
		KeyQuietStart:     FormatClock(s.QuietStart),
		KeyQuietEnd:       FormatClock(s.QuietEnd),
		KeyDedupeMinutes:  strconv.Itoa(s.DedupeMinutes),
		KeyRateLimit:      strconv.Itoa(s.RateLimitPerHour),
		KeyHeartbeatEvery: strconv.Itoa(s.HeartbeatIntervalS),
		KeyHeartbeatGrace: strconv.Itoa(s.HeartbeatGraceS),
	}
}

// Validate checks values the user typed. It returns the first problem found.
func Validate(s Settings) error {
	switch {
	case s.QuietStart < 0 || s.QuietStart >= 24*60 || s.QuietEnd < 0 || s.QuietEnd >= 24*60:
		return fmt.Errorf("quiet hours must be between 00:00 and 23:59")
	case s.DedupeMinutes < 0 || s.DedupeMinutes > maxDedupeMinutes:
		return fmt.Errorf("the duplicate window must be between 0 and %d minutes", maxDedupeMinutes)
	case s.RateLimitPerHour < 0 || s.RateLimitPerHour > maxRateLimit:
		return fmt.Errorf("the hourly limit must be between 0 and %d", maxRateLimit)
	case s.HeartbeatIntervalS < 1 || s.HeartbeatIntervalS > maxHeartbeatSecond:
		return fmt.Errorf("the heartbeat interval must be between 1 and %d seconds", maxHeartbeatSecond)
	case s.HeartbeatGraceS < 0 || s.HeartbeatGraceS > maxHeartbeatSecond:
		return fmt.Errorf("the heartbeat grace must be between 0 and %d seconds", maxHeartbeatSecond)
	}
	return nil
}

// ParseClock reads "HH:MM" into minutes after midnight.
func ParseClock(value string) (int, error) {
	hours, minutes, ok := strings.Cut(strings.TrimSpace(value), ":")
	if !ok {
		return 0, fmt.Errorf("invalid time %q", value)
	}
	h, errH := strconv.Atoi(hours)
	m, errM := strconv.Atoi(minutes)
	if errH != nil || errM != nil || len(minutes) != 2 || h < 0 || h > 23 || m < 0 || m > 59 {
		return 0, fmt.Errorf("invalid time %q", value)
	}
	return h*60 + m, nil
}

// FormatClock writes minutes after midnight as "HH:MM".
func FormatClock(minutes int) string {
	return fmt.Sprintf("%02d:%02d", minutes/60, minutes%60)
}

func parseInt(value string, min, max int) (int, bool) {
	n, err := strconv.Atoi(strings.TrimSpace(value))
	if err != nil || n < min || n > max {
		return 0, false
	}
	return n, true
}
