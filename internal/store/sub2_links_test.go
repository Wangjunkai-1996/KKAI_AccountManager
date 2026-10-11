package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
)

func TestLinkSub2AccountIsTerminalAndPreservesExistingTask(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, err := s.UpsertCredentials(ctx, Credentials{Email: "link@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.LinkSub2Account(ctx, "current", a.ID, 42, "linked-first")
	if err != nil || linked.State != "imported" || linked.Sub2AccountID != 42 {
		t.Fatalf("link = %+v, %v", linked, err)
	}
	queued, err := s.ListQueuedSub2Imports(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("association entered import queue: %+v, %v", queued, err)
	}
	again, err := s.LinkSub2Account(ctx, "current", a.ID, 43, "linked-second")
	if err != nil || again.ID != linked.ID || again.Sub2AccountID != 42 {
		t.Fatalf("existing binding overwritten: %+v, %v", again, err)
	}
	queuedTask, _, err := s.CreateOrGetSub2Import(ctx, "other", a.ID, "queued", "queued-key", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.LinkSub2Account(ctx, "other", a.ID, 45, "linked-other")
	if err != nil || unchanged.ID != queuedTask.ID || unchanged.State != "queued" {
		t.Fatalf("queued import overwritten: %+v, %v", unchanged, err)
	}
	if _, err := s.LinkSub2Account(ctx, "current", a.ID+100, 46, "linked-missing"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account link error = %v", err)
	}
}

func TestDeleteSub2ImportBindingRequiresOperationIdentity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, err := s.UpsertCredentials(ctx, Credentials{Email: "delete-cas@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.LinkSub2Account(ctx, "current", a.ID, 42, "linked-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSub2ImportBinding(ctx, "current", a.ID, 42, "linked-other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSub2Import(ctx, "current", a.ID); err != nil {
		t.Fatalf("wrong operation removed binding: %v", err)
	}
	if err := s.DeleteSub2ImportBinding(ctx, "current", a.ID, 42, linked.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSub2Import(ctx, "current", a.ID); !errors.Is(err, ErrSub2ImportNotFound) {
		t.Fatalf("binding remains after matching delete: %v", err)
	}
}

func TestListQueuedSub2ImportsForDestinationIsolatesPools(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	for _, tc := range []struct {
		email, destination string
	}{
		{"pool-a@example.test", "pool-a"},
		{"pool-b@example.test", "pool-b"},
	} {
		account, err := s.UpsertCredentials(ctx, Credentials{Email: tc.email, Password: "pw"})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := s.CreateOrGetSub2Import(ctx, tc.destination, account.ID, "op-"+tc.destination, "key-"+tc.destination, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
	}
	items, err := s.ListQueuedSub2ImportsForDestination(ctx, "pool-a", 10)
	if err != nil || len(items) != 1 || items[0].DestinationKey != "pool-a" {
		t.Fatalf("pool-a queued imports = %+v, err=%v", items, err)
	}
}

func TestSub2ImportCredentialVersionMigration(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "version-migration@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "at", RefreshToken: "rt"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	task, _, err := s.CreateOrGetSub2Import(ctx, "migration", account.ID, "migration-op", "migration-key", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if task.CredentialVersion != attempt.ID {
		t.Fatalf("new task version=%d want=%d", task.CredentialVersion, attempt.ID)
	}
	if _, err := s.db.Exec(`ALTER TABLE sub2_imports DROP COLUMN credential_version`); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	migrated, err := reopened.GetSub2Import(ctx, "migration", account.ID)
	if err != nil || migrated.CredentialVersion != 0 {
		t.Fatalf("migrated task=%+v err=%v", migrated, err)
	}
}
