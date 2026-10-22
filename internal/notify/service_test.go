// SPDX-License-Identifier: GPL-3.0-only

package notify

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"datacodex-notify/internal/locale"
	"datacodex-notify/internal/store"
)

func TestNotifyFormattingAndValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "notify-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "notify.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer s.Close()

	loc := locale.NewManager("ar")
	svc := NewService(s, loc)
	svc.loc = time.UTC

	// Test formatWith
	fixedTime := time.Date(2026, 10, 21, 14, 30, 0, 0, time.UTC)
	formatted := svc.render(layout{LabelKey: string(store.LevelWarning), Source: "agent:core", Text: "Task finished"}, fixedTime)

	if !strings.Contains(formatted, "🟠") {
		t.Errorf("expected warning emoji 🟠 in formatted message: %s", formatted)
	}
	if !strings.Contains(formatted, "14:30") {
		t.Errorf("expected time 14:30 in formatted message: %s", formatted)
	}
	if !strings.Contains(formatted, "agent:core") {
		t.Errorf("expected source in formatted message: %s", formatted)
	}

	ctx := context.Background()

	// Empty text validation
	_, err = svc.ValidateAndEnqueue(ctx, &SendRequest{
		Level: store.LevelInfo,
		Text:  "",
	})
	if err == nil {
		t.Errorf("expected error for empty text, got nil")
	}

	// No recipients validation
	_, err = svc.ValidateAndEnqueue(ctx, &SendRequest{
		Level: store.LevelInfo,
		Text:  "Valid message",
	})
	if err == nil {
		t.Errorf("expected error for no recipients, got nil")
	}

	// Add recipient and test successful enqueue
	r, err := s.AddRecipient(ctx, "Developer", "+201000000003", "201000000003@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	msg, err := svc.ValidateAndEnqueue(ctx, &SendRequest{
		Level:  store.LevelInfo,
		Source: "claude",
		Text:   "Build succeeded",
	})
	if err != nil {
		t.Fatalf("ValidateAndEnqueue failed: %v", err)
	}
	if msg.Status != store.StatusQueued {
		t.Errorf("expected status queued, got %s", msg.Status)
	}

	deliveries, err := s.GetDeliveriesForMessage(ctx, msg.ID)
	if err != nil || len(deliveries) != 1 {
		t.Fatalf("expected 1 delivery, got %d, err=%v", len(deliveries), err)
	}
	if deliveries[0].RecipientID != r.ID {
		t.Errorf("expected recipient %s, got %s", r.ID, deliveries[0].RecipientID)
	}
}
