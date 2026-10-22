// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"datacodex-notify/internal/heartbeat"
	"datacodex-notify/internal/store"
)

func withHeartbeats(cfg *Config) {
	cfg.Heartbeats = heartbeat.New(cfg.Store, cfg.NotifySvc, cfg.Locale, func(context.Context) (int, int) { return 600, 300 })
}

func agentRequest(t *testing.T, srv *Server, token, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := newTestRequest(method, path, strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	return rr
}

func TestAgentHeartbeatRegisterBeatAndStop(t *testing.T) {
	srv, s, _, token := setupTestServer(t, withHeartbeats)
	defer s.Close()

	rr := agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", `{"label":"Plan 25","source":"claude:core","every_s":120,"grace_s":30}`)
	if rr.Code != http.StatusOK {
		t.Fatalf("register: %d %s", rr.Code, rr.Body.String())
	}
	var created struct {
		Heartbeat heartbeatJSON `json:"heartbeat"`
		Recovered bool          `json:"recovered"`
		Status    string        `json:"status"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Heartbeat.ID != "run-1" || created.Heartbeat.IntervalS != 120 || created.Heartbeat.GraceS != 30 ||
		created.Heartbeat.Label != "Plan 25" || created.Status != "active" || created.Recovered {
		t.Errorf("unexpected response: %+v", created)
	}

	// An empty body is a plain beat and keeps the stored values.
	rr = agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", "")
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if rr.Code != http.StatusOK || created.Heartbeat.IntervalS != 120 || created.Heartbeat.Label != "Plan 25" {
		t.Errorf("plain beat: %d %+v", rr.Code, created)
	}

	// Defaults apply to a new heartbeat without values.
	rr = agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-2", `{}`)
	_ = json.Unmarshal(rr.Body.Bytes(), &created)
	if created.Heartbeat.IntervalS != 600 || created.Heartbeat.GraceS != 300 {
		t.Errorf("defaults not applied: %+v", created.Heartbeat)
	}

	rr = agentRequest(t, srv, token, http.MethodDelete, "/api/v1/heartbeats/run-1", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"found":true`) {
		t.Errorf("stop: %d %s", rr.Code, rr.Body.String())
	}
	rr = agentRequest(t, srv, token, http.MethodDelete, "/api/v1/heartbeats/run-1", "")
	if rr.Code != http.StatusOK || !strings.Contains(rr.Body.String(), `"found":false`) {
		t.Errorf("stopping twice must be harmless: %d %s", rr.Code, rr.Body.String())
	}
}

func TestAgentHeartbeatValidationAndAuth(t *testing.T) {
	srv, s, _, token := setupTestServer(t, withHeartbeats)
	defer s.Close()

	for name, tc := range map[string]struct{ path, body string }{
		"bad id":      {"/api/v1/heartbeats/has%20space", `{}`},
		"negative":    {"/api/v1/heartbeats/x", `{"every_s":-5}`},
		"too long":    {"/api/v1/heartbeats/x", `{"every_s":999999}`},
		"bad json":    {"/api/v1/heartbeats/x", `{nope`},
		"missing id":  {"/api/v1/heartbeats/", `{}`},
		"nested path": {"/api/v1/heartbeats/a/b", `{}`},
	} {
		if rr := agentRequest(t, srv, token, http.MethodPost, tc.path, tc.body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rr.Code)
		}
	}
	if rr := agentRequest(t, srv, token, http.MethodGet, "/api/v1/heartbeats/x", ""); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET: %d, want 405", rr.Code)
	}

	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, newTestRequest(http.MethodPost, "/api/v1/heartbeats/x", strings.NewReader(`{}`)))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no token: %d, want 401", rr.Code)
	}
	if rr := agentRequest(t, srv, "wrong", http.MethodPost, "/api/v1/heartbeats/x", `{}`); rr.Code != http.StatusUnauthorized {
		t.Errorf("wrong token: %d, want 401", rr.Code)
	}
}

func TestHeartbeatRoutesAnswer503WhenNotConfigured(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	if rr := agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/x", `{}`); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}

func TestRecoveryThroughTheAPIQueuesABackMessage(t *testing.T) {
	srv, s, _, token := setupTestServer(t, withHeartbeats)
	defer s.Close()
	ctx := context.Background()
	if _, err := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}

	agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", `{"every_s":60,"grace_s":0,"label":"Nightly"}`)
	if err := s.UpdateHeartbeatState(ctx, "run-1", heartbeat.StateAlerted); err != nil {
		t.Fatal(err)
	}

	rr := agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", `{}`)
	if !strings.Contains(rr.Body.String(), `"recovered":true`) {
		t.Fatalf("recovery not reported: %s", rr.Body.String())
	}
	messages, _ := s.ListRecentMessages(ctx, 10, "", "")
	if len(messages) != 1 || messages[0].Level != store.LevelInfo || !strings.Contains(messages[0].Text, "Nightly") {
		t.Errorf("expected one info message about the recovery: %+v", messages)
	}
}

func TestUIHeartbeatListAndStop(t *testing.T) {
	srv, s, _, token := setupTestServer(t, withHeartbeats)
	defer s.Close()
	ui := newUIClient(t, srv)

	agentRequest(t, srv, token, http.MethodPost, "/api/v1/heartbeats/run-1", `{"every_s":60,"grace_s":0,"label":"Nightly","source":"claude:core"}`)

	rr := ui.do(http.MethodGet, "/api/v1/ui/heartbeats", nil)
	var list []heartbeatJSON
	_ = json.Unmarshal(rr.Body.Bytes(), &list)
	if rr.Code != http.StatusOK || len(list) != 1 || list[0].Label != "Nightly" || list[0].Source != "claude:core" || list[0].State != "active" {
		t.Fatalf("list: %d %s", rr.Code, rr.Body.String())
	}

	if rr := ui.do(http.MethodDelete, "/api/v1/ui/heartbeats/run-1", nil); rr.Code != http.StatusOK {
		t.Fatalf("stop: %d", rr.Code)
	}
	rr = ui.do(http.MethodGet, "/api/v1/ui/heartbeats", nil)
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("stopped heartbeat still listed: %s", rr.Body.String())
	}
	if rr := ui.do(http.MethodDelete, "/api/v1/ui/heartbeats/", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("missing id: %d, want 400", rr.Code)
	}
	if rr := ui.do(http.MethodPost, "/api/v1/ui/heartbeats", nil); rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("POST: %d, want 405", rr.Code)
	}
}
