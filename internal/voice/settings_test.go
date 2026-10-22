// SPDX-License-Identifier: GPL-3.0-only

package voice

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/tts"
)

func TestModelSettingsOverrideDefaults(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)

	pair := f.svc.ModelPair(ctx)
	if pair.Primary != tts.DefaultPrimaryModel || pair.Fallback != tts.DefaultFallbackModel || pair.Custom {
		t.Fatalf("defaults expected, got %+v", pair)
	}

	if err := f.svc.SaveModels(ctx, "my-primary-tts", "my-fallback-tts"); err != nil {
		t.Fatalf("SaveModels failed: %v", err)
	}
	if primary, fallback := f.svc.Models(ctx); primary != "my-primary-tts" || fallback != "my-fallback-tts" {
		t.Errorf("Models = %s, %s", primary, fallback)
	}

	if err := f.svc.SaveModels(ctx, "my-primary-tts", "none"); err != nil {
		t.Fatal(err)
	}
	if pair := f.svc.ModelPair(ctx); pair.Fallback != "" || !pair.Custom {
		t.Errorf("none must mean no fallback: %+v", pair)
	}
	if err := f.svc.SaveModels(ctx, "my-primary-tts", ""); err != nil {
		t.Fatal(err)
	}
	if pair := f.svc.ModelPair(ctx); pair.Fallback != "" {
		t.Errorf("an empty fallback from the UI means none: %+v", pair)
	}

	if err := f.svc.ResetModels(ctx); err != nil {
		t.Fatal(err)
	}
	if pair := f.svc.ModelPair(ctx); pair.Primary != tts.DefaultPrimaryModel || pair.Fallback != tts.DefaultFallbackModel || pair.Custom {
		t.Errorf("reset must restore the defaults: %+v", pair)
	}
}

func TestSaveModelsValidation(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)

	if err := f.svc.SaveModels(ctx, "", "x"); !errors.Is(err, ErrNoModel) {
		t.Errorf("got %v", err)
	}
	if err := f.svc.SaveModels(ctx, "same-tts", "same-tts"); !errors.Is(err, ErrSameModel) {
		t.Errorf("got %v", err)
	}

	f.google.status = http.StatusNotFound
	err := f.svc.SaveModels(ctx, "ghost-tts", "x")
	var rejected *ModelRejectedError
	if !errors.As(err, &rejected) || !strings.Contains(rejected.Message, "google says no") {
		t.Fatalf("got %v, want a ModelRejectedError with Google's message", err)
	}
	if pair := f.svc.ModelPair(ctx); pair.Custom {
		t.Errorf("a rejected model must not be saved: %+v", pair)
	}

	f.google.status = http.StatusTooManyRequests
	if err := f.svc.SaveModels(ctx, "busy-tts", "x"); err != nil {
		t.Errorf("a 429 accepts the model: %v", err)
	}

	if err := newFixtureWithoutKeys(t).SaveModels(ctx, "any-tts", "x"); err == nil {
		t.Error("saving without any key cannot be validated and must fail")
	}
}

func newFixtureWithoutKeys(t *testing.T) *Service {
	return newFixture(t).svc
}

func TestListModelsThroughTheService(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)
	f.google.models = []string{"gemini-pro", "alpha-tts"}

	models, err := f.svc.ListModels(ctx)
	if err != nil || len(models) != 1 || models[0].ID != "alpha-tts" {
		t.Fatalf("got %+v err=%v", models, err)
	}
}

func TestVoiceAndModeSettings(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.svc.Voice(ctx) != tts.VoiceKore {
		t.Errorf("default voice = %s", f.svc.Voice(ctx))
	}
	if err := f.svc.SetVoice(ctx, tts.VoiceCharon); err != nil || f.svc.Voice(ctx) != tts.VoiceCharon {
		t.Errorf("SetVoice failed: %v", err)
	}
	if err := f.svc.SetVoice(ctx, "Nobody"); !errors.Is(err, ErrInvalidVoice) {
		t.Errorf("got %v", err)
	}

	wantDefaults := map[string]string{"info": "text", "warning": "both", "critical": "both"}
	for level, want := range wantDefaults {
		if got := f.svc.Modes(ctx)[level]; got != want {
			t.Errorf("default mode for %s = %s, want %s", level, got, want)
		}
	}
	if err := f.svc.SetModes(ctx, map[string]string{"info": "voice", "critical": "text"}); err != nil {
		t.Fatalf("SetModes failed: %v", err)
	}
	modes := f.svc.Modes(ctx)
	if modes["info"] != "voice" || modes["critical"] != "text" || modes["warning"] != "both" {
		t.Errorf("modes = %v", modes)
	}
	if got := f.svc.ModeFor(ctx, store.LevelInfo); got != store.ModeVoice {
		t.Errorf("ModeFor(info) = %s", got)
	}

	for _, bad := range []map[string]string{{"info": "loud"}, {"urgent": "text"}, {"info": "auto"}} {
		if err := f.svc.SetModes(ctx, bad); !errors.Is(err, ErrInvalidMode) {
			t.Errorf("%v: got %v", bad, err)
		}
	}
	if f.svc.Modes(ctx)["info"] != "voice" {
		t.Error("an invalid request must not change anything")
	}
}

func TestFFmpegPathSetting(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	exe := filepath.Join(t.TempDir(), "ffmpeg.exe")
	_ = os.WriteFile(exe, []byte("x"), 0o755)

	if err := f.svc.SetFFmpegPath(ctx, exe); err != nil || f.svc.FFmpegPath(ctx) != exe {
		t.Fatalf("SetFFmpegPath failed: %v", err)
	}
	if err := f.svc.SetFFmpegPath(ctx, filepath.Join(t.TempDir(), "missing.exe")); !errors.Is(err, ErrFFmpegPath) {
		t.Errorf("got %v", err)
	}
	if err := f.svc.SetFFmpegPath(ctx, t.TempDir()); !errors.Is(err, ErrFFmpegPath) {
		t.Errorf("a folder is not a file: %v", err)
	}
	if err := f.svc.SetFFmpegPath(ctx, ""); err != nil || f.svc.FFmpegPath(ctx) != "" {
		t.Errorf("an empty path returns to auto-detection: %v", err)
	}
}

func TestAvailability(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	found := func(string) (string, error) { return "ffmpeg.exe", nil }
	missing := func(string) (string, error) { return "", audio.ErrFFmpegNotFound }

	f.svc.findFFmpeg = found
	if f.svc.Available(ctx) {
		t.Error("no key: voice must be unavailable")
	}
	_, _ = f.svc.AddKey(ctx, "a", keyA)
	if !f.svc.Available(ctx) {
		t.Error("key and ffmpeg: voice must be available")
	}
	f.svc.findFFmpeg = missing
	if f.svc.Available(ctx) {
		t.Error("no ffmpeg: voice must be unavailable")
	}
}

func TestSpeedSettingDefaultsValidatesAndKeysTheCache(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if f.svc.SpeedName(ctx) != SpeedNameNormal || f.svc.Speed(ctx) != audio.SpeedNormal {
		t.Fatalf("the default speed is normal: %s %g", f.svc.SpeedName(ctx), f.svc.Speed(ctx))
	}
	for name, want := range map[string]float64{SpeedNameFast: 1.25, SpeedNameVeryFast: 1.5, SpeedNameNormal: 1.0} {
		if err := f.svc.SetSpeed(ctx, name); err != nil {
			t.Fatalf("SetSpeed(%s): %v", name, err)
		}
		if f.svc.Speed(ctx) != want || f.svc.SpeedName(ctx) != name {
			t.Errorf("%s: speed = %g (%s)", name, f.svc.Speed(ctx), f.svc.SpeedName(ctx))
		}
	}
	_ = f.svc.SetSpeed(ctx, SpeedNameNormal)
	for _, bad := range []string{"", "turbo", "1.25"} {
		if err := f.svc.SetSpeed(ctx, bad); !errors.Is(err, ErrInvalidSpeed) {
			t.Errorf("SetSpeed(%q) = %v, want ErrInvalidSpeed", bad, err)
		}
	}
	if st, _ := f.svc.Status(ctx); st.Speed != SpeedNameNormal || len(st.Speeds) != 3 {
		t.Errorf("status must report the speed and the choices: %+v", st)
	}

	keys := map[string]bool{}
	for _, speed := range []float64{audio.SpeedNormal, audio.SpeedFast, audio.SpeedVeryFast} {
		keys[audio.CacheKey("same text", "Kore", "model", tts.StyleEgyptian+speedKeySuffix(speed))] = true
	}
	if len(keys) != 3 {
		t.Errorf("each speed needs its own cache key, got %d distinct", len(keys))
	}
	if speedKeySuffix(audio.SpeedNormal) != "" {
		t.Error("normal speed keeps the old cache keys valid")
	}
}
