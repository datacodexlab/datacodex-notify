// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
)

func (s *Store) AddRecipient(ctx context.Context, name, phoneE164, jid string, levels int) (*Recipient, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r := &Recipient{
		ID:        uuid.New().String(),
		Name:      name,
		PhoneE164: phoneE164,
		JID:       jid,
		Enabled:   true,
		Levels:    levels,
		CreatedAt: time.Now().UTC(),
	}

	query := `
	INSERT INTO recipients (id, name, phone_e164, jid, enabled, levels, created_at)
	VALUES (?, ?, ?, ?, ?, ?, ?);`

	_, err := s.db.ExecContext(ctx, query, r.ID, r.Name, r.PhoneE164, r.JID, 1, r.Levels, r.CreatedAt)
	if err != nil {
		return nil, fmt.Errorf("failed to insert recipient: %w", err)
	}
	return r, nil
}

func (s *Store) UpdateRecipient(ctx context.Context, id, name, phoneE164, jid string, enabled bool, levels int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	enabledInt := 0
	if enabled {
		enabledInt = 1
	}

	query := `
	UPDATE recipients 
	SET name = ?, phone_e164 = ?, jid = ?, enabled = ?, levels = ?
	WHERE id = ?;`

	res, err := s.db.ExecContext(ctx, query, name, phoneE164, jid, enabledInt, levels, id)
	if err != nil {
		return fmt.Errorf("failed to update recipient: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("recipient not found: %s", id)
	}
	return nil
}

func (s *Store) DeleteRecipient(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM recipients WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete recipient: %w", err)
	}
	return nil
}

func (s *Store) GetRecipient(ctx context.Context, id string) (*Recipient, error) {
	query := `SELECT id, name, phone_e164, jid, enabled, levels, created_at FROM recipients WHERE id = ?;`
	row := s.db.QueryRowContext(ctx, query, id)

	var r Recipient
	var enabledInt int
	if err := row.Scan(&r.ID, &r.Name, &r.PhoneE164, &r.JID, &enabledInt, &r.Levels, &r.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan recipient: %w", err)
	}
	r.Enabled = (enabledInt == 1)
	return &r, nil
}

func (s *Store) GetRecipientByJID(ctx context.Context, jid string) (*Recipient, error) {
	query := `SELECT id, name, phone_e164, jid, enabled, levels, created_at FROM recipients WHERE jid = ?;`
	row := s.db.QueryRowContext(ctx, query, jid)

	var r Recipient
	var enabledInt int
	if err := row.Scan(&r.ID, &r.Name, &r.PhoneE164, &r.JID, &enabledInt, &r.Levels, &r.CreatedAt); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan recipient: %w", err)
	}
	r.Enabled = (enabledInt == 1)
	return &r, nil
}

func (s *Store) ListRecipients(ctx context.Context) ([]*Recipient, error) {
	query := `SELECT id, name, phone_e164, jid, enabled, levels, created_at FROM recipients ORDER BY created_at ASC;`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query recipients: %w", err)
	}
	defer rows.Close()

	var list []*Recipient
	for rows.Next() {
		var r Recipient
		var enabledInt int
		if err := rows.Scan(&r.ID, &r.Name, &r.PhoneE164, &r.JID, &enabledInt, &r.Levels, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan recipient row: %w", err)
		}
		r.Enabled = (enabledInt == 1)
		list = append(list, &r)
	}
	return list, nil
}

func (s *Store) ListEnabledRecipients(ctx context.Context) ([]*Recipient, error) {
	query := `SELECT id, name, phone_e164, jid, enabled, levels, created_at FROM recipients WHERE enabled = 1 ORDER BY created_at ASC;`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to query enabled recipients: %w", err)
	}
	defer rows.Close()

	var list []*Recipient
	for rows.Next() {
		var r Recipient
		var enabledInt int
		if err := rows.Scan(&r.ID, &r.Name, &r.PhoneE164, &r.JID, &enabledInt, &r.Levels, &r.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan recipient row: %w", err)
		}
		r.Enabled = true
		list = append(list, &r)
	}
	return list, nil
}
