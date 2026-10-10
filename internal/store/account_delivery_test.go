package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func deliveryTestLogin(t *testing.T, s *Store, email string) AccountDelivery {
	t.Helper()
	_, attempt, err := s.BeginAttempt(context.Background(), Credentials{Email: email, Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	delivery, err := s.FinishAttemptAndQueueDelivery(context.Background(), attempt.ID, &Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, "fixture-destination")
	if err != nil {
		t.Fatal(err)
	}
	return delivery
}

func TestAccountDeliveryLoginAndIntentCommitAtomically(t *testing.T) {
	s, _ := testStore(t)
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "delivery-atomic@example.test", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if _, err := s.db.Exec(`CREATE TRIGGER reject_delivery BEFORE INSERT ON account_deliveries BEGIN SELECT RAISE(ABORT,'delivery storage unavailable'); END`); err != nil {
		t.Fatal(err)
	}
	result := &Result{AccessToken: "atomic-at", RefreshToken: "atomic-rt"}
	if _, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, result, "test-destination"); err == nil {
		t.Fatal("delivery insertion error ignored")
	}
	if version, err := s.GetAccountCredentialVersion(ctx, account.ID); err != nil || version != 0 {
		t.Fatalf("OAuth success partially committed: version=%d err=%v", version, err)
	}
	if _, _, err := s.GetOAuthResultByID(ctx, account.ID); !errors.Is(err, ErrOAuthCredentialsMissing) {
		t.Fatalf("OAuth tokens partially committed: %v", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_delivery`); err != nil {
		t.Fatal(err)
	}
	delivery, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, result, "test-destination")
	if err != nil || delivery.State != "queued" || delivery.CredentialVersion != attempt.ID || delivery.AccountID != account.ID {
		t.Fatalf("delivery=%+v err=%v", delivery, err)
	}
	if _, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{AccessToken: "replayed-at", RefreshToken: "replayed-rt"}, "test-destination"); err == nil {
		t.Fatal("successful attempt replay accepted")
	}
	_, saved, err := s.GetOAuthResultByID(ctx, account.ID)
	if err != nil || saved.AccessToken != "atomic-at" {
		t.Fatalf("attempt replay changed credentials: %v", err)
	}
	payload, err := json.Marshal(delivery)
	if err != nil || strings.Contains(string(payload), "test-destination") || strings.Contains(string(payload), "credential_version") || strings.Contains(string(payload), "atomic-at") {
		t.Fatalf("delivery JSON leaked private data: %s err=%v", payload, err)
	}
}

func TestAccountDeliveryQueriesStayWithinDestination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "delivery-destinations@example.test", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	a, err := s.QueueAccountDelivery(ctx, account.ID, "destination-a", DefaultDeliveryOptions())
	if err != nil {
		t.Fatal(err)
	}
	b, err := s.QueueAccountDelivery(ctx, account.ID, "destination-b", DefaultDeliveryOptions())
	if err != nil {
		t.Fatal(err)
	}
	if a.ID >= b.ID {
		t.Fatalf("fixture destinations were not ordered: a=%d b=%d", a.ID, b.ID)
	}
	due := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	if _, err := s.UpdateAccountDelivery(ctx, a.ID, "retry_wait", "暂时失败", "", &due, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountDelivery(ctx, b.ID, "requires_action", "需要核对", "核对目标", nil, 0); err != nil {
		t.Fatal(err)
	}
	latest, err := s.ListLatestAccountDeliveries(ctx, "destination-a")
	if err != nil || len(latest) != 1 || latest[0].ID != a.ID {
		t.Fatalf("destination-a latest=%+v err=%v", latest, err)
	}
	latest, err = s.ListLatestAccountDeliveries(ctx, "destination-b")
	if err != nil || len(latest) != 1 || latest[0].ID != b.ID {
		t.Fatalf("destination-b latest=%+v err=%v", latest, err)
	}
	latest, err = s.ListLatestAccountDeliveries(ctx)
	if err != nil || len(latest) != 1 || latest[0].ID != b.ID {
		t.Fatalf("unfiltered latest compatibility=%+v err=%v", latest, err)
	}
	dueA, err := s.ListDueAccountDeliveries(ctx, due, 10, "destination-a")
	if err != nil || len(dueA) != 1 || dueA[0].ID != a.ID {
		t.Fatalf("destination-a due=%+v err=%v", dueA, err)
	}
	dueB, err := s.ListDueAccountDeliveries(ctx, due, 10, "destination-b")
	if err != nil || len(dueB) != 0 {
		t.Fatalf("destination-b manual task selected=%+v err=%v", dueB, err)
	}
	if err := s.WakeAccountDelivery(ctx, account.ID, "destination-a"); err != nil {
		t.Fatal(err)
	}
	a, err = s.GetAccountDelivery(ctx, a.ID)
	if err != nil || a.State != "queued" || a.NextRetryAt != nil {
		t.Fatalf("destination-a was not woken: %+v err=%v", a, err)
	}
	b, err = s.GetAccountDelivery(ctx, b.ID)
	if err != nil || b.State != "requires_action" {
		t.Fatalf("destination-b was changed by destination-a wake: %+v err=%v", b, err)
	}
}

func TestNewLoginDoesNotCancelAnotherDestinationDelivery(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "delivery-login-destinations@example.test", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	if _, err := s.FinishAttemptAndQueueDelivery(ctx, attempt.ID, &Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, "destination-a"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.QueueAccountDelivery(ctx, account.ID, "destination-b", DefaultDeliveryOptions()); err != nil {
		t.Fatal(err)
	}
	_, nextAttempt, err := s.BeginAttempt(ctx, Credentials{Email: "delivery-login-destinations@example.test", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer nextAttempt.Release()
	if _, err := s.FinishAttemptAndQueueDelivery(ctx, nextAttempt.ID, &Result{AccessToken: "next-at", RefreshToken: "next-rt"}, "destination-a"); err != nil {
		t.Fatal(err)
	}
	deliveries, err := s.ListLatestAccountDeliveries(ctx, "destination-b")
	if err != nil || len(deliveries) != 1 || deliveries[0].State != "queued" {
		t.Fatalf("destination-b delivery was canceled by destination-a login: %+v err=%v", deliveries, err)
	}
}

func TestAccountDeliveryLatestRetryAndRestart(t *testing.T) {
	s, dir := testStore(t)
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	first := deliveryTestLogin(t, s, "delivery-latest@example.test")
	latest := deliveryTestLogin(t, s, "delivery-latest@example.test")
	prior, err := s.GetAccountDelivery(ctx, first.ID)
	if err != nil || prior.State != "canceled" || prior.NextRetryAt != nil || prior.RequiresAction {
		t.Fatalf("old delivery not superseded=%+v err=%v", prior, err)
	}
	if _, err := s.UpdateAccountDelivery(ctx, first.ID, "checking", "", "", nil, 0); !errors.Is(err, ErrAccountDeliverySettled) {
		t.Fatalf("canceled delivery revived: %v", err)
	}
	due := time.Now().Add(time.Minute).Truncate(time.Millisecond)
	latest, err = s.UpdateAccountDelivery(ctx, latest.ID, "retry_wait", "网络暂不可用", "", &due, 0)
	if err != nil || latest.RetryCount != 1 || latest.NextRetryAt == nil || !latest.NextRetryAt.Equal(due) {
		t.Fatalf("retry deadline=%+v err=%v", latest, err)
	}
	if deliveries, err := s.ListDueAccountDeliveries(ctx, due.Add(-time.Millisecond), 10); err != nil || len(deliveries) != 0 {
		t.Fatalf("early due deliveries=%+v err=%v", deliveries, err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if deliveries, err := s.ListDueAccountDeliveries(ctx, due, 10); err != nil || len(deliveries) != 1 || deliveries[0].ID != latest.ID || deliveries[0].RetryCount != 1 {
		t.Fatalf("restart lost queued delivery=%+v err=%v", deliveries, err)
	}
	latest, err = s.UpdateAccountDelivery(ctx, latest.ID, "requires_action", "绑定需核对", "核对 Sub2 账号", nil, 0)
	if err != nil || !latest.RequiresAction || latest.NextRetryAt != nil || latest.RetryCount != 1 {
		t.Fatalf("manual delivery=%+v err=%v", latest, err)
	}
	if deliveries, err := s.ListDueAccountDeliveries(ctx, due.Add(time.Hour), 10); err != nil || len(deliveries) != 0 {
		t.Fatalf("manual delivery selected=%+v err=%v", deliveries, err)
	}
	if deliveries, err := s.ListLatestAccountDeliveries(ctx); err != nil || len(deliveries) != 1 || deliveries[0].ID != latest.ID || !deliveries[0].RequiresAction {
		t.Fatalf("latest reminder not aggregated=%+v err=%v", deliveries, err)
	}
	if err := s.DeleteAccountByID(ctx, latest.AccountID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetAccountDelivery(ctx, latest.ID); !errors.Is(err, ErrAccountDeliveryNotFound) {
		t.Fatalf("delivery not deleted with account: %v", err)
	}
}

func TestAccountDeliveryHandoffSurvivesNewLoginAndRejectsWrongTask(t *testing.T) {
	s, _ := testStore(t)
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	delivery := deliveryTestLogin(t, s, "delivery-seeded@example.test")
	other := deliveryTestLogin(t, s, "delivery-other@example.test")
	task, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, delivery.ID, delivery.AccountID, 42, delivery.CredentialVersion, true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountDelivery(ctx, other.ID, "verifying", "", "", nil, task.ID); err == nil {
		t.Fatal("another account's delivery task accepted")
	}
	delivery, err = s.UpdateAccountDelivery(ctx, delivery.ID, "verifying", "正在验证", "", nil, task.ID)
	if err != nil || delivery.RecoveryTaskID != task.ID {
		t.Fatalf("handoff=%+v err=%v", delivery, err)
	}
	if deliveries, err := s.ListDueAccountDeliveries(ctx, time.Now(), 10); err != nil || len(deliveries) != 1 || deliveries[0].ID != other.ID {
		t.Fatalf("handed off delivery queued again=%+v err=%v", deliveries, err)
	}
	_ = deliveryTestLogin(t, s, "delivery-seeded@example.test")
	delivery, err = s.GetAccountDelivery(ctx, delivery.ID)
	if err != nil || delivery.State != "verifying" || delivery.RecoveryTaskID != task.ID {
		t.Fatalf("new login rewrote handoff=%+v err=%v", delivery, err)
	}
	if err := s.ValidateAccountRecoveryVersion(ctx, task.ID); !errors.Is(err, ErrAccountRecoveryVersionChanged) {
		t.Fatalf("handoff can overwrite newer login: %v", err)
	}
	delivery, err = s.UpdateAccountDelivery(ctx, delivery.ID, "canceled", "凭据已更新", "", nil, 0)
	if err != nil || delivery.RecoveryTaskID != task.ID || delivery.RequiresAction {
		t.Fatalf("handoff reference lost=%+v err=%v", delivery, err)
	}
}
