package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func handleHistoryCredentials(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPatch {
		respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
		return
	}
	if loginHistory == nil {
		respondJSONStatus(w, 503, LoginResponse{Message: "历史记录未初始化"})
		return
	}
	id, err := strconv.ParseInt(strings.TrimSuffix(strings.TrimPrefix(r.URL.Path, "/api/history/"), "/credentials"), 10, 64)
	if err != nil || id <= 0 {
		respondJSONStatus(w, 400, LoginResponse{Message: "账号 ID 无效"})
		return
	}
	mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if err != nil || mediaType != "application/json" {
		respondJSONStatus(w, 415, LoginResponse{Message: "请求必须使用 application/json"})
		return
	}
	var req struct {
		Password     *string `json:"password"`
		TOTPSecret   *string `json:"totp_secret"`
		Proxy        *string `json:"proxy"`
		ClearTOTP    bool    `json:"clear_totp"`
		ClearProxy   bool    `json:"clear_proxy"`
		AutoContinue *bool   `json:"auto_continue"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	decoder := json.NewDecoder(r.Body)
	decoder.DisallowUnknownFields()
	if decoder.Decode(&req) != nil || decoder.Decode(new(any)) != io.EOF || req.ClearTOTP && req.TOTPSecret != nil || req.ClearProxy && req.Proxy != nil {
		respondJSONStatus(w, 400, LoginResponse{Message: "资料格式无效，清除和设置同一字段不能同时提交"})
		return
	}
	if req.Password != nil && (len(*req.Password) < 8 || len(*req.Password) > 4096) {
		respondJSONStatus(w, 400, LoginResponse{Message: "新密码须为 8 至 4096 个字符"})
		return
	}
	if req.TOTPSecret != nil {
		*req.TOTPSecret = strings.TrimSpace(*req.TOTPSecret)
		if *req.TOTPSecret == "" || !login.ValidateTOTPSecret(*req.TOTPSecret) {
			respondJSONStatus(w, 400, LoginResponse{Message: "TOTP 密钥格式不正确；如需清除请勾选清除"})
			return
		}
	}
	if req.Proxy != nil {
		*req.Proxy = strings.TrimSpace(*req.Proxy)
		if *req.Proxy == "" || login.ValidateLoginProxy(*req.Proxy) != nil {
			respondJSONStatus(w, 400, LoginResponse{Message: "代理配置无效；如需清除请勾选清除"})
			return
		}
	}
	emptyTOTP, emptyProxy := "", ""
	if req.ClearTOTP {
		req.TOTPSecret = &emptyTOTP
	}
	if req.ClearProxy {
		req.Proxy = &emptyProxy
	}
	if req.Password == nil && req.TOTPSecret == nil && req.Proxy == nil {
		respondJSONStatus(w, 400, LoginResponse{Message: "请填写需要修改的资料"})
		return
	}
	autoContinue := req.AutoContinue == nil || *req.AutoContinue
	if autoContinue && (accountRecoveryService == nil || accountRecoveryService.loginWithProxies == nil || accountRecoveryService.paused.Load() || accountRecoveryService.ctx.Err() != nil) {
		respondJSONStatus(w, 503, LoginResponse{Message: "后台处理暂不可用，请稍后重试"})
		return
	}
	repair, err := loginHistory.PatchAccountCredentials(r.Context(), id, store.CredentialPatch{Password: req.Password, TOTPSecret: req.TOTPSecret, Proxy: req.Proxy}, autoContinue)
	if errors.Is(err, store.ErrAccountBusy) {
		respondJSONStatus(w, 409, LoginResponse{Message: "账号正在登录或恢复，请稍后修改", Code: "account_busy"})
		return
	}
	if errors.Is(err, store.ErrAccountNotFound) {
		respondJSONStatus(w, 404, LoginResponse{Message: "账号不存在", Code: "account_not_found"})
		return
	}
	if err != nil {
		respondJSONStatus(w, 500, LoginResponse{Message: "资料保存失败，请稍后重试"})
		return
	}
	if autoContinue {
		accountRecoveryService.notify()
	}
	respondJSON(w, map[string]any{"success": true, "account_id": id, "repair": repair})
}

func (s *sub2RecoveryService) processNextCredentialRepair() bool {
	ctx, cancel := context.WithTimeout(s.ctx, 5*time.Second)
	repair, err := s.store.ClaimCredentialRepair(ctx, time.Now())
	cancel()
	if err != nil {
		s.pauseRecovery(0)
		return false
	}
	if repair == nil {
		return false
	}
	s.processCredentialRepair(*repair)
	return true
}

func (s *sub2RecoveryService) finishCredentialRepair(repair store.CredentialRepair, state, message string, next *time.Time) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := s.store.FinishCredentialRepair(ctx, repair, state, message, next); err != nil {
		s.pauseRecovery(0)
	}
}

func (s *sub2RecoveryService) failCredentialRepair(repair store.CredentialRepair, err error) {
	failure := classifyRecoveryError(err)
	if errors.Is(err, store.ErrAccountBusy) {
		failure = &recoveryOperationError{Code: "account_busy"}
	}
	if failure.Code == "manual_pause" || failure.Code == "version_changed" {
		s.finishCredentialRepair(repair, "canceled", failure.Error(), nil)
	} else if failure.RequiresAction {
		s.finishCredentialRepair(repair, "requires_action", failure.Error(), nil)
	} else {
		next := time.Now().Add(recoveryRetryDelay(failure, repair.RetryCount))
		s.finishCredentialRepair(repair, "retry_wait", failure.Error(), &next)
	}
}

func (s *sub2RecoveryService) processCredentialRepair(repair store.CredentialRepair) {
	ctx, cancel := context.WithTimeout(s.ctx, s.timeout)
	defer cancel()
	lease, err := s.store.AcquireAccountRecovery(ctx, repair.AccountID)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	defer lease.Release()
	latestRepair, err := s.store.GetCredentialRepair(ctx, repair.AccountID)
	if err != nil {
		s.pauseRecovery(0)
		return
	}
	if latestRepair.Revision != repair.Revision || latestRepair.State != "checking" {
		return // A later edit or explicit save-only request superseded this claim.
	}
	version, err := s.store.GetAccountCredentialVersion(ctx, repair.AccountID)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	if repair.AttemptID > 0 && version == repair.AttemptID {
		s.finishCredentialRepair(repair, "completed", "新资料已登录成功，后台继续处理交付", nil)
		return // Login and delivery committed before a process restart.
	}
	if version != repair.SourceVersion {
		s.failCredentialRepair(repair, &recoveryOperationError{Code: "version_changed"})
		return
	}
	account, err := s.store.GetAccountByID(ctx, repair.AccountID)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	task, taskErr := s.store.GetLatestAccountRecoveryTask(ctx, repair.AccountID)
	if taskErr != nil && !errors.Is(taskErr, store.ErrAccountRecoveryNotFound) {
		s.failCredentialRepair(repair, taskErr)
		return
	}
	if taskErr == nil {
		switch task.State {
		case store.RecoveryCanceled:
			s.failCredentialRepair(repair, &recoveryOperationError{Code: "manual_pause"})
			return
		case store.RecoveryFailed, store.RecoveryUnknown:
			s.resumeCredentialRepairRecovery(ctx, repair, task, account)
			return
		case store.RecoveryCompleted:
		default:
			s.failCredentialRepair(repair, store.ErrAccountBusy)
			return
		}
	}
	s.loginCredentialRepair(ctx, repair, account, lease)
}

func (s *sub2RecoveryService) resumeCredentialRepairRecovery(ctx context.Context, repair store.CredentialRepair, task store.AccountRecoveryTask, account store.Account) {
	if !s.configured() {
		s.failCredentialRepair(repair, &recoveryOperationError{Code: "configuration", RequiresAction: true})
		return
	}
	binding, err := s.sub2.store.GetSub2Import(ctx, s.sub2.destinationKey, task.AccountID)
	if err != nil && !errors.Is(err, store.ErrSub2ImportNotFound) {
		s.failCredentialRepair(repair, err)
		return
	}
	if err != nil || binding.State != "imported" || binding.Sub2AccountID != task.Sub2AccountID {
		s.failCredentialRepair(repair, &recoveryOperationError{Code: "identity_changed", RequiresAction: true})
		return
	}
	detail, err := s.sub2Account(ctx, task.Sub2AccountID)
	if err == nil {
		// Saving corrected login material explicitly requests one new login,
		// including when the previous recovery had a reusable OAuth checkpoint.
		task.RetryAction = "relogin"
		err = s.verifyRecoveryRetry(ctx, task, detail, account, binding)
	}
	if err == nil {
		err = s.store.RetryAccountRecoveryForCredentialRepair(ctx, repair, task.ID)
	}
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	s.notify()
}

func (s *sub2RecoveryService) loginCredentialRepair(ctx context.Context, repair store.CredentialRepair, account store.Account, lease *store.AccountRecoveryLease) {
	if s.loginWithProxies == nil {
		s.failCredentialRepair(repair, &recoveryOperationError{Code: "configuration", RequiresAction: true})
		return
	}
	credentials, err := s.store.GetCredentialsByID(ctx, account.ID)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	var options store.DeliveryOptions
	if s.configured() {
		var requested *store.DeliveryOptions
		deliveries, err := s.store.ListLatestAccountDeliveries(ctx)
		if err != nil {
			s.failCredentialRepair(repair, err)
			return
		}
		for _, delivery := range deliveries {
			if delivery.AccountID == account.ID && delivery.DestinationKey == s.sub2.destinationKey {
				copy := delivery.Options
				requested = &copy
			}
		}
		options, err = s.sub2.loadOptions(ctx, requested)
		if err != nil {
			s.failCredentialRepair(repair, err)
			return
		}
	}
	release, err := acquireRecoveryLoginSlot(ctx)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	defer release()
	_, attempt, err := lease.BeginAttempt(ctx, credentials)
	if err != nil {
		s.failCredentialRepair(repair, err)
		return
	}
	defer attempt.Release()
	if err := s.store.SetCredentialRepairAttempt(ctx, repair, attempt.ID); err != nil {
		s.pauseRecovery(0)
		return
	}
	result, loginErr := s.loginWithProxies(ctx, credentials.Email, credentials.Password, credentials.TOTPSecret, credentials.Proxy, "")
	if loginErr == nil {
		if result == nil || verifyOAuthIdentity(recoveryOAuthCredentials{Email: result.Email, AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ChatGPTAccountID: result.ChatGPTAccountID}, account) != nil {
			loginErr = &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
		}
	}
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if loginErr != nil {
		info := describeLoginError(loginErr)
		if err := s.store.FinishAttempt(finishCtx, attempt.ID, false, nil, &store.Failure{Stage: info.Stage, Code: info.Code, HTTPStatus: info.HTTPStatus, Retryable: info.Retryable, Message: info.Message, AccountStatus: info.AccountStatus}); err != nil {
			s.pauseRecovery(0)
			return
		}
		s.failCredentialRepair(repair, classifyRecoveryLoginError(loginErr))
		return
	}
	stored := &store.Result{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ChatGPTAccountID: result.ChatGPTAccountID, OrganizationID: result.OrganizationID, PlanType: result.PlanType, ExpiresAt: result.ExpiresAt, ExpiresIn: result.ExpiresIn}
	if s.configured() {
		_, err = s.store.FinishAttemptAndQueueDelivery(finishCtx, attempt.ID, stored, s.sub2.destinationKey, options)
	} else {
		err = s.store.FinishAttempt(finishCtx, attempt.ID, true, stored, nil)
	}
	if err != nil {
		s.pauseRecovery(0)
		return
	}
	s.finishCredentialRepair(repair, "completed", "新资料已登录成功，后台继续处理交付", nil)
}
