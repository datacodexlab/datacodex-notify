// SPDX-License-Identifier: GPL-3.0-only

// Package agentprompt builds the copy-paste prompts of the "Connect agent" tab.
// The prompts are English text that a user gives to any agent: the agent then
// writes a marked block into its own rules file. The package is pure: it reads
// no files, settings, or secrets, and no prompt ever contains the API token.
package agentprompt

import (
	_ "embed"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
	"unicode"
)

// Agent is the kind of agent the prompt is written for.
type Agent string

const (
	AgentClaudeCode Agent = "claude-code"
	AgentCodex      Agent = "codex"
	AgentGemini     Agent = "gemini"
	AgentOther      Agent = "other"
)

// Scope says where the rules file lives.
type Scope string

const (
	ScopeGlobal  Scope = "global"
	ScopeProject Scope = "project"
)

// Template picks how many events the agent reports.
type Template string

const (
	TemplateCalm     Template = "calm"
	TemplateNormal   Template = "normal"
	TemplateDetailed Template = "detailed"
)

// Delivery modes an event can request.
const (
	ModeText  = "text"
	ModeVoice = "voice"
	ModeBoth  = "both"
)

// Language is the language of the notification text.
type Language string

const (
	LanguageEgyptian Language = "egyptian"
	LanguageArabic   Language = "arabic"
	LanguageEnglish  Language = "english"
)

// Activation says when the agent turns notifications on.
type Activation string

const (
	// ActivationOnRequest is the default: only when the user asks; one short suggestion allowed.
	ActivationOnRequest Activation = "on_request"
	// ActivationSuggestLong suggests it at the start of every long task.
	ActivationSuggestLong Activation = "suggest_long"
	// ActivationAlways turns notifications on at the start of every manager session.
	ActivationAlways Activation = "always"
)

// Event is something an agent may report.
type Event string

const (
	EventTaskStart          Event = "task_start"
	EventTaskEnd            Event = "task_end"
	EventFailure            Event = "failure"
	EventBlocked            Event = "blocked"
	EventCriticalStop       Event = "critical_stop"
	EventPhaseFinished      Event = "phase_finished"
	EventImplementerStopped Event = "implementer_stopped"
	EventStepMilestone      Event = "step_milestone"
)

// Level names match the notification levels.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

const (
	defaultHeartbeatEvery = 600
	maxPhraseLen          = 100
	maxPhrases            = 6
	maxExtraLen           = 2000
	maxOtherFileLen       = 100
)

// Options is everything the form collects.
type Options struct {
	Agent     Agent    `json:"agent"`
	OtherFile string   `json:"other_file"`
	Scope     Scope    `json:"scope"`
	Template  Template `json:"template"`

	// Levels overrides the level of single events (event name to level).
	Levels map[string]string `json:"levels"`

	// Modes overrides the delivery mode of single events (event name to text, voice, or both).
	Modes map[string]string `json:"modes"`

	Heartbeat      bool `json:"heartbeat"`
	HeartbeatEvery int  `json:"heartbeat_every_s"`

	Language   Language   `json:"language"`
	Activation Activation `json:"activation"`

	ActivatePhrases   []string `json:"activate_phrases"`
	DeactivatePhrases []string `json:"deactivate_phrases"`

	Extra string `json:"extra"`
}

// Env holds facts about this installation that the prompts need.
type Env struct {
	// CLIPath is the full path of the console CLI. The prompts always use it, never PATH.
	CLIPath string
}

//go:embed defaults.json
var defaultsJSON []byte

type phraseDefaults struct {
	Activate   []string `json:"activate_phrases"`
	Deactivate []string `json:"deactivate_phrases"`
}

// Defaults returns the options the form starts with. The default activation and
// deactivation phrases come from the embedded data file, not from code.
func Defaults() Options {
	var phrases phraseDefaults
	if err := json.Unmarshal(defaultsJSON, &phrases); err != nil {
		panic("agentprompt: damaged defaults.json: " + err.Error())
	}
	return Options{
		Agent:             AgentClaudeCode,
		Scope:             ScopeGlobal,
		Template:          TemplateNormal,
		Levels:            map[string]string{},
		Modes:             map[string]string{},
		Heartbeat:         true,
		HeartbeatEvery:    defaultHeartbeatEvery,
		Language:          LanguageEgyptian,
		Activation:        ActivationOnRequest,
		ActivatePhrases:   phrases.Activate,
		DeactivatePhrases: phrases.Deactivate,
	}
}

var defaultLevels = map[Event]string{
	EventTaskStart:          LevelInfo,
	EventTaskEnd:            LevelInfo,
	EventFailure:            LevelWarning,
	EventBlocked:            LevelWarning,
	EventCriticalStop:       LevelCritical,
	EventPhaseFinished:      LevelInfo,
	EventImplementerStopped: LevelWarning,
	EventStepMilestone:      LevelInfo,
}

var templateEvents = map[Template][]Event{
	TemplateCalm:     {EventTaskStart, EventTaskEnd, EventFailure, EventBlocked, EventCriticalStop},
	TemplateNormal:   {EventTaskStart, EventTaskEnd, EventFailure, EventBlocked, EventCriticalStop, EventPhaseFinished, EventImplementerStopped},
	TemplateDetailed: {EventTaskStart, EventTaskEnd, EventFailure, EventBlocked, EventCriticalStop, EventPhaseFinished, EventImplementerStopped, EventStepMilestone},
}

// EventInfo describes one event of a template.
type EventInfo struct {
	Event        Event  `json:"event"`
	DefaultLevel string `json:"default_level"`
}

// Templates lists the events of every template, in order, with their default levels.
func Templates() map[Template][]EventInfo {
	out := make(map[Template][]EventInfo, len(templateEvents))
	for template, events := range templateEvents {
		infos := make([]EventInfo, len(events))
		for i, e := range events {
			infos[i] = EventInfo{Event: e, DefaultLevel: defaultLevels[e]}
		}
		out[template] = infos
	}
	return out
}

// levelOf returns the level for an event: the override, or the default.
func (o Options) levelOf(e Event) string {
	if level, ok := o.Levels[string(e)]; ok {
		return level
	}
	return defaultLevels[e]
}

// defaultMode is the delivery mode a template asks for at a level. Calm and
// normal speak only critical events; detailed also speaks warnings.
func defaultMode(template Template, level string) string {
	switch {
	case level == LevelCritical:
		return ModeBoth
	case level == LevelWarning && template == TemplateDetailed:
		return ModeBoth
	}
	return ModeText
}

// ModeDefaults lists, for every template, the default mode of each level.
func ModeDefaults() map[Template]map[string]string {
	out := make(map[Template]map[string]string, len(templateEvents))
	for template := range templateEvents {
		out[template] = map[string]string{}
		for _, level := range []string{LevelInfo, LevelWarning, LevelCritical} {
			out[template][level] = defaultMode(template, level)
		}
	}
	return out
}

// modeOf returns the --mode value the agent should pass for an event: the override, or the template default.
func (o Options) modeOf(e Event) string {
	if mode, ok := o.Modes[string(e)]; ok {
		return mode
	}
	return defaultMode(o.Template, o.levelOf(e))
}

var otherFilePattern = regexp.MustCompile(`^[A-Za-z0-9._ -]+$`)

// Validate checks the options and returns the first problem found.
func (o Options) Validate() error {
	switch o.Agent {
	case AgentClaudeCode, AgentCodex, AgentGemini:
	case AgentOther:
		name := strings.TrimSpace(o.OtherFile)
		if name == "" || len(name) > maxOtherFileLen || !otherFilePattern.MatchString(name) || strings.Contains(name, "..") {
			return fmt.Errorf("the rules file name must be a plain file name such as RULES.md")
		}
	default:
		return fmt.Errorf("unknown agent %q", o.Agent)
	}

	if o.Scope != ScopeGlobal && o.Scope != ScopeProject {
		return fmt.Errorf("unknown scope %q", o.Scope)
	}
	if _, ok := templateEvents[o.Template]; !ok {
		return fmt.Errorf("unknown template %q", o.Template)
	}
	for event, level := range o.Levels {
		if _, ok := defaultLevels[Event(event)]; !ok {
			return fmt.Errorf("unknown event %q", event)
		}
		if level != LevelInfo && level != LevelWarning && level != LevelCritical {
			return fmt.Errorf("unknown level %q for the event %s", level, event)
		}
	}

	for event, mode := range o.Modes {
		if _, ok := defaultLevels[Event(event)]; !ok {
			return fmt.Errorf("unknown event %q", event)
		}
		if mode != ModeText && mode != ModeVoice && mode != ModeBoth {
			return fmt.Errorf("unknown mode %q for the event %s", mode, event)
		}
	}
	switch o.Language {
	case LanguageEgyptian, LanguageArabic, LanguageEnglish:
	default:
		return fmt.Errorf("unknown language %q", o.Language)
	}
	switch o.Activation {
	case ActivationOnRequest, ActivationSuggestLong, ActivationAlways:
	default:
		return fmt.Errorf("unknown activation mode %q", o.Activation)
	}

	if o.Heartbeat && (o.HeartbeatEvery < 30 || o.HeartbeatEvery > 86400) {
		return fmt.Errorf("the heartbeat interval must be between 30 and 86400 seconds")
	}
	if err := validatePhrases("activation", o.ActivatePhrases); err != nil {
		return err
	}
	if err := validatePhrases("deactivation", o.DeactivatePhrases); err != nil {
		return err
	}
	if len(o.Extra) > maxExtraLen {
		return fmt.Errorf("the extra instructions are limited to %d characters", maxExtraLen)
	}
	return nil
}

func validatePhrases(kind string, phrases []string) error {
	if len(phrases) == 0 || len(phrases) > maxPhrases {
		return fmt.Errorf("give between 1 and %d %s phrases", maxPhrases, kind)
	}
	for _, phrase := range phrases {
		if strings.TrimSpace(phrase) == "" || len([]rune(phrase)) > maxPhraseLen {
			return fmt.Errorf("every %s phrase must be 1 to %d characters", kind, maxPhraseLen)
		}
		if strings.IndexFunc(phrase, unicode.IsControl) >= 0 || strings.ContainsRune(phrase, '"') {
			return fmt.Errorf("a %s phrase must not contain quotes or line breaks", kind)
		}
	}
	return nil
}
