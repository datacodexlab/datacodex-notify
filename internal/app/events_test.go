// SPDX-License-Identifier: GPL-3.0-only

package app

import (
	"context"
	"path/filepath"
	"testing"

	"datacodex-notify/internal/store"
)

func newEventFixture(t *testing.T) (*eventHandler, *store.Store) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "events.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return &eventHandler{store: s}, s
}

func TestInboundFromEnabledRecipientIsStoredAsData(t *testing.T) {
	h, s := newEventFixture(t)
	ctx := context.Background()
	if _, err := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7); err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	h.OnInboundMessage("201000000001@s.whatsapp.net", "WA-1", "hello")
	h.OnInboundMessage("201000000099@s.whatsapp.net", "WA-2", "stranger")

	items, err := s.ListInbox(ctx, 10)
	if err != nil || len(items) != 1 {
		t.Fatalf("expected exactly one stored inbox item, got %d (err=%v)", len(items), err)
	}
	if items[0].Text != "hello" || items[0].WAMessageID != "WA-1" {
		t.Errorf("unexpected inbox item: %+v", items[0])
	}
}

func TestInboundFromDisabledRecipientIsIgnored(t *testing.T) {
	h, s := newEventFixture(t)
	ctx := context.Background()
	r, err := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}
	if err := s.UpdateRecipient(ctx, r.ID, r.Name, r.PhoneE164, r.JID, false, 7); err != nil {
		t.Fatalf("UpdateRecipient failed: %v", err)
	}

	h.OnInboundMessage(r.JID, "WA-1", "hello")

	if items, _ := s.ListInbox(ctx, 10); len(items) != 0 {
		t.Errorf("disabled recipient must not reach the inbox, got %d items", len(items))
	}
}

func TestReceiptUpdatesMessageStatus(t *testing.T) {
	h, s := newEventFixture(t)
	ctx := context.Background()
	r, _ := s.AddRecipient(ctx, "Admin", "+201000000001", "201000000001@s.whatsapp.net", 7)
	msg, err := s.CreateMessage(ctx, store.LevelInfo, "t", "hi", "", store.ModeText, "", store.StatusQueued, []string{r.ID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	deliveries, _ := s.GetDeliveriesForMessage(ctx, msg.ID)
	_ = s.UpdateDeliveryStatus(ctx, deliveries[0].ID, "WA-9", store.StatusSent)
	_ = s.RecordSendAttempt(ctx, msg.ID, store.StatusSent, "", nil)

	h.OnDeliveryReceipt("WA-9", "read")

	got, _ := s.GetMessage(ctx, msg.ID)
	if got.Status != store.StatusRead {
		t.Errorf("message status = %s, want read", got.Status)
	}
}
