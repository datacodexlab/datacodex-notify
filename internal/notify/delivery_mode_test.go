// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"strings"
	"testing"

	"datacodex-notify/internal/store"
)

// introVoice is a fake voice setting that can also switch the spoken intro off.
type introVoice struct {
	fakeVoiceSettings
	noIntro bool
}

func (v *introVoice) SpokenIntro(context.Context) bool { return !v.noIntro }

func newIntroVoice(available bool) *introVoice {
	return &introVoice{fakeVoiceSettings: fakeVoiceSettings{available: available, modes: levelDefaults()}}
}

func TestAgentMessageHeaderNamesTheAgentAndTheProject(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()

	withProject, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Project: "billing-api", Text: "done"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(withProject.Text, string(rune(0x1F7E2))+" *Codex* · billing-api\n") {
		t.Errorf("header = %q", withProject.Text)
	}

	noProject, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "claude-code", Text: "stop"})
	if !strings.HasPrefix(noProject.Text, string(rune(0x1F6A8))+" *Claude Code* · Critical\n") {
		t.Errorf("a message without a project shows only the agent: %q", noProject.Text)
	}

	other, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelWarning, Source: "admin-ui", Text: "test"})
	if !strings.Contains(other.Text, "Warning") || !strings.Contains(other.Text, "admin-ui") {
		t.Errorf("app-generated messages keep the level header: %q", other.Text)
	}

	if _, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Project: strings.Repeat("p", 81), Text: "x"}); err == nil {
		t.Error("an over-long project name must be rejected")
	}
}

func TestSpokenIntroOpensAgentVoiceNotes(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	voice := newIntroVoice(true)
	f.svc.SetVoice(voice)

	msg, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "codex", Project: "billing-api", Text: "The build failed"})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Mode != store.ModeBoth || msg.Speech != "Message from Codex, project billing-api. The build failed" {
		t.Errorf("mode=%s speech=%q", msg.Mode, msg.Speech)
	}
	if strings.Contains(msg.Text, "Message from") {
		t.Errorf("the intro is spoken only, not written: %q", msg.Text)
	}

	noProject, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "gemini", Text: "Check this"})
	if noProject.Speech != "Message from Gemini. Check this" {
		t.Errorf("speech without a project = %q", noProject.Speech)
	}

	explicit, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Mode: store.ModeVoice, Speech: "Short version", Text: "Long text"})
	if explicit.Speech != "Message from Codex. Short version" {
		t.Errorf("an explicit speech text also gets the intro: %q", explicit.Speech)
	}

	appMessage, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "admin-ui", Text: "Test"})
	if appMessage.Speech != "Test" {
		t.Errorf("only agent messages get an intro: %q", appMessage.Speech)
	}

	voice.noIntro = true
	off, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "codex", Project: "billing-api", Text: "No intro now"})
	if off.Speech != "No intro now" {
		t.Errorf("the intro switch must turn it off: %q", off.Speech)
	}

	voice.noIntro = false
	textOnly, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Text: "Plain text"})
	if textOnly.Speech != "" {
		t.Errorf("a text message has no speech: %q", textOnly.Speech)
	}
}

func TestSpokenIntroCountsInsideTheSpeechLimit(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.SetVoice(newIntroVoice(true))

	long := strings.Repeat("a", 1000)
	msg, err := f.svc.ValidateAndEnqueue(context.Background(), &SendRequest{Level: store.LevelCritical, Source: "codex", Project: "billing-api", Text: long})
	if err != nil {
		t.Fatal(err)
	}
	if got := len([]rune(msg.Speech)); got != MaxSpeechRunes {
		t.Errorf("speech has %d characters, want exactly the cap %d", got, MaxSpeechRunes)
	}
	if !strings.HasPrefix(msg.Speech, "Message from Codex, project billing-api. ") {
		t.Errorf("the intro must stay whole: %q", msg.Speech[:60])
	}
}

func TestAppAlertsFollowTheirOwnSwitchAndMode(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	voice := newIntroVoice(true)
	f.svc.SetVoice(voice)

	defaults := f.svc.AlertSettings(ctx)
	if !defaults[TypeHeartbeatLost].Enabled || defaults[TypeHeartbeatLost].Mode != store.ModeBoth {
		t.Errorf("heartbeat lost defaults: %+v", defaults[TypeHeartbeatLost])
	}
	if defaults[TypeAgentConnect].Enabled {
		t.Error("agent connect notices default to off")
	}

	lost := Alert{Type: TypeHeartbeatLost, Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "codex", Text: "stopped"}
	msg, err := f.svc.Alert(ctx, lost)
	if err != nil || msg == nil || msg.Mode != store.ModeBoth {
		t.Fatalf("default mode: %+v err=%v", msg, err)
	}

	if err := f.svc.SaveAlertSetting(ctx, TypeHeartbeatLost, AlertSetting{Enabled: true, Mode: store.ModeVoice}); err != nil {
		t.Fatal(err)
	}
	if msg, _ := f.svc.Alert(ctx, lost); msg.Mode != store.ModeVoice || msg.Speech == "" {
		t.Errorf("voice-only must carry speech: %+v", msg)
	}

	if err := f.svc.SaveAlertSetting(ctx, TypeHeartbeatLost, AlertSetting{Enabled: true, Mode: store.ModeText}); err != nil {
		t.Fatal(err)
	}
	if msg, _ := f.svc.Alert(ctx, lost); msg.Mode != store.ModeText {
		t.Errorf("text mode ignored: %s", msg.Mode)
	}

	if err := f.svc.SaveAlertSetting(ctx, TypeHeartbeatLost, AlertSetting{Enabled: false, Mode: store.ModeBoth}); err != nil {
		t.Fatal(err)
	}
	if msg, err := f.svc.Alert(ctx, lost); err != nil || msg != nil {
		t.Errorf("a switched-off alert queues nothing: %+v err=%v", msg, err)
	}

	back := Alert{Type: TypeHeartbeatBack, Level: store.LevelInfo, Source: "codex", Text: "back"}
	if msg, _ := f.svc.Alert(ctx, back); msg == nil || msg.Mode != store.ModeText {
		t.Errorf("heartbeat back is on by default and text: %+v", msg)
	}
	connect := Alert{Type: TypeAgentConnect, Level: store.LevelInfo, Source: "agents", Text: "connected"}
	if msg, _ := f.svc.Alert(ctx, connect); msg != nil {
		t.Error("agent connect is off by default")
	}
}

func TestVoiceAlertFallsBackToTextWhenVoiceIsUnavailable(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	f.svc.SetVoice(newIntroVoice(false))
	if err := f.svc.SaveAlertSetting(ctx, TypeConnection, AlertSetting{Enabled: true, Mode: store.ModeVoice}); err != nil {
		t.Fatal(err)
	}
	msg, err := f.svc.Alert(ctx, Alert{Type: TypeConnection, Level: store.LevelWarning, Source: "whatsapp", Text: "offline"})
	if err != nil || msg.Mode != store.ModeText || msg.Speech != "" {
		t.Errorf("without a key or ffmpeg a voice-only alert becomes text, never lost: %+v err=%v", msg, err)
	}
}

func TestAlertSettingValidation(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	if err := f.svc.SaveAlertSetting(ctx, "nonsense", AlertSetting{Enabled: true, Mode: store.ModeText}); err == nil {
		t.Error("unknown kind must be rejected")
	}
	if err := f.svc.SaveAlertSetting(ctx, TypeDigest, AlertSetting{Enabled: true, Mode: "auto"}); err == nil {
		t.Error("auto is not a valid alert mode")
	}
}

func TestDigestHonoursItsModeAndSwitch(t *testing.T) {
	ctx := context.Background()

	run := func(t *testing.T, available bool, setting AlertSetting) (*rulesFixture, []*store.Message) {
		f := newRulesFixture(t)
		f.svc.SetVoice(newIntroVoice(available))
		if err := f.svc.SaveAlertSetting(ctx, TypeDigest, setting); err != nil {
			t.Fatal(err)
		}
		enableQuietHours(t, f)
		f.clock.t = atClock(23, 0)
		f.send(t, store.LevelInfo, "first quiet note")
		f.send(t, store.LevelInfo, "second quiet note")
		f.clock.t = atClock(7, 30)
		if _, err := f.svc.ReleaseHeld(ctx); err != nil {
			t.Fatal(err)
		}
		queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
		var digests []*store.Message
		for _, m := range queued {
			if m.Source == "digest" {
				digests = append(digests, m)
			}
		}
		return f, digests
	}

	t.Run("voice digest speaks the count and the first lines within the cap", func(t *testing.T) {
		_, digests := run(t, true, AlertSetting{Enabled: true, Mode: store.ModeVoice})
		if len(digests) != 1 || digests[0].Mode != store.ModeVoice {
			t.Fatalf("digests: %+v", digests)
		}
		speech := digests[0].Speech
		for _, want := range []string{"2 message(s)", "first quiet note", "second quiet note"} {
			if !strings.Contains(speech, want) {
				t.Errorf("speech is missing %q: %q", want, speech)
			}
		}
		if strings.Contains(speech, "—") || len([]rune(speech)) > MaxSpeechRunes {
			t.Errorf("speech must be plain and within the cap: %q", speech)
		}
	})

	t.Run("voice digest becomes text without voice support", func(t *testing.T) {
		_, digests := run(t, false, AlertSetting{Enabled: true, Mode: store.ModeBoth})
		if len(digests) != 1 || digests[0].Mode != store.ModeText || digests[0].Speech != "" {
			t.Fatalf("digests: %+v", digests)
		}
	})

	t.Run("a switched-off digest sends nothing but still closes the held messages", func(t *testing.T) {
		f, digests := run(t, true, AlertSetting{Enabled: false, Mode: store.ModeText})
		if len(digests) != 0 {
			t.Fatalf("no digest expected: %+v", digests)
		}
		if held, _ := f.store.ListMessagesByStatus(ctx, store.StatusHeldQuietHours); len(held) != 0 {
			t.Errorf("held messages must not pile up: %d", len(held))
		}
	})
}
