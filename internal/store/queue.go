package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrQueueDeferred proves that no claim transaction started. It must never
// label a failed write/commit, whose persisted outcome needs reconciliation.
var ErrQueueDeferred = errors.New("queue claim deferred before transaction")

func (s *Store) beginQueueTx(ctx context.Context) (*sql.Tx, error) {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, errors.Join(ErrQueueDeferred, err, ctx.Err())
	}
	return tx, err
}
