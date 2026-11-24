package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func assertDeferredQueueClaim(t *testing.T, s *Store, claim func(context.Context) (bool, error), unchanged func()) {
	t.Helper()
	conn, err := s.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := claim(ctx); !errors.Is(err, ErrQueueDeferred) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("connection contention must defer without claiming: %v", err)
	}
	conn.Close()
	unchanged()
	if claimed, err := claim(context.Background()); err != nil || !claimed {
		t.Fatalf("queue did not recover after connection returned: claimed=%v err=%v", claimed, err)
	}
}

func TestQueueClaimsRecoverAfterConnectionContention(t *testing.T) {
	ctx := context.Background()
	t.Run("checks", func(t *testing.T) {
		s, _ := testStore(t)
		id := checkTestAccount(t, s, "deferred-check")
		batch := checkTestBatch(t, s, "deferred", 1, id)
		assertDeferredQueueClaim(t, s, func(ctx context.Context) (bool, error) {
			work, err := s.ClaimAccountCheck(ctx)
			return work != nil, err
		}, func() {
			_, checks, err := s.GetAccountCheckBatch(ctx, batch.ID)
			if err != nil || len(checks) != 1 || checks[0].State != "queued" {
				t.Fatalf("deferred check changed state: %v %v", checks, err)
			}
		})
	})
	t.Run("recovery", func(t *testing.T) {
		s, _ := testStore(t)
		id := checkTestAccount(t, s, "deferred-recovery")
		task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 1, 901, true)
		if err != nil {
			t.Fatal(err)
		}
		assertDeferredQueueClaim(t, s, func(ctx context.Context) (bool, error) {
			claimed, err := s.ClaimAccountRecoveryTask(ctx, task.ID)
			return claimed.State == RecoveryValidating, err
		}, func() {
			current, err := s.GetAccountRecoveryTaskByID(ctx, task.ID)
			if err != nil || current.State != RecoveryQueued {
				t.Fatalf("deferred recovery changed state: %s %v", current.State, err)
			}
		})
	})
	t.Run("credential repair", func(t *testing.T) {
		s, _ := testStore(t)
		id := checkTestAccount(t, s, "deferred-repair")
		password := "new-password"
		if _, err := s.PatchAccountCredentials(ctx, id, CredentialPatch{Password: &password}, true); err != nil {
			t.Fatal(err)
		}
		assertDeferredQueueClaim(t, s, func(ctx context.Context) (bool, error) {
			repair, err := s.ClaimCredentialRepair(ctx, time.Now())
			return repair != nil, err
		}, func() {
			repair, err := s.GetCredentialRepair(ctx, id)
			if err != nil || repair.State != "queued" {
				t.Fatalf("deferred repair changed state: %s %v", repair.State, err)
			}
		})
	})
	t.Run("recheck", func(t *testing.T) {
		s, _ := testStore(t)
		id := checkTestAccount(t, s, "deferred-recheck")
		task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 1, 901, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryLoggingIn, "test pause confirmed"); err != nil {
			t.Fatal(err)
		}
		lease, err := s.AcquireAccountRecovery(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		_, err = lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "new-at", RefreshToken: "new-rt", PlanType: "plus"})
		lease.Release()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, ""); err != nil {
			t.Fatal(err)
		}
		assertDeferredQueueClaim(t, s, func(ctx context.Context) (bool, error) {
			recheck, err := s.ClaimAccountRecoveryRecheck(ctx, time.Now().Add(time.Minute))
			return recheck != nil, err
		}, func() {
			rechecks, err := s.ListAccountRecoveryRechecks(ctx)
			if err != nil || rechecks[id].State != "pending" {
				t.Fatalf("deferred recheck changed state: %s %v", rechecks[id].State, err)
			}
		})
	})
}

func TestQueueClaimWriteFailureIsNotDeferred(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "failed-claim")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 1, 901, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_claim BEFORE UPDATE OF state ON account_recovery_tasks BEGIN SELECT RAISE(ABORT,'claim failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err == nil || errors.Is(err, ErrQueueDeferred) {
		t.Fatalf("a failed write must not permit safe queue retry: %v", err)
	}
	current, err := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil || current.State != RecoveryQueued {
		t.Fatalf("failed claim partially committed: %s %v", current.State, err)
	}
}
