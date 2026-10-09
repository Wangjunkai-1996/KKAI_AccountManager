package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAccountRecoveryRecheckPersistenceAndSchedule(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id := checkTestAccount(t, s, "recheck")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 1, 901, true)
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
	_, err = lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "new-at", RefreshToken: "new-rt"})
	lease.Release()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_recheck BEFORE INSERT ON account_recovery_rechecks BEGIN SELECT RAISE(ABORT,'failed recheck'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, ""); err == nil {
		t.Fatal("completion ignored recheck persistence failure")
	}
	failed, _ := s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if failed.State == RecoveryCompleted {
		t.Fatal("completion committed without durable follow-up")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_recheck`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, ""); err != nil {
		t.Fatal(err)
	}
	all, err := s.ListAccountRecoveryRechecks(ctx)
	if err != nil {
		t.Fatal(err)
	}
	first := all[id]
	if first.State != "pending" || first.Round != 1 || first.NextCheckAt == nil || first.NextCheckAt.Sub(first.CompletedAt) != 30*time.Second {
		t.Fatalf("first follow-up: %+v", first)
	}
	if early, err := s.ClaimAccountRecoveryRecheck(ctx, first.NextCheckAt.Add(-time.Millisecond)); err != nil || early != nil {
		t.Fatalf("follow-up claimed before deadline: %+v, %v", early, err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, "again"); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimAccountRecoveryRecheck(ctx, *first.NextCheckAt)
	if err != nil || claimed == nil || claimed.TaskID != task.ID {
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
	restarted, err := s.ClaimAccountRecoveryRecheck(ctx, time.Now())
	if err != nil || restarted == nil || restarted.TaskID != task.ID || !restarted.CompletedAt.Equal(first.CompletedAt) {
		t.Fatalf("restart lost follow-up: %+v, %v", restarted, err)
	}
	if err := s.FinishAccountRecoveryRecheck(ctx, task.ID, "passed", "", nil); err != nil {
		t.Fatal(err)
	}
	all, err = s.ListAccountRecoveryRechecks(ctx)
	if err != nil || all[id].Round != 2 || all[id].State != "pending" || all[id].NextCheckAt.Sub(first.CompletedAt) != 30*time.Minute {
		t.Fatalf("second follow-up: %+v, %v", all[id], err)
	}
	if _, err := s.ClaimAccountRecoveryRecheck(ctx, *all[id].NextCheckAt); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAccountRecoveryRecheck(ctx, task.ID, "passed", "", nil); err != nil {
		t.Fatal(err)
	}
	account, err := s.GetAccountByID(ctx, id)
	if err != nil || account.RecoveryCount != 1 || account.RecoveryAttemptCount != 1 {
		t.Fatalf("probes counted as recovery: %+v, %v", account, err)
	}
	all, _ = s.ListAccountRecoveryRechecks(ctx)
	if all[id].State != "passed" || all[id].NextCheckAt != nil {
		t.Fatalf("finished follow-up still queued: %+v", all[id])
	}
}

func TestAccountRecoveryRecheckShortensOnlyUntouchedLegacyFirstPlans(t *testing.T) {
	dir := t.TempDir()
	dbPath, keyPath := filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key")
	s, err := Open(dbPath, keyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	ctx := context.Background()
	completed := time.Now().UnixMilli()
	cases := []struct {
		name, state, lastError string
		round, retries         int
		delay, want            time.Duration
		superseded             bool
	}{
		{"untouched", "pending", "", 1, 0, 5 * time.Minute, 30 * time.Second, false},
		{"retry", "pending", "", 1, 1, 5 * time.Minute, 5 * time.Minute, false},
		{"waiting", "pending", "auto recovery disabled", 1, 0, 5 * time.Minute, 5 * time.Minute, false},
		{"second", "pending", "", 2, 0, 30 * time.Minute, 30 * time.Minute, false},
		{"manual", "requires_action", "", 1, 0, 5 * time.Minute, 5 * time.Minute, false},
		{"rescheduled", "pending", "", 1, 0, 10 * time.Minute, 10 * time.Minute, false},
		{"superseded", "pending", "", 1, 0, 5 * time.Minute, 5 * time.Minute, true},
	}
	ids := make([]int64, len(cases))
	for i, tc := range cases {
		accountID := checkTestAccount(t, s, tc.name)
		task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 1, int64(901+i), true)
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = task.ID
		if _, err := s.db.Exec(`UPDATE account_recovery_tasks SET state='completed' WHERE id=?`, task.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO account_recovery_rechecks(task_id,state,round,retry_count,last_error,next_check_at,completed_at,updated_at) VALUES (?,?,?,?,?,?,?,?)`, task.ID, tc.state, tc.round, tc.retries, tc.lastError, completed+tc.delay.Milliseconds(), completed, completed); err != nil {
			t.Fatal(err)
		}
		if tc.superseded {
			if _, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 1, int64(901+i), true); err != nil {
				t.Fatal(err)
			}
		}
	}
	for reopen := 0; reopen < 2; reopen++ {
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		s, err = Open(dbPath, keyPath)
		if err != nil {
			t.Fatal(err)
		}
		for i, tc := range cases {
			var due int64
			if err := s.db.QueryRow(`SELECT next_check_at FROM account_recovery_rechecks WHERE task_id=?`, ids[i]).Scan(&due); err != nil {
				t.Fatal(err)
			}
			if want := completed + tc.want.Milliseconds(); due != want {
				t.Errorf("%s after reopen %d: next=%d want=%d", tc.name, reopen, due, want)
			}
		}
	}
}
