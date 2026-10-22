// SPDX-License-Identifier: GPL-3.0-only

package app

import (
	"context"
	"log/slog"
	"time"

	"datacodex-notify/internal/store"
)

const eventTimeout = 5 * time.Second

// eventHandler persists WhatsApp events: receipts update delivery status and
// messages from enabled recipients are recorded in the inbox. Inbound text is
// stored as data only; nothing in this package acts on it.
type eventHandler struct {
	store *store.Store
}

func (h *eventHandler) OnDeliveryReceipt(waMessageID string, status string) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	if err := h.store.ApplyReceipt(ctx, waMessageID, store.MessageStatus(status)); err != nil {
		slog.Error("Failed to apply delivery receipt", "wa_message_id", waMessageID, "status", status, "err", err)
	}
}

func (h *eventHandler) OnInboundMessage(senderJID, waMessageID, text string) {
	ctx, cancel := context.WithTimeout(context.Background(), eventTimeout)
	defer cancel()

	recipient, err := h.store.GetRecipientByJID(ctx, senderJID)
	if err != nil {
		slog.Error("Failed to look up inbound sender", "err", err)
		return
	}
	if recipient == nil || !recipient.Enabled {
		slog.Debug("Ignored inbound message from a sender that is not an enabled recipient")
		return
	}

	if _, err := h.store.InsertInboxItem(ctx, recipient.ID, waMessageID, "text", text); err != nil {
		slog.Error("Failed to record inbound message", "wa_message_id", waMessageID, "err", err)
	}
}
