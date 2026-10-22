// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"datacodex-notify/internal/store"
)

type fakeAutostart struct {
	enabled bool
	fail    error
}

func (f *fakeAutostart) Enabled() (bool, error) { return f.enabled, nil }

func (f *fakeAutostart) Set(enabled bool) error {
	if f.fail != nil {
		return f.fail
	}
	f.enabled = enabled
	return nil
}

type rulesPayload struct {
	Settings  rulesSettingsJSON `json:"settings"`
	Pause     pauseJSON         `json:"pause"`
	Autostart struct {
		Available bool `json:"available"`
		Enabled   bool `json:"enabled"`
	} `json:"autostart"`
	Heartbeats []heartbeatJSON `json:"heartbeats"`
}

func getRules(t *testing.T, ui *uiClient) rulesPayload {
	t.Helper()
	rr := ui.do(http.MethodGet, "/api/v1/ui/rules", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("GET rules: %d %s", rr.Code, rr.Body.String())
	}
	var payload rulesPayload
	if err := json.Unmarshal(rr.Body.Bytes(), &payload); err != nil {
		t.Fatalf("bad rules payload: %v", err)
	}
	return payload
}

func TestRulesDefaultsAndSave(t *testing.T) {
	srv, s, _, token := setupTestServer(t, withHeartbeats, func(cfg *Config) { cfg.Autostart = &fakeAutostart{} })
	defer s.Close()
	ui := newUIClient(t, srv)

	payload := getRules(t, ui)
	want := rulesSettingsJSON{QuietStart: "22:00", QuietEnd: "07:00", DedupeMinutes: 10, RateLimitPerHour: 20, HeartbeatIntervalS: 600, HeartbeatGraceS: 300}
	if payload.Settings != want || payload.Pause.Paused || !payload.Autostart.Available || payload.Autostart.Enabled || len(payload.Heartbeats) != 0 {
		t.Fatalf("unexpected defaults: %+v", payload)
	}

	agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", `{"every_s":60,"grace_s":0,"label":"Nightly"}`)
	update := rulesSettingsJSON{QuietEnabled: true, QuietStart: "23:30", QuietEnd: "06:15", DedupeMinutes: 3, RateLimitPerHour: 7, HeartbeatIntervalS: 90, HeartbeatGraceS: 10}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/rules", update); rr.Code != http.StatusOK {
		t.Fatalf("PUT rules: %d %s", rr.Code, rr.Body.String())
	}
	payload = getRules(t, ui)
	if payload.Settings != update {
		t.Errorf("saved settings differ: %+v", payload.Settings)
	}
	if len(payload.Heartbeats) != 1 || payload.Heartbeats[0].Label != "Nightly" {
		t.Errorf("heartbeats missing from the payload: %+v", payload.Heartbeats)
	}
}

func TestRulesSaveValidation(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)
	good := rulesSettingsJSON{QuietStart: "22:00", QuietEnd: "07:00", DedupeMinutes: 10, RateLimitPerHour: 20, HeartbeatIntervalS: 600, HeartbeatGraceS: 300}

	mutations := map[string]func(*rulesSettingsJSON){
		"bad start":     func(r *rulesSettingsJSON) { r.QuietStart = "25:00" },
		"bad end":       func(r *rulesSettingsJSON) { r.QuietEnd = "7am" },
		"negative":      func(r *rulesSettingsJSON) { r.DedupeMinutes = -1 },
		"huge limit":    func(r *rulesSettingsJSON) { r.RateLimitPerHour = 100000 },
		"zero interval": func(r *rulesSettingsJSON) { r.HeartbeatIntervalS = 0 },
		"negative gr.":  func(r *rulesSettingsJSON) { r.HeartbeatGraceS = -1 },
	}
	for name, mutate := range mutations {
		bad := good
		mutate(&bad)
		if rr := ui.do(http.MethodPut, "/api/v1/ui/rules", bad); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rr.Code)
		}
	}
	if payload := getRules(t, ui); payload.Settings != good {
		t.Errorf("a rejected save must not change anything: %+v", payload.Settings)
	}
	if rr := ui.do(http.MethodPost, "/api/v1/ui/rules", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", rr.Code)
	}
}

func TestPauseAndResumeThroughTheAPI(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)
	ctx := context.Background()
	if _, err := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}

	rr := ui.do(http.MethodPost, "/api/v1/ui/pause", map[string]int{"minutes": 60})
	var pause pauseJSON
	_ = json.Unmarshal(rr.Body.Bytes(), &pause)
	if rr.Code != http.StatusOK || !pause.Paused || pause.Indefinite || pause.Until == nil {
		t.Fatalf("timed pause: %d %s", rr.Code, rr.Body.String())
	}

	held := agentRequest(t, srv, token, http.MethodPost, "/api/v1/notify", `{"level":"info","text":"while paused"}`)
	if !strings.Contains(held.Body.String(), `"status":"held_paused"`) {
		t.Fatalf("a paused message must report held_paused: %s", held.Body.String())
	}
	urgent := agentRequest(t, srv, token, http.MethodPost, "/api/v1/notify", `{"level":"critical","text":"urgent while paused"}`)
	if !strings.Contains(urgent.Body.String(), `"status":"queued"`) {
		t.Errorf("critical must pass a pause: %s", urgent.Body.String())
	}

	rr = ui.do(http.MethodPost, "/api/v1/ui/pause", map[string]int{"minutes": 0})
	_ = json.Unmarshal(rr.Body.Bytes(), &pause)
	if rr.Code != http.StatusOK || !pause.Paused || !pause.Indefinite {
		t.Fatalf("indefinite pause: %d %s", rr.Code, rr.Body.String())
	}

	rr = ui.do(http.MethodDelete, "/api/v1/ui/pause", nil)
	_ = json.Unmarshal(rr.Body.Bytes(), &pause)
	if rr.Code != http.StatusOK || pause.Paused {
		t.Fatalf("resume: %d %s", rr.Code, rr.Body.String())
	}
	waiting, _ := s.ListMessagesByStatus(ctx, store.StatusHeldPaused)
	if len(waiting) != 0 {
		t.Errorf("resuming must release the held messages, %d still held", len(waiting))
	}

	for _, minutes := range []int{-1, maxPauseMinutes + 1} {
		if rr := ui.do(http.MethodPost, "/api/v1/ui/pause", map[string]int{"minutes": minutes}); rr.Code != http.StatusBadRequest {
			t.Errorf("%d minutes: got %d, want 400", minutes, rr.Code)
		}
	}
}

func TestAutostartEndpoint(t *testing.T) {
	fake := &fakeAutostart{}
	srv, s, _, _ := setupTestServer(t, func(cfg *Config) { cfg.Autostart = fake })
	defer s.Close()
	ui := newUIClient(t, srv)

	if rr := ui.do(http.MethodPut, "/api/v1/ui/autostart", map[string]bool{"enabled": true}); rr.Code != http.StatusOK || !fake.enabled {
		t.Fatalf("enable: %d enabled=%v", rr.Code, fake.enabled)
	}
	if !getRules(t, ui).Autostart.Enabled {
		t.Error("the rules payload must show autostart as enabled")
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/autostart", map[string]bool{"enabled": false}); rr.Code != http.StatusOK || fake.enabled {
		t.Fatalf("disable: %d enabled=%v", rr.Code, fake.enabled)
	}

	fake.fail = errors.New("registry denied")
	rr := ui.do(http.MethodPut, "/api/v1/ui/autostart", map[string]bool{"enabled": true})
	if rr.Code != http.StatusInternalServerError || strings.Contains(rr.Body.String(), "registry denied") {
		t.Errorf("a registry failure is a 500 without internal details: %d %s", rr.Code, rr.Body.String())
	}
	if rr := ui.do(http.MethodGet, "/api/v1/ui/autostart", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", rr.Code)
	}
}

func TestAutostartIsUnavailableWithoutAManager(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)

	if rr := ui.do(http.MethodPut, "/api/v1/ui/autostart", map[string]bool{"enabled": true}); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
	if payload := getRules(t, ui); payload.Autostart.Available {
		t.Error("autostart must be reported as unavailable")
	}
}

func TestRulesEndpointsRequireASession(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	for _, path := range []string{"/api/v1/ui/rules", "/api/v1/ui/pause", "/api/v1/ui/autostart"} {
		rr := agentRequest(t, srv, "", http.MethodGet, path, "")
		if rr.Code != http.StatusUnauthorized {
			t.Errorf("%s without a session: %d, want 401", path, rr.Code)
		}
	}
}
