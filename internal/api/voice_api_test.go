// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package api

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"datacodex-notify/internal/secrets/secretstest"
	"datacodex-notify/internal/store"
	"datacodex-notify/internal/voice"
)

const (
	apiKeyA = "test-key-A-0123456789"
	apiKeyB = "test-key-B-9876543210"
)

var apiMiniWAV = append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 28)...)

type voiceGoogle struct {
	mu     sync.Mutex
	status int
	models []string
}

func (g *voiceGoogle) setStatus(status int) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.status = status
}

func (g *voiceGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	status := g.status
	models := g.models
	g.mu.Unlock()

	if r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models") {
		var list []map[string]string
		for _, m := range models {
			list = append(list, map[string]string{"name": "models/" + m, "displayName": "Display " + m})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"models": list})
		return
	}
	if status != 0 && status != http.StatusOK {
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]string{"message": "google says no"}})
		return
	}
	if strings.HasSuffix(r.URL.Path, "/interactions") {
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"steps": []map[string]interface{}{
			{"type": "model_output", "content": []map[string]string{{"type": "audio", "data": base64.StdEncoding.EncodeToString(apiMiniWAV)}}},
		}})
		return
	}
	_, _ = w.Write([]byte(`{"candidates":[]}`))
}

func withVoice(t *testing.T, google *voiceGoogle, ffmpegFound bool) func(*Config) {
	return func(cfg *Config) {
		srv := httptest.NewServer(google)
		t.Cleanup(srv.Close)
		svc := voice.New(cfg.Store, secretstest.NewMemory(), voice.Options{
			CacheDir: t.TempDir(),
			TmpDir:   t.TempDir(),
			BaseURL:  srv.URL + "/v1beta",
			FindFFmpeg: func(string) (string, error) {
				if ffmpegFound {
					return "ffmpeg.exe", nil
				}
				return "", context.DeadlineExceeded
			},
		})
		cfg.Voice = svc
		cfg.NotifySvc.SetVoice(svc)
		cfg.Dispatcher.SetVoice(svc)
	}
}

func newVoiceUI(t *testing.T, ffmpegFound bool) (*uiClient, *voiceGoogle, *store.Store) {
	t.Helper()
	google := &voiceGoogle{models: []string{"gemini-pro", "alpha-tts", "beta-tts"}}
	srv, s, _, _ := setupTestServer(t, withVoice(t, google, ffmpegFound))
	t.Cleanup(func() { s.Close() })
	return newUIClient(t, srv), google, s
}

func addKey(t *testing.T, ui *uiClient, label, key string) *httptest.ResponseRecorder {
	t.Helper()
	return ui.do(http.MethodPost, "/api/v1/ui/voice/keys", map[string]string{"label": label, "key": key})
}

func TestVoiceKeysLifecycleAndNoSecretEverReturned(t *testing.T) {
	ui, _, _ := newVoiceUI(t, true)
	var transcript strings.Builder
	record := func(rr *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		transcript.WriteString(rr.Body.String())
		return rr
	}

	rr := record(addKey(t, ui, "Work", apiKeyA))
	if rr.Code != http.StatusCreated {
		t.Fatalf("add key: %d %s", rr.Code, rr.Body.String())
	}
	var added voice.KeyInfo
	_ = json.Unmarshal(rr.Body.Bytes(), &added)
	if added.Label != "Work" || added.Masked != "test...6789" || added.ID == "" {
		t.Errorf("unexpected key info: %+v", added)
	}

	if rr := record(addKey(t, ui, "Again", apiKeyA)); rr.Code != http.StatusConflict {
		t.Errorf("duplicate key: %d, want 409", rr.Code)
	}
	if rr := record(addKey(t, ui, "Bad", "has space")); rr.Code != http.StatusBadRequest {
		t.Errorf("bad format: %d, want 400", rr.Code)
	}
	record(addKey(t, ui, "Second", apiKeyB))

	rr = record(ui.do(http.MethodGet, "/api/v1/ui/voice", nil))
	var overview struct {
		Status voice.Status    `json:"status"`
		Keys   []voice.KeyInfo `json:"keys"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &overview); err != nil || len(overview.Keys) != 2 || overview.Status.KeyCount != 2 {
		t.Fatalf("overview: %s", rr.Body.String())
	}
	if overview.Keys[0].ID != added.ID {
		t.Error("keys must be listed in the order they were added")
	}

	rr = record(ui.do(http.MethodPost, "/api/v1/ui/voice/keys/"+added.ID+"/test", nil))
	var tested struct {
		Valid       bool   `json:"valid"`
		RateLimited bool   `json:"rate_limited"`
		Message     string `json:"message"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &tested)
	if rr.Code != http.StatusOK || !tested.Valid {
		t.Errorf("key test: %d %s", rr.Code, rr.Body.String())
	}

	if rr := record(ui.do(http.MethodDelete, "/api/v1/ui/voice/keys/"+added.ID, nil)); rr.Code != http.StatusOK {
		t.Errorf("delete key: %d", rr.Code)
	}
	if rr := record(ui.do(http.MethodDelete, "/api/v1/ui/voice/keys/"+added.ID, nil)); rr.Code != http.StatusNotFound {
		t.Errorf("deleting twice: %d, want 404", rr.Code)
	}
	if rr := record(ui.do(http.MethodPost, "/api/v1/ui/voice/keys/missing/test", nil)); rr.Code != http.StatusNotFound {
		t.Errorf("testing an unknown key: %d, want 404", rr.Code)
	}

	for _, secret := range []string{apiKeyA, apiKeyB} {
		if strings.Contains(transcript.String(), secret) {
			t.Errorf("an API response contains a key secret: %s", secret)
		}
	}
}

func TestVoiceKeyTestShowsGoogleMessage(t *testing.T) {
	ui, google, _ := newVoiceUI(t, true)
	var added voice.KeyInfo
	_ = json.Unmarshal(addKey(t, ui, "K", apiKeyA).Body.Bytes(), &added)

	google.setStatus(http.StatusForbidden)
	rr := ui.do(http.MethodPost, "/api/v1/ui/voice/keys/"+added.ID+"/test", nil)
	var tested struct {
		Valid   bool   `json:"valid"`
		Message string `json:"message"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &tested)
	if tested.Valid || tested.Message != "google says no" {
		t.Errorf("got %s", rr.Body.String())
	}
}

func TestVoiceSettingsEndpoint(t *testing.T) {
	ui, _, _ := newVoiceUI(t, true)

	rr := ui.do(http.MethodPut, "/api/v1/ui/voice", map[string]interface{}{
		"voice": "Charon",
		"modes": map[string]string{"info": "voice", "critical": "text"},
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("save settings: %d %s", rr.Code, rr.Body.String())
	}
	var overview struct {
		Status voice.Status `json:"status"`
	}
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/voice", nil).Body.Bytes(), &overview)
	if overview.Status.Voice != "Charon" || overview.Status.Modes["info"] != "voice" || overview.Status.Modes["critical"] != "text" || overview.Status.Modes["warning"] != "both" {
		t.Errorf("unexpected status: %+v", overview.Status)
	}

	for name, body := range map[string]map[string]interface{}{
		"bad voice":  {"voice": "Nobody"},
		"bad mode":   {"modes": map[string]string{"info": "shout"}},
		"bad level":  {"modes": map[string]string{"urgent": "text"}},
		"bad ffmpeg": {"ffmpeg_path": `C:\definitely\missing\ffmpeg.exe`},
	} {
		if rr := ui.do(http.MethodPut, "/api/v1/ui/voice", body); rr.Code != http.StatusBadRequest {
			t.Errorf("%s: got %d, want 400", name, rr.Code)
		}
	}
}

func TestVoiceModelsListSaveAndReset(t *testing.T) {
	ui, google, _ := newVoiceUI(t, true)

	if rr := ui.do(http.MethodGet, "/api/v1/ui/voice/models", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("listing models without a key: %d, want 400", rr.Code)
	}
	addKey(t, ui, "K", apiKeyA)

	rr := ui.do(http.MethodGet, "/api/v1/ui/voice/models", nil)
	var listed struct {
		Models []struct {
			ID          string `json:"id"`
			DisplayName string `json:"display_name"`
		} `json:"models"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &listed)
	if rr.Code != http.StatusOK || len(listed.Models) != 2 || listed.Models[0].ID != "alpha-tts" || listed.Models[0].DisplayName != "Display alpha-tts" {
		t.Fatalf("only TTS models are listed: %d %s", rr.Code, rr.Body.String())
	}

	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice/models", map[string]string{"primary": "alpha-tts", "fallback": "beta-tts"}); rr.Code != http.StatusOK {
		t.Fatalf("save models: %d %s", rr.Code, rr.Body.String())
	}
	var overview struct {
		Status voice.Status `json:"status"`
	}
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/voice", nil).Body.Bytes(), &overview)
	if m := overview.Status.Models; m.Primary != "alpha-tts" || m.Fallback != "beta-tts" || !m.Custom {
		t.Errorf("saved models not in effect: %+v", m)
	}

	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice/models", map[string]string{"primary": "alpha-tts", "fallback": "alpha-tts"}); rr.Code != http.StatusBadRequest {
		t.Errorf("same model twice: %d, want 400", rr.Code)
	}
	if rr := ui.do(http.MethodPut, "/api/v1/ui/voice/models", map[string]string{"primary": "", "fallback": "x"}); rr.Code != http.StatusBadRequest {
		t.Errorf("empty primary: %d, want 400", rr.Code)
	}

	google.setStatus(http.StatusNotFound)
	rr = ui.do(http.MethodPut, "/api/v1/ui/voice/models", map[string]string{"primary": "ghost-tts", "fallback": "none"})
	var rejected struct {
		Error string `json:"error"`
	}
	_ = json.Unmarshal(rr.Body.Bytes(), &rejected)
	if rr.Code != http.StatusUnprocessableEntity || rejected.Error != "google says no" {
		t.Errorf("rejected model: %d %s", rr.Code, rr.Body.String())
	}
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/voice", nil).Body.Bytes(), &overview)
	if overview.Status.Models.Primary != "alpha-tts" {
		t.Errorf("a rejected model must not replace the saved one: %+v", overview.Status.Models)
	}

	google.setStatus(0)
	if rr := ui.do(http.MethodDelete, "/api/v1/ui/voice/models", nil); rr.Code != http.StatusOK {
		t.Fatalf("reset: %d", rr.Code)
	}
	_ = json.Unmarshal(ui.do(http.MethodGet, "/api/v1/ui/voice", nil).Body.Bytes(), &overview)
	if overview.Status.Models.Custom {
		t.Errorf("reset must restore the defaults: %+v", overview.Status.Models)
	}
}

func TestVoiceSampleReturnsAudio(t *testing.T) {
	ui, google, _ := newVoiceUI(t, true)

	if rr := ui.do(http.MethodPost, "/api/v1/ui/voice/sample", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("sample without a key: %d, want 400", rr.Code)
	}
	addKey(t, ui, "K", apiKeyA)

	rr := ui.do(http.MethodPost, "/api/v1/ui/voice/sample", nil)
	if rr.Code != http.StatusOK || rr.Header().Get("Content-Type") != "audio/wav" || !strings.HasPrefix(rr.Body.String(), "RIFF") {
		t.Fatalf("sample: %d %q", rr.Code, rr.Header().Get("Content-Type"))
	}

	google.setStatus(http.StatusTooManyRequests)
	if rr := ui.do(http.MethodPost, "/api/v1/ui/voice/sample", nil); rr.Code != http.StatusTooManyRequests {
		t.Errorf("rate limited sample: %d, want 429", rr.Code)
	}
}

func TestVoiceTestMessageNeedsKeyAndFFmpeg(t *testing.T) {
	ui, _, s := newVoiceUI(t, true)
	ctx := context.Background()
	if _, err := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatal(err)
	}

	if rr := ui.do(http.MethodPost, "/api/v1/ui/voice/test", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("without a key: %d, want 400", rr.Code)
	}
	addKey(t, ui, "K", apiKeyA)

	rr := ui.do(http.MethodPost, "/api/v1/ui/voice/test", nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("voice test: %d %s", rr.Code, rr.Body.String())
	}
	messages, _ := s.ListRecentMessages(ctx, 10, "", "")
	if len(messages) != 1 || messages[0].Mode != store.ModeVoice || messages[0].Speech == "" || messages[0].Source != "admin-ui" {
		t.Errorf("unexpected queued message: %+v", messages)
	}

	noFFmpeg, _, _ := newVoiceUI(t, false)
	addKey(t, noFFmpeg, "K", apiKeyA)
	if rr := noFFmpeg.do(http.MethodPost, "/api/v1/ui/voice/test", nil); rr.Code != http.StatusBadRequest {
		t.Errorf("without ffmpeg: %d, want 400", rr.Code)
	}
}

func TestVoiceEndpointsRequireSessionAndCSRF(t *testing.T) {
	srv, s, _, _ := setupTestServer(t, withVoice(t, &voiceGoogle{}, true))
	defer s.Close()

	rr := httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, newTestRequest(http.MethodGet, "/api/v1/ui/voice", nil))
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("no session: %d, want 401", rr.Code)
	}

	ui := newUIClient(t, srv)
	req := newTestRequest(http.MethodPost, "/api/v1/ui/voice/keys", strings.NewReader(`{"label":"x","key":"`+apiKeyA+`"}`))
	req.AddCookie(ui.cookie)
	rr = httptest.NewRecorder()
	srv.httpServer.Handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("no CSRF token: %d, want 403", rr.Code)
	}
}

func TestVoiceEndpointsAnswer503WhenNotConfigured(t *testing.T) {
	srv, s, _, _ := setupTestServer(t)
	defer s.Close()
	ui := newUIClient(t, srv)
	if rr := ui.do(http.MethodGet, "/api/v1/ui/voice", nil); rr.Code != http.StatusServiceUnavailable {
		t.Errorf("got %d, want 503", rr.Code)
	}
}
