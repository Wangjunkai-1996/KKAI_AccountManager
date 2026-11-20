package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const recentCostFixture = `{"code":0,"data":{"summary":{"days":90,"total_cost":12.34,"total_standard_cost":23.45,"total_requests":12,"total_tokens":567}}}`

func recentAccountCostRequest(t *testing.T, service *sub2ImportService, method, query string) (int, recentAccountCostsResponse) {
	t.Helper()
	w := httptest.NewRecorder()
	service.handleRecentAccountCosts(w, httptest.NewRequest(method, "/api/history/usage"+query, nil))
	var response recentAccountCostsResponse
	if w.Code == http.StatusOK {
		if err := json.Unmarshal(w.Body.Bytes(), &response); err != nil {
			t.Fatal(err)
		}
	}
	return w.Code, response
}

func TestRecentAccountCostBindingCacheAndMissingRemoteDetail(t *testing.T) {
	history, id := testOAuthStore(t)
	if _, err := history.LinkSub2Account(context.Background(), "current", id, 42, "linked-cost"); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != http.MethodGet || r.URL.Path != "/admin/accounts/42/stats" || r.URL.RawQuery != "days=90" || r.Header.Get("x-api-key") != "fixture-key" {
			t.Errorf("unexpected stats request: %s %s", r.Method, r.URL)
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte(recentCostFixture))
	}))
	defer remote.Close()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "fixture-key", destinationKey: "current", client: remote.Client()}
	query := fmt.Sprintf("?account_ids=%d,%d", id, id)
	for i := 0; i < 2; i++ {
		code, response := recentAccountCostRequest(t, service, http.MethodGet, query)
		stats := response.Stats[id]
		if code != 200 || !response.Success || !response.Sub2Configured || len(response.Stats) != 1 || stats.Days != 90 || stats.TotalCost == nil || *stats.TotalCost != 12.34 || stats.TotalStandardCost == nil || *stats.TotalStandardCost != 23.45 || stats.TotalRequests == nil || *stats.TotalRequests != 12 || stats.TotalTokens == nil || *stats.TotalTokens != 567 || stats.Unknown || stats.Stale || stats.Error != "" {
			t.Fatalf("response = %+v, stats = %+v, status = %d", response, stats, code)
		}
		if _, err := time.Parse(time.RFC3339, stats.CheckedAt); err != nil {
			t.Fatalf("invalid checked_at: %q", stats.CheckedAt)
		}
	}
	if calls.Load() != 1 {
		t.Fatalf("cached stats fetched %d times", calls.Load())
	}
}

func TestRecentAccountCostMissingZeroMalformedAndFailures(t *testing.T) {
	history, _ := testOAuthStore(t)
	for _, tc := range []struct {
		name  string
		body  string
		code  int
		valid bool
	}{
		{"zero", `{"code":0,"data":{"summary":{"total_cost":0,"total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, true},
		{"missing cost", `{"code":0,"data":{"summary":{"total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"null cost", `{"code":0,"data":{"summary":{"total_cost":null,"total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"negative cost", `{"code":0,"data":{"summary":{"total_cost":-1,"total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"string cost", `{"code":0,"data":{"summary":{"total_cost":"12","total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"missing standard", `{"code":0,"data":{"summary":{"total_cost":12,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"missing summary", `{"code":0,"data":{}}`, 200, false},
		{"overflow", `{"code":0,"data":{"summary":{"total_cost":1e999,"total_standard_cost":0,"total_requests":0,"total_tokens":0}}}`, 200, false},
		{"remote not found", `{"message":"secret diagnostic"}`, 404, false},
		{"remote unavailable", `{"message":"secret diagnostic"}`, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var calls int
			remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(tc.code)
				_, _ = w.Write([]byte(tc.body))
			}))
			defer remote.Close()
			service := &sub2ImportService{store: history, baseURL: remote.URL, client: remote.Client()}
			for i := 0; i < 2; i++ {
				stats := service.recentAccountCost(context.Background(), 42)
				if tc.valid {
					if stats.Unknown || stats.TotalCost == nil || *stats.TotalCost != 0 || stats.CheckedAt == "" {
						t.Fatalf("zero must be retained: %+v", stats)
					}
				} else if !stats.Unknown || stats.TotalCost != nil || stats.CheckedAt != "" || stats.Error != recentAccountCostError {
					t.Fatalf("failure must not become a zero or leak upstream message: %+v", stats)
				}
			}
			if calls != 1 {
				t.Fatalf("cache missed: %d calls", calls)
			}
		})
	}
}

func TestRecentAccountCostStaleAndBoundedCache(t *testing.T) {
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(recentCostFixture))
	}))
	defer remote.Close()
	service := &sub2ImportService{baseURL: remote.URL, client: remote.Client()}
	first := service.recentAccountCost(context.Background(), 42)
	service.costCache[42] = sub2CostCacheEntry{stats: first, expiresAt: time.Now().Add(-time.Second)}
	for i := 0; i < 2; i++ {
		stale := service.recentAccountCost(context.Background(), 42)
		if !stale.Stale || stale.Unknown || stale.TotalCost == nil || *stale.TotalCost != 12.34 || stale.CheckedAt != first.CheckedAt || stale.Error != recentAccountCostError {
			t.Fatalf("failed refresh did not preserve the previous dated value: %+v", stale)
		}
	}
	if calls.Load() != 2 {
		t.Fatalf("failed refresh wasn't cached: %d calls", calls.Load())
	}
	for id := int64(1); id <= recentAccountCostCacheLimit+5; id++ {
		service.cacheRecentAccountCost(id, first, true)
	}
	if len(service.costCache) != recentAccountCostCacheLimit {
		t.Fatalf("unbounded cache: %d", len(service.costCache))
	}
}

func TestRecentAccountCostInputAndDestinationGuards(t *testing.T) {
	var unconfigured *sub2ImportService
	for _, tc := range []struct {
		method, query string
		want          int
	}{
		{http.MethodPost, "?account_ids=1", 405},
		{http.MethodGet, "", 400},
		{http.MethodGet, "?account_ids=", 400},
		{http.MethodGet, "?account_ids=0", 400},
		{http.MethodGet, "?account_ids=-1", 400},
		{http.MethodGet, "?account_ids=%2B1", 400},
		{http.MethodGet, "?account_ids=1,,2", 400},
		{http.MethodGet, "?account_ids=1.2", 400},
		{http.MethodGet, "?account_ids=9223372036854775808", 400},
		{http.MethodGet, "?account_ids=1&account_ids=2", 400},
		{http.MethodGet, "?account_ids=" + strings.Repeat("1,", 100) + "1", 400},
	} {
		code, _ := recentAccountCostRequest(t, unconfigured, tc.method, tc.query)
		if code != tc.want {
			t.Errorf("%s %s: status %d, want %d", tc.method, tc.query, code, tc.want)
		}
	}
	code, response := recentAccountCostRequest(t, unconfigured, http.MethodGet, "?account_ids=1")
	if code != 200 || response.Sub2Configured || !response.Stats[1].Unknown {
		t.Fatalf("unconfigured response = %+v, status = %d", response, code)
	}
	history, id := testOAuthStore(t)
	if _, err := history.LinkSub2Account(context.Background(), "other", id, 42, "linked-other"); err != nil {
		t.Fatal(err)
	}
	if _, _, err := history.CreateOrGetSub2Import(context.Background(), "queued", id, "queued", "queued", []byte(`{}`)); err != nil {
		t.Fatal(err)
	}
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("wrong destination or queued account reached Sub2")
		w.WriteHeader(500)
	}))
	defer remote.Close()
	for _, destination := range []string{"current", "queued"} {
		service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "fixture-key", destinationKey: destination, client: remote.Client()}
		code, response := recentAccountCostRequest(t, service, http.MethodGet, "?account_ids="+strconv.FormatInt(id, 10))
		if code != 200 || !response.Stats[id].Unknown || response.Stats[id].TotalCost != nil {
			t.Fatalf("destination %s: %+v", destination, response)
		}
	}
}

func TestRecentAccountCostDeletedBindingStopsRequests(t *testing.T) {
	history, id := testOAuthStore(t)
	const destination = "https://sub2.example.test:8443/api/v1#instance"
	_, err := history.RecordLifecycleEvent(context.Background(), store.LifecycleEvent{AccountID: id, Email: "sub2@example.com", Source: "sub2", Status: "deleted", Code: "not_found", HTTPStatus: 404, DedupeKey: "sub2:" + destination + ":42:not_found"})
	if err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { calls.Add(1); w.WriteHeader(500) }))
	defer remote.Close()
	service := &sub2ImportService{store: history, baseURL: remote.URL, adminAPIKey: "key", destinationKey: destination, client: remote.Client()}
	_, response := recentAccountCostRequest(t, service, http.MethodGet, "?account_ids="+strconv.FormatInt(id, 10))
	if !response.Stats[id].Unknown || response.Stats[id].TotalCost != nil || calls.Load() != 0 {
		t.Fatalf("missing account queried stats: %+v calls=%d", response, calls.Load())
	}
}

func TestRecentAccountCostConcurrencyAndCancellation(t *testing.T) {
	var active, peak atomic.Int32
	started := make(chan struct{}, 8)
	release := make(chan struct{})
	remote := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := active.Add(1)
		defer active.Add(-1)
		for previous := peak.Load(); previous < n && !peak.CompareAndSwap(previous, n); previous = peak.Load() {
		}
		started <- struct{}{}
		<-release
		_, _ = w.Write([]byte(recentCostFixture))
	}))
	defer remote.Close()
	service := &sub2ImportService{baseURL: remote.URL, client: remote.Client()}
	var wg sync.WaitGroup
	for id := int64(1); id <= 8; id++ {
		wg.Add(1)
		go func(id int64) { defer wg.Done(); service.recentAccountCost(context.Background(), id) }(id)
	}
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(release)
			t.Fatal("stats requests did not start")
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	failed := service.recentAccountCost(ctx, 99)
	if !failed.Unknown || failed.TotalCost != nil {
		t.Errorf("canceled slot wait became valid: %+v", failed)
	}
	close(release)
	wg.Wait()
	if peak.Load() > 4 {
		t.Fatalf("stats concurrency exceeded four: %d", peak.Load())
	}
}
