package store

import (
	"context"
	"testing"
)

func TestRecoveryHistoryCountsCompletedTasksAndBoundsAccountHistory(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "history@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.UpsertCredentials(ctx, Credentials{Email: "other@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	// Initial authentication and a later standalone login are not completed
	// Sub2 recoveries, even though both save usable OAuth credentials.
	for range 2 {
		attempt, err := s.StartAttempt(ctx, account.Email)
		if err != nil {
			t.Fatal(err)
		}
		err = s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, nil)
		attempt.Release()
		if err != nil {
			t.Fatal(err)
		}
	}
	empty, truncated, err := s.ListAccountRecoveryHistory(ctx, account.ID, 0)
	if err != nil || empty == nil || len(empty) != 0 || truncated {
		t.Fatalf("empty history = %+v, %v, %v", empty, truncated, err)
	}
	account, err = s.GetAccountByID(ctx, account.ID)
	if err != nil || account.RecoveryCount != 0 || account.RecoveryAttemptCount != 0 {
		t.Fatalf("login counted as recovery: %+v, %v", account, err)
	}
	for index := range 103 {
		state := RecoveryUnknown
		if index < 3 {
			state = RecoveryCompleted
		}
		if _, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,sub2_account_id,state,created_at,updated_at) VALUES(?,42,?,?,?)`, account.ID, state, index+1, index+1); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO account_recovery_tasks(account_id,sub2_account_id,state,created_at,updated_at) VALUES(?,43,'completed',999,999)`, other.ID); err != nil {
		t.Fatal(err)
	}
	account, err = s.GetAccountByID(ctx, account.ID)
	if err != nil || account.RecoveryCount != 3 || account.RecoveryAttemptCount != 103 {
		t.Fatalf("counts = %+v, %v", account, err)
	}
	history, truncated, err := s.ListAccountRecoveryHistory(ctx, account.ID, 0)
	if err != nil || len(history) != 100 || !truncated {
		t.Fatalf("bounded history = %d, %v, %v", len(history), truncated, err)
	}
	for index, task := range history {
		if task.AccountID != account.ID || (index > 0 && task.ID >= history[index-1].ID) {
			t.Fatalf("history isolation/order: %+v", history)
		}
	}
	// Continuing one task changes its outcome without adding another attempt.
	if _, err := s.UpdateAccountRecoveryTask(ctx, history[0].ID, RecoveryCompleted, "completed after resume"); err != nil {
		t.Fatal(err)
	}
	accounts, err := s.ListAccounts(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, got := range accounts {
		if got.ID == account.ID && (got.RecoveryCount != 4 || got.RecoveryAttemptCount != 103) {
			t.Fatalf("resume counted twice: %+v", got)
		}
	}
	otherHistory, truncated, err := s.ListAccountRecoveryHistory(ctx, other.ID, 100)
	if err != nil || len(otherHistory) != 1 || truncated || otherHistory[0].AccountID != other.ID {
		t.Fatalf("other history = %+v, %v, %v", otherHistory, truncated, err)
	}
}

func TestRecoveryHistoryFiltersDestination(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "history-destination@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "alpha", account.ID, 101, "linked-alpha-history"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.LinkSub2Account(ctx, "beta", account.ID, 202, "linked-beta-history"); err != nil {
		t.Fatal(err)
	}
	alpha, _, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 1, 101, true, "alpha")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, alpha.ID, RecoveryCompleted, ""); err != nil {
		t.Fatal(err)
	}
	beta, _, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 2, 202, true, "beta")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountRecoveryTask(ctx, beta.ID, RecoveryCompleted, ""); err != nil {
		t.Fatal(err)
	}
	history, _, err := s.ListAccountRecoveryHistory(ctx, account.ID, 100, "alpha")
	if err != nil || len(history) != 1 || history[0].ID != alpha.ID {
		t.Fatalf("alpha history = %+v, err=%v", history, err)
	}
}
