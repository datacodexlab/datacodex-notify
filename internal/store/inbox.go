// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) InsertInboxItem(ctx context.Context, fromRecipientID, waMessageID, kind, text string) (*InboxItem, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	item := &InboxItem{
		ID:              uuid.New().String(),
		ReceivedAt:      time.Now().UTC(),
		FromRecipientID: fromRecipientID,
		WAMessageID:     waMessageID,
		Kind:            kind,
		Text:            text,
		State:           "new",
	}

	query := `
	INSERT INTO inbox (id, received_at, from_recipient_id, wa_message_id, kind, text, state)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(wa_message_id) DO NOTHING;`

	_, err := s.db.ExecContext(ctx, query, item.ID, item.ReceivedAt, item.FromRecipientID, item.WAMessageID, item.Kind, item.Text, item.State)
	if err != nil {
		return nil, fmt.Errorf("failed to insert inbox item: %w", err)
	}
	return item, nil
}

func (s *Store) ListInbox(ctx context.Context, limit int) ([]*InboxItem, error) {
	query := `
	SELECT id, received_at, COALESCE(from_recipient_id, ''), COALESCE(wa_message_id, ''), kind, text, state
	FROM inbox 
	ORDER BY received_at DESC 
	LIMIT ?;`

	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("failed to query inbox: %w", err)
	}
	defer rows.Close()

	var list []*InboxItem
	for rows.Next() {
		var item InboxItem
		if err := rows.Scan(&item.ID, &item.ReceivedAt, &item.FromRecipientID, &item.WAMessageID, &item.Kind, &item.Text, &item.State); err != nil {
			return nil, fmt.Errorf("failed to scan inbox item: %w", err)
		}
		list = append(list, &item)
	}
	return list, nil
}
