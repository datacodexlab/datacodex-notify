// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"datacodex-notify/internal/rules"
	"datacodex-notify/internal/store"
)

const (
	maxDigestRunes = 3500
	digestSource   = "digest"
)

// RulesSettings loads the rule settings from the store.
func (s *Service) RulesSettings(ctx context.Context) (rules.Settings, error) {
	values, err := s.store.GetAllSettings(ctx)
	if err != nil {
		return rules.Settings{}, fmt.Errorf("failed to load the rule settings: %w", err)
	}
	return rules.FromMap(values, s.loc), nil
}

// SaveRulesSettings validates and stores the rule settings (the pause is separate).
func (s *Service) SaveRulesSettings(ctx context.Context, settings rules.Settings) error {
	if err := rules.Validate(settings); err != nil {
		return err
	}
	for key, value := range rules.ToMap(settings) {
		if err := s.store.SetSetting(ctx, key, value); err != nil {
			return err
		}
	}
	return nil
}

// PauseState describes the current pause.
type PauseState struct {
	Paused     bool
	Indefinite bool
	Until      time.Time
}

// Pause holds non-critical messages for d, or until Resume when d is zero or negative.
func (s *Service) Pause(ctx context.Context, d time.Duration) error {
	value := rules.PausedIndefinite
	if d > 0 {
		value = s.now().Add(d).UTC().Format(time.RFC3339)
	}
	return s.store.SetSetting(ctx, rules.KeyPausedUntil, value)
}

// Resume ends a pause. Held messages are released by the next ReleaseHeld pass.
func (s *Service) Resume(ctx context.Context) error {
	return s.store.SetSetting(ctx, rules.KeyPausedUntil, "")
}

// PauseState reports whether notifications are paused now.
func (s *Service) PauseState(ctx context.Context) (PauseState, error) {
	settings, err := s.RulesSettings(ctx)
	if err != nil {
		return PauseState{}, err
	}
	return PauseState{
		Paused:     settings.Paused(s.now()),
		Indefinite: settings.PausedIndefinitely,
		Until:      settings.PausedUntil,
	}, nil
}

// ReleaseHeld lets held messages go once their reason has passed: messages held
// by a pause are queued one by one (or moved to quiet hours if those are active),
// and messages held by quiet hours leave as one digest. It returns how many
// messages or digests were queued so the caller can wake the dispatcher.
func (s *Service) ReleaseHeld(ctx context.Context) (int, error) {
	settings, err := s.RulesSettings(ctx)
	if err != nil {
		return 0, err
	}
	now := s.now()
	if settings.Paused(now) {
		return 0, nil
	}
	quiet := settings.InQuietHours(now)
	released := 0

	paused, err := s.store.ListMessagesByStatus(ctx, store.StatusHeldPaused)
	if err != nil {
		return released, err
	}
	for _, msg := range paused {
		next := store.StatusQueued
		if msg.Level == store.LevelInfo && quiet {
			next = store.StatusHeldQuietHours
		}
		if err := s.store.UpdateMessageStatus(ctx, msg.ID, next, ""); err != nil {
			return released, err
		}
		if next == store.StatusQueued {
			released++
		}
	}

	if quiet {
		return released, nil
	}
	digested, err := s.releaseDigest(ctx, now)
	if err != nil {
		return released, err
	}
	return released + digested, nil
}

// releaseDigest combines the messages held by quiet hours into one info message.
func (s *Service) releaseDigest(ctx context.Context, now time.Time) (int, error) {
	held, err := s.store.ListMessagesByStatus(ctx, store.StatusHeldQuietHours)
	if err != nil || len(held) == 0 {
		return 0, err
	}

	recipients, err := s.digestRecipients(ctx, held)
	if err != nil {
		return 0, err
	}
	queued := 0
	setting := s.AlertSettingFor(ctx, TypeDigest)
	if len(recipients) > 0 && setting.Enabled {
		body := s.digestBody(held)
		formatted := s.render(layout{LabelKey: string(store.LevelInfo), Source: digestSource, Text: body}, now)
		key := digestSource + ":" + strconv.FormatInt(now.Unix(), 10)
		mode, speech := s.digestMode(ctx, setting.Mode), ""
		if mode != store.ModeText {
			speech = s.digestSpeech(held)
		}
		if _, err := s.store.CreateMessage(ctx, store.LevelInfo, digestSource, formatted, speech, mode, key, store.StatusQueued, recipients); err != nil {
			return 0, fmt.Errorf("failed to create the digest: %w", err)
		}
		queued = 1
	}

	for _, msg := range held {
		if err := s.store.UpdateMessageStatus(ctx, msg.ID, store.StatusDigested, ""); err != nil {
			return queued, err
		}
	}
	return queued, nil
}

// digestRecipients returns the enabled recipients of the held messages, each once.
func (s *Service) digestRecipients(ctx context.Context, held []*store.Message) ([]string, error) {
	seen := make(map[string]bool)
	var ids []string
	for _, msg := range held {
		deliveries, err := s.store.GetDeliveriesForMessage(ctx, msg.ID)
		if err != nil {
			return nil, err
		}
		for _, d := range deliveries {
			if seen[d.RecipientID] {
				continue
			}
			seen[d.RecipientID] = true
			recipient, err := s.store.GetRecipient(ctx, d.RecipientID)
			if err != nil {
				return nil, err
			}
			if recipient != nil && recipient.Enabled {
				ids = append(ids, d.RecipientID)
			}
		}
	}
	return ids, nil
}

// digestBody lists the held messages, cut at maxDigestRunes with a note on how many were left out.
func (s *Service) digestBody(held []*store.Message) string {
	var b strings.Builder
	b.WriteString(s.locale.GetMessage("quiet_digest", map[string]string{"count": strconv.Itoa(len(held))}))

	size := len([]rune(b.String()))
	for i, msg := range held {
		header, middle := splitTemplate(msg.Text)
		entry := "\n\n" + header + "\n" + middle
		size += len([]rune(entry))
		if size > maxDigestRunes {
			b.WriteString("\n\n" + s.locale.GetMessage("quiet_digest_more", map[string]string{"count": strconv.Itoa(len(held) - i)}))
			break
		}
		b.WriteString(entry)
	}
	return b.String()
}

// digestMode applies the digest's delivery mode, falling back to text when voice notes cannot be produced.
func (s *Service) digestMode(ctx context.Context, mode store.Mode) store.Mode {
	if mode != store.ModeText && (s.voice == nil || !s.voice.Available(ctx)) {
		return store.ModeText
	}
	return mode
}

// digestSpeech is the short spoken summary of a digest: the count and the first
// lines of the held messages, within MaxSpeechRunes.
func (s *Service) digestSpeech(held []*store.Message) string {
	var b strings.Builder
	b.WriteString(s.locale.GetMessage("quiet_digest", map[string]string{"count": strconv.Itoa(len(held))}))
	b.WriteString(".")
	for _, msg := range held {
		line := messageBodyLine(msg.Text)
		if line == "" {
			continue
		}
		if len([]rune(b.String()))+len([]rune(line))+2 > MaxSpeechRunes {
			break
		}
		b.WriteString(" " + line)
		if !strings.HasSuffix(line, ".") {
			b.WriteString(".")
		}
	}
	return truncateRunes(b.String(), MaxSpeechRunes)
}

// messageBodyLine returns the first line of a stored message's title or body, as plain speakable text.
func messageBodyLine(text string) string {
	_, middle := splitTemplate(text)
	for _, line := range strings.Split(middle, "\n") {
		line = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), titleEmoji))
		line = strings.TrimSpace(speechStripper.Replace(line))
		if line != "" {
			return line
		}
	}
	return ""
}
