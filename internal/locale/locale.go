// SPDX-License-Identifier: GPL-3.0-only

package locale

import (
	"embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
)

//go:embed *.json
var localeFS embed.FS

type Bundle struct {
	Levels   map[string]string `json:"levels"`
	Prefixes map[string]string `json:"prefixes"`
	Messages map[string]string `json:"messages"`
	Tray     map[string]string `json:"tray"`
}

type Manager struct {
	mu          sync.RWMutex
	currentLang string
	bundles     map[string]*Bundle
}

var defaultManager *Manager
var once sync.Once

func GetManager() *Manager {
	once.Do(func() {
		defaultManager = NewManager("ar")
	})
	return defaultManager
}

func NewManager(defaultLang string) *Manager {
	m := &Manager{
		currentLang: defaultLang,
		bundles:     make(map[string]*Bundle),
	}
	_ = m.loadBundle("ar")
	_ = m.loadBundle("en")
	return m
}

func (m *Manager) loadBundle(lang string) error {
	filename := lang + ".json"
	data, err := localeFS.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read locale file %s: %w", filename, err)
	}

	var b Bundle
	if err := json.Unmarshal(data, &b); err != nil {
		return fmt.Errorf("failed to unmarshal locale file %s: %w", filename, err)
	}

	m.bundles[lang] = &b
	return nil
}

func (m *Manager) SetLanguage(lang string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.bundles[lang]; ok {
		m.currentLang = lang
	}
}

func (m *Manager) GetLanguage() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.currentLang
}

func (m *Manager) GetLevel(level string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if b, ok := m.bundles[m.currentLang]; ok {
		if val, exists := b.Levels[level]; exists && val != "" {
			return val
		}
	}
	if b, ok := m.bundles["en"]; ok {
		if val, exists := b.Levels[level]; exists {
			return val
		}
	}
	return level
}

func (m *Manager) GetPrefix(level string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if b, ok := m.bundles[m.currentLang]; ok {
		if val, exists := b.Prefixes[level]; exists && val != "" {
			return val
		}
	}
	if b, ok := m.bundles["en"]; ok {
		if val, exists := b.Prefixes[level]; exists {
			return val
		}
	}
	return ""
}

func (m *Manager) GetMessage(key string, replacements map[string]string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	tmpl := ""
	if b, ok := m.bundles[m.currentLang]; ok {
		tmpl = b.Messages[key]
	}
	if tmpl == "" {
		if b, ok := m.bundles["en"]; ok {
			tmpl = b.Messages[key]
		}
	}
	if tmpl == "" {
		return key
	}

	for k, v := range replacements {
		tmpl = strings.ReplaceAll(tmpl, "{"+k+"}", v)
	}
	return tmpl
}

func (m *Manager) GetTrayText(key string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if b, ok := m.bundles[m.currentLang]; ok {
		if val, exists := b.Tray[key]; exists && val != "" {
			return val
		}
	}
	if b, ok := m.bundles["en"]; ok {
		if val, exists := b.Tray[key]; exists {
			return val
		}
	}
	return key
}
