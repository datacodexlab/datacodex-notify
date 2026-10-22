// SPDX-License-Identifier: GPL-3.0-only

package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// RegisterOrBeatHeartbeat creates a heartbeat or records a beat at the given time,
// returning it to the active state. Empty label, source, or session, a
// non-positive interval, or a negative grace keep the stored values.
func (s *Store) RegisterOrBeatHeartbeat(ctx context.Context, id, label, source, session string, intervalS, graceS int, at time.Time) (*Heartbeat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := at.UTC()
	query := `
	INSERT INTO heartbeats (id, label, source, session, interval_s, grace_s, last_beat_at, state)
	VALUES (?, ?, ?, ?, ?, ?, ?, 'active')
	ON CONFLICT(id) DO UPDATE SET 
		label = CASE WHEN excluded.label != '' THEN excluded.label ELSE heartbeats.label END,
		source = CASE WHEN excluded.source != '' THEN excluded.source ELSE heartbeats.source END,
		session = CASE WHEN excluded.session != '' THEN excluded.session ELSE heartbeats.session END,
		interval_s = CASE WHEN excluded.interval_s > 0 THEN excluded.interval_s ELSE heartbeats.interval_s END,
		grace_s = CASE WHEN excluded.grace_s >= 0 THEN excluded.grace_s ELSE heartbeats.grace_s END,
		last_beat_at = excluded.last_beat_at,
		state = 'active';`

	_, err := s.db.ExecContext(ctx, query, id, label, source, session, intervalS, graceS, now)
	if err != nil {
		return nil, fmt.Errorf("failed to register/beat heartbeat: %w", err)
	}

	return s.getHeartbeatUnlocked(ctx, id)
}

func (s *Store) DeleteHeartbeat(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "DELETE FROM heartbeats WHERE id = ?;", id)
	if err != nil {
		return fmt.Errorf("failed to delete heartbeat: %w", err)
	}
	return nil
}

// DeleteHeartbeatsBySession stops monitoring every heartbeat that belongs to an agent session.
func (s *Store) DeleteHeartbeatsBySession(ctx context.Context, session string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.ExecContext(ctx, "DELETE FROM heartbeats WHERE session = ?;", session); err != nil {
		return fmt.Errorf("failed to delete the session's heartbeats: %w", err)
	}
	return nil
}

// DeleteAllHeartbeats stops monitoring every heartbeat.
func (s *Store) DeleteAllHeartbeats(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, err := s.db.ExecContext(ctx, "DELETE FROM heartbeats;"); err != nil {
		return fmt.Errorf("failed to delete heartbeats: %w", err)
	}
	return nil
}

func (s *Store) UpdateHeartbeatState(ctx context.Context, id, state string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.ExecContext(ctx, "UPDATE heartbeats SET state = ? WHERE id = ?;", state, id)
	if err != nil {
		return fmt.Errorf("failed to update heartbeat state: %w", err)
	}
	return nil
}

func (s *Store) GetHeartbeat(ctx context.Context, id string) (*Heartbeat, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getHeartbeatUnlocked(ctx, id)
}

func (s *Store) getHeartbeatUnlocked(ctx context.Context, id string) (*Heartbeat, error) {
	query := `SELECT id, label, source, session, interval_s, grace_s, last_beat_at, state FROM heartbeats WHERE id = ?;`
	row := s.db.QueryRowContext(ctx, query, id)

	var h Heartbeat
	if err := row.Scan(&h.ID, &h.Label, &h.Source, &h.Session, &h.IntervalS, &h.GraceS, &h.LastBeatAt, &h.State); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan heartbeat: %w", err)
	}
	return &h, nil
}

func (s *Store) ListActiveHeartbeats(ctx context.Context) ([]*Heartbeat, error) {
	query := `SELECT id, label, source, session, interval_s, grace_s, last_beat_at, state FROM heartbeats WHERE state != 'stopped';`
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to list active heartbeats: %w", err)
	}
	defer rows.Close()

	var list []*Heartbeat
	for rows.Next() {
		var h Heartbeat
		if err := rows.Scan(&h.ID, &h.Label, &h.Source, &h.Session, &h.IntervalS, &h.GraceS, &h.LastBeatAt, &h.State); err != nil {
			return nil, fmt.Errorf("failed to scan heartbeat row: %w", err)
		}
		list = append(list, &h)
	}
	return list, nil
}
