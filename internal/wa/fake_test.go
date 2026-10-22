// SPDX-License-Identifier: GPL-3.0-only

//go:build devfake

package wa

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestFakeSenderStateAndSending(t *testing.T) {
	logFile := filepath.Join(t.TempDir(), "fake-log.jsonl")
	fake := NewFakeSender(logFile)

	sub := fake.SubscribeState()

	if fake.GetState() != StateConnected {
		t.Errorf("expected connected, got %s", fake.GetState())
	}

	fake.SetState(StateReconnecting)
	select {
	case s := <-sub:
		if s != StateReconnecting {
			t.Errorf("expected reconnecting on channel, got %s", s)
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timeout waiting for state channel")
	}

	if _, err := fake.SendText(context.Background(), "123@s.whatsapp.net", "hello"); err == nil {
		t.Errorf("expected error when sending while disconnected, got nil")
	}

	fake.SetState(StateConnected)
	id, err := fake.SendText(context.Background(), "123@s.whatsapp.net", "hello")
	if err != nil || id == "" {
		t.Fatalf("SendText failed: %v", err)
	}

	records := fake.GetRecords()
	if len(records) != 1 {
		t.Fatalf("expected 1 record, got %d", len(records))
	}
	if records[0].Text != "hello" {
		t.Errorf("expected 'hello', got %s", records[0].Text)
	}

	data, err := os.ReadFile(logFile)
	if err != nil || len(data) == 0 {
		t.Fatalf("expected log file content, got err=%v", err)
	}
}
