package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// LifecycleEvent records a durable explanation for an account becoming
// unavailable. It intentionally has no foreign key to accounts: deleted
// local identities must not erase the evidence needed by the account table.
type LifecycleEvent struct {
	ID         int64     `json:"id"`
	AccountID  int64     `json:"account_id,omitempty"`
	Email      string    `json:"email"`
	Source     string    `json:"source"`
	Status     string    `json:"status"`
	Reason     string    `json:"reason,omitempty"`
	Code       string    `json:"code,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
	DedupeKey  string    `json:"-"`
	ObservedAt time.Time `json:"observed_at"`
}

func isOfficialLifecycleStatus(status string) bool {
	switch strings.ToLower(strings.TrimSpace(status)) {
	case "deleted", "deactivated", "deleted_or_deactivated":
		return true
	default:
		return false
	}
}

func lifecycleStatusForAccountCheck(result AccountCheckResult) (string, string) {
	for _, candidate := range []string{result.ErrorCode, result.StreamErrorCode, result.Outcome} {
		switch strings.ToLower(strings.TrimSpace(candidate)) {
		case "account_deleted", "user_deleted", "deleted":
			return "deleted", strings.ToLower(strings.TrimSpace(candidate))
		case "account_deactivated", "user_deactivated", "account_disabled", "user_disabled", "deactivated":
			return "deactivated", strings.ToLower(strings.TrimSpace(candidate))
		case "deleted_or_deactivated":
			return "deleted_or_deactivated", "deleted_or_deactivated"
		}
	}
	return "", ""
}

func lifecycleHTTPStatus(value *int) int {
	if value == nil {
		return 0
	}
	return *value
}

func recordLifecycleEventTx(ctx context.Context, tx *sql.Tx, event LifecycleEvent) error {
	event.Email = normalizeEmail(event.Email)
	event.Source = strings.ToLower(strings.TrimSpace(event.Source))
	event.Status = strings.ToLower(strings.TrimSpace(event.Status))
	event.DedupeKey = strings.TrimSpace(event.DedupeKey)
	if event.Email == "" || event.Source == "" || event.Status == "" || event.DedupeKey == "" {
		return errors.New("email, source, status, and dedupe key are required")
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now()
	}
	_, err := tx.ExecContext(ctx, `INSERT INTO account_lifecycle_events(account_id,email,source,status,reason,code,http_status,dedupe_key,observed_at) VALUES(NULLIF(?,0),?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`, event.AccountID, event.Email, event.Source, event.Status, strings.TrimSpace(event.Reason), strings.TrimSpace(event.Code), event.HTTPStatus, event.DedupeKey, event.ObservedAt.Unix())
	return err
}

// RecordLifecycleEvent inserts one event and returns the existing row when the
// same dedupe key was already recorded. This makes retries safe across worker
// restarts and repeated status polling.
func (s *Store) RecordLifecycleEvent(ctx context.Context, event LifecycleEvent) (LifecycleEvent, error) {
	event.Email = normalizeEmail(event.Email)
	event.Source = strings.ToLower(strings.TrimSpace(event.Source))
	event.Status = strings.ToLower(strings.TrimSpace(event.Status))
	event.DedupeKey = strings.TrimSpace(event.DedupeKey)
	if event.Email == "" || event.Source == "" || event.Status == "" || event.DedupeKey == "" {
		return LifecycleEvent{}, errors.New("email, source, status, and dedupe key are required")
	}
	if event.ObservedAt.IsZero() {
		event.ObservedAt = time.Now()
	}
	observedAt := event.ObservedAt.Unix()
	_, err := s.db.ExecContext(ctx, `INSERT INTO account_lifecycle_events(account_id,email,source,status,reason,code,http_status,dedupe_key,observed_at) VALUES(NULLIF(?,0),?,?,?,?,?,?,?,?) ON CONFLICT(dedupe_key) DO NOTHING`, event.AccountID, event.Email, event.Source, event.Status, strings.TrimSpace(event.Reason), strings.TrimSpace(event.Code), event.HTTPStatus, event.DedupeKey, observedAt)
	if err != nil {
		return LifecycleEvent{}, err
	}
	return s.getLifecycleEvent(ctx, event.DedupeKey)
}

func (s *Store) getLifecycleEvent(ctx context.Context, dedupeKey string) (LifecycleEvent, error) {
	return scanLifecycleEvent(s.db.QueryRowContext(ctx, `SELECT id,COALESCE(account_id,0),email,source,status,COALESCE(reason,''),COALESCE(code,''),http_status,dedupe_key,observed_at FROM account_lifecycle_events WHERE dedupe_key=?`, dedupeKey))
}

// ListLifecycleEvents returns the newest lifecycle events across accounts.
func (s *Store) ListLifecycleEvents(ctx context.Context, limit int) ([]LifecycleEvent, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.listLifecycleEvents(ctx, `WHERE 1=1`, limit)
}

// ListLifecycleEventsForAccount returns events by stable local account ID,
// including events whose account row has already been finalized and removed.
func (s *Store) ListLifecycleEventsForAccount(ctx context.Context, accountID int64, limit int) ([]LifecycleEvent, error) {
	if accountID <= 0 {
		return nil, errors.New("account id is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.listLifecycleEvents(ctx, `WHERE account_id=?`, limit, accountID)
}

// ListLifecycleEventsForEmail is useful after a local account has been
// finalized, when only the retained email identity is available.
func (s *Store) ListLifecycleEventsForEmail(ctx context.Context, email string, limit int) ([]LifecycleEvent, error) {
	email = normalizeEmail(email)
	if email == "" {
		return nil, errors.New("email is required")
	}
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	return s.listLifecycleEvents(ctx, `WHERE email=?`, limit, email)
}

func (s *Store) listLifecycleEvents(ctx context.Context, predicate string, limit int, args ...any) ([]LifecycleEvent, error) {
	queryArgs := append(args, limit)
	rows, err := s.db.QueryContext(ctx, `SELECT id,COALESCE(account_id,0),email,source,status,COALESCE(reason,''),COALESCE(code,''),http_status,dedupe_key,observed_at FROM account_lifecycle_events `+predicate+` AND source<>'cleanup_authorization' ORDER BY observed_at DESC,id DESC LIMIT ?`, queryArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	events := make([]LifecycleEvent, 0)
	for rows.Next() {
		event, err := scanLifecycleEvent(rows)
		if err != nil {
			return nil, err
		}
		events = append(events, event)
	}
	return events, rows.Err()
}

func scanLifecycleEvent(row scanner) (LifecycleEvent, error) {
	var event LifecycleEvent
	var accountID sql.NullInt64
	var observedAt int64
	if err := row.Scan(&event.ID, &accountID, &event.Email, &event.Source, &event.Status, &event.Reason, &event.Code, &event.HTTPStatus, &event.DedupeKey, &observedAt); err != nil {
		return LifecycleEvent{}, err
	}
	if accountID.Valid {
		event.AccountID = accountID.Int64
	}
	event.ObservedAt = unixTime(observedAt)
	return event, nil
}
