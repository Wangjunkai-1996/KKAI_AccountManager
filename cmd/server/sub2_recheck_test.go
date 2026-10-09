package main

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func forceDueRecheck(t *testing.T, f *recoveryFixture) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE account_recovery_rechecks SET next_check_at=0 WHERE task_id=? AND state='pending'`, f.task.ID); err != nil {
		t.Fatal(err)
	}
}

func currentRecheck(t *testing.T, f *recoveryFixture) store.AccountRecoveryRecheck {
	t.Helper()
	checks, err := f.store.ListAccountRecoveryRechecks(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return checks[f.account.ID]
}

func TestSub2RecheckPassesWithoutReloginOrChangingScheduling(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.process(f.task)
	if f.state(t).State != store.RecoveryCompleted {
		t.Fatal("recovery did not complete")
	}
	// Sub2 may rotate the same account's token itself between follow-ups.
	f.detailExpiry = time.Now().Add(time.Hour).Unix()
	first := currentRecheck(t, f)
	for round := 1; round <= 2; round++ {
		forceDueRecheck(t, f)
		if !f.service.processNextRecheck() {
			t.Fatal("due follow-up was not processed")
		}
		check := currentRecheck(t, f)
		if round == 1 && (check.State != "pending" || check.Round != 2 || check.NextCheckAt.Sub(first.CompletedAt) != 30*time.Minute) {
			t.Fatalf("first probe did not schedule second: %+v", check)
		}
		if round == 2 && (check.State != "passed" || check.NextCheckAt != nil) {
			t.Fatalf("second probe did not finish: %+v", check)
		}
	}
	if f.loginCalls != 1 || f.applyCalls != 1 || f.testCalls != 3 || !f.schedule || f.state(t).State != store.RecoveryCompleted {
		t.Fatalf("follow-ups mutated successful recovery: login=%d apply=%d test=%d schedule=%v", f.loginCalls, f.applyCalls, f.testCalls, f.schedule)
	}
}

func TestSub2RecheckInvalidActualCredentialQueuesOneRecovery(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.process(f.task)
	f.probeBody = "data: {\"type\":\"error\",\"status\":401,\"code\":\"token_revoked\"}\n\n"
	forceDueRecheck(t, f)
	if !f.service.processNextRecheck() {
		t.Fatal("follow-up not processed")
	}
	parent := f.state(t)
	child, err := f.store.GetLatestAccountRecoveryTask(context.Background(), f.account.ID)
	if err != nil || child.ID == parent.ID || child.State != store.RecoveryQueued || child.RetryAction != "relogin" || child.SourceCredentialAttemptID != parent.ResultCredentialAttemptID {
		t.Fatalf("missing recovery for current Sub2 credential: %+v, %v", child, err)
	}
	again, err := f.store.QueueAccountRecoveryFromRecheck(context.Background(), parent.ID)
	if err != nil || again.ID != child.ID {
		t.Fatalf("duplicate delayed recovery: %+v, %v", again, err)
	}
	if parent.State != store.RecoveryCompleted || f.loginCalls != 1 || !f.schedule {
		t.Fatalf("probe rewrote parent or scheduling: %+v", parent)
	}
	f.probeBody = ""
	f.service.process(child)
	child, _ = f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if child.State != store.RecoveryCompleted || f.loginCalls != 2 || f.applyCalls != 2 || !f.schedule {
		t.Fatalf("delayed 401 failed to relogin active Sub2 account: %+v login=%d apply=%d schedule=%v", child, f.loginCalls, f.applyCalls, f.schedule)
	}
}

func TestSub2RecheckTransientFailureBacksOffAndManualFailureSurfaces(t *testing.T) {
	for _, tc := range []struct {
		name   string
		status int
		state  string
	}{
		{"rate_limit", http.StatusTooManyRequests, "pending"},
		{"upstream", http.StatusServiceUnavailable, "pending"},
		{"management_401", http.StatusUnauthorized, "requires_action"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			f.service.autoRecovery.Store(true)
			f.service.process(f.task)
			f.probeStatus = tc.status
			forceDueRecheck(t, f)
			f.service.processNextRecheck()
			check := currentRecheck(t, f)
			if check.State != tc.state || check.LastError == "" || (tc.state == "requires_action") != check.RequiresAction || f.loginCalls != 1 || !f.schedule {
				t.Fatalf("misclassified follow-up: %+v", check)
			}
			if tc.state == "pending" && (check.NextCheckAt == nil || time.Until(*check.NextCheckAt) < 2*time.Minute || check.Round != 1) {
				t.Fatalf("transient failure was not backed off: %+v", check)
			}
			if f.state(t).State != store.RecoveryCompleted {
				t.Fatal("failed follow-up rewrote completed recovery")
			}
			if tc.state == "requires_action" {
				f.probeStatus = 0
				resumed, err := f.store.RetryAccountRecoveryRecheck(context.Background(), f.account.ID)
				if err != nil || !resumed || !f.service.processNextRecheck() || currentRecheck(t, f).Round != 2 {
					t.Fatalf("corrected configuration did not resume follow-up: resumed=%v err=%v", resumed, err)
				}
			}
		})
	}
}

func TestSub2RecheckRejectsChangedOwnershipAndManualPause(t *testing.T) {
	for _, change := range []string{"pause", "binding", "marker", "version", "newer_task"} {
		t.Run(change, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			f.service.autoRecovery.Store(true)
			f.service.process(f.task)
			switch change {
			case "pause":
				f.schedule = false
			case "binding":
				binding, _ := f.store.GetSub2Import(context.Background(), "fixture", f.account.ID)
				if err := f.store.UpdateSub2Import(context.Background(), binding.ID, "imported", true, 902, ""); err != nil {
					t.Fatal(err)
				}
			case "marker":
				f.recoveryMarker = map[string]any{"task_id": 999, "credential_attempt_id": f.state(t).ResultCredentialAttemptID}
			case "version":
				if err := f.store.UpdateOAuthResultByID(context.Background(), f.account.ID, store.Result{AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
					t.Fatal(err)
				}
			case "newer_task":
				if _, _, err := f.store.CreateOrGetAccountRecoveryTask(context.Background(), f.account.ID, 0, 901, true); err != nil {
					t.Fatal(err)
				}
			}
			forceDueRecheck(t, f)
			f.service.processNextRecheck()
			if f.testCalls != 1 || f.loginCalls != 1 || f.applyCalls != 1 {
				t.Fatalf("stale follow-up had side effects: test=%d login=%d apply=%d", f.testCalls, f.loginCalls, f.applyCalls)
			}
			if change == "pause" && (f.schedule || currentRecheck(t, f).State != "canceled") {
				t.Fatal("manual pause not preserved")
			}
			if change == "pause" {
				if resumed, err := f.store.RetryAccountRecoveryRecheck(context.Background(), f.account.ID); err != nil || resumed {
					t.Fatalf("editing credentials resumed an operator pause: %v, %v", resumed, err)
				}
			}
		})
	}
}

func TestSub2RecheckPreservesPauseDuringProbe(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.process(f.task)
	f.probePausesAccount = true
	f.probeBody = "data: {\"type\":\"error\",\"status\":401,\"code\":\"token_revoked\"}\n\n"
	forceDueRecheck(t, f)
	f.service.processNextRecheck()
	check := currentRecheck(t, f)
	if check.State != "canceled" || f.schedule || f.loginCalls != 1 {
		t.Fatalf("late pause overridden: %+v, schedule=%v login=%d", check, f.schedule, f.loginCalls)
	}
	latest, _ := f.store.GetLatestAccountRecoveryTask(context.Background(), f.account.ID)
	if latest.ID != f.task.ID {
		t.Fatal("paused account queued another recovery")
	}
}

func TestSub2RecheckRecoveryRetainsConfirmedPauseOnRetry(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.process(f.task)
	f.probeBody = "data: {\"type\":\"error\",\"status\":401,\"code\":\"token_revoked\"}\n\n"
	forceDueRecheck(t, f)
	f.service.processNextRecheck()
	child, _ := f.store.GetLatestAccountRecoveryTask(context.Background(), f.account.ID)
	loginOK := f.service.loginWithProxies
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "network_error"}
	}
	f.service.process(child)
	failed, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if failed.RetryAction != "relogin" || failed.FailureStage != store.RecoveryLoggingIn || f.schedule {
		t.Fatalf("missing owned pause evidence: %+v schedule=%v", failed, f.schedule)
	}
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET next_retry_at=0 WHERE id=?`, child.ID); err != nil {
		t.Fatal(err)
	}
	f.service.loginWithProxies = loginOK
	f.service.retryDueRecoveries()
	queued, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if queued.State != store.RecoveryQueued {
		t.Fatalf("confirmed pause blocked retry: %+v", queued)
	}
	f.probeBody = ""
	f.service.process(queued)
	finished, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if finished.State != store.RecoveryCompleted || !f.schedule {
		t.Fatalf("retry did not finish: %+v schedule=%v", finished, f.schedule)
	}
}

func TestSub2RecheckRecoveryReusesCheckpointAfterApplyFailure(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.process(f.task)
	f.probeBody = "data: {\"type\":\"error\",\"status\":401,\"code\":\"token_revoked\"}\n\n"
	forceDueRecheck(t, f)
	f.service.processNextRecheck()
	child, _ := f.store.GetLatestAccountRecoveryTask(context.Background(), f.account.ID)
	f.applyFailsOnce = true
	f.probeBody = ""
	f.service.process(child)
	failed, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if failed.RetryAction != "resume" || failed.ResultCredentialAttemptID == 0 || failed.FailureStage != store.RecoveryApplyingCredentials {
		t.Fatalf("apply failure lost checkpoint: %+v", failed)
	}
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET next_retry_at=0 WHERE id=?`, child.ID); err != nil {
		t.Fatal(err)
	}
	f.service.retryDueRecoveries()
	queued, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if queued.State != store.RecoveryQueued {
		t.Fatalf("recheck parent marker incorrectly rejected saved checkpoint: %+v", queued)
	}
	f.service.process(queued)
	finished, _ := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if finished.State != store.RecoveryCompleted || f.loginCalls != 2 || !f.schedule {
		t.Fatalf("checkpoint recovery did not finish: %+v login=%d schedule=%v", finished, f.loginCalls, f.schedule)
	}
}
