package store

import (
	"context"
	"path/filepath"
	"testing"
)

func TestDestinationMigrationReopensLegacySchema(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	tasks := make(map[string]int64)
	accounts := make(map[string]int64)
	wantDestinations := make(map[string]string)
	for _, tc := range []struct {
		name                string
		bindings            []string
		deliveryDestination string
		orphanDelivery      bool
		wantDestination     string
	}{
		{name: "unique", bindings: []string{"alpha"}, wantDestination: "alpha"},
		{name: "ambiguous", bindings: []string{"alpha", "beta"}},
		{name: "delivery", bindings: []string{"alpha"}, deliveryDestination: "beta", wantDestination: "beta"},
		{name: "orphan", bindings: []string{"alpha"}, orphanDelivery: true},
	} {
		account, err := s.UpsertCredentials(ctx, Credentials{Email: tc.name + "@migration.test", Password: "legacy-password"})
		if err != nil {
			t.Fatal(err)
		}
		accounts[tc.name] = account.ID
		for _, destination := range tc.bindings {
			if _, err := s.LinkSub2Account(ctx, destination, account.ID, 42, "linked-"+tc.name+"-"+destination); err != nil {
				t.Fatal(err)
			}
		}
		var deliveryID int64
		purpose := "recovery"
		if tc.deliveryDestination != "" {
			checkTestLogin(t, s, account.Email)
			delivery, err := s.QueueAccountDelivery(ctx, account.ID, tc.deliveryDestination, DefaultDeliveryOptions())
			if err != nil {
				t.Fatal(err)
			}
			deliveryID, purpose = delivery.ID, "delivery"
		} else if tc.orphanDelivery {
			deliveryID, purpose = 999999, "delivery"
		}
		result, err := s.db.Exec(`INSERT INTO account_recovery_tasks(account_id,sub2_account_id,purpose,delivery_id,state,last_error,retry_count,next_retry_at,created_at,updated_at) VALUES(?,42,?,?,'failed','legacy failure',2,123,100,101)`, account.ID, purpose, deliveryID)
		if err != nil {
			t.Fatal(err)
		}
		tasks[tc.name], err = result.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		wantDestinations[tc.name] = tc.wantDestination
	}

	checkTestLogin(t, s, "unique@migration.test")
	batch, _, err := s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: "legacy-migration-check", AccountIDs: []int64{accounts["unique"]}, Concurrency: 1, ProxyMode: "direct", DestinationKey: "alpha"})
	if err != nil {
		t.Fatal(err)
	}
	work := checkTestClaim(t, s)
	if _, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "credential_invalid"}); err != nil {
		t.Fatal(err)
	}
	// Remove the new fields only after persisting real data, so Open must
	// upgrade both old table shapes instead of merely rerunning a migration.
	for _, table := range []string{"account_recovery_tasks", "account_check_batches"} {
		if _, err := s.db.Exec(`ALTER TABLE ` + table + ` DROP COLUMN destination_key`); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatalf("open legacy database: %v", err)
	}
	defer reopened.Close()

	for name, id := range tasks {
		t.Run(name, func(t *testing.T) {
			task, err := reopened.GetAccountRecoveryTaskByID(ctx, id)
			if err != nil {
				t.Fatal(err)
			}
			if task.DestinationKey != wantDestinations[name] {
				t.Fatalf("destination=%q, want %q", task.DestinationKey, wantDestinations[name])
			}
			if task.AccountID != accounts[name] || task.Sub2AccountID != 42 || task.State != RecoveryFailed || task.LastError != "legacy failure" || task.RetryCount != 2 || task.NextRetryAt == nil || task.NextRetryAt.UnixMilli() != 123 || task.CreatedAt.UnixMilli() != 100 || task.UpdatedAt.UnixMilli() != 101 {
				t.Fatalf("migration changed legacy task: %+v", task)
			}
			credentials, err := reopened.GetCredentialsByID(ctx, accounts[name])
			if err != nil || credentials.Password != "legacy-password" {
				t.Fatalf("legacy credentials lost: %v", err)
			}
		})
	}
	gotBatch, checks, err := reopened.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil || gotBatch.DestinationKey != "" || gotBatch.State != "completed" || len(checks) != 1 || checks[0].ID != work.Check.ID || checks[0].Outcome != "credential_invalid" {
		t.Fatalf("legacy check batch changed: batch=%+v checks=%+v err=%v", gotBatch, checks, err)
	}
	checks, err = reopened.ListAccountChecks(ctx, accounts["unique"], 20, "alpha")
	if err != nil || len(checks) != 1 || checks[0].ID != work.Check.ID {
		t.Fatalf("unique legacy check disappeared from its destination: checks=%+v err=%v", checks, err)
	}
}
