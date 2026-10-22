// SPDX-License-Identifier: GPL-3.0-only

package secrets

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"

	"github.com/danieljoos/wincred"
)

const (
	TargetAPIToken = "datacodex-notify/api-token"
	// TargetGeminiKey is the legacy single-key entry, migrated into the key list.
	TargetGeminiKey = "datacodex-notify/gemini-api-key"
	// GeminiKeyTargetPrefix prefixes one Credential Manager entry per Gemini key.
	GeminiKeyTargetPrefix = "datacodex-notify/gemini-key/"
)

// GeminiKeyTarget is the Credential Manager target holding the Gemini key with the given id.
func GeminiKeyTarget(id string) string {
	return GeminiKeyTargetPrefix + id
}

var (
	ErrNotFound = errors.New("secret not found")
)

type Store interface {
	Get(target string) (string, error)
	Set(target, value string) error
	Delete(target string) error
	EnsureAPIToken() (string, error)
}

type WinCredStore struct{}

func NewWinCredStore() *WinCredStore {
	return &WinCredStore{}
}

func (s *WinCredStore) Get(target string) (string, error) {
	cred, err := wincred.GetGenericCredential(target)
	if err != nil {
		return "", classifyCredError(target, err)
	}
	if cred == nil || len(cred.CredentialBlob) == 0 {
		return "", ErrNotFound
	}
	return string(cred.CredentialBlob), nil
}

// classifyCredError maps a Credential Manager error: a missing entry becomes
// ErrNotFound, anything else is wrapped so callers can tell the two apart.
func classifyCredError(target string, err error) error {
	if errors.Is(err, wincred.ErrElementNotFound) {
		return ErrNotFound
	}
	return fmt.Errorf("credential manager error for %s: %w", target, err)
}

func (s *WinCredStore) Set(target, value string) error {
	cred := wincred.NewGenericCredential(target)
	cred.CredentialBlob = []byte(value)
	cred.Persist = wincred.PersistLocalMachine
	return cred.Write()
}

func (s *WinCredStore) Delete(target string) error {
	cred, err := wincred.GetGenericCredential(target)
	if err != nil {
		if errors.Is(err, wincred.ErrElementNotFound) {
			return nil
		}
		return classifyCredError(target, err)
	}
	return cred.Delete()
}

func GenerateSecureToken(numBytes int) (string, error) {
	b := make([]byte, numBytes)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func (s *WinCredStore) EnsureAPIToken() (string, error) {
	token, err := s.Get(TargetAPIToken)
	if err == nil && token != "" {
		return token, nil
	}
	if err != nil && !errors.Is(err, ErrNotFound) {
		return "", err
	}

	token, err = GenerateSecureToken(32)
	if err != nil {
		return "", err
	}

	if err := s.Set(TargetAPIToken, token); err != nil {
		return "", fmt.Errorf("failed to persist api token: %w", err)
	}
	return token, nil
}
