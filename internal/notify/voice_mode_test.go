// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/store"
)

type fakeVoiceSettings struct {
	available bool
	modes     map[store.Level]store.Mode
}

func (f *fakeVoiceSettings) Available(context.Context) bool { return f.available }

func (f *fakeVoiceSettings) ModeFor(_ context.Context, level store.Level) store.Mode {
	return f.modes[level]
}

func newModeFixture(t *testing.T, voice VoiceSettings) (*Service, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "modes.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err := s.AddRecipient(context.Background(), "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}
	svc := NewService(s, locale.NewManager("en"))
	if voice != nil {
		svc.SetVoice(voice)
	}
	return svc, s
}

func levelDefaults() map[store.Level]store.Mode {
	return map[store.Level]store.Mode{
		store.LevelInfo:     store.ModeText,
		store.LevelWarning:  store.ModeBoth,
		store.LevelCritical: store.ModeBoth,
	}
}

func TestModeResolution(t *testing.T) {
	tests := []struct {
		name      string
		voice     VoiceSettings
		level     store.Level
		mode      store.Mode
		wantMode  store.Mode
		wantSpeak bool
	}{
		{"no voice support: auto is text", nil, store.LevelCritical, store.ModeAuto, store.ModeText, false},
		{"no voice support: explicit voice becomes text", nil, store.LevelInfo, store.ModeVoice, store.ModeText, false},
		{"info uses the level default", &fakeVoiceSettings{true, levelDefaults()}, store.LevelInfo, store.ModeAuto, store.ModeText, false},
		{"warning uses the level default", &fakeVoiceSettings{true, levelDefaults()}, store.LevelWarning, "", store.ModeBoth, true},
		{"critical uses the level default", &fakeVoiceSettings{true, levelDefaults()}, store.LevelCritical, store.ModeAuto, store.ModeBoth, true},
		{"explicit text wins over a voice default", &fakeVoiceSettings{true, levelDefaults()}, store.LevelCritical, store.ModeText, store.ModeText, false},
		{"explicit voice wins over a text default", &fakeVoiceSettings{true, levelDefaults()}, store.LevelInfo, store.ModeVoice, store.ModeVoice, true},
		{"explicit both", &fakeVoiceSettings{true, levelDefaults()}, store.LevelInfo, store.ModeBoth, store.ModeBoth, true},
		{"missing key or ffmpeg: default becomes text", &fakeVoiceSettings{false, levelDefaults()}, store.LevelCritical, store.ModeAuto, store.ModeText, false},
		{"missing key or ffmpeg: explicit both becomes text", &fakeVoiceSettings{false, levelDefaults()}, store.LevelInfo, store.ModeBoth, store.ModeText, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			svc, _ := newModeFixture(t, tc.voice)
			msg, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: tc.level, Text: "hello world", Mode: tc.mode})
			if err != nil {
				t.Fatalf("ValidateAndEnqueue failed: %v", err)
			}
			if msg.Mode != tc.wantMode {
				t.Errorf("mode = %s, want %s", msg.Mode, tc.wantMode)
			}
			if (msg.Speech != "") != tc.wantSpeak {
				t.Errorf("speech = %q, want speech present = %v", msg.Speech, tc.wantSpeak)
			}
		})
	}
}

func TestSpeechDefaultsToTheRawTextCappedAt600Runes(t *testing.T) {
	svc, _ := newModeFixture(t, &fakeVoiceSettings{true, levelDefaults()})

	short, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelWarning, Text: "short text", Source: "agent"})
	if err != nil {
		t.Fatal(err)
	}
	if short.Speech != "short text" {
		t.Errorf("speech = %q, want the raw text without the header or footer", short.Speech)
	}

	long, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelWarning, Text: strings.Repeat("x", 1000)})
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(long.Speech)); got != MaxSpeechRunes {
		t.Errorf("speech length = %d, want %d", got, MaxSpeechRunes)
	}
	if len([]rune(long.Text)) < 1000 {
		t.Error("the written message must not be truncated")
	}

	explicit, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelWarning, Text: "long written text", Speech: "short spoken"})
	if err != nil {
		t.Fatal(err)
	}
	if explicit.Speech != "short spoken" {
		t.Errorf("an explicit speech must be kept, got %q", explicit.Speech)
	}
}

func TestTextMessagesCarryNoSpeech(t *testing.T) {
	svc, _ := newModeFixture(t, &fakeVoiceSettings{true, levelDefaults()})
	msg, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelInfo, Text: "plain"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Mode != store.ModeText || msg.Speech != "" {
		t.Errorf("unexpected message: mode=%s speech=%q", msg.Mode, msg.Speech)
	}
}

func TestInvalidModeIsRejected(t *testing.T) {
	svc, _ := newModeFixture(t, nil)
	_, err := svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelInfo, Text: "x", Mode: "shout"})
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Errorf("got %v", err)
	}
}

func TestVoiceModesCreateTheMatchingDeliveries(t *testing.T) {
	svc, s := newModeFixture(t, &fakeVoiceSettings{true, levelDefaults()})
	ctx := context.Background()

	for mode, want := range map[store.Mode]string{store.ModeText: "text", store.ModeVoice: "voice", store.ModeBoth: "text,voice"} {
		msg, err := svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Text: "x " + string(mode), Mode: mode})
		if err != nil {
			t.Fatal(err)
		}
		deliveries, _ := s.GetDeliveriesForMessage(ctx, msg.ID)
		var got []string
		for _, d := range deliveries {
			got = append(got, d.Kind)
		}
		if strings.Join(got, ",") != want {
			t.Errorf("mode %s: deliveries = %v, want %s", mode, got, want)
		}
	}
}
