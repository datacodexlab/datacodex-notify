// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type fixedModels struct{ primary, fallback string }

func (m fixedModels) Models(context.Context) (string, string) { return m.primary, m.fallback }

func newClientWithModels(t *testing.T, models ModelSource, respond func(c call, w http.ResponseWriter), keys ...Key) (*Client, *fakeGoogle) {
	t.Helper()
	google := &fakeGoogle{respond: respond}
	srv := httptest.NewServer(google)
	t.Cleanup(srv.Close)
	return New(Config{Keys: staticKeys(keys), Models: models, BaseURL: srv.URL + "/v1beta"}), google
}

func TestSettingsOverrideTheDefaultModels(t *testing.T) {
	client, google := newClientWithModels(t, fixedModels{"custom-primary-tts", "custom-fallback-tts"}, func(c call, w http.ResponseWriter) {
		if c.model == "custom-primary-tts" {
			writeStatus(w, 429, "retry in 5s")
			return
		}
		success(c, w)
	}, twoKeys[0])

	if _, err := client.Synthesize(context.Background(), "hi", ""); err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if got := strings.Join(google.pairs(), ","); got != "A:custom-primary-tts,A:custom-fallback-tts" {
		t.Errorf("tried %s", got)
	}
}

func TestNoFallbackModelMeansOnlyThePrimaryIsTried(t *testing.T) {
	for _, fallback := range []string{"", "custom-primary-tts"} {
		client, google := newClientWithModels(t, fixedModels{"custom-primary-tts", fallback}, func(c call, w http.ResponseWriter) {
			writeStatus(w, 429, "retry in 5s")
		}, twoKeys[0])

		if _, err := client.Synthesize(context.Background(), "hi", ""); !errors.Is(err, ErrQuotaExhausted) {
			t.Fatalf("got %v", err)
		}
		if got := strings.Join(google.pairs(), ","); got != "A:custom-primary-tts" {
			t.Errorf("fallback %q: tried %s", fallback, got)
		}
	}
}

func TestModelUnavailableIsSkippedForAllKeys(t *testing.T) {
	client, google := newClientWithModels(t, fixedModels{"gone-tts", "working-tts"}, func(c call, w http.ResponseWriter) {
		if c.model == "gone-tts" {
			writeStatus(w, 404, "models/gone-tts is not found")
			return
		}
		success(c, w)
	}, twoKeys...)

	if _, err := client.Synthesize(context.Background(), "hi", ""); err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if got := strings.Join(google.pairs(), ","); got != "A:gone-tts,A:working-tts" {
		t.Errorf("first call tried %s", got)
	}
	if msg := client.UnavailableModels()["gone-tts"]; !strings.Contains(msg, "not found") {
		t.Errorf("unavailable models = %v", client.UnavailableModels())
	}

	// The unavailable model is not requested again, with any key.
	google.calls = nil
	if _, err := client.Synthesize(context.Background(), "hi", ""); err != nil {
		t.Fatalf("second Synthesize failed: %v", err)
	}
	if got := strings.Join(google.pairs(), ","); got != "A:working-tts" {
		t.Errorf("second call tried %s", got)
	}

	client.ForgetUnavailableModels()
	if len(client.UnavailableModels()) != 0 {
		t.Error("ForgetUnavailableModels should clear the marks")
	}
}

func TestEveryModelUnavailable(t *testing.T) {
	client, _ := newClientWithModels(t, fixedModels{"gone-a-tts", "gone-b-tts"}, func(c call, w http.ResponseWriter) {
		writeStatus(w, 404, "not found")
	}, twoKeys...)

	_, err := client.Synthesize(context.Background(), "hi", "")
	if !errors.Is(err, ErrModelUnavailable) || errors.Is(err, ErrInvalidKey) {
		t.Fatalf("got %v, want ErrModelUnavailable only", err)
	}
	if _, err := client.Synthesize(context.Background(), "hi", ""); !errors.Is(err, ErrModelUnavailable) {
		t.Fatalf("second call: got %v", err)
	}
}

func TestListModelsPagesAndFiltersTTS(t *testing.T) {
	client, google, _ := newClient(t, func(c call, w http.ResponseWriter) {
		if strings.Contains(c.query, "pageToken=page2") {
			_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-3.8-flash-tts","displayName":"Flash TTS"},{"name":"models/embedding-001"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/gemini-3.8-flash-lite-tts","displayName":"Flash-Lite TTS"},{"name":"models/gemini-pro","displayName":"Pro"}],"nextPageToken":"page2"}`))
	}, twoKeys[0])

	models, err := client.ListModels(context.Background())
	if err != nil {
		t.Fatalf("ListModels failed: %v", err)
	}
	if len(models) != 2 || models[0].ID != "gemini-3.8-flash-lite-tts" || models[1].ID != "gemini-3.8-flash-tts" || models[1].DisplayName != "Flash TTS" {
		t.Errorf("unexpected models: %+v", models)
	}
	if len(google.calls) != 2 {
		t.Fatalf("expected two pages, got %d calls", len(google.calls))
	}
	for _, c := range google.calls {
		if c.method != http.MethodGet || c.path != "/v1beta/models" || c.secret != secretA {
			t.Errorf("unexpected request %+v", c)
		}
	}
}

func TestListModelsUsesTheFirstUsableKey(t *testing.T) {
	client, google, _ := newClient(t, func(c call, w http.ResponseWriter) {
		if c.secret == secretA {
			writeStatus(w, 403, "API key not valid")
			return
		}
		_, _ = w.Write([]byte(`{"models":[{"name":"models/x-tts"}]}`))
	}, twoKeys...)

	models, err := client.ListModels(context.Background())
	if err != nil || len(models) != 1 || models[0].DisplayName != "x-tts" {
		t.Fatalf("got %+v err=%v", models, err)
	}
	if got := google.pairs(); len(got) != 2 {
		t.Errorf("expected key A then key B, got %v", got)
	}
}

func TestListModelsErrorsDoNotLeakTheKey(t *testing.T) {
	client, _, _ := newClient(t, func(c call, w http.ResponseWriter) {
		writeStatus(w, 403, "API key "+c.secret+" not valid")
	}, twoKeys...)
	_, err := client.ListModels(context.Background())
	if err == nil || strings.Contains(err.Error(), secretA) || strings.Contains(err.Error(), secretB) {
		t.Errorf("got %v", err)
	}

	empty := New(Config{Keys: staticKeys(nil)})
	if _, err := empty.ListModels(context.Background()); !errors.Is(err, ErrNoKeys) {
		t.Errorf("got %v, want ErrNoKeys", err)
	}
}

func TestTestModelAcceptsSuccessAndRateLimit(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		wantValid   bool
		wantLimited bool
	}{
		{"ok", 200, true, false},
		{"rate limited", 429, true, true},
		{"not found", 404, false, false},
		{"server error", 500, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, google, _ := newClient(t, func(c call, w http.ResponseWriter) {
				if tc.status == 200 {
					success(c, w)
					return
				}
				writeStatus(w, tc.status, "google says "+http.StatusText(tc.status))
			}, twoKeys[0])

			result := client.TestModel(context.Background(), "candidate-tts")
			if result.Valid != tc.wantValid || result.RateLimited != tc.wantLimited {
				t.Errorf("got %+v", result)
			}
			if tc.status != 200 && !strings.Contains(result.Message, "google says") {
				t.Errorf("message should be Google's: %q", result.Message)
			}
			if google.calls[0].model != "candidate-tts" || !strings.Contains(google.calls[0].raw, `"speech_config"`) {
				t.Errorf("the test must be a speech generation with the candidate model: %s", google.calls[0].raw)
			}
		})
	}
}
