package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestSingleAttemptMigrationAndRestart(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	credentials := Credentials{Email: "single-attempt@example.test", Password: "fixture"}
	_, attempt, err := s.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	err = s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "unknown_error"})
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	// Model an existing database before this additive column was installed.
	if _, err := s.db.Exec(`ALTER TABLE login_attempts DROP COLUMN single_attempt`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.ListAttempts(ctx, credentials.Email, 1)
	if err != nil || len(old) != 1 || old[0].SingleAttempt || old[0].Code != "unknown_error" {
		t.Fatalf("migration changed old intent or reason: %+v %v", old, err)
	}
	credentials.SingleAttempt = true
	_, attempt, err = s.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restarted, err := s.ListAttempts(ctx, credentials.Email, 1)
	if err != nil || len(restarted) != 1 || !restarted[0].SingleAttempt || restarted[0].Status != "interrupted" {
		t.Fatalf("restart lost one-shot intent: %+v %v", restarted, err)
	}
}
