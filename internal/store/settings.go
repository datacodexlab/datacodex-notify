// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
)

func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	query := `SELECT value FROM settings WHERE key = ?;`
	row := s.db.QueryRowContext(ctx, query, key)
	var val string
	if err := row.Scan(&val); err != nil {
		if err == sql.ErrNoRows {
			return "", nil
		}
		return "", fmt.Errorf("failed to get setting %s: %w", key, err)
	}
	return val, nil
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
	INSERT INTO settings (key, value) VALUES (?, ?)
	ON CONFLICT(key) DO UPDATE SET value = excluded.value;`

	_, err := s.db.ExecContext(ctx, query, key, value)
	if err != nil {
		return fmt.Errorf("failed to set setting %s: %w", key, err)
	}
	return nil
}

func (s *Store) GetAllSettings(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, "SELECT key, value FROM settings;")
	if err != nil {
		return nil, fmt.Errorf("failed to query settings: %w", err)
	}
	defer rows.Close()

	settings := make(map[string]string)
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			return nil, fmt.Errorf("failed to scan setting: %w", err)
		}
		settings[k] = v
	}
	return settings, nil
}
