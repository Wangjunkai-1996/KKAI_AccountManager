package main

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

const sub2OAuthClientID = "app_EMoamEEZ73f0CkXaXp7hrann"

type sub2ImportService struct {
	store          *store.Store
	baseURL        string
	adminAPIKey    string
	destinationKey string
	client         *http.Client
	workerMu       sync.Mutex
	lifecycleOnce  sync.Once
	startOnce      sync.Once
	stopOnce       sync.Once
	ctx            context.Context
	cancel         context.CancelFunc
	wg             sync.WaitGroup
	statusMu       sync.Mutex
	statusCache    map[int64]sub2DetailCacheEntry
	nameCache      map[string]sub2DisplayName
	costMu         sync.Mutex
	costCache      map[int64]sub2CostCacheEntry
	costSlots      chan struct{}
}

// The history page polls frequently. Cache successful read-only detail
// responses briefly so each refresh does not fan out one request per account.
const sub2StatusCacheTTL = 10 * time.Second
const sub2StatusCacheLimit = 1024

type sub2StatusCacheContextKey struct{}

func withSub2StatusCache(ctx context.Context) context.Context {
	return context.WithValue(ctx, sub2StatusCacheContextKey{}, true)
}

// Monitoring and recovery decisions must observe the current remote state;
// only the frequently polled history view opts into the short cache.
func withSub2StatusFresh(ctx context.Context) context.Context {
	return context.WithValue(ctx, sub2StatusCacheContextKey{}, false)
}

func sub2StatusCacheEnabled(ctx context.Context) bool {
	value, _ := ctx.Value(sub2StatusCacheContextKey{}).(bool)
	return value
}

type sub2DetailCacheEntry struct {
	detail    map[string]any
	code      int
	expiresAt time.Time
}

var sub2Importer *sub2ImportService

func newSub2ImportService(history *store.Store) *sub2ImportService {
	base := strings.TrimSpace(os.Getenv("SUB2API_BASE_URL"))
	normalized, err := normalizeSub2BaseURL(base)
	if err != nil {
		normalized = ""
	}
	instanceID := strings.TrimSpace(os.Getenv("AUTH_INSTANCE_ID"))
	if instanceID == "" {
		instanceID = "default"
	}
	key := instanceID
	if normalized != "" {
		key = normalized + "#" + instanceID
	}
	return &sub2ImportService{
		store:          history,
		baseURL:        normalized,
		adminAPIKey:    strings.TrimSpace(os.Getenv("SUB2API_ADMIN_API_KEY")),
		destinationKey: key,
		client: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
		statusCache: make(map[int64]sub2DetailCacheEntry),
	}
}

func (s *sub2ImportService) initLifecycle() {
	if s == nil {
		return
	}
	s.lifecycleOnce.Do(func() {
		s.ctx, s.cancel = context.WithCancel(context.Background())
	})
}

func normalizeSub2BaseURL(raw string) (string, error) {
	if strings.TrimSpace(raw) == "" {
		return "", nil
	}
	u, err := url.Parse(strings.TrimRight(strings.TrimSpace(raw), "/"))
	if err != nil || u.Scheme == "" || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return "", errors.New("SUB2API_BASE_URL 必须是 http(s) URL")
	}
	path := strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(path, "/api/v1") {
		path += "/api/v1"
	}
	u.Path = path
	u.RawQuery = ""
	u.Fragment = ""
	return strings.TrimRight(u.String(), "/"), nil
}

func (s *sub2ImportService) configured() bool {
	return s != nil && s.store != nil && s.baseURL != "" && s.adminAPIKey != ""
}

func (s *sub2ImportService) Start() {
	if s == nil || s.store == nil {
		return
	}
	s.initLifecycle()
	s.startOnce.Do(func() {
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			ticker := time.NewTicker(2 * time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-s.ctx.Done():
					return
				case <-ticker.C:
					s.runOnce(s.ctx)
				}
			}
		}()
	})
}

// Stop cancels the importer worker and waits for an in-flight request to
// observe the cancellation. It is safe to call more than once.
func (s *sub2ImportService) Stop() {
	if s == nil {
		return
	}
	s.initLifecycle()
	s.stopOnce.Do(func() {
		s.cancel()
		s.wg.Wait()
	})
}

type sub2DataImportRequest struct {
	Data                 sub2DataImportData `json:"data"`
	SkipDefaultGroupBind bool               `json:"skip_default_group_bind"`
}

type sub2DataImportData struct {
	Type     string            `json:"type"`
	Version  int               `json:"version"`
	Proxies  []any             `json:"proxies"`
	Accounts []sub2DataAccount `json:"accounts"`
}

type sub2DataAccount struct {
	Name               string              `json:"name"`
	Platform           string              `json:"platform"`
	Type               string              `json:"type"`
	Credentials        map[string]any      `json:"credentials"`
	Extra              map[string]any      `json:"extra,omitempty"`
	Concurrency        int                 `json:"concurrency"`
	Priority           int                 `json:"priority"`
	RateMultiplier     int                 `json:"rate_multiplier"`
	AutoPauseOnExpired bool                `json:"auto_pause_on_expired"`
	EgressMode         string              `json:"egress_mode,omitempty"`
	EgressPool         *sub2DataEgressPool `json:"egress_pool,omitempty"`
}

type sub2DataEgressPool struct {
	RouteKeys            []string `json:"route_keys"`
	PrimaryRouteKey      string   `json:"primary_route_key"`
	ConcurrencyPerEgress int      `json:"concurrency_per_egress"`
}

type sub2ImportEnvelope struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data"`
}

type sub2CreateDataResult struct {
	AccountCreated int `json:"account_created"`
	AccountFailed  int `json:"account_failed"`
	Errors         []struct {
		Kind    string `json:"kind"`
		Name    string `json:"name"`
		Message string `json:"message"`
	} `json:"errors"`
	Warnings []struct {
		Code string `json:"code"`
	} `json:"warnings"`
}

func newOperationID() (string, error) {
	var bytes [16]byte
	if _, err := rand.Read(bytes[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes[:]), nil
}

func (s *sub2ImportService) buildPayload(accountID int64, options ...store.DeliveryOptions) ([]byte, string, string, error) {
	operationID, err := newOperationID()
	if err != nil {
		return nil, "", "", err
	}
	return s.buildPayloadWithOperation(accountID, operationID, options...)
}

func (s *sub2ImportService) buildPayloadWithOperation(accountID int64, operationID string, options ...store.DeliveryOptions) ([]byte, string, string, error) {
	opt := store.DefaultDeliveryOptions()
	if len(options) > 0 {
		opt = options[0]
	}
	if err := opt.ValidateForImport(); err != nil {
		return nil, "", "", err
	}
	var routes []resolvedEgressRoute
	if len(opt.EgressRouteIDs) > 0 {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		var err error
		routes, err = s.resolveEgressRoutes(ctx, opt.EgressRouteIDs)
		if err != nil {
			return nil, "", "", err
		}
	}
	email, result, err := s.store.GetOAuthResultByID(context.Background(), accountID)
	if err != nil {
		return nil, "", "", err
	}
	// Keep the display name human-readable; the operation marker remains in
	// `extra.kkai_auth_import` and is used for reconciliation/idempotency.
	name := opt.Merchant + " | " + opt.Deadline + " | " + email
	if len([]rune(name)) > 100 {
		name = string([]rune(name)[:100])
	}
	credentials := map[string]any{
		"access_token":       result.AccessToken,
		"refresh_token":      result.RefreshToken,
		"client_id":          sub2OAuthClientID,
		"email":              email,
		"chatgpt_account_id": result.ChatGPTAccountID,
		"organization_id":    result.OrganizationID,
		"plan_type":          result.PlanType,
		"expires_at":         result.ExpiresAt,
	}
	extra := map[string]any{
		"email":                  email,
		"codex_fingerprint_mode": "full",
		"kkai_auth_import": map[string]any{
			"source_instance_id": strings.TrimSpace(os.Getenv("AUTH_INSTANCE_ID")),
			"source_account_id":  accountID,
			"operation_id":       operationID,
			"target_group_ids":   opt.GroupIDs,
		},
	}
	account := sub2DataAccount{
		Name: name, Platform: "openai", Type: "oauth", Credentials: credentials, Extra: extra,
		Concurrency: opt.Concurrency, Priority: opt.Priority, RateMultiplier: 1, AutoPauseOnExpired: true,
	}
	version := 1
	if len(routes) > 0 {
		keys := make([]string, len(routes))
		for i, route := range routes {
			keys[i] = route.RouteKey
		}
		version = 2
		account.EgressMode = "pool"
		account.EgressPool = &sub2DataEgressPool{
			RouteKeys: keys, PrimaryRouteKey: keys[0], ConcurrencyPerEgress: opt.Concurrency,
		}
		extra["kkai_auth_import"].(map[string]any)["target_route_ids"] = append([]int64(nil), opt.EgressRouteIDs...)
	}
	request := sub2DataImportRequest{
		SkipDefaultGroupBind: true,
		Data: sub2DataImportData{
			Type: "sub2api-data", Version: version, Proxies: []any{},
			Accounts: []sub2DataAccount{account},
		},
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, "", "", err
	}
	idempotencyKey := "kkai-auth/" + s.destinationKey + "/" + strconv.FormatInt(accountID, 10) + "/" + operationID
	return payload, operationID, idempotencyKey, nil
}

func (s *sub2ImportService) runOnce(ctx context.Context) {
	if !s.configured() || !s.workerMu.TryLock() {
		return
	}
	defer s.workerMu.Unlock()
	cleanupTasks, err := s.store.ListPendingSub2Cleanup(ctx, s.destinationKey, 1)
	if err != nil {
		return
	}
	if len(cleanupTasks) > 0 {
		lease, leaseErr := s.store.AcquireAccountRecovery(ctx, cleanupTasks[0].AccountID)
		if leaseErr == nil {
			task := cleanupTasks[0]
			if err := s.deleteDeletedAccountWithLease(ctx, lease, task.AccountID, task.CredentialVersion, task); err != nil {
				s.markSub2CleanupPending(ctx, task, err)
			}
			lease.Release()
			return
		}
	}
	tasks, err := s.store.ListQueuedSub2ImportsForDestination(ctx, s.destinationKey, 1)
	if err != nil || len(tasks) == 0 {
		return
	}
	lease, err := s.store.AcquireAccountRecovery(ctx, tasks[0].AccountID)
	if err != nil {
		return
	}
	defer lease.Release()
	_ = s.processQueued(ctx, tasks[0])
}

func (s *sub2ImportService) processQueued(ctx context.Context, task store.Sub2Import) error {
	current, err := s.store.GetSub2ImportByID(ctx, task.ID)
	if err != nil {
		return err
	}
	if current.State == store.Sub2RemoteMissing {
		return store.ErrSub2AccountMissing
	}
	if current.OperationID != task.OperationID {
		return store.ErrAccountRecoveryStale
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		return err
	}
	if err := account.Sub2EligibilityError(); err != nil {
		return err
	}
	if !store.IsSub2PaidPlan(account.PlanType) {
		return store.ErrSub2PlanIneligible
	}
	if err := s.importMissing(ctx, task.AccountID); err != nil {
		return err
	}
	if task.DestinationKey != s.destinationKey {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	currentVersion, err := s.store.GetAccountCredentialVersion(ctx, task.AccountID)
	if err != nil {
		return err
	}
	if currentVersion != task.CredentialVersion {
		if err := s.refreshSub2ImportCredentials(ctx, task); err != nil {
			return err
		}
		task, err = s.store.GetSub2ImportByID(ctx, task.ID)
		if err != nil {
			return err
		}
	}
	if err := s.store.MarkSub2ImportSendingForOperation(ctx, task); err != nil {
		return err
	}
	if currentVersion, err = s.store.GetAccountCredentialVersion(ctx, task.AccountID); err != nil || currentVersion != task.CredentialVersion {
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "queued", false, 0, "登录凭据版本已变化，已等待刷新导入任务")
		return errors.New("登录凭据版本已变化，阻止旧导入任务发送")
	}
	payload, err := s.store.GetSub2ImportPayload(ctx, task.ID)
	if err != nil {
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "failed", false, 0, "读取导入任务失败")
		return err
	}
	status, response, err := s.postImport(ctx, task, payload)
	if err != nil {
		state := "unknown"
		if status >= 400 && status < 500 {
			state = "failed"
		}
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, state, false, 0, err.Error())
		return err
	}
	if response.Code != 0 {
		failure := &recoveryOperationError{Code: "request_rejected", RequiresAction: true}
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "failed", false, 0, failure.Error())
		return failure
	}
	var result sub2CreateDataResult
	if len(response.Data) > 0 {
		_ = json.Unmarshal(response.Data, &result)
	}
	if result.AccountFailed > 0 || result.AccountCreated < 1 {
		failure := &recoveryOperationError{Code: "request_rejected", RequiresAction: true}
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "failed", false, 0, failure.Error())
		return failure
	}
	if len(result.Warnings) > 0 {
		var request sub2DataImportRequest
		if json.Unmarshal(payload, &request) == nil && len(request.Data.Accounts) == 1 && request.Data.Accounts[0].EgressPool != nil {
			for _, warning := range result.Warnings {
				if warning.Code == "EGRESS_POOL_NOT_RESTORED" {
					failure := &recoveryOperationError{Code: "egress_mismatch", RequiresAction: true}
					_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", true, 0, failure.Error())
					return failure
				}
			}
		}
	}
	_ = s.store.UpdateSub2ImportForOperation(ctx, task, "confirming", true, 0, "")
	return s.reconcile(ctx, task.ID)
}

// refreshSub2ImportCredentials keeps the original operation marker and
// display/options payload while replacing only the OAuth snapshot.
func (s *sub2ImportService) refreshSub2ImportCredentials(ctx context.Context, task store.Sub2Import) error {
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		return err
	}
	if err := account.Sub2EligibilityError(); err != nil {
		return err
	}
	payload, err := s.store.GetSub2ImportPayload(ctx, task.ID)
	if err != nil {
		return err
	}
	var request sub2DataImportRequest
	if err := json.Unmarshal(payload, &request); err != nil || len(request.Data.Accounts) != 1 {
		return errors.New("Sub2 导入任务格式无效，无法刷新凭据")
	}
	email, result, err := s.store.GetOAuthResultByID(ctx, task.AccountID)
	if err != nil {
		return err
	}
	if !store.IsSub2PaidPlan(result.PlanType) {
		return store.ErrSub2PlanIneligible
	}
	request.Data.Accounts[0].Credentials = map[string]any{
		"access_token": result.AccessToken, "refresh_token": result.RefreshToken,
		"client_id": sub2OAuthClientID, "email": email,
		"chatgpt_account_id": result.ChatGPTAccountID, "organization_id": result.OrganizationID,
		"plan_type": result.PlanType, "expires_at": result.ExpiresAt,
	}
	if request.Data.Accounts[0].Extra == nil {
		request.Data.Accounts[0].Extra = map[string]any{}
	}
	request.Data.Accounts[0].Extra["email"] = email
	refreshed, err := json.Marshal(request)
	if err != nil {
		return err
	}
	version, err := s.store.GetAccountCredentialVersion(ctx, task.AccountID)
	if err != nil {
		return err
	}
	return s.store.RefreshSub2ImportPayloadForVersion(ctx, task.ID, version, true, refreshed)
}

func (s *sub2ImportService) postImport(ctx context.Context, task store.Sub2Import, payload []byte) (int, sub2ImportEnvelope, error) {
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/admin/accounts/data", strings.NewReader(string(payload)))
	if err != nil {
		return 0, sub2ImportEnvelope{}, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", s.adminAPIKey)
	request.Header.Set("Idempotency-Key", task.IdempotencyKey)
	response, err := s.client.Do(request)
	if err != nil {
		return 0, sub2ImportEnvelope{}, err
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, sub2ImportEnvelope{}, recoveryHTTPError(response.StatusCode, recoveryRetryAfter(response.Header.Get("Retry-After")))
	}
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if readErr != nil {
		return response.StatusCode, sub2ImportEnvelope{}, readErr
	}
	var envelope sub2ImportEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return response.StatusCode, envelope, fmt.Errorf("Sub2 返回格式无效（HTTP %d）", response.StatusCode)
	}
	return response.StatusCode, envelope, nil
}

type sub2ListResponse struct {
	Items    []map[string]any `json:"items"`
	Total    int              `json:"total"`
	Pages    int              `json:"pages"`
	Page     int              `json:"page"`
	PageSize int              `json:"page_size"`
}

// sub2AccountStatus is the latest read-only view of an AUTH account in Sub2.
// It intentionally lives in the response layer rather than the import task
// state: an import task records delivery, while this snapshot records the
// current remote account state. A failed lookup is marked stale/unknown so a
// transient management API failure cannot be mistaken for a deleted account.
type sub2AccountStatus struct {
	Name                    string     `json:"name,omitempty"`
	NameSource              string     `json:"name_source,omitempty"`
	NameStale               bool       `json:"name_stale"`
	Exists                  bool       `json:"exists"`
	Imported                bool       `json:"imported"`
	Sub2AccountID           int64      `json:"sub2_account_id,omitempty"`
	Status                  string     `json:"status,omitempty"`
	Schedulable             *bool      `json:"schedulable,omitempty"`
	EffectiveSchedulable    *bool      `json:"effective_schedulable,omitempty"`
	ErrorMessage            string     `json:"error_message,omitempty"`
	RateLimitedAt           any        `json:"rate_limited_at,omitempty"`
	RateLimitResetAt        any        `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil           any        `json:"overload_until,omitempty"`
	TempUnschedulableUntil  any        `json:"temp_unschedulable_until,omitempty"`
	TempUnschedulableReason string     `json:"temp_unschedulable_reason,omitempty"`
	ExpiresAt               any        `json:"expires_at,omitempty"`
	CheckedAt               time.Time  `json:"checked_at"`
	Stale                   bool       `json:"stale,omitempty"`
	Error                   string     `json:"error,omitempty"`
	Unknown                 bool       `json:"unknown,omitempty"`
	AssociationStatus       string     `json:"association_status,omitempty"`
	Quota                   *sub2Quota `json:"quota,omitempty"`
	UsageUnknown            bool       `json:"usage_unknown,omitempty"`
	UsageStale              bool       `json:"usage_stale,omitempty"`
	UsageError              string     `json:"usage_error,omitempty"`
}

type sub2UsageWindow struct {
	UsedPercent       *float64 `json:"used_percent,omitempty"`
	ResetAt           any      `json:"reset_at,omitempty"`
	ResetAfterSeconds *float64 `json:"reset_after_seconds,omitempty"`
	WindowMinutes     *float64 `json:"window_minutes,omitempty"`
	RemainingSeconds  *float64 `json:"remaining_seconds,omitempty"`
	UsedRequests      *float64 `json:"used_requests,omitempty"`
	LimitRequests     *float64 `json:"limit_requests,omitempty"`
}

type sub2Credits struct {
	Balance    string `json:"balance,omitempty"`
	HasCredits *bool  `json:"has_credits,omitempty"`
	Unlimited  *bool  `json:"unlimited,omitempty"`
}

// sub2Quota is the small, non-sensitive usage snapshot exposed by Sub2's
// account detail response. Credentials, fingerprints and private metadata are
// deliberately excluded.
type sub2Quota struct {
	FiveHour       *sub2UsageWindow `json:"five_hour,omitempty"`
	SevenDay       *sub2UsageWindow `json:"seven_day,omitempty"`
	SevenDaySonnet *sub2UsageWindow `json:"seven_day_sonnet,omitempty"`
	SevenDayFable  *sub2UsageWindow `json:"seven_day_fable,omitempty"`
	ThirtyDay      *sub2UsageWindow `json:"thirty_day,omitempty"`
	Credits        *sub2Credits     `json:"credits,omitempty"`
	UpdatedAt      any              `json:"updated_at,omitempty"`
	Source         string           `json:"source,omitempty"`
}

var (
	errSub2AmbiguousMatch   = errors.New("Sub2 存在多个同邮箱、同账号身份的记录，无法自动关联")
	errSub2IdentityConflict = errors.New("Sub2 同邮箱账号身份不一致，无法自动关联")
)

// syncAccountStatuses shares the same persisted associations between history,
// automatic checks and recovery. An association only reads Sub2; it never
// creates an account or changes remote metadata.
func (s *sub2ImportService) syncAccountStatuses(ctx context.Context) ([]store.Sub2Import, map[int64]sub2AccountStatus, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	accounts, err := s.store.ListAccounts(ctx)
	if err != nil {
		return nil, nil, err
	}
	allImports, err := s.store.ListSub2ImportStatuses(ctx)
	if err != nil {
		return nil, nil, err
	}
	bindings := make(map[int64]store.Sub2Import)
	for _, task := range allImports {
		if task.DestinationKey == s.destinationKey {
			bindings[task.AccountID] = task
		}
	}
	checkedAt := time.Now().UTC()
	statuses := make(map[int64]sub2AccountStatus, len(accounts))
	needsDiscovery := false
	for _, account := range accounts {
		task, bound := bindings[account.ID]
		if account.IsTerminalUnavailable() && !bound {
			status := "deleted"
			if !account.IsOfficialUnavailable() {
				status = "discarded"
			}
			statuses[account.ID] = sub2AccountStatus{CheckedAt: checkedAt, Status: status, AssociationStatus: "unmatched"}
			continue
		}
		if bound && task.State == store.Sub2RemoteMissing {
			statuses[account.ID] = missingSub2Status(task, checkedAt)
			continue
		}
		if !bound {
			missing, readErr := s.store.Sub2AccountMissing(ctx, s.destinationKey, account.ID)
			if readErr != nil {
				return nil, nil, readErr
			}
			if missing {
				statuses[account.ID] = missingSub2Status(task, checkedAt)
				continue
			}
		}
		needsDiscovery = needsDiscovery || !bound
		statuses[account.ID] = sub2AccountStatus{Imported: task.State == "imported", Sub2AccountID: task.Sub2AccountID,
			CheckedAt: checkedAt, Unknown: true, Stale: true, Error: "Sub2 状态查询未完成"}
	}
	var candidates []map[string]any
	var discoveryErr error
	if needsDiscovery {
		// Sub2's search is name-only. A complete list is needed to recognize
		// custom account names and to prove a match is unique across pages.
		candidates, discoveryErr = s.listOAuthAccounts(ctx)
	}
	jobs := make(chan store.Account)
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 8 && i < len(accounts); i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for account := range jobs {
				mu.Lock()
				task, bound := bindings[account.ID]
				mu.Unlock()
				var snapshot sub2AccountStatus
				mu.Lock()
				stopped := statuses[account.ID].AssociationStatus == "missing"
				mu.Unlock()
				if stopped {
					snapshot = missingSub2Status(task, checkedAt)
					s.attachDisplayName(ctx, account, task, &snapshot)
					mu.Lock()
					statuses[account.ID] = snapshot
					mu.Unlock()
					continue
				}
				if !bound {
					if account.IsTerminalUnavailable() {
						continue
					}
					linkErr := discoveryErr
					if linkErr == nil {
						task, linkErr = s.linkExistingAccount(ctx, account, candidates)
					}
					if linkErr != nil {
						snapshot = sub2AccountStatus{CheckedAt: checkedAt, Unknown: true, Stale: true, Error: linkErr.Error()}
						if errors.Is(linkErr, errSub2AmbiguousMatch) {
							snapshot.AssociationStatus = "ambiguous"
						} else if errors.Is(linkErr, errSub2IdentityConflict) {
							snapshot.AssociationStatus = "conflict"
						}
					} else if task.ID == 0 {
						snapshot = sub2AccountStatus{CheckedAt: checkedAt, Unknown: true, AssociationStatus: "unmatched", Error: "Sub2 未找到邮箱和账号身份一致的记录"}
					} else {
						bound = true
						mu.Lock()
						bindings[account.ID] = task
						mu.Unlock()
					}
				}
				if bound {
					if task.Sub2AccountID > 0 {
						snapshot = s.sub2AccountStatus(ctx, task.Sub2AccountID, checkedAt, account)
					} else {
						snapshot = sub2AccountStatus{CheckedAt: checkedAt, Unknown: true, Stale: true, Error: "Sub2 账号尚未完成核对"}
					}
					if snapshot.AssociationStatus == "missing" {
						task.State = store.Sub2RemoteMissing
						mu.Lock()
						bindings[account.ID] = task
						mu.Unlock()
					}
					snapshot.Imported = task.State == "imported"
					if snapshot.Imported && snapshot.AssociationStatus != "missing" {
						snapshot.AssociationStatus = "imported"
						if strings.HasPrefix(task.OperationID, "linked-") {
							snapshot.AssociationStatus = "linked"
						}
					}
				}
				s.attachDisplayName(ctx, account, task, &snapshot)
				mu.Lock()
				statuses[account.ID] = snapshot
				mu.Unlock()
			}
		}()
	}
send:
	for _, account := range accounts {
		select {
		case jobs <- account:
		case <-ctx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	s.attachSub2Usage(ctx, statuses)
	imports := make([]store.Sub2Import, 0, len(bindings))
	for _, account := range accounts {
		if task, ok := bindings[account.ID]; ok {
			imports = append(imports, task)
		}
	}
	return imports, statuses, nil
}

func (s *sub2ImportService) listOAuthAccounts(ctx context.Context) ([]map[string]any, error) {
	query := url.Values{"platform": {"openai"}, "type": {"oauth"}, "lite": {"1"}, "page_size": {"100"}, "sort_by": {"id"}, "sort_order": {"asc"}}
	items := make([]map[string]any, 0)
	seen := make(map[int64]bool)
	for page := 1; ; page++ {
		query.Set("page", strconv.Itoa(page))
		var current sub2ListResponse
		if err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts", query, &current); err != nil {
			return nil, err
		}
		if current.Page != 0 && current.Page != page {
			return nil, errors.New("Sub2 账号列表分页不一致")
		}
		for _, item := range current.Items {
			id := int64(toFloat(item["id"]))
			if id <= 0 || seen[id] {
				return nil, errors.New("Sub2 账号列表不完整，请重新同步")
			}
			seen[id] = true
			items = append(items, item)
		}
		if current.Pages > page || current.Total > len(items) {
			if len(current.Items) == 0 {
				return nil, errors.New("Sub2 账号列表分页不完整")
			}
			continue
		}
		return items, nil
	}
}

func (s *sub2ImportService) linkExistingAccount(ctx context.Context, account store.Account, candidates []map[string]any) (store.Sub2Import, error) {
	if err := account.Sub2EligibilityError(); err != nil {
		return store.Sub2Import{}, err
	}
	if !store.IsSub2PaidPlan(account.PlanType) {
		return store.Sub2Import{}, store.ErrSub2PlanIneligible
	}
	var match map[string]any
	var conflictingIdentity bool
	for _, candidate := range candidates {
		credentials, _ := candidate["credentials"].(map[string]any)
		email, _ := credentials["email"].(string)
		if strings.TrimSpace(email) == "" || !strings.EqualFold(strings.TrimSpace(email), strings.TrimSpace(account.Email)) {
			continue
		}
		if err := verifySub2AccountIdentity(candidate, int64(toFloat(candidate["id"])), account.ID, account); err != nil {
			conflictingIdentity = true
			continue
		}
		if match != nil {
			return store.Sub2Import{}, errSub2AmbiguousMatch
		}
		match = candidate
	}
	if match == nil {
		if conflictingIdentity {
			return store.Sub2Import{}, errSub2IdentityConflict
		}
		return store.Sub2Import{}, nil
	}
	id := int64(toFloat(match["id"]))
	var detail map[string]any
	if err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail); err != nil {
		return store.Sub2Import{}, err
	}
	if err := verifySub2AccountIdentity(detail, id, account.ID, account); err != nil {
		return store.Sub2Import{}, errSub2IdentityConflict
	}
	operationID, err := newOperationID()
	if err != nil {
		return store.Sub2Import{}, err
	}
	return s.store.LinkSub2Account(ctx, s.destinationKey, account.ID, id, "linked-"+operationID)
}

// listSub2AccountStatuses fetches the details for imported accounts in
// parallel. The endpoint is deliberately read-only and does not call Sub2's
// account test action, which can mutate error/scheduling state.
func (s *sub2ImportService) listSub2AccountStatuses(ctx context.Context, imports []store.Sub2Import) map[int64]sub2AccountStatus {
	statuses := make(map[int64]sub2AccountStatus)
	if s == nil || !s.configured() || len(imports) == 0 {
		return statuses
	}

	type item struct {
		accountID int64
		task      store.Sub2Import
	}
	items := make([]item, 0, len(imports))
	checkedAt := time.Now().UTC()
	for _, task := range imports {
		if task.DestinationKey != s.destinationKey {
			continue
		}
		if task.AccountID <= 0 {
			continue
		}
		// Include acknowledged tasks as well, so an import that is waiting for
		// reconciliation remains visible in the history response.
		if current, err := s.store.GetSub2Import(ctx, s.destinationKey, task.AccountID); err == nil && current.State == store.Sub2RemoteMissing {
			statuses[task.AccountID] = missingSub2Status(current, checkedAt)
			continue
		}
		status := sub2AccountStatus{Imported: task.State == "imported", Sub2AccountID: task.Sub2AccountID, CheckedAt: checkedAt}
		status.Unknown, status.Stale, status.Error = true, true, "Sub2 状态查询未完成"
		statuses[task.AccountID] = status
		if task.Sub2AccountID <= 0 {
			status.Unknown = true
			status.Stale = true
			status.Error = "Sub2 账号尚未完成核对"
			statuses[task.AccountID] = status
			continue
		}
		items = append(items, item{accountID: task.AccountID, task: task})
	}
	if len(items) == 0 {
		return statuses
	}
	statusCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Keep a history request bounded even when a user has many imported
	// accounts. A slow/unavailable Sub2 API cannot hold the history page open
	// for the import client's longer request timeout.
	jobs := make(chan item)
	var mu sync.Mutex
	var wg sync.WaitGroup
	workers := len(items)
	if workers > 8 {
		workers = 8
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for current := range jobs {
				snapshot := s.sub2AccountStatus(statusCtx, current.task.Sub2AccountID, checkedAt)
				snapshot.Imported = current.task.State == "imported"
				mu.Lock()
				statuses[current.accountID] = snapshot
				mu.Unlock()
			}
		}()
	}
send:
	for _, current := range items {
		select {
		case jobs <- current:
		case <-statusCtx.Done():
			break send
		}
	}
	close(jobs)
	wg.Wait()
	s.attachSub2Usage(statusCtx, statuses)
	return statuses
}

func (s *sub2ImportService) sub2AccountStatus(ctx context.Context, id int64, checkedAt time.Time, accounts ...store.Account) sub2AccountStatus {
	status := sub2AccountStatus{Sub2AccountID: id, CheckedAt: checkedAt}
	var detail map[string]any
	bindings, err := s.bindingsForRemoteID(ctx, id)
	if err != nil {
		status.Unknown, status.Stale, status.Error = true, true, "Sub2 绑定读取失败"
		return status
	}
	for _, binding := range bindings {
		if binding.State == store.Sub2RemoteMissing {
			return missingSub2Status(binding, checkedAt)
		}
	}
	if sub2StatusCacheEnabled(ctx) {
		detail, _, err = s.cachedSub2AccountDetail(ctx, id)
	} else {
		_, err = s.apiJSONStatus(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail)
	}
	if err != nil {
		if isSub2NotFound(err) {
			for _, binding := range bindings {
				if missingErr := s.markBindingRemoteMissing(ctx, binding); missingErr != nil {
					status.Unknown, status.Stale, status.Error = true, true, "Sub2 缺失状态保存失败"
					return status
				}
			}
			status.AssociationStatus = "missing"
			status.Exists = false
			status.Error = "Sub2 账号不存在"
			return status
		}
		status.Unknown = true
		status.Stale = true
		status.Error = err.Error()
		return status
	}
	if remoteID := int64(toFloat(detail["id"])); remoteID <= 0 || remoteID != id {
		status.Unknown = true
		status.Stale = true
		status.Error = "Sub2 账号身份核对不一致"
		return status
	}
	for _, account := range accounts {
		binding, bindingErr := s.store.GetSub2Import(ctx, s.destinationKey, account.ID)
		if bindingErr == nil && binding.State == store.Sub2RemoteMissing {
			return missingSub2Status(binding, checkedAt)
		}
		sameOperation := false
		for _, before := range bindings {
			if before.ID == binding.ID && before.OperationID == binding.OperationID {
				sameOperation = true
				break
			}
		}
		if bindingErr != nil || !sameOperation || binding.Sub2AccountID != id || verifySub2RecoveryIdentity(detail, id, account.ID, account, binding) != nil {
			status.Unknown, status.Stale = true, true
			status.Error = "Sub2 账号身份与 AUTH 绑定不一致"
			return status
		}
	}
	status.Exists = true
	if len(accounts) > 0 {
		status.Name, _ = detail["name"].(string)
	}
	status.Status, _ = detail["status"].(string)
	status.ErrorMessage, _ = detail["error_message"].(string)
	status.RateLimitedAt = detail["rate_limited_at"]
	status.RateLimitResetAt = detail["rate_limit_reset_at"]
	status.OverloadUntil = detail["overload_until"]
	status.TempUnschedulableUntil = detail["temp_unschedulable_until"]
	status.TempUnschedulableReason, _ = detail["temp_unschedulable_reason"].(string)
	status.ExpiresAt = detail["expires_at"]
	status.Quota = sub2QuotaSnapshot(detail)
	if value, ok := detail["schedulable"].(bool); ok {
		status.Schedulable = &value
		effective := value
		if strings.EqualFold(strings.TrimSpace(status.Status), "active") {
			now := time.Now()
			for _, key := range []string{"rate_limit_reset_at", "overload_until", "temp_unschedulable_until"} {
				if t, ok := remoteStatusTime(detail[key]); ok && t.After(now) {
					effective = false
				}
			}
			if t, ok := remoteStatusTime(detail["expires_at"]); ok && !t.After(now) {
				effective = false
			}
		} else if !strings.EqualFold(strings.TrimSpace(status.Status), "active") {
			effective = false
		}
		status.EffectiveSchedulable = &effective
	}
	return status
}

func (s *sub2ImportService) cachedSub2AccountDetail(ctx context.Context, id int64) (map[string]any, int, error) {
	if s == nil {
		return nil, 0, errors.New("Sub2 服务未初始化")
	}
	now := time.Now()
	s.statusMu.Lock()
	if entry, ok := s.statusCache[id]; ok && now.Before(entry.expiresAt) {
		detail, code := entry.detail, entry.code
		s.statusMu.Unlock()
		return detail, code, nil
	} else if ok {
		delete(s.statusCache, id)
	}
	s.statusMu.Unlock()

	var detail map[string]any
	code, err := s.apiJSONStatus(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail)
	if err != nil {
		return nil, code, err
	}
	s.statusMu.Lock()
	if s.statusCache == nil {
		s.statusCache = make(map[int64]sub2DetailCacheEntry)
	}
	for cachedID, entry := range s.statusCache {
		if !now.Before(entry.expiresAt) {
			delete(s.statusCache, cachedID)
		}
	}
	if len(s.statusCache) < sub2StatusCacheLimit {
		s.statusCache[id] = sub2DetailCacheEntry{detail: detail, code: code, expiresAt: now.Add(sub2StatusCacheTTL)}
	}
	s.statusMu.Unlock()
	return detail, code, nil
}

// remoteStatusTime accepts the RFC3339 timestamps emitted by Sub2 and keeps a
// small numeric fallback for older installations that serialized Unix seconds
// or milliseconds. Unknown values are ignored so a malformed optional field
// cannot turn a healthy account into a false pause.
func remoteStatusTime(value any) (time.Time, bool) {
	switch v := value.(type) {
	case string:
		if strings.TrimSpace(v) == "" {
			return time.Time{}, false
		}
		for _, layout := range []string{time.RFC3339Nano, time.RFC3339} {
			if parsed, err := time.Parse(layout, v); err == nil {
				return parsed, true
			}
		}
	case float64:
		if v > 1e12 {
			return time.UnixMilli(int64(v)), true
		}
		if v > 0 {
			return time.Unix(int64(v), 0), true
		}
	case json.Number:
		parsed, err := v.Float64()
		if err == nil {
			return remoteStatusTime(parsed)
		}
	}
	return time.Time{}, false
}

func optionalNumber(value any) (*float64, bool) {
	switch value.(type) {
	case float64, json.Number, int, int64:
		parsed := toFloat(value)
		return &parsed, true
	default:
		return nil, false
	}
}

func optionalBool(value any) (*bool, bool) {
	parsed, ok := value.(bool)
	if !ok {
		return nil, false
	}
	return &parsed, true
}

func sub2QuotaSnapshot(detail map[string]any) *sub2Quota {
	extra, ok := detail["extra"].(map[string]any)
	if !ok {
		return nil
	}
	window := func(prefix string) (*sub2UsageWindow, bool) {
		result := &sub2UsageWindow{}
		found := false
		if value, ok := optionalNumber(extra[prefix+"_used_percent"]); ok {
			result.UsedPercent, found = value, true
		}
		if value, ok := extra[prefix+"_reset_at"]; ok && value != nil {
			result.ResetAt, found = value, true
		}
		if value, ok := optionalNumber(extra[prefix+"_reset_after_seconds"]); ok {
			result.ResetAfterSeconds, found = value, true
		}
		if value, ok := optionalNumber(extra[prefix+"_window_minutes"]); ok {
			result.WindowMinutes, found = value, true
		}
		return result, found
	}
	fiveHour, hasFiveHour := window("codex_5h")
	sevenDay, hasSevenDay := window("codex_7d")
	result := &sub2Quota{Source: "Sub2 extra"}
	if hasFiveHour {
		result.FiveHour = fiveHour
	}
	if hasSevenDay {
		result.SevenDay = sevenDay
	}
	if value, ok := extra["codex_usage_updated_at"]; ok && value != nil {
		result.UpdatedAt = value
	}
	if snapshot, ok := extra["codex_credits_snapshot"].(map[string]any); ok {
		if credits, ok := snapshot["credits"].(map[string]any); ok {
			result.Credits = &sub2Credits{}
			if balance, ok := credits["balance"].(string); ok {
				result.Credits.Balance = balance
			}
			result.Credits.HasCredits, _ = optionalBool(credits["has_credits"])
			result.Credits.Unlimited, _ = optionalBool(credits["unlimited"])
		}
	}
	if !hasFiveHour && !hasSevenDay && result.UpdatedAt == nil && result.Credits == nil {
		return nil
	}
	return result
}

// sub2QuotaFromUsage converts Sub2's UsageInfo shape into the small snapshot
// used by AUTH. It deliberately keeps only percentages and reset metadata;
// the upstream response may contain provider-specific fields that should not
// be copied into the account table.
func sub2QuotaFromUsage(value map[string]any) *sub2Quota {
	if value == nil {
		return nil
	}
	window := func(input any) (*sub2UsageWindow, bool) {
		item, ok := input.(map[string]any)
		if !ok {
			return nil, false
		}
		result := &sub2UsageWindow{}
		found := false
		number := func(keys ...string) (*float64, bool) {
			for _, key := range keys {
				if parsed, ok := optionalNumber(item[key]); ok {
					return parsed, true
				}
			}
			return nil, false
		}
		if parsed, ok := number("utilization", "used_percent", "usedPercent"); ok {
			result.UsedPercent, found = parsed, true
		}
		for _, key := range []string{"resets_at", "reset_at", "resetsAt", "resetAt"} {
			if item[key] != nil {
				result.ResetAt, found = item[key], true
				break
			}
		}
		if parsed, ok := number("remaining_seconds", "remainingSeconds"); ok {
			result.RemainingSeconds, found = parsed, true
		}
		if parsed, ok := number("used_requests", "usedRequests"); ok {
			result.UsedRequests, found = parsed, true
		}
		if parsed, ok := number("limit_requests", "limitRequests"); ok {
			result.LimitRequests, found = parsed, true
		}
		if parsed, ok := number("reset_after_seconds", "resetAfterSeconds"); ok {
			result.ResetAfterSeconds, found = parsed, true
		}
		if parsed, ok := number("window_minutes", "windowMinutes"); ok {
			result.WindowMinutes, found = parsed, true
		}
		return result, found
	}
	result := &sub2Quota{}
	for key, target := range map[string]**sub2UsageWindow{
		"five_hour":        &result.FiveHour,
		"seven_day":        &result.SevenDay,
		"seven_day_sonnet": &result.SevenDaySonnet,
		"seven_day_fable":  &result.SevenDayFable,
		"thirty_day":       &result.ThirtyDay,
	} {
		if parsed, ok := window(value[key]); ok {
			*target = parsed
		}
	}
	if source, ok := value["source"].(string); ok {
		result.Source = source
	}
	result.UpdatedAt = value["updated_at"]
	if result.FiveHour == nil && result.SevenDay == nil && result.SevenDaySonnet == nil &&
		result.SevenDayFable == nil && result.ThirtyDay == nil && result.UpdatedAt == nil {
		return nil
	}
	return result
}

type sub2BatchUsageResponse struct {
	Usage  map[string]map[string]any  `json:"usage"`
	Errors map[string]json.RawMessage `json:"errors"`
}

func decodeSub2UsagePayload(raw json.RawMessage) (map[string]map[string]any, map[string]json.RawMessage) {
	usage := make(map[string]map[string]any)
	errorsByID := make(map[string]json.RawMessage)
	var object map[string]json.RawMessage
	if len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &object) != nil {
		return usage, errorsByID
	}
	if value, ok := object["usage"]; ok {
		_ = json.Unmarshal(value, &usage)
	}
	if value, ok := object["errors"]; ok {
		_ = json.Unmarshal(value, &errorsByID)
	}
	if len(usage) > 0 || len(errorsByID) > 0 || object["usage"] != nil || object["errors"] != nil {
		return usage, errorsByID
	}
	for id, value := range object {
		var item map[string]any
		if json.Unmarshal(value, &item) == nil {
			usage[id] = item
		}
	}
	return usage, errorsByID
}

func sub2UsageErrorText(raw json.RawMessage) string {
	var value any
	if json.Unmarshal(raw, &value) != nil {
		return "Sub2 额度查询失败"
	}
	if message, ok := value.(string); ok && strings.TrimSpace(message) != "" {
		return message
	}
	if object, ok := value.(map[string]any); ok {
		for _, key := range []string{"message", "error", "detail", "reason"} {
			if message, ok := object[key].(string); ok && strings.TrimSpace(message) != "" {
				return message
			}
		}
	}
	return "Sub2 额度查询失败"
}

// sub2AccountUsageBatch reads cached/passive snapshots in one request. A
// usage failure is returned separately from the HTTP error so callers can
// retain a verified account existence/status result.
func (s *sub2ImportService) sub2AccountUsageBatch(ctx context.Context, ids []int64) (map[int64]*sub2Quota, map[int64]string, error) {
	usage := make(map[int64]*sub2Quota)
	errorsByID := make(map[int64]string)
	unique := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	if len(unique) == 0 {
		return usage, errorsByID, nil
	}
	body, err := json.Marshal(map[string]any{"account_ids": unique, "force": false})
	if err != nil {
		return usage, errorsByID, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+"/admin/accounts/usage/batch", bytes.NewReader(body))
	if err != nil {
		return usage, errorsByID, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("x-api-key", s.adminAPIKey)
	response, err := s.client.Do(request)
	if err != nil {
		return usage, errorsByID, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return usage, errorsByID, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return usage, errorsByID, recoveryHTTPError(response.StatusCode, recoveryRetryAfter(response.Header.Get("Retry-After")))
	}
	var envelope struct {
		Code    int                        `json:"code"`
		Message string                     `json:"message"`
		Data    json.RawMessage            `json:"data"`
		Usage   map[string]map[string]any  `json:"usage"`
		Errors  map[string]json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return usage, errorsByID, errors.New("Sub2 额度查询返回格式无效")
	}
	if envelope.Code != 0 {
		message := envelope.Message
		if message == "" {
			message = "Sub2 额度查询失败"
		}
		return usage, errorsByID, errors.New(message)
	}
	payload := sub2BatchUsageResponse{Usage: envelope.Usage, Errors: envelope.Errors}
	dataUsage, dataErrors := decodeSub2UsagePayload(envelope.Data)
	if len(dataUsage) > 0 || len(dataErrors) > 0 {
		payload.Usage, payload.Errors = dataUsage, dataErrors
	}
	for rawID, item := range payload.Usage {
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr != nil || id <= 0 {
			continue
		}
		if snapshot := sub2QuotaFromUsage(item); snapshot != nil {
			usage[id] = snapshot
		}
	}
	for rawID, rawMessage := range payload.Errors {
		id, parseErr := strconv.ParseInt(rawID, 10, 64)
		if parseErr == nil && id > 0 {
			if message := sub2UsageErrorText(rawMessage); message != "" {
				errorsByID[id] = message
			}
		}
	}
	return usage, errorsByID, nil
}

func (s *sub2ImportService) attachSub2Usage(ctx context.Context, statuses map[int64]sub2AccountStatus) {
	if len(statuses) == 0 {
		return
	}
	remoteIDs := make([]int64, 0, len(statuses))
	for _, status := range statuses {
		if status.Exists && status.Sub2AccountID > 0 {
			remoteIDs = append(remoteIDs, status.Sub2AccountID)
		}
	}
	if len(remoteIDs) == 0 {
		return
	}
	usage, errorsByID, err := s.sub2AccountUsageBatch(ctx, remoteIDs)
	for accountID, status := range statuses {
		if !status.Exists || status.Sub2AccountID <= 0 {
			continue
		}
		if err != nil {
			status.UsageUnknown, status.UsageStale, status.UsageError = true, true, err.Error()
			statuses[accountID] = status
			continue
		}
		if snapshot, ok := usage[status.Sub2AccountID]; ok {
			status.Quota = mergeSub2Quota(status.Quota, snapshot)
			status.UsageUnknown, status.UsageStale, status.UsageError = false, false, ""
		} else if message := errorsByID[status.Sub2AccountID]; message != "" {
			status.UsageUnknown, status.UsageStale, status.UsageError = true, true, message
		} else if status.Quota == nil {
			status.UsageUnknown, status.UsageStale, status.UsageError = true, true, "Sub2 未返回额度数据"
		}
		statuses[accountID] = status
	}
}

func mergeSub2Quota(base, update *sub2Quota) *sub2Quota {
	if base == nil {
		return update
	}
	if update == nil {
		return base
	}
	merged := *base
	if update.FiveHour != nil {
		merged.FiveHour = update.FiveHour
	}
	if update.SevenDay != nil {
		merged.SevenDay = update.SevenDay
	}
	if update.SevenDaySonnet != nil {
		merged.SevenDaySonnet = update.SevenDaySonnet
	}
	if update.SevenDayFable != nil {
		merged.SevenDayFable = update.SevenDayFable
	}
	if update.ThirtyDay != nil {
		merged.ThirtyDay = update.ThirtyDay
	}
	if update.Credits != nil {
		merged.Credits = update.Credits
	}
	if update.UpdatedAt != nil {
		merged.UpdatedAt = update.UpdatedAt
	}
	if update.Source != "" {
		merged.Source = update.Source
	}
	return &merged
}

func (s *sub2ImportService) apiJSON(ctx context.Context, method, path string, query url.Values, out any) error {
	_, err := s.apiJSONStatus(ctx, method, path, query, out)
	return err
}

// apiJSONStatus is apiJSON with the HTTP status retained, where 404 (deleted)
// differs from an unavailable API.
func (s *sub2ImportService) apiJSONStatus(ctx context.Context, method, path string, query url.Values, out any) (int, error) {
	u := s.baseURL + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	request, err := http.NewRequestWithContext(ctx, method, u, nil)
	if err != nil {
		return 0, err
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("x-api-key", s.adminAPIKey)
	response, err := s.client.Do(request)
	if err != nil {
		return 0, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return response.StatusCode, err
	}
	if method == http.MethodGet && response.StatusCode == http.StatusNotFound && isSub2AccountDetailPath(path) {
		if trustedSub2NotFound(path, response.Header.Get("Content-Type"), body) {
			return response.StatusCode, &sub2NotFoundError{}
		}
		return response.StatusCode, &recoveryOperationError{Code: "protocol_error", HTTPStatus: response.StatusCode}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, recoveryHTTPError(response.StatusCode, recoveryRetryAfter(response.Header.Get("Retry-After")))
	}
	var envelope struct {
		Code    int             `json:"code"`
		Message string          `json:"message"`
		Data    json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return response.StatusCode, errors.New("Sub2 查询返回格式无效")
	}
	if envelope.Code != 0 {
		message := envelope.Message
		if message == "" {
			message = "Sub2 查询失败"
		}
		return response.StatusCode, errors.New(message)
	}
	if out == nil {
		return response.StatusCode, nil
	}
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

// deleteDeletedAccount removes the verified Sub2 binding when AUTH has
// explicitly confirmed that the upstream account was deleted or deactivated.
// A missing remote account is already in the desired state and is therefore successful.
func (s *sub2ImportService) deleteDeletedAccount(ctx context.Context, accountID, expectedCredentialAttemptID int64) error {
	if s == nil || s.store == nil || accountID <= 0 {
		return nil
	}
	if !s.configured() {
		lease, err := s.store.AcquireAccountRecovery(ctx, accountID)
		if err != nil {
			return err
		}
		defer lease.Release()
		account, err := s.store.GetAccountByID(ctx, accountID)
		if errors.Is(err, store.ErrAccountNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		if expectedCredentialAttemptID > 0 && account.IsOfficialUnavailable() {
			return s.store.FinalizeDeletedAccount(ctx, accountID)
		}
		return nil
	}
	lease, err := s.store.AcquireAccountRecovery(ctx, accountID)
	if err != nil {
		return err
	}
	defer lease.Release()
	return s.deleteDeletedAccountWithLease(ctx, lease, accountID, expectedCredentialAttemptID)
}

func accountDeactivationConfirmed(code string) bool {
	switch strings.ToLower(strings.TrimSpace(code)) {
	case "account_deleted", "user_deleted", "account_deactivated", "user_deactivated", "account_disabled", "user_disabled":
		return true
	default:
		return false
	}
}

// deleteDeletedAccountWithLease is used by recovery flows that already hold
// the account recovery lease. The caller must keep that lease until this
// method returns.
func (s *sub2ImportService) deleteDeletedAccountWithLease(ctx context.Context, lease *store.AccountRecoveryLease, accountID, expectedCredentialAttemptID int64, frozen ...store.Sub2Import) error {
	if s == nil || !s.configured() || lease == nil || accountID <= 0 {
		return nil
	}
	if expectedCredentialAttemptID <= 0 {
		return store.ErrAccountRecoveryVersionChanged
	}
	task, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID)
	if errors.Is(err, store.ErrSub2ImportNotFound) && len(frozen) == 0 {
		return nil
	}
	if err != nil {
		return err
	}
	if len(frozen) > 0 && !sameCleanupBinding(task, frozen[0]) {
		return store.ErrAccountRecoveryStale
	}
	if task.State == store.Sub2RemoteMissing {
		return nil
	}
	if task.State != "cleanup_pending" && task.State != "imported" || task.Sub2AccountID <= 0 || task.OperationID == "" {
		return store.ErrAccountRecoveryStale
	}
	if task.CredentialVersion != expectedCredentialAttemptID {
		return store.ErrAccountRecoveryVersionChanged
	}
	account, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return err
	}
	var checkEvidence func(context.Context, store.Sub2Import) (bool, error)
	free := account.IsFreeLocal() && !account.IsTerminalUnavailable()
	switch {
	case free:
		checkEvidence = s.store.HasFreeSub2CleanupEvidence
	case account.IsOfficialUnavailable():
		checkEvidence = s.store.HasOfficialSub2CleanupEvidence
	case account.IsTerminalUnavailable():
		checkEvidence = s.store.HasDiscardedSub2CleanupEvidence
	default:
		return store.ErrAccountRecoveryStale
	}
	if authorized, err := checkEvidence(ctx, task); err != nil {
		return err
	} else if !authorized {
		return store.ErrAccountRecoveryStale
	}
	workspace, err := s.store.Sub2CleanupWorkspace(ctx, task, free)
	if err != nil {
		return err
	}
	path := "/admin/accounts/" + strconv.FormatInt(task.Sub2AccountID, 10)
	var writeErr error
	var pausedRevision time.Time
	lastPhase := ""
	for {
		var detail map[string]any
		status, err := s.apiJSONStatus(ctx, http.MethodGet, path, nil, &detail)
		if isSub2NotFound(err) {
			if authorized, err := checkEvidence(ctx, task); err != nil {
				return err
			} else if !authorized {
				return store.ErrAccountRecoveryStale
			}
			return s.finishDeletedSub2Binding(ctx, account, task, status)
		}
		if writeErr != nil {
			return writeErr
		}
		if err != nil {
			return err
		}
		if lastPhase == "delete" {
			return &recoveryOperationError{Code: "checkpoint_unconfirmed"}
		}
		account.ChatGPTAccountID = workspace
		extra, _ := detail["extra"].(map[string]any)
		_, hasMarker := extra["kkai_auth_import"]
		owned := matchesImportMarker(detail, task) || !hasMarker && strings.HasPrefix(task.OperationID, "linked-")
		if detail["parent_account_id"] != nil || fmt.Sprint(extra["synthetic_ui_test"]) == "true" || workspace == "" || verifySub2AccountIdentity(detail, task.Sub2AccountID, accountID, account) != nil || !owned {
			return store.ErrAccountRecoveryStale
		}
		stamp, _ := detail["updated_at"].(string)
		parsed, err := time.Parse(time.RFC3339Nano, stamp)
		scheduled, known := boolField(detail, "schedulable")
		if err != nil || parsed.IsZero() || !known || lastPhase == "pause" && scheduled {
			return &recoveryOperationError{Code: "protocol_error"}
		}
		if lastPhase == "pause" && !parsed.Equal(pausedRevision) {
			return store.ErrAccountRecoveryStale
		}
		bindings, err := s.store.ListSub2ImportsForRemoteID(ctx, task.DestinationKey, task.Sub2AccountID)
		if err != nil {
			return err
		}
		if len(bindings) != 1 || !sameCleanupBinding(bindings[0], task) {
			return store.ErrAccountRecoveryStale
		}
		if authorized, err := checkEvidence(ctx, task); err != nil {
			return err
		} else if !authorized {
			return store.ErrAccountRecoveryStale
		}
		if scheduled {
			lastPhase = "pause"
			var paused struct {
				ID          int64     `json:"id"`
				Schedulable *bool     `json:"schedulable"`
				UpdatedAt   time.Time `json:"updated_at"`
			}
			recovery := sub2RecoveryService{store: s.store, sub2: s}
			writeErr = recovery.sub2JSONBody(ctx, http.MethodPost, path+"/schedulable", map[string]bool{"schedulable": false}, &paused)
			if writeErr == nil && (paused.ID != task.Sub2AccountID || paused.Schedulable == nil || *paused.Schedulable || !paused.UpdatedAt.After(parsed)) {
				writeErr = &recoveryOperationError{Code: "protocol_error"}
			}
			pausedRevision = paused.UpdatedAt
			continue
		}

		// The standard DELETE cascades to Spark shadows. Check the paged
		// account list before deleting so retirement only targets the bound ID.
		accounts, err := s.listOAuthAccounts(ctx)
		if err != nil {
			return err
		}
		found := false
		for _, item := range accounts {
			if int64(toFloat(item["parent_account_id"])) == task.Sub2AccountID {
				return store.ErrAccountRecoveryStale
			}
			if int64(toFloat(item["id"])) == task.Sub2AccountID {
				found = true
			}
		}
		if !found {
			return store.ErrAccountRecoveryStale
		}
		if authorized, err := checkEvidence(ctx, task); err != nil {
			return err
		} else if !authorized {
			return store.ErrAccountRecoveryStale
		}
		lastPhase = "delete"
		writeErr = s.apiJSON(ctx, http.MethodDelete, path, nil, nil)
	}
}

func sameCleanupBinding(current, frozen store.Sub2Import) bool {
	return current.ID == frozen.ID && current.AccountID == frozen.AccountID &&
		current.DestinationKey == frozen.DestinationKey && current.Sub2AccountID == frozen.Sub2AccountID &&
		current.OperationID == frozen.OperationID && current.CredentialVersion == frozen.CredentialVersion &&
		current.State == frozen.State
}

func (s *sub2ImportService) markSub2CleanupPending(ctx context.Context, task store.Sub2Import, err error) {
	if task.ID == 0 {
		return
	}
	message := "Sub2 退役尚未确认，后台将自动复查"
	if errors.Is(err, store.ErrAccountRecoveryStale) || errors.Is(err, store.ErrAccountRecoveryVersionChanged) {
		message = "账号退役证据与当前 Sub2 绑定或版本不一致，后台将自动复查"
	}
	_ = s.store.DeferFrozenSub2Cleanup(ctx, task, message)
}

func (s *sub2ImportService) finishDeletedSub2Binding(ctx context.Context, _ store.Account, task store.Sub2Import, status int) error {
	if status != http.StatusNotFound {
		return &recoveryOperationError{Code: "checkpoint_unconfirmed"}
	}
	if err := s.store.MarkRetiredSub2AccountMissing(ctx, task); err != nil {
		return err
	}
	s.statusMu.Lock()
	delete(s.statusCache, task.Sub2AccountID)
	s.statusMu.Unlock()
	return nil
}

func (s *sub2ImportService) reconcile(ctx context.Context, id int64) error {
	task, err := s.store.GetSub2ImportByID(ctx, id)
	if err != nil {
		return err
	}
	if task.DestinationKey != s.destinationKey {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	if task.State == store.Sub2RemoteMissing {
		return store.ErrSub2AccountMissing
	}
	account, err := s.store.GetAccountByID(ctx, task.AccountID)
	if err != nil {
		return err
	}
	if err := account.Sub2EligibilityError(); err != nil {
		return err
	}
	if !store.IsSub2PaidPlan(account.PlanType) {
		return store.ErrSub2PlanIneligible
	}
	if task.State == "imported" && task.Sub2AccountID > 0 {
		return nil
	}
	items, err := s.listOAuthAccounts(ctx)
	if err != nil {
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", task.CreateAcknowledged, 0, classifyRecoveryError(err).Error())
		return err
	}
	candidates := make([]map[string]any, 0, len(items))
	for _, item := range items {
		if matchesImportMarker(item, task) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) != 1 {
		message := "Sub2 账号尚未可核对"
		if len(candidates) > 1 {
			message = "Sub2 找到多个同来源账号，已暂停自动确认"
		}
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", task.CreateAcknowledged, 0, message)
		if len(candidates) > 1 {
			return errSub2AmbiguousMatch
		}
		return unconfirmedImportError(task)
	}
	candidate := candidates[0]
	sub2ID := int64(toFloat(candidate["id"]))
	if sub2ID <= 0 {
		return errors.New("Sub2 账号 ID 无效")
	}
	var detail map[string]any
	if err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(sub2ID, 10), nil, &detail); err != nil {
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", task.CreateAcknowledged, 0, err.Error())
		return err
	}
	if int64(toFloat(detail["id"])) != sub2ID || !matchesImportMarker(detail, task) {
		message := "Sub2 账号身份核对不一致，已暂停自动确认"
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", task.CreateAcknowledged, 0, message)
		return errSub2IdentityConflict
	}
	if err := s.verifyImportedEgress(ctx, task, detail); err != nil {
		_ = s.store.UpdateSub2ImportForOperation(ctx, task, "unknown", true, sub2ID, classifyRecoveryError(err).Error())
		return err
	}
	return s.store.UpdateSub2ImportForOperation(ctx, task, "imported", true, sub2ID, "")
}

// Only newly imported accounts carry the target route marker. Existing linked
// accounts retain their administrator-managed egress configuration.
func (s *sub2ImportService) verifyImportedEgress(ctx context.Context, task store.Sub2Import, detail map[string]any) error {
	payload, err := s.store.GetSub2ImportPayload(ctx, task.ID)
	if err != nil {
		return err
	}
	var request sub2DataImportRequest
	if err := json.Unmarshal(payload, &request); err != nil || len(request.Data.Accounts) != 1 {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	account := request.Data.Accounts[0]
	marker, _ := account.Extra["kkai_auth_import"].(map[string]any)
	rawIDs, selected := marker["target_route_ids"]
	if !selected {
		return nil
	}
	encodedIDs, err := json.Marshal(rawIDs)
	if err != nil {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	var expectedIDs []int64
	if json.Unmarshal(encodedIDs, &expectedIDs) != nil || len(expectedIDs) == 0 || account.EgressPool == nil {
		return &recoveryOperationError{Code: "configuration", RequiresAction: true}
	}
	var actual struct {
		RouteIDs             []int64 `json:"route_ids"`
		PrimaryRouteID       int64   `json:"primary_route_id"`
		ConcurrencyPerEgress int     `json:"concurrency_per_egress"`
	}
	encodedPool, err := json.Marshal(detail["egress_pool"])
	if err != nil || json.Unmarshal(encodedPool, &actual) != nil ||
		detail["egress_mode"] != "pool" || !slices.Equal(actual.RouteIDs, expectedIDs) ||
		actual.PrimaryRouteID != expectedIDs[0] || actual.ConcurrencyPerEgress != account.EgressPool.ConcurrencyPerEgress {
		return &recoveryOperationError{Code: "egress_mismatch", RequiresAction: true}
	}
	return nil
}

func matchesImportMarker(item map[string]any, task store.Sub2Import) bool {
	extra, ok := item["extra"].(map[string]any)
	if !ok {
		return false
	}
	marker, ok := extra["kkai_auth_import"].(map[string]any)
	if !ok || fmt.Sprint(marker["operation_id"]) != task.OperationID {
		return false
	}
	return int64(toFloat(marker["source_account_id"])) == task.AccountID
}

func toFloat(value any) float64 {
	switch v := value.(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	default:
		return 0
	}
}

type sub2ImportRequest struct {
	DeliveryOptions *store.DeliveryOptions `json:"delivery_options,omitempty"`
	Reimport        bool                   `json:"reimport"`
	// ExpectedCredentialVersion binds an explicit remote-missing reimport to
	// the successful login that immediately preceded it.
	ExpectedCredentialVersion int64 `json:"expected_credential_version,omitempty"`
	// ExpectedImportOperationID binds it to the exact missing binding observed
	// before the login, preventing delayed requests from reviving a later one.
	ExpectedImportOperationID string  `json:"expected_import_operation_id,omitempty"`
	AccountIDs                []int64 `json:"account_ids"`
}

func (s *sub2ImportService) handleImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
		return
	}
	if !s.configured() {
		respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "Sub2API 未配置，请设置 SUB2API_BASE_URL 和 SUB2API_ADMIN_API_KEY", Code: "sub2_unconfigured"})
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 32<<10)
	var request sub2ImportRequest
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&request); err != nil || len(request.AccountIDs) == 0 || len(request.AccountIDs) > 100 {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "account_ids 必须包含 1 到 100 个账号 ID", Code: "invalid_account_ids"})
		return
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "请求只能包含一个 JSON 对象"})
		return
	}
	options, err := s.loadOptions(r.Context(), request.DeliveryOptions)
	if err != nil {
		respondJSONStatus(w, 400, LoginResponse{Message: classifyRecoveryError(err).Error()})
		return
	}
	if err := options.ValidateForImport(); err != nil {
		respondJSONStatus(w, 400, LoginResponse{Message: err.Error(), Code: "invalid_import_options"})
		return
	}
	if request.Reimport && request.ExpectedCredentialVersion <= 0 {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "重新导入必须绑定本次成功登录，请刷新后重试", Code: "reimport_credential_version_required"})
		return
	}
	if request.Reimport && strings.TrimSpace(request.ExpectedImportOperationID) == "" {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "重新导入必须绑定当前缺失关联，请刷新后重试", Code: "reimport_binding_required"})
		return
	}
	if !s.workerMu.TryLock() {
		respondJSONStatus(w, 409, LoginResponse{Message: "正在处理导入，请稍后重试"})
		return
	}
	defer s.workerMu.Unlock()
	created := make([]store.Sub2Import, 0, len(request.AccountIDs))
	var reimportErr error
	discarded := false
	seen := make(map[int64]struct{}, len(request.AccountIDs))
	for _, accountID := range request.AccountIDs {
		if accountID <= 0 {
			continue
		}
		if _, exists := seen[accountID]; exists {
			continue
		}
		seen[accountID] = struct{}{}
		account, err := s.store.GetAccountByID(r.Context(), accountID)
		if err != nil {
			continue
		}
		if errors.Is(account.Sub2EligibilityError(), store.ErrAccountDiscarded) {
			discarded = true
			continue
		}
		if account.IsTerminalUnavailable() || account.Status != "active" {
			continue
		}
		lease, err := s.store.AcquireAccountRecovery(r.Context(), accountID)
		if err != nil {
			continue
		}
		if _, active, checkErr := s.store.GetActiveAccountRecoveryTaskForDestination(r.Context(), accountID, s.destinationKey); checkErr != nil || active {
			lease.Release()
			continue
		}
		if request.Reimport {
			if err := s.authorizeReimport(r.Context(), accountID, options, request.ExpectedCredentialVersion, request.ExpectedImportOperationID); err != nil {
				reimportErr = err
				lease.Release()
				continue
			}
			if task, err := s.store.GetSub2Import(r.Context(), s.destinationKey, accountID); err == nil {
				created = append(created, task)
			}
			lease.Release()
			continue
		}
		// Commit the delivery intent first: process death cannot strand a newly queued import.
		_, queueErr := s.store.QueueAccountDelivery(r.Context(), accountID, s.destinationKey, options)
		err = queueErr
		if err == nil {
			task, taskErr := s.store.GetSub2Import(r.Context(), s.destinationKey, accountID)
			if taskErr == nil {
				created = append(created, task)
			} else {
				created = append(created, store.Sub2Import{AccountID: accountID, State: "queued"})
			}
		}
		lease.Release()
	}
	if len(created) == 0 {
		if discarded {
			respondJSONStatus(w, http.StatusGone, LoginResponse{Message: "账号已在本地退役", Code: "account_discarded"})
			return
		}
		if request.Reimport && errors.Is(reimportErr, store.ErrAccountRecoveryStale) {
			respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: "登录资料已变化或重导入请求已失效，请重新登录后重试", Code: "reimport_credential_version_stale"})
			return
		}
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "没有可导入的认证成功账号", Code: "no_importable_accounts"})
		return
	}
	respondJSON(w, map[string]any{"success": true, "imports": created})
}

func (s *sub2ImportService) ensureTask(ctx context.Context, accountID int64, options ...store.DeliveryOptions) (store.Sub2Import, error) {
	return s.ensureDeliveryTask(ctx, accountID, true, options...)
}

func (s *sub2ImportService) ensureDeliveryTask(ctx context.Context, accountID int64, allowCreate bool, options ...store.DeliveryOptions) (store.Sub2Import, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	account, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return store.Sub2Import{}, err
	}
	if err := account.Sub2EligibilityError(); err != nil {
		return store.Sub2Import{}, err
	}
	if !store.IsSub2PaidPlan(account.PlanType) {
		return store.Sub2Import{}, store.ErrSub2PlanIneligible
	}
	if err := s.importMissing(ctx, accountID); err != nil {
		return store.Sub2Import{}, err
	}
	if existing, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID); err == nil {
		version, versionErr := s.store.GetAccountCredentialVersion(ctx, accountID)
		if versionErr != nil {
			return store.Sub2Import{}, versionErr
		}
		if (existing.State == "failed" || existing.State == "queued") && existing.CredentialVersion != version {
			if err := s.refreshSub2ImportCredentials(ctx, existing); err != nil {
				return store.Sub2Import{}, err
			}
			existing, err = s.store.GetSub2Import(ctx, s.destinationKey, accountID)
			if err != nil {
				return store.Sub2Import{}, err
			}
		}
		if existing.State == "failed" {
			if err := s.store.RequeueSub2Import(ctx, existing.ID); err != nil {
				return store.Sub2Import{}, err
			}
			return s.store.GetSub2Import(ctx, s.destinationKey, accountID)
		}
		if existing.State == "cleanup_pending" {
			return existing, errors.New("Sub2 账号清理仍在重试，请稍后再试")
		}
		return existing, nil
	} else if !errors.Is(err, store.ErrSub2ImportNotFound) {
		return store.Sub2Import{}, err
	}
	if !allowCreate {
		return store.Sub2Import{}, store.ErrSub2AccountMissing
	}
	candidates, err := s.listOAuthAccounts(ctx)
	if err != nil {
		return store.Sub2Import{}, err
	}
	if linked, err := s.linkExistingAccount(ctx, account, candidates); err != nil || linked.ID != 0 {
		return linked, err
	}
	selected := store.DefaultDeliveryOptions()
	if len(options) > 0 {
		selected = options[0]
	}
	checked, err := s.resolveOptions(ctx, &selected)
	if err != nil {
		return store.Sub2Import{}, err
	}
	payload, operationID, idempotencyKey, err := s.buildPayload(accountID, checked)
	if err != nil {
		return store.Sub2Import{}, err
	}
	task, _, err := s.store.CreateOrGetSub2Import(ctx, s.destinationKey, accountID, operationID, idempotencyKey, payload)
	return task, err
}

func (s *sub2ImportService) handleAction(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		respondJSONStatus(w, http.StatusMethodNotAllowed, LoginResponse{Message: "Method not allowed"})
		return
	}
	if !s.configured() {
		respondJSONStatus(w, http.StatusServiceUnavailable, LoginResponse{Message: "Sub2API 未配置", Code: "sub2_unconfigured"})
		return
	}
	text := strings.TrimPrefix(r.URL.Path, "/api/sub2/import/")
	parts := strings.Split(strings.Trim(text, "/"), "/")
	if len(parts) != 2 || parts[1] != "reconcile" {
		respondJSONStatus(w, http.StatusNotFound, LoginResponse{Message: "导入操作不存在"})
		return
	}
	id, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || id <= 0 {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "导入任务 ID 无效"})
		return
	}
	if !s.workerMu.TryLock() {
		respondJSONStatus(w, 409, LoginResponse{Message: "正在处理导入，请稍后重试"})
		return
	}
	defer s.workerMu.Unlock()
	item, err := s.store.GetSub2ImportByID(r.Context(), id)
	if err != nil || item.DestinationKey != s.destinationKey {
		respondJSONStatus(w, 404, LoginResponse{Message: "导入任务不存在"})
		return
	}
	lease, err := s.store.AcquireAccountRecovery(r.Context(), item.AccountID)
	if err != nil {
		respondJSONStatus(w, 409, LoginResponse{Message: "账号正在处理"})
		return
	}
	defer lease.Release()
	if err := s.reconcile(r.Context(), id); err != nil {
		respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: err.Error(), Code: "sub2_reconcile_pending"})
		return
	}
	task, err := s.store.GetSub2ImportByID(r.Context(), id)
	if err != nil {
		respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取导入任务失败"})
		return
	}
	_ = s.store.WakeAccountDelivery(r.Context(), task.AccountID, s.destinationKey)
	respondJSON(w, map[string]any{"success": true, "import": task})
}

func unconfirmedImportError(task store.Sub2Import) *recoveryOperationError {
	return &recoveryOperationError{Code: "import_unconfirmed", RequiresAction: time.Since(task.CreatedAt) >= 15*time.Minute}
}
