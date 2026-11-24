package store

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
)

func TestLifecycleBeginAttemptReleaseIsIdempotent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	credentials := Credentials{Email: "release@example.test", Password: "test-password"}
	_, first, err := s.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()
	if err := s.FinishAttempt(ctx, first.ID, true, &Result{PlanType: "plus"}, nil); err != nil {
		t.Fatal(err)
	}
	first.Release()
	_, second, err := s.BeginAttempt(ctx, credentials)
	if err != nil {
		t.Fatal(err)
	}
	defer second.Release()
	first.Release()
	if _, unexpected, err := s.BeginAttempt(ctx, credentials); !errors.Is(err, ErrAccountBusy) {
		if unexpected.Release != nil {
			unexpected.Release()
		}
		t.Fatalf("repeat release let a third login take the lock: %v", err)
	}
}

func TestLifecycleFreeFrozenBinding(t *testing.T) {
	for _, changed := range []string{"", "operation", "version", "unknown_before_login"} {
		t.Run("binding_"+changed, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			const email = "frozen@example.test"
			newLifecycleAccount(t, s, email)
			finishLifecycleLogin(t, s, email, "team")
			binding := bindLifecycleAccount(t, s, email)
			if changed == "unknown_before_login" {
				if _, err := s.db.Exec(`UPDATE sub2_imports SET credential_version=0 WHERE id=?`, binding.ID); err != nil {
					t.Fatal(err)
				}
				binding.CredentialVersion = 0
			}
			attempt, err := s.StartAttempt(ctx, email)
			if err != nil {
				t.Fatal(err)
			}
			defer attempt.Release()
			switch changed {
			case "operation":
				if _, err := s.db.Exec(`UPDATE sub2_imports SET operation_id='replacement' WHERE id=?`, binding.ID); err != nil {
					t.Fatal(err)
				}
			case "version":
				if _, err := s.db.Exec(`UPDATE sub2_imports SET credential_version=0 WHERE id=?`, binding.ID); err != nil {
					t.Fatal(err)
				}
				binding.CredentialVersion = 0
			}
			if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{PlanType: "free"}, nil); err != nil {
				t.Fatal(err)
			}
			if changed == "" {
				assertLifecycleBinding(t, s, binding.ID, "cleanup_pending", attempt.ID)
			} else {
				assertLifecycleBinding(t, s, binding.ID, "imported", binding.CredentialVersion)
			}
		})
	}
}

func TestLifecycleLegacyEvidenceAndTerminalCredentials(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	const email = "legacy-paid@example.test"
	const terminalEmail = "legacy-terminal@example.test"
	newLifecycleAccount(t, s, email)
	finishLifecycleLogin(t, s, email, "plus")
	binding := bindLifecycleAccount(t, s, email)
	newLifecycleAccount(t, s, terminalEmail)
	finishLifecycleLogin(t, s, terminalEmail, "plus")
	if _, err := s.db.Exec(`UPDATE login_attempts SET plan_type=NULL,observation_source=NULL`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE accounts SET status='deactivated' WHERE email=?`, terminalEmail); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	finishLifecycleLogin(t, s, email, "free")
	version, err := s.GetAccountCredentialVersion(ctx, binding.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	assertLifecycleBinding(t, s, binding.ID, "cleanup_pending", version)
	var retained bool
	if err := s.db.QueryRow(`SELECT status='deactivated' AND sub2_policy<>'official_unavailable'
		AND length(password_cipher)>0 AND length(access_token_cipher)>0 AND length(refresh_token_cipher)>0
		FROM accounts WHERE email=?`, terminalEmail).Scan(&retained); err != nil || !retained {
		t.Fatalf("legacy status alone changed credentials, err=%v", err)
	}
}

func TestLifecycleTerminalCredentialConvergence(t *testing.T) {
	for _, scenario := range []struct {
		name, code, source, eventStatus               string
		unlinked, newerSuccess, explicitPolicy, clear bool
	}{
		{name: "official attempt", code: "account_deactivated", source: "official", clear: true},
		{name: "generic official deleted", code: "deleted", source: "official", eventStatus: "deleted", clear: true},
		{name: "generic official deactivated", code: "deactivated", source: "official", eventStatus: "deactivated", clear: true},
		{name: "payment required", code: "payment_required", source: "official", clear: true},
		{name: "deactivated workspace", code: "deactivated_workspace", source: "official", clear: true},
		{name: "explicit policy", explicitPolicy: true, clear: true},
		{name: "generic classification", code: "account_unavailable", source: "official"},
		{name: "unlinked event", code: "account_deactivated", source: "official", unlinked: true},
		{name: "manual review", code: "account_deactivated", source: "manual"},
		{name: "later successful login", code: "account_deactivated", source: "official", newerSuccess: true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			const email = "terminal@example.test"
			newLifecycleAccount(t, s, email)
			finishLifecycleLogin(t, s, email, "plus")
			attempt, err := s.StartAttempt(ctx, email)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "unauthorized"}); err != nil {
				t.Fatal(err)
			}
			attempt.Release()
			if scenario.newerSuccess {
				finishLifecycleLogin(t, s, email, "plus")
			}
			if _, err := s.db.Exec(`UPDATE login_attempts SET error_code=? WHERE id=?`, scenario.code, attempt.ID); err != nil {
				t.Fatal(err)
			}
			if scenario.source != "" {
				eventAttempt := attempt.ID
				if scenario.unlinked {
					eventAttempt = 0
				}
				eventStatus := scenario.eventStatus
				if eventStatus == "" {
					eventStatus = "deactivated"
				}
				if _, err := s.db.Exec(`INSERT INTO account_lifecycle_events(account_id,email,source,status,code,dedupe_key,observed_at)
					SELECT account_id,?,?,?,?,?,finished_at FROM login_attempts WHERE id=?`,
					email, scenario.source, eventStatus, scenario.code, fmt.Sprintf("login_attempt:%d", eventAttempt), attempt.ID); err != nil {
					t.Fatal(err)
				}
			}
			policy := Sub2PolicyUnknown
			if scenario.explicitPolicy {
				policy = Sub2PolicyOfficialUnavailable
			}
			if _, err := s.db.Exec(`UPDATE accounts SET status='deactivated',sub2_policy=? WHERE email=?`, policy, email); err != nil {
				t.Fatal(err)
			}
			if err := s.convergeTerminalCredentials(ctx); err != nil {
				t.Fatal(err)
			}
			var cleared bool
			if err := s.db.QueryRow(`SELECT length(password_cipher)=0 AND access_token_cipher IS NULL AND refresh_token_cipher IS NULL
				FROM accounts WHERE email=?`, email).Scan(&cleared); err != nil || cleared != scenario.clear {
				t.Fatalf("cleared=%v, want %v, err=%v", cleared, scenario.clear, err)
			}
			if _, err := s.db.Exec(`UPDATE accounts SET updated_at=1 WHERE email=?`, email); err != nil {
				t.Fatal(err)
			}
			if err := s.convergeTerminalCredentials(ctx); err != nil {
				t.Fatal(err)
			}
			var updated int64
			if err := s.db.QueryRow(`SELECT updated_at FROM accounts WHERE email=?`, email).Scan(&updated); err != nil || updated != 1 {
				t.Fatalf("second convergence rewrote account, updated_at=%d err=%v", updated, err)
			}
		})
	}
}

func finishLifecycleLogin(t *testing.T, s *Store, email, plan string) {
	t.Helper()
	attempt, err := s.StartAttempt(context.Background(), email)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err := s.FinishAttempt(context.Background(), attempt.ID, true, &Result{
		AccessToken:  "at-" + plan,
		RefreshToken: "rt-" + plan,
		PlanType:     plan,
	}, nil); err != nil {
		t.Fatal(err)
	}
}

func newLifecycleAccount(t *testing.T, s *Store, email string) {
	t.Helper()
	if _, err := s.UpsertCredentials(context.Background(), Credentials{Email: email, Password: "pw"}); err != nil {
		t.Fatal(err)
	}
}

func TestLifecycleFreeRules(t *testing.T) {
	t.Run("plus empty free", func(t *testing.T) {
		s, _ := testStore(t)
		newLifecycleAccount(t, s, "plus-empty@example.test")
		finishLifecycleLogin(t, s, "plus-empty@example.test", "plus")
		finishLifecycleLogin(t, s, "plus-empty@example.test", "")
		finishLifecycleLogin(t, s, "plus-empty@example.test", "free")
		account, err := s.GetAccount(context.Background(), "plus-empty@example.test")
		if err != nil || !account.IsFreeLocal() {
			t.Fatalf("account=%+v err=%v", account, err)
		}
	})

	t.Run("plus refresh keeps eligibility and empty login keeps only historical plan", func(t *testing.T) {
		s, _ := testStore(t)
		ctx := context.Background()
		const email = "plus-empty-observation@example.test"
		newLifecycleAccount(t, s, email)
		finishLifecycleLogin(t, s, email, "plus")
		account, err := s.GetAccount(ctx, email)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.UpdateOAuthResultByID(ctx, account.ID, Result{AccessToken: "new-at", RefreshToken: "new-rt"}); err != nil {
			t.Fatal(err)
		}
		account, err = s.GetAccount(ctx, email)
		if err != nil || !account.AllowsSub2() || account.PlanType != "plus" {
			t.Fatalf("account=%+v err=%v", account, err)
		}
		finishLifecycleLogin(t, s, email, "")
		account, err = s.GetAccount(ctx, email)
		if err != nil || account.Sub2Policy != Sub2PolicyUnknown || account.PlanType != "plus" {
			t.Fatalf("account=%+v err=%v", account, err)
		}
		var evidence int
		if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM login_attempts WHERE account_id=? AND plan_type='plus'`, account.ID).Scan(&evidence); err != nil || evidence != 1 {
			t.Fatalf("paid observations=%d err=%v", evidence, err)
		}
	})

	t.Run("plus pro free stays local without cleanup", func(t *testing.T) {
		s, _ := testStore(t)
		ctx := context.Background()
		const email = "plus-pro@example.test"
		newLifecycleAccount(t, s, email)
		finishLifecycleLogin(t, s, email, "plus")
		binding := bindLifecycleAccount(t, s, email)
		finishLifecycleLogin(t, s, email, "pro")
		finishLifecycleLogin(t, s, email, "free")
		account, err := s.GetAccount(ctx, email)
		if err != nil || !account.IsFreeLocal() {
			t.Fatalf("account=%+v err=%v", account, err)
		}
		assertLifecycleBinding(t, s, binding.ID, "imported", binding.CredentialVersion)
	})

	t.Run("free empty stays local", func(t *testing.T) {
		s, _ := testStore(t)
		newLifecycleAccount(t, s, "free-empty@example.test")
		finishLifecycleLogin(t, s, "free-empty@example.test", "free")
		finishLifecycleLogin(t, s, "free-empty@example.test", "")
		account, err := s.GetAccount(context.Background(), "free-empty@example.test")
		if err != nil || !account.IsFreeLocal() {
			t.Fatalf("account=%+v err=%v", account, err)
		}
	})
}

func bindLifecycleAccount(t *testing.T, s *Store, email string) Sub2Import {
	t.Helper()
	ctx := context.Background()
	account, err := s.GetAccount(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	importTask, _, err := s.CreateOrGetSub2Import(ctx, "test", account.ID, "op-1", "cleanup-version", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSub2Import(ctx, importTask.ID, "imported", true, 42, ""); err != nil {
		t.Fatal(err)
	}
	return importTask
}

func assertLifecycleBinding(t *testing.T, s *Store, id int64, wantState string, wantVersion int64) {
	t.Helper()
	var state string
	var version int64
	if err := s.db.QueryRowContext(context.Background(), "SELECT state,credential_version FROM sub2_imports WHERE id=?", id).Scan(&state, &version); err != nil {
		t.Fatal(err)
	}
	if state != wantState || version != wantVersion {
		t.Fatalf("binding = %s/%d, want %s/%d", state, version, wantState, wantVersion)
	}
}

func TestLifecycleFreeCleanupVersionDoesNotDrift(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	const email = "cleanup-version@example.test"
	newLifecycleAccount(t, s, email)
	finishLifecycleLogin(t, s, email, "plus")
	binding := bindLifecycleAccount(t, s, email)
	if err := s.UpdateOAuthResultByID(ctx, binding.AccountID, Result{AccessToken: "refreshed-at", RefreshToken: "refreshed-rt"}); err != nil {
		t.Fatal(err)
	}
	finishLifecycleLogin(t, s, email, "free")
	version, err := s.GetAccountCredentialVersion(ctx, binding.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	assertLifecycleBinding(t, s, binding.ID, "cleanup_pending", version)
	attempt, err := s.StartAttempt(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	delivery, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{AccessToken: "at", RefreshToken: "rt"}, "test")
	if err != nil || delivery.State != "canceled" || delivery.ID != 0 {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	assertLifecycleBinding(t, s, binding.ID, "cleanup_pending", version)
}
