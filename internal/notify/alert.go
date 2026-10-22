// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"fmt"

	"datacodex-notify/internal/store"
)

// AlertKind selects a special marker for system alerts; an empty kind uses the level marker.
type AlertKind string

const (
	AlertNone          AlertKind = ""
	AlertHeartbeatLost AlertKind = "heartbeat_lost"
)

// Alert is a message the app itself raises (a missed heartbeat, a long disconnect).
type Alert struct {
	// Type picks the on/off switch and delivery mode from the settings; empty means none.
	Type   AlertType
	Kind   AlertKind
	Level  store.Level
	Source string
	// Project is the project the alert is about, when known; it is shown in an agent's header.
	Project string
	Text    string
}

// Alert queues a system message to every enabled recipient that receives its
// level. Alerts are never swallowed as duplicates, but pause, quiet hours, voice
// settings, and the rate limit apply like any other message of that level.
// When the alert's kind is switched off in the settings nothing is queued and
// the result is (nil, nil). The kind's delivery mode replaces the level default.
func (s *Service) Alert(ctx context.Context, a Alert) (*store.Message, error) {
	if !validLevel(a.Level) {
		return nil, fmt.Errorf("invalid alert level %q", a.Level)
	}
	req := &SendRequest{Level: a.Level, Text: a.Text, Source: a.Source, Project: a.Project}
	if a.Type != "" {
		setting := s.AlertSettingFor(ctx, a.Type)
		if !setting.Enabled {
			return nil, nil
		}
		req.Mode = setting.Mode
	}

	mode := s.resolveMode(ctx, req)
	if mode != store.ModeText {
		req.Speech = truncateRunes(req.Text, MaxSpeechRunes)
	}
	targetIDs, err := s.targets(ctx, a.Level, nil)
	if err != nil {
		return nil, err
	}
	return s.enqueue(ctx, req, mode, targetIDs, enqueueOptions{skipDedupe: true, labelKey: string(a.Kind)})
}
