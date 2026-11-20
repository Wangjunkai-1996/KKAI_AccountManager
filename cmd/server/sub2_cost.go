package main

import (
	"context"
	"errors"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const (
	recentAccountCostDays       = 90
	recentAccountCostCacheLimit = 1024
	recentAccountCostError      = "Sub2 近90天消耗暂不可用"
)

type recentAccountCost struct {
	Days              int      `json:"days"`
	TotalCost         *float64 `json:"total_cost,omitempty"`
	TotalStandardCost *float64 `json:"total_standard_cost,omitempty"`
	TotalRequests     *int64   `json:"total_requests,omitempty"`
	TotalTokens       *int64   `json:"total_tokens,omitempty"`
	CheckedAt         string   `json:"checked_at,omitempty"`
	Stale             bool     `json:"stale,omitempty"`
	Error             string   `json:"error,omitempty"`
	Unknown           bool     `json:"unknown,omitempty"`
}

type sub2CostCacheEntry struct {
	stats     recentAccountCost
	expiresAt time.Time
}

type recentAccountCostsResponse struct {
	Success        bool                        `json:"success"`
	Stats          map[int64]recentAccountCost `json:"stats"`
	Sub2Configured bool                        `json:"sub2_configured"`
}

func parseRecentAccountCostIDs(query url.Values) ([]int64, error) {
	values := query["account_ids"]
	if len(values) != 1 || values[0] == "" {
		return nil, errors.New("请提供账号 ID")
	}
	parts := strings.Split(values[0], ",")
	if len(parts) > 100 {
		return nil, errors.New("每次最多查询 100 个账号")
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, part := range parts {
		for _, ch := range part {
			if ch < '0' || ch > '9' {
				return nil, errors.New("账号 ID 必须是正整数")
			}
		}
		id, err := strconv.ParseInt(part, 10, 64)
		if err != nil || id <= 0 {
			return nil, errors.New("账号 ID 必须是正整数")
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	return ids, nil
}

// Cost reads are independent of history refreshes and never modify the remote
// account. Confirmed missing bindings never issue further remote requests.
func (s *sub2ImportService) handleRecentAccountCosts(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
		return
	}
	ids, err := parseRecentAccountCostIDs(r.URL.Query())
	if err != nil {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: err.Error()})
		return
	}
	response := recentAccountCostsResponse{
		Success: true, Stats: make(map[int64]recentAccountCost, len(ids)), Sub2Configured: s.configured(),
	}
	for _, id := range ids {
		response.Stats[id] = recentAccountCost{Days: recentAccountCostDays, Unknown: true}
	}
	if !response.Sub2Configured {
		respondJSON(w, response)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 9*time.Second)
	defer cancel()
	jobs := make(chan int64, len(ids))
	for _, id := range ids {
		jobs <- id
	}
	close(jobs)
	var wg sync.WaitGroup
	var resultMu sync.Mutex
	for worker := 0; worker < min(4, len(ids)); worker++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for id := range jobs {
				stats := recentAccountCost{Days: recentAccountCostDays, Unknown: true}
				remoteID, err := s.recentAccountCostRemoteID(ctx, id)
				if err != nil {
					stats.Error = recentAccountCostError
				} else if remoteID > 0 {
					stats = s.recentAccountCost(ctx, remoteID)
				}
				resultMu.Lock()
				response.Stats[id] = stats
				resultMu.Unlock()
			}
		}()
	}
	wg.Wait()
	respondJSON(w, response)
}

func (s *sub2ImportService) recentAccountCostRemoteID(ctx context.Context, accountID int64) (int64, error) {
	binding, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID)
	if err == nil {
		if binding.DestinationKey == s.destinationKey && (binding.State == "imported" || binding.State == "cleanup_pending") && binding.Sub2AccountID > 0 {
			return binding.Sub2AccountID, nil
		}
		// An active import or unsupported binding must not reuse an old identity.
		return 0, nil
	}
	if !errors.Is(err, store.ErrSub2ImportNotFound) {
		return 0, err
	}
	return 0, nil
}

func (s *sub2ImportService) recentAccountCost(ctx context.Context, remoteID int64) recentAccountCost {
	s.costMu.Lock()
	if entry, ok := s.costCache[remoteID]; ok && time.Now().Before(entry.expiresAt) {
		s.costMu.Unlock()
		return entry.stats
	}
	if s.costSlots == nil {
		s.costSlots = make(chan struct{}, 4)
	}
	slots := s.costSlots
	s.costMu.Unlock()
	select {
	case slots <- struct{}{}:
		defer func() { <-slots }()
	case <-ctx.Done():
		return s.cacheRecentAccountCost(remoteID, recentAccountCost{}, false)
	}
	// Another request may have filled the cache while this one waited for a slot.
	s.costMu.Lock()
	entry, found := s.costCache[remoteID]
	s.costMu.Unlock()
	if found && time.Now().Before(entry.expiresAt) {
		return entry.stats
	}
	var payload struct {
		Summary *recentAccountCost `json:"summary"`
	}
	err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(remoteID, 10)+"/stats", url.Values{"days": {"90"}}, &payload)
	valid := err == nil && validRecentAccountCost(payload.Summary)
	stats := recentAccountCost{}
	if valid {
		stats = *payload.Summary
		stats.Days = recentAccountCostDays
		stats.CheckedAt = time.Now().UTC().Format(time.RFC3339)
		stats.Stale, stats.Unknown, stats.Error = false, false, ""
	}
	return s.cacheRecentAccountCost(remoteID, stats, valid)
}

func validRecentAccountCost(stats *recentAccountCost) bool {
	if stats == nil || stats.TotalCost == nil || stats.TotalStandardCost == nil || stats.TotalRequests == nil || stats.TotalTokens == nil {
		return false
	}
	for _, amount := range []float64{*stats.TotalCost, *stats.TotalStandardCost} {
		if math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
			return false
		}
	}
	return *stats.TotalRequests >= 0 && *stats.TotalTokens >= 0
}

func (s *sub2ImportService) cacheRecentAccountCost(remoteID int64, stats recentAccountCost, valid bool) recentAccountCost {
	s.costMu.Lock()
	defer s.costMu.Unlock()
	if s.costCache == nil {
		s.costCache = make(map[int64]sub2CostCacheEntry)
	}
	ttl := 5 * time.Minute
	if !valid {
		ttl = 30 * time.Second
		stats = recentAccountCost{Days: recentAccountCostDays, Unknown: true, Error: recentAccountCostError}
		if previous := s.costCache[remoteID].stats; previous.TotalCost != nil {
			stats = previous
			stats.Stale, stats.Error = true, recentAccountCostError
		}
	}
	if _, exists := s.costCache[remoteID]; !exists && len(s.costCache) >= recentAccountCostCacheLimit {
		var oldestID int64
		var oldest time.Time
		for id, entry := range s.costCache {
			if oldest.IsZero() || entry.expiresAt.Before(oldest) {
				oldestID, oldest = id, entry.expiresAt
			}
		}
		delete(s.costCache, oldestID)
	}
	s.costCache[remoteID] = sub2CostCacheEntry{stats: stats, expiresAt: time.Now().Add(ttl)}
	return stats
}
