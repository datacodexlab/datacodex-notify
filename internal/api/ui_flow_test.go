// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/store"
)

type uiClient struct {
	t      *testing.T
	srv    *Server
	cookie *http.Cookie
	csrf   string
}

func newUIClient(t *testing.T, srv *Server) *uiClient {
	t.Helper()
	token, err := srv.GenerateLaunchToken()
	if err != nil {
		t.Fatalf("GenerateLaunchToken failed: %v", err)
	}
	body, _ := json.Marshal(map[string]string{"launch_token": token})
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, newTestRequest(http.MethodPost, "/api/v1/ui/session", bytes.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("session exchange failed: %d", rr.Code)
	}
	var resp struct {
		CSRF string `json:"csrf_token"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	c := &uiClient{t: t, srv: srv, csrf: resp.CSRF}
	for _, cookie := range rr.Result().Cookies() {
		if cookie.Name == "dcn_session" {
			c.cookie = cookie
		}
	}
	if c.cookie == nil {
		t.Fatal("missing session cookie")
	}
	return c
}

func (c *uiClient) do(method, path string, payload interface{}) *httptest.ResponseRecorder {
	c.t.Helper()
	var body *bytes.Reader
	if payload != nil {
		data, _ := json.Marshal(payload)
		body = bytes.NewReader(data)
	} else {
		body = bytes.NewReader(nil)
	}
	req := newTestRequest(method, path, body)
	req.AddCookie(c.cookie)
	req.Header.Set("X-CSRF-Token", c.csrf)
	rr := httptest.NewRecorder()
	c.srv.httpServer.Handler.ServeHTTP(rr, req)
	return rr
}

func TestUIRecipientCRUDAndTestMessageDelivery(t *testing.T) {
	srv, s, fake, _ := setupTestServer(t)
	defer s.Close()
	srv.dispatcher.Start()
	defer srv.dispatcher.Stop()
	ui := newUIClient(t, srv)

	rr := ui.do(http.MethodPost, "/api/v1/ui/recipients", map[string]interface{}{"name": "Admin", "phone_e164": "+201000000001", "levels": 7})
	if rr.Code != http.StatusCreated {
		t.Fatalf("create recipient: %d %s", rr.Code, rr.Body.String())
	}
	var created store.Recipient
	_ = json.Unmarshal(rr.Body.Bytes(), &created)

	if rr = ui.do(http.MethodPost, "/api/v1/ui/recipients", map[string]interface{}{"name": "Bad", "phone_e164": "0101"}); rr.Code != http.StatusBadRequest {
		t.Errorf("invalid phone should be rejected, got %d", rr.Code)
	}

	rr = ui.do(http.MethodPut, "/api/v1/ui/recipients/"+created.ID, map[string]interface{}{"name": "Admin 2", "phone_e164": "+201000000001", "enabled": true, "levels": 3})
	if rr.Code != http.StatusOK {
		t.Fatalf("update recipient: %d", rr.Code)
	}

	if rr = ui.do(http.MethodPost, "/api/v1/ui/test", nil); rr.Code != http.StatusOK {
		t.Fatalf("send test: %d %s", rr.Code, rr.Body.String())
	}

	deadline := time.Now().Add(5 * time.Second)
	for len(fake.GetRecords()) == 0 && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	records := fake.GetRecords()
	if len(records) != 1 {
		t.Fatalf("expected the test message to reach the fake sender, got %d records", len(records))
	}
	if !strings.Contains(records[0].Text, "admin-ui") || records[0].Recipient != "201000000001@s.whatsapp.net" {
		t.Errorf("unexpected delivery: %+v", records[0])
	}

	if rr = ui.do(http.MethodDelete, "/api/v1/ui/recipients/"+created.ID, nil); rr.Code != http.StatusOK {
		t.Fatalf("delete recipient: %d", rr.Code)
	}
	rr = ui.do(http.MethodGet, "/api/v1/ui/recipients", nil)
	if strings.TrimSpace(rr.Body.String()) != "[]" {
		t.Errorf("expected empty list after delete, got %s", rr.Body.String())
	}
}

func TestUIQRRequiresActiveCode(t *testing.T) {
	srv, s, fake, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)

	if rr := ui.do(http.MethodGet, "/api/v1/ui/qr", nil); rr.Code != http.StatusNotFound {
		t.Errorf("expected 404 without a QR code, got %d", rr.Code)
	}
	fake.SetLatestQR("2@test-pairing-code")
	rr := ui.do(http.MethodGet, "/api/v1/ui/qr", nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "image/png" {
		t.Errorf("expected a PNG, got %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}
}

func TestUISettingsLanguage(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)

	if rr := ui.do(http.MethodPut, "/api/v1/ui/settings", map[string]string{"language": "fr"}); rr.Code != http.StatusBadRequest {
		t.Errorf("unsupported language should be rejected, got %d", rr.Code)
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/settings", map[string]string{"language": "en"}); rr.Code != http.StatusOK {
		t.Fatalf("set language: %d", rr.Code)
	}

	var state struct {
		Language string `json:"language"`
	}
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/state", nil).Body.Bytes(), &state)
	if state.Language != "en" {
		t.Errorf("state language = %q", state.Language)
	}
	saved, err := s.GetSetting(context.Background(), "language")
	if err != nil || saved != "en" {
		t.Errorf("setting not persisted: %q err=%v", saved, err)
	}
}

func TestUILogFilterAndRetry(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)
	ctx := context.Background()

	failed, _ := s.CreateMessage(ctx, store.LevelCritical, "a", "failed one", "", store.ModeText, "", store.StatusQueued, nil)
	sent, _ := s.CreateMessage(ctx, store.LevelInfo, "b", "sent one", "", store.ModeText, "", store.StatusQueued, nil)
	_ = s.RecordSendAttempt(ctx, failed.ID, store.StatusFailed, "boom", nil)
	_ = s.RecordSendAttempt(ctx, sent.ID, store.StatusSent, "", nil)

	var list []store.Message
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/log?status=failed", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != failed.ID {
		t.Fatalf("status filter returned %+v", list)
	}
	list = nil
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/log?level=info", nil).Body.Bytes(), &list)
	if len(list) != 1 || list[0].ID != sent.ID {
		t.Fatalf("level filter returned %+v", list)
	}

	if rr := ui.do(http.MethodPost, "/api/v1/ui/log/"+sent.ID+"/retry", nil); rr.Code != http.StatusConflict {
		t.Errorf("retrying a sent message: %d, want 409", rr.Code)
	}
	if rr := ui.do(http.MethodPost, "/api/v1/ui/log/missing/retry", nil); rr.Code != http.StatusNotFound {
		t.Errorf("retrying an unknown message: %d, want 404", rr.Code)
	}
	if rr := ui.do(http.MethodPost, "/api/v1/ui/log/"+failed.ID+"/retry", nil); rr.Code != http.StatusOK {
		t.Fatalf("retry failed message: %d", rr.Code)
	}
	got, _ := s.GetMessage(ctx, failed.ID)
	if got.Status != store.StatusQueued || got.Attempts != 0 || got.LastError != "" {
		t.Errorf("message not reset: %+v", got)
	}
}
