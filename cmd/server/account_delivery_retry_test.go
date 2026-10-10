package main

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestAccountDeliveryBusyWaitsForNextTickWithoutFailure(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	ctx := context.Background()
	lease, err := f.store.AcquireAccountRecovery(ctx, delivery.AccountID)
	if err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	started := time.Now()
	service.runOnce()
	blocked, err := f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || blocked.State != "queued" || blocked.RetryCount != 0 || blocked.NextRetryAt == nil || blocked.LastError != "" {
		t.Fatalf("lock contention became a delivery failure: %+v err=%v", blocked, err)
	}
	if blocked.NextRetryAt.Before(started.Add(2*time.Second).Truncate(time.Millisecond)) || blocked.NextRetryAt.After(time.Now().Add(2*time.Second)) {
		t.Fatalf("lock contention did not yield for one tick: %+v", blocked)
	}
	lease.Release()
	if _, err := f.db.Exec(`UPDATE account_deliveries SET next_retry_at=? WHERE id=?`, time.Now().Add(-time.Second).UnixMilli(), delivery.ID); err != nil {
		t.Fatal(err)
	}
	service.runOnce()
	after, err := f.store.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || after.State != "verifying" || after.RecoveryTaskID == 0 || after.RetryCount != 0 {
		t.Fatalf("delivery did not proceed on the next tick: %+v err=%v", after, err)
	}
}

func TestAccountDeliveryBusyBatchDoesNotStarveFollowingTask(t *testing.T) {
	f, service, ready := newDeliveryFixture(t)
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		account, attempt, err := f.store.BeginAttempt(ctx, store.Credentials{Email: fmt.Sprintf("busy-%d@example.test", i), Password: "fixture"})
		if err != nil {
			t.Fatal(err)
		}
		busy, err := f.store.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &store.Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, "fixture")
		attempt.Release()
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.db.Exec(`UPDATE account_deliveries SET created_at=1 WHERE id=?`, busy.ID); err != nil {
			t.Fatal(err)
		}
		lease, err := f.store.AcquireAccountRecovery(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		defer lease.Release()
	}
	if _, err := f.db.Exec(`UPDATE account_deliveries SET created_at=2 WHERE id=?`, ready.ID); err != nil {
		t.Fatal(err)
	}
	service.runOnce()
	service.runOnce()
	after, err := f.store.GetAccountDelivery(ctx, ready.ID)
	if err != nil || after.State != "verifying" || after.RecoveryTaskID == 0 {
		t.Fatalf("five busy deliveries starved the next account: %+v err=%v", after, err)
	}
}

func TestAccountDeliveryNonBusyLeaseErrorIsRecorded(t *testing.T) {
	f, service, delivery := newDeliveryFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	service.process(ctx, delivery)
	failed, err := f.store.GetAccountDelivery(context.Background(), delivery.ID)
	if err != nil || failed.State != "retry_wait" || failed.RetryCount != 1 || failed.NextRetryAt == nil || !strings.Contains(failed.LastError, "任务被中断") {
		t.Fatalf("non-busy lease failure was lost or mislabeled: %+v err=%v", failed, err)
	}
}

func TestDeliveryProbeRetryDelayIsScopedAndBounded(t *testing.T) {
	for previous, want := range []time.Duration{30 * time.Second, time.Minute, 5 * time.Minute, 10 * time.Minute, 20 * time.Minute, 40 * time.Minute, time.Hour, time.Hour} {
		if got := recoveryTaskRetryDelay(store.AccountRecoveryTask{Purpose: "delivery", RetryCount: previous}, &recoveryOperationError{Code: "probe_failed"}); got != want {
			t.Fatalf("delivery probe failure %d: delay=%v want=%v", previous, got, want)
		}
	}
	for _, tt := range []struct {
		purpose, code string
		retryAfter    int
	}{
		{"recovery", "probe_failed", 0},
		{"delivery", "protocol_error", 0},
		{"delivery", "rate_limited", 0},
		{"delivery", "probe_failed", 10},
		{"delivery", "probe_failed", 3600},
	} {
		failure := &recoveryOperationError{Code: tt.code, RetryAfterSeconds: tt.retryAfter}
		for previous := 0; previous < 3; previous++ {
			got := recoveryTaskRetryDelay(store.AccountRecoveryTask{Purpose: tt.purpose, RetryCount: previous}, failure)
			if want := recoveryRetryDelay(failure, previous); got != want {
				t.Fatalf("changed existing policy for %+v failure %d: got=%v want=%v", tt, previous, got, want)
			}
		}
	}
}

func TestDeliveryProbeFailurePersistsShortResumeWithoutRelogin(t *testing.T) {
	f, deliveryService, delivery := newDeliveryFixture(t)
	deliveryService.runOnce()
	delivery, err := f.store.GetAccountDelivery(context.Background(), delivery.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := f.store.ClaimAccountRecoveryTask(context.Background(), delivery.RecoveryTaskID)
	if err != nil {
		t.Fatal(err)
	}
	f.probeOK = false
	started := time.Now()
	f.service.process(task)
	failed, err := f.store.GetAccountRecoveryTaskByID(context.Background(), task.ID)
	if err != nil || failed.ErrorCode != "probe_failed" || failed.RetryAction != "resume" || failed.RequiresAction || failed.NextRetryAt == nil || f.loginCalls != 0 || f.schedule {
		t.Fatalf("fresh delivery probe did not retain its checkpoint: %+v err=%v", failed, err)
	}
	if failed.NextRetryAt.Before(started.Add(30*time.Second)) || failed.NextRetryAt.After(time.Now().Add(30*time.Second)) {
		t.Fatalf("first delivery recheck does not use short deadline: %+v", failed)
	}
}
