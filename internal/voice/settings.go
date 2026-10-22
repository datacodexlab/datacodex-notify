// SPDX-License-Identifier: GPL-3.0-only

package voice

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tts"
)

const (
	settingPrimaryModel  = "tts_primary_model"
	settingFallbackModel = "tts_fallback_model"
	settingVoice         = "tts_voice"
	settingFFmpegPath    = "ffmpeg_path"
	settingModePrefix    = "mode_"
	settingSpokenIntro   = "voice_spoken_intro"
	settingSpeed         = "voice_speed"

	// noFallback is stored when the user chooses no fallback model; an empty value means the default.
	noFallback = "none"
)

var (
	// ErrNoModel means a model id was empty.
	ErrNoModel = errors.New("choose a model")
	// ErrSameModel means primary and fallback are the same model.
	ErrSameModel = errors.New("the fallback model must differ from the primary model")
	// ErrInvalidVoice means the voice is not selectable.
	ErrInvalidVoice = errors.New("unsupported voice")
	// ErrInvalidMode means a delivery mode or level is not valid.
	ErrInvalidMode = errors.New("unsupported delivery mode or level")
	// ErrInvalidSpeed means the speaking speed is not selectable.
	ErrInvalidSpeed = errors.New("unsupported speaking speed")
	// ErrFFmpegPath means the configured ffmpeg path is not a file.
	ErrFFmpegPath = errors.New("the ffmpeg path is not a file")
)

// ModelRejectedError reports that Google refused the chosen primary model.
type ModelRejectedError struct {
	Message string
}

func (e *ModelRejectedError) Error() string { return "the model was rejected: " + e.Message }

// ModelPair is the effective primary and fallback model. Fallback is empty when there is none.
type ModelPair struct {
	Primary  string `json:"primary"`
	Fallback string `json:"fallback"`
	Custom   bool   `json:"custom"`
}

var levelDefaults = map[store.Level]store.Mode{
	store.LevelInfo:     store.ModeText,
	store.LevelWarning:  store.ModeBoth,
	store.LevelCritical: store.ModeBoth,
}

func (s *Service) setting(ctx context.Context, key string) string {
	value, err := s.store.GetSetting(ctx, key)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(value)
}

// ModelPair returns the models in effect: saved settings, or the defaults.
func (s *Service) ModelPair(ctx context.Context) ModelPair {
	pair := ModelPair{Primary: tts.DefaultPrimaryModel, Fallback: tts.DefaultFallbackModel}
	if primary := s.setting(ctx, settingPrimaryModel); primary != "" {
		pair.Primary = primary
		pair.Custom = true
	}
	switch fallback := s.setting(ctx, settingFallbackModel); fallback {
	case "":
	case noFallback:
		pair.Fallback = ""
		pair.Custom = true
	default:
		pair.Fallback = fallback
		pair.Custom = true
	}
	return pair
}

// Models implements tts.ModelSource.
func (s *Service) Models(ctx context.Context) (string, string) {
	pair := s.ModelPair(ctx)
	return pair.Primary, pair.Fallback
}

// ListModels asks Google for the TTS models the account can use.
func (s *Service) ListModels(ctx context.Context) ([]tts.ModelInfo, error) {
	return s.tts.ListModels(ctx)
}

// SaveModels checks the primary model with a real minimal generation (success or
// a rate limit accepts it) and only then stores the pair. An empty fallback or
// "none" means no fallback.
func (s *Service) SaveModels(ctx context.Context, primary, fallback string) error {
	primary = strings.TrimSpace(primary)
	fallback = strings.TrimSpace(fallback)
	if primary == "" {
		return ErrNoModel
	}
	if fallback == "" {
		fallback = noFallback
	}
	if fallback != noFallback && fallback == primary {
		return ErrSameModel
	}

	if result := s.tts.TestModel(ctx, primary); !result.Valid {
		return &ModelRejectedError{Message: result.Message}
	}

	if err := s.store.SetSetting(ctx, settingPrimaryModel, primary); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, settingFallbackModel, fallback); err != nil {
		return err
	}
	s.tts.ForgetUnavailableModels()
	return nil
}

// ResetModels returns to the default model pair.
func (s *Service) ResetModels(ctx context.Context) error {
	if err := s.store.SetSetting(ctx, settingPrimaryModel, ""); err != nil {
		return err
	}
	if err := s.store.SetSetting(ctx, settingFallbackModel, ""); err != nil {
		return err
	}
	s.tts.ForgetUnavailableModels()
	return nil
}

// Voice returns the selected voice.
func (s *Service) Voice(ctx context.Context) string {
	return tts.NormalizeVoice(s.setting(ctx, settingVoice))
}

// SetVoice selects Kore or Charon.
func (s *Service) SetVoice(ctx context.Context, voice string) error {
	for _, v := range tts.Voices {
		if v == voice {
			return s.store.SetSetting(ctx, settingVoice, voice)
		}
	}
	return ErrInvalidVoice
}

func validMode(mode store.Mode) bool {
	return mode == store.ModeText || mode == store.ModeVoice || mode == store.ModeBoth
}

// ModeFor returns the delivery mode for a level: the saved setting or the built-in default.
func (s *Service) ModeFor(ctx context.Context, level store.Level) store.Mode {
	if saved := store.Mode(s.setting(ctx, settingModePrefix+string(level))); validMode(saved) {
		return saved
	}
	if mode, ok := levelDefaults[level]; ok {
		return mode
	}
	return store.ModeText
}

// Modes returns the delivery mode of every level.
func (s *Service) Modes(ctx context.Context) map[string]string {
	modes := make(map[string]string, len(levelDefaults))
	for level := range levelDefaults {
		modes[string(level)] = string(s.ModeFor(ctx, level))
	}
	return modes
}

// SetModes saves delivery modes for levels. Every level and mode is validated before anything is saved.
func (s *Service) SetModes(ctx context.Context, modes map[string]string) error {
	for level, mode := range modes {
		if _, ok := levelDefaults[store.Level(level)]; !ok || !validMode(store.Mode(mode)) {
			return fmt.Errorf("%w: %s=%s", ErrInvalidMode, level, mode)
		}
	}
	for level, mode := range modes {
		if err := s.store.SetSetting(ctx, settingModePrefix+level, mode); err != nil {
			return err
		}
	}
	return nil
}

// FFmpegPath returns the path the user configured, which may be empty.
func (s *Service) FFmpegPath(ctx context.Context) string {
	return s.setting(ctx, settingFFmpegPath)
}

// SetFFmpegPath saves a custom ffmpeg path; an empty path returns to auto-detection.
func (s *Service) SetFFmpegPath(ctx context.Context, path string) error {
	path = strings.TrimSpace(path)
	if path != "" {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			return ErrFFmpegPath
		}
	}
	return s.store.SetSetting(ctx, settingFFmpegPath, path)
}

// SpokenIntro reports whether voice notes from agents start with a short spoken
// intro naming the agent and the project (default on).
func (s *Service) SpokenIntro(ctx context.Context) bool {
	return s.setting(ctx, settingSpokenIntro) != "false"
}

// SetSpokenIntro switches the spoken intro on or off.
func (s *Service) SetSpokenIntro(ctx context.Context, enabled bool) error {
	return s.store.SetSetting(ctx, settingSpokenIntro, fmt.Sprintf("%t", enabled))
}

// Speaking speeds the user can pick, by name.
const (
	SpeedNameNormal   = "normal"
	SpeedNameFast     = "fast"
	SpeedNameVeryFast = "very_fast"
)

// SpeedNames lists the selectable speeds, slowest first.
func SpeedNames() []string {
	return []string{SpeedNameNormal, SpeedNameFast, SpeedNameVeryFast}
}

func speedFactor(name string) (float64, bool) {
	switch name {
	case SpeedNameNormal:
		return audio.SpeedNormal, true
	case SpeedNameFast:
		return audio.SpeedFast, true
	case SpeedNameVeryFast:
		return audio.SpeedVeryFast, true
	}
	return 0, false
}

// speedKeySuffix is added to the cache key style so notes at different speeds never mix.
// Normal speed adds nothing, so notes cached before the setting existed stay valid.
func speedKeySuffix(speed float64) string {
	if speed == audio.SpeedNormal {
		return ""
	}
	return fmt.Sprintf("|speed=%g", speed)
}

// SpeedName returns the selected speed name (default normal).
func (s *Service) SpeedName(ctx context.Context) string {
	name := s.setting(ctx, settingSpeed)
	if _, ok := speedFactor(name); ok {
		return name
	}
	return SpeedNameNormal
}

// Speed returns the speed factor applied to voice notes after synthesis.
func (s *Service) Speed(ctx context.Context) float64 {
	factor, _ := speedFactor(s.SpeedName(ctx))
	return factor
}

// SetSpeed selects normal, fast, or very_fast.
func (s *Service) SetSpeed(ctx context.Context, name string) error {
	if _, ok := speedFactor(name); !ok {
		return ErrInvalidSpeed
	}
	return s.store.SetSetting(ctx, settingSpeed, name)
}
