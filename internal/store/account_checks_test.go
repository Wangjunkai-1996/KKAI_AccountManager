package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func checkTestAccount(t *testing.T, s *Store, name string) int64 {
	t.Helper()
	ctx := context.Background()
	email := name + "@example.test"
	a, err := s.UpsertCredentials(ctx, Credentials{Email: email, Password: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	checkTestLogin(t, s, email)
	task, _, err := s.CreateOrGetSub2Import(ctx, "test", a.ID, "op-"+name, "key-"+name, []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err = s.UpdateSub2Import(ctx, task.ID, "imported", true, 100+a.ID, ""); err != nil {
		t.Fatal(err)
	}
	return a.ID
}
func checkTestLogin(t *testing.T, s *Store, email string) {
	t.Helper()
	ctx := context.Background()
	attempt, err := s.StartAttempt(ctx, email)
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err = s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "test-secret-at", RefreshToken: "test-secret-rt", ChatGPTAccountID: "workspace"}, nil); err != nil {
		t.Fatal(err)
	}
}
func checkTestBatch(t *testing.T, s *Store, key string, concurrency int, ids ...int64) AccountCheckBatch {
	t.Helper()
	b, reused, err := s.CreateAccountCheckBatch(context.Background(), AccountCheckInput{RequestKey: key, AccountIDs: ids, Concurrency: concurrency, ProxyMode: "direct"})
	if err != nil || reused {
		t.Fatalf("create: reused=%v err=%v", reused, err)
	}
	return b
}
func checkTestClaim(t *testing.T, s *Store) *AccountCheckWork {
	t.Helper()
	work, err := s.ClaimAccountCheck(context.Background())
	if err != nil || work == nil {
		t.Fatalf("claim work=%v err=%v", work, err)
	}
	return work
}
func checkTestErrorCode(t *testing.T, err error, code string) {
	t.Helper()
	var typed *AccountCheckError
	if !errors.As(err, &typed) || typed.Code != code {
		t.Fatalf("error=%v want %s", err, code)
	}
}

func TestAccountCheckConcurrentLimit(t *testing.T) {
	for _, concurrency := range []int{1, 2} {
		t.Run(fmt.Sprint(concurrency), func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			ids := []int64{checkTestAccount(t, s, "a"), checkTestAccount(t, s, "b"), checkTestAccount(t, s, "c")}
			batch := checkTestBatch(t, s, "concurrent", concurrency, ids...)
			var wg sync.WaitGroup
			works := make(chan *AccountCheckWork, 8)
			errs := make(chan error, 8)
			for range 8 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					w, e := s.ClaimAccountCheck(ctx)
					if e != nil {
						errs <- e
					}
					if w != nil {
						works <- w
					}
				}()
			}
			wg.Wait()
			close(works)
			close(errs)
			for err := range errs {
				t.Fatal(err)
			}
			if len(works) != concurrency {
				t.Fatalf("claimed=%d expected=%d", len(works), concurrency)
			}
			for w := range works {
				if _, err := s.FinishAccountCheck(ctx, w.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
					t.Fatal(err)
				}
			}
			for {
				w, e := s.ClaimAccountCheck(ctx)
				if e != nil {
					t.Fatal(e)
				}
				if w == nil {
					break
				}
				if _, e = s.FinishAccountCheck(ctx, w.Check.ID, AccountCheckResult{Outcome: "ok"}); e != nil {
					t.Fatal(e)
				}
			}
			b, _, err := s.GetAccountCheckBatch(ctx, batch.ID)
			if err != nil || b.State != "completed" || b.Counts.Settled != 3 || b.Counts.OK != 3 {
				t.Fatalf("batch=%+v err=%v", b, err)
			}
		})
	}
}

func TestAccountCheckIdempotencyAndEligibility(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := checkTestAccount(t, s, "a")
	b := checkTestAccount(t, s, "b")
	input := AccountCheckInput{RequestKey: "same", AccountIDs: []int64{b, a, a}, Concurrency: 2, ProxyMode: "default", Proxy: "http://user:private-proxy-password@example.test:8080"}
	batch, reused, err := s.CreateAccountCheckBatch(ctx, input)
	if err != nil || reused {
		t.Fatal(err)
	}
	input.AccountIDs = []int64{a, b}
	input.Proxy = ""
	lookedUp, found, lookupErr := s.LookupAccountCheckBatch(ctx, input)
	if lookupErr != nil || !found || lookedUp.ID != batch.ID {
		t.Fatalf("lookup after proxy removal=%+v found=%v err=%v", lookedUp, found, lookupErr)
	}
	again, reused, err := s.CreateAccountCheckBatch(ctx, input)
	if err != nil || !reused || again.ID != batch.ID {
		t.Fatalf("idempotency=%+v,%v,%v", again, reused, err)
	}
	input.Concurrency = 1
	_, _, err = s.LookupAccountCheckBatch(ctx, input)
	checkTestErrorCode(t, err, "idempotency_conflict")
	_, _, err = s.CreateAccountCheckBatch(ctx, input)
	checkTestErrorCode(t, err, "idempotency_conflict")
	input.RequestKey = "new"
	if _, found, err := s.LookupAccountCheckBatch(ctx, input); err != nil || found {
		t.Fatalf("absent request lookup found=%v err=%v", found, err)
	}
	_, _, err = s.CreateAccountCheckBatch(ctx, input)
	checkTestErrorCode(t, err, "batch_active")
	var encrypted []byte
	if err = s.db.QueryRow(`SELECT proxy_cipher FROM account_check_batches WHERE id=?`, batch.ID).Scan(&encrypted); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encrypted), "private-proxy-password") {
		t.Fatal("plaintext proxy persisted")
	}
	if _, err = s.CancelAccountCheckBatch(ctx, batch.ID); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT proxy_cipher FROM account_check_batches WHERE id=?`, batch.ID).Scan(&encrypted); err != nil || len(encrypted) != 0 {
		t.Fatal("terminal batch retained proxy snapshot")
	}
	input.AccountIDs = []int64{a, 999999}
	input.ProxyMode = "direct"
	_, _, err = s.CreateAccountCheckBatch(ctx, input)
	checkTestErrorCode(t, err, "ineligible_accounts")
	batches, err := s.ListAccountCheckBatches(ctx, false, 20)
	if err != nil || len(batches) != 1 {
		t.Fatal("invalid batch was partly created")
	}
	raw, _ := json.Marshal(batches)
	if strings.Contains(string(raw), "private-proxy-password") {
		t.Fatal("proxy in JSON")
	}
}

func TestAccountCheckCancelAndProxyStop(t *testing.T) {
	for _, cause := range []string{"user", "407"} {
		t.Run(cause, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			batch := checkTestBatch(t, s, "cancel", 2, checkTestAccount(t, s, "a"), checkTestAccount(t, s, "b"), checkTestAccount(t, s, "c"))
			first := checkTestClaim(t, s)
			second := checkTestClaim(t, s)
			if cause == "user" {
				if _, err := s.CancelAccountCheckBatch(ctx, batch.ID); err != nil {
					t.Fatal(err)
				}
				if _, err := s.FinishAccountCheck(ctx, first.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
					t.Fatal(err)
				}
			} else {
				status := 407
				b, err := s.FinishAccountCheck(ctx, first.Check.ID, AccountCheckResult{Outcome: "proxy_error", ProxyHTTPStatus: &status})
				if err != nil || b.State != "stopping" || b.StopReason != "shared_proxy_failure" {
					t.Fatalf("407 stop=%+v %v", b, err)
				}
			}
			b, err := s.CancelAccountCheckBatch(ctx, batch.ID)
			if err != nil || b.State != "stopping" {
				t.Fatal("running task released activity early")
			}
			if cause == "407" && b.StopReason != "shared_proxy_failure" {
				t.Fatal("cancel overwrote stop reason")
			}
			if marked, err := s.MarkAccountCheckAttempted(ctx, second.Check.ID); err != nil || marked {
				t.Fatal("stopping task entered request stage")
			}
			if next, err := s.ClaimAccountCheck(ctx); err != nil || next != nil {
				t.Fatal("stopping batch dispatched task")
			}
			_, _, err = s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: "new", AccountIDs: []int64{*second.Check.AccountID}, Concurrency: 1, ProxyMode: "direct"})
			checkTestErrorCode(t, err, "batch_active")
			if _, err = s.FinishAccountCheck(ctx, second.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
				t.Fatal(err)
			}
			b, items, err := s.GetAccountCheckBatch(ctx, batch.ID)
			if err != nil || b.State != "stopped" || b.Counts.Settled != 3 {
				t.Fatalf("batch=%+v err=%v", b, err)
			}
			want := "canceled"
			if cause == "407" {
				want = "finished"
			}
			if items[0].State != want || items[1].State != "canceled" || items[2].State != "canceled" {
				t.Fatalf("items=%+v", items)
			}
			// A duplicate/late result cannot rewrite a terminal result.
			if _, err = s.FinishAccountCheck(ctx, first.Check.ID, AccountCheckResult{Outcome: "unauthorized"}); err != nil {
				t.Fatal(err)
			}
			_, after, _ := s.GetAccountCheckBatch(ctx, batch.ID)
			if after[0].Outcome != items[0].Outcome {
				t.Fatal("late result overwrote terminal outcome")
			}
		})
	}
}

func TestAccountCheckRecover(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	ids := []int64{checkTestAccount(t, s, "a"), checkTestAccount(t, s, "b"), checkTestAccount(t, s, "c"), checkTestAccount(t, s, "d")}
	batch := checkTestBatch(t, s, "recover", 2, ids...)
	a := checkTestClaim(t, s)
	b := checkTestClaim(t, s)
	if ok, err := s.MarkAccountCheckAttempted(ctx, b.Check.ID); err != nil || !ok {
		t.Fatal(err)
	}
	if _, err := s.FinishAccountCheck(ctx, a.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	c := checkTestClaim(t, s)
	// Unknown legacy/interrupted stage evidence must remain unknown after recovery.
	if _, err := s.db.Exec(`UPDATE account_checks SET request_attempted=NULL WHERE id=?`, c.Check.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	current, items, err := restored.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil || current.State != "active" || current.Counts.Interrupted != 2 || current.Counts.Queued != 1 {
		t.Fatalf("batch=%+v err=%v", current, err)
	}
	if items[1].RequestAttempted == nil || !*items[1].RequestAttempted || items[2].RequestAttempted != nil {
		t.Fatal("recovery fabricated request-stage evidence")
	}
	d := checkTestClaim(t, restored)
	if *d.Check.AccountID != ids[3] {
		t.Fatal("recovery replayed an attempted account")
	}
	if _, err = restored.FinishAccountCheck(ctx, d.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	current, _, _ = restored.GetAccountCheckBatch(ctx, batch.ID)
	if current.State != "completed" {
		t.Fatal("recovered batch retained active lock")
	}
	// Recover stopping with a live owner only after that owner is gone.
	newer := checkTestBatch(t, restored, "stopping", 1, ids[1], ids[2])
	checkTestClaim(t, restored)
	if _, err = restored.CancelAccountCheckBatch(ctx, newer.ID); err != nil {
		t.Fatal(err)
	}
	if err = restored.RecoverAccountChecks(ctx); err != nil {
		t.Fatal(err)
	}
	current, _, _ = restored.GetAccountCheckBatch(ctx, newer.ID)
	if current.State != "stopped" || current.StopReason != "user_cancel" {
		t.Fatalf("stopping recovery=%+v", current)
	}
}

func TestAccountCheckCredentialVersionAndDelete(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := checkTestAccount(t, s, "a")
	batch := checkTestBatch(t, s, "old", 1, a)
	old := checkTestClaim(t, s)
	// Re-login within the same clock second still creates a distinct credential version.
	checkTestLogin(t, s, "a@example.test")
	if _, err := s.FinishAccountCheck(ctx, old.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	_, items, _ := s.GetAccountCheckBatch(ctx, batch.ID)
	if items[0].Freshness != "stale" {
		t.Fatal("old result appears current")
	}
	summary, err := s.ListAccountCheckSummaries(ctx)
	if err != nil || summary[a].LastResult.Freshness != "stale" || summary[a].CooldownRemainingSeconds != 0 {
		t.Fatal("old version caused cooldown/current result")
	}
	newer := checkTestBatch(t, s, "new", 1, a)
	work := checkTestClaim(t, s)
	if _, err = s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	checkTestLogin(t, s, "a@example.test")
	_, items, _ = s.GetAccountCheckBatch(ctx, newer.ID)
	if items[0].Freshness != "stale" {
		t.Fatal("post-completion login did not invalidate freshness")
	}
	removal := checkTestBatch(t, s, "delete", 1, a)
	work = checkTestClaim(t, s)
	if err = s.DeleteAccount(ctx, "a@example.test"); err != nil {
		t.Fatal(err)
	}
	replacement, err := s.UpsertCredentials(ctx, Credentials{Email: "a@example.test", Password: "new-password"})
	if err != nil {
		t.Fatal(err)
	}
	if replacement.ID == a {
		t.Fatal("fixture failed to create new identity")
	}
	if _, err = s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	_, items, _ = s.GetAccountCheckBatch(ctx, removal.ID)
	if items[0].AccountID != nil || items[0].Freshness != "account_removed" || items[0].SkipReason != "account_removed" {
		t.Fatalf("deleted result=%+v", items[0])
	}
	summaries, _ := s.ListAccountCheckSummaries(ctx)
	if summaries[replacement.ID].LastResult != nil || summaries[replacement.ID].Eligible {
		t.Fatal("new identity inherited old result/import")
	}
}

func TestAccountCheckPrecheckAndNoRefreshRead(t *testing.T) {
	for _, test := range []struct{ name, sql, outcome, code string }{
		{"missing", "UPDATE accounts SET access_token_cipher=NULL", "credential_missing", "credential_missing"},
		{"version", "DELETE FROM login_attempts", "credential_incomplete", "credential_version_missing"},
		{"workspace", "UPDATE accounts SET chatgpt_account_id=''", "credential_incomplete", "credential_incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			a := checkTestAccount(t, s, "a")
			if _, err := s.db.Exec(test.sql); err != nil {
				t.Fatal(err)
			}
			batch := checkTestBatch(t, s, "precheck", 1, a)
			work, err := s.ClaimAccountCheck(ctx)
			if err != nil || work != nil {
				t.Fatalf("unexpected request work=%+v err=%v", work, err)
			}
			b, items, _ := s.GetAccountCheckBatch(ctx, batch.ID)
			if b.State != "completed" || b.Counts.Precheck != 1 || items[0].Outcome != test.outcome || items[0].ErrorCode != test.code || items[0].RequestAttempted == nil || *items[0].RequestAttempted {
				t.Fatalf("precheck=%+v", items)
			}
		})
	}
	s, _ := testStore(t)
	ctx := context.Background()
	a := checkTestAccount(t, s, "a")
	// Corrupt RT ciphertext: successful claim proves it was not decrypted.
	if _, err := s.db.Exec(`UPDATE accounts SET refresh_token_cipher=x'010203' WHERE id=?`, a); err != nil {
		t.Fatal(err)
	}
	batch := checkTestBatch(t, s, "rt", 1, a)
	work := checkTestClaim(t, s)
	if work.AccessToken != "test-secret-at" {
		t.Fatal("missing AT")
	}
	raw, _ := json.Marshal(work)
	if strings.Contains(string(raw), "test-secret-at") || strings.Contains(string(raw), "test-secret-rt") {
		t.Fatal("credential exposed in safe JSON")
	}
	if _, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "ok"}); err != nil {
		t.Fatal(err)
	}
	repeated := checkTestBatch(t, s, "cooldown", 1, a)
	if next, err := s.ClaimAccountCheck(ctx); err != nil || next != nil {
		t.Fatal("cooldown started another request")
	}
	_, items, _ := s.GetAccountCheckBatch(ctx, repeated.ID)
	if items[0].SkipReason != "cooldown" || items[0].PreviousCheckID == nil {
		t.Fatalf("cooldown=%+v", items)
	}
	summaries, err := s.ListAccountCheckSummaries(ctx)
	if err != nil || summaries[a].LastResult.BatchID != batch.ID || summaries[a].LatestTask.BatchID != repeated.ID || summaries[a].CooldownRemainingSeconds < 1 {
		t.Fatal("skip erased last result")
	}
}

func TestAccountCheckCancelConcurrent(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	a := checkTestAccount(t, s, "a")
	batch := checkTestBatch(t, s, "race", 1, a)
	work := checkTestClaim(t, s)
	var wg sync.WaitGroup
	errs := make(chan error, 2)
	wg.Add(2)
	go func() { defer wg.Done(); _, err := s.CancelAccountCheckBatch(ctx, batch.ID); errs <- err }()
	go func() {
		defer wg.Done()
		_, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "ok"})
		errs <- err
	}()
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
	b, items, err := s.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !((b.State == "completed" && items[0].State == "finished") || (b.State == "stopped" && items[0].State == "canceled")) {
		t.Fatalf("inconsistent final state=%+v %+v", b, items)
	}
	var cipher sql.NullString
	if err = s.db.QueryRow(`SELECT proxy_cipher FROM account_check_batches WHERE id=?`, batch.ID).Scan(&cipher); err != nil || cipher.Valid {
		t.Fatal("terminal state retained secret")
	}
}

func TestAccountCheckAttemptedFinalEvidence(t *testing.T) {
	for _, scenario := range []string{"precheck", "canceled", "deleted"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			id := checkTestAccount(t, s, "attempted")
			batch := checkTestBatch(t, s, "final-evidence", 1, id)
			work := checkTestClaim(t, s)
			if marked, err := s.MarkAccountCheckAttempted(ctx, work.Check.ID); err != nil || !marked {
				t.Fatalf("mark=%v err=%v", marked, err)
			}
			attempted := scenario != "precheck"
			result := AccountCheckResult{Outcome: "ok", RequestAttempted: &attempted}
			switch scenario {
			case "precheck":
				result.Outcome = "access_token_expired"
				result.FailureStage = "precheck"
			case "canceled":
				if _, err := s.CancelAccountCheckBatch(ctx, batch.ID); err != nil {
					t.Fatal(err)
				}
				result.Canceled = true
			case "deleted":
				if err := s.DeleteAccount(ctx, "attempted@example.test"); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.FinishAccountCheck(ctx, work.Check.ID, result); err != nil {
				t.Fatal(err)
			}
			_, items, err := s.GetAccountCheckBatch(ctx, batch.ID)
			if err != nil {
				t.Fatal(err)
			}
			if items[0].RequestAttempted == nil || *items[0].RequestAttempted != attempted {
				t.Fatalf("attempted evidence=%v want %v", items[0].RequestAttempted, attempted)
			}
			if scenario == "precheck" && items[0].FailureStage != "precheck" {
				t.Fatalf("item=%+v", items[0])
			}
			if scenario == "canceled" && items[0].State != "canceled" {
				t.Fatalf("item=%+v", items[0])
			}
			if scenario == "deleted" && items[0].SkipReason != "account_removed" {
				t.Fatalf("item=%+v", items[0])
			}
		})
	}
}
