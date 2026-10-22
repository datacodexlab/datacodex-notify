// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"fmt"
	"strings"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/store"
)

type SendRequest struct {
	Level     store.Level `json:"level"`
	Text      string      `json:"text"`
	Speech    string      `json:"speech,omitempty"`
	Mode      store.Mode  `json:"mode,omitempty"`
	To        []string    `json:"to,omitempty"` // recipient names or phone numbers; empty means all enabled
	Source    string      `json:"source,omitempty"`
	Project   string      `json:"project,omitempty"`
	Title     string      `json:"title,omitempty"`
	Fields    []Field     `json:"fields,omitempty"`
	DedupeKey string      `json:"dedupe_key,omitempty"`
}

// VoiceSettings tells the service whether voice notes can be produced and which
// mode applies to a level when the sender does not choose one.
type VoiceSettings interface {
	Available(ctx context.Context) bool
	ModeFor(ctx context.Context, level store.Level) store.Mode
}

// MaxSpeechRunes is the longest text that is spoken; longer text is cut.
const MaxSpeechRunes = 600

const maxProjectRunes = 80

// introSettings is optionally implemented by VoiceSettings to switch the spoken
// intro off. Without it the intro is on.
type introSettings interface {
	SpokenIntro(ctx context.Context) bool
}

// isAgentSource reports whether source names an agent (not an app-generated sender).
func isAgentSource(source string) bool {
	switch source {
	case "claude-code", "codex", "gemini", "other":
		return true
	}
	return false
}

// agentLabel is the agent's display name from the locale files.
func (s *Service) agentLabel(source string) string {
	return s.locale.GetMessage("agent_name_"+source, nil)
}

// spokenIntro returns the short sentence that opens a voice note from an agent,
// or "" when the intro is off or the message does not come from an agent.
func (s *Service) spokenIntro(ctx context.Context, source, project string) string {
	if !isAgentSource(source) {
		return ""
	}
	if v, ok := s.voice.(introSettings); ok && !v.SpokenIntro(ctx) {
		return ""
	}
	key := "voice_intro"
	if project == "" {
		key = "voice_intro_no_project"
	}
	return s.locale.GetMessage(key, map[string]string{"agent": s.agentLabel(source), "project": project})
}

// withIntro puts the intro in front of the speech; the whole text stays within MaxSpeechRunes.
func withIntro(intro, speech string) string {
	if intro == "" {
		return speech
	}
	room := MaxSpeechRunes - len([]rune(intro)) - 1
	if room <= 0 {
		return truncateRunes(intro, MaxSpeechRunes)
	}
	return intro + " " + truncateRunes(speech, room)
}

type Service struct {
	store  *store.Store
	locale *locale.Manager
	voice  VoiceSettings
	now    func() time.Time
	loc    *time.Location
}

func NewService(s *store.Store, loc *locale.Manager) *Service {
	if loc == nil {
		loc = locale.GetManager()
	}
	return &Service{
		store:  s,
		locale: loc,
		now:    time.Now,
		loc:    time.Local,
	}
}

// SetVoice enables voice delivery. Without it every message is sent as text.
func (s *Service) SetVoice(v VoiceSettings) {
	s.voice = v
}

// resolveMode applies the sender's explicit mode, then the per-level setting, and
// falls back to text whenever voice notes cannot be produced.
func (s *Service) resolveMode(ctx context.Context, req *SendRequest) store.Mode {
	mode := req.Mode
	if mode == "" || mode == store.ModeAuto {
		mode = store.ModeText
		if s.voice != nil {
			mode = s.voice.ModeFor(ctx, req.Level)
		}
	}
	if mode != store.ModeText && (s.voice == nil || !s.voice.Available(ctx)) {
		return store.ModeText
	}
	return mode
}

func truncateRunes(text string, limit int) string {
	runes := []rune(text)
	if len(runes) <= limit {
		return text
	}
	return string(runes[:limit])
}

func validLevel(level store.Level) bool {
	switch level {
	case store.LevelInfo, store.LevelWarning, store.LevelCritical:
		return true
	}
	return false
}

func (s *Service) ValidateAndEnqueue(ctx context.Context, req *SendRequest) (*store.Message, error) {
	if !validLevel(req.Level) {
		return nil, fmt.Errorf("invalid level '%s': must be info, warning, or critical", req.Level)
	}

	req.Text = strings.TrimSpace(req.Text)
	if req.Text == "" {
		return nil, fmt.Errorf("message text cannot be empty")
	}
	if len([]rune(req.Text)) > 4000 {
		return nil, fmt.Errorf("message text exceeds 4,000 characters limit")
	}

	if req.Speech != "" {
		req.Speech = strings.TrimSpace(req.Speech)
		if len([]rune(req.Speech)) > MaxSpeechRunes {
			return nil, fmt.Errorf("speech text exceeds %d characters limit", MaxSpeechRunes)
		}
	}

	switch req.Mode {
	case "", store.ModeAuto, store.ModeText, store.ModeVoice, store.ModeBoth:
	default:
		return nil, fmt.Errorf("invalid mode '%s': must be auto, text, voice, or both", req.Mode)
	}
	title, fields, err := validateParts(req.Title, req.Fields)
	if err != nil {
		return nil, err
	}
	req.Title, req.Fields = title, fields
	req.Project = strings.TrimSpace(req.Project)
	if len([]rune(req.Project)) > maxProjectRunes {
		return nil, fmt.Errorf("project name exceeds %d characters limit", maxProjectRunes)
	}
	mode := s.resolveMode(ctx, req)
	if mode != store.ModeText {
		if req.Speech == "" {
			req.Speech = defaultSpeech(req.Title, req.Text, req.Fields)
		}
		req.Speech = withIntro(s.spokenIntro(ctx, req.Source, req.Project), req.Speech)
	}

	targetIDs, err := s.targets(ctx, req.Level, req.To)
	if err != nil {
		return nil, err
	}
	return s.enqueue(ctx, req, mode, targetIDs, enqueueOptions{})
}

// targets resolves the recipients of a message: the named ones, or every
// enabled recipient that receives the level.
func (s *Service) targets(ctx context.Context, level store.Level, to []string) ([]string, error) {
	allEnabled, err := s.store.ListEnabledRecipients(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch recipients: %w", err)
	}
	if len(allEnabled) == 0 {
		return nil, fmt.Errorf("no enabled recipients configured")
	}

	var ids []string
	if len(to) == 0 {
		// Level bitmask: info=1, warning=2, critical=4.
		mask := 1
		if level == store.LevelWarning {
			mask = 2
		} else if level == store.LevelCritical {
			mask = 4
		}
		for _, r := range allEnabled {
			if (r.Levels & mask) != 0 {
				ids = append(ids, r.ID)
			}
		}
	} else {
		wanted := make(map[string]bool)
		for _, name := range to {
			wanted[strings.ToLower(strings.TrimSpace(name))] = true
		}
		for _, r := range allEnabled {
			if wanted[strings.ToLower(r.Name)] || wanted[strings.ToLower(r.PhoneE164)] || wanted[r.ID] {
				ids = append(ids, r.ID)
			}
		}
	}

	if len(ids) == 0 {
		return nil, fmt.Errorf("no recipients matched the criteria or enabled levels")
	}
	return ids, nil
}
