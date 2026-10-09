package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// The outbox only resolves/imports the destination. Credential updates, probes
// and enabling reuse the recovery worker and its durable checkpoints.
type accountDeliveryService struct {
	store    *store.Store
	importer *sub2ImportService
	recovery *sub2RecoveryService
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

var deliveryService *accountDeliveryService

func newAccountDeliveryService(history *store.Store, importer *sub2ImportService, recovery *sub2RecoveryService) *accountDeliveryService {
	ctx, cancel := context.WithCancel(context.Background())
	return &accountDeliveryService{store: history, importer: importer, recovery: recovery, ctx: ctx, cancel: cancel}
}
func (s *accountDeliveryService) Start() {
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for {
			s.runOnce()
			select {
			case <-s.ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
func (s *accountDeliveryService) Stop() { s.cancel(); s.wg.Wait() }
func (s *accountDeliveryService) runOnce() {
	if s.store == nil || s.importer == nil || s.recovery == nil || s.recovery.paused.Load() {
		return
	}
	if !s.importer.workerMu.TryLock() {
		return
	}
	defer s.importer.workerMu.Unlock()
	tasks, err := s.store.ListDueAccountDeliveries(s.ctx, time.Now(), 5)
	if err != nil {
		return
	}
	for _, task := range tasks {
		// update() opens the recovery circuit on a durable storage failure. Stop
		// this batch immediately so a later task cannot create a remote account
		// after the circuit has already tripped.
		if s.ctx.Err() != nil || s.recovery.paused.Load() {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 90*time.Second)
		s.process(ctx, task)
		cancel()
	}
}
func (s *accountDeliveryService) update(task store.AccountDelivery, state, message, manual string, next *time.Time, recoveryID int64) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.store.UpdateAccountDelivery(ctx, task.ID, state, message, manual, next, recoveryID); err != nil {
		if !errors.Is(err, store.ErrAccountNotFound) && !errors.Is(err, store.ErrAccountDeliveryNotFound) && !errors.Is(err, store.ErrAccountDeliverySettled) {
			s.recovery.paused.Store(true)
			log.Printf("交付任务存储故障：已停止后续派发 task_id=%d", task.ID)
		}
		return false
	}
	return true
}
func (s *accountDeliveryService) failed(task store.AccountDelivery, err error) {
	failure := classifyRecoveryError(err)
	if errors.Is(err, errSub2AmbiguousMatch) || errors.Is(err, errSub2IdentityConflict) {
		failure = &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	if failure.RequiresAction {
		s.update(task, "requires_action", failure.Error(), failure.Error(), nil, 0)
	} else {
		next := time.Now().Add(recoveryRetryDelay(failure, task.RetryCount))
		s.update(task, "retry_wait", failure.Error(), "", &next, 0)
	}
}
func (s *accountDeliveryService) process(ctx context.Context, task store.AccountDelivery) {
	if !s.importer.configured() || task.DestinationKey != s.importer.destinationKey {
		s.failed(task, &recoveryOperationError{Code: "configuration", RequiresAction: true})
		return
	}
	lease, err := s.store.AcquireAccountRecovery(ctx, task.AccountID)
	if err != nil {
		s.failed(task, &recoveryOperationError{Code: "account_busy"})
		return
	}
	defer lease.Release()
	// A crash can occur after the recovery task commits but before its link does.
	// Reattach that exact task even when its automatic re-login advanced version.
	prior, priorErr := s.store.GetLatestAccountRecoveryTask(ctx, task.AccountID)
	if priorErr == nil && prior.DeliveryID == task.ID && prior.Purpose == "delivery" {
		s.update(task, "verifying", "", "", nil, prior.ID)
		return
	}
	if priorErr != nil && !errors.Is(priorErr, store.ErrAccountRecoveryNotFound) {
		s.failed(task, priorErr)
		return
	}
	version, err := s.store.GetAccountCredentialVersion(ctx, task.AccountID)
	if err != nil {
		s.failed(task, err)
		return
	}
	if version != task.CredentialVersion {
		s.update(task, "canceled", "已有更新的登录结果，旧交付已取消", "", nil, 0)
		return
	}
	if _, active, err := s.store.GetActiveAccountRecoveryTask(ctx, task.AccountID); err != nil || active {
		s.failed(task, &recoveryOperationError{Code: "account_busy"})
		return
	}
	if !s.update(task, "checking", "", "", nil, 0) {
		return
	}
	binding, err := s.importer.ensureTask(ctx, task.AccountID, task.Options)
	if err != nil {
		s.failed(task, err)
		return
	}
	if binding.State != "imported" {
		if !s.update(task, "importing", "", "", nil, 0) {
			return
		}
		if binding.State == "queued" {
			err = s.importer.processQueued(ctx, binding)
		} else {
			// Never blindly replay a POST whose outcome is uncertain. Confirmation is
			// itself retried automatically with backoff, using the existing marker.
			err = s.importer.reconcile(ctx, binding.ID)
		}
		if err != nil {
			s.failed(task, err)
			return
		}
		binding, err = s.store.GetSub2ImportByID(ctx, binding.ID)
		if err != nil {
			s.failed(task, err)
			return
		}
	}
	if binding.State != "imported" || binding.Sub2AccountID <= 0 {
		s.failed(task, &recoveryOperationError{Code: "protocol_error"})
		return
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		s.failed(task, err)
		return
	}
	detail, err := s.recovery.sub2Account(ctx, binding.Sub2AccountID)
	if err != nil {
		s.failed(task, err)
		return
	}
	if verifySub2RecoveryIdentity(detail, binding.Sub2AccountID, task.AccountID, account, binding) != nil {
		s.failed(task, &recoveryOperationError{Code: "identity_changed", RequiresAction: true})
		return
	}
	scheduled, known := boolField(detail, "schedulable")
	status, _ := detail["status"].(string)
	if !known || recoveryAccountDisabled(detail) || !scheduled && !strings.EqualFold(status, "error") {
		s.failed(task, &recoveryOperationError{Code: "manual_pause", RequiresAction: true})
		return
	}
	recovery, _, err := s.store.CreateAccountDeliveryRecoveryTask(ctx, task.ID, task.AccountID, binding.Sub2AccountID, task.CredentialVersion, true)
	if err != nil {
		s.failed(task, err)
		return
	}
	if !s.update(task, "verifying", "", "", nil, recovery.ID) {
		return
	}
	// Release before waking; the recovery worker uses the same account lease.
	lease.Release()
	s.recovery.notify()
}

func listDeliveryStatuses(ctx context.Context, history *store.Store) (map[int64]store.AccountDelivery, error) {
	tasks, err := history.ListLatestAccountDeliveries(ctx)
	if err != nil {
		return nil, err
	}
	result := make(map[int64]store.AccountDelivery, len(tasks))
	for _, task := range tasks {
		if task.RecoveryTaskID > 0 {
			recovery, err := history.GetAccountRecoveryTaskByID(ctx, task.RecoveryTaskID)
			if err != nil {
				return nil, err
			}
			task.LastError, task.ManualAction, task.RequiresAction = recovery.LastError, recovery.ManualAction, recovery.RequiresAction
			task.NextRetryAt, task.RetryCount, task.UpdatedAt = recovery.NextRetryAt, recovery.RetryCount, recovery.UpdatedAt
			switch {
			case recovery.State == store.RecoveryCompleted:
				task.State = "completed"
			case recovery.State == store.RecoveryCanceled:
				task.State = "canceled"
			case recovery.RequiresAction:
				task.State = "requires_action"
			case recovery.NextRetryAt != nil:
				task.State = "retry_wait"
			default:
				task.State = "verifying"
			}
		}
		result[task.AccountID] = task
	}
	return result, nil
}

// Retry rechecks the existing durable operation. An unknown create outcome is
// reconciled by its original marker, never converted into another create.
func (s *accountDeliveryService) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSONStatus(w, 405, LoginResponse{Message: "Method not allowed"})
		return
	}
	parts := strings.Split(strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/account-deliveries/"), "/"), "/")
	if len(parts) != 2 || parts[1] != "retry" {
		respondJSONStatus(w, 404, LoginResponse{Message: "交付操作不存在"})
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		respondJSONStatus(w, 400, LoginResponse{Message: "交付任务 ID 无效"})
		return
	}
	if !s.importer.configured() {
		respondJSONStatus(w, 503, LoginResponse{Message: "Sub2 未配置"})
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	task, err := s.store.GetAccountDelivery(ctx, id)
	if err != nil || task.DestinationKey != s.importer.destinationKey {
		respondJSONStatus(w, 404, LoginResponse{Message: "交付任务不存在"})
		return
	}
	latest, err := s.store.ListLatestAccountDeliveries(ctx)
	if err != nil {
		respondJSONStatus(w, 500, LoginResponse{Message: "交付状态读取失败"})
		return
	}
	current := false
	for _, d := range latest {
		if d.ID == task.ID {
			current = true
		}
	}
	if !current || task.State == "canceled" || task.State == "completed" {
		respondJSONStatus(w, 409, LoginResponse{Message: "任务已结束或已被新登录替代"})
		return
	}
	if !s.importer.workerMu.TryLock() {
		respondJSONStatus(w, 409, LoginResponse{Message: "正在处理导入，请稍后重试"})
		return
	}
	defer s.importer.workerMu.Unlock()
	lease, err := s.store.AcquireAccountRecovery(ctx, task.AccountID)
	if err != nil {
		respondJSONStatus(w, 409, LoginResponse{Message: "账号正在处理"})
		return
	}
	defer lease.Release()
	if task.RecoveryTaskID > 0 {
		_, _, err = s.recovery.enqueue(ctx, task.AccountID)
	} else {
		version, versionErr := s.store.GetAccountCredentialVersion(ctx, task.AccountID)
		if versionErr != nil || version != task.CredentialVersion {
			respondJSONStatus(w, 409, LoginResponse{Message: "登录凭据版本已变化"})
			return
		}
		err = s.store.WakeAccountDelivery(ctx, task.AccountID)
	}
	if err != nil {
		respondJSONStatus(w, 409, LoginResponse{Message: classifyRecoveryError(err).Error()})
		return
	}
	respondJSONStatus(w, 202, map[string]any{"success": true, "queued": true})
}
