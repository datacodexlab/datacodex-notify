// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"datacodex-notify/internal/store"
)

func lastMessage(t *testing.T, s *store.Store) *store.Message {
	t.Helper()
	list, err := s.ListRecentMessages(context.Background(), 1, "", "")
	if err != nil || len(list) != 1 {
		t.Fatalf("expected one message: %v %v", list, err)
	}
	return list[0]
}

func TestSessionProjectShowsInTheHeaderAndSendInheritsIt(t *testing.T) {
	f := newAgentFixture(t)

	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"billing-k7f2","project":"billing-api"}`); code != http.StatusOK || !strings.Contains(body, `"project":"billing-api"`) {
		t.Fatalf("connect: %d %s", code, body)
	}

	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"first","source":"codex","session":"billing-k7f2"}`); code != http.StatusOK {
		t.Fatalf("send: %d %s", code, body)
	}
	if text := lastMessage(t, f.srv.store).Text; !strings.Contains(text, "billing-api") || !strings.Contains(text, "·") {
		t.Errorf("the session's project must reach the header: %q", text)
	}

	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"second","source":"codex","session":"billing-k7f2","project":"other-app"}`); code != http.StatusOK {
		t.Fatalf("send: %d %s", code, body)
	}
	if text := lastMessage(t, f.srv.store).Text; !strings.Contains(text, "other-app") || strings.Contains(text, "billing-api") {
		t.Errorf("an explicit project wins over the session's: %q", text)
	}

	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"x","project":"`+strings.Repeat("p", 81)+`"}`); code != http.StatusBadRequest {
		t.Errorf("over-long project: %d, want 400", code)
	}

	// Connecting again without a project keeps the one the session already has.
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"billing-k7f2"}`)
	if got := f.list(t); got.Sessions[0].SessionName != "billing-k7f2" {
		t.Fatalf("unexpected list: %+v", got)
	}
	f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"third","source":"codex","session":"billing-k7f2"}`)
	if text := lastMessage(t, f.srv.store).Text; !strings.Contains(text, "billing-api") {
		t.Errorf("reconnect without a project keeps it: %q", text)
	}
}

func TestAlertSettingsEndpoint(t *testing.T) {
	f := newAgentFixture(t)

	type alertRow struct {
		Kind    string `json:"kind"`
		Enabled bool   `json:"enabled"`
		Mode    string `json:"mode"`
	}
	read := func() map[string]alertRow {
		rr := f.ui.do(http.MethodGet, "/api/v1/ui/alerts", nil)
		if rr.Code != http.StatusOK {
			t.Fatalf("GET alerts: %d %s", rr.Code, rr.Body.String())
		}
		var out struct {
			Alerts []alertRow `json:"alerts"`
		}
		if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
			t.Fatal(err)
		}
		rows := map[string]alertRow{}
		for _, row := range out.Alerts {
			rows[row.Kind] = row
		}
		return rows
	}

	rows := read()
	if len(rows) != 5 || !rows["heartbeat_lost"].Enabled || rows["heartbeat_lost"].Mode != "both" || rows["agent_connect"].Enabled {
		t.Fatalf("unexpected defaults: %+v", rows)
	}

	rr := f.ui.do(http.MethodPut, "/api/v1/ui/alerts", map[string]interface{}{"kind": "digest", "enabled": false, "mode": "voice"})
	if rr.Code != http.StatusOK {
		t.Fatalf("PUT alert: %d %s", rr.Code, rr.Body.String())
	}
	if row := read()["digest"]; row.Enabled || row.Mode != "voice" {
		t.Errorf("the change was not saved: %+v", row)
	}

	// The agent connect switch is the same setting as the Connect agent tab's checkbox.
	f.ui.do(http.MethodPut, "/api/v1/ui/alerts", map[string]interface{}{"kind": "agent_connect", "enabled": true, "mode": "text"})
	if listed := f.ui.do(http.MethodGet, "/api/v1/ui/agents", nil); !strings.Contains(listed.Body.String(), `"notify_connect":true`) {
		t.Errorf("the Connect agent tab must see the same switch: %s", listed.Body.String())
	}

	for name, payload := range map[string]map[string]interface{}{
		"unknown kind": {"kind": "nope", "enabled": true, "mode": "text"},
		"bad mode":     {"kind": "digest", "enabled": true, "mode": "auto"},
	} {
		if rr := f.ui.do(http.MethodPut, "/api/v1/ui/alerts", payload); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: %d, want 400", name, rr.Code)
		}
	}
}

func TestUITestMessageAcceptsAMode(t *testing.T) {
	f := newAgentFixture(t)
	for _, mode := range []string{"text", "voice", "both"} {
		rr := f.ui.do(http.MethodPost, "/api/v1/ui/test", map[string]string{"mode": mode})
		if rr.Code != http.StatusOK {
			t.Fatalf("test %s: %d %s", mode, rr.Code, rr.Body.String())
		}
		// Without voice support in this fixture the message falls back to text and is never lost.
		if got := lastMessage(t, f.srv.store); got.Mode != store.ModeText || got.Status != store.StatusQueued {
			t.Errorf("%s: mode=%s status=%s", mode, got.Mode, got.Status)
		}
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/test", map[string]string{"mode": "shout"}); rr.Code != http.StatusBadRequest {
		t.Errorf("bad mode: %d, want 400", rr.Code)
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/test", nil); rr.Code != http.StatusOK {
		t.Errorf("no body: %d", rr.Code)
	}
}

func TestVoiceTestModesAndSpokenIntroSetting(t *testing.T) {
	ui, _, s := newVoiceUI(t, true)
	if _, err := s.AddRecipient(context.Background(), "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}
	if rr := addKey(t, ui, "Work", apiKeyA); rr.Code != http.StatusCreated {
		t.Fatalf("add key: %d %s", rr.Code, rr.Body.String())
	}

	for _, mode := range []string{"voice", "both"} {
		if rr := ui.do(http.MethodPost, "/api/v1/ui/test", map[string]string{"mode": mode}); rr.Code != http.StatusOK {
			t.Fatalf("test %s: %d %s", mode, rr.Code, rr.Body.String())
		}
		if got := lastMessage(t, s); string(got.Mode) != mode || got.Speech == "" {
			t.Errorf("%s: mode=%s speech=%q", mode, got.Mode, got.Speech)
		}
	}

	read := func() bool {
		var out struct {
			SpokenIntro bool `json:"spoken_intro"`
		}
		rr := ui.do(http.MethodGet, "/api/v1/ui/voice", nil)
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		return out.SpokenIntro
	}
	if !read() {
		t.Error("the spoken intro defaults to on")
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice", map[string]bool{"spoken_intro": false}); rr.Code != http.StatusOK {
		t.Fatalf("PUT intro: %d %s", rr.Code, rr.Body.String())
	}
	if read() {
		t.Error("the spoken intro must turn off")
	}
}

func TestSendCarriesTitleAndFieldsIntoTheTemplate(t *testing.T) {
	f := newAgentFixture(t)
	body := `{"level":"info","source":"codex","text":"Merged.","title":"Phase 3 done","fields":[{"key":"Phase","value":"3 of 5"}]}`
	if code, out := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", body); code != http.StatusOK {
		t.Fatalf("send: %d %s", code, out)
	}
	text := lastMessage(t, f.srv.store).Text
	for _, want := range []string{"📌 *Phase 3 done*", "▫️ *Phase:* 3 of 5", "Merged.", "━━━━━━━━━━━━━━", "🕐 "} {
		if !strings.Contains(text, want) {
			t.Errorf("the message is missing %q:\n%s", want, text)
		}
	}
	bad := `{"level":"info","text":"x","fields":[{"key":"","value":"v"}]}`
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", bad); code != http.StatusBadRequest {
		t.Errorf("an invalid field: %d, want 400", code)
	}
}

func TestVoiceSpeedSetting(t *testing.T) {
	ui, _, _ := newVoiceUI(t, true)
	read := func() string {
		var out struct {
			Status struct {
				Speed  string   `json:"speed"`
				Speeds []string `json:"speeds"`
			} `json:"status"`
		}
		rr := ui.do(http.MethodGet, "/api/v1/ui/voice", nil)
		_ = json.Unmarshal(rr.Body.Bytes(), &out)
		if len(out.Status.Speeds) != 3 {
			t.Errorf("the choices must be listed: %v", out.Status.Speeds)
		}
		return out.Status.Speed
	}
	if read() != "normal" {
		t.Error("the default speed is normal")
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice", map[string]string{"speed": "very_fast"}); rr.Code != http.StatusOK {
		t.Fatalf("PUT speed: %d %s", rr.Code, rr.Body.String())
	}
	if read() != "very_fast" {
		t.Error("the speed was not saved")
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice", map[string]string{"speed": "turbo"}); rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown speed: %d, want 400", rr.Code)
	}
}
