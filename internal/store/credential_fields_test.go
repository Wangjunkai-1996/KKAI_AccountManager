package store

import (
	"context"
	"errors"
	"testing"
)

func TestCredentialFields(t *testing.T) {
	s, err := Open(t.TempDir()+"/accounts.db", t.TempDir()+"/key")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	ctx := context.Background()
	a, err := s.UpsertCredentials(ctx, Credentials{Email: "single-field@example.test", Password: "secret-password", TOTPSecret: "secret-totp"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `UPDATE accounts SET totp_cipher=x'01',proxy_cipher=x'01' WHERE id=?`, a.ID); err != nil {
		t.Fatal(err)
	}
	value, err := s.RevealCredential(ctx, a.ID, "password")
	if err != nil || value != "secret-password" {
		t.Fatal("revealing password decrypted unrelated corrupt fields")
	}
	if _, err = s.RevealCredential(ctx, a.ID, "totp_secret"); err == nil {
		t.Fatal("corrupt ciphertext accepted")
	}
	if _, err = s.RevealCredential(ctx, a.ID, "password_cipher FROM accounts"); !errors.Is(err, ErrCredentialField) {
		t.Fatal("unapproved field accepted")
	}
	if _, err = s.RevealCredential(ctx, 999, "password"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatal("missing account not distinguished")
	}
	meta, err := s.CredentialMetadata(ctx, a.ID)
	if err != nil || meta.Password.State != "saved" || meta.TOTPSecret.State != "saved" {
		t.Fatal("metadata should not decrypt secrets")
	}
}
