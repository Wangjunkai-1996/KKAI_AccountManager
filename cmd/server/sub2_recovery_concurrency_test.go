package main

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2RecoveryActiveTransientFailureRetainsPause(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.detailStatus, f.detailError = "active", ""
	f.service.autoRecovery.Store(true)
	loginOK := f.service.loginWithProxies
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "network_error"}
	}
	f.service.process(f.task)
	failed := f.state(t)
	if failed.RetryAction != "relogin" || failed.FailureStage != store.RecoveryLoggingIn || f.schedule {
		t.Fatalf("unexpected initial failure: %+v schedule=%v", failed, f.schedule)
	}
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	got := f.state(t)
	if got.State != store.RecoveryQueued {
		t.Fatalf("worker-owned pause blocked retry: state=%s code=%s action=%s next=%v schedule=%v", got.State, got.ErrorCode, got.RetryAction, got.NextRetryAt, f.schedule)
	}
	f.service.loginWithProxies = loginOK
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || !f.schedule {
		t.Fatalf("active recovery did not complete after transient failure: %+v schedule=%v", got, f.schedule)
	}
}

func TestSub2RecoveryValidationTransientFailureRetriesOnActiveScheduledAccount(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.detailStatus = "active"
	f.detailError = ""
	f.service.recordRecoveryFailure(f.task, store.RecoveryValidating, &recoveryOperationError{Code: "upstream_error", HTTPStatus: http.StatusBadGateway})
	failed := f.state(t)
	if failed.RetryAction != "relogin" || failed.FailureStage != store.RecoveryValidating || failed.NextRetryAt == nil {
		t.Fatalf("validation failure was not queued for retry: %+v", failed)
	}
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	queued := f.state(t)
	if queued.State != store.RecoveryQueued {
		t.Fatalf("validation retry was not requeued: %+v", queued)
	}
	f.service.process(queued)
	finished := f.state(t)
	if finished.State != store.RecoveryCompleted || f.loginCalls != 1 || !f.schedule {
		t.Fatalf("validation retry did not complete: %+v login=%d schedule=%v", finished, f.loginCalls, f.schedule)
	}
}

func TestSub2RecoveryDeletedAccountDoesNotPauseGlobalWorker(t *testing.T) {
	f := newRecoveryFixture(t, true)
	if _, err := f.db.Exec(`DELETE FROM account_recovery_tasks WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.service.recordRecoveryFailure(f.task, store.RecoveryLoggingIn, &recoveryOperationError{Code: "account_unavailable", RequiresAction: true})
	if f.service.paused.Load() {
		t.Fatal("deleted account paused the global recovery worker")
	}
}

type recoveryConcurrencyTransport func(*http.Request) (*http.Response, error)

func (f recoveryConcurrencyTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestSub2RecoveryStaleRetryDoesNotOverwriteNewLogin(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "network_error"}
	}
	f.service.process(f.task)
	recoveryRetryDue(t, f)

	monitorBlocked, releaseMonitor, monitorDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	loginBlocked, releaseLogin, workerDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	base := f.service.sub2.client.Transport
	var intercepted atomic.Bool
	f.service.sub2.client.Transport = recoveryConcurrencyTransport(func(r *http.Request) (*http.Response, error) {
		if r.Method == http.MethodGet && r.URL.Path == "/admin/accounts/901" && intercepted.CompareAndSwap(false, true) {
			close(monitorBlocked)
			<-releaseMonitor
			return nil, &recoveryOperationError{Code: "network_error"}
		}
		return base.RoundTrip(r)
	})
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		close(loginBlocked)
		<-releaseLogin
		return &login.LoginResult{Email: f.account.Email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace"}, nil
	}
	go func() { f.service.retryDueRecoveries(); close(monitorDone) }()
	<-monitorBlocked
	task, _, err := f.service.enqueue(context.Background(), f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	go func() { f.service.process(task); close(workerDone) }()
	<-loginBlocked
	close(releaseMonitor)
	<-monitorDone
	close(releaseLogin)
	<-workerDone
	got := f.state(t)
	if f.service.paused.Load() || got.State != store.RecoveryCompleted {
		t.Fatalf("stale monitor failure overwrote active login: paused=%v state=%s stage=%s code=%s", f.service.paused.Load(), got.State, got.FailureStage, got.ErrorCode)
	}
}

func TestSub2RecoveryRestartClaimPreservesOperatorPause(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.service.process(f.task)
	f.probeBody = "data: {\"type\":\"error\",\"status\":401,\"code\":\"token_revoked\"}\n\n"
	forceDueRecheck(t, f)
	if !f.service.processNextRecheck() {
		t.Fatal("recheck not processed")
	}
	child, err := f.store.GetLatestAccountRecoveryTask(context.Background(), f.account.ID)
	if err != nil || child.ID == f.task.ID {
		t.Fatalf("child: %+v err=%v", child, err)
	}
	// Operator pauses the still-active account after recheck queues a child.
	f.schedule = false
	// A crash occurs after claim but before process performs any remote write.
	if _, err := f.store.ClaimAccountRecoveryTask(context.Background(), child.ID); err != nil {
		t.Fatal(err)
	}
	if err := f.store.RecoverAccountRecoveryTasks(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.service.retryDueRecoveries()
	resumed, err := f.store.GetAccountRecoveryTaskByID(context.Background(), child.ID)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.State != store.RecoveryCanceled || resumed.ErrorCode != "manual_pause" || f.schedule || f.loginCalls != 1 {
		t.Fatalf("claim was mistaken for confirmed pause: %+v schedule=%v login=%d", resumed, f.schedule, f.loginCalls)
	}
}

func TestSub2RecoveryAppliedCheckpointRequiresMatchingMarker(t *testing.T) {
	for _, stage := range []string{store.RecoveryCredentialsApplied, store.RecoveryEnablingSchedule} {
		for _, marker := range []string{"deleted", "replaced"} {
			t.Run(stage+"/"+marker, func(t *testing.T) {
				f := newRecoveryFixture(t, true)
				f.service.autoRecovery.Store(true)
				if stage == store.RecoveryCredentialsApplied {
					f.probeStatus = http.StatusServiceUnavailable
				} else {
					f.enableFailsOnce = true
				}
				f.service.process(f.task)
				failed := f.state(t)
				if failed.FailureStage != stage || failed.RetryAction != "resume" || failed.ResultCredentialAttemptID == 0 || f.schedule {
					t.Fatalf("missing applied checkpoint: %+v schedule=%v", failed, f.schedule)
				}
				if marker == "deleted" {
					f.recoveryMarker = nil
				} else {
					f.recoveryMarker = map[string]any{"task_id": float64(f.task.ID + 1), "credential_attempt_id": float64(failed.ResultCredentialAttemptID)}
				}
				f.probeStatus = 0
				recoveryRetryDue(t, f)
				f.service.retryDueRecoveries()
				got := f.state(t)
				if !got.RequiresAction || got.NextRetryAt != nil || got.State == store.RecoveryQueued || f.schedule || f.loginCalls != 1 || f.applyCalls != 1 {
					t.Fatalf("changed marker resumed applied checkpoint: %+v schedule=%v login=%d apply=%d", got, f.schedule, f.loginCalls, f.applyCalls)
				}
			})
		}
	}
}

func TestSub2RecoveryStaleFailureSameMillisecondDoesNotOverwriteNewRound(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return nil, &recoveryOperationError{Code: "network_error"}
	}
	f.service.process(f.task)
	old := f.state(t)
	ctx := context.Background()
	if _, err := f.store.RetryAccountRecoveryTask(ctx, old.ID, true); err != nil {
		t.Fatal(err)
	}
	runQueuedRecovery(t, f)
	current := f.state(t)
	if old.State != current.State || current.RetryCount != old.RetryCount+1 {
		t.Fatalf("unexpected failed rounds: old=%+v current=%+v", old, current)
	}
	// Force the timestamp collision of two fast rounds. The persisted failure
	// count must fence a late response even with the same state and timestamp.
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET updated_at=? WHERE id=?`, old.UpdatedAt.UnixMilli(), old.ID); err != nil {
		t.Fatal(err)
	}
	f.service.recordRecoveryFailure(old, old.FailureStage, &recoveryOperationError{Code: "identity_changed", RequiresAction: true})
	got := f.state(t)
	if got.RetryCount != current.RetryCount || got.ErrorCode != current.ErrorCode || got.RequiresAction || f.service.paused.Load() {
		t.Fatalf("same-millisecond stale failure overwrote new round: %+v paused=%v", got, f.service.paused.Load())
	}
}
