package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestCredentialRepairPersistsEditsAndContinuationAtomically(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "edit@example.test", Password: "old-password", TOTPSecret: "OLDCODE", Proxy: "http://old-proxy.test:80"})
	if err != nil {
		t.Fatal(err)
	}
	password, empty := "  new-password  ", ""
	if _, err := s.db.Exec(`CREATE TRIGGER reject_repair BEFORE INSERT ON account_credential_repairs BEGIN SELECT RAISE(ABORT,'repair failed'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAccountCredentials(ctx, account.ID, CredentialPatch{Password: &password}, true); err == nil {
		t.Fatal("missing repair was silently dropped")
	}
	credentials, _ := s.GetCredentialsByID(ctx, account.ID)
	if credentials.Password != "old-password" {
		t.Fatal("credential changed without durable intent")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_repair`); err != nil {
		t.Fatal(err)
	}
	repair, err := s.PatchAccountCredentials(ctx, account.ID, CredentialPatch{Password: &password, TOTPSecret: &empty}, true)
	if err != nil || repair.State != "queued" {
		t.Fatalf("save: %+v, %v", repair, err)
	}
	credentials, _ = s.GetCredentialsByID(ctx, account.ID)
	if credentials.Password != password || credentials.TOTPSecret != "" || credentials.Proxy != "http://old-proxy.test:80" {
		t.Fatal("patch changed an omitted field, trimmed password, or failed explicit clear")
	}
	claimed, err := s.ClaimCredentialRepair(ctx, time.Now())
	if err != nil || claimed == nil {
		t.Fatalf("claim: %+v, %v", claimed, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	restored, err := s.ClaimCredentialRepair(ctx, time.Now())
	if err != nil || restored == nil || restored.Revision != repair.Revision {
		t.Fatalf("repair lost after restart: %+v, %v", restored, err)
	}
	lease, err := s.AcquireAccountRecovery(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PatchAccountCredentials(ctx, account.ID, CredentialPatch{Proxy: &empty}, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("edited credentials during worker lease: %v", err)
	}
	lease.Release()
	savedOnly, err := s.PatchAccountCredentials(ctx, account.ID, CredentialPatch{Proxy: &empty}, false)
	if err != nil || savedOnly.State != "canceled" || savedOnly.Revision != restored.Revision+1 {
		t.Fatalf("save only: %+v, %v", savedOnly, err)
	}
	if err := s.FinishCredentialRepair(ctx, *restored, "requires_action", "stale failure", nil); err != nil {
		t.Fatal(err)
	}
	current, _ := s.GetCredentialRepair(ctx, account.ID)
	if current.State != "canceled" || current.LastError != "" {
		t.Fatal("old worker overwrote latest edit")
	}
}

func TestCredentialRepairRecoveryHandoffIsAtomicAndNotReplayed(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "repair-handoff")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 901, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryFailed, Stage: RecoveryLoggingIn, Code: "invalid_password", RetryAction: "manual", ManualAction: "需要更新密码"}); err != nil {
		t.Fatal(err)
	}
	password := "corrected-password"
	if _, err := s.PatchAccountCredentials(ctx, id, CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	repair, err := s.ClaimCredentialRepair(ctx, time.Now())
	if err != nil || repair == nil {
		t.Fatalf("claim: %+v %v", repair, err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_repair_completion BEFORE UPDATE OF state ON account_credential_repairs WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'completion failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryAccountRecoveryForCredentialRepair(ctx, *repair, task.ID); err == nil {
		t.Fatal("handoff ignored repair completion failure")
	}
	failed, _ := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if failed.State != RecoveryFailed || failed.RetryAction != "manual" {
		t.Fatalf("recovery requeued without consuming repair: %+v", failed)
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_repair_completion`); err != nil {
		t.Fatal(err)
	}
	if err := s.RetryAccountRecoveryForCredentialRepair(ctx, *repair, task.ID); err != nil {
		t.Fatal(err)
	}
	consumed, _ := s.GetCredentialRepair(ctx, id)
	queued, _ := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if consumed.State != "completed" || queued.State != RecoveryQueued {
		t.Fatalf("incomplete atomic handoff: repair=%+v task=%+v", consumed, queued)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// The requeued recovery failed again before the repair worker's next scan.
	// Its new failure policy must survive both restart and stale handoff calls.
	deadline := time.Now().Add(time.Hour).Truncate(time.Millisecond)
	if _, err := s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryFailed, Stage: RecoveryLoggingIn, Code: "rate_limited", RetryAction: "relogin", NextRetryAt: &deadline}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimCredentialRepair(ctx, time.Now()); err != nil || claimed != nil {
		t.Fatalf("completed edit replayed after restart: %+v %v", claimed, err)
	}
	if err := s.RetryAccountRecoveryForCredentialRepair(ctx, *repair, task.ID); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("consumed revision replay allowed: %v", err)
	}
	failed, _ = s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if failed.State != RecoveryFailed || failed.NextRetryAt == nil || !failed.NextRetryAt.Equal(deadline) {
		t.Fatalf("repair replay bypassed recovery backoff: %+v", failed)
	}
}
