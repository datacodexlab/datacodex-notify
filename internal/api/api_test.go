// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/notify"
	"datacodex-notify/internal/queue"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/wa"
)

// testPort is the port the test server pretends to listen on; handlers are
// called directly, so nothing binds to it.
const testPort = 48700

func newTestRequest(method, target string, body io.Reader) *http.Request {
	req := httptest.NewRequest(method, target, body)
	req.Host = fmt.Sprintf("127.0.0.1:%d", testPort)
	return req
}

func setupTestServer(t *testing.T, opts ...func(*Config)) (*Server, *store.Store, *wa.FakeSender, string) {
	tmpDir, err := os.MkdirTemp("", "api-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}

	dbPath := filepath.Join(tmpDir, "api.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open store failed: %v", err)
	}

	fakeSender := wa.NewFakeSender("")
	loc := locale.NewManager("ar")
	notifySvc := notify.NewService(s, loc)
	dispatcher := queue.NewDispatcher(s, fakeSender)

	apiToken := "test-agent-token-12345"
	cfg := Config{
		Port:       testPort,
		APIToken:   apiToken,
		Store:      s,
		NotifySvc:  notifySvc,
		WASender:   fakeSender,
		Dispatcher: dispatcher,
		Locale:     loc,
		Version:    "0.1.0",
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	srv := NewServer(cfg)

	return srv, s, fakeSender, apiToken
}

func TestHealthEndpoint(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()

	req := newTestRequest(http.MethodGet, "/api/v1/health", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), `"ok":true`) {
		t.Errorf("expected ok:true in body, got %s", rr.Body.String())
	}
}

func TestAgentAuthMiddleware(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()

	// 1. Missing token -> 401
	req := newTestRequest(http.MethodGet, "/api/v1/status", nil)
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for missing token, got %d", rr.Code)
	}

	// 2. Invalid token -> 401
	req = newTestRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("expected 401 for invalid token, got %d", rr.Code)
	}

	// 3. Origin header present -> 403 Forbidden
	req = newTestRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Origin", "http://evil.com")
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for Origin header, got %d", rr.Code)
	}

	// 4. Valid token -> 200
	req = newTestRequest(http.MethodGet, "/api/v1/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

func TestBodySizeLimit(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()

	// Create oversized payload (> 32 KiB)
	hugeString := strings.Repeat("A", 35*1024)
	body, _ := json.Marshal(map[string]string{
		"text": hugeString,
	})

	req := newTestRequest(http.MethodPost, "/api/v1/notify", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	// MaxBytesReader triggers an error on reading
	if rr.Code != http.StatusBadRequest && rr.Code != http.StatusRequestEntityTooLarge {
		t.Errorf("expected 400 or 413, got %d", rr.Code)
	}
}

func TestRateLimiter(t *testing.T) {
	rl := NewRateLimiter(3, 1*time.Minute)
	key := "test-key"

	if !rl.Allow(key) || !rl.Allow(key) || !rl.Allow(key) {
		t.Errorf("expected first 3 calls to be allowed")
	}
	if rl.Allow(key) {
		t.Errorf("expected 4th call to be rate limited")
	}
}

func TestNotifyEndpoint(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()

	// Add enabled recipient
	_, err := s.AddRecipient(context.Background(), "Dev", "+201000000002", "201000000002@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	payload := map[string]interface{}{
		"level":  "info",
		"source": "claude",
		"text":   "Agent completed task successfully",
	}
	body, _ := json.Marshal(payload)

	req := newTestRequest(http.MethodPost, "/api/v1/notify", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}

	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp["id"] == "" || resp["status"] != "queued" {
		t.Errorf("unexpected notify response: %v", resp)
	}
}

func TestUISessionAndCSRF(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()

	launchToken, err := srv.GenerateLaunchToken()
	if err != nil {
		t.Fatalf("GenerateLaunchToken failed: %v", err)
	}

	// Exchange launch token for session cookie + CSRF token
	sessionBody, _ := json.Marshal(map[string]string{
		"launch_token": launchToken,
	})
	req := newTestRequest(http.MethodPost, "/api/v1/ui/session", bytes.NewReader(sessionBody))
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}

	var sessionResp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &sessionResp)
	csrfToken := sessionResp["csrf_token"].(string)

	cookies := rr.Result().Cookies()
	var sessionCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == "dcn_session" {
			sessionCookie = c
			break
		}
	}
	if sessionCookie == nil {
		t.Fatal("expected dcn_session cookie")
	}

	// GET /api/v1/ui/state with cookie
	req = newTestRequest(http.MethodGet, "/api/v1/ui/state", nil)
	req.AddCookie(sessionCookie)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for UI state, got %d", rr.Code)
	}

	// POST without CSRF header should fail (403)
	req = newTestRequest(http.MethodPost, "/api/v1/ui/test", nil)
	req.AddCookie(sessionCookie)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403 for missing CSRF token on POST, got %d", rr.Code)
	}

	// POST with valid CSRF header succeeds
	req = newTestRequest(http.MethodPost, "/api/v1/ui/test", nil)
	req.AddCookie(sessionCookie)
	req.Header.Set("X-CSRF-Token", csrfToken)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	// Even if no recipients, status might be 400 (from notify service), but auth/CSRF passed!
	if rr.Code == http.StatusForbidden || rr.Code == http.StatusUnauthorized {
		t.Errorf("expected auth to pass, got %d", rr.Code)
	}
}

func TestHostGuardRejectsForeignHosts(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()

	tests := []struct {
		host string
		want int
	}{
		{"127.0.0.1:48700", http.StatusOK},
		{"localhost:48700", http.StatusOK},
		{"LOCALHOST:48700", http.StatusOK},
		{"evil.example.com", http.StatusForbidden},
		{"evil.example.com:48700", http.StatusForbidden},
		{"127.0.0.1:9999", http.StatusForbidden},
		{"127.0.0.1", http.StatusForbidden},
		{"", http.StatusForbidden},
	}
	for _, tc := range tests {
		req := newTestRequest(http.MethodGet, "/api/v1/status", nil)
		req.Host = tc.host
		req.Header.Set("Authorization", "Bearer "+token)
		rr := httptest.NewRecorder()
		srv.httpServer.Handler.ServeHTTP(rr, req)
		if rr.Code != tc.want {
			t.Errorf("Host %q: got %d, want %d", tc.host, rr.Code, tc.want)
		}
	}
}

func postNotify(t *testing.T, srv *Server, token string, payload map[string]interface{}) map[string]interface{} {
	t.Helper()
	body, _ := json.Marshal(payload)
	req := newTestRequest(http.MethodPost, "/api/v1/notify", bytes.NewReader(body))
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d, body=%s", rr.Code, rr.Body.String())
	}
	var resp map[string]interface{}
	_ = json.Unmarshal(rr.Body.Bytes(), &resp)
	return resp
}

func TestNotifyWaitReturnsTheFinalStatus(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	if _, err := s.AddRecipient(context.Background(), "Dev", "+201000000002", "201000000002@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}
	srv.dispatcher.Start()
	defer srv.dispatcher.Stop()

	resp := postNotify(t, srv, token, map[string]interface{}{"level": "info", "text": "hello", "wait": 10})
	if status := resp["status"]; status != "sent" && status != "delivered" {
		t.Errorf("a waiting send must return the final status, got %v", status)
	}
}

func TestNotifyWaitEndsWithQueuedWhenNothingSendsIt(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	if _, err := s.AddRecipient(context.Background(), "Dev", "+201000000002", "201000000002@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}
	// The dispatcher is not started, so the message stays queued until the wait ends.

	start := time.Now()
	resp := postNotify(t, srv, token, map[string]interface{}{"level": "info", "text": "hello", "wait": 1})
	if resp["status"] != "queued" {
		t.Errorf("expected queued, got %v", resp["status"])
	}
	if elapsed := time.Since(start); elapsed < 900*time.Millisecond || elapsed > 5*time.Second {
		t.Errorf("the wait should last about one second, took %v", elapsed)
	}
}

func TestNotifyWithoutWaitReturnsAtOnce(t *testing.T) {
	srv, s, _, token := setupTestServer(t)
	defer s.Close()
	if _, err := s.AddRecipient(context.Background(), "Dev", "+201000000002", "201000000002@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	resp := postNotify(t, srv, token, map[string]interface{}{"level": "info", "text": "hello"})
	if resp["status"] != "queued" || time.Since(start) > time.Second {
		t.Errorf("no wait means an immediate queued reply: %v after %v", resp["status"], time.Since(start))
	}
}
