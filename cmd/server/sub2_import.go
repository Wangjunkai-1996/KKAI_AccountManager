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
	}
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
	go func() {
		ticker := time.NewTicker(2 * time.Second)
		defer ticker.Stop()
		for range ticker.C {
			s.runOnce(context.Background())
		}
	}()
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

func (s *sub2ImportService) buildPayload(accountID int64) ([]byte, string, string, error) {
	operationID, err := newOperationID()
	if err != nil {
		return nil, "", "", err
	}
	return s.buildPayloadWithOperation(accountID, operationID)
}

func (s *sub2ImportService) buildPayloadWithOperation(accountID int64, operationID string) ([]byte, string, string, error) {
	email, result, err := s.store.GetOAuthResultByID(context.Background(), accountID)
	if err != nil {
		return nil, "", "", err
	}
	marker := "AUTH-" + operationID
	name := marker + " | " + email
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
		},
	}
	request := sub2DataImportRequest{
		SkipDefaultGroupBind: true,
		Data: sub2DataImportData{
			Type: "sub2api-data", Version: 1, Proxies: []any{},
			Accounts: []sub2DataAccount{{
				Name: name, Platform: "openai", Type: "oauth", Credentials: credentials, Extra: extra,
				Concurrency: 3, Priority: 1, RateMultiplier: 1, AutoPauseOnExpired: true,
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
	tasks, err := s.store.ListQueuedSub2Imports(ctx, 1)
	if err != nil || len(tasks) == 0 {
		return
	}
	_ = s.processQueued(ctx, tasks[0])
}

func (s *sub2ImportService) processQueued(ctx context.Context, task store.Sub2Import) error {
	if err := s.store.MarkSub2ImportSending(ctx, task.ID); err != nil {
		return err
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
		message := response.Message
		if message == "" {
			message = "Sub2 导入接口返回失败"
		}
		_ = s.store.UpdateSub2Import(ctx, task.ID, "failed", false, 0, message)
		return errors.New(message)
	}
	var result sub2CreateDataResult
	if len(response.Data) > 0 {
		_ = json.Unmarshal(response.Data, &result)
	}
	if result.AccountFailed > 0 || result.AccountCreated < 1 {
		message := response.Message
		if len(result.Errors) > 0 && result.Errors[0].Message != "" {
			message = result.Errors[0].Message
		}
		if message == "" {
			message = "Sub2 未创建账号"
		}
		_ = s.store.UpdateSub2Import(ctx, task.ID, "failed", false, 0, message)
		return errors.New(message)
	}
	_ = s.store.UpdateSub2Import(ctx, task.ID, "confirming", true, 0, "")
	return s.reconcile(ctx, task.ID)
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
	body, readErr := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if readErr != nil {
		return response.StatusCode, sub2ImportEnvelope{}, readErr
	}
	var envelope sub2ImportEnvelope
	if err := json.Unmarshal(body, &envelope); err != nil {
		return response.StatusCode, envelope, fmt.Errorf("Sub2 返回格式无效（HTTP %d）", response.StatusCode)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		message := envelope.Message
		if message == "" {
			message = fmt.Sprintf("Sub2 导入失败（HTTP %d）", response.StatusCode)
		}
		return response.StatusCode, envelope, errors.New(message)
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
	Exists        bool      `json:"exists"`
	Imported      bool      `json:"imported"`
	Sub2AccountID int64     `json:"sub2_account_id,omitempty"`
	Status        string    `json:"status,omitempty"`
	Schedulable   *bool     `json:"schedulable,omitempty"`
	ErrorMessage  string    `json:"error_message,omitempty"`
	CheckedAt     time.Time `json:"checked_at"`
	Stale         bool      `json:"stale,omitempty"`
	Error         string    `json:"error,omitempty"`
	Unknown       bool      `json:"unknown,omitempty"`
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

func (s *sub2ImportService) sub2AccountStatus(ctx context.Context, id int64, checkedAt time.Time) sub2AccountStatus {
	status := sub2AccountStatus{Sub2AccountID: id, CheckedAt: checkedAt}
	var detail map[string]any
	code, err := s.apiJSONStatus(ctx, http.MethodGet, "/admin/accounts/"+strconv.FormatInt(id, 10), nil, &detail)
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
	status.Exists = true
	status.Status, _ = detail["status"].(string)
	status.ErrorMessage, _ = detail["error_message"].(string)
	if value, ok := detail["schedulable"].(bool); ok {
		status.Schedulable = &value
	}
	return status
}

func (s *sub2ImportService) apiJSON(ctx context.Context, method, path string, query url.Values, out any) error {
	_, err := s.apiJSONStatus(ctx, method, path, query, out)
	return err
}

// apiJSONStatus is apiJSON with the HTTP status retained for read-only
// account snapshots, where 404 (deleted) differs from an unavailable API.
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
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return response.StatusCode, fmt.Errorf("Sub2 查询失败（HTTP %d）", response.StatusCode)
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
	if err := json.Unmarshal(envelope.Data, out); err != nil {
		return response.StatusCode, err
	}
	return response.StatusCode, nil
}

func (s *sub2ImportService) reconcile(ctx context.Context, id int64) error {
	task, err := s.store.GetSub2ImportByID(ctx, id)
	if err != nil {
		return err
	}
	var list sub2ListResponse
	query := url.Values{}
	query.Set("platform", "openai")
	query.Set("type", "oauth")
	query.Set("lite", "1")
	query.Set("group", "ungrouped")
	query.Set("search", task.OperationID)
	query.Set("page_size", "1000")
	for page := 1; page <= 20; page++ {
		query.Set("page", strconv.Itoa(page))
		var current sub2ListResponse
		if err := s.apiJSON(ctx, http.MethodGet, "/admin/accounts", query, &current); err != nil {
			_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, err.Error())
			return err
		}
		list.Items = append(list.Items, current.Items...)
		list.Pages = current.Pages
		if current.Pages <= page || len(current.Items) == 0 {
			break
		}
	}
	candidates := make([]map[string]any, 0, len(list.Items))
	for _, item := range list.Items {
		if strings.Contains(fmt.Sprint(item["name"]), task.OperationID) && matchesImportMarker(item, task) {
			candidates = append(candidates, item)
		}
	}
	if len(candidates) != 1 {
		message := "Sub2 账号尚未可核对"
		if len(candidates) > 1 {
			message = "Sub2 找到多个同来源账号，已暂停自动确认"
		}
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, 0, message)
		return errors.New(message)
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
		return errors.New(message)
	}
	if groups, ok := detail["group_ids"].([]any); ok && len(groups) != 0 {
		message := "Sub2 账号已被分组，未满足未分组导入条件"
		_ = s.store.UpdateSub2Import(ctx, id, "unknown", task.CreateAcknowledged, sub2ID, message)
		return errors.New(message)
	}
	_ = s.store.UpdateSub2Import(ctx, id, "imported", true, sub2ID, "")
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
	AccountIDs []int64 `json:"account_ids"`
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
		task, err := s.ensureTask(r.Context(), accountID)
		if err == nil {
			created = append(created, task)
		}
	}
	if len(created) == 0 {
		respondJSONStatus(w, http.StatusBadRequest, LoginResponse{Message: "没有可导入的认证成功账号", Code: "no_importable_accounts"})
		return
	}
	go s.runOnce(context.Background())
	respondJSON(w, map[string]any{"success": true, "imports": created})
}

func (s *sub2ImportService) ensureTask(ctx context.Context, accountID int64) (store.Sub2Import, error) {
	if existing, err := s.store.GetSub2Import(ctx, s.destinationKey, accountID); err == nil {
		if existing.State == "failed" {
			payload, _, _, buildErr := s.buildPayloadWithOperation(accountID, existing.OperationID)
			if buildErr != nil {
				return store.Sub2Import{}, buildErr
			}
			if refreshErr := s.store.RefreshSub2ImportPayload(ctx, existing.ID, payload); refreshErr != nil {
				return store.Sub2Import{}, refreshErr
			}
			return s.store.GetSub2Import(ctx, s.destinationKey, accountID)
		}
		return existing, nil
	}
	payload, operationID, idempotencyKey, err := s.buildPayload(accountID)
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
	if err := s.reconcile(r.Context(), id); err != nil {
		respondJSONStatus(w, http.StatusConflict, LoginResponse{Message: err.Error(), Code: "sub2_reconcile_pending"})
		return
	}
	task, err := s.store.GetSub2ImportByID(r.Context(), id)
	if err != nil {
		respondJSONStatus(w, http.StatusInternalServerError, LoginResponse{Message: "读取导入任务失败"})
		return
	}
	respondJSON(w, map[string]any{"success": true, "import": task})
}
