// SPDX-License-Identifier: GPL-3.0-only

package ui

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strings"
	"testing"

	"datacodex-notify/web"
)

func TestHandlerServesIndexWithSecurityHeaders(t *testing.T) {
	rr := httptest.NewRecorder()
	Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "DataCodex Notify") {
		t.Error("index.html not served")
	}
	for _, header := range []string{"Content-Security-Policy", "X-Content-Type-Options", "Referrer-Policy", "Cache-Control"} {
		if rr.Header().Get(header) == "" {
			t.Errorf("missing header %s", header)
		}
	}
}

func TestHandlerServesModuleScriptsAndFonts(t *testing.T) {
	for _, tc := range []struct{ path, mime string }{
		{"/js/app.js", "javascript"},
		{"/app.css", "text/css"},
		{"/i18n/ar.json", "json"},
		{"/fonts/ibm-plex-sans-arabic-arabic-400.woff2", "font/woff2"},
	} {
		rr := httptest.NewRecorder()
		Handler().ServeHTTP(rr, httptest.NewRequest(http.MethodGet, tc.path, nil))
		if rr.Code != http.StatusOK || !strings.Contains(rr.Header().Get("Content-Type"), tc.mime) {
			t.Errorf("%s: status=%d type=%q", tc.path, rr.Code, rr.Header().Get("Content-Type"))
		}
	}
}

func TestCenteredPosition(t *testing.T) {
	x, y := centeredPosition(1920, 1080)
	if x != 730 || y != 200 {
		t.Errorf("got (%d,%d), want (730,200)", x, y)
	}
	x, y = centeredPosition(300, 300)
	if x != 0 || y != 0 {
		t.Errorf("small screen got (%d,%d), want (0,0)", x, y)
	}
}

func TestEdgeArgs(t *testing.T) {
	args := strings.Join(edgeArgs("http://127.0.0.1:1/", `C:\profile`, 10, 20), " ")
	for _, want := range []string{
		"--app=http://127.0.0.1:1/",
		"--window-size=460,680",
		"--window-position=10,20",
		`--user-data-dir=C:\profile`,
		"--no-first-run",
	} {
		if !strings.Contains(args, want) {
			t.Errorf("args %q missing %q", args, want)
		}
	}
}

func TestLauncherIsNotOpenInitially(t *testing.T) {
	if NewLauncher(t.TempDir()).IsOpen() {
		t.Error("new launcher must not report an open window")
	}
}

func readFile(t *testing.T, name string) []byte {
	t.Helper()
	data, err := fs.ReadFile(web.FS, name)
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return data
}

func flatten(prefix string, node interface{}, out map[string]bool) {
	if m, ok := node.(map[string]interface{}); ok {
		for key, value := range m {
			flatten(path.Join(prefix, key), value, out)
		}
		return
	}
	out[strings.ReplaceAll(prefix, "/", ".")] = true
}

func bundleKeys(t *testing.T, lang string) map[string]bool {
	t.Helper()
	var tree map[string]interface{}
	if err := json.Unmarshal(readFile(t, "i18n/"+lang+".json"), &tree); err != nil {
		t.Fatalf("parse %s: %v", lang, err)
	}
	keys := map[string]bool{}
	flatten("", tree, keys)
	delete(keys, "")
	return keys
}

func TestI18nBundlesHaveIdenticalKeys(t *testing.T) {
	ar, en := bundleKeys(t, "ar"), bundleKeys(t, "en")
	for key := range en {
		if !ar[key] {
			t.Errorf("key %q missing in ar.json", key)
		}
	}
	for key := range ar {
		if !en[key] {
			t.Errorf("key %q missing in en.json", key)
		}
	}
}

func TestEveryReferencedI18nKeyExists(t *testing.T) {
	en := bundleKeys(t, "en")
	literal := regexp.MustCompile(`(?:^|[^A-Za-z_.])t\(\s*["']([a-z_]+(?:\.[a-z_]+)+)["']`)
	attr := regexp.MustCompile(`data-i18n="([a-z_.]+)"`)

	check := func(file string, re *regexp.Regexp) {
		for _, m := range re.FindAllStringSubmatch(string(readFile(t, file)), -1) {
			if !en[m[1]] {
				t.Errorf("%s references unknown key %q", file, m[1])
			}
		}
	}
	jsFiles, _ := fs.Glob(web.FS, "js/*.js")
	for _, file := range jsFiles {
		check(file, literal)
	}
	check("index.html", attr)

	for _, prefix := range []string{"levels.info", "levels.warning", "levels.critical",
		"status.queued", "status.sending", "status.sent", "status.delivered", "status.read", "status.failed",
		"state.unlinked", "state.connecting", "state.connected", "state.reconnecting", "state.logged_out", "state.outdated",
		"connection.step1", "connection.step2", "connection.step3",
		"language.switch_ar", "language.switch_en",
		"voice.mode_text", "voice.mode_voice", "voice.mode_both", "voice.speed_normal", "voice.speed_fast", "voice.speed_very_fast",
		"rules.hb_state_active", "rules.hb_state_alerted",
		"agents.agent_claude-code", "agents.agent_codex", "agents.agent_gemini", "agents.agent_other", "agents.scope_global", "agents.scope_project", "agents.template_calm", "agents.template_normal", "agents.template_detailed", "agents.language_egyptian", "agents.language_arabic", "agents.language_english", "agents.activation_on_request", "agents.activation_suggest_long", "agents.activation_always", "agents.event_task_start", "agents.event_task_end", "agents.event_failure", "agents.event_blocked", "agents.event_critical_stop", "agents.event_phase_finished", "agents.event_implementer_stopped", "agents.event_step_milestone", "agents.status_connected", "agents.status_disconnected", "agents.status_muted"} {
		if !en[prefix] {
			t.Errorf("dynamic key %q missing", prefix)
		}
	}
}

func TestCodeAssetsContainNoNonASCII(t *testing.T) {
	var files []string
	for _, pattern := range []string{"*.html", "*.css", "js/*.js"} {
		matches, _ := fs.Glob(web.FS, pattern)
		files = append(files, matches...)
	}
	for _, file := range files {
		for i, b := range readFile(t, file) {
			if b > 127 {
				t.Errorf("%s has a non-ASCII byte at offset %d", file, i)
				break
			}
		}
	}
}
