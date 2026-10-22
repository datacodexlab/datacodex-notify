// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"os"
	"path/filepath"
	"testing"
)

type mockCallback struct {
	receipts []string
	inbound  []string
}

func (m *mockCallback) OnDeliveryReceipt(waMessageID string, status string) {
	m.receipts = append(m.receipts, waMessageID+":"+status)
}

func (m *mockCallback) OnInboundMessage(senderJID, waMessageID, text string) {
	m.inbound = append(m.inbound, senderJID+":"+text)
}

func TestClientLifecycleInit(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "wa-client-test-*")
	if err != nil {
		t.Fatalf("MkdirTemp failed: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "whatsapp.db")
	cb := &mockCallback{}
	client, err := NewClient(dbPath, cb)
	if err != nil {
		t.Fatalf("NewClient failed: %v", err)
	}

	if client.GetState() != StateUnlinked {
		t.Errorf("initial state should be unlinked, got %s", client.GetState())
	}
}
