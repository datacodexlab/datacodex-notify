// SPDX-License-Identifier: GPL-3.0-only

package voice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"
	"unicode"

	"github.com/google/uuid"

	"datacodex-notify/internal/secrets"
	"datacodex-notify/internal/tts"
)

const settingKeys = "gemini_keys"

var (
	// ErrDuplicateKey means the same key is already in the list.
	ErrDuplicateKey = errors.New("this key is already added")
	// ErrInvalidKeyFormat means the key is empty or contains whitespace.
	ErrInvalidKeyFormat = errors.New("the key must not be empty or contain spaces")
	// ErrKeyNotFound means no key has the given id.
	ErrKeyNotFound = errors.New("key not found")
)

// KeyInfo is what the UI may know about a key. The secret itself is never exposed.
type KeyInfo struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	AddedAt time.Time `json:"added_at"`
	Masked  string    `json:"masked"`
}

// keyRecord is the stored metadata: the secret lives only in Credential Manager,
// and Hash (SHA-256 of the key) exists to reject duplicates.
type keyRecord struct {
	ID      string    `json:"id"`
	Label   string    `json:"label"`
	AddedAt time.Time `json:"added_at"`
	Masked  string    `json:"masked"`
	Hash    string    `json:"hash"`
}

func (r keyRecord) info() KeyInfo {
	return KeyInfo{ID: r.ID, Label: r.Label, AddedAt: r.AddedAt, Masked: r.Masked}
}

// mask shows the first and last four characters. Short keys are fully hidden.
func mask(secret string) string {
	if len(secret) < 12 {
		return strings.Repeat("*", len(secret))
	}
	return secret[:4] + "..." + secret[len(secret)-4:]
}

func hashKey(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

func (s *Service) loadRecords(ctx context.Context) ([]keyRecord, error) {
	raw, err := s.store.GetSetting(ctx, settingKeys)
	if err != nil {
		return nil, err
	}
	if raw == "" {
		return nil, nil
	}
	var records []keyRecord
	if err := json.Unmarshal([]byte(raw), &records); err != nil {
		return nil, fmt.Errorf("the stored key list is damaged: %w", err)
	}
	return records, nil
}

func (s *Service) saveRecords(ctx context.Context, records []keyRecord) error {
	data, err := json.Marshal(records)
	if err != nil {
		return fmt.Errorf("failed to encode the key list: %w", err)
	}
	return s.store.SetSetting(ctx, settingKeys, string(data))
}

// ListKeys returns the keys in the order they were added.
func (s *Service) ListKeys(ctx context.Context) ([]KeyInfo, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	infos := make([]KeyInfo, len(records))
	for i, r := range records {
		infos[i] = r.info()
	}
	return infos, nil
}

// Keys implements tts.KeySource: the keys in added order with their secrets loaded.
func (s *Service) Keys(ctx context.Context) ([]tts.Key, error) {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return nil, err
	}
	keys := make([]tts.Key, 0, len(records))
	for _, r := range records {
		secret, err := s.secrets.Get(secrets.GeminiKeyTarget(r.ID))
		if err != nil {
			slog.Warn("A Gemini key has no stored secret and is skipped", "id", r.ID, "err", err)
			continue
		}
		keys = append(keys, tts.Key{ID: r.ID, Secret: secret})
	}
	return keys, nil
}

// AddKey stores a new key. Duplicates (same SHA-256) are rejected.
func (s *Service) AddKey(ctx context.Context, label, secret string) (KeyInfo, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" || strings.IndexFunc(secret, unicode.IsSpace) >= 0 {
		return KeyInfo{}, ErrInvalidKeyFormat
	}

	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return KeyInfo{}, err
	}
	hash := hashKey(secret)
	for _, r := range records {
		if r.Hash == hash {
			return KeyInfo{}, ErrDuplicateKey
		}
	}

	record := keyRecord{
		ID:      uuid.New().String(),
		Label:   strings.TrimSpace(label),
		AddedAt: time.Now().UTC(),
		Masked:  mask(secret),
		Hash:    hash,
	}
	if record.Label == "" {
		record.Label = fmt.Sprintf("Key %d", len(records)+1)
	}

	target := secrets.GeminiKeyTarget(record.ID)
	if err := s.secrets.Set(target, secret); err != nil {
		return KeyInfo{}, fmt.Errorf("failed to store the key: %w", err)
	}
	if err := s.saveRecords(ctx, append(records, record)); err != nil {
		_ = s.secrets.Delete(target)
		return KeyInfo{}, err
	}
	return record.info(), nil
}

// DeleteKey removes a key and its secret.
func (s *Service) DeleteKey(ctx context.Context, id string) error {
	s.keyMu.Lock()
	defer s.keyMu.Unlock()

	records, err := s.loadRecords(ctx)
	if err != nil {
		return err
	}
	kept := make([]keyRecord, 0, len(records))
	found := false
	for _, r := range records {
		if r.ID == id {
			found = true
			continue
		}
		kept = append(kept, r)
	}
	if !found {
		return ErrKeyNotFound
	}
	if err := s.saveRecords(ctx, kept); err != nil {
		return err
	}
	if err := s.secrets.Delete(secrets.GeminiKeyTarget(id)); err != nil {
		return fmt.Errorf("the key was removed from the list but its secret could not be deleted: %w", err)
	}
	return nil
}

// TestKey runs a real minimal generation with one stored key.
func (s *Service) TestKey(ctx context.Context, id string) (tts.KeyTestResult, error) {
	keys, err := s.Keys(ctx)
	if err != nil {
		return tts.KeyTestResult{}, err
	}
	for _, key := range keys {
		if key.ID == id {
			return s.tts.TestKey(ctx, key), nil
		}
	}
	return tts.KeyTestResult{}, ErrKeyNotFound
}

// MigrateLegacyKey moves the old single Gemini key entry into the key list.
func (s *Service) MigrateLegacyKey(ctx context.Context) error {
	secret, err := s.secrets.Get(secrets.TargetGeminiKey)
	if errors.Is(err, secrets.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}

	if _, err := s.AddKey(ctx, "Migrated key", secret); err != nil && !errors.Is(err, ErrDuplicateKey) {
		return fmt.Errorf("failed to migrate the existing Gemini key: %w", err)
	}
	return s.secrets.Delete(secrets.TargetGeminiKey)
}
