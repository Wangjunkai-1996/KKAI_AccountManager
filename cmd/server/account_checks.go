package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/probe"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

var accountChecker *accountCheckService

type accountCheckRunning struct {
	batchID string
	cancel  context.CancelFunc
}

// The database owns ordering, concurrency and terminal states. This service
// only owns the two live HTTP requests and their cancellation functions.
type accountCheckService struct {
	store         *store.Store
	proxy         string
	upstreamProxy string
	runProbe      func(context.Context, string, string, string) probe.Result
	ctx           context.Context
	cancel        context.CancelFunc
	wg            sync.WaitGroup
	wake          chan struct{}
	paused        atomic.Bool
	mu            sync.Mutex
	running       map[int64]accountCheckRunning
}

func newAccountCheckService(history *store.Store, proxy, upstream string) *accountCheckService {
	ctx, cancel := context.WithCancel(context.Background())
	return &accountCheckService{
		store: history, proxy: strings.TrimSpace(proxy), upstreamProxy: strings.TrimSpace(upstream),
		ctx: ctx, cancel: cancel, wake: make(chan struct{}, 2), running: make(map[int64]accountCheckRunning),
		runProbe: func(ctx context.Context, proxy, token, accountID string) probe.Result {
			client, err := probe.NewClient(proxy)
			if err != nil {
				return probe.Result{Outcome: "proxy_error", ErrorCode: "invalid_proxy", Message: "检测代理配置无效", FailureStage: "precheck"}
			}
			defer client.Close()
			return client.Check(ctx, token, accountID)
		},
	}
}

func (s *accountCheckService) Start() {
	if s == nil || s.store == nil {
		return
	}
	for i := 0; i < 2; i++ {
		s.wg.Add(1)
		go s.worker()
	}
}

func (s *accountCheckService) Stop() {
	s.cancel()
	s.wg.Wait()
}

func (s *accountCheckService) notify() {
	for i := 0; i < 2; i++ {
		select {
		case s.wake <- struct{}{}:
		default:
		}
	}
}

func (s *accountCheckService) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for s.ctx.Err() == nil && !s.paused.Load() {
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		work, err := s.store.ClaimAccountCheck(ctx)
		cancel()
		if err != nil {
			if s.ctx.Err() == nil {
				s.pause()
			}
			return
		}
		if work != nil {
			s.process(work)
			continue
		}
		select {
		case <-s.ctx.Done():
			return
		case <-s.wake:
		case <-ticker.C:
		}
	}
}

func (s *accountCheckService) pause() {
	if !s.paused.Swap(true) {
		// Raw database/network errors can contain private material.
		log.Print("账号检测存储故障：已暂停派发，需恢复存储后重启检测服务")
	}
	s.cancelBatch("")
}

func (s *accountCheckService) cancelBatch(batchID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, run := range s.running {
		if batchID == "" || run.batchID == batchID {
			run.cancel()
		}
	}
}

func (s *accountCheckService) process(work *store.AccountCheckWork) {
	ctx, cancel := context.WithTimeout(s.ctx, 20*time.Second)
	accountID := int64(0)
	if work.Check.AccountID != nil {
		accountID = *work.Check.AccountID
	}
	s.mu.Lock()
	s.running[work.Check.ID] = accountCheckRunning{work.Batch.ID, cancel}
	if s.paused.Load() {
		cancel()
	}
	s.mu.Unlock()
	defer func() {
		cancel()
		s.mu.Lock()
		delete(s.running, work.Check.ID)
		s.mu.Unlock()
	}()

	// Recheck after registration: cancellation may have committed just before
	// this worker registered its cancel function.
	readCtx, readCancel := context.WithTimeout(context.Background(), 5*time.Second)
	batch, _, err := s.store.GetAccountCheckBatch(readCtx, work.Batch.ID)
	readCancel()
	if err != nil {
		s.pause()
		return
	}
	if batch.State != "active" || ctx.Err() != nil {
		s.finish(work.Check.ID, accountID, store.AccountCheckResult{Canceled: true})
		return
	}
	if probe.AccessTokenExpired(work.AccessToken, time.Now()) {
		s.finish(work.Check.ID, accountID, store.AccountCheckResult{Outcome: "access_token_expired", ErrorCode: "access_token_expired", Message: "本地访问令牌已过期，请重新登录后检测", FailureStage: "precheck"})
		return
	}
	if strings.ContainsAny(work.AccessToken+work.ChatGPTAccountID, "\r\n") {
		s.finish(work.Check.ID, accountID, store.AccountCheckResult{Outcome: "credential_incomplete", ErrorCode: "credential_incomplete", Message: "本地凭据格式不完整，请重新登录", FailureStage: "precheck"})
		return
	}
	markCtx, markCancel := context.WithTimeout(ctx, 5*time.Second)
	allowed, err := s.store.MarkAccountCheckAttempted(markCtx, work.Check.ID)
	markCancel()
	notAttempted := false
	if err != nil {
		if ctx.Err() != nil {
			s.finish(work.Check.ID, accountID, store.AccountCheckResult{Canceled: errors.Is(ctx.Err(), context.Canceled), Outcome: "timeout", Message: "检测等待超时", FailureStage: "precheck", RequestAttempted: &notAttempted})
		} else {
			s.pause()
		}
		return
	}
	if !allowed || ctx.Err() != nil {
		s.finish(work.Check.ID, accountID, store.AccountCheckResult{Canceled: !allowed || errors.Is(ctx.Err(), context.Canceled), Outcome: "timeout", Message: "检测等待超时", FailureStage: "precheck", RequestAttempted: &notAttempted})
		return
	}
	result := s.runProbe(ctx, work.Proxy, work.AccessToken, work.ChatGPTAccountID)
	s.finish(work.Check.ID, accountID, store.AccountCheckResult{
		Outcome: result.Outcome, HTTPStatus: optionalCheckInt(result.HTTPStatus),
		ProxyHTTPStatus: optionalCheckInt(result.ProxyHTTPStatus), StreamErrorStatus: optionalCheckInt(result.StreamErrorStatus),
		ErrorCode: result.ErrorCode, StreamErrorCode: result.StreamErrorCode, Message: result.Message,
		FailureStage: result.FailureStage, RetryAfterSeconds: optionalCheckInt(result.RetryAfterSeconds),
		DurationMS: result.DurationMS, Canceled: result.Outcome == "canceled",
		RequestAttempted: &result.RequestAttempted,
	})
}

func optionalCheckInt(value int) *int {
	if value == 0 {
		return nil
	}
	return &value
}

func (s *accountCheckService) finish(id, accountID int64, result store.AccountCheckResult) {
	// A canceled request context must never prevent its durable final state.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	batch, err := s.store.FinishAccountCheck(ctx, id, result)
	if err != nil {
		s.pause()
		return
	}
	if batch.State == "stopping" {
		s.cancelBatch(batch.ID)
	}
	if !result.Canceled && batch.State != "stopping" && batch.State != "stopped" && accountID > 0 && accountRecoveryService != nil && accountRecoveryService.autoRecovery.Load() && recoveryCheckCandidate(result) {
		accountRecoveryService.enqueueAutomatic(accountID)
	}
	s.notify()
}

func recoveryCheckCandidate(result store.AccountCheckResult) bool {
	if result.HTTPStatus != nil && *result.HTTPStatus == http.StatusUnauthorized {
		return true
	}
	for _, value := range []string{result.Outcome, result.ErrorCode, result.StreamErrorCode} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "credential_missing", "credential_incomplete", "access_token_expired", "token_expired", "token_revoked", "credential_revoked", "unauthorized", "auth_failed", "authentication_failed":
			return true
		}
	}
	return false
}

type accountCheckRequest struct {
	RequestKey  string  `json:"request_key"`
	AccountIDs  []int64 `json:"account_ids"`
	Concurrency int     `json:"concurrency"`
	ProxyMode   string  `json:"proxy_mode"`
}

func (s *accountCheckService) capabilities() map[string]any {
	configured, supported := s.proxy != "", s.upstreamProxy == ""
	if configured && supported {
		client, err := probe.NewClient(s.proxy)
		supported = err == nil
		if client != nil {
			client.Close()
		}
	}
	return map[string]any{"model": probe.Model, "max_batch_size": 100, "max_concurrency": 2,
		"default_proxy_configured": configured, "default_proxy_supported": supported,
		"available": s.store != nil && !s.paused.Load() && s.ctx.Err() == nil}
}

func (s *accountCheckService) handleCollection(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		checkAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	if s == nil || s.store == nil {
		checkAPIError(w, http.StatusServiceUnavailable, "checks_unavailable", "账号检测服务不可用")
		return
	}
	if r.Method == http.MethodGet {
		activeOnly := r.URL.Query().Get("active") == "1"
		limit := 20
		if raw := r.URL.Query().Get("limit"); raw != "" {
			value, err := strconv.Atoi(raw)
			if err != nil || value < 1 || value > 20 {
				checkAPIError(w, 400, "invalid_limit", "limit 必须在 1 到 20 之间")
				return
			}
			limit = value
		}
		batches, err := s.store.ListAccountCheckBatches(r.Context(), activeOnly, limit)
		if err != nil {
			s.respondError(w, r, err)
			return
		}
		if activeOnly {
			var active *store.AccountCheckBatch
			if len(batches) != 0 {
				active = &batches[0]
			}
			respondJSON(w, map[string]any{"success": true, "active": active, "capabilities": s.capabilities()})
		} else {
			respondJSON(w, map[string]any{"success": true, "data": batches})
		}
		return
	}
	if s.paused.Load() || s.ctx.Err() != nil {
		checkAPIError(w, 503, "checks_unavailable", "检测已暂停，请恢复服务后重试")
		return
	}
	var req accountCheckRequest
	if !decodeCheckJSON(w, r, &req) {
		return
	}
	input := store.AccountCheckInput{RequestKey: req.RequestKey, AccountIDs: req.AccountIDs, Concurrency: req.Concurrency, ProxyMode: req.ProxyMode}
	if previous, found, err := s.store.LookupAccountCheckBatch(r.Context(), input); err != nil {
		s.respondError(w, r, err)
		return
	} else if found {
		writeCheckCreated(w, previous, true)
		return
	}
	effectiveProxy := ""
	switch req.ProxyMode {
	case "default":
		if s.proxy == "" {
			checkAPIError(w, 422, "proxy_not_configured", "服务器默认代理未配置，请明确选择直连或配置代理")
			return
		}
		if s.upstreamProxy != "" {
			checkAPIError(w, 422, "unsupported_proxy_chain", "检测暂不支持服务器的前置代理配置")
			return
		}
		client, err := probe.NewClient(s.proxy)
		if err != nil {
			checkAPIError(w, 422, "invalid_proxy", "服务器默认代理配置无效")
			return
		}
		client.Close()
		effectiveProxy = s.proxy
	case "direct":
	default:
		checkAPIError(w, 400, "invalid_proxy_mode", "请选择服务器默认代理或 sys1 IPv4 直连")
		return
	}
	input.Proxy = effectiveProxy
	batch, reused, err := s.store.CreateAccountCheckBatch(r.Context(), input)
	if err != nil {
		s.respondError(w, r, err)
		return
	}
	s.notify()
	writeCheckCreated(w, batch, reused)
}

func writeCheckCreated(w http.ResponseWriter, batch store.AccountCheckBatch, reused bool) {
	status := http.StatusAccepted
	if reused {
		status = http.StatusOK
	}
	respondJSONStatus(w, status, struct {
		Success bool   `json:"success"`
		Reused  bool   `json:"reused"`
		BatchID string `json:"batch_id"`
		Total   int    `json:"total"`
		store.AccountCheckBatch
	}{true, reused, batch.ID, batch.Counts.Total, batch})
}

func (s *accountCheckService) handleAction(w http.ResponseWriter, r *http.Request) {
	if s == nil || s.store == nil {
		checkAPIError(w, 503, "checks_unavailable", "账号检测服务不可用")
		return
	}
	parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/api/account-checks/"), "/")
	if len(parts) > 2 || len(parts[0]) != 32 {
		checkAPIError(w, 404, "batch_not_found", "检测批次不存在")
		return
	}
	id := parts[0]
	if len(parts) == 1 && r.Method == http.MethodGet {
		batch, items, err := s.store.GetAccountCheckBatch(r.Context(), id)
		if err != nil {
			s.respondError(w, r, err)
			return
		}
		respondJSON(w, map[string]any{"success": true, "batch": batch, "items": items})
		return
	}
	if len(parts) == 2 && parts[1] == "cancel" && r.Method == http.MethodPost {
		var body struct{}
		if !decodeCheckJSON(w, r, &body) {
			return
		}
		batch, err := s.store.CancelAccountCheckBatch(r.Context(), id)
		if err != nil {
			s.respondError(w, r, err)
			return
		}
		s.cancelBatch(id)
		s.notify()
		respondJSON(w, map[string]any{"success": true, "batch": batch})
		return
	}
	checkAPIError(w, 405, "method_not_allowed", "请求方法不支持")
}

func decodeCheckJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		checkAPIError(w, 415, "invalid_content_type", "请求必须使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	err = decoder.Decode(target)
	if err == nil {
		if trailing := decoder.Decode(new(any)); trailing != io.EOF {
			err = trailing
			if err == nil {
				err = errors.New("trailing JSON")
			}
		}
	}
	if err != nil {
		status := 400
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			status = 413
		}
		checkAPIError(w, status, "invalid_request", "请求格式错误或包含不支持的字段")
		return false
	}
	return true
}

func checkAPIError(w http.ResponseWriter, status int, code, message string) {
	respondJSONStatus(w, status, map[string]any{"success": false, "code": code, "message": message})
}

func (s *accountCheckService) respondError(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		// A browser disconnect/timeout is not a storage failure and must not
		// disable the independent background workers.
		checkAPIError(w, 408, "request_canceled", "查询或提交已中断，请恢复页面查看任务")
		return
	}
	var safe *store.AccountCheckError
	if errors.As(err, &safe) {
		status := 400
		switch safe.Code {
		case "batch_not_found":
			status = 404
		case "batch_active", "idempotency_conflict":
			status = 409
		case "ineligible_accounts", "proxy_not_configured":
			status = 422
		}
		respondJSONStatus(w, status, struct {
			Success bool `json:"success"`
			*store.AccountCheckError
		}{false, safe})
		return
	}
	s.pause()
	checkAPIError(w, 503, "checks_storage_error", "检测记录暂不可用，请恢复服务后重试")
}
