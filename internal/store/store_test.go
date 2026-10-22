// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreMigrationsAndCRUD(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "store-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test_app.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()

	// Test Settings
	if err := s.SetSetting(ctx, "theme", "dark"); err != nil {
		t.Fatalf("SetSetting failed: %v", err)
	}
	val, err := s.GetSetting(ctx, "theme")
	if err != nil || val != "dark" {
		t.Fatalf("GetSetting failed: val=%s, err=%v", val, err)
	}

	// Test Recipients
	r, err := s.AddRecipient(ctx, "Dev Admin", "+201000000003", "201000000003@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}
	if r.Name != "Dev Admin" {
		t.Errorf("expected name 'Dev Admin', got %s", r.Name)
	}

	recipients, err := s.ListRecipients(ctx)
	if err != nil || len(recipients) != 1 {
		t.Fatalf("ListRecipients expected 1, got %d, err=%v", len(recipients), err)
	}

	// Test Messages and Deliveries
	msg, err := s.CreateMessage(ctx, LevelInfo, "claude:test", "Hello World", "", ModeText, "key-123", StatusQueued, []string{r.ID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	if msg.Status != StatusQueued {
		t.Errorf("expected status queued, got %s", msg.Status)
	}

	queued, err := s.GetNextQueuedMessage(ctx, time.Now())
	if err != nil || queued == nil || queued.ID != msg.ID {
		t.Fatalf("GetNextQueuedMessage failed: %v", err)
	}

	deliveries, err := s.GetDeliveriesForMessage(ctx, msg.ID)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("GetDeliveriesForMessage expected 1, got %d, err=%v", len(deliveries), err)
	}

	// Update Delivery and Message
	err = s.UpdateDeliveryStatus(ctx, deliveries[0].ID, "WAMID-999", StatusSent)
	if err != nil {
		t.Fatalf("UpdateDeliveryStatus failed: %v", err)
	}

	err = s.UpdateMessageStatus(ctx, msg.ID, StatusSent, "")
	if err != nil {
		t.Fatalf("UpdateMessageStatus failed: %v", err)
	}

	updatedMsg, err := s.GetMessage(ctx, msg.ID)
	if err != nil || updatedMsg.Status != StatusSent {
		t.Fatalf("expected status sent, got %v", updatedMsg.Status)
	}

	// Test Heartbeats
	hb, err := s.RegisterOrBeatHeartbeat(ctx, "run-1", "Test Runner", "claude", "", 300, 60, time.Now())
	if err != nil {
		t.Fatalf("RegisterOrBeatHeartbeat failed: %v", err)
	}
	if hb.State != "active" {
		t.Errorf("expected active, got %s", hb.State)
	}

	activeHBs, err := s.ListActiveHeartbeats(ctx)
	if err != nil || len(activeHBs) != 1 {
		t.Fatalf("ListActiveHeartbeats expected 1, got %d", len(activeHBs))
	}

	// Test Inbox
	inboxItem, err := s.InsertInboxItem(ctx, r.ID, "WAMID-INBOX-1", "text", "Developer reply")
	if err != nil {
		t.Fatalf("InsertInboxItem failed: %v", err)
	}
	if inboxItem.Text != "Developer reply" {
		t.Errorf("unexpected inbox text: %s", inboxItem.Text)
	}

	inboxList, err := s.ListInbox(ctx, 10)
	if err != nil || len(inboxList) != 1 {
		t.Fatalf("ListInbox expected 1, got %d", len(inboxList))
	}

	// Test Pruning
	if err := s.Prune(ctx, 10, 10); err != nil {
		t.Fatalf("Prune failed: %v", err)
	}
}

func TestStoreDuplicateDetection(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "store-dedupe-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "dedupe_app.db")
	s, err := Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	dedupeKey := "hash-12345"

	_, err = s.CreateMessage(ctx, LevelWarning, "agent:test", "Warning message", "", ModeText, dedupeKey, StatusQueued, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	dup, err := s.FindDuplicateMessage(ctx, dedupeKey, time.Now().UTC().Add(-10*time.Minute))
	if err != nil {
		t.Fatalf("FindDuplicateMessage error: %v", err)
	}
	if dup == nil {
		t.Fatalf("expected duplicate message, got nil")
	}

	// Non-existent key
	nonDup, err := s.FindDuplicateMessage(ctx, "non-existent", time.Now().UTC().Add(-10*time.Minute))
	if err != nil || nonDup != nil {
		t.Fatalf("expected nil for non-existent key, got %v", nonDup)
	}
}

func TestRecordSendAttemptAndBackoffSkip(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "attempts.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	first, err := s.CreateMessage(ctx, LevelInfo, "t", "first", "", ModeText, "", StatusQueued, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	second, err := s.CreateMessage(ctx, LevelInfo, "t", "second", "", ModeText, "", StatusQueued, nil)
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	// Plain status updates never count as attempts.
	if err := s.UpdateMessageStatus(ctx, first.ID, StatusSending, ""); err != nil {
		t.Fatalf("UpdateMessageStatus failed: %v", err)
	}
	got, _ := s.GetMessage(ctx, first.ID)
	if got.Attempts != 0 {
		t.Fatalf("status update changed attempts to %d", got.Attempts)
	}

	now := time.Now().UTC()
	next := now.Add(time.Minute)
	if err := s.RecordSendAttempt(ctx, first.ID, StatusQueued, "boom", &next); err != nil {
		t.Fatalf("RecordSendAttempt failed: %v", err)
	}
	got, _ = s.GetMessage(ctx, first.ID)
	if got.Attempts != 1 || got.Status != StatusQueued || got.LastError != "boom" {
		t.Fatalf("unexpected message after attempt: %+v", got)
	}

	due, err := s.GetNextQueuedMessage(ctx, now)
	if err != nil || due == nil || due.ID != second.ID {
		t.Fatalf("expected the later message to be due while the first backs off, got %+v err=%v", due, err)
	}
	queued, err := s.HasQueuedMessages(ctx)
	if err != nil || !queued {
		t.Fatalf("HasQueuedMessages = %v, err=%v", queued, err)
	}

	due, err = s.GetNextQueuedMessage(ctx, next.Add(time.Second))
	if err != nil || due == nil || due.ID != first.ID {
		t.Fatalf("expected the first message to be due after its backoff, got %+v err=%v", due, err)
	}
}

func TestApplyReceiptOnlyMovesForward(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "receipts.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	r, err := s.AddRecipient(ctx, "A", "+201000000001", "201000000001@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}
	msg, err := s.CreateMessage(ctx, LevelInfo, "t", "hello", "", ModeText, "", StatusQueued, []string{r.ID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	deliveries, _ := s.GetDeliveriesForMessage(ctx, msg.ID)
	if err := s.UpdateDeliveryStatus(ctx, deliveries[0].ID, "WAMID-1", StatusSent); err != nil {
		t.Fatalf("UpdateDeliveryStatus failed: %v", err)
	}
	if err := s.RecordSendAttempt(ctx, msg.ID, StatusSent, "", nil); err != nil {
		t.Fatalf("RecordSendAttempt failed: %v", err)
	}

	check := func(want MessageStatus) {
		t.Helper()
		got, _ := s.GetMessage(ctx, msg.ID)
		d, _ := s.GetDeliveriesForMessage(ctx, msg.ID)
		if got.Status != want || d[0].Status != want {
			t.Fatalf("message=%s delivery=%s, want %s", got.Status, d[0].Status, want)
		}
	}

	if err := s.ApplyReceipt(ctx, "WAMID-1", StatusDelivered); err != nil {
		t.Fatalf("ApplyReceipt failed: %v", err)
	}
	check(StatusDelivered)
	if err := s.ApplyReceipt(ctx, "WAMID-1", StatusRead); err != nil {
		t.Fatalf("ApplyReceipt failed: %v", err)
	}
	check(StatusRead)
	if err := s.ApplyReceipt(ctx, "WAMID-1", StatusDelivered); err != nil {
		t.Fatalf("ApplyReceipt failed: %v", err)
	}
	check(StatusRead)

	if err := s.ApplyReceipt(ctx, "UNKNOWN", StatusRead); err != nil {
		t.Fatalf("ApplyReceipt for an unknown id must not fail: %v", err)
	}
}

func TestDeliveriesAreOrderedTextFirstAndAddDeliveryWorks(t *testing.T) {
	s, err := Open(filepath.Join(t.TempDir(), "deliveries.db"))
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	r, err := s.AddRecipient(ctx, "A", "+201000000001", "201000000001@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}
	msg, err := s.CreateMessage(ctx, LevelWarning, "t", "text", "speech", ModeBoth, "", StatusQueued, []string{r.ID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	deliveries, _ := s.GetDeliveriesForMessage(ctx, msg.ID)
	if len(deliveries) != 2 || deliveries[0].Kind != "text" || deliveries[1].Kind != "voice" {
		t.Fatalf("text must come before voice: %+v", deliveries)
	}

	added, err := s.AddDelivery(ctx, msg.ID, r.ID, "text", "WA-FALLBACK", StatusSent)
	if err != nil {
		t.Fatalf("AddDelivery failed: %v", err)
	}
	if added.Status != StatusSent || added.WAMessageID != "WA-FALLBACK" {
		t.Errorf("unexpected delivery: %+v", added)
	}
	deliveries, _ = s.GetDeliveriesForMessage(ctx, msg.ID)
	if len(deliveries) != 3 {
		t.Fatalf("expected 3 deliveries, got %d", len(deliveries))
	}
	if err := s.UpdateDeliveryStatus(ctx, deliveries[2].ID, "", StatusVoiceFailed); err != nil {
		t.Fatalf("voice_failed must be a valid delivery status: %v", err)
	}
}
