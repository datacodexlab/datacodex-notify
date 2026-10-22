// SPDX-License-Identifier: GPL-3.0-only

//go:build !devfake

package app

import (
	"context"

	"datacodex-notify/internal/config"
	"datacodex-notify/internal/wa"
)

// newSender creates the WhatsApp client. The returned start function connects it.
func newSender(cfg *config.Config, callback wa.EventCallback) (wa.Sender, func(context.Context) error, error) {
	client, err := wa.NewClient(cfg.WhatsAppDB, callback)
	if err != nil {
		return nil, nil, err
	}
	return client, client.Start, nil
}
