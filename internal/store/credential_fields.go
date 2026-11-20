package store

import (
	"context"
	"database/sql"
	"errors"
)

var ErrCredentialField = errors.New("invalid credential field")

type CredentialFieldMeta struct {
	State string `json:"state"`
}

type CredentialsMeta struct {
	Password   CredentialFieldMeta `json:"password"`
	TOTPSecret CredentialFieldMeta `json:"totp_secret"`
}

// CredentialMetadata reports presence without decrypting any stored secret.
func (s *Store) CredentialMetadata(ctx context.Context, id int64) (CredentialsMeta, error) {
	var password, totp bool
	var account Account
	err := s.db.QueryRowContext(ctx, `SELECT status,sub2_policy,COALESCE(length(password_cipher),0)>0,COALESCE(length(totp_cipher),0)>0 FROM accounts WHERE id=?`, id).Scan(&account.Status, &account.Sub2Policy, &password, &totp)
	if errors.Is(err, sql.ErrNoRows) {
		return CredentialsMeta{}, ErrAccountNotFound
	}
	if err != nil {
		return CredentialsMeta{}, err
	}
	meta := CredentialsMeta{Password: CredentialFieldMeta{State: "missing"}, TOTPSecret: CredentialFieldMeta{State: "missing"}}
	if account.IsTerminalUnavailable() {
		return meta, nil
	}
	if password {
		meta.Password.State = "saved"
	}
	if totp {
		meta.TOTPSecret.State = "saved"
	}
	return meta, nil
}

// RevealCredential reads and decrypts only the explicitly requested field.
func (s *Store) RevealCredential(ctx context.Context, id int64, field string) (string, error) {
	var query string
	switch field {
	case "password":
		query = `SELECT status,sub2_policy,password_cipher FROM accounts WHERE id=?`
	case "totp_secret":
		query = `SELECT status,sub2_policy,totp_cipher FROM accounts WHERE id=?`
	default:
		return "", ErrCredentialField
	}
	var ciphertext []byte
	var account Account
	err := s.db.QueryRowContext(ctx, query, id).Scan(&account.Status, &account.Sub2Policy, &ciphertext)
	if errors.Is(err, sql.ErrNoRows) {
		return "", ErrAccountNotFound
	}
	if err != nil {
		return "", err
	}
	if err := account.terminalError(); err != nil {
		return "", err
	}
	return s.open(ciphertext)
}
