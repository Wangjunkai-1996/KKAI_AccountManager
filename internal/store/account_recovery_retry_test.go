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
