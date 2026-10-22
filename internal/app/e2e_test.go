// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/autostart"
	"datacodex-notify/internal/config"
	"datacodex-notify/internal/wa"
)

// arabicSample is built from code points so the source file stays ASCII-only.
var arabicSample = string([]rune{
	0x0631, 0x0633, 0x0627, 0x0644, 0x0629, ' ',
	0x062a, 0x062c, 0x0631, 0x064a, 0x0628, 0x064a, 0x0629, ' ',
	0x0628, 0x0627, 0x0644, 0x0639, 0x0631, 0x0628, 0x064a, 0x0629,
})

const (
	greenCircle = '\U0001F7E2'
	skull       = '\U0001F480'
)

var cliBinary string

// TestMain builds the CLI once for every end-to-end test.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "dcn-e2e-cli-")
	if err != nil {
		panic(err)
	}
	cliBinary = filepath.Join(dir, "datacodex-notify-cli.exe")
	goExe := filepath.Join(runtime.GOROOT(), "bin", "go.exe")
	build := exec.Command(goExe, "build", "-tags", "devfake", "-o", cliBinary, "./cmd/datacodex-notify-cli")
	build.Dir = filepath.Join("..", "..")
	if output, err := build.CombinedOutput(); err != nil {
		os.RemoveAll(dir)
		panic("building the CLI failed: " + err.Error() + "\n" + string(output))
	}
	code := m.Run()
	os.RemoveAll(dir)
	os.Exit(code)
}

func runCLI(t *testing.T, args ...string) (string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	cmd := exec.Command(cliBinary, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	code := 0
	if exitErr, ok := err.(*exec.ExitError); ok {
		code = exitErr.ExitCode()
	} else if err != nil {
		t.Fatalf("running the CLI failed: %v", err)
	}
	return strings.TrimSpace(stdout.String()), code
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// memoryRegistry stands in for the Run key so tests never touch the real registry.
type memoryRegistry struct{ values map[string]string }

func (m *memoryRegistry) Get(name string) (string, bool, error) {
	v, ok := m.values[name]
	return v, ok, nil
}
func (m *memoryRegistry) Set(name, value string) error { m.values[name] = value; return nil }
func (m *memoryRegistry) Delete(name string) error     { delete(m.values, name); return nil }

type e2eApp struct {
	registry   *memoryRegistry
	cfg        *config.Config
	windowURLs chan string
	stop       context.CancelFunc
	done       chan error
}

// startApp runs the whole service in this process with the fake sender, in its own data folder.
func startApp(t *testing.T, tick time.Duration) *e2eApp {
	t.Helper()
	dataRoot := t.TempDir()
	t.Setenv("LOCALAPPDATA", dataRoot)

	cfg, err := config.LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig failed: %v", err)
	}

	a := &e2eApp{cfg: cfg, registry: &memoryRegistry{values: map[string]string{}}, windowURLs: make(chan string, 4), done: make(chan error, 1)}
	ctx, stop := context.WithCancel(context.Background())
	a.stop = stop
	t.Cleanup(func() {
		stop()
		select {
		case <-a.done:
		case <-time.After(15 * time.Second):
		}
	})

	go func() {
		a.done <- Run(ctx, Options{
			Version:           "e2e",
			InstanceName:      `Local\DatacodexNotifyE2E-` + filepath.Base(dataRoot),
			Headless:          true,
			HeartbeatTick:     tick,
			AutostartRegistry: a.registry,
			SkillRoots:        []string{filepath.Join(dataRoot, "claude-skills"), filepath.Join(dataRoot, "codex-skills")},
			OpenWindow:        func(u string) error { a.windowURLs <- u; return nil },
		})
	}()
	waitFor(t, "runtime.json", func() bool { _, err := cfg.ReadRuntime(); return err == nil })
	return a
}

// session opens the admin window through the CLI and exchanges the launch token for a UI session.
func (a *e2eApp) session(t *testing.T) (client *http.Client, base, csrf string) {
	t.Helper()
	if out, code := runCLI(t, "open"); code != 0 {
		t.Fatalf("open failed: code=%d out=%s", code, out)
	}
	var windowURL string
	select {
	case windowURL = <-a.windowURLs:
	case <-time.After(5 * time.Second):
		t.Fatal("the admin window was not requested")
	}
	parsed, err := url.Parse(windowURL)
	if err != nil || parsed.Query().Get("launch") == "" {
		t.Fatalf("window URL %q carries no launch token", windowURL)
	}

	jar, _ := cookiejar.New(nil)
	client = &http.Client{Jar: jar, Timeout: 5 * time.Second}
	base = parsed.Scheme + "://" + parsed.Host

	page, err := client.Get(base + "/")
	if err != nil || page.StatusCode != http.StatusOK {
		t.Fatalf("admin page not served: %v", err)
	}
	page.Body.Close()

	body, _ := json.Marshal(map[string]string{"launch_token": parsed.Query().Get("launch")})
	resp, err := client.Post(base+"/api/v1/ui/session", "application/json", bytes.NewReader(body))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("session exchange failed: %v", err)
	}
	defer resp.Body.Close()
	var parsedBody struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&parsedBody)
	return client, base, parsedBody.CSRF
}

func (a *e2eApp) addRecipient(t *testing.T, client *http.Client, base, csrf string) {
	t.Helper()
	body, _ := json.Marshal(map[string]interface{}{"name": "Admin", "phone_e164": "+201000000001", "levels": 7})
	req, _ := http.NewRequest(http.MethodPost, base+"/api/v1/ui/recipients", bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil || resp.StatusCode != http.StatusCreated {
		t.Fatalf("adding the recipient failed: %v", err)
	}
	resp.Body.Close()
}

// sent returns what the fake sender has recorded so far.
func (a *e2eApp) sent() []wa.FakeRecord {
	file, err := os.Open(filepath.Join(a.cfg.DataDir, FakeLogName))
	if err != nil {
		return nil
	}
	defer file.Close()

	var records []wa.FakeRecord
	scanner := bufio.NewScanner(file)
	scanner.Buffer(nil, 1<<20)
	for scanner.Scan() {
		var r wa.FakeRecord
		if json.Unmarshal(scanner.Bytes(), &r) == nil {
			records = append(records, r)
		}
	}
	return records
}

func (a *e2eApp) waitForRecords(t *testing.T, count int) []wa.FakeRecord {
	t.Helper()
	var records []wa.FakeRecord
	waitFor(t, "the fake sender records", func() bool {
		records = a.sent()
		return len(records) >= count
	})
	return records
}

func TestEndToEndSendThroughCLIWithFakeSender(t *testing.T) {
	a := startApp(t, 0)
	client, base, csrf := a.session(t)
	a.addRecipient(t, client, base, csrf)

	// Send Arabic text from a UTF-8 file (no BOM) through the CLI.
	textFile := filepath.Join(t.TempDir(), "message.txt")
	if err := os.WriteFile(textFile, []byte(arabicSample), 0o644); err != nil {
		t.Fatal(err)
	}
	out, code := runCLI(t, "send", "--level", "info", "--source", "claude:e2e", "--text-file", textFile)
	if code != 0 {
		t.Fatalf("send failed: code=%d out=%s", code, out)
	}
	var queued struct{ ID, Status string }
	if err := json.Unmarshal([]byte(out), &queued); err != nil || queued.ID == "" || queued.Status != "queued" {
		t.Fatalf("unexpected send output %q (err=%v)", out, err)
	}

	record := a.waitForRecords(t, 1)[0]
	if !strings.HasPrefix(record.Text, string(greenCircle)) {
		t.Errorf("message does not start with the info marker: %q", record.Text)
	}
	if !strings.Contains(record.Text, "\n"+arabicSample+"\n") {
		t.Errorf("Arabic text did not survive intact: %q", record.Text)
	}
	if record.Recipient != "201000000001@s.whatsapp.net" || record.Kind != "text" {
		t.Errorf("unexpected delivery: %+v", record)
	}

	// The same text again is a duplicate: reported with exit code 5 and never sent.
	out, code = runCLI(t, "send", "--level", "info", "--source", "claude:e2e", "--text-file", textFile)
	if code != 5 || !strings.Contains(out, `"status":"deduplicated"`) {
		t.Errorf("duplicate: code=%d out=%s, want exit 5 and status deduplicated", code, out)
	}

	if status, code := runCLI(t, "status"); code != 0 || !strings.Contains(status, `"state":"connected"`) {
		t.Errorf("status: code=%d out=%s", code, status)
	}

	// A clean exit removes runtime.json, so the CLI reports "not running".
	a.stop()
	select {
	case err := <-a.done:
		if err != nil {
			t.Fatalf("Run returned an error: %v", err)
		}
	case <-time.After(15 * time.Second):
		t.Fatal("the app did not stop")
	}
	if _, code := runCLI(t, "status"); code != 2 {
		t.Errorf("status after exit: code=%d, want 2", code)
	}
	if got := len(a.sent()); got != 1 {
		t.Errorf("the fake sender recorded %d messages, want 1", got)
	}
}

func TestEndToEndHeartbeatAlertAndRecovery(t *testing.T) {
	a := startApp(t, 100*time.Millisecond)
	client, base, csrf := a.session(t)
	a.addRecipient(t, client, base, csrf)

	out, code := runCLI(t, "heartbeat", "--id", "run-1", "--every", "1", "--grace", "0", "--label", "Plan 25", "--source", "claude:e2e")
	if code != 0 || !strings.Contains(out, `"status":"active"`) {
		t.Fatalf("heartbeat failed: code=%d out=%s", code, out)
	}

	// No beat for longer than the interval: one critical alert with the skull marker.
	lost := a.waitForRecords(t, 1)[0]
	if !strings.HasPrefix(lost.Text, string(skull)) || !strings.Contains(lost.Text, "Plan 25") || !strings.Contains(lost.Text, "claude:e2e") {
		t.Errorf("unexpected alert: %q", lost.Text)
	}
	time.Sleep(600 * time.Millisecond)
	if got := len(a.sent()); got != 1 {
		t.Fatalf("an outage must be announced once, saw %d messages", got)
	}

	// A new beat ends the outage with one "back" message.
	out, code = runCLI(t, "heartbeat", "--id", "run-1")
	if code != 0 || !strings.Contains(out, `"recovered":true`) {
		t.Fatalf("recovery beat: code=%d out=%s", code, out)
	}
	back := a.waitForRecords(t, 2)[1]
	if !strings.HasPrefix(back.Text, string(greenCircle)) || !strings.Contains(back.Text, "Plan 25") {
		t.Errorf("unexpected recovery message: %q", back.Text)
	}

	// The Rules tab sees the heartbeat, and the startup switch writes to the (fake) per-user entry.
	rulesResp, err := client.Get(base + "/api/v1/ui/rules")
	if err != nil || rulesResp.StatusCode != http.StatusOK {
		t.Fatalf("GET rules failed: %v", err)
	}
	var rulesBody struct {
		Heartbeats []struct{ Label, State string } `json:"heartbeats"`
	}
	_ = json.NewDecoder(rulesResp.Body).Decode(&rulesBody)
	rulesResp.Body.Close()
	if len(rulesBody.Heartbeats) != 1 || rulesBody.Heartbeats[0].Label != "Plan 25" {
		t.Errorf("rules payload heartbeats: %+v", rulesBody.Heartbeats)
	}

	enable, _ := json.Marshal(map[string]bool{"enabled": true})
	req, _ := http.NewRequest(http.MethodPut, base+"/api/v1/ui/autostart", bytes.NewReader(enable))
	req.Header.Set("X-CSRF-Token", csrf)
	if resp, err := client.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("enabling autostart failed: %v", err)
	}
	if value := a.registry.values[autostart.ValueName]; !strings.HasPrefix(value, `"`) || !strings.HasSuffix(value, `.exe"`) && !strings.HasSuffix(value, `.test"`) {
		t.Errorf("startup entry = %q, want a quoted executable path", value)
	}
	disable, _ := json.Marshal(map[string]bool{"enabled": false})
	req, _ = http.NewRequest(http.MethodPut, base+"/api/v1/ui/autostart", bytes.NewReader(disable))
	req.Header.Set("X-CSRF-Token", csrf)
	if resp, err := client.Do(req); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("disabling autostart failed: %v", err)
	}
	if len(a.registry.values) != 0 {
		t.Errorf("the startup entry must be removed: %v", a.registry.values)
	}

	// Stopping removes it from monitoring: no more alerts, however long it stays silent.
	if out, code := runCLI(t, "heartbeat-stop", "--id", "run-1"); code != 0 || !strings.Contains(out, `"found":true`) {
		t.Fatalf("stop: code=%d out=%s", code, out)
	}
	time.Sleep(1500 * time.Millisecond)
	if got := len(a.sent()); got != 2 {
		t.Errorf("a stopped heartbeat must stay silent, saw %d messages", got)
	}
}

// uiCall sends a request through the admin session and returns the status and body.
func uiCall(t *testing.T, client *http.Client, base, csrf, method, path string, payload interface{}) (int, []byte) {
	t.Helper()
	var body []byte
	if payload != nil {
		body, _ = json.Marshal(payload)
	}
	req, _ := http.NewRequest(method, base+path, bytes.NewReader(body))
	req.Header.Set("X-CSRF-Token", csrf)
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("%s %s failed: %v", method, path, err)
	}
	defer resp.Body.Close()
	var buf bytes.Buffer
	_, _ = buf.ReadFrom(resp.Body)
	return resp.StatusCode, buf.Bytes()
}

func TestEndToEndAgentSessionsMuteAndDisconnect(t *testing.T) {
	a := startApp(t, 100*time.Millisecond)
	client, base, csrf := a.session(t)
	a.addRecipient(t, client, base, csrf)

	rulesFile := filepath.Join(t.TempDir(), "CLAUDE.md")
	out, code := runCLI(t, "connect", "--source", "claude-code", "--session", "e2e-1", "--rules-file", rulesFile)
	if code != 0 || !strings.Contains(out, `"success":true`) {
		t.Fatalf("connect: code=%d out=%s", code, out)
	}
	time.Sleep(500 * time.Millisecond)
	if got := len(a.sent()); got != 0 {
		t.Fatalf("connect must not send a WhatsApp message by default, got %d", got)
	}

	// The registry shows the session with its rules file.
	status, body := uiCall(t, client, base, csrf, http.MethodGet, "/api/v1/ui/agents", nil)
	var listed struct {
		Sessions []struct {
			ID         string   `json:"id"`
			RulesFiles []string `json:"rules_files"`
			Status     string   `json:"status"`
		} `json:"sessions"`
	}
	_ = json.Unmarshal(body, &listed)
	if status != http.StatusOK || len(listed.Sessions) != 1 || listed.Sessions[0].RulesFiles[0] != rulesFile || listed.Sessions[0].Status != "connected" {
		t.Fatalf("session list: %d %s", status, body)
	}
	id := listed.Sessions[0].ID

	// A named send goes out; after muting the session it is refused with exit code 6.
	out, code = runCLI(t, "send", "--level", "info", "--text", "first", "--session", "e2e-1", "--wait", "20")
	if code != 0 || (!strings.Contains(out, `"status":"sent"`) && !strings.Contains(out, `"status":"delivered"`)) {
		t.Fatalf("send --wait must return the final status: code=%d out=%s", code, out)
	}
	a.waitForRecords(t, 1)
	if _, code := runCLI(t, "heartbeat", "--id", "hb-1", "--every", "600", "--session", "e2e-1"); code != 0 {
		t.Fatalf("heartbeat failed with %d", code)
	}

	if status, _ := uiCall(t, client, base, csrf, http.MethodPost, "/api/v1/ui/agents/sessions/"+id+"/mute", map[string]bool{"muted": true}); status != http.StatusOK {
		t.Fatalf("mute failed: %d", status)
	}
	if _, code := runCLI(t, "send", "--level", "info", "--text", "second", "--session", "e2e-1"); code != 6 {
		t.Errorf("a muted session's send: exit %d, want 6", code)
	}
	if _, code := runCLI(t, "heartbeat", "--id", "hb-1", "--session", "e2e-1"); code != 6 {
		t.Errorf("a muted session's heartbeat: exit %d, want 6", code)
	}
	if out, code := runCLI(t, "send", "--level", "info", "--text", "anonymous"); code != 0 {
		t.Errorf("a send without a session is not muted: code=%d out=%s", code, out)
	}
	a.waitForRecords(t, 2)

	// The global mute refuses everyone, then lets them through again.
	uiCall(t, client, base, csrf, http.MethodPost, "/api/v1/ui/agents/mute-all", map[string]bool{"muted": true})
	if _, code := runCLI(t, "send", "--level", "critical", "--text", "nope"); code != 6 {
		t.Errorf("the global mute: exit %d, want 6", code)
	}
	uiCall(t, client, base, csrf, http.MethodPost, "/api/v1/ui/agents/mute-all", map[string]bool{"muted": false})

	// Disconnecting a muted session is quiet: no new message, and no heartbeat alert ever follows.
	before := len(a.sent())
	if out, code := runCLI(t, "disconnect", "--session", "e2e-1"); code != 0 || !strings.Contains(out, `"found":true`) {
		t.Fatalf("disconnect: code=%d out=%s", code, out)
	}
	time.Sleep(1500 * time.Millisecond)
	if got := len(a.sent()); got != before {
		t.Errorf("a muted session must disconnect quietly and never raise a heartbeat alert (%d -> %d messages)", before, got)
	}
}
