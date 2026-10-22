// SPDX-License-Identifier: GPL-3.0-only

// Package secretstest provides an in-memory secrets.Store for tests.
package secretstest

import (
	"sync"

	"datacodex-notify/internal/secrets"
)

// Memory keeps secrets in a map and never touches Credential Manager.
type Memory struct {
	mu      sync.Mutex
	secrets map[string]string
}

// NewMemory creates an empty store.
func NewMemory() *Memory {
	return &Memory{secrets: make(map[string]string)}
}

func (m *Memory) Get(target string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.secrets[target]
	if !ok || value == "" {
		return "", secrets.ErrNotFound
	}
	return value, nil
}

func (m *Memory) Set(target, value string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.secrets[target] = value
	return nil
}

func (m *Memory) Delete(target string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.secrets, target)
	return nil
}

func (m *Memory) EnsureAPIToken() (string, error) {
	if token, err := m.Get(secrets.TargetAPIToken); err == nil {
		return token, nil
	}
	token, err := secrets.GenerateSecureToken(32)
	if err != nil {
		return "", err
	}
	return token, m.Set(secrets.TargetAPIToken, token)
}

// Targets lists the stored target names, for assertions.
func (m *Memory) Targets() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.secrets))
	for target := range m.secrets {
		out = append(out, target)
	}
	return out
}

var _ secrets.Store = (*Memory)(nil)
