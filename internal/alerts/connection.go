// SPDX-License-Identifier: GPL-3.0-only

// Package alerts reacts to WhatsApp connection changes: it opens the admin
// window when the account is logged out and warns when the connection was lost
// for a long time.
package alerts

import (
	"sync"
	"time"

	"datacodex-notify/internal/wa"
)

const (
	// LongDisconnect is how long the connection must stay down before a warning is sent.
	LongDisconnect = 5 * time.Minute
	// WindowThrottle is the minimum gap between two automatic window openings.
	WindowThrottle = 10 * time.Minute
)

// Options connects the watcher to the rest of the app. Now defaults to time.Now.
type Options struct {
	Now func() time.Time
	// OpenWindow shows the admin window (on the QR screen after a logout).
	OpenWindow func()
	// WarnDisconnect reports an outage that lasted more than LongDisconnect.
	WarnDisconnect func(from, to time.Time)
}

// Connection watches connection states. Call OnState for every state change.
type Connection struct {
	opts Options

	mu         sync.Mutex
	previous   wa.ConnectionState
	lostAt     time.Time
	lastWindow time.Time
}

// NewConnection creates a watcher that starts in the unlinked state.
func NewConnection(opts Options) *Connection {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Connection{opts: opts, previous: wa.StateUnlinked}
}

// OnState handles a state change.
func (c *Connection) OnState(state wa.ConnectionState) {
	now := c.opts.Now()

	c.mu.Lock()
	var warnFrom time.Time
	openWindow := false

	switch state {
	case wa.StateReconnecting:
		if c.previous == wa.StateConnected && c.lostAt.IsZero() {
			c.lostAt = now
		}
	case wa.StateConnected:
		if !c.lostAt.IsZero() && now.Sub(c.lostAt) > LongDisconnect {
			warnFrom = c.lostAt
		}
		c.lostAt = time.Time{}
	case wa.StateLoggedOut:
		// A logout is not an outage to report afterwards; it needs the QR screen instead.
		c.lostAt = time.Time{}
		if c.lastWindow.IsZero() || now.Sub(c.lastWindow) >= WindowThrottle {
			c.lastWindow = now
			openWindow = true
		}
	}
	c.previous = state
	c.mu.Unlock()

	// Callbacks run without the lock so they may call back into the app.
	if !warnFrom.IsZero() && c.opts.WarnDisconnect != nil {
		c.opts.WarnDisconnect(warnFrom, now)
	}
	if openWindow && c.opts.OpenWindow != nil {
		c.opts.OpenWindow()
	}
}
