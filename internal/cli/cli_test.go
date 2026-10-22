// SPDX-License-Identifier: GPL-3.0-only

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// arabicSample is built from code points so the source file stays ASCII-only.
var arabicSample = string([]rune{
	0x0631, 0x0633, 0x0627, 0x0644, 0x0629, ' ',
	0x062a, 0x062c, 0x0631, 0x064a, 0x0628, 0x064a, 0x0629,
})

func TestReadTextOrFile(t *testing.T) {
	dir := t.TempDir()

	plain := filepath.Join(dir, "plain.txt")
	if err := os.WriteFile(plain, []byte(arabicSample), 0o644); err != nil {
		t.Fatal(err)
	}
	withBOM := filepath.Join(dir, "bom.txt")
	if err := os.WriteFile(withBOM, append([]byte{0xEF, 0xBB, 0xBF}, arabicSample...), 0o644); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name, text, file, want string
		wantErr                bool
	}{
		{name: "direct text wins", text: "direct", file: plain, want: "direct"},
		{name: "file without BOM", file: plain, want: arabicSample},
		{name: "file with BOM", file: withBOM, want: arabicSample},
		{name: "both empty", want: ""},
		{name: "missing file", file: filepath.Join(dir, "missing.txt"), wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := readTextOrFile(tc.text, tc.file)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, wantErr %v", err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

func newTestServer(t *testing.T, token string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+token {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "unauthorized"})
			return
		}
		switch {
		case r.URL.Path == "/api/v1/status" && r.Method == http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"state": "unlinked"})
		case r.URL.Path == "/api/v1/ui/open" && r.Method == http.MethodPost:
			_ = json.NewEncoder(w).Encode(map[string]bool{"ok": true})
		case r.URL.Path == "/api/v1/notify" && r.Method == http.MethodPost:
			var body map[string]interface{}
			_ = json.NewDecoder(r.Body).Decode(&body)
			switch body["level"] {
			case "invalid":
				w.WriteHeader(http.StatusBadRequest)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "invalid level"})
			case "limited":
				w.WriteHeader(http.StatusTooManyRequests)
				_ = json.NewEncoder(w).Encode(map[string]string{"error": "rate limit"})
			case "dup":
				_ = json.NewEncoder(w).Encode(map[string]string{"id": "m2", "status": "deduplicated"})
			default:
				_ = json.NewEncoder(w).Encode(map[string]interface{}{"id": "m1", "status": "queued", "echo": body["text"]})
			}
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(srv.Close)
	return srv
}

func testEnv(srv *httptest.Server, token string) (env, *bytes.Buffer, *bytes.Buffer) {
	var out, errOut bytes.Buffer
	return env{
		stdout: &out,
		stderr: &errOut,
		newClient: func() (*Client, int, error) {
			return &Client{BaseURL: srv.URL + "/api/v1", Token: token, HTTPClient: srv.Client()}, exitOK, nil
		},
	}, &out, &errOut
}

func TestClientExitCodes(t *testing.T) {
	srv := newTestServer(t, "good")
	c := &Client{BaseURL: srv.URL + "/api/v1", Token: "good", HTTPClient: srv.Client()}

	var status map[string]interface{}
	if code, err := c.Get("/status", &status); err != nil || code != exitOK || status["state"] != "unlinked" {
		t.Fatalf("Get status: code=%d err=%v body=%v", code, err, status)
	}
	if code, _ := c.Post("/notify", map[string]string{"level": "invalid"}, nil); code != exitValidation {
		t.Errorf("validation: got %d, want %d", code, exitValidation)
	}
	if code, _ := c.Post("/notify", map[string]string{"level": "limited"}, nil); code != exitThrottled {
		t.Errorf("rate limit: got %d, want %d", code, exitThrottled)
	}
	if code, _ := c.Get("/missing", nil); code != exitServerError {
		t.Errorf("not found: got %d, want %d", code, exitServerError)
	}

	bad := &Client{BaseURL: srv.URL + "/api/v1", Token: "bad", HTTPClient: srv.Client()}
	if code, _ := bad.Get("/status", nil); code != exitAuth {
		t.Errorf("auth: got %d, want %d", code, exitAuth)
	}

	srv.Close()
	if code, _ := c.Get("/status", nil); code != exitNotRunning {
		t.Errorf("closed server: got %d, want %d", code, exitNotRunning)
	}
}

func TestExecuteBasicCommands(t *testing.T) {
	srv := newTestServer(t, "tok")

	e, out, _ := testEnv(srv, "tok")
	if code := execute([]string{"version"}, "9.9.9", e); code != exitOK || !strings.Contains(out.String(), "v9.9.9") {
		t.Errorf("version: code=%d out=%q", code, out.String())
	}

	e, out, _ = testEnv(srv, "tok")
	if code := execute(nil, "1", e); code != exitOK || !strings.Contains(out.String(), "Usage:") {
		t.Errorf("usage: code=%d out=%q", code, out.String())
	}

	e, out, _ = testEnv(srv, "tok")
	if code := execute([]string{"status"}, "1", e); code != exitOK || strings.TrimSpace(out.String()) != `{"state":"unlinked"}` {
		t.Errorf("status: code=%d out=%q", code, out.String())
	}

	e, out, _ = testEnv(srv, "tok")
	if code := execute([]string{"open"}, "1", e); code != exitOK || strings.TrimSpace(out.String()) != `{"ok":true}` {
		t.Errorf("open: code=%d out=%q", code, out.String())
	}

	e, _, errOut := testEnv(srv, "tok")
	if code := execute([]string{"bogus"}, "1", e); code != exitValidation || !strings.Contains(errOut.String(), "unknown command") {
		t.Errorf("unknown: code=%d err=%q", code, errOut.String())
	}
}

func TestExecuteClientFailureCodes(t *testing.T) {
	var out, errOut bytes.Buffer
	e := env{stdout: &out, stderr: &errOut, newClient: func() (*Client, int, error) {
		return nil, exitNotRunning, errors.New("not running")
	}}
	for _, args := range [][]string{{"status"}, {"open"}, {"send", "--level", "info", "--text", "x"}} {
		if code := execute(args, "1", e); code != exitNotRunning {
			t.Errorf("%v: got %d, want %d", args, code, exitNotRunning)
		}
	}
	if !strings.Contains(errOut.String(), `"error"`) {
		t.Errorf("expected JSON error on stderr, got %q", errOut.String())
	}
}

func TestExecuteSend(t *testing.T) {
	srv := newTestServer(t, "tok")
	dir := t.TempDir()
	file := filepath.Join(dir, "msg.txt")
	if err := os.WriteFile(file, append([]byte{0xEF, 0xBB, 0xBF}, arabicSample...), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("text file arabic survives", func(t *testing.T) {
		e, out, _ := testEnv(srv, "tok")
		code := execute([]string{"send", "--level", "info", "--text-file", file, "--to", "main"}, "1", e)
		if code != exitOK {
			t.Fatalf("code = %d", code)
		}
		var resp map[string]interface{}
		if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
			t.Fatalf("stdout is not one JSON line: %q", out.String())
		}
		if resp["echo"] != arabicSample || resp["status"] != "queued" {
			t.Errorf("unexpected response: %v", resp)
		}
	})

	failures := []struct {
		name string
		args []string
		want int
	}{
		{"missing level", []string{"send", "--text", "x"}, exitValidation},
		{"missing text", []string{"send", "--level", "info"}, exitValidation},
		{"unknown flag", []string{"send", "--nope"}, exitValidation},
		{"missing text file", []string{"send", "--level", "info", "--text-file", filepath.Join(dir, "none.txt")}, exitValidation},
		{"server validation", []string{"send", "--level", "invalid", "--text", "x"}, exitValidation},
		{"server rate limit", []string{"send", "--level", "limited", "--text", "x"}, exitThrottled},
		{"deduplicated", []string{"send", "--level", "dup", "--text", "x"}, exitThrottled},
	}
	for _, tc := range failures {
		t.Run(tc.name, func(t *testing.T) {
			e, _, _ := testEnv(srv, "tok")
			if code := execute(tc.args, "1", e); code != tc.want {
				t.Errorf("got %d, want %d", code, tc.want)
			}
		})
	}
}

func TestSendWaitFlag(t *testing.T) {
	var gotWait interface{}
	status := "sent"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]interface{}
		_ = json.NewDecoder(r.Body).Decode(&body)
		gotWait = body["wait"]
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "m1", "status": status})
	}))
	defer srv.Close()

	run := func(args ...string) int {
		e := env{stdout: &bytes.Buffer{}, stderr: &bytes.Buffer{}, newClient: func() (*Client, int, error) {
			return &Client{BaseURL: srv.URL + "/api/v1", Token: "t", HTTPClient: &http.Client{}}, exitOK, nil
		}}
		return execute(append([]string{"send", "--level", "info", "--text", "x"}, args...), "1", e)
	}

	if code := run("--wait", "20"); code != exitOK || gotWait != float64(20) {
		t.Errorf("sent: code=%d wait=%v", code, gotWait)
	}
	status = "delivered"
	if code := run("--wait", "5"); code != exitOK {
		t.Errorf("delivered: code=%d", code)
	}
	status = "queued"
	if code := run("--wait", "5"); code != exitStillQueued {
		t.Errorf("still queued after the wait must exit 7, got %d", code)
	}
	if code := run(); code != exitOK {
		t.Errorf("queued without --wait is a normal accept, got %d", code)
	}
	status = "failed"
	if code := run("--wait", "5"); code != exitServerError {
		t.Errorf("failed: got %d", code)
	}
	for _, bad := range []string{"-1", "61", "abc"} {
		if code := run("--wait", bad); code != exitValidation {
			t.Errorf("--wait %s: got %d, want validation error", bad, code)
		}
	}
}

func TestSendTitleAndFieldOptions(t *testing.T) {
	srv, calls := newRecordingServer(t, http.StatusOK, `{"id":"m1","status":"queued"}`)
	run := func(args ...string) int {
		e, _, _ := testEnv(srv, "tok")
		return execute(append([]string{"send", "--level", "info", "--text", "x"}, args...), "1", e)
	}

	if code := run("--title", "  Phase 3 done ", "--field", "Phase=3 of 5", "--field", "Note = a=b "); code != exitOK {
		t.Fatalf("code = %d", code)
	}
	body := (*calls)[0].body
	fields, _ := body["fields"].([]interface{})
	if body["title"] != "Phase 3 done" || len(fields) != 2 {
		t.Fatalf("unexpected body: %v", body)
	}
	if first := fields[0].(map[string]interface{}); first["key"] != "Phase" || first["value"] != "3 of 5" {
		t.Errorf("first field = %v", first)
	}
	if second := fields[1].(map[string]interface{}); second["key"] != "Note" || second["value"] != "a=b" {
		t.Errorf("only the first = splits the field: %v", second)
	}

	*calls = nil
	if code := run(); code != exitOK {
		t.Fatalf("plain send: %d", code)
	}
	if _, has := (*calls)[0].body["title"]; has {
		t.Errorf("no title is sent when none is given: %v", (*calls)[0].body)
	}
	if _, has := (*calls)[0].body["fields"]; has {
		t.Errorf("no fields are sent when none are given: %v", (*calls)[0].body)
	}

	nine := []string{}
	for i := 0; i < 9; i++ {
		nine = append(nine, "--field", "k=v")
	}
	for name, args := range map[string][]string{
		"title too long": {"--title", strings.Repeat("x", 121)},
		"no equals":      {"--field", "nokey"},
		"empty key":      {"--field", "=v"},
		"empty value":    {"--field", "k="},
		"key too long":   {"--field", strings.Repeat("k", 41) + "=v"},
		"value too long": {"--field", "k=" + strings.Repeat("v", 201)},
		"nine fields":    nine,
	} {
		*calls = nil
		if code := run(args...); code != exitValidation || len(*calls) != 0 {
			t.Errorf("%s: code=%d calls=%d, want exit 4 with no request", name, code, len(*calls))
		}
	}
	if code := run("--title", strings.Repeat("x", 120), "--field", strings.Repeat("k", 40)+"="+strings.Repeat("v", 200)); code != exitOK {
		t.Errorf("the limits themselves are allowed: %d", code)
	}
}
