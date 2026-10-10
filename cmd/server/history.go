package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
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
	DeliveryOptions *store.DeliveryOptions `json:"delivery_options,omitempty"`
	AutoDeliver     bool                   `json:"auto_deliver"`
	AccountID       int64                  `json:"account_id"`
	Proxy           string                 `json:"proxy"`
	UpstreamProxy   string                 `json:"upstream_proxy"`
}

type historyListResponse struct {
	Success          bool                                   `json:"success"`
	Data             []store.Account                        `json:"data"`
	Imports          []store.Sub2Import                     `json:"imports,omitempty"`
	Sub2Statuses     map[int64]sub2AccountStatus            `json:"sub2_statuses,omitempty"`
	Sub2Configured   bool                                   `json:"sub2_configured"`
	ImportsAvailable bool                                   `json:"imports_available"`
	ChecksAvailable  bool                                   `json:"checks_available"`
	Checks           map[int64]store.AccountCheckSummary    `json:"checks"`
	Recoveries       map[int64]store.AccountRecoveryTask    `json:"recoveries"`
	Deliveries       map[int64]store.AccountDelivery        `json:"deliveries"`
	Rechecks         map[int64]store.AccountRecoveryRecheck `json:"rechecks"`
	Repairs          map[int64]store.CredentialRepair       `json:"repairs"`
}

type historyDetailResponse struct {
	Success                  bool                        `json:"success"`
	Account                  store.Account               `json:"account"`
	Attempts                 []store.AttemptRecord       `json:"attempts"`
	RecoveryHistory          []store.AccountRecoveryTask `json:"recovery_history"`
	RecoveryHistoryAvailable bool                        `json:"recovery_history_available"`
	RecoveryHistoryTruncated bool                        `json:"recovery_history_truncated"`
	Checks                   []store.AccountCheck        `json:"checks"`
	ChecksAvailable          bool                        `json:"checks_available"`
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
		respondJSON(w, historyListResponse{Success: true, Data: accounts, Imports: imports, Sub2Statuses: sub2Statuses, Sub2Configured: configured,
			ImportsAvailable: importsErr == nil, ChecksAvailable: checksErr == nil, Checks: checks, Recoveries: recoveries, Deliveries: deliveries, Rechecks: rechecks, Repairs: repairs})
	}
}

func handleHistoryDelete() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
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
			attempts, err := loginHistory.ListAttempts(r.Context(), account.Email, 100)
			if err != nil {
				respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取登录历史失败", Code: "history_read_failed"})
				return
			}
			destination := ""
			if sub2Importer != nil && sub2Importer.configured() {
				destination = sub2Importer.destinationKey
			}
			checks, checksErr := loginHistory.ListAccountChecks(r.Context(), account.ID, 20, destination)
			recoveryHistory, recoveryHistoryTruncated, recoveryHistoryErr := loginHistory.ListAccountRecoveryHistory(r.Context(), account.ID, 100, destination)
			respondJSON(w, historyDetailResponse{Success: true, Account: account, Attempts: attempts,
				RecoveryHistory: recoveryHistory, RecoveryHistoryAvailable: recoveryHistoryErr == nil,
				RecoveryHistoryTruncated: recoveryHistoryTruncated, Checks: checks, ChecksAvailable: checksErr == nil})
			return
		}
		if err := loginHistory.DeleteAccountByID(r.Context(), id); errors.Is(err, store.ErrAccountBusy) {
			respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: "账号有进行中或待确认的登录、导入任务，请结束或核对后再删除", Code: "account_busy"})
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
			if delay := limiter.reserve(getClientIP(r)); delay > 0 {
				seconds := int((delay + time.Second - 1) / time.Second)
				w.Header().Set("Retry-After", strconv.Itoa(seconds))
				respondJSONStatus(w, http.StatusTooManyRequests, LoginResponse{Success: false, Message: "平台请求达到上限，等待后继续", Code: "platform_rate_limit", RetryAfter: seconds})
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
			if err := validateLoginRequest(&loginReq); err != nil {
				respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: err.Error()})
				return
			}

			logLogin := func(ctx context.Context) (*login.LoginResult, error) {
				return service.LoginWithProxiesContext(ctx, loginReq.Email, loginReq.Password, loginReq.TotpSecret, loginReq.Proxy, loginReq.UpstreamProxy)
			}
			serveLoginStream(w, r, func(ctx context.Context) (*login.LoginResult, error) {
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

func runWithHistoryAccount(ctx context.Context, accountID int64, req LoginRequest, run func(context.Context) (*login.LoginResult, error)) (*login.LoginResult, error) {
	if req.AutoDeliver && (loginHistory == nil || sub2Importer == nil || !sub2Importer.configured()) {
		return nil, &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	if loginHistory == nil {
		return run(ctx)
	}
	var options store.DeliveryOptions
	if req.AutoDeliver {
		requested := req.DeliveryOptions
		if requested == nil && accountID > 0 {
			deliveries, err := loginHistory.ListLatestAccountDeliveries(ctx, sub2Importer.destinationKey)
			if err != nil {
				return nil, err
			}
			for _, d := range deliveries {
				if d.AccountID == accountID && d.DestinationKey == sub2Importer.destinationKey {
					copy := d.Options
					requested = &copy
					break
				}
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
	_, attempt, err := loginHistory.BeginAttempt(persistCtx, store.Credentials{Email: req.Email, Password: req.Password, TOTPSecret: req.TotpSecret, Proxy: req.Proxy, ExistingAccountID: accountID})
	if err != nil {
		return nil, err
	}
	defer attempt.Release()
	result, loginErr := run(ctx)
	finishCtx, finishCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer finishCancel()
	if loginErr != nil {
		info := describeLoginError(loginErr)
		failure := &store.Failure{Stage: info.Stage, Code: info.Code, HTTPStatus: info.HTTPStatus, Retryable: info.Retryable, Message: info.Message, AccountStatus: info.AccountStatus}
		if err := loginHistory.FinishAttempt(finishCtx, attempt.ID, false, nil, failure); err != nil {
			return nil, fmt.Errorf("save failed login history: %w", err)
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
	if req.AutoDeliver {
		delivery, err := loginHistory.FinishAttemptAndQueueDelivery(finishCtx, attempt.ID, stored, sub2Importer.destinationKey, options)
		if err != nil {
			return nil, fmt.Errorf("save login and delivery: %w", err)
		}
		result.Delivery = delivery
	} else if err := loginHistory.FinishAttempt(finishCtx, attempt.ID, true, stored, nil); err != nil {
		return nil, fmt.Errorf("save login history: %w", err)
	}
	return result, nil
}

func describeLoginError(err error) login.LoginErrorInfo {
	if errors.Is(err, store.ErrAccountBusy) {
		return login.LoginErrorInfo{Stage: login.LoginStageConfiguration, Code: "account_busy", Retryable: true, Message: "该账号正在登录，请稍后重试"}
	}
	if errors.Is(err, store.ErrAccountNotFound) {
		return login.LoginErrorInfo{Stage: login.LoginStageConfiguration, Code: "account_not_found", Message: "账号不存在"}
	}
	return login.DescribeError(err)
}
