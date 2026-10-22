// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package app

import (
	"context"
	"path/filepath"

	"datacodex-notify/internal/config"
	"datacodex-notify/internal/wa"
)

// FakeLogName is the JSON-lines file in the data directory where the fake sender records sends.
const FakeLogName = "fake-whatsapp.jsonl"

// newSender returns the recording fake used by end-to-end tests; it never touches WhatsApp.
func newSender(cfg *config.Config, _ wa.EventCallback) (wa.Sender, func(context.Context) error, error) {
	fake := wa.NewFakeSender(filepath.Join(cfg.DataDir, FakeLogName))
	return fake, func(context.Context) error { return nil }, nil
}
