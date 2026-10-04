package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func testStore(t *testing.T) (*Store, string) {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s, dir
}

func TestDeleteAccountByIDRejectsBusy(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "busy@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAccountByID(ctx, a.ID); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("delete active login = %v", err)
	}
	if err = s.FinishAttempt(ctx, attempt.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
	// The attempt still owns its lock until Release, even after persistence.
	if err = s.DeleteAccountByID(ctx, a.ID); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("delete unreleased login = %v", err)
	}
	attempt.Release()
	task, _, err := s.CreateOrGetSub2Import(ctx, "test", a.ID, "busy-op", "busy-key", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"queued", "sending", "confirming", "unknown"} {
		if err = s.UpdateSub2Import(ctx, task.ID, state, false, 0, ""); err != nil {
			t.Fatal(err)
		}
		if err = s.DeleteAccountByID(ctx, a.ID); !errors.Is(err, ErrAccountBusy) {
			t.Fatalf("delete %s import = %v", state, err)
		}
	}
	if err = s.UpdateSub2Import(ctx, task.ID, "failed", false, 0, ""); err != nil {
		t.Fatal(err)
	}
	if err = s.DeleteAccountByID(ctx, a.ID); err != nil {
		t.Fatalf("delete finished work = %v", err)
	}
}

func TestDeleteAccountByIDRemovesOnlySelectedIdentity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "delete")
	batch := checkTestBatch(t, s, "delete-history", 1, id)
	work := checkTestClaim(t, s)
	if err := s.DeleteAccountByID(ctx, id); err != nil {
		t.Fatal(err)
	}
	imports, err := s.ListSub2ImportStatuses(ctx)
	if err != nil || len(imports) != 0 {
		t.Fatalf("retained local import snapshots: %+v, %v", imports, err)
	}
	attempts, err := s.ListAttempts(ctx, "delete@example.test", 100)
	if err != nil || len(attempts) != 0 {
		t.Fatalf("retained attempts: %+v, %v", attempts, err)
	}
	replacement, err := s.UpsertCredentials(ctx, Credentials{Email: "delete@example.test", Password: "replacement"})
	if err != nil || replacement.ID == id {
		t.Fatalf("replacement = %+v, %v", replacement, err)
	}
	if err = s.DeleteAccountByID(ctx, id); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("stale delete = %v", err)
	}
	if _, _, err = s.BeginAttempt(ctx, Credentials{Email: replacement.Email, Password: "stale", ExistingAccountID: id}); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("stale login = %v", err)
	}
	if _, _, err = s.CreateOrGetSub2Import(ctx, "test", id, "stale-op", "stale-key", []byte(`{}`)); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("stale import = %v", err)
	}
	credentials, err := s.GetCredentialsByID(ctx, replacement.ID)
	if err != nil || credentials.Password != "replacement" {
		t.Fatalf("replacement credentials changed: %+v, %v", credentials, err)
	}
	if _, err = s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	_, checks, err := s.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil || len(checks) != 1 || checks[0].AccountID != nil || checks[0].Freshness != "account_removed" {
		t.Fatalf("deleted account check evidence = %+v, %v", checks, err)
	}
}

func TestDeleteAccountByIDConcurrentWork(t *testing.T) {
	for _, operation := range []string{"login", "import"} {
		t.Run(operation, func(t *testing.T) {
			s, _ := testStore(t)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			for i := 0; i < 20; i++ {
				a, err := s.UpsertCredentials(ctx, Credentials{Email: "concurrent@example.test", Password: "pw"})
				if err != nil {
					t.Fatal(err)
				}
				var deleted, started error
				var attempt Attempt
				var task Sub2Import
				var wg sync.WaitGroup
				gate := make(chan struct{})
				wg.Add(2)
				go func() {
					defer wg.Done()
					<-gate
					deleted = s.DeleteAccountByID(ctx, a.ID)
				}()
				go func() {
					defer wg.Done()
					<-gate
					if operation == "login" {
						_, attempt, started = s.BeginAttempt(ctx, Credentials{Email: a.Email, Password: "pw", ExistingAccountID: a.ID})
					} else {
						task, _, started = s.CreateOrGetSub2Import(ctx, "test", a.ID, "concurrent-op", "concurrent-key", []byte(`{}`))
					}
				}()
				close(gate)
				wg.Wait()
				if started == nil {
					if !errors.Is(deleted, ErrAccountBusy) {
						t.Fatalf("started work but delete = %v", deleted)
					}
					if operation == "login" {
						if err = s.FinishAttempt(ctx, attempt.ID, false, nil, nil); err != nil {
							t.Fatal(err)
						}
						attempt.Release()
					} else if err = s.UpdateSub2Import(ctx, task.ID, "failed", false, 0, ""); err != nil {
						t.Fatal(err)
					}
					if err = s.DeleteAccountByID(ctx, a.ID); err != nil {
						t.Fatal(err)
					}
				} else if deleted != nil || !errors.Is(started, ErrAccountNotFound) {
					t.Fatalf("delete=%v, start=%v", deleted, started)
				}
			}
		})
	}
}

func TestCredentialsAreEncryptedAndRoundTrip(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "User@Example.com", Password: "pw-do-not-store", TOTPSecret: "totp-secret", Proxy: "http://user:pass@proxy"})
	if err != nil {
		t.Fatal(err)
	}
	if account.Email != "user@example.com" || account.Status != "new" {
		t.Fatalf("account = %+v", account)
	}
	got, err := s.GetCredentials(ctx, "USER@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if got.Password != "pw-do-not-store" || got.TOTPSecret != "totp-secret" || got.Proxy == "" {
		t.Fatalf("credentials did not round-trip: %+v", got)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "accounts.db"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pw-do-not-store") || strings.Contains(string(raw), "totp-secret") {
		t.Fatal("plaintext secret found in sqlite database")
	}
	info, err := os.Stat(filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0600 {
		t.Fatalf("key mode = %o, want 600", info.Mode().Perm())
	}
}

func TestWrongKeyCannotOpenExistingDatabase(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	wrongKey := filepath.Join(dir, "wrong.key")
	if err := os.WriteFile(wrongKey, []byte(strings.Repeat("x", 32)), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(dbPath, wrongKey); err == nil {
		t.Fatal("database opened with a different encryption key")
	}
}

func TestAttemptHistoryBusySuccessAndFailure(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.UpsertCredentials(ctx, Credentials{Email: "a@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "a@example.com", Password: "pw2"})
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != "running" {
		t.Fatalf("status = %q, want running", account.Status)
	}
	if _, err := s.StartAttempt(ctx, "a@example.com"); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("busy error = %v", err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "access", RefreshToken: "refresh", PlanType: "plus", ExpiresAt: 123}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err = s.GetAccount(ctx, "a@example.com")
	if err != nil || account.Status != "active" || account.PlanType != "plus" || account.LastSuccessAt.IsZero() || account.LastAttemptAt.IsZero() {
		t.Fatalf("successful account = %+v, err=%v", account, err)
	}

	attempt, err = s.StartAttempt(ctx, "a@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Stage: "password", Code: "invalid_credentials", Message: "bad password", HTTPStatus: 401}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err = s.GetAccount(ctx, "a@example.com")
	if err != nil || account.Status != "failed" || account.LastErrorCode != "invalid_credentials" || account.LastSuccessAt.IsZero() {
		t.Fatalf("failed account = %+v, err=%v", account, err)
	}
	history, err := s.ListAttempts(ctx, "a@example.com", 10)
	if err != nil || len(history) != 2 || history[0].Status != "failed" || history[1].Status != "success" {
		t.Fatalf("history = %+v, err=%v", history, err)
	}
}

func TestDeletedAccountIsRemovedAndDeactivatedIsRetained(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.UpsertCredentials(ctx, Credentials{Email: "deleted@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, "deleted@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "account_deactivated", AccountStatus: "deactivated"}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if _, err := s.GetAccount(ctx, "deleted@example.com"); err != nil {
		t.Fatalf("deactivated account removed: %v", err)
	}
	attempt, err = s.StartAttempt(ctx, "deleted@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "account_deleted", AccountStatus: "deleted"}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if _, err := s.GetAccount(ctx, "deleted@example.com"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("deleted account lookup = %v", err)
	}
}

func TestRecoverInterrupted(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpsertCredentials(context.Background(), Credentials{Email: "crash@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(context.Background(), "crash@example.com")
	if err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	account, err := s.GetAccount(context.Background(), "crash@example.com")
	if err != nil || account.Status != "interrupted" {
		t.Fatalf("recovered account = %+v, err=%v", account, err)
	}
}

func TestOAuthResultAndSub2ImportTaskRoundTrip(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	if _, err := s.UpsertCredentials(ctx, Credentials{Email: "oauth@example.com", Password: "pw"}); err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, "oauth@example.com")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "at-secret", RefreshToken: "rt-secret", ChatGPTAccountID: "acct-1", OrganizationID: "org-1", PlanType: "plus", ExpiresAt: 123}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err := s.GetAccount(ctx, "oauth@example.com")
	if err != nil {
		t.Fatal(err)
	}
	email, result, err := s.GetOAuthResultByID(ctx, account.ID)
	if err != nil || email != "oauth@example.com" || result.AccessToken != "at-secret" || result.RefreshToken != "rt-secret" {
		t.Fatalf("oauth result = email=%q result=%+v err=%v", email, result, err)
	}
	task, inserted, err := s.CreateOrGetSub2Import(ctx, "sub2-test", account.ID, "op-1", "idem-1", []byte(`{"data":{}}`))
	if err != nil || !inserted || task.State != "queued" {
		t.Fatalf("created task = %+v inserted=%v err=%v", task, inserted, err)
	}
	same, inserted, err := s.CreateOrGetSub2Import(ctx, "sub2-test", account.ID, "op-2", "idem-2", []byte(`{"different":true}`))
	if err != nil || inserted || same.ID != task.ID || same.OperationID != "op-1" {
		t.Fatalf("duplicate task = %+v inserted=%v err=%v", same, inserted, err)
	}
	payload, err := s.GetSub2ImportPayload(ctx, task.ID)
	if err != nil || string(payload) != `{"data":{}}` {
		t.Fatalf("payload = %q err=%v", payload, err)
	}
	if err := s.MarkSub2ImportSending(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
