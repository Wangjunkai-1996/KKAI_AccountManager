package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

func missingTestBinding(t *testing.T, s *Store, delivery AccountDelivery) Sub2Import {
	t.Helper()
	ctx := context.Background()
	binding, _, err := s.CreateOrGetSub2Import(ctx, delivery.DestinationKey, delivery.AccountID, "old-operation", "old-key", []byte(`{"name":"saved"}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateSub2Import(ctx, binding.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	binding, err = s.GetSub2ImportByID(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	return binding
}

func TestSub2MissingRetiresWorkAndFencesReimport(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	delivery := deliveryTestLogin(t, s, "missing@example.test")
	binding := missingTestBinding(t, s, delivery)
	recovery, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, delivery.ID, delivery.AccountID, 42, delivery.CredentialVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateAccountDelivery(ctx, delivery.ID, "verifying", "", "", nil, recovery.ID); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO account_recovery_rechecks(task_id,state,next_check_at,completed_at,updated_at) VALUES(?,'checking',?,?,?)`, recovery.ID, time.Now().UnixMilli(), time.Now().UnixMilli(), time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	batch, _, err := s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: "missing-check", AccountIDs: []int64{delivery.AccountID}, Concurrency: 1, ProxyMode: "direct", DestinationKey: binding.DestinationKey})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`INSERT INTO account_credential_repairs(account_id,state,updated_at) VALUES(?,'checking',?)`, delivery.AccountID, time.Now().UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	missing, err := s.Sub2AccountMissing(ctx, binding.DestinationKey, binding.AccountID)
	if err != nil || !missing {
		t.Fatalf("missing=%v err=%v", missing, err)
	}
	saved, err := s.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || saved.State != "canceled" || saved.NextRetryAt != nil {
		t.Fatalf("delivery=%+v err=%v", saved, err)
	}
	task, err := s.GetAccountRecoveryTaskByID(ctx, recovery.ID)
	if err != nil || task.State != RecoveryCanceled || task.ErrorCode != "sub2_account_missing" || task.NextRetryAt != nil {
		t.Fatalf("recovery=%+v err=%v", task, err)
	}
	if _, err = s.UpdateAccountRecoveryTask(ctx, recovery.ID, RecoveryCompleted, ""); !errors.Is(err, ErrAccountRecoveryNotResumable) {
		t.Fatalf("late completion=%v", err)
	}
	recheck, err := scanAccountRecoveryRecheck(s.db.QueryRowContext(ctx, accountRecheckSelect+` WHERE r.task_id=?`, recovery.ID))
	if err != nil || recheck.State != "canceled" || recheck.NextCheckAt != nil {
		t.Fatalf("recheck=%+v err=%v", recheck, err)
	}
	if err = s.FinishAccountRecoveryRecheck(ctx, recovery.ID, "passed", "", nil); err != nil {
		t.Fatal(err)
	}
	recheck, _ = scanAccountRecoveryRecheck(s.db.QueryRowContext(ctx, accountRecheckSelect+` WHERE r.task_id=?`, recovery.ID))
	if recheck.State != "canceled" {
		t.Fatalf("late recheck revived: %+v", recheck)
	}
	_, checks, err := s.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil || len(checks) != 1 || checks[0].State != "canceled" || (checks[0].RequestAttempted != nil && *checks[0].RequestAttempted) {
		t.Fatalf("checks=%+v err=%v", checks, err)
	}
	var repairState string
	if err = s.db.QueryRow(`SELECT state FROM account_credential_repairs WHERE account_id=?`, delivery.AccountID).Scan(&repairState); err != nil || repairState != "canceled" {
		t.Fatalf("repair=%s err=%v", repairState, err)
	}
	if _, err = s.QueueAccountDelivery(ctx, delivery.AccountID, binding.DestinationKey, DefaultDeliveryOptions()); !errors.Is(err, ErrSub2AccountMissing) {
		t.Fatalf("retry bypass=%v", err)
	}
	// Explicit reimport replaces the intent atomically and preserves lifecycle history.
	binding, _ = s.GetSub2ImportByID(ctx, binding.ID)
	options := DefaultDeliveryOptions()
	options.Merchant = "merchant"
	options.Deadline = "2026-12-01"
	fresh, err := s.AuthorizeRemoteMissingReimport(ctx, binding, delivery.CredentialVersion, "new-operation", "new-key", []byte(`{"new":true}`), options)
	if err != nil || fresh.State != "queued" || fresh.OperationID != "new-operation" || fresh.Sub2AccountID != 0 {
		t.Fatalf("fresh=%+v err=%v", fresh, err)
	}
	newDelivery, err := s.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || newDelivery.ImportOperationID != "new-operation" || newDelivery.State != "queued" || newDelivery.RecoveryTaskID != 0 || !newDelivery.AllowCreate {
		t.Fatalf("new delivery=%+v err=%v", newDelivery, err)
	}
	if _, err = s.UpdateAccountDeliveryForOperation(ctx, delivery, "checking", "", "", nil, 0); !errors.Is(err, ErrAccountDeliverySettled) {
		t.Fatalf("old delivery changed new intent: %v", err)
	}
	if err = s.MarkSub2AccountMissing(ctx, binding); !errors.Is(err, ErrAccountRecoveryStale) {
		t.Fatalf("late 404=%v", err)
	}
	if err = s.UpdateSub2ImportForOperation(ctx, binding, "imported", true, 42, ""); !errors.Is(err, ErrAccountRecoveryStale) {
		t.Fatalf("late import=%v", err)
	}
	if err = s.UpdateSub2ImportForOperation(ctx, fresh, "imported", true, 43, ""); err != nil {
		t.Fatal(err)
	}
	handoff, created, err := s.CreateAccountDeliveryRecoveryTask(ctx, delivery.ID, delivery.AccountID, 43, delivery.CredentialVersion, true)
	if err != nil || !created || handoff.ID == recovery.ID {
		t.Fatalf("new reimport handoff=%+v created=%v err=%v", handoff, created, err)
	}
	old, _ := s.GetAccountRecoveryTaskByID(ctx, recovery.ID)
	if old.State != RecoveryCanceled || old.DeliveryID != 0 || old.Sub2AccountID != 42 {
		t.Fatalf("old history lost=%+v", old)
	}
	events, err := s.ListLifecycleEventsForAccount(ctx, delivery.AccountID, 20)
	if err != nil || len(events) != 1 {
		t.Fatalf("history=%+v err=%v", events, err)
	}
}

func TestSub2MissingNewLoginStaysLocalAndDestinationIsolated(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	delivery := deliveryTestLogin(t, s, "missing-login@example.test")
	binding := missingTestBinding(t, s, delivery)
	other, err := s.QueueAccountDelivery(ctx, delivery.AccountID, "other", DefaultDeliveryOptions())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.LinkSub2Account(ctx, "other", delivery.AccountID, 99, "linked-other"); err != nil {
		t.Fatal(err)
	}
	if err = s.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetAccountDelivery(ctx, other.ID)
	if err != nil || current.State != "queued" {
		t.Fatalf("other destination=%+v %v", current, err)
	}
	account, _ := s.GetAccountByID(ctx, delivery.AccountID)
	_, attempt, err := s.BeginAttempt(ctx, Credentials{Email: account.Email, Password: "new-password", ExistingAccountID: delivery.AccountID})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	result, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{PlanType: "plus", AccessToken: "new-at", RefreshToken: "new-rt"}, binding.DestinationKey)
	if err != nil || result.ID != 0 || result.State != "canceled" {
		t.Fatalf("new login delivery=%+v %v", result, err)
	}
	missing, err := s.Sub2AccountMissing(ctx, binding.DestinationKey, binding.AccountID)
	if err != nil || !missing {
		t.Fatalf("login released missing=%v %v", missing, err)
	}
	_, saved, err := s.GetOAuthResultByID(ctx, delivery.AccountID)
	if err != nil || saved.AccessToken != "new-at" {
		t.Fatalf("OAuth was lost: %v", err)
	}
}

func TestSub2MissingHistoricalEvidenceNotTruncated(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "history-missing@example.test", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.RecordLifecycleEvent(ctx, LifecycleEvent{AccountID: account.ID, Email: account.Email, Source: "sub2", Status: "deleted", Code: "not_found", HTTPStatus: 404, DedupeKey: "sub2:destination:42:not_found"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 1001; i++ {
		if _, err = s.RecordLifecycleEvent(ctx, LifecycleEvent{AccountID: account.ID, Email: account.Email, Source: "login", Status: "active", Code: "other", DedupeKey: fmt.Sprintf("other:%d", i)}); err != nil {
			t.Fatal(err)
		}
	}
	missing, err := s.Sub2AccountMissing(ctx, "destination", account.ID)
	if err != nil || !missing {
		t.Fatalf("old missing lost: %v %v", missing, err)
	}
	missing, err = s.Sub2AccountMissing(ctx, "destination-extra", account.ID)
	if err != nil || missing {
		t.Fatalf("destination leaked: %v %v", missing, err)
	}
	if _, err = s.LinkSub2Account(ctx, "destination", account.ID, 43, "linked-override"); !errors.Is(err, ErrSub2AccountMissing) {
		t.Fatalf("history relinked: %v", err)
	}
}

func TestSub2MissingReimportRollbackOnDeliveryFailure(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	delivery := deliveryTestLogin(t, s, "rollback-missing@example.test")
	binding := missingTestBinding(t, s, delivery)
	if err := s.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	binding, _ = s.GetSub2ImportByID(ctx, binding.ID)
	if _, err := s.db.Exec(`CREATE TRIGGER reject_reimport BEFORE INSERT ON account_deliveries BEGIN SELECT RAISE(ABORT,'blocked'); END`); err != nil {
		t.Fatal(err)
	}
	options := DefaultDeliveryOptions()
	options.Merchant = "merchant"
	options.Deadline = "later"
	if _, err := s.AuthorizeRemoteMissingReimport(ctx, binding, delivery.CredentialVersion, "new-op", "new-key", []byte(`{}`), options); err == nil {
		t.Fatal("failure ignored")
	}
	current, _ := s.GetSub2ImportByID(ctx, binding.ID)
	if current.State != Sub2RemoteMissing || current.OperationID != binding.OperationID {
		t.Fatalf("partially committed: %+v", current)
	}
}

func TestSub2MissingReimportFencesCredentialVersionAndOperation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	delivery := deliveryTestLogin(t, s, "missing-version@example.test")
	binding := missingTestBinding(t, s, delivery)
	if err := s.MarkSub2AccountMissing(ctx, binding); err != nil {
		t.Fatal(err)
	}
	freshBinding, err := s.GetSub2ImportByID(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	binding = freshBinding
	oldVersion := delivery.CredentialVersion
	oldOperation := binding.OperationID
	attempt, err := s.StartAttempt(ctx, "missing-version@example.test")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{PlanType: "plus", AccessToken: "fresh-at", RefreshToken: "fresh-rt"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	options := DefaultDeliveryOptions()
	options.Merchant = "merchant"
	options.Deadline = "later"
	if _, err := s.AuthorizeRemoteMissingReimport(ctx, binding, oldVersion, "stale-operation", "stale-key", []byte(`{}`), options); !errors.Is(err, ErrAccountRecoveryStale) {
		t.Fatalf("stale credential version authorized: %v", err)
	}
	currentVersion, err := s.GetAccountCredentialVersion(ctx, delivery.AccountID)
	if err != nil || currentVersion != attempt.ID {
		t.Fatalf("current version=%d attempt=%d err=%v", currentVersion, attempt.ID, err)
	}
	fresh, err := s.GetSub2ImportByID(ctx, binding.ID)
	if err != nil || fresh.OperationID != oldOperation || fresh.State != Sub2RemoteMissing {
		t.Fatalf("stale request changed binding: %+v %v", fresh, err)
	}
	if _, err := s.AuthorizeRemoteMissingReimport(ctx, fresh, currentVersion, "fresh-operation", "fresh-key", []byte(`{}`), options); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AuthorizeRemoteMissingReimport(ctx, fresh, currentVersion, "replayed-operation", "replayed-key", []byte(`{}`), options); !errors.Is(err, ErrAccountRecoveryStale) {
		t.Fatalf("replayed operation changed queued binding: %v", err)
	}
}

func TestAccountDeliveryRepairDoesNotGainCreateIntent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	_, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "repair-no-create@example.test", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	task, err := s.FinishAttemptAndQueueDeliveryUpdate(ctx, attempt.ID, &Result{PlanType: "plus", AccessToken: "at", RefreshToken: "rt"}, "destination")
	if err != nil || task.AllowCreate {
		t.Fatalf("repair gained create intent: %+v %v", task, err)
	}
	if err = s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetAccountDelivery(ctx, task.ID)
	if err != nil || current.AllowCreate {
		t.Fatalf("restart granted create: %+v %v", current, err)
	}
}

func TestAccountCredentialVersionDTOContainsNoSecrets(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	task := deliveryTestLogin(t, s, "safe-version@example.test")
	accounts, err := s.ListAccounts(ctx)
	if err != nil || len(accounts) != 1 || accounts[0].CredentialVersion != task.CredentialVersion {
		t.Fatalf("account versions=%+v err=%v", accounts, err)
	}
	raw, err := json.Marshal(accounts)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fixture-password", "fixture-at", "fixture-rt", "password_cipher", "access_token", "refresh_token", "totp_secret"} {
		if strings.Contains(string(raw), secret) {
			t.Fatalf("list leaked %s", secret)
		}
	}
	attempt, err := s.StartAttempt(ctx, accounts[0].Email)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err = s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "invalid_password"}); err != nil {
		t.Fatal(err)
	}
	account, err := s.GetAccountByID(ctx, task.AccountID)
	if err != nil || account.CredentialVersion != task.CredentialVersion {
		t.Fatalf("failure advanced successful version: %+v %v", account, err)
	}
}

func TestAccountDeliveryLegacyMigrationDoesNotAuthorizeCreation(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	task := deliveryTestLogin(t, s, "legacy-intent@example.test")
	for _, column := range []string{"allow_create", "import_operation_id"} {
		if _, err := s.db.Exec(`ALTER TABLE account_deliveries DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	current, err := s.GetAccountDelivery(ctx, task.ID)
	if err != nil || current.AllowCreate || current.ImportOperationID != "" {
		t.Fatalf("migration granted intent: %+v %v", current, err)
	}
}

func TestAccountDeliveryNewLoginCannotRecreateHistoricalUnboundAccount(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	old := deliveryTestLogin(t, s, "historical-unbound@example.test")
	if !old.AllowCreate {
		t.Fatal("first login lost create intent")
	}
	if _, err := s.UpdateAccountDelivery(ctx, old.ID, "completed", "", "", nil, 0); err != nil {
		t.Fatal(err)
	}
	account, _ := s.GetAccountByID(ctx, old.AccountID)
	_, attempt, err := s.BeginAttempt(ctx, Credentials{Email: account.Email, Password: "new-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	fresh, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{PlanType: "plus", AccessToken: "new-at", RefreshToken: "new-rt"}, old.DestinationKey)
	if err != nil || fresh.AllowCreate {
		t.Fatalf("historical account gained create=%+v err=%v", fresh, err)
	}
}

func TestAccountDeliveryExplicitImportAfterUnboundRepair(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	_, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "repair-first-import@example.test", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := s.FinishAttemptAndQueueDeliveryUpdate(ctx, attempt.ID, &Result{PlanType: "plus", AccessToken: "at", RefreshToken: "rt"}, "destination")
	attempt.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateAccountDelivery(ctx, old.ID, "canceled", Sub2MissingMessage, "", nil, 0); err != nil {
		t.Fatal(err)
	}
	options := DefaultDeliveryOptions()
	options.Merchant = "merchant"
	options.Deadline = "later"
	fresh, err := s.QueueAccountDelivery(ctx, old.AccountID, "destination", options)
	if err != nil || fresh.State != "queued" || !fresh.AllowCreate || fresh.ImportOperationID == old.ImportOperationID {
		t.Fatalf("explicit import blocked: %+v %v", fresh, err)
	}
	if _, err = s.UpdateAccountDeliveryForOperation(ctx, old, "canceled", Sub2MissingMessage, "", nil, 0); !errors.Is(err, ErrAccountDeliverySettled) {
		t.Fatalf("old repair overwrote intent: %v", err)
	}
}
