package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountRecoveryTaskLifecycle(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "recover@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}

	task, created, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 77, 1234, true)
	if err != nil || !created {
		t.Fatalf("create task = %+v,%v,%v", task, created, err)
	}
	if task.State != RecoveryQueued || task.CheckID != 77 || !task.OriginalSchedulable || task.Sub2AccountID != 1234 {
		t.Fatalf("created task = %+v", task)
	}
	duplicate, created, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 88, 9999, false)
	if err != nil || created || duplicate.ID != task.ID {
		t.Fatalf("duplicate task = %+v,%v,%v", duplicate, created, err)
	}

	claimed, err := s.ClaimAccountRecoveryTask(ctx, task.ID)
	if err != nil || claimed.State != RecoveryValidating {
		t.Fatalf("claim = %+v,%v", claimed, err)
	}
	if _, err = s.ClaimAccountRecoveryTask(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryNotQueued) {
		t.Fatalf("second claim = %v", err)
	}
	updated, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, "")
	if err != nil || updated.State != RecoveryCompleted {
		t.Fatalf("update = %+v,%v", updated, err)
	}

	second, created, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 90, 5678, false)
	if err != nil || !created || second.ID == task.ID {
		t.Fatalf("new task after terminal = %+v,%v,%v", second, created, err)
	}
	all, err := s.ListAccountRecoveryTasks(ctx, "", 0)
	if err != nil || len(all) != 2 {
		t.Fatalf("list = %+v,%v", all, err)
	}
	got, err := s.GetAccountRecoveryTaskByID(ctx, second.ID)
	if err != nil || got.AccountEmail != account.Email {
		t.Fatalf("get = %+v,%v", got, err)
	}
}

func TestAccountRecoveryTaskRecoveryOnRestart(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "restart@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 1, 2, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	recovered, err := reopened.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || recovered.State != RecoveryUnknown || recovered.LastError == "" || recovered.FailureStage != RecoveryValidating || recovered.ErrorCode != "process_interrupted" || recovered.RetryAction != "relogin" || recovered.NextRetryAt == nil || recovered.RetryCount != 1 {
		t.Fatalf("recovered task = %+v,%v", recovered, err)
	}
}

func TestAccountRecoveryTaskRequiresAccount(t *testing.T) {
	s, _ := testStore(t)
	_, _, err := s.CreateOrGetAccountRecoveryTask(context.Background(), 9999, 1, 2, true)
	if !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account = %v", err)
	}
}

func TestUpdateOAuthResultByIDRotatesStoredTokens(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "rotate@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "old-at", RefreshToken: "old-rt", ChatGPTAccountID: "workspace"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if err = s.UpdateOAuthResultByID(ctx, account.ID, Result{AccessToken: "new-at", RefreshToken: "new-rt", ExpiresAt: 123}); err != nil {
		t.Fatal(err)
	}
	_, result, err := s.GetOAuthResultByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.AccessToken != "new-at" || result.RefreshToken != "new-rt" || result.ChatGPTAccountID != "workspace" {
		t.Fatalf("rotated result = %+v", result)
	}
	version, err := s.GetAccountCredentialVersion(ctx, account.ID)
	if err != nil || version <= attempt.ID {
		t.Fatalf("refresh did not advance credential version: version=%d old=%d err=%v", version, attempt.ID, err)
	}
}

func TestOAuthMissingCredentialsDiffersFromStorageError(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "missing-oauth@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetOAuthResultByID(ctx, account.ID); !errors.Is(err, ErrOAuthCredentialsMissing) {
		t.Fatalf("missing OAuth credentials=%v", err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET access_token_cipher=? WHERE id=?`, []byte("corrupt-cipher"), account.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.GetOAuthResultByID(ctx, account.ID); err == nil || errors.Is(err, ErrOAuthCredentialsMissing) {
		t.Fatalf("corrupt cipher classified as missing credentials: %v", err)
	}
	if _, _, err := s.GetOAuthResultByID(ctx, account.ID+100); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account=%v", err)
	}
}

func TestOAuthRefreshInvalidatesPreviousChecks(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "oauth-check")
	checkTestBatch(t, s, "before-refresh", 1, id)
	work := checkTestClaim(t, s)
	status := 401
	if _, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{HTTPStatus: &status, Outcome: "auth_failed"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "rotated-at", RefreshToken: "rotated-rt"}); err != nil {
		t.Fatal(err)
	}
	checks, err := s.ListAccountChecks(ctx, id, 1)
	if err != nil || len(checks) != 1 || checks[0].Freshness != "stale" {
		t.Fatalf("old check still eligible after refresh: %+v err=%v", checks, err)
	}
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, work.Check.ID, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceCredentialAttemptID != *work.Check.CredentialAttemptID {
		t.Fatal("task did not bind the checked credential version")
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("stale queued task accepted: %v", err)
	}
}

func TestRecoveryLeaseExcludesNormalLoginAndDelete(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "lease")
	credentials, err := s.GetCredentialsByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, _, err := s.BeginAttempt(ctx, credentials); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("normal login entered recovery: %v", err)
	}
	if _, err := s.StartAttempt(ctx, credentials.Email); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("legacy login entered recovery: %v", err)
	}
	if err := s.DeleteAccountByID(ctx, id); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("deleted recovering account: %v", err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "racing-at", RefreshToken: "racing-rt"}); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("external refresh entered recovery: %v", err)
	}
	lease.Release()
	_, attempt, err := s.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if _, err := s.AcquireAccountRecovery(ctx, id); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("recovery entered normal login: %v", err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "racing-at", RefreshToken: "racing-rt"}); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("refresh overwrote running login: %v", err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, nil); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveryCheckpointResumeAndCredentialVersionGuard(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "resume")
	version, err := s.GetAccountCredentialVersion(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 777, true)
	if err != nil {
		t.Fatal(err)
	}
	if task.SourceCredentialAttemptID != version {
		t.Fatalf("source=%d want=%d", task.SourceCredentialAttemptID, version)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryLoggingIn, "test pause confirmed"); err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	newVersion, err := lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "saved-at", RefreshToken: "saved-rt"})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryUnknown, "Sub2 write response unknown")
	if err != nil || !failed.Resumable || failed.ResultCredentialAttemptID != newVersion {
		t.Fatalf("checkpoint=%+v err=%v", failed, err)
	}
	if err := s.RecoverAccountRecoveryTasks(ctx); err != nil {
		t.Fatal(err)
	}
	resumed, err := s.ResumeAccountRecoveryTask(ctx, task.ID)
	if err != nil || resumed.State != RecoveryQueued || resumed.ID != task.ID || !resumed.OriginalSchedulable || resumed.ResultCredentialAttemptID != newVersion {
		t.Fatalf("resume=%+v err=%v", resumed, err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "second-at", RefreshToken: "second-rt"}); !errors.Is(err, ErrAccountRecoveryNotResumable) {
		t.Fatalf("overwrote completed checkpoint: %v", err)
	}
	_, tokens, err := s.GetOAuthResultByID(ctx, id)
	if err != nil || tokens.AccessToken != "saved-at" || tokens.RefreshToken != "saved-rt" {
		t.Fatalf("resume changed saved credentials, err=%v", err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryUnknown, "retry interrupted"); err != nil {
		t.Fatal(err)
	}
	lease.Release()
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("stale task accepted: %v", err)
	}
	if _, err := s.ResumeAccountRecoveryTask(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("stale checkpoint resumed: %v", err)
	}
	stale, err := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || stale.Resumable {
		t.Fatalf("stale checkpoint advertised resumable: %+v err=%v", stale, err)
	}
}

func TestRecoveryCredentialCheckpointIsAtomic(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "atomic-checkpoint")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryLoggingIn, "test pause confirmed"); err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if _, err := s.db.Exec(`CREATE TRIGGER fail_checkpoint BEFORE UPDATE OF result_credential_attempt_id ON account_recovery_tasks BEGIN SELECT RAISE(ABORT,'checkpoint persistence failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "not-committed-at", RefreshToken: "not-committed-rt"}); err == nil {
		t.Fatal("checkpoint persistence error was ignored")
	}
	version, err := s.GetAccountCredentialVersion(ctx, id)
	if err != nil || version != task.SourceCredentialAttemptID {
		t.Fatalf("partial refresh commit: version=%d err=%v", version, err)
	}
	credentials, err := s.GetCredentialsByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	_, attempt, err := lease.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	result := &Result{AccessToken: "login-at", RefreshToken: "login-rt", ChatGPTAccountID: "workspace"}
	if err := lease.FinishAttempt(ctx, task.ID, attempt.ID, true, result, nil); err == nil {
		t.Fatal("login checkpoint persistence error was ignored")
	}
	version, err = s.GetAccountCredentialVersion(ctx, id)
	if err != nil || version != task.SourceCredentialAttemptID {
		t.Fatalf("partial login commit: version=%d err=%v", version, err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_checkpoint`); err != nil {
		t.Fatal(err)
	}
	if err := lease.FinishAttempt(ctx, task.ID, attempt.ID, true, result, nil); err != nil {
		t.Fatal(err)
	}
	checkpoint, err := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || checkpoint.ResultCredentialAttemptID != attempt.ID {
		t.Fatalf("login checkpoint=%+v err=%v", checkpoint, err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "replayed-at", RefreshToken: "replayed-rt"}, nil); err == nil {
		t.Fatal("old attempt replay overwrote credentials")
	}
}

func TestRecoveryRejectsMissingVersionAndUncheckpointedResume(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "no-version@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("zero version accepted: %v", err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryUnknown, "RT response lost"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResumeAccountRecoveryTask(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryNotResumable) {
		t.Fatalf("uncheckpointed task resumed: %v", err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := s.UpdateAccountRecoveryTask(canceled, task.ID, RecoveryCompleted, ""); err == nil {
		t.Fatal("state persistence cancellation was ignored")
	}
}

func TestRecoveryCheckpointSurvivesRestart(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "durable-checkpoint")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryLoggingIn, "test pause confirmed"); err != nil {
		t.Fatal(err)
	}
	lease, err := s.AcquireAccountRecovery(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	version, err := lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "durable-at", RefreshToken: "durable-rt"})
	lease.Release()
	if err != nil {
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
	recovered, err := reopened.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || recovered.State != RecoveryUnknown || !recovered.Resumable || recovered.ResultCredentialAttemptID != version {
		t.Fatalf("restart checkpoint=%+v err=%v", recovered, err)
	}
	if _, err := reopened.ResumeAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	_, result, err := reopened.GetOAuthResultByID(ctx, id)
	if err != nil || result.AccessToken != "durable-at" || result.RefreshToken != "durable-rt" {
		t.Fatalf("restart lost saved credentials, err=%v", err)
	}
}

func TestRecoveryMigrationAddsCheckpointColumns(t *testing.T) {
	s, dir := testStore(t)
	if _, err := s.db.Exec(`DROP TABLE account_recovery_tasks; CREATE TABLE account_recovery_tasks (id INTEGER PRIMARY KEY AUTOINCREMENT, account_id INTEGER NOT NULL REFERENCES accounts(id) ON DELETE CASCADE, check_id INTEGER NOT NULL DEFAULT 0, sub2_account_id INTEGER NOT NULL, original_schedulable INTEGER NOT NULL DEFAULT 0, state TEXT NOT NULL, last_error TEXT NOT NULL DEFAULT '', created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL);`); err != nil {
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
	ctx := context.Background()
	account, err := reopened.UpsertCredentials(ctx, Credentials{Email: "migration@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	task, _, err := reopened.CreateOrGetAccountRecoveryTask(ctx, account.ID, 0, 42, false)
	if err != nil || task.SourceCredentialAttemptID != 0 || task.ResultCredentialAttemptID != 0 || task.Purpose != "recovery" || task.DeliveryID != 0 || task.RetryCount != 0 || task.RequiresAction || task.NextRetryAt != nil {
		t.Fatalf("migration task=%+v err=%v", task, err)
	}
}

func TestRecoveryRetryUsesLoginAfterUncheckpointedFailure(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "retry-login")
	first, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 10, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if needed, err := s.AccountRecoveryNeedsLogin(ctx, first.ID); err != nil || needed {
		t.Fatalf("initial needs login=%v err=%v", needed, err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, first.ID, RecoveryUnknown, "refresh response lost"); err != nil {
		t.Fatal(err)
	}
	retry, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 11, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	if needed, err := s.AccountRecoveryNeedsLogin(ctx, retry.ID); err != nil || !needed {
		t.Fatalf("new check reused uncertain RT: needed=%v err=%v", needed, err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, retry.ID, RecoveryFailed, "login failed"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "new-version-at", RefreshToken: "new-version-rt"}); err != nil {
		t.Fatal(err)
	}
	newVersionTask, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 12, 42, false)
	if err != nil {
		t.Fatal(err)
	}
	if needed, err := s.AccountRecoveryNeedsLogin(ctx, newVersionTask.ID); err != nil || needed {
		t.Fatalf("new credential incorrectly blocked by old failure: needed=%v err=%v", needed, err)
	}
	if _, err := s.AccountRecoveryNeedsLogin(ctx, 9999); !errors.Is(err, ErrAccountRecoveryNotFound) {
		t.Fatalf("missing task=%v", err)
	}
}

func TestRecoveryLatestPerAccountAndFIFOQueue(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	firstAccount := checkTestAccount(t, s, "first-queued")
	secondAccount := checkTestAccount(t, s, "recent-history")
	first, _, err := s.CreateOrGetAccountRecoveryTask(ctx, firstAccount, 1, 41, true)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 501; i++ {
		if _, err := tx.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,check_id,sub2_account_id,state,created_at,updated_at) VALUES(?,?,42,'failed',1,1)`, secondAccount, i+2); err != nil {
			tx.Rollback()
			t.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	second, _, err := s.CreateOrGetAccountRecoveryTask(ctx, secondAccount, 999, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	queued, err := s.ListAccountRecoveryTasks(ctx, RecoveryQueued, 1)
	if err != nil || len(queued) != 1 || queued[0].ID != first.ID {
		t.Fatalf("queue not FIFO: %+v err=%v", queued, err)
	}
	latest, err := s.ListLatestAccountRecoveryTasks(ctx)
	if err != nil || len(latest) != 2 || latest[0].ID != second.ID || latest[1].ID != first.ID {
		t.Fatalf("latest tasks lost older account: %+v err=%v", latest, err)
	}
}

func TestRecoveryQueriesStayWithinDestination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "recovery-destination@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "alpha", account.ID, 101, "linked-alpha"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "beta", account.ID, 202, "linked-beta"); err != nil {
		t.Fatal(err)
	}
	alpha, created, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 11, 101, true, "alpha")
	if err != nil || !created {
		t.Fatalf("alpha task = %+v,%v,%v", alpha, created, err)
	}
	alphaDue := time.Now().Add(-time.Second)
	if _, err := s.RecordAccountRecoveryFailure(ctx, alpha.ID, RecoveryFailure{State: RecoveryFailed, Code: "alpha", Message: "retry", RetryAction: "resume", NextRetryAt: &alphaDue}); err != nil {
		t.Fatal(err)
	}
	beta, created, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 12, 202, true, "beta")
	if err != nil || !created {
		t.Fatalf("beta task = %+v,%v,%v", beta, created, err)
	}
	betaDue := time.Now().Add(-time.Second)
	if _, err := s.RecordAccountRecoveryFailure(ctx, beta.ID, RecoveryFailure{State: RecoveryFailed, Code: "beta", Message: "retry", RetryAction: "resume", NextRetryAt: &betaDue}); err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetLatestAccountRecoveryTaskForDestination(ctx, account.ID, "alpha"); err != nil || got.ID != alpha.ID {
		t.Fatalf("alpha latest = %+v,%v", got, err)
	}
	if got, err := s.GetLatestAccountRecoveryTaskForDestination(ctx, account.ID, "beta"); err != nil || got.ID != beta.ID {
		t.Fatalf("beta latest = %+v,%v", got, err)
	}
	latest, err := s.ListLatestAccountRecoveryTasksForDestination(ctx, "alpha")
	if err != nil || len(latest) != 1 || latest[0].ID != alpha.ID {
		t.Fatalf("alpha list = %+v,%v", latest, err)
	}
	if tasks, err := s.ListAccountRecoveryTasksForDestination(ctx, RecoveryQueued, 10, "alpha"); err != nil || len(tasks) != 0 {
		t.Fatalf("alpha queue leaked beta task = %+v,%v", tasks, err)
	}
	due, err := s.ListDueAccountRecoveryTasksForDestination(ctx, time.Now(), 10, "alpha")
	if err != nil || len(due) != 1 || due[0].ID != alpha.ID {
		t.Fatalf("alpha due = %+v,%v", due, err)
	}
	due, err = s.ListDueAccountRecoveryTasksForDestination(ctx, time.Now(), 10, "beta")
	if err != nil || len(due) != 1 || due[0].ID != beta.ID {
		t.Fatalf("beta due = %+v,%v", due, err)
	}
	// Explicit destination ownership remains isolated even when two targets
	// happen to reuse the same remote account identifier.
	other, err := s.UpsertCredentials(ctx, Credentials{Email: "recovery-destination-same-id@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "alpha", other.ID, 303, "linked-alpha-same"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "beta", other.ID, 303, "linked-beta-same"); err != nil {
		t.Fatal(err)
	}
	alphaSame, _, err := s.CreateOrGetAccountRecoveryTask(ctx, other.ID, 21, 303, true, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, alphaSame.ID, RecoveryCompleted, ""); err != nil {
		t.Fatal(err)
	}
	betaSame, _, err := s.CreateOrGetAccountRecoveryTask(ctx, other.ID, 22, 303, true, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if got, err := s.GetLatestAccountRecoveryTaskForDestination(ctx, other.ID, "alpha"); err != nil || got.ID != alphaSame.ID {
		t.Fatalf("same remote id alpha latest = %+v,%v", got, err)
	}
	if got, err := s.GetLatestAccountRecoveryTaskForDestination(ctx, other.ID, "beta"); err != nil || got.ID != betaSame.ID {
		t.Fatalf("same remote id beta latest = %+v,%v", got, err)
	}
}

func TestLegacyRecoveryMigrationLeavesAmbiguousDestinationEmpty(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "legacy-destination@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "alpha", account.ID, 404, "linked-alpha-legacy"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "beta", account.ID, 404, "linked-beta-legacy"); err != nil {
		t.Fatal(err)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,sub2_account_id,destination_key,state,created_at,updated_at) VALUES(?,?, '', 'completed', ?, ?)`, account.ID, 404, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrateAccountRecoveryTasks(); err != nil {
		t.Fatal(err)
	}
	var destination string
	if err := s.db.QueryRowContext(ctx, `SELECT destination_key FROM account_recovery_tasks WHERE id=?`, taskID).Scan(&destination); err != nil {
		t.Fatal(err)
	}
	if destination != "" {
		t.Fatalf("ambiguous legacy destination = %q, want empty", destination)
	}
}

func TestLegacyDeliveryRecoveryMigrationUsesDeliveryDestination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "legacy-delivery-destination@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "alpha", account.ID, 505, "linked-alpha-delivery"); err != nil {
		t.Fatal(err)
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO account_deliveries(account_id,credential_version,destination_key,state,created_at,updated_at,options_json) VALUES(?,?,?,?,?,?,?)`, account.ID, 1, "beta", "queued", time.Now().UnixMilli(), time.Now().UnixMilli(), `{"group_ids":[],"priority":1,"concurrency":3}`)
	if err != nil {
		t.Fatal(err)
	}
	deliveryID, err := result.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	taskResult, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,sub2_account_id,delivery_id,destination_key,state,created_at,updated_at) VALUES(?,?,?, 'alpha', 'completed', ?, ?)`, account.ID, 505, deliveryID, time.Now().UnixMilli(), time.Now().UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	taskID, err := taskResult.LastInsertId()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.migrateAccountRecoveryTasks(); err != nil {
		t.Fatal(err)
	}
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	var destination string
	if err := s.db.QueryRowContext(ctx, `SELECT destination_key FROM account_recovery_tasks WHERE id=?`, taskID).Scan(&destination); err != nil {
		t.Fatal(err)
	}
	if destination != "beta" {
		t.Fatalf("delivery destination = %q, want beta", destination)
	}
}
