// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"datacodex-notify/internal/agents"
	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/secrets/secretstest"
	"datacodex-notify/internal/skillinstall"
)

const (
	skillSample = "skill text for tests"
	testCLIPath = `D:\Apps\Notify\datacodex-notify-cli.exe`
)

type agentFixture struct {
	srv    *Server
	ui     *uiClient
	token  string
	tokens *secretstest.Memory
	roots  []string
}

func newAgentFixture(t *testing.T) *agentFixture {
	t.Helper()
	base := t.TempDir()
	roots := []string{filepath.Join(base, ".claude", "skills"), filepath.Join(base, ".codex", "skills")}
	for _, r := range roots {
		if err := os.MkdirAll(r, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	tokens := secretstest.NewMemory()

	srv, s, _, token := setupTestServer(t, withHeartbeats, func(cfg *Config) {
		reg := agents.New(cfg.Store, cfg.NotifySvc, cfg.Locale)
		cfg.Agents = reg
		cfg.Skill = skillinstall.New(roots, []byte(skillSample), `D:\Apps\Notify`)
		cfg.Tokens = tokens
		cfg.CLIPath = testCLIPath
		if hb, ok := cfg.Heartbeats.(interface {
			SetSuppress(func(context.Context) bool)
		}); ok {
			hb.SetSuppress(func(ctx context.Context) bool { m, _ := reg.MuteAll(ctx); return m })
		}
	})
	t.Cleanup(func() { s.Close() })
	if _, err := s.AddRecipient(context.Background(), "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}
	if err := tokens.Set(secrets.TargetAPIToken, token); err != nil {
		t.Fatal(err)
	}
	return &agentFixture{srv: srv, ui: newUIClient(t, srv), token: token, tokens: tokens, roots: roots}
}

func (f *agentFixture) agent(t *testing.T, token, method, path, body string) (int, string) {
	t.Helper()
	rr := agentRequest(t, f.srv, token, method, path, body)
	return rr.Code, rr.Body.String()
}

type agentList struct {
	Sessions []struct {
		ID          string   `json:"id"`
		Source      string   `json:"source"`
		SessionName string   `json:"session_name"`
		RulesFiles  []string `json:"rules_files"`
		Status      string   `json:"status"`
		Muted       bool     `json:"muted"`
	} `json:"sessions"`
	MuteAll bool `json:"mute_all"`
	Skill   []skillinstall.TargetStatus
}

func (f *agentFixture) list(t *testing.T) agentList {
	t.Helper()
	rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET agents: %d %s", rr.Code, rr.Body.String())
	}
	var out agentList
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("bad payload: %v", err)
	}
	return out
}

func TestConnectDisconnectAndListing(t *testing.T) {
	f := newAgentFixture(t)

	code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect",
		`{"source":"claude-code","session":"proj-1","rules_files":["C:\\Users\\x\\.claude\\CLAUDE.md"]}`)
	if code != http.StatusOK || !strings.Contains(body, `"session_name":"proj-1"`) {
		t.Fatalf("connect: %d %s", code, body)
	}

	listed := f.list(t)
	if len(listed.Sessions) != 1 || listed.Sessions[0].Status != "connected" || listed.Sessions[0].RulesFiles[0] != `C:\Users\x\.claude\CLAUDE.md` || listed.MuteAll {
		t.Fatalf("unexpected list: %+v", listed)
	}

	for name, payload := range map[string]string{
		"bad source":    `{"source":"bing","session":"x"}`,
		"bad name":      `{"source":"codex","session":"has space"}`,
		"relative file": `{"source":"codex","session":"x","rules_files":["AGENTS.md"]}`,
		"bad json":      `{nope`,
	} {
		if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", payload); code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, code)
		}
	}

	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/disconnect", `{"session":"proj-1"}`); code != http.StatusOK {
		t.Errorf("disconnect: %d", code)
	}
	if got := f.list(t).Sessions[0].Status; got != "disconnected" {
		t.Errorf("status = %s, want disconnected", got)
	}
	code, body = f.agent(t, f.token, http.MethodPost, "/api/v1/agents/disconnect", `{"session":"never"}`)
	if code != http.StatusOK || !strings.Contains(body, `"found":false`) {
		t.Errorf("unknown session: %d %s", code, body)
	}
	if code, _ := f.agent(t, f.token, http.MethodGet, "/api/v1/agents/connect", ""); code != http.StatusMethodNotAllowed {
		t.Errorf("GET connect: %d, want 405", code)
	}
	if code, _ := f.agent(t, "wrong", http.MethodPost, "/api/v1/agents/connect", `{}`); code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d, want 401", code)
	}
}

func TestSessionActivityIsRecordedWhenASessionIsNamed(t *testing.T) {
	f := newAgentFixture(t)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"proj-1"}`)

	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"hello","session":"proj-1"}`); code != http.StatusOK || !strings.Contains(body, `"status":"queued"`) {
		t.Fatalf("notify: %d %s", code, body)
	}
	got, _ := f.srv.agents.(*agents.Service).Session(context.Background(), f.list(t).Sessions[0].ID)
	if got.LastMessageAt == nil || got.LastSeenAt == nil {
		t.Errorf("a named send must update last seen and last message: %+v", got)
	}

	f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/run-1", `{"every_s":60,"session":"proj-1"}`)
	hbList, _ := f.srv.heartbeats.List(context.Background())
	if len(hbList) != 1 || hbList[0].Session != "proj-1" {
		t.Errorf("the heartbeat must remember its session: %+v", hbList)
	}
}

func TestMutedSessionGetsHTTP423AndLosesItsHeartbeats(t *testing.T) {
	f := newAgentFixture(t)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"claude-code","session":"alpha"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"beta"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/hb-a", `{"every_s":60,"session":"alpha"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/hb-b", `{"every_s":60,"session":"beta"}`)

	var alphaID string
	for _, s := range f.list(t).Sessions {
		if s.SessionName == "alpha" {
			alphaID = s.ID
		}
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/sessions/"+alphaID+"/mute", map[string]bool{"muted": true}); rr.Code != http.StatusOK {
		t.Fatalf("mute: %d %s", rr.Code, rr.Body.String())
	}

	if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"x","session":"alpha"}`); code != http.StatusLocked || !strings.Contains(body, "disabled by the user") {
		t.Errorf("muted notify: %d %s, want 423", code, body)
	}
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/hb-a", `{"session":"alpha"}`); code != http.StatusLocked {
		t.Errorf("muted heartbeat: %d, want 423", code)
	}
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"claude-code","session":"alpha"}`); code != http.StatusLocked {
		t.Errorf("muted connect: %d, want 423", code)
	}
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"x","session":"beta"}`); code != http.StatusOK {
		t.Errorf("another session is unaffected: %d", code)
	}
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"anonymous"}`); code != http.StatusOK {
		t.Errorf("a request without a session is unaffected: %d", code)
	}
	if code, _ := f.agent(t, f.token, http.MethodDelete, "/api/v1/heartbeats/hb-a", ""); code != http.StatusOK {
		t.Errorf("stopping a heartbeat is always allowed: %d", code)
	}

	hbList, _ := f.srv.heartbeats.List(context.Background())
	if len(hbList) != 1 || hbList[0].ID != "hb-b" {
		t.Errorf("only the muted session's heartbeats are removed: %+v", hbList)
	}
	if got := f.list(t).Sessions; got[0].SessionName == "alpha" && got[0].Status != "muted" && got[1].Status != "muted" {
		t.Errorf("the list must show the muted status: %+v", got)
	}

	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/sessions/"+alphaID+"/mute", map[string]bool{"muted": false}); rr.Code != http.StatusOK {
		t.Fatalf("unmute: %d", rr.Code)
	}
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"back","session":"alpha"}`); code != http.StatusOK {
		t.Errorf("unmuted session: %d", code)
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/sessions/missing/mute", map[string]bool{"muted": true}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown session: %d, want 404", rr.Code)
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/sessions/x/other", map[string]bool{"muted": true}); rr.Code != http.StatusNotFound {
		t.Errorf("unknown action: %d, want 404", rr.Code)
	}
}

func TestGlobalMuteStopsEveryAgent(t *testing.T) {
	f := newAgentFixture(t)
	f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", `{"source":"codex","session":"alpha"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/hb-a", `{"every_s":60,"session":"alpha"}`)
	f.agent(t, f.token, http.MethodPost, "/api/v1/heartbeats/hb-none", `{"every_s":60}`)

	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/mute-all", map[string]bool{"muted": true}); rr.Code != http.StatusOK {
		t.Fatalf("mute all: %d", rr.Code)
	}
	if !f.list(t).MuteAll {
		t.Error("the list must report the global mute")
	}
	for name, tc := range map[string]struct{ path, body string }{
		"notify":    {"/api/v1/notify", `{"level":"critical","text":"x"}`},
		"named":     {"/api/v1/notify", `{"level":"info","text":"x","session":"alpha"}`},
		"heartbeat": {"/api/v1/heartbeats/hb-none", `{}`},
		"connect":   {"/api/v1/agents/connect", `{"source":"codex","session":"fresh"}`},
	} {
		if code, _ := f.agent(t, f.token, http.MethodPost, tc.path, tc.body); code != http.StatusLocked {
			t.Errorf("%s: got %d, want 423", name, code)
		}
	}
	if hbList, _ := f.srv.heartbeats.List(context.Background()); len(hbList) != 0 {
		t.Errorf("the global mute removes every heartbeat: %+v", hbList)
	}

	f.ui.do(http.MethodPost, "/api/v1/ui/agents/mute-all", map[string]bool{"muted": false})
	if code, _ := f.agent(t, f.token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"free again"}`); code != http.StatusOK {
		t.Errorf("after unmuting: %d", code)
	}
}

func TestTokenRotationSwapsLiveWithoutRestart(t *testing.T) {
	f := newAgentFixture(t)
	if code, _ := f.agent(t, f.token, http.MethodGet, "/api/v1/status", ""); code != http.StatusOK {
		t.Fatalf("the original token must work: %d", code)
	}

	rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/rotate-token", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("rotate: %d %s", rr.Code, rr.Body.String())
	}
	newToken, err := f.tokens.Get(secrets.TargetAPIToken)
	if err != nil || newToken == "" || newToken == f.token {
		t.Fatalf("a new token must be stored: %q err=%v", newToken, err)
	}
	if strings.Contains(rr.Body.String(), newToken) || strings.Contains(rr.Body.String(), f.token) {
		t.Error("the response must never contain a token")
	}

	if code, _ := f.agent(t, f.token, http.MethodGet, "/api/v1/status", ""); code != http.StatusUnauthorized {
		t.Errorf("the old token must be cut off at once: %d", code)
	}
	if code, _ := f.agent(t, newToken, http.MethodGet, "/api/v1/status", ""); code != http.StatusOK {
		t.Errorf("the new token must work at once: %d", code)
	}
}

func TestTokenRotationNeedsATokenStore(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	if rr := newUIClient(t, srv).do(http.MethodPost, "/api/v1/ui/agents/rotate-token", nil); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}

func TestSkillInstallAndRemoveTouchOnlyTheTwoFolders(t *testing.T) {
	f := newAgentFixture(t)
	neighbour := filepath.Join(f.roots[0], "other-skill", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(neighbour), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(neighbour, []byte("not ours"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, st := range f.list(t).Skill {
		if !st.Available || st.Installed {
			t.Errorf("before install: %+v", st)
		}
	}
	rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/skill", map[string]string{"action": "install"})
	if rr.Code != http.StatusOK {
		t.Fatalf("install: %d %s", rr.Code, rr.Body.String())
	}
	for _, root := range f.roots {
		if data, err := os.ReadFile(filepath.Join(root, "datacodex-notify", "SKILL.md")); err != nil || string(data) != skillSample {
			t.Errorf("the skill was not installed in %s: %v", root, err)
		}
	}
	for _, st := range f.list(t).Skill {
		if !st.Installed || !st.UpToDate {
			t.Errorf("after install: %+v", st)
		}
	}

	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/skill", map[string]string{"action": "remove"}); rr.Code != http.StatusOK {
		t.Fatalf("remove: %d", rr.Code)
	}
	for _, root := range f.roots {
		if _, err := os.Stat(filepath.Join(root, "datacodex-notify")); !os.IsNotExist(err) {
			t.Errorf("the skill folder must be gone from %s", root)
		}
	}
	if data, err := os.ReadFile(neighbour); err != nil || string(data) != "not ours" {
		t.Error("a neighbouring skill must never be touched")
	}

	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/skill", map[string]string{"action": "format-disk"}); rr.Code != http.StatusBadRequest {
		t.Errorf("an unknown action: %d, want 400", rr.Code)
	}
}

func TestAgentEndpointsRequireASessionAndAreUnavailableWithoutTheService(t *testing.T) {
	f := newAgentFixture(t)
	rr := agentRequest(t, f.srv, "", http.MethodGet, "/api/v1/ui/agents", "")
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no UI session: %d, want 401", rr.Code)
	}

	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	if rr := newUIClient(t, srv).do(http.MethodGet, "/api/v1/ui/agents", nil); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("without the service: %d, want 503", rr.Code)
	}
	if rr := agentRequest(t, srv, token, http.MethodPost, "/api/v1/agents/connect", `{}`); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("connect without the service: %d, want 503", rr.Code)
	}
}

func TestRemovingASessionDeletesItsRowAndConnectNoticeSettingPersists(t *testing.T) {
	f := newAgentFixture(t)
	for _, name := range []string{"one", "two"} {
		payload := `{"source":"codex","session":"` + name + `"}`
		if code, body := f.agent(t, f.token, http.MethodPost, "/api/v1/agents/connect", payload); code != http.StatusOK {
			t.Fatalf("connect: %d %s", code, body)
		}
	}
	listed := f.list(t)
	if len(listed.Sessions) != 2 {
		t.Fatalf("want two sessions: %+v", listed)
	}
	if rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents", nil); !strings.Contains(rr.Body.String(), `"notify_connect":false`) {
		t.Errorf("the connect notice defaults to off: %s", rr.Body.String())
	}
	if rr := f.ui.do(http.MethodPost, "/api/v1/ui/agents/notify-connect", map[string]bool{"enabled": true}); rr.Code != http.StatusOK {
		t.Fatalf("notify-connect: %d %s", rr.Code, rr.Body.String())
	}
	if rr := f.ui.do(http.MethodGet, "/api/v1/ui/agents", nil); !strings.Contains(rr.Body.String(), `"notify_connect":true`) {
		t.Errorf("the setting must persist: %s", rr.Body.String())
	}

	id := listed.Sessions[0].ID
	if rr := f.ui.do(http.MethodDelete, "/api/v1/ui/agents/sessions/"+id, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete: %d %s", rr.Code, rr.Body.String())
	}
	if after := f.list(t); len(after.Sessions) != 1 || after.Sessions[0].ID == id {
		t.Errorf("the row must be gone: %+v", after)
	}
	if rr := f.ui.do(http.MethodDelete, "/api/v1/ui/agents/sessions/"+id, nil); rr.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d, want 404", rr.Code)
	}
}
