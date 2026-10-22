// SPDX-License-Identifier: GPL-3.0-only

package voice

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/secrets/secretstest"
	"datacodex-notify/internal/store"
)

const (
	keyA = "test-key-A-0123456789"
	keyB = "test-key-B-9876543210"
)

func toneWAV() []byte {
	const rate = 24000
	samples := make([]int16, rate) // one second
	for i := range samples {
		samples[i] = int16(9000 * math.Sin(2*math.Pi*330*float64(i)/rate))
	}
	var buf bytes.Buffer
	buf.WriteString("RIFF")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(36+len(samples)*2))
	buf.WriteString("WAVEfmt ")
	_ = binary.Write(&buf, binary.LittleEndian, struct {
		Size       uint32
		Format     uint16
		Channels   uint16
		SampleRate uint32
		ByteRate   uint32
		Align      uint16
		Bits       uint16
	}{16, 1, 1, rate, rate * 2, 2, 16})
	buf.WriteString("data")
	_ = binary.Write(&buf, binary.LittleEndian, uint32(len(samples)*2))
	_ = binary.Write(&buf, binary.LittleEndian, samples)
	return buf.Bytes()
}

type fakeGoogle struct {
	mu          sync.Mutex
	speechCalls int
	voices      []string
	status      int
	models      []string
}

func (g *fakeGoogle) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()

	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/models"):
		var list []map[string]string
		for _, m := range g.models {
			list = append(list, map[string]string{"name": "models/" + m, "displayName": m})
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"models": list})
	case strings.HasSuffix(r.URL.Path, "/interactions"):
		var body struct {
			GenerationConfig struct {
				SpeechConfig []struct {
					Voice string `json:"voice"`
				} `json:"speech_config"`
			} `json:"generation_config"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		g.speechCalls++
		g.voices = append(g.voices, body.GenerationConfig.SpeechConfig[0].Voice)
		if g.status != 0 && g.status != http.StatusOK {
			w.WriteHeader(g.status)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "google says no: retry in 30s"}})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"steps": []map[string]interface{}{
			{"type": "model_output", "content": []map[string]string{{"type": "audio", "data": base64.StdEncoding.EncodeToString(toneWAV())}}},
		}})
	default: // key test
		if g.status != 0 && g.status != http.StatusOK {
			w.WriteHeader(g.status)
			_ = json.NewEncoder(w).Encode(map[string]interface{}{"error": map[string]interface{}{"message": "google says no"}})
			return
		}
		_, _ = w.Write([]byte(`{"candidates":[]}`))
	}
}

type fixture struct {
	svc     *Service
	store   *store.Store
	secrets *secretstest.Memory
	google  *fakeGoogle
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "voice.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	google := &fakeGoogle{}
	srv := httptest.NewServer(google)
	t.Cleanup(srv.Close)

	mem := secretstest.NewMemory()
	root := t.TempDir()
	svc := New(st, mem, Options{
		CacheDir: filepath.Join(root, "cache"),
		TmpDir:   root,
		BaseURL:  srv.URL + "/v1beta",
	})
	return &fixture{svc: svc, store: st, secrets: mem, google: google}
}

func TestAddKeyStoresMetadataAndSecretSeparately(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	info, err := f.svc.AddKey(ctx, "  Work  ", " "+keyA+" ")
	if err != nil {
		t.Fatalf("AddKey failed: %v", err)
	}
	if info.Label != "Work" || info.Masked != "test...6789" || info.ID == "" || info.AddedAt.IsZero() {
		t.Errorf("unexpected info: %+v", info)
	}

	stored, _ := f.secrets.Get(secrets.GeminiKeyTarget(info.ID))
	if stored != keyA {
		t.Errorf("the secret must be stored under its own target, got %q", stored)
	}
	raw, _ := f.store.GetSetting(ctx, settingKeys)
	if strings.Contains(raw, keyA) {
		t.Error("the key must never be stored in the database")
	}

	second, err := f.svc.AddKey(ctx, "", keyB)
	if err != nil || second.Label != "Key 2" {
		t.Fatalf("second key: %+v err=%v", second, err)
	}
	list, _ := f.svc.ListKeys(ctx)
	if len(list) != 2 || list[0].ID != info.ID || list[1].ID != second.ID {
		t.Errorf("keys must stay in added order: %+v", list)
	}
}

func TestAddKeyRejectsDuplicatesAndBadFormats(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	if _, err := f.svc.AddKey(ctx, "one", keyA); err != nil {
		t.Fatal(err)
	}

	if _, err := f.svc.AddKey(ctx, "again", keyA); !errors.Is(err, ErrDuplicateKey) {
		t.Errorf("got %v, want ErrDuplicateKey", err)
	}
	for _, bad := range []string{"", "   ", "has space inside", "tab\tinside"} {
		if _, err := f.svc.AddKey(ctx, "x", bad); !errors.Is(err, ErrInvalidKeyFormat) {
			t.Errorf("%q: got %v, want ErrInvalidKeyFormat", bad, err)
		}
	}
	if list, _ := f.svc.ListKeys(ctx); len(list) != 1 {
		t.Errorf("rejected keys must not be added, got %d", len(list))
	}
	if len(f.secrets.Targets()) != 1 {
		t.Errorf("rejected keys must not leave secrets behind: %v", f.secrets.Targets())
	}
}

func TestShortKeysAreFullyMasked(t *testing.T) {
	if got := mask("short"); got != "*****" {
		t.Errorf("mask = %q", got)
	}
}

func TestKeysReturnsSecretsInOrderAndDeleteRemovesThem(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	a, _ := f.svc.AddKey(ctx, "a", keyA)
	b, _ := f.svc.AddKey(ctx, "b", keyB)

	keys, err := f.svc.Keys(ctx)
	if err != nil || len(keys) != 2 || keys[0].Secret != keyA || keys[1].Secret != keyB {
		t.Fatalf("Keys = %+v err=%v", keys, err)
	}

	if err := f.svc.DeleteKey(ctx, a.ID); err != nil {
		t.Fatalf("DeleteKey failed: %v", err)
	}
	if _, err := f.secrets.Get(secrets.GeminiKeyTarget(a.ID)); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("the secret must be deleted with the key")
	}
	list, _ := f.svc.ListKeys(ctx)
	if len(list) != 1 || list[0].ID != b.ID {
		t.Errorf("list after delete: %+v", list)
	}
	if err := f.svc.DeleteKey(ctx, "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("got %v", err)
	}
}

func TestMigrateLegacyKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()

	if err := f.svc.MigrateLegacyKey(ctx); err != nil {
		t.Fatalf("nothing to migrate must not fail: %v", err)
	}

	_ = f.secrets.Set(secrets.TargetGeminiKey, keyA)
	if err := f.svc.MigrateLegacyKey(ctx); err != nil {
		t.Fatalf("MigrateLegacyKey failed: %v", err)
	}
	list, _ := f.svc.ListKeys(ctx)
	if len(list) != 1 || list[0].Label != "Migrated key" {
		t.Fatalf("list = %+v", list)
	}
	if _, err := f.secrets.Get(secrets.TargetGeminiKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("the legacy entry must be removed")
	}

	// The same legacy key again is a duplicate: no second entry, legacy still removed.
	_ = f.secrets.Set(secrets.TargetGeminiKey, keyA)
	if err := f.svc.MigrateLegacyKey(ctx); err != nil {
		t.Fatalf("duplicate migration failed: %v", err)
	}
	if list, _ := f.svc.ListKeys(ctx); len(list) != 1 {
		t.Errorf("duplicate legacy key must not be added twice: %+v", list)
	}
	if _, err := f.secrets.Get(secrets.TargetGeminiKey); !errors.Is(err, secrets.ErrNotFound) {
		t.Error("the legacy entry must be removed even when it was a duplicate")
	}
}

func TestTestKey(t *testing.T) {
	f := newFixture(t)
	ctx := context.Background()
	info, _ := f.svc.AddKey(ctx, "a", keyA)

	result, err := f.svc.TestKey(ctx, info.ID)
	if err != nil || !result.Valid {
		t.Fatalf("got %+v err=%v", result, err)
	}

	f.google.status = http.StatusTooManyRequests
	if result, _ := f.svc.TestKey(ctx, info.ID); !result.Valid || !result.RateLimited {
		t.Errorf("a 429 means valid but rate limited: %+v", result)
	}
	f.google.status = http.StatusForbidden
	if result, _ := f.svc.TestKey(ctx, info.ID); result.Valid || result.Message != "google says no" {
		t.Errorf("a rejected key must show Google's message: %+v", result)
	}
	if _, err := f.svc.TestKey(ctx, "missing"); !errors.Is(err, ErrKeyNotFound) {
		t.Errorf("got %v", err)
	}
}
