// SPDX-License-Identifier: GPL-3.0-only

package tts

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	secretA = "test-key-A-0123456789"
	secretB = "test-key-B-9876543210"
)

// miniWAV is a valid RIFF/WAVE header with an empty data chunk.
var miniWAV = append([]byte("RIFF\x24\x00\x00\x00WAVEfmt "), make([]byte, 28)...)

type staticKeys []Key

func (k staticKeys) Keys(context.Context) ([]Key, error) { return k, nil }

type call struct {
	method string
	query  string
	secret string
	model  string
	path   string
	body   map[string]interface{}
	raw    string
}

type fakeGoogle struct {
	mu      sync.Mutex
	calls   []call
	respond func(c call, w http.ResponseWriter)
}

func (f *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	buf := new(strings.Builder)
	var raw []byte
	raw = make([]byte, 0, 1024)
	b := make([]byte, 1024)
	for {
		n, err := r.Body.Read(b)
		raw = append(raw, b[:n]...)
		if err != nil {
			break
		}
	}
	buf.Write(raw)

	var body map[string]interface{}
	_ = json.Unmarshal(raw, &body)
	model, _ := body["model"].(string)
	c := call{method: r.Method, query: r.URL.RawQuery, secret: r.Header.Get("x-goog-api-key"), model: model, path: r.URL.Path, body: body, raw: buf.String()}

	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	f.respond(c, w)
}

func (f *fakeGoogle) pairs() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		id := "?"
		switch c.secret {
		case secretA:
			id = "A"
		case secretB:
			id = "B"
		}
		out = append(out, id+":"+c.model)
	}
	return out
}

func audioJSON() string {
	return `{"steps":[{"type":"model_output","content":[{"type":"audio","data":"AAAA"},{"type":"audio","data":"` +
		base64.StdEncoding.EncodeToString(miniWAV) + `"}]}]}`
}

func writeStatus(w http.ResponseWriter, status int, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]interface{}{
		"error": map[string]interface{}{"code": status, "message": message},
	})
}

func newClient(t *testing.T, respond func(c call, w http.ResponseWriter), keys ...Key) (*Client, *fakeGoogle, *time.Time) {
	t.Helper()
	google := &fakeGoogle{respond: respond}
	srv := httptest.NewServer(google)
	t.Cleanup(srv.Close)

	clock := time.Date(2026, 10, 21, 12, 0, 0, 0, time.UTC)
	client := New(Config{
		Keys:    staticKeys(keys),
		BaseURL: srv.URL + "/v1beta",
		Now:     func() time.Time { return clock },
	})
	return client, google, &clock
}

var twoKeys = []Key{{ID: "A", Secret: secretA}, {ID: "B", Secret: secretB}}

func success(_ call, w http.ResponseWriter) { _, _ = w.Write([]byte(audioJSON())) }

func TestSynthesizeRequestShape(t *testing.T) {
	client, google, _ := newClient(t, success, twoKeys[0])

	wav, err := client.Synthesize(context.Background(), "hello there", VoiceCharon)
	if err != nil {
		t.Fatalf("Synthesize failed: %v", err)
	}
	if string(wav) != string(miniWAV) {
		t.Error("did not return the last audio item")
	}

	c := google.calls[0]
	if c.path != "/v1beta/interactions" || c.secret != secretA || c.model != DefaultPrimaryModel {
		t.Errorf("unexpected request: %+v", c)
	}
	for _, want := range []string{
		`"type":"user_input"`,
		`"text":"hello there"`,
		`"annotations":[{"type":"speech_metadata","style":"` + StyleEgyptian + `"}]`,
		`"response_format":{"type":"audio","mime_type":"audio/wav"}`,
		`"speech_config":[{"voice":"Charon"}]`,
	} {
		if !strings.Contains(c.raw, want) {
			t.Errorf("request body %s is missing %s", c.raw, want)
		}
	}
}

func TestSynthesizeDefaultsUnknownVoiceToKore(t *testing.T) {
	client, google, _ := newClient(t, success, twoKeys[0])
	if _, err := client.Synthesize(context.Background(), "hi", "Nobody"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(google.calls[0].raw, `"voice":"Kore"`) {
		t.Errorf("unknown voice should become Kore: %s", google.calls[0].raw)
	}
}

func TestFallbackMatrix(t *testing.T) {
	rateLimited := func(c call, w http.ResponseWriter) { writeStatus(w, 429, "Quota exceeded. Please retry in 38s") }
	invalid := func(c call, w http.ResponseWriter) { writeStatus(w, 403, "API key not valid") }

	tests := []struct {
		name      string
		respond   func(c call, w http.ResponseWriter)
		wantPairs []string
		wantErr   error
		wantOK    bool
	}{
		{
			name: "429 on primary falls back to the other model of the same key",
			respond: func(c call, w http.ResponseWriter) {
				if c.model == DefaultPrimaryModel {
					rateLimited(c, w)
					return
				}
				success(c, w)
			},
			wantPairs: []string{"A:" + DefaultPrimaryModel, "A:" + DefaultFallbackModel},
			wantOK:    true,
		},
		{
			name: "invalid first key moves to the next key",
			respond: func(c call, w http.ResponseWriter) {
				if c.secret == secretA {
					invalid(c, w)
					return
				}
				success(c, w)
			},
			wantPairs: []string{"A:" + DefaultPrimaryModel, "B:" + DefaultPrimaryModel},
			wantOK:    true,
		},
		{
			name: "rate limited key falls through to the next key",
			respond: func(c call, w http.ResponseWriter) {
				if c.secret == secretA {
					rateLimited(c, w)
					return
				}
				success(c, w)
			},
			wantPairs: []string{"A:" + DefaultPrimaryModel, "A:" + DefaultFallbackModel, "B:" + DefaultPrimaryModel},
			wantOK:    true,
		},
		{
			name:      "every pair rate limited gives ErrQuotaExhausted",
			respond:   rateLimited,
			wantPairs: []string{"A:" + DefaultPrimaryModel, "A:" + DefaultFallbackModel, "B:" + DefaultPrimaryModel, "B:" + DefaultFallbackModel},
			wantErr:   ErrQuotaExhausted,
		},
		{
			name:      "every key invalid gives ErrInvalidKey",
			respond:   invalid,
			wantPairs: []string{"A:" + DefaultPrimaryModel, "B:" + DefaultPrimaryModel},
			wantErr:   ErrInvalidKey,
		},
		{
			name:      "other errors stop immediately",
			respond:   func(c call, w http.ResponseWriter) { writeStatus(w, 500, "backend exploded") },
			wantPairs: []string{"A:" + DefaultPrimaryModel},
		},
		{
			name:      "plain 400 is an other error and stops",
			respond:   func(c call, w http.ResponseWriter) { writeStatus(w, 400, "Invalid argument: bad voice") },
			wantPairs: []string{"A:" + DefaultPrimaryModel},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, google, _ := newClient(t, tc.respond, twoKeys...)
			_, err := client.Synthesize(context.Background(), "hi", "")

			if got := google.pairs(); strings.Join(got, ",") != strings.Join(tc.wantPairs, ",") {
				t.Errorf("tried %v, want %v", got, tc.wantPairs)
			}
			switch {
			case tc.wantOK && err != nil:
				t.Errorf("unexpected error: %v", err)
			case tc.wantErr != nil && !errors.Is(err, tc.wantErr):
				t.Errorf("error = %v, want %v", err, tc.wantErr)
			case !tc.wantOK && tc.wantErr == nil:
				var apiErr *Error
				if !errors.As(err, &apiErr) || apiErr.Kind != KindOther {
					t.Errorf("expected an other-kind error, got %v", err)
				}
			}
		})
	}
}

func TestNoKeys(t *testing.T) {
	client, google, _ := newClient(t, success)
	if _, err := client.Synthesize(context.Background(), "hi", ""); !errors.Is(err, ErrNoKeys) {
		t.Fatalf("got %v, want ErrNoKeys", err)
	}
	if len(google.calls) != 0 {
		t.Error("no request should be made without keys")
	}
}

func TestKeyNeverAppearsInErrors(t *testing.T) {
	leak := func(c call, w http.ResponseWriter) { writeStatus(w, 403, "API key "+c.secret+" is not valid") }
	client, _, _ := newClient(t, leak, twoKeys...)

	_, err := client.Synthesize(context.Background(), "hi", "")
	if err == nil {
		t.Fatal("expected an error")
	}
	for _, secret := range []string{secretA, secretB} {
		if strings.Contains(err.Error(), secret) {
			t.Errorf("error leaks a key: %v", err)
		}
	}
	if !strings.Contains(err.Error(), "[redacted]") {
		t.Errorf("expected Google's message with the key redacted: %v", err)
	}

	other := func(c call, w http.ResponseWriter) { writeStatus(w, 500, "failed for "+c.secret) }
	client, _, _ = newClient(t, other, twoKeys[0])
	_, err = client.Synthesize(context.Background(), "hi", "")
	if err == nil || strings.Contains(err.Error(), secretA) {
		t.Errorf("error leaks a key: %v", err)
	}
}

func TestSynthesizeKeepsGoogleMessage(t *testing.T) {
	client, _, _ := newClient(t, func(c call, w http.ResponseWriter) { writeStatus(w, 500, "model overloaded, try later") }, twoKeys[0])
	_, err := client.Synthesize(context.Background(), "hi", "")
	if err == nil || !strings.Contains(err.Error(), "model overloaded, try later") {
		t.Errorf("Google's message should be preserved: %v", err)
	}
}

func TestSynthesizeRejectsBadPayloads(t *testing.T) {
	payloads := map[string]string{
		"not json":   `nope`,
		"no audio":   `{"steps":[{"type":"model_output","content":[{"type":"text","text":"x"}]}]}`,
		"bad base64": `{"steps":[{"type":"model_output","content":[{"type":"audio","data":"***"}]}]}`,
		"not wav":    `{"steps":[{"type":"model_output","content":[{"type":"audio","data":"` + base64.StdEncoding.EncodeToString([]byte("raw pcm bytes here")) + `"}]}]}`,
	}
	for name, payload := range payloads {
		t.Run(name, func(t *testing.T) {
			client, _, _ := newClient(t, func(c call, w http.ResponseWriter) { _, _ = w.Write([]byte(payload)) }, twoKeys[0])
			_, err := client.Synthesize(context.Background(), "hi", "")
			var apiErr *Error
			if !errors.As(err, &apiErr) || apiErr.Kind != KindOther {
				t.Fatalf("got %v, want an other-kind error", err)
			}
		})
	}
}

func TestSynthesizeNetworkErrorStopsAndNamesNoKey(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	base := srv.URL
	srv.Close()
	client := New(Config{Keys: staticKeys(twoKeys), BaseURL: base})

	_, err := client.Synthesize(context.Background(), "hi", "")
	var apiErr *Error
	if !errors.As(err, &apiErr) || apiErr.Kind != KindNetwork {
		t.Fatalf("got %v, want a network error", err)
	}
	if strings.Contains(err.Error(), secretA) {
		t.Errorf("error leaks a key: %v", err)
	}
}

func TestTestKey(t *testing.T) {
	tests := []struct {
		name        string
		status      int
		message     string
		wantValid   bool
		wantLimited bool
	}{
		{"valid", 200, "", true, false},
		{"rate limited but valid", 429, "Resource exhausted, retry in 5s", true, true},
		{"rejected", 400, "API key not valid. Please pass a valid API key.", false, false},
		{"server error", 500, "internal", false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			client, google, _ := newClient(t, func(c call, w http.ResponseWriter) {
				if tc.status == 200 {
					_, _ = w.Write([]byte(`{"candidates":[]}`))
					return
				}
				writeStatus(w, tc.status, tc.message)
			}, twoKeys[0])

			result := client.TestKey(context.Background(), twoKeys[0])
			if result.Valid != tc.wantValid || result.RateLimited != tc.wantLimited {
				t.Errorf("got %+v", result)
			}
			if tc.message != "" && result.Message != tc.message {
				t.Errorf("message = %q, want Google's %q", result.Message, tc.message)
			}

			c := google.calls[0]
			if c.path != "/v1beta/models/"+ModelText+":generateContent" || c.secret != secretA {
				t.Errorf("unexpected request %s", c.path)
			}
			if !strings.Contains(c.raw, `"maxOutputTokens":1`) {
				t.Errorf("the test must request one output token: %s", c.raw)
			}
		})
	}
}
