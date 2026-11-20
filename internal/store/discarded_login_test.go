package store

import (
	"context"
	"errors"
	"testing"
)

func TestDiscardedAccountWithImportedBindingCannotBeCheckedOrLinked(t *testing.T) {
	ctx := context.Background()
	s, _ := testStore(t)
	accountID := checkTestAccount(t, s, "discarded-check")
	batch := checkTestBatch(t, s, "before-discard", 1, accountID)
	if _, err := s.db.ExecContext(ctx, `UPDATE accounts SET status='discarded',sub2_policy='discarded' WHERE id=?`, accountID); err != nil {
		t.Fatal(err)
	}
	if work, err := s.ClaimAccountCheck(ctx); err != nil || work != nil {
		t.Fatalf("discarded account yielded check work: %v, %v", work != nil, err)
	}
	summaries, err := s.ListAccountCheckSummaries(ctx)
	if summary, found := summaries[accountID]; err != nil || !found || summary.Eligible {
		t.Fatalf("discarded account summary: %+v, %v", summary, err)
	}
	_, _, err = s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: batch.ID + "-after", AccountIDs: []int64{accountID}, Concurrency: 1, ProxyMode: "direct"})
	checkTestErrorCode(t, err, "ineligible_accounts")
	for _, destination := range []string{"test", "new-destination"} {
		if _, err := s.LinkSub2Account(ctx, destination, accountID, 42, "linked-retired"); !errors.Is(err, ErrAccountDiscarded) {
			t.Fatalf("link %q returned %v", destination, err)
		}
	}
	if _, err := s.GetSub2Import(ctx, "new-destination", accountID); !errors.Is(err, ErrSub2ImportNotFound) {
		t.Fatalf("unexpected new binding: %v", err)
	}
}

func TestDiscardedLoginRevokesAccess(t *testing.T) {
	for _, scenario := range []struct {
		code, stage   string
		wantDiscarded bool
	}{
		{"password_rejected", "password", true},
		{"credentials_rejected", "password", true},
		{"invalid_username_or_password", "password", true},
		{"email_verification_required", "email", true},
		{"invalid_credentials", "password", true},
		{"invalid_credentials", "token", false},
		{"invalid_credentials", "token_refresh", false},
		{"invalid_credentials", "proxy", false},
		{"invalid_credentials", "configuration", false},
		{"invalid_credentials", "", false},
		{"password_rejected", "proxy", false},
		{"email_verification_required", "token", false},
		{"unauthorized", "password", false},
		{"invalid_otp", "mfa", false},
	} {
		t.Run(scenario.code+"/"+scenario.stage, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			const email = "discard@example.test"
			newLifecycleAccount(t, s, email)
			finishLifecycleLogin(t, s, email, "plus")
			binding := bindLifecycleAccount(t, s, email)
			attempt, err := s.StartAttempt(ctx, email)
			if err != nil {
				t.Fatal(err)
			}
			defer attempt.Release()
			if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: scenario.code, Stage: scenario.stage, HTTPStatus: 401}); err != nil {
				t.Fatal(err)
			}
			attempt.Release()
			account, err := s.GetAccountByID(ctx, binding.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			if account.IsOfficialUnavailable() || account.IsTerminalUnavailable() != scenario.wantDiscarded {
				t.Fatalf("unexpected account state %s/%s", account.Status, account.Sub2Policy)
			}
			if !scenario.wantDiscarded {
				assertLifecycleBinding(t, s, binding.ID, "imported", binding.CredentialVersion)
				if _, err := s.GetCredentialsByID(ctx, account.ID); err != nil {
					t.Fatal(err)
				}
				return
			}
			for _, blocked := range []func() error{
				func() error { _, err := s.StartAttempt(ctx, email); return err },
				func() error { _, err := s.RevealCredential(ctx, account.ID, "password"); return err },
				func() error { _, _, err := s.GetOAuthResultByID(ctx, account.ID); return err },
				func() error {
					_, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{AccessToken: "at", RefreshToken: "rt", PlanType: "plus"}, "test")
					return err
				},
			} {
				if err := blocked(); !errors.Is(err, ErrAccountDiscarded) {
					t.Fatalf("discarded operation returned %v", err)
				}
			}
			binding, err = s.GetSub2ImportByID(ctx, binding.ID)
			if err != nil || binding.State != "cleanup_pending" {
				t.Fatalf("binding state=%s err=%v", binding.State, err)
			}
			if allowed, err := s.HasDiscardedSub2CleanupEvidence(ctx, binding); err != nil || !allowed {
				t.Fatalf("cleanup evidence=%v err=%v", allowed, err)
			}
			if err := s.UpdateSub2ImportForOperation(ctx, binding, "imported", true, 42, ""); !errors.Is(err, ErrAccountRecoveryStale) {
				t.Fatalf("late import response returned %v", err)
			}
			var cleared bool
			if err := s.db.QueryRow(`SELECT length(password_cipher)=0 AND access_token_cipher IS NULL AND refresh_token_cipher IS NULL FROM accounts WHERE id=?`, account.ID).Scan(&cleared); err != nil || !cleared {
				t.Fatalf("secrets cleared=%v err=%v", cleared, err)
			}
		})
	}
}

func TestDiscardedLoginDoesNotAuthorizeReplacementBinding(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const email = "discard-rebound@example.test"
	newLifecycleAccount(t, s, email)
	finishLifecycleLogin(t, s, email, "plus")
	binding := bindLifecycleAccount(t, s, email)
	attempt, err := s.StartAttempt(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if _, err := s.db.ExecContext(ctx, `UPDATE sub2_imports SET operation_id='replacement' WHERE id=?`, binding.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "invalid_username_or_password", Stage: "password"}); err != nil {
		t.Fatal(err)
	}
	assertLifecycleBinding(t, s, binding.ID, "imported", binding.CredentialVersion)
	latest, err := s.GetSub2ImportByID(ctx, binding.ID)
	if err != nil {
		t.Fatal(err)
	}
	if authorized, err := s.HasDiscardedSub2CleanupEvidence(ctx, latest); err != nil || authorized {
		t.Fatalf("replacement authorized=%v err=%v", authorized, err)
	}
}
