package main

import (
	"context"
	"database/sql/driver"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
	"modernc.org/sqlite"
)

func TestSub2RecoveryWorkerRetriesContendedQueue(t *testing.T) {
	// Hold the Store's actual single connection inside a SQLite statement. This
	// reproduces a queue read timing out behind a slow metadata operation.
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	name := fmt.Sprintf("hold_queue_connection_%d", time.Now().UnixNano())
	if err := sqlite.RegisterScalarFunction(name, 0, func(_ *sqlite.FunctionContext, _ []driver.Value) (driver.Value, error) {
		enteredOnce.Do(func() { close(entered) })
		<-release
		return int64(0), nil
	}); err != nil {
		t.Fatal(err)
	}
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	binding, err := f.store.GetSub2Import(ctx, "fixture", f.account.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER hold_queue BEFORE UPDATE ON sub2_imports BEGIN SELECT ` + name + `(); END`); err != nil {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { written <- f.store.UpdateSub2Import(ctx, binding.ID, "imported", true, 901, "") }()
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("metadata operation did not hold connection")
	}
	f.service.wg.Add(1)
	go f.service.worker()
	// The production read deadline is five seconds. The old worker latched its
	// pause here and never consumed this persisted task again.
	time.Sleep(6 * time.Second)
	close(release)
	if err := <-written; err != nil {
		t.Fatal(err)
	}
	if f.service.paused.Load() {
		t.Fatal("temporary queue read timeout permanently paused recovery")
	}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		task, err := f.store.GetAccountRecoveryTaskByID(ctx, f.task.ID)
		if err != nil {
			t.Fatal(err)
		}
		if task.State == store.RecoveryCompleted {
			return
		}
		if f.service.paused.Load() {
			t.Fatal("worker paused after storage recovered")
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("worker did not finish queued recovery after storage recovered")
}

func TestAccountDeliveryReportsPausedService(t *testing.T) {
	f, _, delivery := newDeliveryFixture(t)
	old := accountRecoveryService
	accountRecoveryService = f.service
	defer func() { accountRecoveryService = old }()
	f.service.paused.Store(true)
	tasks, err := listDeliveryStatuses(context.Background(), f.store, "fixture")
	if err != nil || tasks[f.account.ID].State != "queued" || tasks[f.account.ID].BlockedReason == "" {
		t.Fatalf("queued delivery hid its paused service: %+v %v", tasks[f.account.ID], err)
	}
	if _, err := f.store.UpdateAccountDelivery(context.Background(), delivery.ID, "completed", "", "", nil, 0); err != nil {
		t.Fatal(err)
	}
	tasks, err = listDeliveryStatuses(context.Background(), f.store, "fixture")
	if err != nil || tasks[f.account.ID].State != "completed" || tasks[f.account.ID].BlockedReason != "" {
		t.Fatalf("completed delivery was masked by pause: %+v %v", tasks[f.account.ID], err)
	}
}

func TestSub2RecoveryWorkerPauseStopsRecheckDispatch(t *testing.T) {
	f := newRecoveryFixture(t, true)
	ctx := context.Background()
	f.service.process(f.task)
	if task, err := f.store.GetAccountRecoveryTaskByID(ctx, f.task.ID); err != nil || task.State != store.RecoveryCompleted {
		t.Fatalf("setup recovery did not complete: %s %v", task.State, err)
	}
	if _, err := f.db.Exec(`UPDATE account_recovery_rechecks SET next_check_at=0`); err != nil {
		t.Fatal(err)
	}
	account, err := f.store.UpsertCredentials(ctx, store.Credentials{Email: "repair-failure@example.test", Password: "old"})
	if err != nil {
		t.Fatal(err)
	}
	password := "updated"
	if _, err := f.store.PatchAccountCredentials(ctx, account.ID, store.CredentialPatch{Password: &password}, true); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`CREATE TRIGGER reject_repair_claim BEFORE UPDATE ON account_credential_repairs BEGIN SELECT RAISE(ABORT,'claim failure'); END`); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	callsBefore := len(f.methods)
	f.mu.Unlock()
	done := make(chan struct{})
	f.service.wg.Add(1)
	go func() { f.service.worker(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("storage circuit did not stop the worker")
	}
	if !f.service.paused.Load() {
		t.Fatal("claim write failure did not retain storage protection")
	}
	rechecks, err := f.store.ListAccountRecoveryRechecks(ctx)
	if err != nil || rechecks[f.account.ID].State != "pending" || rechecks[f.account.ID].Round != 1 {
		t.Fatalf("worker consumed recheck after durable claim failure: %+v %v", rechecks[f.account.ID], err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.methods) != callsBefore {
		t.Fatal("paused worker performed subsequent remote work")
	}
}
