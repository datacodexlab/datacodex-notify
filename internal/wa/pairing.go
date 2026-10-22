// SPDX-License-Identifier: GPL-3.0-only

package wa

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/socket"
)

// PairingResult is the outcome of RequestPairing.
type PairingResult string

const (
	// PairingStarted means a fresh pairing began and a QR code will appear shortly.
	PairingStarted PairingResult = "started"
	// PairingActive means a pairing is already running; nothing was changed.
	PairingActive PairingResult = "already_active"
	// PairingThrottled means a pairing was started less than pairThrottle ago.
	PairingThrottled PairingResult = "throttled"
	// PairingNotNeeded means the device is already linked.
	PairingNotNeeded PairingResult = "not_needed"
	// PairingOutdated means WhatsApp rejects this library version; pairing cannot work.
	PairingOutdated PairingResult = "outdated"
)

// PairingStatus describes the pairing the UI should show.
type PairingStatus struct {
	// Active is true while a pairing is running (a code is shown or about to be).
	Active bool
	// CodeExpiresAt is when the code on screen stops working; zero when there is no code.
	CodeExpiresAt time.Time
	// Expired is true after a pairing ended without a scan, until a new one starts.
	Expired bool
	// Started is true once a pairing has been started since the app launched.
	Started bool
}

// pairThrottle is the shortest gap between two pairing starts, so WhatsApp never
// sees a burst of pairing attempts.
const pairThrottle = 10 * time.Second

// pairingConn is the part of the WhatsApp connection the pairing flow uses.
type pairingConn interface {
	GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error)
	Connect() error
	Disconnect()
	Paired() bool
}

// whatsmeowConn adapts a whatsmeow client to pairingConn.
type whatsmeowConn struct{ client *whatsmeow.Client }

func (w whatsmeowConn) GetQRChannel(ctx context.Context) (<-chan whatsmeow.QRChannelItem, error) {
	return w.client.GetQRChannel(ctx)
}
func (w whatsmeowConn) Connect() error { return w.client.Connect() }
func (w whatsmeowConn) Disconnect()    { w.client.Disconnect() }
func (w whatsmeowConn) Paired() bool   { return w.client.Store.ID != nil }

// isOutdatedError reports whether a connect error is WhatsApp's "client outdated" rejection (HTTP 405).
func isOutdatedError(err error) bool {
	var statusErr socket.ErrWithStatusCode
	return errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusMethodNotAllowed
}

// RequestPairing starts a fresh pairing when the device is not linked and none is
// running. It is called when someone looks at the Connection screen, never in the
// background, and at most once per pairThrottle.
func (c *Client) RequestPairing(ctx context.Context) (PairingResult, error) {
	c.mu.Lock()
	conn := c.pairer
	switch {
	case conn == nil:
		c.mu.Unlock()
		return "", errors.New("whatsapp client not initialized")
	case c.state == StateOutdated:
		c.mu.Unlock()
		return PairingOutdated, nil
	case conn.Paired():
		c.mu.Unlock()
		return PairingNotNeeded, nil
	case c.pairActive:
		c.mu.Unlock()
		return PairingActive, nil
	case !c.lastPairStart.IsZero() && c.now().Sub(c.lastPairStart) < pairThrottle:
		c.mu.Unlock()
		return PairingThrottled, nil
	}
	c.pairActive = true
	c.pairExpired = false
	c.lastPairStart = c.now()
	runCtx := c.runCtx
	c.mu.Unlock()

	conn.Disconnect()
	qrChan, err := conn.GetQRChannel(runCtx)
	if err != nil {
		c.endPairing(false)
		return "", err
	}
	go c.listenQR(qrChan)

	if err := conn.Connect(); err != nil {
		if isOutdatedError(err) {
			c.setOutdated()
			return PairingOutdated, nil
		}
		c.endPairing(false)
		return "", err
	}
	return PairingStarted, nil
}

// PairingStatus reports the running pairing, the code on screen, and whether the last one expired.
func (c *Client) PairingStatus() PairingStatus {
	c.mu.RLock()
	defer c.mu.RUnlock()
	status := PairingStatus{Active: c.pairActive, Expired: c.pairExpired, Started: !c.lastPairStart.IsZero()}
	if c.latestQR != "" {
		status.CodeExpiresAt = c.codeExpiresAt
	}
	return status
}

// endPairing marks the running pairing as over. expired is true when it ended without a scan.
func (c *Client) endPairing(expired bool) {
	c.mu.Lock()
	c.pairActive = false
	c.latestQR = ""
	c.codeExpiresAt = time.Time{}
	if expired {
		c.pairExpired = true
	}
	c.mu.Unlock()
}

// listenQR follows one QR channel until it closes. The channel emits a new code
// when the previous one expires, then a final item (success, timeout, or an error).
func (c *Client) listenQR(qrChan <-chan whatsmeow.QRChannelItem) {
	for item := range qrChan {
		switch item.Event {
		case whatsmeow.QRChannelEventCode:
			c.mu.Lock()
			c.latestQR = item.Code
			c.codeExpiresAt = c.now().Add(item.Timeout)
			c.pairExpired = false
			c.mu.Unlock()
			c.setState(StateUnlinked, false)

		case whatsmeow.QRChannelSuccess.Event:
			c.endPairing(false)
			c.setState(StateConnected, true)

		case whatsmeow.QRChannelTimeout.Event:
			c.endPairing(true)

		case whatsmeow.QRChannelClientOutdated.Event:
			c.setOutdated()

		default:
			slog.Warn("WhatsApp pairing ended without a link", "event", item.Event)
			c.endPairing(true)
		}
	}
	// The channel is closed: whatever the last item was, no pairing is running any more.
	c.mu.Lock()
	c.pairActive = false
	c.mu.Unlock()
}

// setOutdated records that WhatsApp rejects this library version. It is logged once.
func (c *Client) setOutdated() {
	c.mu.Lock()
	already := c.state == StateOutdated
	c.pairActive = false
	c.latestQR = ""
	c.codeExpiresAt = time.Time{}
	c.mu.Unlock()

	if already {
		return
	}
	slog.Error("WhatsApp rejected this version of the app (client outdated): the WhatsApp library must be updated")
	c.setState(StateOutdated, true)
}
