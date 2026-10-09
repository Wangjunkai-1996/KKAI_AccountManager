package main

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

type recoveryFixture struct {
	store   *store.Store
	db      *sql.DB
	service *sub2RecoveryService
	task    store.AccountRecoveryTask
	account store.Account
	server  *httptest.Server

	mu                  sync.Mutex
	schedule            bool
	detailEmail         string
	detailWorkspace     string
	detailOrg           string
	detailPlan          string
	detailExpiry        int64
	detailStatus        string
	detailError         string
	credentialReady     bool
	externalAccount     bool
	recoveryMarker      map[string]any
	refreshMode         string
	refreshCalls        int
	loginCalls          int
	applyCalls          int
	applyBody           map[string]any
	applyKeepsError     bool
	applyKeepsWorkspace bool
	applyFailsOnce      bool
	probeOK             bool
	probeBody           string
	probeStatus         int
	probePausesAccount  bool
	enableFailsOnce     bool
	testCalls           int
	methods             []string
	events              []string
}

func newRecoveryFixture(t *testing.T, schedulable bool, accessTokens ...string) *recoveryFixture {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "accounts.db")
	history, err := store.Open(dbPath, filepath.Join(dir, "secret.key"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = history.Close() })
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	account, err := history.UpsertCredentials(ctx, store.Credentials{Email: "recover@example.test", Password: "password"})
	if err != nil {
		t.Fatal(err)
	}
	attempt, err := history.StartAttempt(ctx, account.Email)
	if err != nil {
		t.Fatal(err)
	}
	accessToken := "old-at"
	if len(accessTokens) > 0 {
		accessToken = accessTokens[0]
	}
	if err := history.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: accessToken, RefreshToken: "old-rt", ChatGPTAccountID: "workspace", PlanType: "self_serve_business_prolite"}, nil); err != nil {
		t.Fatal(err)
	}
	attempt.Release()
	account, err = history.GetAccountByID(ctx, account.ID)
	if err != nil {
		t.Fatal(err)
	}
	importTask, _, err := history.CreateOrGetSub2Import(ctx, "fixture", account.ID, "operation", "idempotency", []byte(`{}`))
	if err != nil {
		t.Fatal(err)
	}
	if err := history.UpdateSub2Import(ctx, importTask.ID, "imported", true, 901, ""); err != nil {
		t.Fatal(err)
	}
	batch, reused, err := history.CreateAccountCheckBatch(ctx, store.AccountCheckInput{
		RequestKey: "recovery-check", AccountIDs: []int64{account.ID}, Concurrency: 1, ProxyMode: "direct",
	})
	if err != nil || reused {
		t.Fatalf("create check batch reused=%v err=%v", reused, err)
	}
	work, err := history.ClaimAccountCheck(ctx)
	if err != nil || work == nil || work.Check.BatchID != batch.ID {
		t.Fatalf("claim check work=%+v err=%v", work, err)
	}
	status := http.StatusUnauthorized
	if _, err := history.FinishAccountCheck(ctx, work.Check.ID, store.AccountCheckResult{
		Outcome: "unauthorized", ErrorCode: "unauthorized", HTTPStatus: &status,
	}); err != nil {
		t.Fatal(err)
	}
	f := &recoveryFixture{
		store: history, db: db, account: account, schedule: schedulable,
		detailEmail: account.Email, detailStatus: "error", detailError: "401 unauthorized",
		detailWorkspace: "workspace", detailOrg: "business-org", detailPlan: "self_serve_business_prolite",
		credentialReady: true, probeOK: true,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	importer := &sub2ImportService{
		store: history, baseURL: f.server.URL, adminAPIKey: "fixture-key", destinationKey: "fixture", client: f.server.Client(),
	}
	f.service = newSub2RecoveryService(history, nil, importer)
	f.service.loginWithProxies = func(_ context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
		f.loginCalls++
		if email != f.account.Email || password != "password" {
			t.Fatalf("login did not use saved account credentials")
		}
		return &login.LoginResult{Email: email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace"}, nil
	}
	t.Cleanup(f.service.Stop)
	// Sub2's error state represents an automatic pause after an auth failure;
	// the successful recovery should reopen that schedule. An active account
	// with schedulable=false below remains a manual pause case.
	restoreSchedule := schedulable || f.detailStatus == "error"
	f.task, _, err = history.CreateOrGetAccountRecoveryTask(ctx, account.ID, work.Check.ID, 901, restoreSchedule)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func (f *recoveryFixture) handle(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.methods = append(f.methods, r.Method+" "+r.URL.Path)
	w.Header().Set("Content-Type", "application/json")
	switch r.URL.Path {
	case "/admin/accounts/901", "/admin/accounts":
		detail := map[string]any{
			"id": 901, "platform": "openai", "type": "oauth", "schedulable": f.schedule,
			"status": f.detailStatus, "error_message": f.detailError,
			"credentials":        map[string]any{"email": f.detailEmail, "chatgpt_account_id": f.detailWorkspace, "organization_id": f.detailOrg, "plan_type": f.detailPlan, "expires_at": f.detailExpiry},
			"credentials_status": map[string]any{"has_access_token": f.credentialReady, "has_refresh_token": f.credentialReady},
			"extra":              map[string]any{"kkai_auth_import": map[string]any{"source_account_id": f.account.ID}},
		}
		if f.externalAccount {
			detail["extra"] = map[string]any{}
		}
		if f.recoveryMarker != nil {
			detail["extra"].(map[string]any)["kkai_auth_recovery"] = f.recoveryMarker
		}
		if r.URL.Path == "/admin/accounts" {
			writeRecoveryEnvelope(w, map[string]any{"items": []any{detail}, "total": 1, "pages": 1})
		} else {
			writeRecoveryEnvelope(w, detail)
		}
	case "/admin/openai/refresh-token":
		f.refreshCalls++
		f.events = append(f.events, "refresh")
		switch f.refreshMode {
		case "invalid_grant":
			writeRecoveryError(w, http.StatusBadGateway, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", `token refresh failed: status 400, body: {"error":"invalid_grant"}`)
		case "invalid_client":
			writeRecoveryError(w, http.StatusBadGateway, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", `token refresh failed: status 400, body: {"error":"invalid_client"}`)
		case "429":
			writeRecoveryError(w, http.StatusTooManyRequests, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", "rate limited")
		case "502":
			writeRecoveryError(w, http.StatusBadGateway, "UPSTREAM_FAILURE", "upstream failure")
		case "400_invalid_grant":
			writeRecoveryError(w, http.StatusBadRequest, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", `token refresh failed: status 400, body: {"error":"invalid_grant"}`)
		case "502_invalid_grant_wrong_reason":
			writeRecoveryError(w, http.StatusBadGateway, "UPSTREAM_FAILURE", `token refresh failed: status 400, body: {"error":"invalid_grant"}`)
		case "500_invalid_grant":
			writeRecoveryError(w, http.StatusInternalServerError, "OPENAI_OAUTH_TOKEN_REFRESH_FAILED", `token refresh failed: status 400, body: {"error":"invalid_grant"}`)
		default:
			email := f.account.Email
			if f.refreshMode == "wrong_identity" {
				email = "wrong@example.test"
			}
			writeRecoveryEnvelope(w, map[string]any{"access_token": "new-at", "refresh_token": "new-rt", "email": email, "chatgpt_account_id": "workspace", "organization_id": "org", "plan_type": "plus", "expires_at": 123})
		}
	case "/admin/accounts/901/schedulable":
		var body struct {
			Schedulable bool `json:"schedulable"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body.Schedulable && f.enableFailsOnce {
			f.enableFailsOnce = false
			w.Header().Set("Retry-After", "180")
			writeRecoveryError(w, http.StatusServiceUnavailable, "unavailable", "temporary failure")
			return
		}
		f.schedule = body.Schedulable
		if body.Schedulable {
			f.events = append(f.events, "enable")
		} else {
			f.events = append(f.events, "disable")
		}
		writeRecoveryEnvelope(w, map[string]any{})
	case "/admin/accounts/901/apply-oauth-credentials":
		var body map[string]any
		_ = json.NewDecoder(r.Body).Decode(&body)
		f.applyCalls++
		f.applyBody = body
		f.events = append(f.events, "apply")
		if f.applyFailsOnce {
			f.applyFailsOnce = false
			writeRecoveryError(w, http.StatusServiceUnavailable, "unavailable", "temporary failure")
			return
		}
		if values, ok := body["credentials"].(map[string]any); ok {
			if !f.applyKeepsWorkspace {
				f.detailWorkspace, _ = values["chatgpt_account_id"].(string)
			}
			f.detailOrg, _ = values["organization_id"].(string)
			f.detailPlan, _ = values["plan_type"].(string)
			f.detailExpiry = int64(toFloat(values["expires_at"]))
		}
		if !f.applyKeepsError {
			f.detailStatus, f.detailError, f.credentialReady = "active", "", true
		}
		if extra, ok := body["extra"].(map[string]any); ok {
			if marker, ok := extra["kkai_auth_recovery"].(map[string]any); ok {
				f.recoveryMarker = marker
			}
		}
		writeRecoveryEnvelope(w, map[string]any{})
	case "/admin/accounts/901/test":
		f.testCalls++
		f.events = append(f.events, "test")
		if f.probePausesAccount {
			f.schedule = false
		}
		w.Header().Set("Content-Type", "text/event-stream")
		if f.probeStatus != 0 {
			w.Header().Set("Retry-After", "180")
			w.WriteHeader(f.probeStatus)
			return
		}
		if f.probeBody != "" {
			_, _ = io.WriteString(w, f.probeBody)
		} else if f.probeOK {
			_, _ = io.WriteString(w, "data: {\"type\":\"test_complete\",\"success\":true}\n\n")
		} else {
			_, _ = io.WriteString(w, "data: {\"type\":\"test_complete\",\"success\":false}\n\n")
		}
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"code":1,"message":"not found"}`)
	}
}

func writeRecoveryEnvelope(w http.ResponseWriter, data any) {
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "data": data})
}

func writeRecoveryError(w http.ResponseWriter, status int, reason, message string) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"code": 1, "reason": reason, "message": message})
}

func (f *recoveryFixture) state(t *testing.T) store.AccountRecoveryTask {
	t.Helper()
	task, err := f.store.GetAccountRecoveryTaskByID(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	return task
}

func finishRecoveryCheck(t *testing.T, s *store.Store, db *sql.DB, accountID int64, requestKey, outcome, errorCode, streamErrorCode string, httpStatus *int) int64 {
	t.Helper()
	if _, err := db.Exec(`UPDATE account_checks SET finished_at=0 WHERE account_id=? AND state='finished'`, accountID); err != nil {
		t.Fatal(err)
	}
	batch, reused, err := s.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{
		RequestKey: requestKey, AccountIDs: []int64{accountID}, Concurrency: 1, ProxyMode: "direct",
	})
	if err != nil || reused {
		t.Fatalf("create check batch reused=%v err=%v", reused, err)
	}
	work, err := s.ClaimAccountCheck(context.Background())
	if err != nil || work == nil || work.Check.BatchID != batch.ID {
		t.Fatalf("claim check work=%+v err=%v", work, err)
	}
	if _, err := s.FinishAccountCheck(context.Background(), work.Check.ID, store.AccountCheckResult{
		Outcome: outcome, ErrorCode: errorCode, StreamErrorCode: streamErrorCode, HTTPStatus: httpStatus,
	}); err != nil {
		t.Fatal(err)
	}
	return work.Check.ID
}

func TestSub2RecoveryValidateCandidateRecognizesAuthFailures(t *testing.T) {
	for _, tc := range []struct {
		name, outcome, errorCode, streamErrorCode string
	}{
		{name: "expired_outcome", outcome: "access_token_expired"},
		{name: "missing_error_code", outcome: "error", errorCode: "credential_missing"},
		{name: "stream_auth_error", outcome: "error", streamErrorCode: "authentication_failed"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			status := http.StatusBadGateway
			if tc.outcome == "" {
				tc.outcome = "authentication_error"
			}
			id := finishRecoveryCheck(t, f.store, f.db, f.account.ID, "candidate-"+tc.name, tc.outcome, tc.errorCode, tc.streamErrorCode, &status)
			got, err := f.service.validateCandidate(context.Background(), f.account.ID)
			if err != nil || got != id {
				t.Fatalf("candidate id=%d err=%v want=%d", got, err, id)
			}
		})
	}
}

func TestSub2RecoveryAutoSettings(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.service.autoRecovery.Store(false)
	put := httptest.NewRequest(http.MethodPut, "/api/account-recovery/settings", strings.NewReader(`{"auto_recovery_enabled":true}`))
	put.Header.Set("Content-Type", "application/json")
	putRecorder := httptest.NewRecorder()
	f.service.handleAction(putRecorder, put)
	if putRecorder.Code != http.StatusOK || !f.service.autoRecovery.Load() {
		t.Fatalf("settings PUT code=%d enabled=%v body=%s", putRecorder.Code, f.service.autoRecovery.Load(), putRecorder.Body.String())
	}
	get := httptest.NewRequest(http.MethodGet, "/api/account-recovery/settings", nil)
	getRecorder := httptest.NewRecorder()
	f.service.handleAction(getRecorder, get)
	if getRecorder.Code != http.StatusOK || !strings.Contains(getRecorder.Body.String(), `"auto_recovery_enabled":true`) {
		t.Fatalf("settings GET code=%d body=%s", getRecorder.Code, getRecorder.Body.String())
	}
}

func TestSub2RecoveryLogsInAppliesProbesAndRestoresSchedule(t *testing.T) {
	for _, initialSchedule := range []bool{false, true} {
		t.Run(map[bool]string{true: "enabled", false: "disabled"}[initialSchedule], func(t *testing.T) {
			f := newRecoveryFixture(t, initialSchedule)
			f.service.process(f.task)
			got := f.state(t)
			if got.State != store.RecoveryCompleted {
				t.Fatalf("state=%s error=%q", got.State, got.LastError)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.refreshCalls != 0 || f.loginCalls != 1 || f.applyCalls != 1 || f.testCalls != 1 || !f.schedule {
				t.Fatalf("refresh=%d login=%d apply=%d test=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.testCalls, f.schedule)
			}
			credentials, _ := f.applyBody["credentials"].(map[string]any)
			if credentials["access_token"] != "new-at" || credentials["refresh_token"] != "new-rt" {
				t.Fatalf("apply credentials=%v", credentials)
			}
			if f.applyBody["require_recovery_verification"] != nil || f.recoveryMarker["task_id"] != float64(got.ID) || f.recoveryMarker["credential_attempt_id"] != float64(got.ResultCredentialAttemptID) {
				t.Fatalf("apply body=%v", f.applyBody)
			}
			want := "apply test enable"
			if initialSchedule {
				want = "disable apply test enable"
			}
			if strings.Join(f.events, " ") != want {
				t.Fatalf("side effect order=%v want=%s", f.events, want)
			}
			_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
			if err != nil || saved.AccessToken != "new-at" || saved.RefreshToken != "new-rt" {
				t.Fatalf("rotated token not persisted: %+v err=%v", saved, err)
			}
		})
	}
}

func TestSub2RecoveryPreservesManualPause(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.detailStatus = "active"
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET original_schedulable=0 WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.task.OriginalSchedulable = false
	f.service.process(f.task)
	if got := f.state(t); got.State != store.RecoveryCompleted {
		t.Fatalf("state=%s error=%q", got.State, got.LastError)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.schedule || strings.Contains(strings.Join(f.events, " "), "enable") {
		t.Fatalf("manual pause was reopened: schedule=%v events=%v", f.schedule, f.events)
	}
}

func TestSub2RecoveryRejectsIdentityAndNeverWrites(t *testing.T) {
	for _, stage := range []string{"existing", "refreshed"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			if stage == "existing" {
				f.detailEmail = "wrong@example.test"
			} else {
				f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
					f.loginCalls++
					return &login.LoginResult{Email: "wrong@example.test", AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "workspace"}, nil
				}
			}
			f.service.process(f.task)
			got := f.state(t)
			if got.State != store.RecoveryFailed && got.State != store.RecoveryUnknown {
				t.Fatalf("state=%s error=%q", got.State, got.LastError)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.applyCalls != 0 || f.schedule {
				t.Fatalf("unsafe side effects methods=%v", f.methods)
			}
			_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
			if err != nil || saved.AccessToken != "old-at" || saved.RefreshToken != "old-rt" {
				t.Fatalf("identity mismatch changed local tokens: %+v err=%v", saved, err)
			}
		})
	}
}

func TestSub2RecoveryCannotEnableWhenPostApplyChecksFail(t *testing.T) {
	for _, failure := range []string{"still_error", "probe_failure"} {
		t.Run(failure, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			switch failure {
			case "still_error":
				f.applyKeepsError = true
			case "probe_failure":
				f.probeOK = false
			}
			f.service.process(f.task)
			if got := f.state(t); got.State != store.RecoveryUnknown {
				t.Fatalf("state=%s error=%q", got.State, got.LastError)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.schedule || strings.Contains(strings.Join(f.events, " "), "enable") {
				t.Fatalf("schedule enabled after %s: events=%v", failure, f.events)
			}
		})
	}
}

func TestSub2RecoveryInvalidGrantLogsInAndCheckpointsVersion(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.loginWithProxies = func(_ context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
		f.loginCalls++
		if email != f.account.Email || password != "password" {
			t.Error("login did not use saved account credentials")
		}
		return &login.LoginResult{Email: email, AccessToken: "login-at", RefreshToken: "login-rt", ChatGPTAccountID: "workspace"}, nil
	}
	f.service.process(f.task)
	got := f.state(t)
	if got.State != store.RecoveryCompleted {
		t.Fatalf("state=%s error=%q", got.State, got.LastError)
	}
	if got.ResultCredentialAttemptID == 0 || got.ResultCredentialAttemptID == got.SourceCredentialAttemptID {
		t.Fatalf("login checkpoint=%+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshCalls != 0 || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
		t.Fatalf("refresh=%d login=%d apply=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
	}
	_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
	if err != nil || saved.AccessToken != "login-at" || saved.RefreshToken != "login-rt" {
		t.Fatalf("new login not stored=%+v err=%v", saved, err)
	}
}

func TestSub2RecoveryDoesNotUseRefreshEndpoint(t *testing.T) {
	for _, mode := range []string{"invalid_client", "429", "502", "400_invalid_grant", "502_invalid_grant_wrong_reason", "500_invalid_grant"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			f.refreshMode = mode
			f.service.process(f.task)
			if got := f.state(t); got.State != store.RecoveryCompleted || got.ResultCredentialAttemptID == 0 {
				t.Fatalf("mode=%s state=%+v", mode, got)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.refreshCalls != 0 || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
				t.Fatalf("mode=%s refresh=%d login=%d apply=%d schedule=%v", mode, f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
			}
		})
	}
}

func TestSub2RecoveryRejectsBlankLoginEmail(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.refreshMode = "invalid_grant"
	f.service.loginWithProxies = func(_ context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
		f.loginCalls++
		return &login.LoginResult{Email: "", AccessToken: "login-at", RefreshToken: "login-rt", ChatGPTAccountID: "workspace"}, nil
	}
	f.service.process(f.task)
	got := f.state(t)
	if got.State != store.RecoveryFailed && got.State != store.RecoveryUnknown {
		t.Fatalf("state=%s error=%q", got.State, got.LastError)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.loginCalls != 1 || f.applyCalls != 0 || f.schedule {
		t.Fatalf("blank email was accepted: login=%d apply=%d schedule=%v", f.loginCalls, f.applyCalls, f.schedule)
	}
}

func recoveryAccessToken(t *testing.T, workspace, userID, plan string) string {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"exp": int64(2100000000),
		"https://api.openai.com/auth": map[string]any{
			"chatgpt_account_id": workspace, "chatgpt_user_id": userID, "chatgpt_plan_type": plan,
		},
		"https://api.openai.com/profile": map[string]any{"email": "recover@example.test"},
	})
	if err != nil {
		t.Fatal(err)
	}
	return "e30." + base64.RawURLEncoding.EncodeToString(body) + ".sig"
}

func TestSub2RecoveryChangesWorkspaceAndPlan(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "resume_before_remote_update"}[interrupted], func(t *testing.T) {
			oldToken := recoveryAccessToken(t, "workspace", "same-user", "self_serve_business_prolite")
			newToken := recoveryAccessToken(t, "personal-workspace", "same-user", "free")
			f := newRecoveryFixture(t, true, oldToken)
			f.applyFailsOnce = interrupted
			f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
				f.loginCalls++
				return &login.LoginResult{Email: f.account.Email, AccessToken: newToken, RefreshToken: "new-rt", ChatGPTAccountID: "personal-workspace", PlanType: "free", ExpiresAt: 2100000000}, nil
			}
			f.service.process(f.task)
			if interrupted {
				if got := f.state(t); got.State != store.RecoveryUnknown || !got.Resumable || f.schedule || f.detailWorkspace != "workspace" {
					t.Fatalf("checkpoint before apply=%+v schedule=%v workspace=%s", got, f.schedule, f.detailWorkspace)
				}
				_, statuses, err := f.service.sub2.syncAccountStatuses(context.Background())
				status := statuses[f.account.ID]
				if err != nil || status.Unknown || !status.Exists || status.Sub2AccountID != 901 {
					t.Fatalf("workspace change hid resumable binding: status=%+v err=%v", status, err)
				}
				queued, created, err := f.service.enqueue(context.Background(), f.account.ID)
				if err != nil || created || queued.ID != f.task.ID || queued.State != store.RecoveryQueued {
					t.Fatalf("workspace change blocked resume: task=%+v created=%v err=%v", queued, created, err)
				}
				f.service.process(queued)
			}
			if got := f.state(t); got.State != store.RecoveryCompleted || got.Sub2AccountID != 901 || f.loginCalls != 1 || !f.schedule {
				t.Fatalf("workspace migration=%+v login=%d schedule=%v", got, f.loginCalls, f.schedule)
			}
			_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
			if err != nil || saved.AccessToken != newToken || saved.ChatGPTAccountID != "personal-workspace" || saved.PlanType != "free" {
				t.Fatalf("new local credentials not saved: workspace=%s plan=%s err=%v", saved.ChatGPTAccountID, saved.PlanType, err)
			}
			if f.detailWorkspace != "personal-workspace" || f.detailPlan != "free" || f.detailOrg != "" || f.detailExpiry != 2100000000 {
				t.Fatalf("remote metadata workspace=%s plan=%s org=%s expires=%d", f.detailWorkspace, f.detailPlan, f.detailOrg, f.detailExpiry)
			}
		})
	}
}

func TestSub2RecoveryAllowsDifferentStableUserWhenEmailMatches(t *testing.T) {
	oldToken := recoveryAccessToken(t, "workspace", "original-user", "self_serve_business_prolite")
	f := newRecoveryFixture(t, true, oldToken)
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		f.loginCalls++
		return &login.LoginResult{Email: f.account.Email, AccessToken: recoveryAccessToken(t, "personal-workspace", "another-user", "free"), RefreshToken: "new-rt", ChatGPTAccountID: "personal-workspace", PlanType: "free"}, nil
	}
	f.service.process(f.task)
	account, err := f.store.GetAccountByID(context.Background(), f.account.ID)
	if err != nil || account.LastErrorCode != "" || f.applyCalls != 1 || !f.schedule {
		t.Fatalf("different user was not recovered: code=%s apply=%d schedule=%v err=%v", account.LastErrorCode, f.applyCalls, f.schedule, err)
	}
	_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
	if err != nil || saved.AccessToken == oldToken || saved.ChatGPTAccountID != "personal-workspace" {
		t.Fatal("new credentials were not saved")
	}
}

func TestSub2RecoveryCannotEnableWhenNewWorkspaceWasNotApplied(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.applyKeepsWorkspace = true
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		return &login.LoginResult{Email: f.account.Email, AccessToken: "new-at", RefreshToken: "new-rt", ChatGPTAccountID: "personal-workspace", PlanType: "free"}, nil
	}
	f.service.process(f.task)
	if got := f.state(t); got.State != store.RecoveryUnknown || !got.Resumable || f.schedule || f.testCalls != 0 {
		t.Fatalf("old workspace passed post-apply validation: task=%+v schedule=%v probes=%d", got, f.schedule, f.testCalls)
	}
}

func TestSub2RecoveryAllowsMissingStableUserMetadata(t *testing.T) {
	known := recoveryAccessToken(t, "workspace", "same-user", "free")
	for _, tokens := range [][2]string{{known, "opaque-token"}, {"opaque-token", known}} {
		credentials := recoveryOAuthCredentials{AccessToken: tokens[1], RefreshToken: "rt", Email: "recover@example.test", ChatGPTAccountID: "new-workspace"}
		if err := verifyOAuthIdentity(credentials, store.Account{Email: credentials.Email, ChatGPTAccountID: "old-workspace"}); err != nil {
			t.Fatalf("legacy token metadata blocked recovery: %v", err)
		}
	}
}

func TestSub2RecoveryResumeUsesCheckpointWithoutRefresh(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.probeOK = false
	f.service.process(f.task)
	if got := f.state(t); got.State != store.RecoveryUnknown || !got.Resumable {
		t.Fatalf("first attempt=%+v", got)
	}
	f.probeOK = true
	queued, created, err := f.service.enqueue(context.Background(), f.account.ID)
	if err != nil || created || queued.ID != f.task.ID || queued.State != store.RecoveryQueued {
		t.Fatalf("resume enqueue=%+v created=%v err=%v", queued, created, err)
	}
	claimed, err := f.store.ClaimAccountRecoveryTask(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(claimed)
	if got := f.state(t); got.State != store.RecoveryCompleted {
		t.Fatalf("resumed=%+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshCalls != 0 || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
		t.Fatalf("resume reused checkpoint incorrectly: refresh=%d login=%d apply=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
	}
}

func recoveryRetryDue(t *testing.T, f *recoveryFixture) {
	t.Helper()
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET next_retry_at=? WHERE id=?`, time.Now().Add(-time.Second).UnixMilli(), f.task.ID); err != nil {
		t.Fatal(err)
	}
}

func runQueuedRecovery(t *testing.T, f *recoveryFixture) {
	t.Helper()
	claimed, err := f.store.ClaimAccountRecoveryTask(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.service.process(claimed)
}

func TestSub2RecoveryAutomaticCheckpointRetry(t *testing.T) {
	for _, stage := range []string{"probe", "enable"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			f.service.autoRecovery.Store(true)
			if stage == "probe" {
				f.probeStatus = http.StatusServiceUnavailable
			} else {
				f.enableFailsOnce = true
			}
			f.service.process(f.task)
			failed := f.state(t)
			if failed.State != store.RecoveryUnknown || failed.RetryAction != "resume" || failed.NextRetryAt == nil || failed.RequiresAction || f.schedule || f.detailStatus != "active" {
				t.Fatalf("checkpoint not safely retained: %+v", failed)
			}
			f.service.retryDueRecoveries()
			if f.state(t).State != store.RecoveryUnknown {
				t.Fatal("retry bypassed persisted deadline")
			}
			f.probeStatus = 0
			recoveryRetryDue(t, f)
			f.service.retryDueRecoveries()
			runQueuedRecovery(t, f)
			got := f.state(t)
			if got.State != store.RecoveryCompleted || got.RetryAction != "" || got.NextRetryAt != nil || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
				t.Fatalf("checkpoint did not complete without relogin/reapply: %+v login=%d apply=%d schedule=%v", got, f.loginCalls, f.applyCalls, f.schedule)
			}
		})
	}
}

func TestSub2RecoveryInvalidCheckpointRelogs(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	f.probeBody = "data: {\"type\":\"error\",\"error\":{\"code\":\"token_revoked\",\"status\":401,\"message\":\"at-secret\"}}\n\n"
	f.service.process(f.task)
	failed := f.state(t)
	if failed.RetryAction != "relogin" || failed.Resumable || failed.RequiresAction || failed.NextRetryAt == nil || strings.Contains(failed.LastError, "at-secret") {
		t.Fatalf("invalid checkpoint retained or exposed error: %+v", failed)
	}
	oldVersion := failed.ResultCredentialAttemptID
	f.probeBody = ""
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	queued := f.state(t)
	if queued.ResultCredentialAttemptID != 0 || queued.SourceCredentialAttemptID != oldVersion {
		t.Fatalf("checkpoint not invalidated: %+v", queued)
	}
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || got.ResultCredentialAttemptID == oldVersion || f.loginCalls != 2 || f.applyCalls != 2 || !f.schedule {
		t.Fatalf("invalid checkpoint did not relogin safely: %+v login=%d apply=%d", got, f.loginCalls, f.applyCalls)
	}
}

func TestSub2RecoveryRetryRejectsChangedOwnership(t *testing.T) {
	for _, change := range []string{"marker", "metadata", "disabled", "version", "original_pause"} {
		t.Run(change, func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			f.service.autoRecovery.Store(true)
			f.probeStatus = http.StatusServiceUnavailable
			f.service.process(f.task)
			switch change {
			case "marker":
				f.recoveryMarker["task_id"] = float64(9999)
			case "metadata":
				f.detailWorkspace = "someone-else"
			case "disabled":
				f.detailStatus = "inactive"
			case "version":
				if err := f.store.UpdateOAuthResultByID(context.Background(), f.account.ID, store.Result{AccessToken: "separate-at", RefreshToken: "separate-rt", ChatGPTAccountID: "workspace"}); err != nil {
					t.Fatal(err)
				}
			case "original_pause":
				if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET original_schedulable=0 WHERE id=?`, f.task.ID); err != nil {
					t.Fatal(err)
				}
			}
			recoveryRetryDue(t, f)
			f.service.retryDueRecoveries()
			got := f.state(t)
			if !got.RequiresAction || got.NextRetryAt != nil || f.loginCalls != 1 || f.applyCalls != 1 || f.schedule {
				t.Fatalf("unsafe retry: %+v", got)
			}
		})
	}
}

func TestSub2RecoveryTemporaryLoginFailureRetriesBeyondThree(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.service.autoRecovery.Store(true)
	workingLogin := f.service.loginWithProxies
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		f.loginCalls++
		return nil, context.DeadlineExceeded
	}
	f.service.process(f.task)
	for i := 0; i < 3; i++ {
		recoveryRetryDue(t, f)
		f.service.retryDueRecoveries()
		runQueuedRecovery(t, f)
	}
	if got := f.state(t); got.RetryCount != 4 || got.RequiresAction || got.RetryAction != "relogin" || got.NextRetryAt == nil {
		t.Fatalf("temporary errors hard-stopped: %+v", got)
	}
	f.service.loginWithProxies = workingLogin
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || !f.schedule {
		t.Fatalf("retry did not recover: %+v", got)
	}
}

func TestSub2RecoveryDeliveryRetryIgnoresAutomaticSwitch(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(false)
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET purpose='delivery',result_credential_attempt_id=source_credential_attempt_id,check_id=0 WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	var err error
	f.task, err = f.store.GetAccountRecoveryTaskByID(context.Background(), f.task.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.probeStatus = http.StatusServiceUnavailable
	f.service.process(f.task)
	f.probeStatus = 0
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || f.loginCalls != 0 || !f.schedule {
		t.Fatalf("delivery stopped with auto switch off: %+v login=%d", got, f.loginCalls)
	}
}

func TestSub2RecoveryRejectsStaleCheckAfterNewLogin(t *testing.T) {
	f := newRecoveryFixture(t, true)
	if err := f.store.UpdateOAuthResultByID(context.Background(), f.account.ID, store.Result{AccessToken: "newer-at", RefreshToken: "newer-rt", ChatGPTAccountID: "workspace"}); err != nil {
		t.Fatal(err)
	}
	f.service.process(f.task)
	if got := f.state(t); got.State != store.RecoveryFailed {
		t.Fatalf("state=%s error=%q", got.State, got.LastError)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.methods) != 0 {
		t.Fatalf("stale task made external calls: %v", f.methods)
	}
}

func TestSub2RecoveryStorageFailureStopsBeforeSideEffects(t *testing.T) {
	for _, state := range []string{store.RecoveryValidating, store.RecoveryApplyingCredentials, store.RecoveryEnablingSchedule} {
		t.Run(state, func(t *testing.T) {
			f := newRecoveryFixture(t, state == store.RecoveryEnablingSchedule)
			if _, err := f.db.Exec(`CREATE TRIGGER recovery_state_failure BEFORE UPDATE OF state ON account_recovery_tasks WHEN NEW.state='` + state + `' BEGIN SELECT RAISE(ABORT,'state persistence failed'); END`); err != nil {
				t.Fatal(err)
			}
			f.service.process(f.task)
			if !f.service.paused.Load() {
				t.Fatal("storage failure did not pause recovery service")
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if state == store.RecoveryValidating && len(f.methods) != 0 {
				t.Fatalf("validation state failure made external calls: %v", f.methods)
			}
			if state == store.RecoveryApplyingCredentials && f.applyCalls != 0 {
				t.Fatal("apply request sent despite state persistence failure")
			}
			if strings.Contains(strings.Join(f.events, " "), "enable") {
				t.Fatalf("enabled despite storage failure: %v", f.events)
			}
		})
	}
}

func TestSub2RecoveryCheckpointFailureCannotApplyUnstoredCredentials(t *testing.T) {
	f := newRecoveryFixture(t, true)
	if _, err := f.db.Exec(`CREATE TRIGGER recovery_checkpoint_failure BEFORE UPDATE OF result_credential_attempt_id ON account_recovery_tasks BEGIN SELECT RAISE(ABORT,'checkpoint persistence failed'); END`); err != nil {
		t.Fatal(err)
	}
	f.service.process(f.task)
	if !f.service.paused.Load() {
		t.Fatal("credential checkpoint failure did not pause service")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshCalls != 0 || f.loginCalls != 1 || f.applyCalls != 0 || f.schedule {
		t.Fatalf("unpersisted tokens reached Sub2: refresh=%d login=%d apply=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
	}
	version, err := f.store.GetAccountCredentialVersion(context.Background(), f.account.ID)
	if err != nil || version != f.task.SourceCredentialAttemptID {
		t.Fatalf("credential transaction partially committed: version=%d err=%v", version, err)
	}
}
