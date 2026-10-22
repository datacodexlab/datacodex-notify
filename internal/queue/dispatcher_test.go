// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package queue

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"datacodex-notify/internal/store"
	"datacodex-notify/internal/wa"
)

func TestDispatcherSuccessAndOfflineHold(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "queue-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "queue.db")
	s, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("Open store failed: %v", err)
	}
	defer s.Close()

	ctx := context.Background()
	r, err := s.AddRecipient(ctx, "Test Admin", "+201000000004", "201000000004@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	fakeSender := wa.NewFakeSender("")
	dispatcher := NewDispatcher(s, fakeSender)

	// Step 1: When offline, message stays queued
	fakeSender.SetState(wa.StateReconnecting)

	msg, err := s.CreateMessage(ctx, store.LevelInfo, "test", "Offline text", "", store.ModeText, "", store.StatusQueued, []string{r.ID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}

	dispatcher.processNext()

	checkMsg, err := s.GetMessage(ctx, msg.ID)
	if err != nil || checkMsg.Status != store.StatusQueued {
		t.Fatalf("expected message to remain queued when offline, got %v", checkMsg.Status)
	}

	// Step 2: When connected, processNext delivers it
	fakeSender.SetState(wa.StateConnected)
	dispatcher.processNext()

	checkMsg, err = s.GetMessage(ctx, msg.ID)
	if err != nil || checkMsg.Status != store.StatusSent {
		t.Fatalf("expected message to be sent, got %v", checkMsg.Status)
	}

	deliveries, err := s.GetDeliveriesForMessage(ctx, msg.ID)
	if err != nil || len(deliveries) != 1 || deliveries[0].Status != store.StatusSent {
		t.Fatalf("expected delivery sent, got %v", deliveries)
	}
}

// flakySender wraps a sender and fails SendText for texts listed in failing.
type flakySender struct {
	wa.Sender
	failing map[string]bool
	calls   map[string]int
}

func (f *flakySender) SendText(ctx context.Context, jid, text string) (string, error) {
	f.calls[text]++
	if f.failing[text] {
		return "", errors.New("simulated send failure")
	}
	return f.Sender.SendText(ctx, jid, text)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func newQueueFixture(t *testing.T, failing ...string) (*Dispatcher, *store.Store, *flakySender, *clock, string) {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "queue.db"))
	if err != nil {
		t.Fatalf("Open store failed: %v", err)
	}
	t.Cleanup(func() { s.Close() })

	r, err := s.AddRecipient(context.Background(), "Admin", "+201000000004", "201000000004@s.whatsapp.net", 7)
	if err != nil {
		t.Fatalf("AddRecipient failed: %v", err)
	}

	sender := &flakySender{Sender: wa.NewFakeSender(""), failing: map[string]bool{}, calls: map[string]int{}}
	for _, text := range failing {
		sender.failing[text] = true
	}
	clk := &clock{t: time.Now().UTC()}
	d := NewDispatcher(s, sender)
	d.now = clk.now
	return d, s, sender, clk, r.ID
}

func enqueue(t *testing.T, s *store.Store, recipientID, text string) *store.Message {
	t.Helper()
	msg, err := s.CreateMessage(context.Background(), store.LevelInfo, "test", text, "", store.ModeText, "", store.StatusQueued, []string{recipientID})
	if err != nil {
		t.Fatalf("CreateMessage failed: %v", err)
	}
	time.Sleep(2 * time.Millisecond)
	return msg
}

func TestDispatcherBackoffAndMaxAttempts(t *testing.T) {
	d, s, sender, clk, rid := newQueueFixture(t, "doomed")
	ctx := context.Background()
	msg := enqueue(t, s, rid, "doomed")

	for attempt := 1; attempt <= MaxAttempts; attempt++ {
		d.processNext()
		got, err := s.GetMessage(ctx, msg.ID)
		if err != nil {
			t.Fatalf("GetMessage failed: %v", err)
		}
		if got.Attempts != attempt {
			t.Fatalf("after attempt %d: attempts = %d", attempt, got.Attempts)
		}
		if attempt == MaxAttempts {
			if got.Status != store.StatusFailed {
				t.Fatalf("expected failed after %d attempts, got %s", MaxAttempts, got.Status)
			}
			break
		}
		if got.Status != store.StatusQueued {
			t.Fatalf("attempt %d: expected queued for retry, got %s", attempt, got.Status)
		}

		// Not due one second before the scheduled delay: no new attempt.
		delay := BackoffSchedule[attempt-1]
		clk.t = clk.t.Add(delay - time.Second)
		d.processNext()
		if calls := sender.calls["doomed"]; calls != attempt {
			t.Fatalf("attempt %d: message retried before its backoff elapsed (calls=%d)", attempt, calls)
		}
		clk.t = clk.t.Add(time.Second)
	}

	if calls := sender.calls["doomed"]; calls != MaxAttempts {
		t.Errorf("expected %d send calls, got %d", MaxAttempts, calls)
	}
}

func TestDispatcherFailingMessageDoesNotBlockOthers(t *testing.T) {
	d, s, sender, _, rid := newQueueFixture(t, "bad")
	ctx := context.Background()
	bad := enqueue(t, s, rid, "bad")
	good := enqueue(t, s, rid, "good")

	d.processNext() // bad fails and is scheduled for later
	d.processNext() // good is due and must go out

	gotGood, _ := s.GetMessage(ctx, good.ID)
	if gotGood.Status != store.StatusSent {
		t.Fatalf("expected the later message to be sent, got %s", gotGood.Status)
	}
	gotBad, _ := s.GetMessage(ctx, bad.ID)
	if gotBad.Status != store.StatusQueued || gotBad.Attempts != 1 {
		t.Fatalf("unexpected state of the failing message: %+v", gotBad)
	}
	if sender.calls["bad"] != 1 {
		t.Errorf("failing message was retried immediately (calls=%d)", sender.calls["bad"])
	}
}

func TestDispatcherResumesSendingMessageAfterRestart(t *testing.T) {
	d, s, _, _, rid := newQueueFixture(t)
	ctx := context.Background()
	msg := enqueue(t, s, rid, "resume me")
	if err := s.UpdateMessageStatus(ctx, msg.ID, store.StatusSending, ""); err != nil {
		t.Fatalf("UpdateMessageStatus failed: %v", err)
	}

	d.processNext()

	got, _ := s.GetMessage(ctx, msg.ID)
	if got.Status != store.StatusSent || got.Attempts != 1 {
		t.Fatalf("expected resumed message sent after one attempt, got %+v", got)
	}
}

func TestDispatcherDisabledRecipientFailsAfterMaxAttempts(t *testing.T) {
	d, s, _, clk, rid := newQueueFixture(t)
	ctx := context.Background()
	r, err := s.GetRecipient(ctx, rid)
	if err != nil {
		t.Fatalf("GetRecipient failed: %v", err)
	}
	if err := s.UpdateRecipient(ctx, r.ID, r.Name, r.PhoneE164, r.JID, false, 7); err != nil {
		t.Fatalf("UpdateRecipient failed: %v", err)
	}
	msg := enqueue(t, s, rid, "nobody home")

	for i := 0; i < MaxAttempts; i++ {
		d.processNext()
		clk.t = clk.t.Add(time.Hour)
	}

	got, _ := s.GetMessage(ctx, msg.ID)
	if got.Status != store.StatusFailed || got.Attempts != MaxAttempts {
		t.Fatalf("expected failed with %d attempts, got %+v", MaxAttempts, got)
	}
}
