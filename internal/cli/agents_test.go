// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func newRecordingServer(t *testing.T, status int, reply string) (*httptest.Server, *[]heartbeatCall) {
	t.Helper()
	var calls []heartbeatCall
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		calls = append(calls, heartbeatCall{method: r.Method, path: r.URL.Path, body: body})
		w.WriteHeader(status)
		_, _ = w.Write([]byte(reply))
	}))
	t.Cleanup(srv.Close)
	return srv, &calls
}

func TestConnectSendsTheSessionAndAbsoluteRulesFiles(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{"success":true}`)
	e, out, _ := testEnv(srv, "tok")

	relative := filepath.Join("docs", "CLAUDE.md")
	code := execute([]string{"connect", "--source", "claude-code", "--session", "proj-1",
		"--rules-file", `C:\Users\x\.claude\CLAUDE.md`, "--rules-file", relative}, "1", e)
	if code != exitOK || len(*calls) != 1 {
		t.Fatalf("code=%d calls=%d", code, len(*calls))
	}
	c := (*calls)[0]
	if c.method != http.MethodPost || c.path != "/api/v1/agents/connect" || c.body["source"] != "claude-code" || c.body["session"] != "proj-1" {
		t.Errorf("unexpected request: %+v", c)
	}
	files, _ := c.body["rules_files"].([]interface{})
	if len(files) != 2 || files[0] != `C:\Users\x\.claude\CLAUDE.md` {
		t.Fatalf("rules files = %v", files)
	}
	if second, _ := files[1].(string); !filepath.IsAbs(second) || !strings.HasSuffix(second, relative) {
		t.Errorf("a relative path must be made absolute, got %q", second)
	}
	if !strings.Contains(out.String(), `"success":true`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestConnectWithoutRulesFilesSendsAnEmptyList(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{}`)
	e, _, _ := testEnv(srv, "tok")
	if code := execute([]string{"connect", "--source", "codex", "--session", "s"}, "1", e); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	if files, ok := (*calls)[0].body["rules_files"].([]interface{}); !ok || len(files) != 0 {
		t.Errorf("rules_files = %v", (*calls)[0].body["rules_files"])
	}
}

func TestDisconnectSendsOnlyTheSession(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{"success":true,"found":true}`)
	e, out, _ := testEnv(srv, "tok")

	if code := execute([]string{"disconnect", "--source", "codex", "--session", "proj-1"}, "1", e); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	c := (*calls)[0]
	if c.path != "/api/v1/agents/disconnect" || c.body["session"] != "proj-1" || len(c.body) != 1 {
		t.Errorf("unexpected request: %+v", c)
	}
	if !strings.Contains(out.String(), `"found":true`) {
		t.Errorf("stdout = %q", out.String())
	}
}

func TestConnectAndDisconnectValidation(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{}`)
	for name, args := range map[string][]string{
		"connect no source":     {"connect", "--session", "s"},
		"connect no session":    {"connect", "--source", "codex"},
		"connect unknown flag":  {"connect", "--nope"},
		"disconnect no session": {"disconnect"},
		"disconnect bad flag":   {"disconnect", "--nope"},
	} {
		e, _, _ := testEnv(srv, "tok")
		if code := execute(args, "1", e); code != exitValidation {
			t.Errorf("%s: got %d, want %d", name, code, exitValidation)
		}
	}
	if len(*calls) != 0 {
		t.Errorf("invalid input must not reach the server: %+v", *calls)
	}
}

func TestMutedAgentsGetExitCode6(t *testing.T) {
	srv, _ := newRecordingServer(t, http.StatusLocked, `{"error":"notifications disabled by the user","success":false}`)

	for name, args := range map[string][]string{
		"send":      {"send", "--level", "info", "--text", "x", "--session", "s"},
		"heartbeat": {"heartbeat", "--id", "h", "--session", "s"},
		"connect":   {"connect", "--source", "codex", "--session", "s"},
	} {
		e, _, errOut := testEnv(srv, "tok")
		if code := execute(args, "1", e); code != exitMuted {
			t.Errorf("%s: got %d, want %d", name, code, exitMuted)
		}
		if !strings.Contains(errOut.String(), "disabled by the user") {
			t.Errorf("%s: the reason must reach the agent: %q", name, errOut.String())
		}
	}
	if exitMuted != 6 {
		t.Errorf("exit code for muted agents must be 6, got %d", exitMuted)
	}
}

func TestSendAndHeartbeatCarryTheSession(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{"id":"m1","status":"queued"}`)

	e, _, _ := testEnv(srv, "tok")
	if code := execute([]string{"send", "--level", "info", "--text", "hello", "--session", "proj-1"}, "1", e); code != exitOK {
		t.Fatalf("send code = %d", code)
	}
	e, _, _ = testEnv(srv, "tok")
	if code := execute([]string{"heartbeat", "--id", "h1", "--session", "proj-1"}, "1", e); code != exitOK {
		t.Fatalf("heartbeat code = %d", code)
	}
	for _, c := range *calls {
		if c.body["session"] != "proj-1" {
			t.Errorf("%s did not carry the session: %v", c.path, c.body)
		}
	}

	*calls = nil
	e, _, _ = testEnv(srv, "tok")
	_ = execute([]string{"send", "--level", "info", "--text", "hello"}, "1", e)
	if _, has := (*calls)[0].body["session"]; has {
		t.Errorf("without --session no session is sent: %v", (*calls)[0].body)
	}
}

func TestRulesFilesFlagCanBeRepeated(t *testing.T) {
	var list stringList
	if err := list.Set("a"); err != nil {
		t.Fatal(err)
	}
	_ = list.Set("b")
	if list.String() != "a,b" {
		t.Errorf("list = %s", list.String())
	}
}

func TestProjectFlagReachesConnectAndSend(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{"id":"m1","status":"queued"}`)

	e, _, _ := testEnv(srv, "tok")
	if code := execute([]string{"connect", "--source", "codex", "--session", "s1", "--project", "billing-api"}, "1", e); code != exitOK {
		t.Fatalf("connect code = %d", code)
	}
	e, _, _ = testEnv(srv, "tok")
	if code := execute([]string{"send", "--level", "info", "--text", "hello", "--project", "billing-api"}, "1", e); code != exitOK {
		t.Fatalf("send code = %d", code)
	}
	for _, c := range *calls {
		if c.body["project"] != "billing-api" {
			t.Errorf("%s did not carry the project: %v", c.path, c.body)
		}
	}

	*calls = nil
	e, _, _ = testEnv(srv, "tok")
	_ = execute([]string{"send", "--level", "info", "--text", "hello"}, "1", e)
	if _, has := (*calls)[0].body["project"]; has {
		t.Errorf("without --project nothing is sent: %v", (*calls)[0].body)
	}
}
