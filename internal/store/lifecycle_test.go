package store

import (
	"context"
	"errors"
	"testing"
)

func TestOfficialLifecycleEventAndAccountTombstoneAreRetained(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "official@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := s.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishAttempt(ctx, attempt.ID, false, nil, &Failure{Code: "account_deactivated", AccountStatus: "deactivated", Message: "official account disabled", HTTPStatus: 403}); err != nil {
		t.Fatal(err)
	}
	attempt.Release()

	events, err := s.ListLifecycleEventsForAccount(ctx, account.ID, 10)
	if err != nil || len(events) != 1 {
		t.Fatalf("events = %+v, err=%v", events, err)
	}
	if events[0].Email != account.Email || events[0].Source != "official" || events[0].Status != "deactivated" || events[0].Code != "account_deactivated" || events[0].HTTPStatus != 403 {
		t.Fatalf("event = %+v", events[0])
	}
	if err := s.DeleteAccountByID(ctx, account.ID); !errors.Is(err, ErrAccountOfficialUnavailable) {
		t.Fatalf("delete tombstone: %v", err)
	}
	if _, err := s.UpsertCredentials(ctx, Credentials{Email: account.Email, Password: "replacement"}); !errors.Is(err, ErrAccountOfficialUnavailable) {
		t.Fatalf("replace tombstone credentials: %v", err)
	}
	if secret, err := s.RevealCredential(ctx, account.ID, "password"); secret != "" || !errors.Is(err, ErrAccountOfficialUnavailable) {
		t.Fatalf("read tombstone credential: %v", err)
	}
	byID, err := s.ListLifecycleEventsForAccount(ctx, account.ID, 10)
	if err != nil || len(byID) != 1 {
		t.Fatalf("events by deleted id = %+v, err=%v", byID, err)
	}
	byEmail, err := s.ListLifecycleEventsForEmail(ctx, account.Email, 10)
	if err != nil || len(byEmail) != 1 {
		t.Fatalf("events by deleted email = %+v, err=%v", byEmail, err)
	}
}

func TestRecordLifecycleEventIsIdempotent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	event := LifecycleEvent{AccountID: 42, Email: "repeat@example.test", Source: "sub2", Status: "deleted", DedupeKey: "sub2:42:deleted"}
	first, err := s.RecordLifecycleEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.RecordLifecycleEvent(ctx, event)
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == 0 || second.ID != first.ID {
		t.Fatalf("duplicate event ids = %d and %d", first.ID, second.ID)
	}
	all, err := s.ListLifecycleEvents(ctx, 10)
	if err != nil || len(all) != 1 {
		t.Fatalf("events = %+v, err=%v", all, err)
	}
	if _, err := s.ListLifecycleEventsForAccount(ctx, 0, 10); err == nil {
		t.Fatal("zero account id unexpectedly listed events")
	}
}
