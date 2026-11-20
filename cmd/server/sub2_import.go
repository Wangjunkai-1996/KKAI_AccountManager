package main

import (
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
	Name               string         `json:"name"`
	Platform           string         `json:"platform"`
	Type               string         `json:"type"`
	Credentials        map[string]any `json:"credentials"`
	Extra              map[string]any `json:"extra,omitempty"`
	Concurrency        int            `json:"concurrency"`
	Priority           int            `json:"priority"`
	RateMultiplier     int            `json:"rate_multiplier"`
	AutoPauseOnExpired bool           `json:"auto_pause_on_expired"`
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
		"email": email,
		"kkai_auth_import": map[string]any{
			"source_instance_id": strings.TrimSpace(os.Getenv("AUTH_INSTANCE_ID")),
			"source_account_id":  accountID,
			"operation_id":       operationID,
			"target_group_ids":   opt.GroupIDs,
		},
	}
	request := sub2DataImportRequest{
		SkipDefaultGroupBind: true,
		Data: sub2DataImportData{
			Type: "sub2api-data", Version: 1, Proxies: []any{},
			Accounts: []sub2DataAccount{{
				Name: name, Platform: "openai", Type: "oauth", Credentials: credentials, Extra: extra,
				Concurrency: opt.Concurrency, Priority: opt.Priority, RateMultiplier: 1, AutoPauseOnExpired: true,
			}},
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
			_ = s.deleteDeletedAccountWithLease(ctx, lease, cleanupTasks[0].AccountID, cleanupTasks[0].CredentialVersion)
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
	if err := s.store.MarkSub2ImportSendingForVersion(ctx, task.ID, task.CredentialVersion); err != nil {
		return err
	}
	if currentVersion, err = s.store.GetAccountCredentialVersion(ctx, task.AccountID); err != nil || currentVersion != task.CredentialVersion {
		_ = s.store.UpdateSub2Import(ctx, task.ID, "queued", false, 0, "登录凭据版本已变化，已等待刷新导入任务")
		return errors.New("登录凭据版本已变化，阻止旧导入任务发送")
	}
	payload, err := s.store.GetSub2ImportPayload(ctx, task.ID)
	if err != nil {
		_ = s.store.UpdateSub2Import(ctx, task.ID, "failed", false, 0, "读取导入任务失败")
		return err
	}
	status, response, err := s.postImport(ctx, task, payload)
	if err != nil {
		state := "unknown"
		if status >= 400 && status < 500 {
			state = "failed"
		}
		_ = s.store.UpdateSub2Import(ctx, task.ID, state, false, 0, err.Error())
		return err
	}
	if response.Code != 0 {
		failure := &recoveryOperationError{Code: "request_rejected", RequiresAction: true}
		_ = s.store.UpdateSub2Import(ctx, task.ID, "failed", false, 0, failure.Error())
		return failure
	}
	var result sub2CreateDataResult
	if len(response.Data) > 0 {
		_ = json.Unmarshal(response.Data, &result)
	}
	if result.AccountFailed > 0 || result.AccountCreated < 1 {
		failure := &recoveryOperationError{Code: "request_rejected", RequiresAction: true}
		_ = s.store.UpdateSub2Import(ctx, task.ID, "failed", false, 0, failure.Error())
		return failure
	}
	_ = s.store.UpdateSub2Import(ctx, task.ID, "confirming", true, 0, "")
	return s.reconcile(ctx, task.ID)
}

// refreshSub2ImportCredentials keeps the original operation marker and
// display/options payload while replacing only the OAuth snapshot.
func (s *sub2ImportService) refreshSub2ImportCredentials(ctx context.Context, task store.Sub2Import) error {
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
	Exists                  bool      `json:"exists"`
	Imported                bool      `json:"imported"`
	Sub2AccountID           int64     `json:"sub2_account_id,omitempty"`
	Status                  string    `json:"status,omitempty"`
	Schedulable             *bool     `json:"schedulable,omitempty"`
	EffectiveSchedulable    *bool     `json:"effective_schedulable,omitempty"`
	ErrorMessage            string    `json:"error_message,omitempty"`
	RateLimitedAt           any       `json:"rate_limited_at,omitempty"`
	RateLimitResetAt        any       `json:"rate_limit_reset_at,omitempty"`
	OverloadUntil           any       `json:"overload_until,omitempty"`
	TempUnschedulableUntil  any       `json:"temp_unschedulable_until,omitempty"`
	TempUnschedulableReason string    `json:"temp_unschedulable_reason,omitempty"`
	ExpiresAt               any       `json:"expires_at,omitempty"`
	CheckedAt               time.Time `json:"checked_at"`
	Stale                   bool      `json:"stale,omitempty"`
	Error                   string    `json:"error,omitempty"`
	Unknown                 bool      `json:"unknown,omitempty"`
	AssociationStatus       string    `json:"association_status,omitempty"`
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
				if !bound {
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
					snapshot.Imported = task.State == "imported"
					if snapshot.Imported {
						snapshot.AssociationStatus = "imported"
						if strings.HasPrefix(task.OperationID, "linked-") {
							snapshot.AssociationStatus = "linked"
						}
					}
				}
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
	return statuses
}

func (s *sub2ImportService) sub2AccountStatus(ctx context.Context, id int64, checkedAt time.Time, accounts ...store.Account) sub2AccountStatus {
	status := sub2AccountStatus{Sub2AccountID: id, CheckedAt: checkedAt}
	var detail map[string]any
	var code int
	var err error
	if sub2StatusCacheEnabled(ctx) {
		detail, code, err = s.cachedSub2AccountDetail(ctx, id)
	} else {
		code, err = s.apiJSONStatus(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail)
	}
	if err != nil {
		if code == http.StatusNotFound {
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
		if bindingErr != nil || binding.Sub2AccountID != id || verifySub2RecoveryIdentity(detail, id, account.ID, account, binding) != nil {
			status.Unknown, status.Stale = true, true
			status.Error = "Sub2 账号身份与 AUTH 绑定不一致"
			return status
		}
	}
	status.Exists = true
	status.Status, _ = detail["status"].(string)
	status.ErrorMessage, _ = detail["error_message"].(string)
	status.RateLimitedAt = detail["rate_limited_at"]
	status.RateLimitResetAt = detail["rate_limit_reset_at"]
	status.OverloadUntil = detail["overload_until"]
	status.TempUnschedulableUntil = detail["temp_unschedulable_until"]
	status.TempUnschedulableReason, _ = detail["temp_unschedulable_reason"].(string)
	status.ExpiresAt = detail["expires_at"]
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
	if method == http.MethodGet && response.StatusCode == http.StatusNotFound && trustedSub2NotFound(path, response.Header.Get("Content-Type"), body) {
		return response.StatusCode, errConfirmedSub2Missing
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

var errConfirmedSub2Missing = errors.New("Sub2 账号不存在")

func isSub2AccountDetailPath(path string) bool {
	if !strings.HasPrefix(path, "/admin/accounts/") {
		return false
	}
	n, err := strconv.ParseInt(strings.TrimPrefix(path, "/admin/accounts/"), 10, 64)
	return err == nil && n > 0
}
func trustedSub2NotFound(path, contentType string, body []byte) bool {
	if !isSub2AccountDetailPath(path) || !strings.HasPrefix(strings.ToLower(contentType), "application/json") {
		return false
	}
	var payload struct {
		Code    json.RawMessage `json:"code"`
		Message string          `json:"message"`
	}
	if json.Unmarshal(body, &payload) != nil {
		return false
	}
	code := strings.TrimSpace(string(payload.Code))
	message := strings.ToLower(strings.TrimSpace(payload.Message))
	// Sub2's management error envelope uses a numeric code and a message.
	// Empty values, arbitrary error objects and reverse-proxy JSON are not proof.
	if code != "1" && code != "404" && code != `"ACCOUNT_NOT_FOUND"` {
		return false
	}
	switch message {
	case "not found", "account not found", "account does not exist", "账号不存在", "账户不存在":
		return true
	}
	return false
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
		if account.Status == "deleted" {
			return s.store.FinalizeDeletedAccount(ctx, accountID)
		}
		return nil
	}
	lease, err := s.store.AcquireAccountRecovery(ctx, accountID)
	if err != nil {
		if errors.Is(err, store.ErrAccountBusy) {
			if task, taskErr := s.store.GetSub2Import(ctx, s.destinationKey, accountID); taskErr == nil && task.State == "imported" {
				s.markSub2CleanupPending(ctx, task, err)
			}
		}
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
func (s *sub2ImportService) deleteDeletedAccountWithLease(ctx context.Context, lease *store.AccountRecoveryLease, accountID, expectedCredentialAttemptID int64) error {
	if s == nil || !s.configured() || lease == nil || accountID <= 0 {
		return nil
	}
	task, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID)
	if errors.Is(err, store.ErrSub2ImportNotFound) {
		account, accountErr := s.store.GetAccountByID(ctx, accountID)
		if errors.Is(accountErr, store.ErrAccountNotFound) {
			return nil
		}
		if accountErr != nil {
			return accountErr
		}
		if account.Status == "deleted" {
			return s.store.FinalizeDeletedAccount(ctx, accountID)
		}
		return nil
	}
	if err != nil || (task.State != "imported" && task.State != "cleanup_pending") || task.Sub2AccountID <= 0 {
		return err
	}
	currentVersion, err := s.store.GetAccountCredentialVersion(ctx, accountID)
	if err != nil {
		return err
	}
	account, err := s.store.GetAccountByID(ctx, accountID)
	if errors.Is(err, store.ErrAccountNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	// A later successful login supersedes a pending deletion intent. Restore
	// the verified binding and let the normal delivery/recovery path update it;
	// never delete a remote account using an obsolete credential observation.
	if task.State == "cleanup_pending" && currentVersion != task.CredentialVersion {
		if account.Status == "active" {
			_ = s.store.UpdateSub2Import(ctx, task.ID, "imported", true, task.Sub2AccountID, "新的登录结果已取代待清理任务")
		}
		return errors.New("登录凭据版本已变化，跳过待清理任务")
	}
	if task.State != "cleanup_pending" && currentVersion != expectedCredentialAttemptID {
		return errors.New("检测使用的凭据版本已变化，跳过删除")
	}
	path := "/admin/accounts/" + strconv.FormatInt(task.Sub2AccountID, 10)
	var detail map[string]any
	status, err := s.apiJSONStatus(ctx, http.MethodGet, path, nil, &detail)
	if errors.Is(err, errConfirmedSub2Missing) {
		return s.finishDeletedSub2Binding(ctx, account, task)
	}
	if err != nil {
		if status == 0 || status >= 500 || status == http.StatusNotFound {
			s.markSub2CleanupPending(ctx, task, err)
		}
		return err
	}
	extra, _ := detail["extra"].(map[string]any)
	_, hasMarker := extra["kkai_auth_import"]
	owned := matchesImportMarker(detail, task) || !hasMarker && strings.HasPrefix(task.OperationID, "linked-")
	if detail["parent_account_id"] != nil || fmt.Sprint(extra["synthetic_ui_test"]) == "true" || account.ChatGPTAccountID == "" || verifySub2AccountIdentity(detail, task.Sub2AccountID, accountID, account) != nil || !owned {
		_ = s.store.UpdateSub2Import(ctx, task.ID, "cleanup_manual", true, task.Sub2AccountID, "Sub2 账号身份核对失败，需要人工确认")
		return errors.New("Sub2 退役目标与原绑定不一致")
	}
	// The standard DELETE also deletes Spark shadows. Verify that only the
	// original account is affected before sending that request.
	items, err := s.listOAuthAccounts(ctx)
	if err != nil {
		return err
	}
	found := false
	for _, item := range items {
		if int64(toFloat(item["parent_account_id"])) == task.Sub2AccountID {
			return errors.New("Sub2 账号仍有关联子账号，已停止删除")
		}
		if int64(toFloat(item["id"])) == task.Sub2AccountID {
			found = true
		}
	}
	if !found {
		return errors.New("Sub2 列表无法确认原绑定")
	}
	// The remote GET and DELETE are separate calls. Re-read the local
	// association before the destructive call so a concurrent relink/requeue
	// cannot make this stale deletion target a newly assigned account.
	latest, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID)
	if err != nil {
		return err
	}
	if latest.ID != task.ID || (latest.State != "imported" && latest.State != "cleanup_pending") || latest.Sub2AccountID != task.Sub2AccountID || latest.OperationID != task.OperationID {
		_ = s.store.UpdateSub2Import(ctx, task.ID, "cleanup_manual", true, task.Sub2AccountID, "Sub2 绑定在删除前发生变化，需要人工确认")
		return errors.New("Sub2 绑定在删除前发生变化")
	}
	writeErr := s.apiJSON(ctx, http.MethodDelete, path, nil, nil)
	// A lost reply can follow a successful delete. Only a trusted GET on the
	// original account ID confirms removal.
	_, readErr := s.apiJSONStatus(ctx, http.MethodGet, path, nil, &detail)
	if errors.Is(readErr, errConfirmedSub2Missing) {
		return s.finishDeletedSub2Binding(ctx, account, task)
	}
	if writeErr != nil {
		err = writeErr
	} else if readErr != nil {
		err = readErr
	} else {
		err = errors.New("Sub2 账号仍然存在，删除尚未确认")
	}
	s.markSub2CleanupPending(ctx, task, err)
	return err
}

func (s *sub2ImportService) markSub2CleanupPending(ctx context.Context, task store.Sub2Import, err error) {
	if task.ID == 0 {
		return
	}
	message := "Sub2 删除失败"
	if err != nil {
		message = err.Error()
	}
	version, versionErr := s.store.GetAccountCredentialVersion(ctx, task.AccountID)
	if versionErr != nil {
		return
	}
	_ = s.store.MarkSub2ImportCleanupPending(ctx, task.ID, version, task.Sub2AccountID, message)
}

func (s *sub2ImportService) finishDeletedSub2Binding(ctx context.Context, account store.Account, task store.Sub2Import) error {
	if err := s.store.DeleteSub2ImportBinding(ctx, s.destinationKey, task.AccountID, task.Sub2AccountID, task.OperationID); err != nil {
		s.markSub2CleanupPending(ctx, task, err)
		return err
	}
	if account.Status == "deleted" {
		if err := s.store.FinalizeDeletedAccount(ctx, account.ID); err != nil && !errors.Is(err, store.ErrAccountNotFound) {
			return err
		}
	}
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
	if task.State == "imported" && task.Sub2AccountID > 0 {
		return nil
	}
	items, err := s.listOAuthAccounts(ctx)
	if err != nil {
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, classifyRecoveryError(err).Error())
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
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, message)
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
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, err.Error())
		return err
	}
	if int64(toFloat(detail["id"])) != sub2ID || !matchesImportMarker(detail, task) {
		message := "Sub2 账号身份核对不一致，已暂停自动确认"
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, message)
		return errSub2IdentityConflict
	}
	return s.store.UpdateSub2Import(ctx, id, "imported", true, sub2ID, "")
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
	AccountIDs      []int64                `json:"account_ids"`
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
	if !s.workerMu.TryLock() {
		respondJSONStatus(w, 409, LoginResponse{Message: "正在处理导入，请稍后重试"})
		return
	}
	defer s.workerMu.Unlock()
	created := make([]store.Sub2Import, 0, len(request.AccountIDs))
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
		if account.Status != "active" {
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
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "没有可导入的认证成功账号", Code: "no_importable_accounts"})
		return
	}
	respondJSON(w, map[string]any{"success": true, "imports": created})
}

func (s *sub2ImportService) ensureTask(ctx context.Context, accountID int64, options ...store.DeliveryOptions) (store.Sub2Import, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
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
	account, err := s.store.GetAccountByID(ctx, accountID)
	if err != nil {
		return store.Sub2Import{}, err
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
