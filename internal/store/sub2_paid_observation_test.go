package store

import (
	"context"
	"errors"
	"testing"
)

func TestSub2WritesRequirePaidObservationForCurrentCredentials(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "observation-gate@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{PlanType: "plus", AccessToken: "at", RefreshToken: "rt", ChatGPTAccountID: "workspace"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err = s.GetAccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.CreateOrGetSub2Import(ctx, "fixture", account.ID, "old-op", "old-key", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}

	// Simulate the old database shape. The account still carries a historical
	// paid plan, while the current successful credential version has no claim.
	if _, err := s.db.Exec(`UPDATE accounts SET sub2_policy='eligible',plan_type='plus' WHERE id=?`, account.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE login_attempts SET plan_type='',observation_source='token_refresh' WHERE id=?`, account.CredentialVersion); err != nil {
		t.Fatal(err)
	}
	if _, inserted, err := s.CreateOrGetSub2Import(ctx, "other", account.ID, "new-op", "new-key", []byte(`{}`)); !errors.Is(err, ErrSub2PlanIneligible) || inserted {
		t.Fatalf("legacy plan admitted new import: inserted=%v err=%v", inserted, err)
	}
	if _, err := s.QueueAccountDelivery(ctx, account.ID, "other", DefaultDeliveryOptions()); !errors.Is(err, ErrSub2PlanIneligible) {
		t.Fatalf("legacy plan admitted new delivery: %v", err)
	}
	// Existing import identity remains readable for idempotent callers.
	item, inserted, err := s.CreateOrGetSub2Import(ctx, "fixture", account.ID, "different-op", "different-key", []byte(`{}`))
	if err != nil || inserted || item.OperationID != "old-op" {
		t.Fatalf("existing import was not reused: item=%+v inserted=%v err=%v", item, inserted, err)
	}
}
