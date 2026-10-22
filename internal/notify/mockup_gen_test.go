// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/store"
)

// TestGenerateMockupMessages writes the texts the presentation and README mockups
// show, produced by the real template code. It only runs when DCN_MOCKUP_OUT names
// a folder: DCN_MOCKUP_OUT=docs/assets/mockups go test ./internal/notify -run Mockup
func TestGenerateMockupMessages(t *testing.T) {
	out := os.Getenv("DCN_MOCKUP_OUT")
	if out == "" {
		t.Skip("set DCN_MOCKUP_OUT to regenerate the mockup texts")
	}
	type item struct {
		Kind string `json:"kind"`
		Text string `json:"text"`
	}
	result := map[string][]item{}
	ctx := context.Background()

	for _, lang := range []string{"en", "ar"} {
		f := newRulesFixture(t)
		f.svc.locale = locale.NewManager(lang)
		f.svc.loc = time.UTC
		f.clock.t = time.Date(2026, 10, 21, 10, 42, 0, 0, time.UTC)
		var items []item
		add := func(kind string, msg *store.Message, err error) {
			if err != nil || msg == nil {
				t.Fatalf("%s: %v", kind, err)
			}
			items = append(items, item{kind, msg.Text})
		}

		m, err := f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelInfo, Source: "codex", Project: "acme-shop", Title: titleFor(lang, "phase"), Text: bodyFor(lang, "phase"), Fields: []Field{{fieldFor(lang, "phase"), "3 / 5"}, {fieldFor(lang, "tests"), "42 ✓"}}})
		add("codex-info", m, err)
		m, err = f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelWarning, Source: "claude-code", Project: "data-pipeline", Title: titleFor(lang, "warn"), Text: bodyFor(lang, "warn"), Fields: []Field{{fieldFor(lang, "tests"), "2 ✕"}}})
		add("claude-warning", m, err)
		m, err = f.svc.ValidateAndEnqueue(ctx, &SendRequest{Level: store.LevelCritical, Source: "codex", Project: "acme-shop", Title: titleFor(lang, "crit"), Text: bodyFor(lang, "crit")})
		add("codex-critical", m, err)
		m, err = f.svc.Alert(ctx, Alert{Type: TypeHeartbeatLost, Kind: AlertHeartbeatLost, Level: store.LevelCritical, Source: "claude-code", Project: "data-pipeline", Text: bodyFor(lang, "hb")})
		add("heartbeat-lost", m, err)

		enableQuietHours(t, f)
		f.clock.t = atClock(23, 0)
		for _, held := range []*SendRequest{
			{Level: store.LevelInfo, Source: "codex", Project: "acme-shop", Text: bodyFor(lang, "held1")},
			{Level: store.LevelInfo, Source: "claude-code", Project: "data-pipeline", Text: bodyFor(lang, "held2")},
		} {
			if _, err := f.svc.ValidateAndEnqueue(ctx, held); err != nil {
				t.Fatal(err)
			}
		}
		f.clock.t = atClock(7, 30)
		if _, err := f.svc.ReleaseHeld(ctx); err != nil {
			t.Fatal(err)
		}
		queued, _ := f.store.ListMessagesByStatus(ctx, store.StatusQueued)
		for _, q := range queued {
			if q.Source == "digest" {
				items = append(items, item{"digest", q.Text})
			}
		}
		if len(items) != 5 {
			t.Fatalf("expected 5 messages, got %d", len(items))
		}
		items = append(items, item{"voice-speech", speechFor(lang)})
		result[lang] = items
	}

	if err := os.MkdirAll(out, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(result, "", "  ")
	if err := os.WriteFile(filepath.Join(out, "messages.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func ar(codes ...rune) string { return string(codes) }

func titleFor(lang, key string) string {
	if lang == "en" {
		return map[string]string{"phase": "Phase 3 finished", "warn": "Two tests failed", "crit": "Build is blocked"}[key]
	}
	return map[string]string{
		"phase": ar(0x0627, 0x0646, 0x062a, 0x0647, 0x062a, 0x0020, 0x0627, 0x0644, 0x0645, 0x0631, 0x062d, 0x0644, 0x0629, 0x0020, 0x0663),
		"warn":  ar(0x0641, 0x0634, 0x0644, 0x0020, 0x0627, 0x062e, 0x062a, 0x0628, 0x0627, 0x0631, 0x0627, 0x0646),
		"crit":  ar(0x0627, 0x0644, 0x0628, 0x0646, 0x0627, 0x0621, 0x0020, 0x0645, 0x062a, 0x0648, 0x0642, 0x0641),
	}[key]
}

func bodyFor(lang, key string) string {
	if lang == "en" {
		return map[string]string{
			"phase": "Merged the branch and the checkout flow is ready for review.",
			"warn":  "The payment tests fail after the last refactor.",
			"crit":  "Needs your approval to continue with the migration.",
			"hb":    "The session stopped reporting. Last beat: 10:31.",
			"held1": "Step 4 finished.",
			"held2": "Step 5 finished.",
		}[key]
	}
	return map[string]string{
		"phase": ar(0x062a, 0x0645, 0x0020, 0x062f, 0x0645, 0x062c, 0x0020, 0x0627, 0x0644, 0x0641, 0x0631, 0x0639, 0x0020, 0x0648, 0x0627, 0x0644, 0x0643, 0x0648, 0x062f, 0x0020, 0x062c, 0x0627, 0x0647, 0x0632, 0x0020, 0x0644, 0x0644, 0x0645, 0x0631, 0x0627, 0x062c, 0x0639, 0x0629),
		"warn":  ar(0x0627, 0x062e, 0x062a, 0x0628, 0x0627, 0x0631, 0x0627, 0x062a, 0x0020, 0x0627, 0x0644, 0x062f, 0x0641, 0x0639, 0x0020, 0x0628, 0x062a, 0x0641, 0x0634, 0x0644, 0x0020, 0x0628, 0x0639, 0x062f, 0x0020, 0x0622, 0x062e, 0x0631, 0x0020, 0x062a, 0x0639, 0x062f, 0x064a, 0x0644),
		"crit":  ar(0x0645, 0x062d, 0x062a, 0x0627, 0x062c, 0x0020, 0x0645, 0x0648, 0x0627, 0x0641, 0x0642, 0x062a, 0x0643, 0x0020, 0x0639, 0x0644, 0x0634, 0x0627, 0x0646, 0x0020, 0x0623, 0x0643, 0x0645, 0x0644),
		"hb":    ar(0x0627, 0x0644, 0x062c, 0x0644, 0x0633, 0x0629, 0x0020, 0x0648, 0x0642, 0x0641, 0x062a, 0x0020, 0x0639, 0x0646, 0x0020, 0x0627, 0x0644, 0x062a, 0x0628, 0x0644, 0x064a, 0x063a),
		"held1": ar(0x062e, 0x0637, 0x0648, 0x0629, 0x0020, 0x0664, 0x0020, 0x062e, 0x0644, 0x0635, 0x062a),
		"held2": ar(0x062e, 0x0637, 0x0648, 0x0629, 0x0020, 0x0665, 0x0020, 0x062e, 0x0644, 0x0635, 0x062a),
	}[key]
}

func fieldFor(lang, key string) string {
	if lang == "en" {
		return map[string]string{"phase": "Phase", "tests": "Tests"}[key]
	}
	return map[string]string{
		"phase": ar(0x0627, 0x0644, 0x0645, 0x0631, 0x062d, 0x0644, 0x0629),
		"tests": ar(0x0627, 0x0644, 0x0627, 0x062e, 0x062a, 0x0628, 0x0627, 0x0631, 0x0627, 0x062a),
	}[key]
}

func speechFor(lang string) string {
	if lang == "en" {
		return "Message from Codex, project acme-shop. Build is blocked."
	}
	return ar(0x0631, 0x0633, 0x0627, 0x0644, 0x0629, 0x0020, 0x0645, 0x0646, 0x0020, 0x0643, 0x0648, 0x062f, 0x0643, 0x0633, 0x0020, 0x0641, 0x064a, 0x0020, 0x0645, 0x0634, 0x0631, 0x0648, 0x0639, 0x0020, 0x0027) + "acme-shop" + "'"
}
