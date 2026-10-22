// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"context"
)

type ConnectionState string

const (
	StateUnlinked     ConnectionState = "unlinked"
	StateConnecting   ConnectionState = "connecting"
	StateConnected    ConnectionState = "connected"
	StateReconnecting ConnectionState = "reconnecting"
	StateLoggedOut    ConnectionState = "logged_out"
	// StateOutdated means WhatsApp rejects this version of the library; an update is needed.
	StateOutdated ConnectionState = "outdated"
)

type Sender interface {
	SendText(ctx context.Context, recipientJID string, text string) (waMessageID string, err error)
	SendVoice(ctx context.Context, recipientJID string, oggBytes []byte, durationSec int, waveform []byte) (waMessageID string, err error)
	IsOnWhatsApp(ctx context.Context, phoneE164 string) (isOnWhatsApp bool, jid string, err error)
	GetState() ConnectionState
	SubscribeState() <-chan ConnectionState
	GetLatestQR() string
	// LinkedPhone returns the digits of the linked phone number, or "" when not linked.
	LinkedPhone() string
	// RequestPairing starts a fresh pairing when the device is not linked and none is running.
	RequestPairing(ctx context.Context) (PairingResult, error)
	PairingStatus() PairingStatus
	Disconnect()
	Logout(ctx context.Context) error
}
