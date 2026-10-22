// SPDX-License-Identifier: GPL-3.0-only

package locale

import (
	"testing"
)

func TestLocaleLookup(t *testing.T) {
	m := NewManager("ar")

	prefix := m.GetPrefix("info")
	if prefix != "🟢" {
		t.Errorf("expected 🟢, got %s", prefix)
	}

	arLevel := m.GetLevel("warning")
	if arLevel == "" || arLevel == "warning" {
		t.Errorf("expected translated Arabic level, got %s", arLevel)
	}

	m.SetLanguage("en")
	enLevel := m.GetLevel("warning")
	if enLevel != "Warning" {
		t.Errorf("expected Warning, got %s", enLevel)
	}

	msg := m.GetMessage("disconnected_alert", map[string]string{
		"from": "10:00",
		"to":   "10:05",
	})
	if msg == "" || msg == "disconnected_alert" {
		t.Errorf("expected formatted message, got %s", msg)
	}
}
