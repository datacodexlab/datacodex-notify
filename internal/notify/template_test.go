// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/store"
)

func TestTemplateLayoutForEveryLevel(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	at := time.Date(2026, 10, 21, 9, 5, 0, 0, time.UTC)
	sep := "━━━━━━━━━━━━━━"

	tests := []struct {
		name   string
		layout layout
		want   string
	}{
		{"agent info with project", layout{LabelKey: "info", Source: "codex", Project: "billing-api", Text: "All green", Agent: true},
			"🟢 *Codex* · billing-api\n" + sep + "\nAll green\n" + sep + "\n🕐 09:05 · 21/10"},
		{"agent warning adds the level word", layout{LabelKey: "warning", Source: "claude-code", Project: "app", Text: "Tests failed", Agent: true},
			"🟠 *Claude Code* · app · Warning\n" + sep + "\nTests failed\n" + sep + "\n🕐 09:05 · 21/10"},
		{"agent critical without project", layout{LabelKey: "critical", Source: "gemini", Text: "Stop", Agent: true},
			"🚨 *Gemini* · Critical\n" + sep + "\nStop\n" + sep + "\n🕐 09:05 · 21/10"},
		{"app message names the level and the source", layout{LabelKey: "warning", Source: "whatsapp", Text: "Offline"},
			"🟠 *Warning* · whatsapp\n" + sep + "\nOffline\n" + sep + "\n🕐 09:05 · 21/10"},
		{"app message without a source", layout{LabelKey: "info", Text: "Hello"},
			"🟢 *Info*\n" + sep + "\nHello\n" + sep + "\n🕐 09:05 · 21/10"},
		{"heartbeat marker", layout{LabelKey: "heartbeat_lost", Source: "codex", Text: "Silent"},
			"💀 *Heartbeat Lost* · codex\n" + sep + "\nSilent\n" + sep + "\n🕐 09:05 · 21/10"},
		{"title and fields", layout{LabelKey: "info", Source: "codex", Project: "app", Title: "Phase 3 done", Text: "Merged the branch.", Fields: []Field{{"Phase", "3 of 5"}, {"Tests", "42 passed"}}, Agent: true},
			"🟢 *Codex* · app\n" + sep + "\n📌 *Phase 3 done*\n\nMerged the branch.\n\n▫️ *Phase:* 3 of 5\n▫️ *Tests:* 42 passed\n" + sep + "\n🕐 09:05 · 21/10"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := f.svc.render(tc.layout, at); got != tc.want {
				t.Errorf("got:\n%s\nwant:\n%s", got, tc.want)
			}
		})
	}
}

func TestTemplateNeutralizesFormattingInHeaderTitleAndFieldsButNotTheBody(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	got := f.svc.render(layout{
		LabelKey: "info", Source: "codex", Project: "my_proj*x", Agent: true,
		Title:  "*bold* _it_ ~st~ `code`\nsecond line",
		Text:   "*keep* the _body_ format",
		Fields: []Field{{"ke*y_", "va~l`ue"}},
	}, time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC))

	for _, line := range strings.Split(got, "\n") {
		if strings.HasPrefix(line, "📌") || strings.HasPrefix(line, "▫️") || strings.HasPrefix(line, "🟢") {
			inner := strings.TrimSuffix(strings.TrimPrefix(strings.SplitN(line, "*", 2)[1], ""), "")
			// Only the label's own two asterisks may remain: the opening one removed above plus the closing one.
			if strings.Count(line, "*") != 2 {
				t.Errorf("a sender's text must not add formatting marks: %q (%s)", line, inner)
			}
			if strings.ContainsAny(line, "_~`") {
				t.Errorf("underscore, tilde, and backtick must be neutralized: %q", line)
			}
		}
	}
	if !strings.Contains(got, "*keep* the _body_ format") {
		t.Errorf("the body keeps the sender's formatting:\n%s", got)
	}
	if strings.Contains(got, "bold* _it_\nsecond") || strings.Contains(got, "\nsecond line*") {
		t.Errorf("a title is one line:\n%s", got)
	}
}

func TestTemplateKeepsArabicIntact(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	f.svc.locale = locale.NewManager("ar")
	arabic := string([]rune{0x0645, 0x0631, 0x062d, 0x0628, 0x0627})
	got := f.svc.render(layout{LabelKey: "info", Source: "codex", Project: arabic, Title: arabic, Text: arabic + " " + arabic, Fields: []Field{{arabic, arabic}}, Agent: true},
		time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC))
	if strings.Count(got, arabic) != 6 {
		t.Errorf("Arabic text must survive unchanged (found %d of 6):\n%s", strings.Count(got, arabic), got)
	}
	if !strings.Contains(got, "*"+f.svc.agentLabel("codex")+"*") {
		t.Errorf("the agent name comes from the Arabic locale:\n%s", got)
	}
}

func TestTimeFooterUsesLocalTime(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.FixedZone("plus3", 3*3600)
	got := f.svc.render(layout{LabelKey: "info", Text: "x"}, time.Date(2026, 3, 31, 22, 30, 0, 0, time.UTC))
	if !strings.HasSuffix(got, "🕐 01:30 · 01/04") {
		t.Errorf("the footer must show local time and date: %q", got)
	}
}

func TestTitleAndFieldsAreValidatedAndStored(t *testing.T) {
	f := newRulesFixture(t)
	ctx := context.Background()
	long := func(n int) string { return strings.Repeat("x", n) }

	ok, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Project: "app", Text: "body", Title: "Done", Fields: []Field{{"Phase", "3"}}})
	if err != nil || !strings.Contains(ok.Text, "📌 *Done*") || !strings.Contains(ok.Text, "▫️ *Phase:* 3") {
		t.Fatalf("a valid request: %v\n%v", err, ok)
	}

	nineFields := make([]Field, 9)
	for i := range nineFields {
		nineFields[i] = Field{"k", "v"}
	}
	bad := map[string]*SendRequest{
		"title too long":  {Level: store.LevelInfo, Text: "b", Title: long(121)},
		"too many fields": {Level: store.LevelInfo, Text: "b", Fields: nineFields},
		"key too long":    {Level: store.LevelInfo, Text: "b", Fields: []Field{{long(41), "v"}}},
		"value too long":  {Level: store.LevelInfo, Text: "b", Fields: []Field{{"k", long(201)}}},
		"empty key":       {Level: store.LevelInfo, Text: "b", Fields: []Field{{" ", "v"}}},
		"empty value":     {Level: store.LevelInfo, Text: "b", Fields: []Field{{"k", ""}}},
	}
	for name, req := range bad {
		if _, err := f.svc.ValidateAndEnqueue(ctx, req); err == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
	edge := &SendRequest{Level: store.LevelInfo, Text: "b", Title: long(120), Fields: []Field{{long(40), long(200)}}}
	if _, err := f.svc.ValidateAndEnqueue(ctx, edge); err != nil {
		t.Errorf("the limits themselves are allowed: %v", err)
	}
}

func TestVoiceSpeaksTitleBodyAndFittingFieldsButNotLayout(t *testing.T) {
	f := newRulesFixture(t)
	voice := newIntroVoice(true)
	f.svc.SetVoice(voice)
	ctx := context.Background()

	msg, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{
		Level: store.LevelCritical, Source: "codex", Project: "billing-api",
		Title: "Build *failed*", Text: "Two tests broke.", Fields: []Field{{"Phase", "3 of 5"}, {"Tests", "2 failed"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "Message from Codex, project billing-api. Build failed. Two tests broke. Phase: 3 of 5. Tests: 2 failed."
	if msg.Speech != want {
		t.Errorf("speech = %q\nwant     %q", msg.Speech, want)
	}
	for _, banned := range []string{"━", "🕐", "📌", "▫", "*"} {
		if strings.Contains(msg.Speech, banned) {
			t.Errorf("speech must not contain %q: %q", banned, msg.Speech)
		}
	}

	long, _ := f.svc.ValidateAndEnqueue(ctx, &SendRequest{
		Level: store.LevelCritical, Source: "codex", Text: strings.Repeat("a", 590), Fields: []Field{{"Phase", "3 of 5"}},
	})
	if strings.Contains(long.Speech, "Phase") || len([]rune(long.Speech)) > MaxSpeechRunes {
		t.Errorf("fields that do not fit in the cap are not spoken: %d characters", len([]rune(long.Speech)))
	}
}

func TestDigestUsesTheTemplateAndSummarizesHeldMessages(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	ctx := context.Background()
	enableQuietHours(t, f)
	f.clock.t = atClock(23, 0)
	if _, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Project: "app", Title: "Step one", Text: "first detail"}); err != nil {
		t.Fatal(err)
	}
	f.send(t, store.LevelInfo, "second quiet note")
	f.clock.t = atClock(7, 30)
	if n, err := f.svc.ReleaseHeld(ctx); err != nil || n != 1 {
		t.Fatalf("released %d err=%v", n, err)
	}
	queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
	var digest *store.Message
	for _, m := range queued {
		if m.Source == "digest" {
			digest = m
		}
	}
	if digest == nil {
		t.Fatal("no digest")
	}
	if !strings.HasPrefix(digest.Text, "🟢 *Info* · digest\n━━━━━━━━━━━━━━\n") || !strings.Contains(digest.Text, "━━━━━━━━━━━━━━\n🕐 ") {
		t.Errorf("the digest uses the template:\n%s", digest.Text)
	}
	for _, want := range []string{"2 message(s)", "*Codex* · app", "Step one", "first detail", "second quiet note"} {
		if !strings.Contains(digest.Text, want) {
			t.Errorf("digest is missing %q:\n%s", want, digest.Text)
		}
	}
}

func TestAgentAlertsUseTheAgentHeaderWithProject(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	ctx := context.Background()

	lost, err := f.svc.Alert(ctx, Alert{Type: TypeHeartbeatLost, Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "claude-code", Project: "acme-shop", Text: "stopped"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(lost.Text, "\U0001F480 *Claude Code* \u00b7 acme-shop \u00b7 Heartbeat Lost\n") {
		t.Errorf("heartbeat lost header = %q", lost.Text)
	}
	noProject, _ := f.svc.Alert(ctx, Alert{Type: TypeHeartbeatLost, Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "codex", Text: "stopped"})
	if !strings.HasPrefix(noProject.Text, "\U0001F480 *Codex* \u00b7 Heartbeat Lost\n") {
		t.Errorf("without a project: %q", noProject.Text)
	}
	back, _ := f.svc.Alert(ctx, Alert{Type: TypeHeartbeatBack, Level: store.LevelInfo, Source: "gemini", Project: "data-pipeline", Text: "back"})
	if !strings.HasPrefix(back.Text, "\U0001F7E2 *Gemini* \u00b7 data-pipeline\n") {
		t.Errorf("heartbeat back: %q", back.Text)
	}
}

func TestDigestListsEachHeldItemWithItsOwnHeader(t *testing.T) {
	f := newRulesFixture(t)
	f.svc.loc = time.UTC
	ctx := context.Background()
	enableQuietHours(t, f)
	f.clock.t = atClock(23, 0)
	for _, req := range []*SendRequest{
		{Level: store.LevelInfo, Source: "codex", Project: "acme-shop", Text: "step four"},
		{Level: store.LevelInfo, Source: "claude-code", Project: "data-pipeline", Text: "step five"},
	} {
		if _, err := f.svc.ValidateAndEnqueue(ctx, req); err != nil {
			t.Fatal(err)
		}
	}
	f.clock.t = atClock(7, 30)
	if _, err := f.svc.ReleaseHeld(ctx); err != nil {
		t.Fatal(err)
	}
	queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
	for _, m := range queued {
		if m.Source != "digest" {
			continue
		}
		for _, want := range []string{"\U0001F7E2 *Codex* \u00b7 acme-shop\nstep four", "\U0001F7E2 *Claude Code* \u00b7 data-pipeline\nstep five"} {
			if !strings.Contains(m.Text, want) {
				t.Errorf("digest is missing the item %q:\n%s", want, m.Text)
			}
		}
		return
	}
	t.Fatal("no digest")
}
