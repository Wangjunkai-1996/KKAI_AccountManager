package store

import (
	"context"
	"database/sql"
	"errors"
)

func (s *Store) initRecoverySettings(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS recovery_settings (id INTEGER PRIMARY KEY CHECK(id=1), enabled INTEGER NOT NULL CHECK(enabled IN (0,1)))`)
	return err
}

// AutoRecoveryEnabled distinguishes an explicit saved choice from the startup
// environment default. No account credentials are stored here.
func (s *Store) AutoRecoveryEnabled(ctx context.Context) (enabled, saved bool, err error) {
	if err = s.initRecoverySettings(ctx); err != nil {
		return false, false, err
	}
	err = s.db.QueryRowContext(ctx, `SELECT enabled FROM recovery_settings WHERE id=1`).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, false, nil
	}
	return enabled, err == nil, err
}

func (s *Store) SetAutoRecoveryEnabled(ctx context.Context, enabled bool) error {
	if err := s.initRecoverySettings(ctx); err != nil {
		return err
	}
	_, err := s.db.ExecContext(ctx, `INSERT INTO recovery_settings(id,enabled) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET enabled=excluded.enabled`, boolInt(enabled))
	return err
}

// ExplicitRecoveryCheck persists the user's one-account recovery intent in
// the existing idempotency key; ordinary check requests reserve this prefix.
func (s *Store) ExplicitRecoveryCheck(ctx context.Context, batchID string) (bool, error) {
	var explicit bool
	err := s.db.QueryRowContext(ctx, `SELECT request_key LIKE 'recovery-manual-%' FROM account_check_batches WHERE id=?`, batchID).Scan(&explicit)
	return explicit, err
}

func (s *Store) AutomaticRecoveryCheck(ctx context.Context, batchID string) (bool, error) {
	var automatic bool
	err := s.db.QueryRowContext(ctx, `SELECT request_key LIKE 'recovery-auto-%' FROM account_check_batches WHERE id=?`, batchID).Scan(&automatic)
	return automatic, err
}
