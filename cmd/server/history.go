package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

// The database is intentionally opened by the server process rather than per
// request. Store owns the per-email lock and all credential encryption.
var (
	historyDBPath  = flag.String("history-db", envOr("OPENAI_LOGIN_DB", filepath.Join("data", "accounts.db")), "账号历史 SQLite 数据库路径")
	historyKeyPath = flag.String("history-key", envOr("OPENAI_LOGIN_KEY", filepath.Join("data", "accounts.key")), "账号历史加密密钥路径")
	loginHistory   *store.Store
)

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func initLoginHistory(dbPath, keyPath string) error {
	history, err := store.Open(dbPath, keyPath)
	if err != nil {
		return err
	}
	loginHistory = history
	return nil
}

type historyLoginRequest struct {
	ManualAttempt   bool                   `json:"manual_attempt"`
	DeliveryOptions *store.DeliveryOptions `json:"delivery_options,omitempty"`
	AutoDeliver     bool                   `json:"auto_deliver"`
	AccountID       int64                  `json:"account_id"`
	Proxy           string                 `json:"proxy"`
	UpstreamProxy   string                 `json:"upstream_proxy"`
}

type historyListResponse struct {
	Success            bool                                   `json:"success"`
	Data               []store.Account                        `json:"data"`
	LifecycleEvents    map[int64][]store.LifecycleEvent       `json:"lifecycle_events,omitempty"`
	LifecycleAvailable bool                                   `json:"lifecycle_events_available"`
	Imports            []store.Sub2Import                     `json:"imports,omitempty"`
	Sub2Statuses       map[int64]sub2AccountStatus            `json:"sub2_statuses,omitempty"`
	Sub2Configured     bool                                   `json:"sub2_configured"`
	ImportsAvailable   bool                                   `json:"imports_available"`
	ChecksAvailable    bool                                   `json:"checks_available"`
	Checks             map[int64]store.AccountCheckSummary    `json:"checks"`
	Recoveries         map[int64]store.AccountRecoveryTask    `json:"recoveries"`
	Deliveries         map[int64]store.AccountDelivery        `json:"deliveries"`
	Rechecks           map[int64]store.AccountRecoveryRecheck `json:"rechecks"`
	Repairs            map[int64]store.CredentialRepair       `json:"repairs"`
}

type historyDetailResponse struct {
	CredentialsMeta           store.CredentialsMeta               `json:"credentials_meta"`
	Success                   bool                                `json:"success"`
	Account                   store.Account                       `json:"account"`
	Attempts                  []store.AttemptRecord               `json:"attempts"`
	RecoveryHistory           []store.AccountRecoveryTask         `json:"recovery_history"`
	RecoveryHistoryAvailable  bool                                `json:"recovery_history_available"`
	RecoveryHistoryTruncated  bool                                `json:"recovery_history_truncated"`
	RecoveryFailures          []store.AccountRecoveryFailureEvent `json:"recovery_failures"`
	RecoveryFailuresAvailable bool                                `json:"recovery_failures_available"`
	RecoveryFailuresTruncated bool                                `json:"recovery_failures_truncated"`
	Checks                    []store.AccountCheck                `json:"checks"`
	ChecksAvailable           bool                                `json:"checks_available"`
	LifecycleEvents           []store.LifecycleEvent              `json:"lifecycle_events"`
	LifecycleEventsAvailable  bool                                `json:"lifecycle_events_available"`
}

func handleHistory() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
			return
		}
		if loginHistory == nil {
			respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "历史记录未初始化", Code: "history_unavailable"})
			return
		}
		accounts, err := loginHistory.ListAccounts(r.Context())
		if err != nil {
			respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取历史记录失败", Code: "history_read_failed"})
			return
		}
		for i := range accounts {
			normalizeHistoryLoginFailure(&accounts[i])
		}
		var imports []store.Sub2Import
		var importsErr error
		var sub2Statuses map[int64]sub2AccountStatus
		if sub2Importer != nil && sub2Importer.configured() {
			imports, sub2Statuses, importsErr = sub2Importer.syncAccountStatuses(withSub2StatusCache(r.Context()))
		}
		var recoveries map[int64]store.AccountRecoveryTask
		recoveryDestination := ""
		if sub2Importer != nil && sub2Importer.configured() {
			recoveryDestination = sub2Importer.destinationKey
		}
		checks, checksErr := loginHistory.ListAccountCheckSummaries(r.Context(), recoveryDestination)
		if tasks, recoveryErr := loginHistory.ListLatestAccountRecoveryTasksForDestination(r.Context(), recoveryDestination); recoveryErr == nil {
			recoveries = make(map[int64]store.AccountRecoveryTask)
			for _, task := range tasks {
				if _, exists := recoveries[task.AccountID]; !exists {
					recoveries[task.AccountID] = task
				}
			}
		}
		configured := sub2Importer != nil && sub2Importer.configured()
		destination := ""
		if configured {
			destination = sub2Importer.destinationKey
		}
		deliveries, deliveryErr := listDeliveryStatuses(r.Context(), loginHistory, destination)
		if deliveryErr != nil {
			respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "读取交付状态失败，请稍后刷新", Code: "delivery_read_failed"})
			return
		}
		rechecks, recheckErr := loginHistory.ListAccountRecoveryRechecksForDestination(r.Context(), recoveryDestination)
		repairs, repairErr := loginHistory.ListCredentialRepairs(r.Context())
		if recheckErr != nil || repairErr != nil {
			respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "读取后台处理状态失败，请稍后刷新"})
			return
		}
		lifecycleEvents, lifecycleErr := loginHistory.ListLifecycleEvents(r.Context(), 1000)
		lifecycleByAccount := make(map[int64][]store.LifecycleEvent)
		if lifecycleErr == nil {
			for _, event := range lifecycleEvents {
				if event.AccountID > 0 {
					lifecycleByAccount[event.AccountID] = append(lifecycleByAccount[event.AccountID], event)
				}
			}
		}
		respondJSON(w, historyListResponse{Success: true, Data: accounts, Imports: imports, Sub2Statuses: sub2Statuses, Sub2Configured: configured,
			LifecycleEvents: lifecycleByAccount, LifecycleAvailable: lifecycleErr == nil,
			ImportsAvailable: importsErr == nil, ChecksAvailable: checksErr == nil, Checks: checks, Recoveries: recoveries, Deliveries: deliveries, Rechecks: rechecks, Repairs: repairs})
	}
}

func handleHistoryDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/credentials/reveal") {
			handleHistoryCredentialReveal(w, r)
			return
		}
		if strings.HasSuffix(r.URL.Path, "/credentials") {
			handleHistoryCredentials(w, r)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodDelete {
			respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
			return
		}
		if loginHistory == nil {
			respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "历史记录未初始化", Code: "history_unavailable"})
			return
		}
		idText := strings.TrimPrefix(r.URL.Path, "/api/history/")
		id, err := strconv.ParseInt(idText, 10, 64)
		if err != nil || id <= 0 {
			respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "账号 ID 无效", Code: "invalid_account_id"})
			return
		}
		account, err := loginHistory.GetAccountByID(r.Context(), id)
		if errors.Is(err, store.ErrAccountNotFound) {
			respondJSONStatus(w, http.StatusNotFound, LoginResponse{Message: "账号不存在", Code: "account_not_found"})
			return
		}
		if err != nil {
			respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取账号失败", Code: "history_read_failed"})
			return
		}
		if r.Method == http.MethodGet {
			normalizeHistoryLoginFailure(&account)
			meta, err := loginHistory.CredentialMetadata(r.Context(), account.ID)
			if err != nil {
				respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取登录资料状态失败", Code: "credential_read_failed"})
				return
			}
			attempts, err := loginHistory.ListAttempts(r.Context(), account.Email, 100)
			if err != nil {
				respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取登录历史失败", Code: "history_read_failed"})
				return
			}
			for i := range attempts {
				info := login.NormalizeErrorInfo(login.LoginErrorInfo{Stage: attempts[i].Stage, Code: attempts[i].Code, Message: attempts[i].Message, HTTPStatus: attempts[i].HTTPStatus, Retryable: attempts[i].Retryable})
				attempts[i].Stage, attempts[i].Code, attempts[i].Message, attempts[i].Retryable = info.Stage, info.Code, info.Message, info.Retryable
			}
			destination := ""
			if sub2Importer != nil && sub2Importer.configured() {
				destination = sub2Importer.destinationKey
			}
			checks, checksErr := loginHistory.ListAccountChecks(r.Context(), account.ID, 20, destination)
			recoveryHistory, recoveryHistoryTruncated, recoveryHistoryErr := loginHistory.ListAccountRecoveryHistory(r.Context(), account.ID, 100, destination)
			recoveryFailures, recoveryFailuresTruncated, recoveryFailuresErr := loginHistory.ListAccountRecoveryFailureEvents(r.Context(), account.ID, 100, destination)
			lifecycleEvents, lifecycleErr := loginHistory.ListLifecycleEventsForAccount(r.Context(), account.ID, 100)
			respondJSON(w, historyDetailResponse{Success: true, Account: account, Attempts: attempts, CredentialsMeta: meta,
				RecoveryHistory: recoveryHistory, RecoveryHistoryAvailable: recoveryHistoryErr == nil,
				RecoveryHistoryTruncated: recoveryHistoryTruncated, RecoveryFailures: recoveryFailures,
				RecoveryFailuresAvailable: recoveryFailuresErr == nil, RecoveryFailuresTruncated: recoveryFailuresTruncated,
				Checks: checks, ChecksAvailable: checksErr == nil, LifecycleEvents: lifecycleEvents, LifecycleEventsAvailable: lifecycleErr == nil})
			return
		}
		if err := loginHistory.DeleteAccountByID(r.Context(), id); errors.Is(err, store.ErrAccountBusy) {
			respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: "账号有进行中或待确认的登录、导入任务，请结束或核对后再删除", Code: "account_busy"})
			return
		} else if errors.Is(err, store.ErrAccountDiscarded) {
			respondJSONStatus(w, http.StatusGone, LoginResponse{Message: "本地退役记录已保留", Code: "account_discarded"})
			return
		} else if errors.Is(err, store.ErrAccountOfficialUnavailable) {
			respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: "官方终态记录已保留", Code: "account_official_unavailable"})
			return
		} else if errors.Is(err, store.ErrAccountNotFound) {
			respondJSONStatus(w, http.StatusNotFound, LoginResponse{Message: "账号不存在", Code: "account_not_found"})
			return
		} else if err != nil {
			respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "删除账号失败", Code: "history_delete_failed"})
			return
		}
		respondJSON(w, map[string]interface{}{"success": true, "id": id})
	}
}

// Project legacy saved codes through the same classifier as new logins.
// This does not rewrite historical evidence or infer a verification method.
func normalizeHistoryLoginFailure(account *store.Account) {
	info := login.NormalizeErrorInfo(login.LoginErrorInfo{Code: account.LastErrorCode, Message: account.LastError, HTTPStatus: account.LastHTTPStatus})
	account.LastErrorCode, account.LastError = info.Code, info.Message
}

func handleHistoryLogin(service *login.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
			return
		}
		limitConcurrentLogins(loginSlots, func(w http.ResponseWriter, r *http.Request) {
			if loginHistory == nil {
				respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "历史记录未初始化", Code: "history_unavailable"})
				return
			}
			mediaType, _, err := mime.ParseMediaType(r.Header.Get("Content-Type"))
			if err != nil || mediaType != "application/json" {
				respondJSONStatus(w, http.StatusUnsupportedMediaType, LoginResponse{Message: "请求必须使用 application/json"})
				return
			}
			r.Body = http.MaxBytesReader(w, r.Body, 8<<10)
			var req historyLoginRequest
			decoder := json.NewDecoder(r.Body)
			if err := decoder.Decode(&req); err != nil || req.AccountID <= 0 {
				respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "account_id 必须是正整数", Code: "invalid_account_id"})
				return
			}
			if err := decoder.Decode(new(interface{})); err != io.EOF {
				respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "请求只能包含一个 JSON 对象"})
				return
			}
			account, err := loginHistory.GetAccountByID(r.Context(), req.AccountID)
			if err == nil && (account.Status == "discarded" || account.Sub2Policy == store.Sub2PolicyDiscarded) {
				respondJSONStatus(w, http.StatusGone, LoginResponse{Message: "账号已在本地退役", Code: "account_discarded"})
				return
			}
			if errors.Is(err, store.ErrAccountNotFound) || err == nil && account.IsOfficialUnavailable() {
				respondJSONStatus(w, http.StatusNotFound, LoginResponse{Message: "账号不存在或已删除", Code: "account_not_found"})
				return
			}
			if err != nil {
				respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取账号失败", Code: "history_read_failed"})
				return
			}
			credentials, err := loginHistory.GetCredentialsByID(r.Context(), req.AccountID)
			if errors.Is(err, store.ErrAccountNotFound) {
				respondJSONStatus(w, http.StatusNotFound, LoginResponse{Message: "账号不存在或已删除", Code: "account_not_found"})
				return
			}
			if err != nil {
				respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取账号凭据失败", Code: "history_read_failed"})
				return
			}
			proxy := strings.TrimSpace(req.Proxy)
			if proxy == "" {
				proxy = credentials.Proxy
			}
			loginReq := LoginRequest{Email: credentials.Email, Password: credentials.Password, TotpSecret: credentials.TOTPSecret, Proxy: proxy, UpstreamProxy: strings.TrimSpace(req.UpstreamProxy), AutoDeliver: req.AutoDeliver, DeliveryOptions: req.DeliveryOptions}
			if loginReq.DeliveryOptions == nil && sub2Importer != nil && sub2Importer.configured() {
				options, err := sub2Importer.historyDeliveryOptions(r.Context(), req.AccountID)
				if err != nil {
					respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取历史交付参数失败"})
					return
				}
				loginReq.DeliveryOptions = options
			}
			if sub2Importer != nil {
				loginReq.Proxy, err = sub2Importer.accountLoginProxy(r.Context(), account, loginReq.DeliveryOptions, loginReq.Proxy, service.CheckProxy)
				if err != nil {
					failure := classifyRecoveryError(err)
					respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: failure.Error(), Code: failure.Code})
					return
				}
			}
			loginReq.UpstreamProxy = ""
			if err := validateLoginRequest(&loginReq); err != nil {
				respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: err.Error()})
				return
			}

			logLogin := func(ctx context.Context) (*login.LoginResult, error) {
				return service.LoginWithProxiesContext(ctx, loginReq.Email, loginReq.Password, loginReq.TotpSecret, loginReq.Proxy, loginReq.UpstreamProxy)
			}
			serveLoginStream(w, r, func(ctx context.Context) (*login.LoginResult, error) {
				if req.ManualAttempt {
					ctx = login.WithSingleAttempt(ctx)
				}
				return runWithHistoryAccount(ctx, req.AccountID, loginReq, logLogin)
			})
		})(w, r)
	}
}

// runWithHistory wraps both JSON and SSE login paths. It uses a fresh context
// for final persistence so a client disconnect cannot erase the attempt.
func runWithHistory(ctx context.Context, req LoginRequest, run func(context.Context) (*login.LoginResult, error)) (*login.LoginResult, error) {
	return runWithHistoryAccount(ctx, 0, req, run)
}

func (s *sub2ImportService) historyDeliveryOptions(ctx context.Context, accountID int64) (*store.DeliveryOptions, error) {
	deliveries, err := s.store.ListLatestAccountDeliveries(ctx, s.destinationKey)
	if err != nil {
		return nil, err
	}
	for _, delivery := range deliveries {
		if delivery.AccountID == accountID && delivery.DestinationKey == s.destinationKey {
			options := delivery.Options
			return &options, nil
		}
	}
	return nil, nil
}

// Workspace membership can change on a legitimate relogin. Only compare a
// subject when both access tokens use the same issuer's subject namespace.
func verifyStoredOAuthSubject(ctx context.Context, history *store.Store, accountID int64, accessToken string) error {
	current, err := login.ParseJWT(accessToken)
	if err != nil || current.Issuer == "" || current.Subject == "" {
		return nil
	}
	_, saved, err := history.GetOAuthResultByID(ctx, accountID)
	if errors.Is(err, store.ErrOAuthCredentialsMissing) {
		return nil
	}
	if err != nil {
		return err
	}
	previous, err := login.ParseJWT(saved.AccessToken)
	if err == nil && previous.Issuer == current.Issuer && previous.Subject != "" && previous.Subject != current.Subject {
		return &recoveryOperationError{Code: "identity_changed", RequiresAction: true}
	}
	return nil
}

func runWithHistoryAccount(ctx context.Context, accountID int64, req LoginRequest, run func(context.Context) (*login.LoginResult, error)) (*login.LoginResult, error) {
	if req.AutoDeliver && (loginHistory == nil || sub2Importer == nil || !sub2Importer.configured()) {
		return nil, &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	if loginHistory == nil {
		return run(ctx)
	}
	var options store.DeliveryOptions
	importer := sub2Importer
	if req.AutoDeliver {
		requested := req.DeliveryOptions
		if requested == nil && accountID > 0 {
			var err error
			requested, err = sub2Importer.historyDeliveryOptions(ctx, accountID)
			if err != nil {
				return nil, err
			}
		}
		var err error
		options, err = sub2Importer.loadOptions(ctx, requested)
		if err != nil {
			return nil, &recoveryOperationError{Code: "configuration", RequiresAction: true}
		}
	}
	persistCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	_, attempt, err := loginHistory.BeginAttempt(persistCtx, store.Credentials{Email: req.Email, Password: req.Password, TOTPSecret: req.TotpSecret, Proxy: req.Proxy, ExistingAccountID: accountID, SingleAttempt: login.IsSingleAttempt(ctx)})
	if err != nil {
		return nil, err
	}
	defer attempt.Release()
	expectedCredentialVersion, versionErr := loginHistory.GetAccountCredentialVersion(persistCtx, attempt.AccountID)
	result, loginErr := run(ctx)
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if loginErr == nil && result != nil {
		loginErr = verifyStoredOAuthSubject(finishCtx, loginHistory, attempt.AccountID, result.AccessToken)
	}
	if loginErr != nil {
		info := describeLoginError(loginErr)
		failure := &store.Failure{Stage: info.Stage, Code: info.Code, HTTPStatus: info.HTTPStatus, Retryable: info.Retryable, Message: info.Message, AccountStatus: info.AccountStatus}
		if err := loginHistory.FinishAttempt(finishCtx, attempt.ID, false, nil, failure); err != nil {
			return nil, fmt.Errorf("save failed login history: %w", err)
		}
		account, err := loginHistory.GetAccountByID(finishCtx, attempt.AccountID)
		if err != nil {
			return nil, fmt.Errorf("read saved account status: %w", err)
		}
		if errors.Is(account.Sub2EligibilityError(), store.ErrAccountDiscarded) {
			return nil, store.ErrAccountDiscarded
		}
		if accountDeactivationConfirmed(info.Code) && versionErr == nil {
			// FinishAttempt releases the database state, but the per-email attempt
			// lock remains until attempt.Release below. Release it before the
			// cleanup acquires the account recovery lease.
			attempt.Release()
			cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 10*time.Second)
			if err := sub2Importer.deleteDeletedAccount(cleanupCtx, attempt.AccountID, expectedCredentialVersion); err != nil {
				log.Printf("检测确认账号已停用，但清理 Sub2 账号失败: account_id=%d", attempt.AccountID)
			}
			cleanupCancel()
		}
		return result, loginErr
	}
	if result == nil {
		failure := &store.Failure{Stage: login.LoginStageUnknown, Code: login.LoginErrorUnknown, Message: "登录未返回结果"}
		if err := loginHistory.FinishAttempt(finishCtx, attempt.ID, false, nil, failure); err != nil {
			return nil, fmt.Errorf("save empty login history: %w", err)
		}
		return nil, fmt.Errorf("login returned no result")
	}
	stored := &store.Result{AccessToken: result.AccessToken, RefreshToken: result.RefreshToken, ChatGPTAccountID: result.ChatGPTAccountID, OrganizationID: result.OrganizationID, PlanType: result.PlanType, ExpiresAt: result.ExpiresAt, ExpiresIn: result.ExpiresIn}
	result.AccountID = attempt.AccountID
	var delivery store.AccountDelivery
	switch {
	case req.AutoDeliver:
		delivery, err = loginHistory.FinishAttemptAndQueueDelivery(finishCtx, attempt.ID, stored, importer.destinationKey, options)
	case accountID > 0 && importer.configured():
		delivery, err = loginHistory.FinishBoundRelogin(finishCtx, attempt.ID, stored, importer.destinationKey)
	default:
		err = loginHistory.FinishAttempt(finishCtx, attempt.ID, true, stored, nil)
	}
	if err != nil {
		return nil, fmt.Errorf("save login history: %w", err)
	}
	savedAccount, err := loginHistory.GetAccountByID(finishCtx, attempt.AccountID)
	if err != nil {
		return nil, fmt.Errorf("read saved account status: %w", err)
	}
	if savedAccount.Status == "discarded" || savedAccount.Sub2Policy == store.Sub2PolicyDiscarded {
		return nil, store.ErrAccountDiscarded
	}
	if delivery.State != "" {
		result.Delivery = delivery
	}
	return result, nil
}

func describeLoginError(err error) login.LoginErrorInfo {
	if errors.Is(err, store.ErrAccountDiscarded) {
		return login.LoginErrorInfo{Stage: login.LoginStageConfiguration, Code: "account_discarded", Message: "账号已在本地退役"}
	}
	if errors.Is(err, store.ErrAccountBusy) {
		return login.LoginErrorInfo{Stage: login.LoginStageConfiguration, Code: "account_busy", Retryable: true, Message: "该账号正在登录，请稍后重试"}
	}
	if errors.Is(err, store.ErrAccountNotFound) {
		return login.LoginErrorInfo{Stage: login.LoginStageConfiguration, Code: "account_not_found", Message: "账号不存在"}
	}
	return login.DescribeError(err)
}
