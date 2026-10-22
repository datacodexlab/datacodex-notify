// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"testing"
	"time"

	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"
)

type recordingCallback struct {
	receipts []string
	inbound  []string
	onEvent  func()
}

func (r *recordingCallback) OnDeliveryReceipt(waMessageID string, status string) {
	if r.onEvent != nil {
		r.onEvent()
	}
	r.receipts = append(r.receipts, waMessageID+":"+status)
}

func (r *recordingCallback) OnInboundMessage(senderJID, waMessageID, text string) {
	if r.onEvent != nil {
		r.onEvent()
	}
	r.inbound = append(r.inbound, senderJID+"|"+waMessageID+"|"+text)
}

func newEventClient(cb EventCallback) *Client {
	c, _ := NewClient("unused.db", cb)
	return c
}

func TestReceiptMapping(t *testing.T) {
	tests := []struct {
		name string
		typ  types.ReceiptType
		want string
		ok   bool
	}{
		{"default is delivered", types.ReceiptTypeDelivered, "delivered", true},
		{"read", types.ReceiptTypeRead, "read", true},
		{"read self", types.ReceiptTypeReadSelf, "read", true},
		{"retry ignored", types.ReceiptTypeRetry, "", false},
		{"sender ignored", types.ReceiptTypeSender, "", false},
		{"played ignored", types.ReceiptTypePlayed, "", false},
		{"unknown ignored", types.ReceiptType("server-error"), "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := receiptStatus(tc.typ)
			if got != tc.want || ok != tc.ok {
				t.Errorf("receiptStatus(%q) = (%q, %v), want (%q, %v)", tc.typ, got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestHandleReceiptEventForwardsOnlyRelevantTypes(t *testing.T) {
	cb := &recordingCallback{}
	c := newEventClient(cb)

	c.handleEvent(&events.Receipt{Type: types.ReceiptTypeDelivered, MessageIDs: []types.MessageID{"A"}})
	c.handleEvent(&events.Receipt{Type: types.ReceiptTypeRetry, MessageIDs: []types.MessageID{"B"}})
	c.handleEvent(&events.Receipt{Type: types.ReceiptTypeRead, MessageIDs: []types.MessageID{"C", "D"}})

	want := []string{"A:delivered", "C:read", "D:read"}
	if len(cb.receipts) != len(want) {
		t.Fatalf("receipts = %v, want %v", cb.receipts, want)
	}
	for i := range want {
		if cb.receipts[i] != want[i] {
			t.Errorf("receipts[%d] = %s, want %s", i, cb.receipts[i], want[i])
		}
	}
}

func messageEvent(chat, sender types.JID, fromMe bool, text string) *events.Message {
	return &events.Message{
		Info: types.MessageInfo{
			MessageSource: types.MessageSource{Chat: chat, Sender: sender, IsFromMe: fromMe},
			ID:            "MSG1",
		},
		Message: &waE2E.Message{Conversation: proto.String(text)},
	}
}

func TestInboxAcceptsOnlyDirectChats(t *testing.T) {
	user := types.NewJID("201000000005", types.DefaultUserServer)
	lid := types.NewJID("987654321", types.HiddenUserServer)
	group := types.NewJID("12345-67890", types.GroupServer)
	newsletter := types.NewJID("555", types.NewsletterServer)

	tests := []struct {
		name   string
		evt    *events.Message
		stored bool
	}{
		{"direct phone chat", messageEvent(user, user, false, "hi"), true},
		{"direct lid chat", messageEvent(lid, lid, false, "hi"), true},
		{"group chat", messageEvent(group, user, false, "hi"), false},
		{"status broadcast", messageEvent(types.StatusBroadcastJID, user, false, "hi"), false},
		{"newsletter", messageEvent(newsletter, newsletter, false, "hi"), false},
		{"own message", messageEvent(user, user, true, "hi"), false},
		{"empty text", messageEvent(user, user, false, ""), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cb := &recordingCallback{}
			newEventClient(cb).handleEvent(tc.evt)
			if got := len(cb.inbound) == 1; got != tc.stored {
				t.Errorf("stored = %v (%v), want %v", got, cb.inbound, tc.stored)
			}
		})
	}
}

func TestCallbacksRunWithoutHoldingClientLock(t *testing.T) {
	cb := &recordingCallback{}
	c := newEventClient(cb)
	cb.onEvent = func() { _ = c.GetState() } // would deadlock if c.mu were held

	user := types.NewJID("201000000005", types.DefaultUserServer)
	done := make(chan struct{})
	go func() {
		c.handleEvent(&events.Receipt{Type: types.ReceiptTypeRead, MessageIDs: []types.MessageID{"A"}})
		c.handleEvent(messageEvent(user, user, false, "hi"))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("callback deadlocked: client lock held while calling back")
	}
}

func TestConnectionStateTransitions(t *testing.T) {
	c := newEventClient(nil)
	sub := c.SubscribeState()

	expect := func(want ConnectionState) {
		t.Helper()
		if got := c.GetState(); got != want {
			t.Fatalf("state = %s, want %s", got, want)
		}
		select {
		case s := <-sub:
			if s != want {
				t.Fatalf("broadcast = %s, want %s", s, want)
			}
		case <-time.After(time.Second):
			t.Fatalf("no broadcast for %s", want)
		}
	}

	c.handleEvent(&events.Disconnected{})
	if got := c.GetState(); got != StateUnlinked {
		t.Fatalf("disconnect while unlinked changed state to %s", got)
	}

	c.handleEvent(&events.Connected{})
	expect(StateConnected)
	c.handleEvent(&events.Disconnected{})
	expect(StateReconnecting)
	c.handleEvent(&events.Connected{})
	expect(StateConnected)
	c.handleEvent(&events.LoggedOut{})
	expect(StateLoggedOut)
}
