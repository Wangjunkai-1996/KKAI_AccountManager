package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/probe"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const sub2MonitorInterval = time.Minute

type sub2MonitorState struct {
	LastScanAt       *time.Time `json:"last_scan_at"`
	NextScanAt       *time.Time `json:"next_scan_at"`
	Scanning         bool       `json:"scanning"`
	LastError        string     `json:"last_error"`
	AutoCheckBatchID string     `json:"auto_check_batch_id"`
	Summary          string     `json:"summary"`
}

func (s *sub2RecoveryService) recoverySettings() any {
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	state := s.monitorState
	enabled := s.autoRecovery.Load()
	blockedReason := ""
	switch {
	case s.paused.Load():
		blockedReason = "恢复任务存储故障，已停止派发；修复存储后需重启服务"
	case s.ctx.Err() != nil:
		blockedReason = "恢复服务已停止"
	case !s.configured():
		blockedReason = "Sub2 恢复服务未配置"
	}
	running := enabled && blockedReason == ""
	if !running {
		state.NextScanAt = nil
		state.Scanning = false
	}
	return struct {
		Success             bool   `json:"success"`
		Enabled             bool   `json:"enabled"`
		AutoRecoveryEnabled bool   `json:"auto_recovery_enabled"`
		Running             bool   `json:"running"`
		BlockedReason       string `json:"blocked_reason"`
		IntervalSeconds     int    `json:"interval_seconds"`
		sub2MonitorState
	}{true, enabled, enabled, running, blockedReason, int(sub2MonitorInterval.Seconds()), state}
}

func (s *sub2RecoveryService) notifyMonitor() {
	select {
	case s.monitorWake <- struct{}{}:
	default:
	}
}

func (s *sub2RecoveryService) saveRecoverySetting(ctx context.Context, enabled bool) error {
	// Serialize concurrent settings writes so the database and running monitor
	// always agree about the user's last saved choice.
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	if err := s.store.SetAutoRecoveryEnabled(ctx, enabled); err != nil {
		return err
	}
	s.autoRecovery.Store(enabled)
	return nil
}

func (s *sub2RecoveryService) monitor() {
	defer s.wg.Done()
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-s.ctx.Done():
			return
		case <-timer.C:
		case <-s.monitorWake:
		}
		if !s.paused.Load() {
			s.retryDueRecoveries()
			if s.autoRecovery.Load() {
				s.scanSub2Accounts()
			}
		}
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		timer.Reset(sub2MonitorInterval)
	}
}

func recoveryAccountDisabled(detail map[string]any) bool {
	status, _ := detail["status"].(string)
	return !strings.EqualFold(status, "active") && !strings.EqualFold(status, "error")
}

func sub2RecoverySuspect(status sub2AccountStatus) bool {
	// Sub2 active + schedulable=false is an operator pause. Neither a local
	// expired token nor that pause is a reason to turn scheduling back on.
	return status.Imported && status.Exists && !status.Unknown && !status.Stale && strings.EqualFold(status.Status, "error") && status.Schedulable != nil
}

func sub2RecoveryPoolCandidate(status sub2AccountStatus) bool {
	if !status.Imported || !status.Exists || status.Unknown || status.Stale || status.Schedulable == nil {
		return false
	}
	if strings.EqualFold(strings.TrimSpace(status.Status), "error") {
		return true
	}
	// An active account may still have an expired AUTH-local credential. The
	// check result must prove that failure before the account can be recovered;
	// the raw Sub2 status alone never triggers this path.
	return strings.EqualFold(strings.TrimSpace(status.Status), "active") && *status.Schedulable
}

func accountCheckRecoveryCandidate(check *store.AccountCheck) bool {
	if check == nil || check.State != "finished" || check.Freshness != "current" {
		return false
	}
	if check.HTTPStatus != nil && *check.HTTPStatus == http.StatusUnauthorized {
		return true
	}
	for _, value := range []string{check.Outcome, check.ErrorCode, check.StreamErrorCode} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "credential_missing", "credential_incomplete", "access_token_expired", "token_expired", "token_revoked", "credential_revoked", "unauthorized", "auth_failed", "authentication_failed":
			return true
		}
	}
	return false
}

func (s *sub2RecoveryService) scanSub2Accounts() {
	if !s.configured() || !s.autoRecovery.Load() || s.ctx.Err() != nil {
		return
	}
	s.monitorMu.Lock()
	if s.monitorState.Scanning {
		s.monitorMu.Unlock()
		return
	}
	s.monitorState.Scanning = true
	s.monitorState.LastError = ""
	s.monitorMu.Unlock()
	summary, lastError := "", ""
	defer func() {
		now, next := time.Now().UTC(), time.Now().UTC().Add(sub2MonitorInterval)
		s.monitorMu.Lock()
		s.monitorState.Scanning = false
		s.monitorState.LastScanAt, s.monitorState.NextScanAt = &now, &next
		s.monitorState.Summary, s.monitorState.LastError = summary, lastError
		s.monitorMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	_, statuses, err := s.sub2.syncAccountStatuses(withSub2StatusFresh(ctx))
	if err != nil {
		lastError = "Sub2 状态同步失败，下次扫描重试"
		return
	}
	checks, err := s.store.ListAccountCheckSummaries(ctx, s.sub2.destinationKey)
	if err != nil {
		lastError = "读取检测记录失败"
		return
	}
	ids := make([]int64, 0, len(statuses))
	direct := make([]int64, 0)
	unknown := 0
	for id, status := range statuses {
		if status.Unknown || status.Stale {
			unknown++
		}
		if sub2RecoverySuspect(status) {
			ids = append(ids, id)
		} else if sub2RecoveryPoolCandidate(status) && accountCheckRecoveryCandidate(checks[id].LastResult) {
			direct = append(direct, id)
		}
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	sort.Slice(direct, func(i, j int) bool { return direct[i] < direct[j] })
	queued, waiting, submitted, toCheck := 0, 0, 0, make([]int64, 0)
	for _, id := range ids {
		if !s.autoRecovery.Load() || ctx.Err() != nil {
			return
		}
		allowed, err := s.automaticRecoveryAllowed(ctx, id)
		if err != nil {
			lastError = "读取恢复记录失败"
			return
		}
		if !allowed {
			waiting++
			continue
		}
		check := checks[id]
		if check.LastResult != nil && check.LastResult.FinishedAt != nil && time.Since(time.UnixMilli(*check.LastResult.FinishedAt)) <= 5*time.Minute {
			if _, err := s.validateCandidate(ctx, id); err == nil {
				if _, _, err := s.enqueue(ctx, id, true); err == nil {
					queued++
				} else {
					lastError = "部分账号恢复入队失败，请查看账号检测和恢复详情"
				}
				continue
			}
		}
		if !check.Eligible || check.CooldownRemainingSeconds > 0 || (check.LatestTask != nil && (check.LatestTask.State == "queued" || check.LatestTask.State == "running")) {
			waiting++
			continue
		}
		toCheck = append(toCheck, id)
		if len(toCheck) == 100 {
			break
		}
	}
	for _, id := range direct {
		if !s.autoRecovery.Load() || ctx.Err() != nil {
			return
		}
		allowed, err := s.automaticRecoveryAllowed(ctx, id)
		if err != nil {
			lastError = "读取恢复记录失败"
			return
		}
		if !allowed {
			waiting++
			continue
		}
		if _, _, err := s.enqueue(ctx, id); err == nil {
			queued++
		} else {
			waiting++
			lastError = "部分账号恢复入队失败，请查看账号检测和恢复详情"
		}
	}
	if len(toCheck) > 0 && s.autoRecovery.Load() {
		batch, err := s.queueRecoveryChecks(ctx, toCheck, false)
		if err != nil {
			var checkErr *store.AccountCheckError
			if errors.As(err, &checkErr) && checkErr.Code == "batch_active" {
				summary = "已有检测批次运行，下一轮继续检查 Sub2 异常账号"
				return
			}
			lastError = "自动检测暂不可用，请检查检测代理配置"
		} else {
			submitted = len(toCheck)
			s.monitorMu.Lock()
			s.monitorState.AutoCheckBatchID = batch.ID
			s.monitorMu.Unlock()
		}
	}
	summary = fmt.Sprintf("待恢复账号 %d 个，提交检测 %d 个，直接提交恢复 %d 个，等待或冷却 %d 个", len(ids)+len(direct), submitted, queued, waiting)
	if unknown > 0 {
		summary += fmt.Sprintf("；%d 个状态暂时未知", unknown)
	}
}

func (s *sub2RecoveryService) automaticRecoveryAllowed(ctx context.Context, id int64) (bool, error) {
	if _, active, err := s.store.GetActiveAccountRecoveryTaskForDestination(ctx, id, s.sub2.destinationKey); err != nil || active {
		return false, err
	}
	previous, err := s.store.GetLatestAccountRecoveryTaskForDestination(ctx, id, s.sub2.destinationKey)
	if errors.Is(err, store.ErrAccountRecoveryNotFound) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	// Structured failures are owned by the durable retry path. A fresh scan
	// must not bypass its deadline or restart a task that needs a person.
	if previous.RetryAction != "" {
		return false, nil
	}
	// Success is not a failed attempt: a newly revoked credential must be
	// checked and recovered without waiting for the second delayed recheck.
	if previous.State != store.RecoveryCompleted && time.Since(previous.UpdatedAt) < 30*time.Minute {
		return false, nil
	}
	return true, nil
}

// Delivery is an explicit batch intent and continues after the automatic
// recovery switch is turned off. Automatic recovery itself still obeys it.
func (s *sub2RecoveryService) retryDueRecoveries() {
	if !s.configured() || s.paused.Load() || s.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 30*time.Second)
	defer cancel()
	tasks, err := s.store.ListLatestAccountRecoveryTasksForDestination(ctx, s.sub2.destinationKey)
	if err != nil {
		s.pauseRecovery(0)
		return
	}
	for _, task := range tasks {
		if ctx.Err() != nil || s.paused.Load() {
			return
		}
		if task.Purpose != "delivery" && !s.autoRecovery.Load() {
			continue
		}
		if task.RetryAction == "" && (task.State == store.RecoveryFailed || task.State == store.RecoveryUnknown) {
			s.classifyLegacyRecovery(ctx, task)
			continue
		}
		if task.NextRetryAt == nil || task.NextRetryAt.After(time.Now()) || task.RequiresAction ||
			(task.State != store.RecoveryFailed && task.State != store.RecoveryUnknown) ||
			(task.RetryAction != "resume" && task.RetryAction != "relogin") {
			continue
		}
		account, err := s.store.GetAccountByID(ctx, task.AccountID)
		if err != nil {
			if !errors.Is(err, store.ErrAccountNotFound) {
				s.pauseRecovery(task.ID)
				return
			}
			s.recordRecoveryFailure(task, task.FailureStage, &recoveryOperationError{Code: "account_missing", RequiresAction: true})
			continue
		}
		binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
		if err != nil && !errors.Is(err, store.ErrSub2ImportNotFound) {
			s.pauseRecovery(task.ID)
			return
		}
		if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID {
			s.recordRecoveryFailure(task, task.FailureStage, &recoveryOperationError{Code: "identity_changed", RequiresAction: true})
			continue
		}
		detail, err := s.sub2Account(ctx, task.Sub2AccountID)
		if err == nil {
			err = s.verifyRecoveryRetry(ctx, task, detail, account, binding)
		}
		if err != nil {
			s.recordRecoveryFailure(task, task.FailureStage, err)
			continue
		}
		if _, err := s.store.RetryAccountRecoveryTask(ctx, task.ID, task.RetryAction == "relogin"); err != nil {
			if errors.Is(err, store.ErrAccountBusy) || errors.Is(err, store.ErrAccountRecoveryNotResumable) {
				continue
			}
			if errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
				s.recordRecoveryFailure(task, task.FailureStage, &recoveryOperationError{Code: "version_changed", RequiresAction: true})
				continue
			}
			s.pauseRecovery(task.ID)
			return
		}
		s.notify()
	}
}

// Ordinary checks still trigger recovery while the switch is on, but always
// re-read Sub2 before enqueuing: a deleted, disabled, healthy or manually paused
// account must not be reauthorized due to a stale local token.
func (s *sub2RecoveryService) enqueueAutomatic(accountID int64) {
	if !s.configured() || !s.autoRecovery.Load() || s.paused.Load() || s.ctx.Err() != nil {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, 10*time.Second)
	defer cancel()
	allowed, err := s.automaticRecoveryAllowed(ctx, accountID)
	if err != nil || !allowed {
		return
	}
	if !s.remoteRecoverySuspect(ctx, accountID) {
		return
	}
	if _, _, err := s.enqueue(ctx, accountID); err != nil {
		s.monitorMu.Lock()
		s.monitorState.LastError = "部分账号自动恢复入队失败，请查看检测与恢复详情"
		s.monitorMu.Unlock()
	}
}

func (s *sub2RecoveryService) remoteRecoverySuspect(ctx context.Context, accountID int64) bool {
	binding, err := s.store.GetSub2Import(ctx, s.sub2.destinationKey, accountID)
	if err != nil || binding.State != "imported" {
		return false
	}
	status := s.sub2.sub2AccountStatus(ctx, binding.Sub2AccountID, time.Now().UTC())
	status.Imported = true
	return sub2RecoveryPoolCandidate(status)
}

func (s *sub2RecoveryService) recordRecoveryEnqueueFailure(accountID int64) {
	s.monitorMu.Lock()
	defer s.monitorMu.Unlock()
	s.monitorState.LastError = fmt.Sprintf("账号 %d 的认证失败已确认，但恢复未能入队；请刷新历史后查看账号绑定与恢复详情", accountID)
}

func (s *sub2RecoveryService) queueRecoveryChecks(ctx context.Context, ids []int64, manual bool) (store.AccountCheckBatch, error) {
	checker := s.checker
	if checker == nil || checker.paused.Load() || checker.ctx.Err() != nil {
		return store.AccountCheckBatch{}, errors.New("检测服务不可用")
	}
	prefix := "recovery-auto-"
	if manual {
		prefix = "recovery-manual-"
	}
	var key [16]byte
	if _, err := rand.Read(key[:]); err != nil {
		return store.AccountCheckBatch{}, err
	}
	input := store.AccountCheckInput{RequestKey: prefix + hex.EncodeToString(key[:]), AccountIDs: ids, Concurrency: 2, ProxyMode: "direct", DestinationKey: s.sub2.destinationKey}
	if checker.proxy != "" {
		if checker.upstreamProxy != "" {
			return store.AccountCheckBatch{}, errors.New("检测暂不支持前置代理")
		}
		client, err := probe.NewClient(checker.proxy)
		if err != nil {
			return store.AccountCheckBatch{}, errors.New("检测代理配置无效")
		}
		client.Close()
		input.ProxyMode, input.Proxy = "default", checker.proxy
	}
	batch, _, err := s.store.CreateAccountCheckBatch(ctx, input)
	if err == nil {
		checker.notify()
	}
	return batch, err
}

func (s *sub2RecoveryService) handleRecoveryCheck(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		recoveryAPIError(w, 405, "method_not_allowed", "请求方法不支持")
		return
	}
	if !s.configured() || s.paused.Load() {
		recoveryAPIError(w, 503, "recovery_unavailable", "Sub2 恢复服务不可用")
		return
	}
	var req sub2RecoveryRequest
	if !decodeRecoveryJSON(w, r, &req) {
		return
	}
	if req.AccountID <= 0 {
		recoveryAPIError(w, 400, "invalid_account_id", "account_id 必须是正整数")
		return
	}
	if !s.remoteRecoverySuspect(r.Context(), req.AccountID) {
		recoveryAPIError(w, 422, "not_recoverable", "仅恢复仍在 Sub2 池中的异常账号；禁用、人工暂停或未知状态请先核对")
		return
	}
	if task, _, err := s.enqueue(r.Context(), req.AccountID); err == nil {
		respondJSONStatus(w, 202, map[string]any{"success": true, "task": task})
		return
	} else if !errors.Is(err, errRecoveryNotCandidate) {
		recoveryAPIError(w, 409, "recovery_unavailable", safeRecoveryMessage(err))
		return
	}
	batch, err := s.queueRecoveryChecks(r.Context(), []int64{req.AccountID}, true)
	if err != nil {
		var safe *store.AccountCheckError
		if errors.As(err, &safe) {
			respondJSONStatus(w, 409, map[string]any{"success": false, "code": safe.Code, "message": safe.Message, "active_batch_id": safe.ActiveBatchID})
			return
		}
		recoveryAPIError(w, 503, "checks_unavailable", "检测服务不可用，请检查检测代理配置")
		return
	}
	respondJSONStatus(w, 202, map[string]any{"success": true, "batch_id": batch.ID})
}

// Older releases stored no retry policy. Only adopt a saved remote checkpoint
// with matching ownership; login failures use structured local error codes.
func (s *sub2RecoveryService) classifyLegacyRecovery(ctx context.Context, task store.AccountRecoveryTask) {
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		return
	}
	if s.store.ValidateAccountRecoveryVersion(ctx, task.ID) != nil {
		s.recordRecoveryFailure(task, store.RecoveryValidating, &recoveryOperationError{Code: "version_changed", RequiresAction: true})
		return
	}
	if task.ResultCredentialAttemptID > 0 {
		detail, err := s.sub2Account(ctx, task.Sub2AccountID)
		if err != nil {
			s.recordRecoveryFailure(task, store.RecoveryCredentialsApplied, err)
			return
		}
		if !recoveryMarkerMatches(detail, task) {
			s.recordRecoveryFailure(task, store.RecoveryCredentialsApplied, &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true})
			return
		}
		s.recordRecoveryFailure(task, store.RecoveryCredentialsApplied, &recoveryOperationError{Code: "interrupted"})
		return
	}
	failure := &recoveryOperationError{Code: "login_failed"}
	switch account.LastErrorCode {
	case "invalid_password", "incorrect_password", "invalid_totp", "invalid_mfa":
		failure.Code, failure.RequiresAction = "login_required", true
	}
	// Legacy identity_mismatch also represented workspace/user-id changes that
	// are now allowed. Retry once under current identity checks; a real email
	// conflict is classified as requiring action by loginAgain.
	if account.Status == "deactivated" || account.Status == "deleted" || account.Status == "deleted_or_deactivated" {
		failure.Code, failure.RequiresAction = "account_unavailable", true
	}
	s.recordRecoveryFailure(task, store.RecoveryLoggingIn, failure)
}
