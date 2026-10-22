// SPDX-License-Identifier: GPL-3.0-only

// Package autostart starts the app with Windows for the current user only,
// through the per-user Run key. It never writes machine-wide settings.
package autostart

import (
	"fmt"
	"strings"
)

// ValueName is the Run key value that starts the app.
const ValueName = "DatacodexNotify"

// Registry is the small part of the Windows registry this package needs.
type Registry interface {
	Get(name string) (value string, found bool, err error)
	Set(name, value string) error
	Delete(name string) error
}

// Manager turns the Run entry on and off.
type Manager struct {
	registry Registry
	name     string
	command  string
}

// New creates a manager for the entry called name that starts exePath.
func New(registry Registry, name, exePath string) *Manager {
	return &Manager{registry: registry, name: name, command: quote(exePath)}
}

// quote wraps the path in double quotes so spaces in folder names are safe.
func quote(path string) string {
	return `"` + strings.Trim(path, `"`) + `"`
}

// Enabled reports whether an entry exists for this app.
func (m *Manager) Enabled() (bool, error) {
	_, found, err := m.registry.Get(m.name)
	if err != nil {
		return false, fmt.Errorf("failed to read the startup entry: %w", err)
	}
	return found, nil
}

// Set creates or removes the entry. Enabling rewrites an existing entry so it
// points at the current executable.
func (m *Manager) Set(enabled bool) error {
	if enabled {
		if err := m.registry.Set(m.name, m.command); err != nil {
			return fmt.Errorf("failed to write the startup entry: %w", err)
		}
		return nil
	}
	if err := m.registry.Delete(m.name); err != nil {
		return fmt.Errorf("failed to remove the startup entry: %w", err)
	}
	return nil
}
