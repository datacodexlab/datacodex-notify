// SPDX-License-Identifier: GPL-3.0-only

package voice

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"datacodex-notify/internal/audio"
	"datacodex-notify/internal/tts"
)

func requireFFmpeg(t *testing.T, f *fixture) {
	t.Helper()
	if _, err := audio.FindFFmpeg(""); err != nil {
		t.Skip("ffmpeg is not installed on this machine; skipping the conversion test")
	}
}

func TestPrepareUsesTheCacheAfterTheFirstCall(t *testing.T) {
	f := newFixture(t)
	requireFFmpeg(t, f)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)

	clip, err := f.svc.Prepare(ctx, "hello")
	if err != nil {
		t.Fatalf("Prepare failed: %v", err)
	}
	if !bytes.HasPrefix(clip.Ogg, []byte("OggS")) || clip.DurationSec != 1 || len(clip.Waveform) != audio.WaveformSize {
		t.Fatalf("unexpected clip: %d bytes, %ds", len(clip.Ogg), clip.DurationSec)
	}
	if f.google.speechCalls != 1 {
		t.Fatalf("speech calls = %d", f.google.speechCalls)
	}

	again, err := f.svc.Prepare(ctx, "hello")
	if err != nil || !bytes.Equal(again.Ogg, clip.Ogg) {
		t.Fatalf("cached Prepare failed: %v", err)
	}
	if f.google.speechCalls != 1 {
		t.Errorf("a cache hit must skip Gemini, speech calls = %d", f.google.speechCalls)
	}

	if _, err := f.svc.Prepare(ctx, "different text"); err != nil || f.google.speechCalls != 2 {
		t.Errorf("new text must call Gemini: err=%v calls=%d", err, f.google.speechCalls)
	}
	_ = f.svc.SetVoice(ctx, tts.VoiceCharon)
	if _, err := f.svc.Prepare(ctx, "hello"); err != nil || f.google.speechCalls != 3 {
		t.Errorf("a new voice must call Gemini: err=%v calls=%d", err, f.google.speechCalls)
	}
	if got := f.google.voices; len(got) != 3 || got[2] != tts.VoiceCharon {
		t.Errorf("voices requested: %v", got)
	}
}

func TestPrepareErrors(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	f.svc.findFFmpeg = func(string) (string, error) { return "ffmpeg.exe", nil }
	if _, err := f.svc.Prepare(ctx, "hi"); !errors.Is(err, tts.ErrNoKeys) {
		t.Errorf("got %v, want ErrNoKeys", err)
	}

	_, _ = f.svc.AddKey(ctx, "a", keyA)
	f.google.status = http.StatusTooManyRequests
	if _, err := f.svc.Prepare(ctx, "hi"); !errors.Is(err, tts.ErrQuotaExhausted) {
		t.Errorf("got %v, want ErrQuotaExhausted", err)
	}

	f.svc.findFFmpeg = func(string) (string, error) { return "", audio.ErrFFmpegNotFound }
	callsBefore := f.google.speechCalls
	if _, err := f.svc.Prepare(ctx, "other text"); !errors.Is(err, audio.ErrFFmpegNotFound) {
		t.Errorf("got %v, want ErrFFmpegNotFound", err)
	}
	if f.google.speechCalls != callsBefore {
		t.Error("without ffmpeg Gemini must not be called")
	}
}

func TestStatusNeverContainsKeysAndShowsCooldown(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	f.svc.findFFmpeg = func(string) (string, error) { return "C:/tools/ffmpeg.exe", nil }
	_, _ = f.svc.AddKey(ctx, "a", keyA)
	_, _ = f.svc.AddKey(ctx, "b", keyB)

	status, err := f.svc.Status(ctx)
	if err != nil {
		t.Fatalf("Status failed: %v", err)
	}
	if status.KeyCount != 2 || !status.FFmpegFound || !status.Available || status.NextAvailableAt != nil {
		t.Errorf("unexpected status: %+v", status)
	}

	f.google.status = http.StatusTooManyRequests
	_, _ = f.svc.Sample(ctx, "hi")
	status, _ = f.svc.Status(ctx)
	if status.NextAvailableAt == nil {
		t.Error("when every key is cooling down the next available time must be reported")
	}

	data, _ := json.Marshal(status)
	for _, secret := range []string{keyA, keyB} {
		if strings.Contains(string(data), secret) {
			t.Errorf("status leaks a key: %s", data)
		}
	}
}

func TestStatusReportsUnavailableModels(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)
	f.google.status = http.StatusNotFound

	if _, err := f.svc.Sample(ctx, "hi"); !errors.Is(err, tts.ErrModelUnavailable) {
		t.Fatalf("got %v", err)
	}
	status, _ := f.svc.Status(ctx)
	if len(status.UnavailableModels) == 0 {
		t.Error("unavailable models must be surfaced in the status")
	}

	f.google.status = http.StatusOK
	f.google.models = nil
	_ = f.svc.SaveModels(ctx, "new-primary-tts", "none")
	if status, _ := f.svc.Status(ctx); len(status.UnavailableModels) != 0 {
		t.Error("saving models must clear the unavailable marks")
	}
}

func TestPrepareCachesPerSpeedAndShortensTheNote(t *testing.T) {
	f := newFixture(t)
	requireFFmpeg(t, f)
	ctx := context.Background()
	_, _ = f.svc.AddKey(ctx, "a", keyA)

	if _, err := f.svc.Prepare(ctx, "hello"); err != nil || f.google.speechCalls != 1 {
		t.Fatalf("first: err=%v calls=%d", err, f.google.speechCalls)
	}
	if err := f.svc.SetSpeed(ctx, SpeedNameFast); err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.Prepare(ctx, "hello"); err != nil || f.google.speechCalls != 2 {
		t.Errorf("a new speed must not reuse the normal-speed note: err=%v calls=%d", err, f.google.speechCalls)
	}
	if _, err := f.svc.Prepare(ctx, "hello"); err != nil || f.google.speechCalls != 2 {
		t.Errorf("the same speed hits the cache: err=%v calls=%d", err, f.google.speechCalls)
	}
	_ = f.svc.SetSpeed(ctx, SpeedNameNormal)
	if _, err := f.svc.Prepare(ctx, "hello"); err != nil || f.google.speechCalls != 2 {
		t.Errorf("switching back reuses the normal note: err=%v calls=%d", err, f.google.speechCalls)
	}
}
