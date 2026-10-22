// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
)

// handleEvent reacts to whatsmeow events. State is changed through setState,
// and callbacks run without c.mu held so they may call back into the client.
func (c *Client) handleEvent(evt interface{}) {
	switch v := evt.(type) {
	case *events.Connected:
		c.setState(StateConnected, true)

	case *events.Disconnected:
		c.markReconnecting()

	case *events.ClientOutdated:
		c.setOutdated()

	case *events.ConnectFailure:
		if v.Reason == events.ConnectFailureClientOutdated {
			c.setOutdated()
		}

	case *events.LoggedOut:
		c.setState(StateLoggedOut, true)

	case *events.Receipt:
		if c.callback == nil {
			return
		}
		status, ok := receiptStatus(v.Type)
		if !ok {
			return
		}
		for _, id := range v.MessageIDs {
			c.callback.OnDeliveryReceipt(string(id), status)
		}

	case *events.Message:
		if c.callback == nil || v.Info.IsFromMe || !isDirectChat(v.Info.Chat) {
			return
		}
		text := v.Message.GetConversation()
		if text == "" && v.Message.GetExtendedTextMessage() != nil {
			text = v.Message.GetExtendedTextMessage().GetText()
		}
		if text != "" {
			c.callback.OnInboundMessage(v.Info.Sender.ToNonAD().String(), string(v.Info.ID), text)
		}
	}
}

// receiptStatus maps a receipt type to a delivery status. Only delivered and
// read receipts matter; retry, sender, played, and other types are ignored.
func receiptStatus(t types.ReceiptType) (string, bool) {
	switch t {
	case types.ReceiptTypeDelivered:
		return "delivered", true
	case types.ReceiptTypeRead, types.ReceiptTypeReadSelf:
		return "read", true
	default:
		return "", false
	}
}

// isDirectChat reports whether chat is a 1:1 user chat. Groups, broadcast
// lists (including status@broadcast), and newsletters are not.
func isDirectChat(chat types.JID) bool {
	return chat.Server == types.DefaultUserServer || chat.Server == types.HiddenUserServer
}

func (c *Client) setState(s ConnectionState, clearQR bool) {
	c.mu.Lock()
	c.state = s
	if clearQR {
		c.latestQR = ""
	}
	chans := append([]chan ConnectionState(nil), c.stateChans...)
	c.mu.Unlock()

	broadcast(chans, s)
}

func (c *Client) markReconnecting() {
	c.mu.Lock()
	if c.state != StateConnected {
		c.mu.Unlock()
		return
	}
	c.state = StateReconnecting
	chans := append([]chan ConnectionState(nil), c.stateChans...)
	c.mu.Unlock()

	broadcast(chans, StateReconnecting)
}

func broadcast(chans []chan ConnectionState, s ConnectionState) {
	for _, ch := range chans {
		select {
		case ch <- s:
		default:
		}
	}
}
