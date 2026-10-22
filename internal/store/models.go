// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"time"
)

type Level string

const (
	LevelInfo     Level = "info"
	LevelWarning  Level = "warning"
	LevelCritical Level = "critical"
)

type Mode string

const (
	ModeAuto  Mode = "auto"
	ModeText  Mode = "text"
	ModeVoice Mode = "voice"
	ModeBoth  Mode = "both"
)

type MessageStatus string

const (
	StatusQueued         MessageStatus = "queued"
	StatusSending        MessageStatus = "sending"
	StatusSent           MessageStatus = "sent"
	StatusDelivered      MessageStatus = "delivered"
	StatusRead           MessageStatus = "read"
	StatusFailed         MessageStatus = "failed"
	StatusVoiceFailed    MessageStatus = "voice_failed"
	StatusDigested       MessageStatus = "digested"
	StatusDeduplicated   MessageStatus = "deduplicated"
	StatusRateLimited    MessageStatus = "rate_limited"
	StatusHeldQuietHours MessageStatus = "held_quiet_hours"
	StatusHeldPaused     MessageStatus = "held_paused"
)

type Recipient struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	PhoneE164 string    `json:"phone_e164"`
	JID       string    `json:"jid"`
	Enabled   bool      `json:"enabled"`
	Levels    int       `json:"levels"` // Bitmask: 1=info, 2=warning, 4=critical
	CreatedAt time.Time `json:"created_at"`
}

type Message struct {
	ID        string        `json:"id"`
	CreatedAt time.Time     `json:"created_at"`
	Level     Level         `json:"level"`
	Source    string        `json:"source"`
	Text      string        `json:"text"`
	Speech    string        `json:"speech,omitempty"`
	Mode      Mode          `json:"mode"`
	DedupeKey string        `json:"dedupe_key,omitempty"`
	Status    MessageStatus `json:"status"`
	Attempts  int           `json:"attempts"`
	LastError string        `json:"last_error,omitempty"`
	SentAt    *time.Time    `json:"sent_at,omitempty"`
}

type Delivery struct {
	ID          string        `json:"id"`
	MessageID   string        `json:"message_id"`
	RecipientID string        `json:"recipient_id"`
	Kind        string        `json:"kind"` // "text" or "voice"
	WAMessageID string        `json:"wa_message_id,omitempty"`
	Status      MessageStatus `json:"status"`
	UpdatedAt   time.Time     `json:"updated_at"`
}

type Heartbeat struct {
	ID         string    `json:"id"`
	Label      string    `json:"label"`
	Source     string    `json:"source"`
	Session    string    `json:"session"`
	IntervalS  int       `json:"interval_s"`
	GraceS     int       `json:"grace_s"`
	LastBeatAt time.Time `json:"last_beat_at"`
	State      string    `json:"state"` // "active", "alerted", "stopped"
}

type InboxItem struct {
	ID              string    `json:"id"`
	ReceivedAt      time.Time `json:"received_at"`
	FromRecipientID string    `json:"from_recipient_id,omitempty"`
	WAMessageID     string    `json:"wa_message_id"`
	Kind            string    `json:"kind"`
	Text            string    `json:"text"`
	State           string    `json:"state"` // "new", "read"
}

// AgentSession is an agent session that connected itself through the CLI.
type AgentSession struct {
	ID             string     `json:"id"`
	Source         string     `json:"source"`
	SessionName    string     `json:"session_name"`
	Project        string     `json:"project"`
	RulesFiles     []string   `json:"rules_files"`
	ConnectedAt    time.Time  `json:"connected_at"`
	LastSeenAt     *time.Time `json:"last_seen_at,omitempty"`
	LastMessageAt  *time.Time `json:"last_message_at,omitempty"`
	Muted          bool       `json:"muted"`
	DisconnectedAt *time.Time `json:"disconnected_at,omitempty"`
}
