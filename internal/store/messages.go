// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
)

var (
	ErrMessageNotFound  = errors.New("message not found")
	ErrMessageNotFailed = errors.New("message is not in the failed state")
)

func (s *Store) CreateMessage(ctx context.Context, level Level, source, text, speech string, mode Mode, dedupeKey string, status MessageStatus, recipientIDs []string) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	msg := &Message{
		ID:        uuid.New().String(),
		CreatedAt: time.Now().UTC(),
		Level:     level,
		Source:    source,
		Text:      text,
		Speech:    speech,
		Mode:      mode,
		DedupeKey: dedupeKey,
		Status:    status,
		Attempts:  0,
	}

	msgQuery := `
	INSERT INTO messages (id, created_at, level, source, text, speech, mode, dedupe_key, status, attempts)
	VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, 0);`

	_, err = tx.ExecContext(ctx, msgQuery, msg.ID, msg.CreatedAt, msg.Level, msg.Source, msg.Text, msg.Speech, msg.Mode, msg.DedupeKey, msg.Status)
	if err != nil {
		return nil, fmt.Errorf("failed to insert message: %w", err)
	}

	for _, rID := range recipientIDs {
		// Determine delivery kind based on mode
		kinds := []string{"text"}
		if mode == ModeVoice {
			kinds = []string{"voice"}
		} else if mode == ModeBoth {
			kinds = []string{"text", "voice"}
		}

		for _, kind := range kinds {
			delID := uuid.New().String()
			delQuery := `
			INSERT INTO deliveries (id, message_id, recipient_id, kind, status, updated_at)
			VALUES (?, ?, ?, ?, ?, ?);`
			_, err = tx.ExecContext(ctx, delQuery, delID, msg.ID, rID, kind, StatusQueued, msg.CreatedAt)
			if err != nil {
				return nil, fmt.Errorf("failed to insert delivery: %w", err)
			}
		}
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("failed to commit message creation: %w", err)
	}
	return msg, nil
}

func (s *Store) GetMessage(ctx context.Context, id string) (*Message, error) {
	query := `
	SELECT id, created_at, level, source, text, COALESCE(speech, ''), mode, COALESCE(dedupe_key, ''), status, attempts, COALESCE(last_error, ''), sent_at
	FROM messages WHERE id = ?;`

	row := s.db.QueryRowContext(ctx, query, id)
	var m Message
	var sentAt sql.NullTime
	if err := row.Scan(&m.ID, &m.CreatedAt, &m.Level, &m.Source, &m.Text, &m.Speech, &m.Mode, &m.DedupeKey, &m.Status, &m.Attempts, &m.LastError, &sentAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan message: %w", err)
	}
	if sentAt.Valid {
		m.SentAt = &sentAt.Time
	}
	return &m, nil
}

func (s *Store) UpdateMessageStatus(ctx context.Context, id string, status MessageStatus, lastError string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sentAt *time.Time
	if status == StatusSent || status == StatusDelivered {
		now := time.Now().UTC()
		sentAt = &now
	}

	query := `
	UPDATE messages
	SET status = ?, last_error = ?, sent_at = CASE WHEN ? IS NOT NULL THEN ? ELSE sent_at END
	WHERE id = ?;`

	_, err := s.db.ExecContext(ctx, query, status, lastError, sentAt, sentAt, id)
	if err != nil {
		return fmt.Errorf("failed to update message status: %w", err)
	}
	return nil
}

// RecordSendAttempt stores the outcome of one real send attempt: it increments
// attempts exactly once and schedules the next try (nil clears the schedule).
func (s *Store) RecordSendAttempt(ctx context.Context, id string, status MessageStatus, lastError string, nextAttemptAt *time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var sentAt *time.Time
	if status == StatusSent {
		now := time.Now().UTC()
		sentAt = &now
	}

	query := `
	UPDATE messages
	SET status = ?, last_error = ?, attempts = attempts + 1, next_attempt_at = ?,
	    sent_at = CASE WHEN ? IS NOT NULL THEN ? ELSE sent_at END
	WHERE id = ?;`

	if _, err := s.db.ExecContext(ctx, query, status, lastError, nextAttemptAt, sentAt, sentAt, id); err != nil {
		return fmt.Errorf("failed to record send attempt: %w", err)
	}
	return nil
}

// HasQueuedMessages reports whether any message is still waiting to be sent,
// including those whose next attempt is scheduled in the future.
func (s *Store) HasQueuedMessages(ctx context.Context) (bool, error) {
	var n int
	row := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM messages WHERE status IN ('queued', 'sending');`)
	if err := row.Scan(&n); err != nil {
		return false, fmt.Errorf("failed to count queued messages: %w", err)
	}
	return n > 0, nil
}

// GetNextQueuedMessage returns the oldest queued message whose next attempt is
// due at now. Messages waiting for a backoff delay are skipped so one failing
// message never blocks the ones behind it.
func (s *Store) GetNextQueuedMessage(ctx context.Context, now time.Time) (*Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	SELECT id, created_at, level, source, text, COALESCE(speech, ''), mode, COALESCE(dedupe_key, ''), status, attempts, COALESCE(last_error, ''), sent_at
	FROM messages
	WHERE status IN ('queued', 'sending')
	AND (next_attempt_at IS NULL OR next_attempt_at <= ?)
	ORDER BY created_at ASC
	LIMIT 1;`

	row := s.db.QueryRowContext(ctx, query, now.UTC())
	var m Message
	var sentAt sql.NullTime
	if err := row.Scan(&m.ID, &m.CreatedAt, &m.Level, &m.Source, &m.Text, &m.Speech, &m.Mode, &m.DedupeKey, &m.Status, &m.Attempts, &m.LastError, &sentAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get queued message: %w", err)
	}
	if sentAt.Valid {
		m.SentAt = &sentAt.Time
	}
	return &m, nil
}

func (s *Store) GetDeliveriesForMessage(ctx context.Context, messageID string) ([]*Delivery, error) {
	query := `
	SELECT id, message_id, recipient_id, kind, COALESCE(wa_message_id, ''), status, updated_at
	FROM deliveries 
	WHERE message_id = ?
	ORDER BY updated_at ASC, CASE kind WHEN 'text' THEN 0 ELSE 1 END, id ASC;`

	rows, err := s.db.QueryContext(ctx, query, messageID)
	if err != nil {
		return nil, fmt.Errorf("failed to query deliveries: %w", err)
	}
	defer rows.Close()

	var list []*Delivery
	for rows.Next() {
		var d Delivery
		if err := rows.Scan(&d.ID, &d.MessageID, &d.RecipientID, &d.Kind, &d.WAMessageID, &d.Status, &d.UpdatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan delivery: %w", err)
		}
		list = append(list, &d)
	}
	return list, nil
}

func (s *Store) UpdateDeliveryStatus(ctx context.Context, deliveryID string, waMessageID string, status MessageStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	UPDATE deliveries 
	SET wa_message_id = CASE WHEN ? != '' THEN ? ELSE wa_message_id END, status = ?, updated_at = ?
	WHERE id = ?;`

	_, err := s.db.ExecContext(ctx, query, waMessageID, waMessageID, status, time.Now().UTC(), deliveryID)
	if err != nil {
		return fmt.Errorf("failed to update delivery status: %w", err)
	}
	return nil
}

// statusRankSQL ranks the statuses a receipt may move through, so a late
// "delivered" receipt never overwrites "read" and failed rows are not revived.
const statusRankSQL = `CASE %s WHEN 'sent' THEN 1 WHEN 'delivered' THEN 2 WHEN 'read' THEN 3 ELSE 0 END`

// ApplyReceipt moves the deliveries of waMessageID, and their messages, forward
// to status (delivered or read). It never moves a status backward.
func (s *Store) ApplyReceipt(ctx context.Context, waMessageID string, status MessageStatus) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	rankOf := func(col string) string { return fmt.Sprintf(statusRankSQL, col) }
	newRank := rankOf("?")

	messageQuery := fmt.Sprintf(`
	UPDATE messages SET status = ?
	WHERE id IN (SELECT message_id FROM deliveries WHERE wa_message_id = ?)
	AND status IN ('sent', 'delivered') AND %s < %s;`, rankOf("status"), newRank)
	if _, err := tx.ExecContext(ctx, messageQuery, status, waMessageID, status); err != nil {
		return fmt.Errorf("failed to apply receipt to message: %w", err)
	}

	deliveryQuery := fmt.Sprintf(`
	UPDATE deliveries SET status = ?, updated_at = ?
	WHERE wa_message_id = ? AND status IN ('sent', 'delivered') AND %s < %s;`, rankOf("status"), newRank)
	if _, err := tx.ExecContext(ctx, deliveryQuery, status, time.Now().UTC(), waMessageID, status); err != nil {
		return fmt.Errorf("failed to apply receipt to delivery: %w", err)
	}
	return tx.Commit()
}

func (s *Store) FindDuplicateMessage(ctx context.Context, dedupeKey string, since time.Time) (*Message, error) {
	query := `
	SELECT id, created_at, level, source, text, COALESCE(speech, ''), mode, COALESCE(dedupe_key, ''), status, attempts, COALESCE(last_error, ''), sent_at
	FROM messages 
	WHERE dedupe_key = ? AND created_at >= ?
	AND status NOT IN ('deduplicated', 'rate_limited', 'failed', 'digested')
	ORDER BY created_at DESC 
	LIMIT 1;`

	row := s.db.QueryRowContext(ctx, query, dedupeKey, since.UTC())
	var m Message
	var sentAt sql.NullTime
	if err := row.Scan(&m.ID, &m.CreatedAt, &m.Level, &m.Source, &m.Text, &m.Speech, &m.Mode, &m.DedupeKey, &m.Status, &m.Attempts, &m.LastError, &sentAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to query duplicate message: %w", err)
	}
	if sentAt.Valid {
		m.SentAt = &sentAt.Time
	}
	return &m, nil
}

func (s *Store) ListRecentMessages(ctx context.Context, limit int, levelFilter string, statusFilter string) ([]*Message, error) {
	query := `
	SELECT id, created_at, level, source, text, COALESCE(speech, ''), mode, COALESCE(dedupe_key, ''), status, attempts, COALESCE(last_error, ''), sent_at
	FROM messages
	WHERE (? = '' OR level = ?)
	AND (? = '' OR status = ?)
	ORDER BY created_at DESC
	LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, query, levelFilter, levelFilter, statusFilter, statusFilter, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to list recent messages: %w", err)
	}
	defer rows.Close()

	var list []*Message
	for rows.Next() {
		var m Message
		var sentAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.CreatedAt, &m.Level, &m.Source, &m.Text, &m.Speech, &m.Mode, &m.DedupeKey, &m.Status, &m.Attempts, &m.LastError, &sentAt); err != nil {
			return nil, fmt.Errorf("failed to scan recent message: %w", err)
		}
		if sentAt.Valid {
			m.SentAt = &sentAt.Time
		}
		list = append(list, &m)
	}
	return list, nil
}

func (s *Store) GetRecentMessageCount(ctx context.Context, recipientID string, since time.Time) (int, error) {
	query := `
	SELECT COUNT(DISTINCT m.id)
	FROM messages m
	JOIN deliveries d ON d.message_id = m.id
	WHERE d.recipient_id = ?
	AND m.created_at >= ?
	AND m.level != 'critical';`

	row := s.db.QueryRowContext(ctx, query, recipientID, since.UTC())
	var count int
	if err := row.Scan(&count); err != nil {
		return 0, fmt.Errorf("failed to count recent messages: %w", err)
	}
	return count, nil
}

// RetryMessage puts a failed message (and its failed deliveries) back in the
// queue with a fresh attempt counter.
func (s *Store) RetryMessage(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	var status MessageStatus
	if err := tx.QueryRowContext(ctx, `SELECT status FROM messages WHERE id = ?;`, id).Scan(&status); err != nil {
		if err == sql.ErrNoRows {
			return ErrMessageNotFound
		}
		return fmt.Errorf("failed to read message: %w", err)
	}
	if status != StatusFailed {
		return ErrMessageNotFailed
	}

	if _, err := tx.ExecContext(ctx,
		`UPDATE messages SET status = ?, attempts = 0, last_error = NULL, next_attempt_at = NULL WHERE id = ?;`,
		StatusQueued, id); err != nil {
		return fmt.Errorf("failed to requeue message: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE deliveries SET status = ?, updated_at = ? WHERE message_id = ? AND status = ?;`,
		StatusQueued, time.Now().UTC(), id, StatusFailed); err != nil {
		return fmt.Errorf("failed to requeue deliveries: %w", err)
	}
	return tx.Commit()
}

// AddDelivery records an extra delivery for a message, for example the text
// fallback that replaces a voice note that could not be produced.
func (s *Store) AddDelivery(ctx context.Context, messageID, recipientID, kind, waMessageID string, status MessageStatus) (*Delivery, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	d := &Delivery{
		ID:          uuid.New().String(),
		MessageID:   messageID,
		RecipientID: recipientID,
		Kind:        kind,
		WAMessageID: waMessageID,
		Status:      status,
		UpdatedAt:   time.Now().UTC(),
	}
	_, err := s.db.ExecContext(ctx, `
	INSERT INTO deliveries (id, message_id, recipient_id, kind, wa_message_id, status, updated_at)
	VALUES (?, ?, ?, ?, NULLIF(?, ''), ?, ?);`,
		d.ID, d.MessageID, d.RecipientID, d.Kind, d.WAMessageID, d.Status, d.UpdatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert delivery: %w", err)
	}
	return d, nil
}

// ListMessagesByStatus returns the messages in a status, oldest first.
func (s *Store) ListMessagesByStatus(ctx context.Context, status MessageStatus) ([]*Message, error) {
	query := `
	SELECT id, created_at, level, source, text, COALESCE(speech, ''), mode, COALESCE(dedupe_key, ''), status, attempts, COALESCE(last_error, ''), sent_at
	FROM messages
	WHERE status = ?
	ORDER BY created_at ASC;`

	rows, err := s.db.QueryContext(ctx, query, status)
	if err != nil {
		return nil, fmt.Errorf("failed to list messages by status: %w", err)
	}
	defer rows.Close()

	var list []*Message
	for rows.Next() {
		var m Message
		var sentAt sql.NullTime
		if err := rows.Scan(&m.ID, &m.CreatedAt, &m.Level, &m.Source, &m.Text, &m.Speech, &m.Mode, &m.DedupeKey, &m.Status, &m.Attempts, &m.LastError, &sentAt); err != nil {
			return nil, fmt.Errorf("failed to scan message: %w", err)
		}
		if sentAt.Valid {
			m.SentAt = &sentAt.Time
		}
		list = append(list, &m)
	}
	return list, nil
}

// LastSentAt returns when a message was last sent, or nil when none was.
func (s *Store) LastSentAt(ctx context.Context) (*time.Time, error) {
	var raw sql.NullString
	if err := s.db.QueryRowContext(ctx, `SELECT MAX(sent_at) FROM messages WHERE sent_at IS NOT NULL;`).Scan(&raw); err != nil {
		return nil, fmt.Errorf("failed to read the last sent time: %w", err)
	}
	if !raw.Valid || raw.String == "" {
		return nil, nil
	}
	var at time.Time
	row := s.db.QueryRowContext(ctx, `SELECT sent_at FROM messages WHERE sent_at IS NOT NULL ORDER BY sent_at DESC LIMIT 1;`)
	if err := row.Scan(&at); err != nil {
		return nil, fmt.Errorf("failed to read the last sent time: %w", err)
	}
	return &at, nil
}
