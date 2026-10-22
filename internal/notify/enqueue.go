// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"time"

	"datacodex-notify/internal/rules"
	"datacodex-notify/internal/store"
)

type enqueueOptions struct {
	// skipDedupe is set for system alerts, which must never be swallowed as duplicates.
	skipDedupe bool
	// labelKey overrides the marker and level name; empty means the level.
	labelKey string
}

func dedupeKey(req *SendRequest) string {
	if req.DedupeKey != "" {
		return req.DedupeKey
	}
	sum := sha256.Sum256([]byte(strings.Join([]string{string(req.Level), req.Source, req.Text}, "\x00")))
	return hex.EncodeToString(sum[:])
}

// enqueue applies the rules to a validated request and stores the message.
// Duplicates and rate-limited messages are stored without deliveries so the
// Log still shows them; held messages keep their deliveries until released.
func (s *Service) enqueue(ctx context.Context, req *SendRequest, mode store.Mode, targetIDs []string, opts enqueueOptions) (*store.Message, error) {
	settings, err := s.RulesSettings(ctx)
	if err != nil {
		return nil, err
	}
	now := s.now()

	labelKey := opts.labelKey
	if labelKey == "" {
		labelKey = string(req.Level)
	}
	formatted := s.render(layout{
		LabelKey: labelKey,
		Source:   req.Source,
		Project:  req.Project,
		Title:    req.Title,
		Text:     req.Text,
		Fields:   req.Fields,
		Agent:    isAgentSource(req.Source),
	}, now)
	key := dedupeKey(req)

	create := func(status store.MessageStatus, ids []string) (*store.Message, error) {
		msg, err := s.store.CreateMessage(ctx, req.Level, req.Source, formatted, req.Speech, mode, key, status, ids)
		if err != nil {
			return nil, fmt.Errorf("failed to enqueue message: %w", err)
		}
		return msg, nil
	}

	duplicate := false
	if since, ok := settings.DedupeSince(now); ok && !opts.skipDedupe {
		found, err := s.store.FindDuplicateMessage(ctx, key, since)
		if err != nil {
			return nil, err
		}
		duplicate = found != nil
	}

	decision := rules.Decide(settings, string(req.Level), now, duplicate)
	if decision == rules.Deduplicated {
		return create(store.StatusDeduplicated, nil)
	}

	kept, err := s.withinRateLimit(ctx, settings, string(req.Level), targetIDs, now)
	if err != nil {
		return nil, err
	}
	if len(kept) == 0 {
		return create(store.StatusRateLimited, nil)
	}

	status := store.StatusQueued
	switch decision {
	case rules.HeldPaused:
		status = store.StatusHeldPaused
	case rules.HeldQuietHours:
		status = store.StatusHeldQuietHours
	}
	return create(status, kept)
}

// withinRateLimit drops the recipients that already reached the hourly limit.
func (s *Service) withinRateLimit(ctx context.Context, settings rules.Settings, level string, ids []string, now time.Time) ([]string, error) {
	if level == rules.LevelCritical || settings.RateLimitPerHour <= 0 {
		return ids, nil
	}
	kept := make([]string, 0, len(ids))
	for _, id := range ids {
		recent, err := s.store.GetRecentMessageCount(ctx, id, now.Add(-time.Hour))
		if err != nil {
			return nil, err
		}
		if !rules.RateExceeded(settings, level, recent) {
			kept = append(kept, id)
		}
	}
	return kept, nil
}
