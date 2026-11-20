package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestRecoveryRetryPolicyPersistsAndClears(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "retry-policy")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	due := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	failure := RecoveryFailure{State: RecoveryUnknown, Stage: RecoveryLoggingIn, Code: "proxy_unavailable", Message: "代理暂不可用", RetryAction: "relogin", NextRetryAt: &due}
	task, err = s.RecordAccountRecoveryFailure(ctx, task.ID, failure)
	if err != nil || task.RetryCount != 1 || task.NextRetryAt == nil || !task.NextRetryAt.Equal(due) || task.RequiresAction {
		t.Fatalf("persisted failure=%+v err=%v", task, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if tasks, err := s.ListDueAccountRecoveryTasks(ctx, due.Add(-time.Millisecond), 10); err != nil || len(tasks) != 0 {
		t.Fatalf("retry before deadline=%+v err=%v", tasks, err)
	}
	if tasks, err := s.ListDueAccountRecoveryTasks(ctx, due, 10); err != nil || len(tasks) != 1 || tasks[0].ID != task.ID || tasks[0].RetryCount != 1 {
		t.Fatalf("durable due task=%+v err=%v", tasks, err)
	}
	task, err = s.RetryAccountRecoveryTask(ctx, task.ID, true)
	if err != nil || task.State != RecoveryQueued || task.NextRetryAt != nil || task.RetryCount != 1 || task.FailureStage != RecoveryLoggingIn || task.ErrorCode != "proxy_unavailable" || task.RetryAction != "relogin" {
		t.Fatalf("retry=%+v err=%v", task, err)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryFailed, Stage: RecoveryLoggingIn, Code: "invalid_password", RetryAction: "manual", ManualAction: "更新账号密码后重试"})
	if err != nil || !task.RequiresAction || task.RetryCount != 2 || task.NextRetryAt != nil {
		t.Fatalf("manual failure=%+v err=%v", task, err)
	}
	if tasks, err := s.ListDueAccountRecoveryTasks(ctx, due.Add(time.Hour), 10); err != nil || len(tasks) != 0 {
		t.Fatalf("manual task automatically selected=%+v err=%v", tasks, err)
	}
	task, err = s.RetryAccountRecoveryTask(ctx, task.ID, true)
	if err != nil || task.RequiresAction || task.ManualAction != "" || task.RetryCount != 2 {
		t.Fatalf("manual retry=%+v err=%v", task, err)
	}
	task, err = s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, "已恢复")
	if err != nil || task.RetryCount != 2 || task.FailureStage != "" || task.ErrorCode != "" || task.RetryAction != "" || task.RequiresAction || task.NextRetryAt != nil {
		t.Fatalf("completion retained actionable failure=%+v err=%v", task, err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, task.ID, failure); !errors.Is(err, ErrAccountRecoveryNotResumable) {
		t.Fatalf("late failure overwrote completed task: %v", err)
	}
}

func TestRecoveryFailureEventsPersistRedactedProbeEvidenceAfterCompletion(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "failure-event-evidence")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := &RecoveryDiagnostic{CurrentGroupIDs: []int64{12, 13}, TargetGroupIDs: []int64{12, 44}}
	got, err := s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{
		State: RecoveryUnknown, Stage: RecoveryValidating, Code: "upstream_error", Message: "上游服务暂不可用",
		HTTPStatus: 503, Diagnostic: diagnostic, RetryAction: "resume",
	})
	if err != nil || got.HTTPStatus != 503 || got.FailureStage != RecoveryValidating || got.ErrorCode != "upstream_error" {
		t.Fatalf("probe evidence not persisted: %+v err=%v", got, err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, "已恢复"); err != nil {
		t.Fatal(err)
	}
	events, truncated, err := s.ListAccountRecoveryFailureEvents(ctx, id, 20)
	if err != nil || truncated || len(events) != 1 {
		t.Fatalf("failure event history=%+v truncated=%v err=%v", events, truncated, err)
	}
	event := events[0]
	if event.TaskID != task.ID || event.FailureStage != RecoveryValidating || event.ErrorCode != "upstream_error" || event.HTTPStatus != 503 || event.RetryCount != 1 || event.Diagnostic == nil || len(event.Diagnostic.CurrentGroupIDs) != 2 || event.Diagnostic.CurrentGroupIDs[0] != 12 || len(event.Diagnostic.TargetGroupIDs) != 2 || event.Diagnostic.TargetGroupIDs[1] != 44 {
		t.Fatalf("failure event evidence=%+v", event)
	}
}

func TestRecoveryFailureSnapshotRejectsSameMillisecondNewRound(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "retry-failure-aba")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	failure := RecoveryFailure{State: RecoveryUnknown, Stage: RecoveryLoggingIn, Code: "network_error", RetryAction: "relogin"}
	old, err := s.RecordAccountRecoveryFailure(ctx, task.ID, failure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, task.ID, true); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ClaimAccountRecoveryTask(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	current, err := s.RecordAccountRecoveryFailure(ctx, task.ID, failure)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE account_recovery_tasks SET updated_at=? WHERE id=?`, old.UpdatedAt.UnixMilli(), task.ID); err != nil {
		t.Fatal(err)
	}
	failure.ExpectedState, failure.ExpectedUpdatedAt, failure.ExpectedRetryCount = old.State, old.UpdatedAt.UnixMilli(), old.RetryCount
	failure.Code = "identity_changed"
	got, err := s.RecordAccountRecoveryFailure(ctx, task.ID, failure)
	if !errors.Is(err, ErrAccountRecoveryStale) || got.RetryCount != current.RetryCount || got.ErrorCode != current.ErrorCode {
		t.Fatalf("same-millisecond stale snapshot accepted: %+v err=%v", got, err)
	}
}

func TestRecoveryRetryCheckpointNeverAdoptsNewerLogin(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "retry-checkpoint")
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
	version, err := lease.UpdateOAuthResult(ctx, task.ID, Result{AccessToken: "checkpoint-at", RefreshToken: "checkpoint-rt"})
	lease.Release()
	if err != nil {
		t.Fatal(err)
	}
	task, err = s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryUnknown, Stage: RecoveryCredentialsApplied, Code: "token_expired", RetryAction: "relogin"})
	if err != nil || task.Resumable {
		t.Fatalf("expired checkpoint advertised reusable=%+v err=%v", task, err)
	}
	if _, err := s.ResumeAccountRecoveryTask(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryNotResumable) {
		t.Fatalf("expired checkpoint resumed: %v", err)
	}
	task, err = s.RetryAccountRecoveryTask(ctx, task.ID, true)
	if err != nil || task.SourceCredentialAttemptID != version || task.ResultCredentialAttemptID != 0 || task.RetryCount != 1 {
		t.Fatalf("checkpoint reset=%+v err=%v", task, err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "newer-at", RefreshToken: "newer-rt"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, task.ID, true); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("old retry adopted newer login: %v", err)
	}
	_, result, err := s.GetOAuthResultByID(ctx, id)
	if err != nil || result.AccessToken != "newer-at" {
		t.Fatalf("retry changed newer credentials, err=%v", err)
	}
}

func TestRecoveryDueSelectionExcludesSupersededAndManualTasks(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	due := time.Now().Add(-time.Minute)
	firstID := checkTestAccount(t, s, "superseded-policy")
	first, _, err := s.CreateOrGetAccountRecoveryTask(ctx, firstID, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, first.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin", NextRetryAt: &due}); err != nil {
		t.Fatal(err)
	}
	latest, _, err := s.CreateOrGetAccountRecoveryTask(ctx, firstID, 0, 42, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, latest.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "manual", ManualAction: "核对绑定"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, first.ID, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("superseded task retried: %v", err)
	}
	secondID := checkTestAccount(t, s, "due-policy")
	second, _, err := s.CreateOrGetAccountRecoveryTask(ctx, secondID, 0, 43, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, second.ID, RecoveryFailure{State: RecoveryUnknown, RetryAction: "relogin", NextRetryAt: &due}); err != nil {
		t.Fatal(err)
	}
	tasks, err := s.ListDueAccountRecoveryTasks(ctx, time.Now(), 100)
	if err != nil || len(tasks) != 1 || tasks[0].ID != second.ID {
		t.Fatalf("due selection=%+v err=%v", tasks, err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, latest.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "manual", NextRetryAt: &due}); err == nil {
		t.Fatal("manual action accepted an automatic retry deadline")
	}
}

func TestRecoveryRetryNewerTaskIsDestinationScoped(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	accountID := checkTestAccount(t, s, "retry-two-destinations")
	alpha, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, 101, true, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, alpha.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
		t.Fatal(err)
	}
	beta, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, 202, true, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, alpha.ID, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("active beta task failed to block alpha: %v", err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, beta.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
		t.Fatal(err)
	}
	if resumed, err := s.RetryAccountRecoveryTask(ctx, alpha.ID, true); err != nil || resumed.State != RecoveryQueued {
		t.Fatalf("later failed beta task blocked alpha: %+v, %v", resumed, err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, beta.ID, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("active alpha task failed to block beta: %v", err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, alpha.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
		t.Fatal(err)
	}
	newAlpha, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, 101, true, "alpha")
	if err != nil || newAlpha.ID == alpha.ID {
		t.Fatalf("could not create newer alpha task: %+v, %v", newAlpha, err)
	}
	if _, err := s.RecordAccountRecoveryFailure(ctx, newAlpha.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RetryAccountRecoveryTask(ctx, alpha.ID, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("newer same-destination task failed to supersede alpha: %v", err)
	}
}

func TestRecoveryRetryResolvesOnlyUnambiguousLegacyDestinations(t *testing.T) {
	for _, tc := range []struct {
		name, firstDestination, secondDestination string
		secondRemote                              int64
		wantBusy                                  bool
	}{
		{"legacy_current_other_destination", "", "beta", 202, false},
		{"legacy_current_same_destination", "", "alpha", 101, true},
		{"legacy_newer_other_destination", "alpha", "", 202, false},
		{"legacy_newer_same_destination", "alpha", "", 101, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			accountID := checkTestAccount(t, s, "legacy-retry")
			if _, err := s.db.Exec(`DELETE FROM sub2_imports WHERE account_id=?`, accountID); err != nil {
				t.Fatal(err)
			}
			for _, binding := range []struct {
				destination string
				remoteID    int64
			}{{"alpha", 101}, {"beta", 202}} {
				if _, err := s.LinkSub2Account(ctx, binding.destination, accountID, binding.remoteID, "linked-"+binding.destination); err != nil {
					t.Fatal(err)
				}
			}
			first, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, 101, true, tc.firstDestination)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordAccountRecoveryFailure(ctx, first.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
				t.Fatal(err)
			}
			second, _, err := s.CreateOrGetAccountRecoveryTask(ctx, accountID, 0, tc.secondRemote, true, tc.secondDestination)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.RecordAccountRecoveryFailure(ctx, second.ID, RecoveryFailure{State: RecoveryFailed, RetryAction: "relogin"}); err != nil {
				t.Fatal(err)
			}
			_, err = s.RetryAccountRecoveryTask(ctx, first.ID, true)
			if tc.wantBusy && !errors.Is(err, ErrAccountBusy) || !tc.wantBusy && err != nil {
				t.Fatalf("legacy destination retry err=%v wantBusy=%v", err, tc.wantBusy)
			}
		})
	}
}

func TestDeliveryRecoveryHandoffIsIdempotentAndNotRecoveryHistory(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "delivery-handoff")
	version, err := s.GetAccountCredentialVersion(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	task, created, err := s.CreateAccountDeliveryRecoveryTask(ctx, 91, id, 42, version, false)
	if err != nil || !created || task.Purpose != "delivery" || task.DeliveryID != 91 || task.SourceCredentialAttemptID != version || task.ResultCredentialAttemptID != version || task.OriginalSchedulable || task.CheckID != 0 {
		t.Fatalf("delivery task=%+v created=%v err=%v", task, created, err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		t.Fatal(err)
	}
	duplicate, created, err := s.CreateAccountDeliveryRecoveryTask(ctx, 91, id, 42, version, false)
	if err != nil || created || duplicate.ID != task.ID {
		t.Fatalf("delivery duplicate=%+v created=%v err=%v", duplicate, created, err)
	}
	if _, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, 92, id, 42, version, true); !errors.Is(err, ErrAccountBusy) {
		t.Fatalf("delivery bypassed active task: %v", err)
	}
	if _, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, 91, id, 99, version, false); err == nil {
		t.Fatal("delivery ID rebound to another Sub2 account")
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCompleted, "delivered"); err != nil {
		t.Fatal(err)
	}
	history, truncated, err := s.ListAccountRecoveryHistory(ctx, id, 100)
	if err != nil || len(history) != 0 || truncated {
		t.Fatalf("delivery appeared in recovery history=%+v err=%v", history, err)
	}
	latest, err := s.ListLatestAccountRecoveryTasks(ctx)
	if err != nil || len(latest) != 1 || latest[0].ID != task.ID {
		t.Fatalf("delivery missing from operational state=%+v err=%v", latest, err)
	}
	if err := s.UpdateOAuthResultByID(ctx, id, Result{AccessToken: "later-at", RefreshToken: "later-rt"}); err != nil {
		t.Fatal(err)
	}
	duplicate, created, err = s.CreateAccountDeliveryRecoveryTask(ctx, 91, id, 42, version, false)
	if err != nil || created || duplicate.ID != task.ID {
		t.Fatalf("credential change broke delivery idempotency=%+v created=%v err=%v", duplicate, created, err)
	}
	if _, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, 92, id, 42, version, false); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("new delivery accepted stale credentials: %v", err)
	}
}

func TestDeliveryRecoveryConcurrentHandoffCreatesOneTask(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "delivery-concurrent")
	version, err := s.GetAccountCredentialVersion(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	type outcome struct {
		task    AccountRecoveryTask
		created bool
		err     error
	}
	results := make(chan outcome, 8)
	for range 8 {
		go func() {
			task, created, err := s.CreateAccountDeliveryRecoveryTask(ctx, 97, id, 42, version, true)
			results <- outcome{task, created, err}
		}()
	}
	var taskID int64
	created := 0
	for range 8 {
		result := <-results
		if result.err != nil || (taskID != 0 && result.task.ID != taskID) {
			t.Fatalf("concurrent handoff=%+v expected task=%d", result, taskID)
		}
		taskID = result.task.ID
		if result.created {
			created++
		}
	}
	if created != 1 {
		t.Fatalf("created %d tasks for one delivery", created)
	}
}
