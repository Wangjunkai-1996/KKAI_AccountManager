package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LinkSub2Account records a verified existing remote account in a terminal
// state. It must never enter the import queue or replace an in-flight import.
func (s *Store) LinkSub2Account(ctx context.Context, destinationKey string, accountID, sub2AccountID int64, operationID string) (Sub2Import, error) {
	if strings.TrimSpace(destinationKey) == "" || accountID <= 0 || sub2AccountID <= 0 || !strings.HasPrefix(operationID, "linked-") {
		return Sub2Import{}, errors.New("invalid sub2 account binding")
	}
	payload, err := s.seal(`{}`)
	if err != nil {
		return Sub2Import{}, err
	}
	now := time.Now().Unix()
	_, err = s.db.ExecContext(ctx, `INSERT INTO sub2_imports(destination_key,account_id,operation_id,idempotency_key,payload_cipher,state,create_acknowledged,sub2_account_id,created_at,updated_at)
		SELECT ?,id,?,?,?,'imported',1,?,?,? FROM accounts WHERE id=?
		ON CONFLICT(destination_key,account_id) DO NOTHING`, destinationKey, operationID, operationID, payload, sub2AccountID, now, now, accountID)
	if err != nil {
		return Sub2Import{}, fmt.Errorf("link sub2 account: %w", err)
	}
	item, err := s.GetSub2Import(ctx, destinationKey, accountID)
	if errors.Is(err, ErrSub2ImportNotFound) {
		return Sub2Import{}, ErrAccountNotFound
	}
	return item, err
}

// DeleteSub2ImportBinding removes a confirmed remote binding after the remote
// account has been deleted. The identity conditions keep a concurrent rebind
// from being removed accidentally.
func (s *Store) DeleteSub2ImportBinding(ctx context.Context, destinationKey string, accountID, sub2AccountID int64) error {
	if strings.TrimSpace(destinationKey) == "" || accountID <= 0 || sub2AccountID <= 0 {
		return errors.New("invalid sub2 account binding")
	}
	_, err := s.db.ExecContext(ctx, "DELETE FROM sub2_imports WHERE destination_key=? AND account_id=? AND sub2_account_id=? AND state='imported'", destinationKey, accountID, sub2AccountID)
	return err
}
