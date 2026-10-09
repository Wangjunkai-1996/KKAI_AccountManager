package main

// Account recovery is deliberately owned by AUTH. The browser submits only an
// AUTH account id; encrypted credentials stay on the server and the existing
// Sub2 admin API updates the already-imported account.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const recoveryClientID = sub2OAuthClientID

var (
	errRecoveryNotCandidate = errors.New("账号没有可恢复的最新认证失败检测结果")
	errRecoveryManualReview = errors.New("上次恢复未能确认结果，Sub2 调度仍处于暂停状态，请先人工核对")
	accountRecoveryService  *sub2RecoveryService
)

type sub2RecoveryService struct {
	store            *store.Store
	loginWithProxies func(context.Context, string, string, string, string, string) (*login.LoginResult, error)
	sub2             *sub2ImportService

	timeout      time.Duration
	ctx          context.Context
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	wake         chan struct{}
	paused       atomic.Bool
	autoRecovery atomic.Bool
	checker      *accountCheckService
	monitorMu    sync.Mutex
	monitorWake  chan struct{}
	monitorState sub2MonitorState
}

type sub2RecoveryRequest struct {
	AccountID int64 `json:"account_id"`
}

type recoveryOAuthCredentials struct {
	AccessToken      string `json:"access_token"`
	RefreshToken     string `json:"refresh_token"`
	ClientID         string `json:"client_id"`
	Email            string `json:"email"`
	ChatGPTAccountID string `json:"chatgpt_account_id"`
	OrganizationID   string `json:"organization_id"`
	PlanType         string `json:"plan_type"`
	ExpiresAt        int64  `json:"expires_at"`
	ExpiresIn        int    `json:"expires_in"`
}

type sub2APIError struct {
	Status            int
	Code              int
	Reason            string
	Message           string
	RetryAfterSeconds int
}

func (e *sub2APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("Sub2 请求失败（HTTP %d）", e.Status)
}

func newSub2RecoveryService(history *store.Store, loginService *login.Service, importer *sub2ImportService) *sub2RecoveryService {
	ctx, cancel := context.WithCancel(context.Background())
	service := &sub2RecoveryService{store: history, sub2: importer, timeout: 6 * time.Minute, ctx: ctx, cancel: cancel, wake: make(chan struct{}, 1), monitorWake: make(chan struct{}, 1)}
	service.autoRecovery.Store(envBool("AUTH_AUTO_RECOVERY"))
	if history != nil {
		readCtx, readCancel := context.WithTimeout(ctx, 5*time.Second)
		enabled, saved, err := history.AutoRecoveryEnabled(readCtx)
		readCancel()
		if err != nil {
			service.autoRecovery.Store(false)
			service.monitorState.LastError = "自动恢复设置读取失败，请重新保存开关"
		} else if saved {
			service.autoRecovery.Store(enabled)
		}
	}
	if loginService != nil {
		service.loginWithProxies = loginService.LoginWithProxiesContext
	}
	return service
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

func (s *sub2RecoveryService) configured() bool {
	return s != nil && s.store != nil && s.sub2 != nil && s.sub2.configured()
}

func (s *sub2RecoveryService) Start() {
	if s == nil || s.store == nil || (!s.configured() && s.loginWithProxies == nil) {
		return
	}
	s.wg.Add(2)
	go s.worker()
	go s.monitor()
	s.notify()
}

func (s *sub2RecoveryService) Stop() {
	if s == nil {
		return
	}
	s.cancel()
	s.wg.Wait()
}

func (s *sub2RecoveryService) notify() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *sub2RecoveryService) worker() {
	defer s.wg.Done()
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()
	for {
		if s.ctx.Err() != nil || s.paused.Load() {
			return
		}
		ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
		var tasks []store.AccountRecoveryTask
		var err error
		if s.configured() {
			tasks, err = s.store.ListAccountRecoveryTasks(ctx, store.RecoveryQueued, 1)
		}
		cancel()
		if err != nil {
			s.pauseRecovery(0)
			return
		}
		if len(tasks) > 0 {
			claimCtx, claimCancel := context.WithTimeout(s.ctx, 5*time.Second)
			task, claimErr := s.store.ClaimAccountRecoveryTask(claimCtx, tasks[0].ID)
			claimCancel()
			if claimErr == nil {
				s.process(task)
				continue
			}
			if !errors.Is(claimErr, store.ErrAccountRecoveryNotQueued) {
				s.pauseRecovery(tasks[0].ID)
				return
			}
		}
		if s.processNextCredentialRepair() {
			continue
		}
		if s.configured() && s.processNextRecheck() {
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

func (s *sub2RecoveryService) handleCollection(w http.ResponseWriter, r *http.Request) {
	if !s.configured() || s.paused.Load() {
		recoveryAPIError(w, http.StatusServiceUnavailable, "recovery_unavailable", "Sub2 恢复服务未配置")
		return
	}
	switch r.Method {
	case http.MethodPost:
		var req sub2RecoveryRequest
		if !decodeRecoveryJSON(w, r, &req) {
			return
		}
		if req.AccountID <= 0 {
			recoveryAPIError(w, http.StatusBadRequest, "invalid_account_id", "account_id 必须是正整数")
			return
		}
		task, created, err := s.enqueue(r.Context(), req.AccountID)
		if err != nil {
			status, code := http.StatusInternalServerError, "recovery_failed"
			switch {
			case errors.Is(err, store.ErrAccountBusy):
				status, code = http.StatusConflict, "recovery_busy"
			case errors.Is(err, store.ErrAccountRecoveryVersionChanged):
				status, code = http.StatusConflict, "recovery_version_changed"
			case errors.Is(err, store.ErrAccountNotFound):
				status, code = http.StatusNotFound, "account_not_found"
			case errors.Is(err, errRecoveryNotCandidate):
				status, code = http.StatusUnprocessableEntity, "not_recoverable"
			case errors.Is(err, store.ErrAccountRecoveryNotFound):
				status, code = http.StatusUnprocessableEntity, "sub2_binding_missing"
			case errors.Is(err, errRecoveryManualReview):
				status, code = http.StatusConflict, "recovery_manual_review"
			}
			recoveryAPIError(w, status, code, safeRecoveryMessage(err))
			return
		}
		status := http.StatusAccepted
		if !created {
			status = http.StatusOK
		}
		respondJSONStatus(w, status, map[string]any{"success": true, "reused": !created, "task": task})
	case http.MethodGet:
		var task store.AccountRecoveryTask
		var err error
		if raw := strings.TrimSpace(r.URL.Query().Get("task_id")); raw != "" {
			id, parseErr := strconv.ParseInt(raw, 10, 64)
			if parseErr != nil || id <= 0 {
				recoveryAPIError(w, http.StatusBadRequest, "invalid_task_id", "恢复任务 ID 无效")
				return
			}
			task, err = s.store.GetAccountRecoveryTaskByID(r.Context(), id)
		} else {
			id, parseErr := strconv.ParseInt(strings.TrimSpace(r.URL.Query().Get("account_id")), 10, 64)
			if parseErr != nil || id <= 0 {
				recoveryAPIError(w, http.StatusBadRequest, "invalid_account_id", "account_id 必须是正整数")
				return
			}
			task, err = s.store.GetLatestAccountRecoveryTask(r.Context(), id)
		}
		if errors.Is(err, store.ErrAccountRecoveryNotFound) {
			recoveryAPIError(w, http.StatusNotFound, "recovery_not_found", "恢复任务不存在")
			return
		}
		if err != nil {
			recoveryAPIError(w, http.StatusInternalServerError, "recovery_read_failed", "读取恢复任务失败")
			return
		}
		respondJSON(w, map[string]any{"success": true, "task": task})
	default:
		recoveryAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
	}
}

func (s *sub2RecoveryService) handleAction(w http.ResponseWriter, r *http.Request) {
	path := strings.Trim(strings.TrimPrefix(r.URL.Path, "/api/account-recovery/"), "/")
	if path == "check" {
		s.handleRecoveryCheck(w, r)
		return
	}
	if path == "settings" {
		if s == nil || s.store == nil {
			recoveryAPIError(w, http.StatusServiceUnavailable, "recovery_unavailable", "恢复服务不可用")
			return
		}
		switch r.Method {
		case http.MethodGet:
			respondJSON(w, s.recoverySettings())
		case http.MethodPut:
			var req struct {
				Enabled            *bool `json:"enabled"`
				AutoRecoveryEnable *bool `json:"auto_recovery_enabled"`
			}
			if !decodeRecoveryJSON(w, r, &req) {
				return
			}
			enabled := req.Enabled
			if enabled == nil {
				enabled = req.AutoRecoveryEnable
			}
			if enabled == nil {
				recoveryAPIError(w, http.StatusBadRequest, "invalid_request", "enabled 必须是布尔值")
				return
			}
			if err := s.saveRecoverySetting(r.Context(), *enabled); err != nil {
				recoveryAPIError(w, http.StatusServiceUnavailable, "settings_save_failed", "自动恢复设置保存失败")
				return
			}
			s.notifyMonitor()
			respondJSON(w, s.recoverySettings())
		default:
			recoveryAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		}
		return
	}
	if r.Method != http.MethodGet {
		recoveryAPIError(w, http.StatusMethodNotAllowed, "method_not_allowed", "请求方法不支持")
		return
	}
	id, err := strconv.ParseInt(path, 10, 64)
	if err != nil || id <= 0 {
		recoveryAPIError(w, http.StatusBadRequest, "invalid_task_id", "恢复任务 ID 无效")
		return
	}
	task, err := s.store.GetAccountRecoveryTaskByID(r.Context(), id)
	if errors.Is(err, store.ErrAccountRecoveryNotFound) {
		recoveryAPIError(w, http.StatusNotFound, "recovery_not_found", "恢复任务不存在")
		return
	}
	if err != nil {
		recoveryAPIError(w, http.StatusInternalServerError, "recovery_read_failed", "读取恢复任务失败")
		return
	}
	respondJSON(w, map[string]any{"success": true, "task": task})
}

func (s *sub2RecoveryService) enqueue(ctx context.Context, accountID int64, automatic ...bool) (store.AccountRecoveryTask, bool, error) {
	account, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return store.AccountRecoveryTask{}, false, err
	}
	if task, found, err := s.store.GetActiveAccountRecoveryTask(ctx, accountID); err != nil {
		return task, false, err
	} else if found {
		return task, false, nil
	}
	binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, accountID)
	if err != nil || binding.State != "imported" || binding.Sub2AccountID <= 0 {
		return store.AccountRecoveryTask{}, false, store.ErrAccountRecoveryNotFound
	}
	detail, err := s.sub2Account(ctx, binding.Sub2AccountID)
	if err != nil {
		return store.AccountRecoveryTask{}, false, errors.New("Sub2 账号状态暂时无法核对")
	}
	if err := verifySub2RecoveryIdentity(detail, binding.Sub2AccountID, accountID, account, binding); err != nil {
		return store.AccountRecoveryTask{}, false, errors.New("Sub2 账号身份与 AUTH 绑定不一致")
	}
	if recoveryAccountDisabled(detail) {
		return store.AccountRecoveryTask{}, false, errors.New("Sub2 账号已被禁用，请先在 Sub2 启用后恢复")
	}
	previous, previousErr := s.store.GetLatestAccountRecoveryTask(ctx, accountID)
	if previousErr != nil && !errors.Is(previousErr, store.ErrAccountRecoveryNotFound) {
		return previous, false, previousErr
	}
	if previousErr == nil && previous.Sub2AccountID == binding.Sub2AccountID &&
		(previous.Resumable || previous.RetryAction == "relogin") && (previous.State == store.RecoveryFailed || previous.State == store.RecoveryUnknown) {
		// Resume is independent of the original 401 and current Sub2 error flag:
		// the successful apply may already have made the account active/paused.
		retryTask := previous
		if retryTask.RetryAction != "relogin" {
			retryTask.RetryAction = "resume"
		}
		if err := s.verifyRecoveryRetry(ctx, retryTask, detail, account, binding); err != nil {
			return previous, false, err
		}
		task, err := s.store.RetryAccountRecoveryTask(ctx, previous.ID, retryTask.RetryAction == "relogin")
		if err == nil {
			s.notify()
		} else if errors.Is(err, store.ErrAccountRecoveryNotResumable) {
			// Another enqueue may have won the same failed/unknown task between
			// validation and the CAS update. Reuse its active task instead of
			// turning an expected loser into an HTTP 500.
			if active, found, readErr := s.store.GetActiveAccountRecoveryTask(ctx, accountID); readErr == nil && found {
				return active, false, nil
			}
		}
		return task, false, err
	}
	if len(automatic) > 0 && automatic[0] {
		status, _ := detail["status"].(string)
		if !s.autoRecovery.Load() || !strings.EqualFold(status, "error") {
			return store.AccountRecoveryTask{}, false, errRecoveryNotCandidate
		}
	}
	original, ok := boolField(detail, "schedulable")
	if !ok {
		return store.AccountRecoveryTask{}, false, errors.New("Sub2 未返回账号调度状态")
	}
	// Sub2 marks credentials that fail upstream authentication as error and
	// pauses their scheduler. Once AUTH has a current 401 observation, that
	// pause is recoverable; an active account that was manually paused remains
	// paused.
	if !original {
		if status, _ := detail["status"].(string); strings.EqualFold(strings.TrimSpace(status), "error") {
			original = true
		}
	}
	checkID, err := s.validateCandidate(ctx, accountID)
	if err != nil {
		return store.AccountRecoveryTask{}, false, err
	}
	if previousErr == nil && previous.CheckID == checkID {
		if previous.State == store.RecoveryCompleted {
			return previous, false, nil
		}

	}
	task, created, err := s.store.CreateOrGetAccountRecoveryTask(ctx, accountID, checkID, binding.Sub2AccountID, original)
	if err == nil && created {
		s.notify()
	}
	return task, created, err
}

func (s *sub2RecoveryService) process(task store.AccountRecoveryTask) {
	ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
	defer cancel()
	fail := func(stage string, err error) { s.recordRecoveryFailure(task, stage, err) }
	require := func(stage, code string) { fail(stage, &recoveryOperationError{Code: code, RequiresAction: true}) }
	lease, err := s.store.AcquireAccountRecovery(ctx, task.AccountID)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			require(store.RecoveryValidating, "account_missing")
		} else if errors.Is(err, store.ErrAccountBusy) {
			fail(store.RecoveryValidating, &recoveryOperationError{Code: "account_busy"})
		} else {
			s.pauseRecovery(task.ID)
		}
		return
	}
	defer lease.Release()
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		if errors.Is(err, store.ErrAccountNotFound) {
			require(store.RecoveryValidating, "account_missing")
		} else {
			s.pauseRecovery(task.ID)
		}
		return
	}
	if err := s.store.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		require(store.RecoveryValidating, "version_changed")
		return
	}
	if task.ResultCredentialAttemptID == 0 && task.RetryAction != "relogin" {
		checkID, err := s.validateCandidate(ctx, task.AccountID)
		if err != nil || checkID != task.CheckID {
			require(store.RecoveryValidating, "version_changed")
			return
		}
	}
	binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
	if err != nil && !errors.Is(err, store.ErrSub2ImportNotFound) {
		s.pauseRecovery(task.ID)
		return
	}
	if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID {
		require(store.RecoveryValidating, "identity_changed")
		return
	}
	if !s.setState(task.ID, store.RecoveryValidating, "正在核对账号绑定和调度状态") {
		return
	}
	detail, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		fail(store.RecoveryValidating, err)
		return
	}
	if verifySub2RecoveryIdentity(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil {
		require(store.RecoveryValidating, "identity_changed")
		return
	}
	if recoveryAccountDisabled(detail) {
		require(store.RecoveryValidating, "account_unavailable")
		return
	}
	scheduled, ok := boolField(detail, "schedulable")
	if !ok {
		require(store.RecoveryValidating, "checkpoint_unconfirmed")
		return
	}
	if task.RetryAction != "" {
		if err := s.verifyRecoveryRetry(ctx, task, detail, account, binding); err != nil {
			fail(task.FailureStage, err)
			return
		}
	} else if status, _ := detail["status"].(string); strings.EqualFold(status, "active") && !scheduled && task.OriginalSchedulable &&
		(task.ResultCredentialAttemptID == 0 || task.Purpose == "delivery") {
		require(store.RecoveryValidating, "manual_pause")
		return
	}
	if task.ResultCredentialAttemptID == 0 && !task.OriginalSchedulable && scheduled {
		require(store.RecoveryValidating, "manual_pause")
		return
	}
	if scheduled {
		if !s.setState(task.ID, store.RecoveryDisablingSchedule, "正在暂时关闭 Sub2 调度") {
			return
		}
		if err := s.setSchedulable(ctx, task.Sub2AccountID, false); err != nil {
			fail(store.RecoveryDisablingSchedule, err)
			return
		}
	}
	var oauth recoveryOAuthCredentials
	if task.ResultCredentialAttemptID > 0 {
		_, saved, readErr := s.store.GetOAuthResultByID(ctx, task.AccountID)
		if readErr != nil {
			fail(store.RecoveryLoginSucceeded, readErr)
			return
		}
		oauth = recoveryOAuthCredentials{AccessToken: saved.AccessToken, RefreshToken: saved.RefreshToken,
			ClientID: recoveryClientID, Email: account.Email, ChatGPTAccountID: saved.ChatGPTAccountID,
			OrganizationID: saved.OrganizationID, PlanType: saved.PlanType, ExpiresAt: saved.ExpiresAt, ExpiresIn: saved.ExpiresIn}
	} else {
		if !s.setState(task.ID, store.RecoveryLoggingIn, "正在重新登录 AUTH 取得新凭据") {
			return
		}
		oauth, err = s.loginAgain(ctx, task.ID, account, lease)
		if err != nil {
			if !s.paused.Load() {
				fail(store.RecoveryLoggingIn, classifyRecoveryLoginError(err))
			}
			return
		}
	}
	if verifyOAuthIdentity(oauth, account) != nil {
		require(store.RecoveryIdentityVerified, "identity_changed")
		return
	}
	account.ChatGPTAccountID = oauth.ChatGPTAccountID
	account.OrganizationID, account.PlanType, account.ExpiresAt = oauth.OrganizationID, oauth.PlanType, oauth.ExpiresAt
	savedTask, err := s.store.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil {
		s.pauseRecovery(task.ID)
		return
	}
	task = savedTask
	if s.store.ValidateAccountRecoveryVersion(ctx, task.ID) != nil {
		require(store.RecoveryIdentityVerified, "version_changed")
		return
	}
	if !s.setState(task.ID, store.RecoveryIdentityVerified, "新凭据身份已核对") {
		return
	}
	// A verified remote checkpoint already contains this version. Re-probe it
	// without rewriting credentials or clearing an error raised by Sub2.
	alreadyApplied := recoveryMarkerMatches(detail, task) && verifySub2RecoveryMetadata(detail, task.Sub2AccountID, task.AccountID, account, binding) == nil
	if !alreadyApplied {
		if !s.setState(task.ID, store.RecoveryApplyingCredentials, "正在写回原 Sub2 账号") {
			return
		}
		if err := s.applyCredentials(ctx, task, oauth); err != nil {
			fail(store.RecoveryApplyingCredentials, err)
			return
		}
	}
	after, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		fail(store.RecoveryCredentialsApplied, err)
		return
	}
	if verifySub2RecoveryMetadata(after, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(after, task) {
		require(store.RecoveryCredentialsApplied, "identity_changed")
		return
	}
	if recoveryAccountDisabled(after) {
		require(store.RecoveryCredentialsApplied, "account_unavailable")
		return
	}
	if value, ok := boolField(after, "schedulable"); !ok || value {
		require(store.RecoveryCredentialsApplied, "manual_pause")
		return
	}
	if !s.setState(task.ID, store.RecoveryCredentialsApplied, "凭据已写回，正在通过 Sub2 检测可用性") {
		return
	}
	if err := s.verifySub2AccountProbe(ctx, task.Sub2AccountID); err != nil {
		fail(store.RecoveryCredentialsApplied, err)
		return
	}
	beforeEnable, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		fail(store.RecoveryEnablingSchedule, err)
		return
	}
	if verifySub2RecoveryDetail(beforeEnable, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(beforeEnable, task) {
		require(store.RecoveryEnablingSchedule, "identity_changed")
		return
	}
	if s.store.ValidateAccountRecoveryVersion(ctx, task.ID) != nil {
		require(store.RecoveryEnablingSchedule, "version_changed")
		return
	}
	if task.Purpose == "delivery" {
		if err := s.sub2.applyDeliveryGroups(ctx, task, binding, beforeEnable); err != nil {
			fail(store.RecoveryEnablingSchedule, err)
			return
		}
	}
	if !task.OriginalSchedulable {
		if !s.setState(task.ID, store.RecoveryCompleted, "凭据已更新，Sub2 检测通过并保持原调度状态") {
			s.pauseSchedule(task.Sub2AccountID)
		}
		return
	}
	if !s.setState(task.ID, store.RecoveryEnablingSchedule, "检测成功，正在开启 Sub2 调度") {
		return
	}
	if err := s.setSchedulable(ctx, task.Sub2AccountID, true); err != nil {
		s.pauseSchedule(task.Sub2AccountID)
		fail(store.RecoveryEnablingSchedule, err)
		return
	}
	enabled, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err != nil {
		s.pauseSchedule(task.Sub2AccountID)
		fail(store.RecoveryEnablingSchedule, err)
		return
	}
	value, ok := boolField(enabled, "schedulable")
	if !ok || !value || verifySub2RecoveryDetail(enabled, task.Sub2AccountID, task.AccountID, account, binding) != nil || !recoveryMarkerMatches(enabled, task) {
		s.pauseSchedule(task.Sub2AccountID)
		require(store.RecoveryEnablingSchedule, "checkpoint_unconfirmed")
		return
	}
	if !s.setState(task.ID, store.RecoveryCompleted, "凭据已更新，Sub2 检测通过并已开启调度") {
		s.pauseSchedule(task.Sub2AccountID)
	}
}

func (s *sub2RecoveryService) pauseSchedule(id int64) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := s.setSchedulable(ctx, id, false); err != nil {
		log.Printf("恢复任务无法确认暂停调度：sub2_account_id=%d", id)
	}
}

func (s *sub2RecoveryService) loginAgain(ctx context.Context, taskID int64, account store.Account, lease *store.AccountRecoveryLease) (recoveryOAuthCredentials, error) {
	var out recoveryOAuthCredentials
	if s.loginWithProxies == nil {
		return out, &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	credentials, err := s.store.GetCredentialsByID(ctx, account.ID)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(credentials.Password) == "" {
		return out, &recoveryOperationError{Code: "login_required", RequiresAction: true}
	}
	release, err := acquireRecoveryLoginSlot(ctx)
	if err != nil {
		return out, err
	}
	defer release()
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, attempt, err := lease.BeginAttempt(persistCtx, store.Credentials{Email: credentials.Email, Password: credentials.Password, TOTPSecret: credentials.TOTPSecret, Proxy: credentials.Proxy, ExistingAccountID: account.ID})
	cancel()
	if err != nil {
		return out, err
	}
	defer attempt.Release()
	result, loginErr := s.loginWithProxies(ctx, credentials.Email, credentials.Password, credentials.TOTPSecret, credentials.Proxy, "")
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if loginErr != nil {
		info := describeLoginError(loginErr)
		if err := lease.FinishAttempt(finishCtx, taskID, attempt.ID, false, nil, &store.Failure{Stage: info.Stage, Code: info.Code, HTTPStatus: info.HTTPStatus, Retryable: info.Retryable, Message: info.Message, AccountStatus: info.AccountStatus}); err != nil {
			if !recoveryTaskGone(err) {
				s.pauseRecovery(taskID)
			}
			return out, errors.New("AUTH 登录结果保存失败")
		}
		return out, loginErr
	}
	if result == nil || strings.TrimSpace(result.AccessToken) == "" || strings.TrimSpace(result.RefreshToken) == "" {
		if err := lease.FinishAttempt(finishCtx, taskID, attempt.ID, false, nil, &store.Failure{Stage: login.LoginStageUnknown, Code: login.LoginErrorUnknown, Message: "登录未返回完整凭据"}); err != nil {
			if !recoveryTaskGone(err) {
				s.pauseRecovery(taskID)
			}
			return out, errors.New("AUTH 登录结果保存失败")
		}
		return out, errors.New("login returned incomplete credentials")
	}
	out = recoveryOAuthCredentials{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ClientID: recoveryClientID, Email: result.Email, ChatGPTAccountID: result.ChatGPTAccountID, OrganizationID: result.OrganizationID, PlanType: result.PlanType, ExpiresAt: result.ExpiresAt, ExpiresIn: result.ExpiresIn}
	if err := verifyOAuthIdentity(out, account); err != nil {
		if err := lease.FinishAttempt(finishCtx, taskID, attempt.ID, false, nil, &store.Failure{Stage: login.LoginStageToken, Code: "identity_mismatch", Message: "新登录账号身份与原账号不一致"}); err != nil {
			if !recoveryTaskGone(err) {
				s.pauseRecovery(taskID)
			}
			return out, errors.New("AUTH 登录结果保存失败")
		}
		return recoveryOAuthCredentials{}, &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	stored := &store.Result{AccessToken: out.AccessToken, RefreshToken: out.RefreshToken, ChatGPTAccountID: out.ChatGPTAccountID, OrganizationID: out.OrganizationID, PlanType: out.PlanType, ExpiresAt: out.ExpiresAt, ExpiresIn: out.ExpiresIn}
	if err := lease.FinishAttempt(finishCtx, taskID, attempt.ID, true, stored, nil); err != nil {
		if !recoveryTaskGone(err) {
			s.pauseRecovery(taskID)
		}
		return recoveryOAuthCredentials{}, errors.New("AUTH 登录结果保存失败")
	}
	return out, nil
}

func acquireRecoveryLoginSlot(ctx context.Context) (func(), error) {
	if loginSlots == nil {
		return func() {}, nil
	}
	select {
	case loginSlots <- struct{}{}:
		return func() { <-loginSlots }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (s *sub2RecoveryService) validateCandidate(ctx context.Context, accountID int64) (int64, error) {
	checks, err := s.store.ListAccountChecks(ctx, accountID, 20)
	if err != nil || len(checks) == 0 {
		return 0, errRecoveryNotCandidate
	}
	check := checks[0]
	// A canceled or cooldown-skipped check does not replace the last observation.
	if check.State != "queued" && check.State != "running" {
		for _, result := range checks {
			if result.State == "finished" {
				check = result
				break
			}
		}
	}
	if check.State != "finished" || check.Freshness != "current" {
		return 0, errRecoveryNotCandidate
	}
	if check.HTTPStatus != nil && *check.HTTPStatus == http.StatusUnauthorized {
		return check.ID, nil
	}
	for _, value := range []string{check.Outcome, check.ErrorCode, check.StreamErrorCode} {
		switch strings.ToLower(strings.TrimSpace(value)) {
		case "credential_missing", "credential_incomplete", "access_token_expired", "token_expired", "token_revoked", "credential_revoked", "unauthorized", "auth_failed", "authentication_failed":
			return check.ID, nil
		}
	}
	return 0, errRecoveryNotCandidate
}

func (s *sub2RecoveryService) sub2Account(ctx context.Context, id int64) (map[string]any, error) {
	var detail map[string]any
	status, err := s.sub2.apiJSONStatus(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail)
	if err != nil && status > 0 {
		var classified *recoveryOperationError
		if !errors.As(err, &classified) {
			err = recoveryHTTPError(status, 0)
		}
	}
	return detail, err
}

func (s *sub2RecoveryService) setSchedulable(ctx context.Context, id int64, enabled bool) error {
	return s.sub2JSONBody(ctx, http.MethodPost, "/admin/accounts/"+strconv.FormatInt(id, 10)+"/schedulable", map[string]any{"schedulable": enabled}, nil)
}

func (s *sub2RecoveryService) applyCredentials(ctx context.Context, task store.AccountRecoveryTask, credentials recoveryOAuthCredentials) error {
	values := map[string]any{"access_token": credentials.AccessToken, "refresh_token": credentials.RefreshToken, "client_id": recoveryClientID,
		"organization_id": credentials.OrganizationID, "plan_type": credentials.PlanType}
	if credentials.Email != "" {
		values["email"] = credentials.Email
	}
	if credentials.ChatGPTAccountID != "" {
		values["chatgpt_account_id"] = credentials.ChatGPTAccountID
	}
	if credentials.ExpiresAt > 0 {
		values["expires_at"] = credentials.ExpiresAt
	}
	return s.sub2JSONBody(ctx, http.MethodPost, "/admin/accounts/"+strconv.FormatInt(task.Sub2AccountID, 10)+"/apply-oauth-credentials", map[string]any{"type": "oauth", "credentials": values, "extra": map[string]any{"kkai_auth_recovery": map[string]any{"task_id": task.ID, "credential_attempt_id": task.ResultCredentialAttemptID}}}, nil)
}

func (s *sub2RecoveryService) setState(id int64, state, message string) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := s.store.UpdateAccountRecoveryTask(ctx, id, state, message); err != nil {
		s.pauseRecovery(id)
		return false
	}
	return true
}

func (s *sub2RecoveryService) fail(id int64, state, message string) { s.setState(id, state, message) }

func recoveryRetryDelay(failure *recoveryOperationError, previousFailures int) time.Duration {
	delay := time.Minute
	switch failure.Code {
	case "rate_limited", "probe_failed", "protocol_error", "login_failed":
		delay = 5 * time.Minute
	case "upstream_error":
		delay = 2 * time.Minute
	case "challenge":
		delay = 15 * time.Minute
	}
	for i := 0; i < previousFailures && delay < time.Hour; i++ {
		delay *= 2
	}
	if delay > time.Hour {
		delay = time.Hour
	}
	if retryAfter := time.Duration(failure.RetryAfterSeconds) * time.Second; retryAfter > delay {
		delay = retryAfter
	}
	return delay
}

func classifyRecoveryLoginError(err error) *recoveryOperationError {
	var classified *recoveryOperationError
	if errors.As(err, &classified) {
		return classified
	}
	info := describeLoginError(err)
	retryAfter := 0
	var retry interface{ RetryAfterDuration() time.Duration }
	if errors.As(err, &retry) {
		retryAfter = int(retry.RetryAfterDuration().Seconds())
	}
	return classifyRecoveryLoginInfo(info, retryAfter)
}

func classifyRecoveryLoginInfo(info login.LoginErrorInfo, retryAfter int) *recoveryOperationError {
	if info.AccountStatus != "" {
		return &recoveryOperationError{Code: "account_unavailable", HTTPStatus: info.HTTPStatus, RequiresAction: true}
	}
	isChallenge := info.Code == login.LoginErrorCloudflareChallenge
	if info.HTTPStatus == http.StatusRequestTimeout {
		return &recoveryOperationError{Code: "timeout", HTTPStatus: info.HTTPStatus, RetryAfterSeconds: retryAfter}
	}
	if !isChallenge && (info.HTTPStatus == http.StatusTooManyRequests || info.HTTPStatus >= 500) {
		return recoveryHTTPError(info.HTTPStatus, retryAfter)
	}
	if !isChallenge && info.HTTPStatus >= 400 && info.HTTPStatus < 500 {
		return &recoveryOperationError{Code: "login_required", HTTPStatus: info.HTTPStatus, RequiresAction: true}
	}
	switch info.Code {
	case login.LoginErrorTimeout:
		return &recoveryOperationError{Code: "timeout"}
	case login.LoginErrorCanceled:
		return &recoveryOperationError{Code: "interrupted"}
	case login.LoginErrorConnectionReset:
		return &recoveryOperationError{Code: "network_error"}
	case login.LoginErrorCloudflareChallenge:
		return &recoveryOperationError{Code: "challenge", HTTPStatus: info.HTTPStatus, RetryAfterSeconds: retryAfter}
	case login.LoginErrorUnsupportedRegion, login.LoginErrorConfiguration, login.LoginErrorIdentity,
		login.LoginErrorOAuth, login.LoginErrorTokenExchange, login.LoginErrorUnexpectedPage,
		"invalid_password", "incorrect_password", "invalid_otp", "invalid_totp", "invalid_mfa", "mfa_required", "identity_mismatch", "invalid_credentials":
		return &recoveryOperationError{Code: "login_required", HTTPStatus: info.HTTPStatus, RequiresAction: true}
	}
	if info.Stage == "oauth" && info.HTTPStatus == 0 {
		return &recoveryOperationError{Code: "login_required", RequiresAction: true}
	}
	if !info.Retryable {
		// Login already exhausted its local retry policy. Do not turn a
		// deterministic OAuth/page/token failure into an endless durable retry.
		return &recoveryOperationError{Code: "login_required", HTTPStatus: info.HTTPStatus, RequiresAction: true}
	}
	return &recoveryOperationError{Code: "login_failed", HTTPStatus: info.HTTPStatus}
}

func recoveryTaskGone(err error) bool {
	return errors.Is(err, store.ErrAccountRecoveryNotFound) || errors.Is(err, store.ErrAccountNotFound)
}

func (s *sub2RecoveryService) recordRecoveryFailure(task store.AccountRecoveryTask, stage string, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	current, readErr := s.store.GetAccountRecoveryTaskByID(ctx, task.ID)
	if readErr != nil {
		if !recoveryTaskGone(readErr) {
			s.pauseRecovery(task.ID)
		}
		return
	}
	// A retry worker may have spent time in a remote request while an operator
	// manually requeued the same task. Never let that old response classify the
	// new round. Active workers are allowed to advance through states locally,
	// so only failed/unknown snapshots get this early stale check.
	if (task.State == store.RecoveryFailed || task.State == store.RecoveryUnknown) &&
		(current.State != task.State || current.UpdatedAt.UnixMilli() != task.UpdatedAt.UnixMilli() || current.RetryCount != task.RetryCount) {
		return
	}
	failure := classifyRecoveryError(err)
	record := store.RecoveryFailure{State: store.RecoveryUnknown, Stage: stage, Code: failure.Code, Message: failure.Error()}
	if failure.RequiresAction {
		record.RetryAction, record.ManualAction = "manual", failure.Error()
		if current.ResultCredentialAttemptID == 0 {
			record.State = store.RecoveryFailed
		}
	} else {
		record.RetryAction = "resume"
		if current.ResultCredentialAttemptID == 0 || failure.CredentialInvalid {
			record.RetryAction = "relogin"
		}
		next := time.Now().UTC().Add(recoveryRetryDelay(failure, current.RetryCount))
		record.NextRetryAt = &next
	}
	if failure.Code == "manual_pause" {
		record.State = store.RecoveryCanceled
	}
	record.ExpectedState = current.State
	record.ExpectedUpdatedAt = current.UpdatedAt.UnixMilli()
	record.ExpectedRetryCount = current.RetryCount
	if _, err := s.store.RecordAccountRecoveryFailure(ctx, task.ID, record); err != nil {
		// A late result losing the CAS is expected during a manual requeue or
		// completion. It is not a storage failure and must not globally pause
		// recovery dispatch.
		if !errors.Is(err, store.ErrAccountRecoveryStale) && !errors.Is(err, store.ErrAccountRecoveryNotResumable) {
			s.pauseRecovery(task.ID)
		}
	}
}

// This fence is shared by automatic requeue and execution, so changes between
// scanning and claiming cannot turn an unrelated pause into a resume request.
func (s *sub2RecoveryService) verifyRecoveryRetry(ctx context.Context, task store.AccountRecoveryTask, detail map[string]any, account store.Account, binding store.Sub2Import) error {
	if task.RetryAction != "resume" && task.RetryAction != "relogin" {
		return &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true}
	}
	if err := s.store.ValidateAccountRecoveryVersion(ctx, task.ID); err != nil {
		return &recoveryOperationError{Code: "version_changed", RequiresAction: true}
	}
	if !task.OriginalSchedulable || recoveryAccountDisabled(detail) {
		return &recoveryOperationError{Code: "manual_pause", RequiresAction: true}
	}
	if verifySub2RecoveryIdentity(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	scheduled, ok := boolField(detail, "schedulable")
	if !ok {
		return &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true}
	}
	if s.delayedRecheckRetryOwned(ctx, task, detail) {
		return nil
	}
	markerTask := task
	if markerTask.ResultCredentialAttemptID == 0 && task.RetryAction == "relogin" {
		markerTask.ResultCredentialAttemptID = task.SourceCredentialAttemptID
	}
	if recoveryMarkerMatches(detail, markerTask) {
		if verifySub2RecoveryMetadata(detail, task.Sub2AccountID, task.AccountID, account, binding) != nil {
			return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
		}
		return nil
	}
	// A retry before the first successful apply has no remote checkpoint yet.
	// It may continue only while Sub2 is still in the original error state.
	status, _ := detail["status"].(string)
	extra, _ := detail["extra"].(map[string]any)
	_, hasMarker := extra["kkai_auth_recovery"]
	priorMarker, _ := extra["kkai_auth_recovery"].(map[string]any)
	priorVersion := int64(toFloat(priorMarker["credential_attempt_id"]))
	priorTask := int64(toFloat(priorMarker["task_id"]))
	originalMarker := !hasMarker || (priorTask > 0 && priorTask < task.ID && priorVersion == task.SourceCredentialAttemptID)
	if task.Purpose == "delivery" && priorTask > 0 && priorTask < task.ID && priorVersion > 0 && priorVersion <= task.SourceCredentialAttemptID {
		originalMarker = true
	}
	beforeApply := task.FailureStage == store.RecoveryValidating || task.FailureStage == store.RecoveryDisablingSchedule ||
		task.FailureStage == store.RecoveryLoggingIn || task.FailureStage == store.RecoveryLoginSucceeded ||
		task.FailureStage == store.RecoveryIdentityVerified || task.FailureStage == store.RecoveryApplyingCredentials
	if beforeApply && originalMarker && strings.EqualFold(status, "error") {
		return nil
	}
	// Validation and the first attempt to disable scheduling have no durable
	// remote write yet. If the account is still active and scheduled, a
	// transient failure can be retried idempotently; the later login/apply
	// stages still require the worker-owned pause proof below.
	if beforeApply && originalMarker && scheduled && strings.EqualFold(status, "active") &&
		(task.FailureStage == store.RecoveryValidating || task.FailureStage == store.RecoveryDisablingSchedule) {
		return nil
	}
	// Delivery begins with saved fresh credentials on a healthy account. A
	// successful pause precedes these persisted stages, so an interrupted apply
	// can continue without another login. A timeout in the pause itself has no
	// such proof and is deliberately excluded.
	if task.Purpose == "delivery" && beforeApply && originalMarker && strings.EqualFold(status, "active") {
		pauseConfirmed := task.FailureStage == store.RecoveryIdentityVerified || task.FailureStage == store.RecoveryApplyingCredentials
		if scheduled || pauseConfirmed {
			return nil
		}
	}
	// logging_in is written only after this worker successfully disabled the
	// schedule. It is therefore the durable proof that an unscheduled active
	// account is our pause after a transient login failure. A queued claim is
	// validating and deliberately does not provide this proof.
	workerPauseConfirmed := task.FailureStage == store.RecoveryLoggingIn ||
		task.FailureStage == store.RecoveryLoginSucceeded ||
		task.FailureStage == store.RecoveryIdentityVerified ||
		task.FailureStage == store.RecoveryApplyingCredentials
	if !scheduled && strings.EqualFold(status, "active") {
		// A saved checkpoint must still prove the same remote identity. If a
		// marker was present and no longer matches, treat it as ownership loss;
		// the worker's old pause does not authorize retrying another account.
		if beforeApply && workerPauseConfirmed && originalMarker {
			return nil
		}
		return &recoveryOperationError{Code: "manual_pause", RequiresAction: true}
	}
	return &recoveryOperationError{Code: "checkpoint_unconfirmed", RequiresAction: true}
}

func (s *sub2RecoveryService) pauseRecovery(id int64) {
	s.paused.Store(true)
	log.Printf("恢复任务存储故障：已停止后续派发，修复存储后重启服务核对 task_id=%d", id)
}

func verifyOAuthIdentity(credentials recoveryOAuthCredentials, account store.Account) error {
	if strings.TrimSpace(credentials.AccessToken) == "" || strings.TrimSpace(credentials.RefreshToken) == "" {
		return errors.New("OAuth credentials incomplete")
	}
	if strings.TrimSpace(credentials.Email) == "" || !strings.EqualFold(strings.TrimSpace(credentials.Email), strings.TrimSpace(account.Email)) {
		return errors.New("email mismatch")
	}
	if strings.TrimSpace(credentials.ChatGPTAccountID) == "" {
		return errors.New("chatgpt account id missing")
	}
	return nil
}

func boolField(value map[string]any, key string) (bool, bool) {
	v, ok := value[key]
	b, valid := v.(bool)
	return b, ok && valid
}

func (s *sub2RecoveryService) sub2JSONBody(ctx context.Context, method, path string, input, out any) error {
	body, err := json.Marshal(input)
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, method, s.sub2.baseURL+path, strings.NewReader(string(body)))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("x-api-key", s.sub2.adminAPIKey)
	resp, err := s.sub2.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return recoveryHTTPError(resp.StatusCode, recoveryRetryAfter(resp.Header.Get("Retry-After")))
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if err != nil {
		return err
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Reason  string          `json:"reason"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return &recoveryOperationError{Code: "protocol_error", HTTPStatus: resp.StatusCode}
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 || envelope.Code != 0 {
		return &sub2APIError{Status: resp.StatusCode, Code: envelope.Code, Message: "Sub2 请求未成功"}
	}
	if out != nil && len(envelope.Data) != 0 && string(envelope.Data) != "null" {
		if err := json.Unmarshal(envelope.Data, out); err != nil {
			return errors.New("Sub2 响应数据无效")
		}
	}
	return nil
}

func recoveryMarkerMatches(detail map[string]any, task store.AccountRecoveryTask) bool {
	extra, _ := detail["extra"].(map[string]any)
	marker, _ := extra["kkai_auth_recovery"].(map[string]any)
	return task.ResultCredentialAttemptID > 0 && toFloat(marker["task_id"]) == float64(task.ID) && toFloat(marker["credential_attempt_id"]) == float64(task.ResultCredentialAttemptID)
}

func recoveryStageMessage(err error) string {
	return "认证恢复失败，Sub2 保持停止调度"
}

func safeRecoveryMessage(err error) string {
	if errors.Is(err, store.ErrAccountBusy) {
		return "账号已有登录或恢复任务，请稍后重试"
	}
	if errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
		return "账号凭据已更新，请重新检测后恢复"
	}
	if errors.Is(err, errRecoveryNotCandidate) {
		return "当前账号没有明确且最新的 401 或令牌失效检测结果"
	}
	if errors.Is(err, store.ErrAccountRecoveryNotFound) {
		return "账号尚未成功导入 Sub2"
	}
	switch err.Error() {
	case "Sub2 账号状态暂时无法核对", "Sub2 账号身份与 AUTH 绑定不一致", "Sub2 未返回账号调度状态":
		return err.Error()
	case errRecoveryManualReview.Error():
		return errRecoveryManualReview.Error()
	default:
		return "恢复任务无法创建，请稍后重试"
	}
}

func decodeRecoveryJSON(w http.ResponseWriter, r *http.Request, target any) bool {
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		recoveryAPIError(w, http.StatusUnsupportedMediaType, "invalid_content_type", "请求必须使用 application/json")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		recoveryAPIError(w, http.StatusBadRequest, "invalid_request", "请求格式错误")
		return false
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		recoveryAPIError(w, http.StatusBadRequest, "invalid_request", "请求只能包含一个 JSON 对象")
		return false
	}
	return true
}

func recoveryAPIError(w http.ResponseWriter, status int, code, message string) {
	respondJSONStatus(w, status, map[string]any{"success": false, "code": code, "message": message})
}
