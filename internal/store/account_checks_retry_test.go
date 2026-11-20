package store

import (
	"context"
	"testing"
	"time"
)

func TestAccountCheckRetryAfterSharedCooldown(t *testing.T) {
	for _, tc := range []struct {
		name       string
		retryAfter int
		age        time.Duration
		want       int
	}{
		{"rate_limit", 3600, time.Minute, 3540},
		{"minimum", 1, 10 * time.Second, 20},
		{"expired", 60, 61 * time.Second, 0},
		{"negative", -1, 31 * time.Second, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			accountID := checkTestAccount(t, s, "retry-after")
			checkTestBatch(t, s, "first", 1, accountID)
			work := checkTestClaim(t, s)
			status := 429
			if _, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "rate_limited", HTTPStatus: &status, RetryAfterSeconds: &tc.retryAfter}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.Exec(`UPDATE account_checks SET finished_at=? WHERE id=?`, time.Now().Add(-tc.age).UnixMilli(), work.Check.ID); err != nil {
				t.Fatal(err)
			}
			summaries, err := s.ListAccountCheckSummaries(ctx)
			if err != nil || summaries[accountID].CooldownRemainingSeconds < tc.want-1 || summaries[accountID].CooldownRemainingSeconds > tc.want {
				t.Fatalf("summary=%+v err=%v want=%d", summaries[accountID], err, tc.want)
			}
			batch := checkTestBatch(t, s, "repeat", 1, accountID)
			next, err := s.ClaimAccountCheck(ctx)
			if err != nil || (next == nil) != (tc.want > 0) {
				t.Fatalf("claim bypassed displayed cooldown: next=%+v err=%v", next, err)
			}
			if tc.want > 0 {
				_, items, err := s.GetAccountCheckBatch(ctx, batch.ID)
				if err != nil || len(items) != 1 || items[0].SkipReason != "cooldown" || items[0].PreviousCheckID == nil || *items[0].PreviousCheckID != work.Check.ID {
					t.Fatalf("cooldown skip=%+v err=%v", items, err)
				}
				checkTestLogin(t, s, "retry-after@example.test")
				summaries, err = s.ListAccountCheckSummaries(ctx)
				if err != nil || summaries[accountID].CooldownRemainingSeconds != 0 {
					t.Fatalf("old credential retained cooldown: %+v %v", summaries[accountID], err)
				}
				checkTestBatch(t, s, "fresh-credential", 1, accountID)
				if next, err := s.ClaimAccountCheck(ctx); err != nil || next == nil {
					t.Fatalf("old credential blocked fresh check: %+v %v", next, err)
				}
			}
		})
	}
}

func TestAccountCheckRetryAfterAcrossDestinations(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	accountID := checkTestAccount(t, s, "shared-retry")
	if _, err := s.LinkSub2Account(ctx, "beta", accountID, 999, "linked-beta"); err != nil {
		t.Fatal(err)
	}
	_, _, err := s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: "first-target", AccountIDs: []int64{accountID}, Concurrency: 1, ProxyMode: "direct", DestinationKey: "test"})
	if err != nil {
		t.Fatal(err)
	}
	work := checkTestClaim(t, s)
	retryAfter := 3600
	if _, err := s.FinishAccountCheck(ctx, work.Check.ID, AccountCheckResult{Outcome: "rate_limited", RetryAfterSeconds: &retryAfter}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE account_checks SET finished_at=? WHERE id=?`, time.Now().Add(-time.Minute).UnixMilli(), work.Check.ID); err != nil {
		t.Fatal(err)
	}
	summaries, err := s.ListAccountCheckSummaries(ctx, "beta")
	if err != nil {
		t.Fatal(err)
	}
	summary := summaries[accountID]
	if summary.CooldownRemainingSeconds < 3539 || summary.CooldownRemainingSeconds > 3540 || summary.LastResult != nil || summary.LatestTask != nil {
		t.Fatalf("global cooldown missing or another target's results leaked: %+v", summary)
	}
	batch, _, err := s.CreateAccountCheckBatch(ctx, AccountCheckInput{RequestKey: "second-target", AccountIDs: []int64{accountID}, Concurrency: 1, ProxyMode: "direct", DestinationKey: "beta"})
	if err != nil {
		t.Fatal(err)
	}
	if work, err := s.ClaimAccountCheck(ctx); err != nil || work != nil {
		t.Fatalf("target switch bypassed global cooldown: %+v %v", work, err)
	}
	_, items, err := s.GetAccountCheckBatch(ctx, batch.ID)
	if err != nil || len(items) != 1 || items[0].SkipReason != "cooldown" {
		t.Fatalf("claim did not match the displayed cooldown: %+v %v", items, err)
	}
	checkTestLogin(t, s, "shared-retry@example.test")
	summaries, err = s.ListAccountCheckSummaries(ctx, "beta")
	if err != nil || summaries[accountID].CooldownRemainingSeconds != 0 || summaries[accountID].LastResult != nil {
		t.Fatalf("old credentials retained cooldown or foreign result: %+v %v", summaries[accountID], err)
	}
}
