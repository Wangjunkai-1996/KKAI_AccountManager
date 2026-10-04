package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"

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

	mu              sync.Mutex
	schedule        bool
	detailEmail     string
	detailStatus    string
	detailError     string
	credentialReady bool
	recoveryMarker  map[string]any
	refreshMode     string
	refreshCalls    int
	loginCalls      int
	applyCalls      int
	applyBody       map[string]any
	applyKeepsError bool
	probeOK         bool
	testCalls       int
	methods         []string
	events          []string
}

func newRecoveryFixture(t *testing.T, schedulable bool) *recoveryFixture {
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
	if err := history.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "old-at", RefreshToken: "old-rt", ChatGPTAccountID: "workspace"}, nil); err != nil {
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
		credentialReady: true, probeOK: true,
	}
	f.server = httptest.NewServer(http.HandlerFunc(f.handle))
	t.Cleanup(f.server.Close)
	importer := &sub2ImportService{
		store: history, baseURL: f.server.URL, adminAPIKey: "fixture-key", destinationKey: "fixture", client: f.server.Client(),
	}
	f.service = newSub2RecoveryService(history, nil, importer)
	t.Cleanup(f.service.Stop)
	f.task, _, err = history.CreateOrGetAccountRecoveryTask(ctx, account.ID, work.Check.ID, 901, schedulable)
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
	case "/admin/accounts/901":
		detail := map[string]any{
			"id": 901, "platform": "openai", "type": "oauth", "schedulable": f.schedule,
			"status": f.detailStatus, "error_message": f.detailError,
			"credentials":        map[string]any{"email": f.detailEmail, "chatgpt_account_id": "workspace"},
			"credentials_status": map[string]any{"has_access_token": f.credentialReady, "has_refresh_token": f.credentialReady},
			"extra":              map[string]any{"kkai_auth_import": map[string]any{"source_account_id": f.account.ID}},
		}
		if f.recoveryMarker != nil {
			detail["extra"].(map[string]any)["kkai_auth_recovery"] = f.recoveryMarker
		}
		writeRecoveryEnvelope(w, detail)
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
		w.Header().Set("Content-Type", "text/event-stream")
		if f.probeOK {
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

func TestSub2RecoveryRefreshesAppliesProbesAndEnables(t *testing.T) {
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
			if f.refreshCalls != 1 || f.applyCalls != 1 || f.testCalls != 1 || !f.schedule {
				t.Fatalf("refresh=%d apply=%d test=%d schedule=%v", f.refreshCalls, f.applyCalls, f.testCalls, f.schedule)
			}
			credentials, _ := f.applyBody["credentials"].(map[string]any)
			if credentials["access_token"] != "new-at" || credentials["refresh_token"] != "new-rt" {
				t.Fatalf("apply credentials=%v", credentials)
			}
			if f.applyBody["require_recovery_verification"] != nil || f.recoveryMarker["task_id"] != float64(got.ID) || f.recoveryMarker["credential_attempt_id"] != float64(got.ResultCredentialAttemptID) {
				t.Fatalf("apply body=%v", f.applyBody)
			}
			want := "refresh apply test enable"
			if initialSchedule {
				want = "disable " + want
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

func TestSub2RecoveryRejectsIdentityAndNeverWrites(t *testing.T) {
	for _, stage := range []string{"existing", "refreshed"} {
		t.Run(stage, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			if stage == "existing" {
				f.detailEmail = "wrong@example.test"
			} else {
				f.refreshMode = "wrong_identity"
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
	f.refreshMode = "invalid_grant"
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
	if f.refreshCalls != 1 || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
		t.Fatalf("refresh=%d login=%d apply=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
	}
	_, saved, err := f.store.GetOAuthResultByID(context.Background(), f.account.ID)
	if err != nil || saved.AccessToken != "login-at" || saved.RefreshToken != "login-rt" {
		t.Fatalf("new login not stored=%+v err=%v", saved, err)
	}
}

func TestSub2RecoveryTransientErrorsDoNotFallback(t *testing.T) {
	for _, mode := range []string{"invalid_client", "429", "502", "400_invalid_grant", "502_invalid_grant_wrong_reason", "500_invalid_grant"} {
		t.Run(mode, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			f.refreshMode = mode
			f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
				f.loginCalls++
				return &login.LoginResult{Email: f.account.Email, AccessToken: "login-at", RefreshToken: "login-rt", ChatGPTAccountID: "workspace"}, nil
			}
			f.service.process(f.task)
			if got := f.state(t); got.State != store.RecoveryUnknown || got.ResultCredentialAttemptID != 0 {
				t.Fatalf("mode=%s state=%+v", mode, got)
			}
			f.mu.Lock()
			defer f.mu.Unlock()
			if f.loginCalls != 0 || f.applyCalls != 0 || f.schedule {
				t.Fatalf("mode=%s login=%d apply=%d schedule=%v", mode, f.loginCalls, f.applyCalls, f.schedule)
			}
		})
	}
}

func TestSub2RecoveryNewCheckAfterUncertainRefreshForcesLogin(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.refreshMode = "502"
	f.service.process(f.task)
	first := f.state(t)
	if first.State != store.RecoveryUnknown || first.ResultCredentialAttemptID != 0 {
		t.Fatalf("first attempt=%+v", first)
	}

	f.service.loginWithProxies = func(_ context.Context, email, password, totp, proxy, upstream string) (*login.LoginResult, error) {
		f.loginCalls++
		return &login.LoginResult{Email: email, AccessToken: "login-at", RefreshToken: "login-rt", ChatGPTAccountID: "workspace"}, nil
	}
	checkID := finishRecoveryCheck(t, f.store, f.db, f.account.ID, "candidate-after-uncertain-refresh", "unauthorized", "unauthorized", "", func() *int { v := http.StatusUnauthorized; return &v }())
	queued, created, err := f.service.enqueue(context.Background(), f.account.ID)
	if err != nil || !created || queued.CheckID != checkID || queued.SourceCredentialAttemptID != first.SourceCredentialAttemptID {
		t.Fatalf("new task=%+v created=%v err=%v", queued, created, err)
	}
	claimed, err := f.store.ClaimAccountRecoveryTask(context.Background(), queued.ID)
	if err != nil {
		t.Fatal(err)
	}
	f.task = claimed
	f.service.process(claimed)
	if got := f.state(t); got.State != store.RecoveryCompleted {
		t.Fatalf("recovered task=%+v", got)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refreshCalls != 1 || f.loginCalls != 1 || f.applyCalls != 1 || !f.schedule {
		t.Fatalf("refresh=%d login=%d apply=%d schedule=%v", f.refreshCalls, f.loginCalls, f.applyCalls, f.schedule)
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
	if f.refreshCalls != 1 || f.applyCalls != 2 || !f.schedule {
		t.Fatalf("resume consumed RT again: refresh=%d apply=%d schedule=%v", f.refreshCalls, f.applyCalls, f.schedule)
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
	for _, state := range []string{store.RecoveryValidating, store.RecoveryRefreshingCredentials, store.RecoveryApplyingCredentials, store.RecoveryEnablingSchedule} {
		t.Run(state, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
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
			if state == store.RecoveryRefreshingCredentials && f.refreshCalls != 0 {
				t.Fatal("refresh request sent despite state persistence failure")
			}
			if state == store.RecoveryApplyingCredentials && f.applyCalls != 0 {
				t.Fatal("apply request sent despite state persistence failure")
			}
			if f.schedule || strings.Contains(strings.Join(f.events, " "), "enable") {
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
	if f.refreshCalls != 1 || f.applyCalls != 0 || f.schedule {
		t.Fatalf("unpersisted tokens reached Sub2: refresh=%d apply=%d schedule=%v", f.refreshCalls, f.applyCalls, f.schedule)
	}
	version, err := f.store.GetAccountCredentialVersion(context.Background(), f.account.ID)
	if err != nil || version != f.task.SourceCredentialAttemptID {
		t.Fatalf("credential transaction partially committed: version=%d err=%v", version, err)
	}
}
