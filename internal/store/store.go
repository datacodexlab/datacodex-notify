// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
	mu sync.Mutex
}

func Open(dbPath string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=busy_timeout(10000)&_pragma=journal_mode(WAL)", dbPath)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	db.SetMaxOpenConns(1) // SQLite single writer to avoid concurrency locks
	db.SetMaxIdleConns(1)

	s := &Store{db: db}
	if err := s.migrate(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("database migration failed: %w", err)
	}

	return s, nil
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) DB() *sql.DB {
	return s.db
}

func (s *Store) migrate(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	createSchemaTable := `
	CREATE TABLE IF NOT EXISTS schema_version (
		version INTEGER PRIMARY KEY,
		applied_at DATETIME NOT NULL
	);`
	if _, err := s.db.ExecContext(ctx, createSchemaTable); err != nil {
		return fmt.Errorf("failed to create schema_version: %w", err)
	}

	var currentVersion int
	row := s.db.QueryRowContext(ctx, "SELECT COALESCE(MAX(version), 0) FROM schema_version")
	if err := row.Scan(&currentVersion); err != nil {
		return fmt.Errorf("failed to check schema version: %w", err)
	}

	migrations := []struct {
		version int
		sql     string
	}{
		{
			version: 1,
			sql: `
			CREATE TABLE IF NOT EXISTS settings (
				key TEXT PRIMARY KEY,
				value TEXT NOT NULL
			);

			CREATE TABLE IF NOT EXISTS recipients (
				id TEXT PRIMARY KEY,
				name TEXT NOT NULL,
				phone_e164 TEXT NOT NULL UNIQUE,
				jid TEXT NOT NULL,
				enabled INTEGER NOT NULL DEFAULT 1,
				levels INTEGER NOT NULL DEFAULT 7,
				created_at DATETIME NOT NULL
			);

			CREATE TABLE IF NOT EXISTS messages (
				id TEXT PRIMARY KEY,
				created_at DATETIME NOT NULL,
				level TEXT NOT NULL,
				source TEXT NOT NULL,
				text TEXT NOT NULL,
				speech TEXT,
				mode TEXT NOT NULL,
				dedupe_key TEXT,
				status TEXT NOT NULL,
				attempts INTEGER NOT NULL DEFAULT 0,
				last_error TEXT,
				sent_at DATETIME
			);

			CREATE INDEX IF NOT EXISTS idx_messages_status ON messages(status, created_at);
			CREATE INDEX IF NOT EXISTS idx_messages_dedupe ON messages(dedupe_key, created_at);

			CREATE TABLE IF NOT EXISTS deliveries (
				id TEXT PRIMARY KEY,
				message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
				recipient_id TEXT NOT NULL REFERENCES recipients(id) ON DELETE CASCADE,
				kind TEXT NOT NULL,
				wa_message_id TEXT,
				status TEXT NOT NULL,
				updated_at DATETIME NOT NULL
			);

			CREATE INDEX IF NOT EXISTS idx_deliveries_msg ON deliveries(message_id);

			CREATE TABLE IF NOT EXISTS heartbeats (
				id TEXT PRIMARY KEY,
				label TEXT NOT NULL,
				source TEXT NOT NULL,
				interval_s INTEGER NOT NULL,
				grace_s INTEGER NOT NULL,
				last_beat_at DATETIME NOT NULL,
				state TEXT NOT NULL
			);

			CREATE TABLE IF NOT EXISTS inbox (
				id TEXT PRIMARY KEY,
				received_at DATETIME NOT NULL,
				from_recipient_id TEXT,
				wa_message_id TEXT UNIQUE,
				kind TEXT NOT NULL,
				text TEXT NOT NULL,
				state TEXT NOT NULL DEFAULT 'new'
			);

			CREATE INDEX IF NOT EXISTS idx_inbox_received ON inbox(received_at);
			`,
		},
		{
			version: 2,
			sql: `
			ALTER TABLE messages ADD COLUMN next_attempt_at DATETIME;
			`,
		},
		{
			version: 3,
			sql: `
			ALTER TABLE heartbeats ADD COLUMN session TEXT NOT NULL DEFAULT '';

			CREATE TABLE IF NOT EXISTS agent_sessions (
				id TEXT PRIMARY KEY,
				source TEXT NOT NULL,
				session_name TEXT NOT NULL UNIQUE,
				rules_files TEXT NOT NULL DEFAULT '[]',
				connected_at DATETIME NOT NULL,
				last_seen_at DATETIME,
				last_message_at DATETIME,
				muted INTEGER NOT NULL DEFAULT 0,
				disconnected_at DATETIME
			);
			`,
		},
		{
			version: 4,
			sql: `
			ALTER TABLE agent_sessions ADD COLUMN project TEXT NOT NULL DEFAULT '';
			`,
		},
	}

	for _, m := range migrations {
		if m.version > currentVersion {
			tx, err := s.db.BeginTx(ctx, nil)
			if err != nil {
				return fmt.Errorf("begin migration %d failed: %w", m.version, err)
			}
			if _, err := tx.ExecContext(ctx, m.sql); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("execute migration %d failed: %w", m.version, err)
			}
			if _, err := tx.ExecContext(ctx, "INSERT INTO schema_version (version, applied_at) VALUES (?, ?)", m.version, time.Now().UTC()); err != nil {
				_ = tx.Rollback()
				return fmt.Errorf("record migration %d failed: %w", m.version, err)
			}
			if err := tx.Commit(); err != nil {
				return fmt.Errorf("commit migration %d failed: %w", m.version, err)
			}
		}
	}

	return nil
}

func (s *Store) Prune(ctx context.Context, maxMessages int, maxInbox int) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Prune messages beyond maxMessages (excluding queued, sending, or held)
	pruneMessagesSQL := `
	DELETE FROM messages 
	WHERE status NOT IN ('queued', 'sending', 'held_paused', 'held_quiet_hours')
	AND id NOT IN (
		SELECT id FROM messages 
		ORDER BY created_at DESC 
		LIMIT ?
	);`
	if _, err := s.db.ExecContext(ctx, pruneMessagesSQL, maxMessages); err != nil {
		return fmt.Errorf("failed to prune messages: %w", err)
	}

	// Prune inbox beyond maxInbox
	pruneInboxSQL := `
	DELETE FROM inbox 
	WHERE id NOT IN (
		SELECT id FROM inbox 
		ORDER BY received_at DESC 
		LIMIT ?
	);`
	if _, err := s.db.ExecContext(ctx, pruneInboxSQL, maxInbox); err != nil {
		return fmt.Errorf("failed to prune inbox: %w", err)
	}

	return nil
}
