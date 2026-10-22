// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
)

const agentSessionColumns = `id, source, session_name, project, rules_files, connected_at, last_seen_at, last_message_at, muted, disconnected_at`

func scanAgentSession(scan func(dest ...interface{}) error) (*AgentSession, error) {
	var (
		a                          AgentSession
		rules                      string
		seen, message, disconnects sql.NullTime
		muted                      int
	)
	if err := scan(&a.ID, &a.Source, &a.SessionName, &a.Project, &rules, &a.ConnectedAt, &seen, &message, &muted, &disconnects); err != nil {
		return nil, err
	}
	if err := json.Unmarshal([]byte(rules), &a.RulesFiles); err != nil {
		return nil, fmt.Errorf("damaged rules file list for session %s: %w", a.SessionName, err)
	}
	if a.RulesFiles == nil {
		a.RulesFiles = []string{}
	}
	a.Muted = muted == 1
	if seen.Valid {
		a.LastSeenAt = &seen.Time
	}
	if message.Valid {
		a.LastMessageAt = &message.Time
	}
	if disconnects.Valid {
		a.DisconnectedAt = &disconnects.Time
	}
	return &a, nil
}

// UpsertAgentSession registers a session or, when the name is known, updates its
// source and rules files (and the project, when given) and marks it connected
// again. The mute flag is kept.
func (s *Store) UpsertAgentSession(ctx context.Context, source, name, project string, rulesFiles []string, at time.Time) (*AgentSession, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if rulesFiles == nil {
		rulesFiles = []string{}
	}
	rules, err := json.Marshal(rulesFiles)
	if err != nil {
		return nil, fmt.Errorf("failed to encode the rules files: %w", err)
	}
	now := at.UTC()
	_, err = s.db.ExecContext(ctx, `
	INSERT INTO agent_sessions (id, source, session_name, project, rules_files, connected_at, last_seen_at)
	VALUES (?, ?, ?, ?, ?, ?, ?)
	ON CONFLICT(session_name) DO UPDATE SET
		source = excluded.source,
		project = CASE WHEN excluded.project <> '' THEN excluded.project ELSE project END,
		rules_files = excluded.rules_files,
		connected_at = excluded.connected_at,
		last_seen_at = excluded.last_seen_at,
		disconnected_at = NULL;`,
		uuid.New().String(), source, name, project, string(rules), now, now)
	if err != nil {
		return nil, fmt.Errorf("failed to save the agent session: %w", err)
	}
	return s.getAgentSessionByNameUnlocked(ctx, name)
}

func (s *Store) getAgentSessionByNameUnlocked(ctx context.Context, name string) (*AgentSession, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+agentSessionColumns+` FROM agent_sessions WHERE session_name = ?;`, name)
	a, err := scanAgentSession(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// GetAgentSessionByName returns the session with that name, or nil.
func (s *Store) GetAgentSessionByName(ctx context.Context, name string) (*AgentSession, error) {
	return s.getAgentSessionByNameUnlocked(ctx, name)
}

// GetAgentSession returns the session with that id, or nil.
func (s *Store) GetAgentSession(ctx context.Context, id string) (*AgentSession, error) {
	row := s.db.QueryRowContext(ctx, `SELECT `+agentSessionColumns+` FROM agent_sessions WHERE id = ?;`, id)
	a, err := scanAgentSession(row.Scan)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return a, err
}

// ListAgentSessions returns every session, newest connection first.
func (s *Store) ListAgentSessions(ctx context.Context) ([]*AgentSession, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+agentSessionColumns+` FROM agent_sessions ORDER BY connected_at DESC;`)
	if err != nil {
		return nil, fmt.Errorf("failed to list agent sessions: %w", err)
	}
	defer rows.Close()

	var list []*AgentSession
	for rows.Next() {
		a, err := scanAgentSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		list = append(list, a)
	}
	return list, nil
}

// SetAgentSessionMuted mutes or unmutes a session. It reports whether the session exists.
func (s *Store) SetAgentSessionMuted(ctx context.Context, id string, muted bool) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	flag := 0
	if muted {
		flag = 1
	}
	res, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET muted = ? WHERE id = ?;`, flag, id)
	if err != nil {
		return false, fmt.Errorf("failed to change the mute flag: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// MarkAgentSessionDisconnected records the disconnect time. It reports whether the session exists.
func (s *Store) MarkAgentSessionDisconnected(ctx context.Context, name string, at time.Time) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `UPDATE agent_sessions SET disconnected_at = ? WHERE session_name = ?;`, at.UTC(), name)
	if err != nil {
		return false, fmt.Errorf("failed to disconnect the agent session: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}

// TouchAgentSession records activity: last seen, and last message when message is true.
// An unknown name is ignored.
func (s *Store) TouchAgentSession(ctx context.Context, name string, at time.Time, message bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := at.UTC()
	query := `UPDATE agent_sessions SET last_seen_at = ? WHERE session_name = ?;`
	args := []interface{}{now, name}
	if message {
		query = `UPDATE agent_sessions SET last_seen_at = ?, last_message_at = ? WHERE session_name = ?;`
		args = []interface{}{now, now, name}
	}
	if _, err := s.db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("failed to record agent activity: %w", err)
	}
	return nil
}

// DeleteAgentSession removes a session row. It reports whether the session existed.
func (s *Store) DeleteAgentSession(ctx context.Context, id string) (bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM agent_sessions WHERE id = ?;`, id)
	if err != nil {
		return false, fmt.Errorf("failed to delete the agent session: %w", err)
	}
	n, _ := res.RowsAffected()
	return n > 0, nil
}
