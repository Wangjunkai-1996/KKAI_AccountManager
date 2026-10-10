package store

import (
	"context"
	"errors"
	"testing"
)

func TestLinkSub2AccountIsTerminalAndPreservesExistingTask(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, err := s.UpsertCredentials(ctx, Credentials{Email: "link@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.LinkSub2Account(ctx, "current", a.ID, 42, "linked-first")
	if err != nil || linked.State != "imported" || linked.Sub2AccountID != 42 {
		t.Fatalf("link = %+v, %v", linked, err)
	}
	queued, err := s.ListQueuedSub2Imports(ctx, 10)
	if err != nil || len(queued) != 0 {
		t.Fatalf("association entered import queue: %+v, %v", queued, err)
	}
	again, err := s.LinkSub2Account(ctx, "current", a.ID, 43, "linked-second")
	if err != nil || again.ID != linked.ID || again.Sub2AccountID != 42 {
		t.Fatalf("existing binding overwritten: %+v, %v", again, err)
	}
	queuedTask, _, err := s.CreateOrGetSub2Import(ctx, "other", a.ID, "queued", "queued-key", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	unchanged, err := s.LinkSub2Account(ctx, "other", a.ID, 45, "linked-other")
	if err != nil || unchanged.ID != queuedTask.ID || unchanged.State != "queued" {
		t.Fatalf("queued import overwritten: %+v, %v", unchanged, err)
	}
	if _, err := s.LinkSub2Account(ctx, "current", a.ID+100, 46, "linked-missing"); !errors.Is(err, ErrAccountNotFound) {
		t.Fatalf("missing account link error = %v", err)
	}
}

func TestDeleteSub2ImportBindingRequiresOperationIdentity(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a, err := s.UpsertCredentials(ctx, Credentials{Email: "delete-cas@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	linked, err := s.LinkSub2Account(ctx, "current", a.ID, 42, "linked-first")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSub2ImportBinding(ctx, "current", a.ID, 42, "linked-other"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSub2Import(ctx, "current", a.ID); err != nil {
		t.Fatalf("wrong operation removed binding: %v", err)
	}
	if err := s.DeleteSub2ImportBinding(ctx, "current", a.ID, 42, linked.OperationID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSub2Import(ctx, "current", a.ID); !errors.Is(err, ErrSub2ImportNotFound) {
		t.Fatalf("binding remains after matching delete: %v", err)
	}
}
