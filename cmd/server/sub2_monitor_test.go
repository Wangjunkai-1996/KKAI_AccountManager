package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/probe"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func newMonitorFixture(t *testing.T) *recoveryFixture {
	t.Helper()
	f := newRecoveryFixture(t, false)
	for _, table := range []string{"account_recovery_tasks", "account_checks", "account_check_batches"} {
		if _, err := f.db.Exec("DELETE FROM " + table); err != nil {
			t.Fatal(err)
		}
	}
	f.service.autoRecovery.Store(false)
	f.service.checker = newAccountCheckService(f.store, "", "")
	t.Cleanup(f.service.checker.Stop)
	previous := accountRecoveryService
	accountRecoveryService = f.service
	t.Cleanup(func() { accountRecoveryService = previous })
	f.service.checker.runProbe = func(context.Context, string, string, string) probe.Result {
		return probe.Result{Outcome: "unauthorized", HTTPStatus: http.StatusUnauthorized, RequestAttempted: true}
	}
	return f
}

func TestSub2RecoverySettingsRuntimeStatus(t *testing.T) {
	for _, scenario := range []string{"running", "disabled", "storage_failure", "stopped", "unconfigured", "transient_error", "checker_unconfigured", "checker_paused", "checker_stopped"} {
		t.Run(scenario, func(t *testing.T) {
			f := newRecoveryFixture(t, false)
			s := f.service
			s.checker = newAccountCheckService(f.store, "", "")
			t.Cleanup(s.checker.Stop)
			s.autoRecovery.Store(true)
			next := time.Now().Add(time.Minute)
			s.monitorState.NextScanAt = &next
			s.monitorState.Scanning = true
			wantReason := ""
			switch scenario {
			case "disabled":
				s.autoRecovery.Store(false)
			case "storage_failure":
				if _, err := f.db.Exec(`CREATE TRIGGER settings_storage_failure BEFORE UPDATE ON account_recovery_tasks BEGIN SELECT RAISE(ABORT,'storage failure'); END`); err != nil {
					t.Fatal(err)
				}
				if s.setState(f.task.ID, store.RecoveryValidating, "") {
					t.Fatal("expected injected storage failure")
				}
				wantReason = "恢复任务存储故障"
				for _, enabled := range []string{"false", "true"} {
					request := httptest.NewRequest(http.MethodPut, "/api/account-recovery/settings", strings.NewReader(`{"enabled":`+enabled+`}`))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					s.handleAction(response, request)
					if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"running":false`) || !strings.Contains(response.Body.String(), wantReason) {
						t.Fatalf("toggling settings hid storage failure: %d %s", response.Code, response.Body.String())
					}
				}
			case "stopped":
				s.cancel()
				wantReason = "恢复服务已停止"
			case "unconfigured":
				s.sub2 = nil
				wantReason = "Sub2 恢复服务未配置"
			case "transient_error":
				s.monitorState.LastError = "Sub2 状态同步失败，下次扫描重试"
			case "checker_unconfigured":
				s.checker = nil
				wantReason = "账号检测服务未配置"
			case "checker_paused":
				s.checker.paused.Store(true)
				wantReason = "账号检测服务已暂停"
			case "checker_stopped":
				s.checker.cancel()
				wantReason = "账号检测服务已停止"
			}
			response := httptest.NewRecorder()
			s.handleAction(response, httptest.NewRequest(http.MethodGet, "/api/account-recovery/settings", nil))
			var settings struct {
				Enabled       bool       `json:"auto_recovery_enabled"`
				Running       bool       `json:"running"`
				BlockedReason string     `json:"blocked_reason"`
				NextScanAt    *time.Time `json:"next_scan_at"`
				Scanning      bool       `json:"scanning"`
				LastError     string     `json:"last_error"`
			}
			if response.Code != http.StatusOK || json.Unmarshal(response.Body.Bytes(), &settings) != nil {
				t.Fatalf("settings response: %d %s", response.Code, response.Body.String())
			}
			wantEnabled := scenario != "disabled"
			wantRunning := wantEnabled && wantReason == ""
			if settings.Enabled != wantEnabled || settings.Running != wantRunning || settings.Scanning != wantRunning || (settings.NextScanAt != nil) != wantRunning {
				t.Fatalf("incorrect runtime status: %s", response.Body.String())
			}
			if (wantReason == "" && settings.BlockedReason != "") || !strings.Contains(settings.BlockedReason, wantReason) {
				t.Fatalf("blocked reason=%q want=%q", settings.BlockedReason, wantReason)
			}
			if scenario == "transient_error" && settings.LastError == "" {
				t.Fatal("transient scan error was hidden")
			}
		})
	}
}

func TestSub2RecoverySetStateIgnoresDeletedTask(t *testing.T) {
	f := newRecoveryFixture(t, false)
	if _, err := f.db.Exec(`DELETE FROM account_recovery_tasks WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	if f.service.setState(f.task.ID, store.RecoveryValidating, "stale worker") {
		t.Fatal("expected deleted task update to fail")
	}
	if f.service.paused.Load() {
		t.Fatal("deleted recovery task must not pause the worker")
	}
}

func assertCheckAction(t *testing.T, f *recoveryFixture, want string) {
	t.Helper()
	check, err := f.store.LatestFinishedAccountCheck(context.Background(), f.account.ID, "fixture")
	if err != nil || check == nil || check.RequiredAction != want {
		t.Fatalf("latest check=%+v err=%v want=%s", check, err, want)
	}
	if _, active, err := f.store.GetActiveAccountRecoveryTask(context.Background(), f.account.ID); err != nil || active {
		t.Fatalf("local check started a recovery: active=%v err=%v", active, err)
	}
}

func TestSub2MonitorEnableScansChecksAndReportsAction(t *testing.T) {
	f := newMonitorFixture(t)
	// Run only the independent monitor. Claiming checks/tasks below makes each
	// transition deterministic without substituting the actual scan/probe paths.
	f.service.wg.Add(1)
	go f.service.monitor()
	request := httptest.NewRequest(http.MethodPut, "/api/account-recovery/settings", strings.NewReader(`{"enabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.service.handleAction(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("settings: %d %s", response.Code, response.Body.String())
	}
	var work *store.AccountCheckWork
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		var err error
		work, err = f.store.ClaimAccountCheck(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if work != nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if work == nil {
		t.Fatal("enabling did not immediately scan and queue AUTH check")
	}
	f.service.checker.process(work)
	assertCheckAction(t, f, "reauthorize")
	if f.loginCalls != 0 || f.applyCalls != 0 || f.refreshCalls != 0 || len(f.events) != 0 {
		t.Fatalf("login=%d refresh=%d remote events=%v", f.loginCalls, f.refreshCalls, f.events)
	}
	get := httptest.NewRecorder()
	f.service.handleAction(get, httptest.NewRequest(http.MethodGet, "/api/account-recovery/settings", nil))
	for _, field := range []string{`"interval_seconds":60`, `"last_scan_at":"`, `"auto_check_batch_id":"` + work.Batch.ID} {
		if !strings.Contains(get.Body.String(), field) {
			t.Fatalf("missing %s in %s", field, get.Body.String())
		}
	}
}

func TestSub2MonitorSkipsDisabledHealthyAndPausedAccounts(t *testing.T) {
	for _, state := range []string{"switch_off", "active", "inactive"} {
		t.Run(state, func(t *testing.T) {
			f := newMonitorFixture(t)
			if state != "switch_off" {
				f.service.autoRecovery.Store(true)
				f.detailStatus = state
			}
			f.service.scanSub2Accounts()
			checks, err := f.store.ListAccountChecks(context.Background(), f.account.ID, 20)
			if err != nil || len(checks) != 0 {
				t.Fatalf("unexpected checks=%v err=%v", checks, err)
			}
			if f.loginCalls != 0 {
				t.Fatal("non-suspect account reauthorized")
			}
		})
	}
	for _, status := range []sub2AccountStatus{
		{Imported: true, Exists: false, Status: "error", Schedulable: new(bool)},
		{Imported: true, Exists: true, Unknown: true, Status: "error", Schedulable: new(bool)},
		{Imported: true, Exists: true, Stale: true, Status: "error", Schedulable: new(bool)},
	} {
		if sub2RecoverySuspect(status) {
			t.Fatalf("unsafe candidate %+v", status)
		}
	}
	active := true
	if !sub2RecoveryPoolCandidate(sub2AccountStatus{Imported: true, Exists: true, Status: "active", Schedulable: &active}) {
		t.Fatal("active schedulable account should be eligible only after an AUTH 401 result")
	}
	paused := false
	if sub2RecoveryPoolCandidate(sub2AccountStatus{Imported: true, Exists: true, Status: "active", Schedulable: &paused}) {
		t.Fatal("active manually paused account became a recovery candidate")
	}
}

func TestSub2MonitorPreservesCurrentActionsAcrossScansAndAPI(t *testing.T) {
	for _, tc := range []struct{ outcome, action string }{{"credential_revoked", "reauthorize"}, {"access_token_expired", "verify_remote_refresh"}} {
		for _, remote := range []string{"active", "error"} {
			t.Run(tc.outcome+"/"+remote, func(t *testing.T) {
				f := newMonitorFixture(t)
				f.service.autoRecovery.Store(true)
				f.detailStatus, f.schedule = remote, remote == "active"
				f.service.checker.runProbe = func(context.Context, string, string, string) probe.Result {
					return probe.Result{Outcome: tc.outcome, HTTPStatus: http.StatusUnauthorized, RequestAttempted: true}
				}
				_, err := f.service.queueRecoveryChecks(context.Background(), []int64{f.account.ID}, false)
				if err != nil {
					t.Fatal(err)
				}
				work, err := f.store.ClaimAccountCheck(context.Background())
				if err != nil || work == nil {
					t.Fatalf("check=%+v err=%v", work, err)
				}
				f.service.checker.process(work)
				f.service.scanSub2Accounts()
				assertCheckAction(t, f, tc.action)
				// More skipped attempts than fit on the history page must not
				// hide the latest finished observation from either endpoint.
				for i := 0; i < 21; i++ {
					batch, _, err := f.store.CreateAccountCheckBatch(context.Background(), store.AccountCheckInput{RequestKey: fmt.Sprintf("cooldown-%d", i), AccountIDs: []int64{f.account.ID}, Concurrency: 1, ProxyMode: "direct", DestinationKey: "fixture"})
					if err != nil {
						t.Fatal(err)
					}
					if _, err := f.store.ClaimAccountCheck(context.Background()); err != nil {
						t.Fatal(err)
					}
					got, _, err := f.store.GetAccountCheckBatch(context.Background(), batch.ID)
					if err != nil || got.Counts.Skipped != 1 {
						t.Fatalf("cooldown batch=%+v err=%v", got, err)
					}
				}
				for _, path := range []string{"/api/account-recovery", "/api/account-recovery/check"} {
					request := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"account_id":1}`))
					request.Header.Set("Content-Type", "application/json")
					response := httptest.NewRecorder()
					if path == "/api/account-recovery" {
						f.service.handleCollection(response, request)
					} else {
						f.service.handleAction(response, request)
					}
					if response.Code != http.StatusConflict || !strings.Contains(response.Body.String(), `"code":"`+tc.action+`"`) {
						t.Fatalf("%s response=%d %s", path, response.Code, response.Body.String())
					}
				}
				if len(f.events) != 0 || f.loginCalls != 0 || f.schedule != (remote == "active") {
					t.Fatalf("local check changed remote events=%v logins=%d", f.events, f.loginCalls)
				}
			})
		}
	}
}

func TestSub2ExplicitCheckReportsActionWithAutomaticSwitchOff(t *testing.T) {
	f := newMonitorFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/account-recovery/check", strings.NewReader(`{"account_id":1}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.service.handleAction(response, request)
	if response.Code != http.StatusAccepted {
		t.Fatalf("manual check: %d %s", response.Code, response.Body.String())
	}
	work, err := f.store.ClaimAccountCheck(context.Background())
	if err != nil || work == nil {
		t.Fatalf("manual work=%v err=%v", work, err)
	}
	f.service.checker.process(work)
	assertCheckAction(t, f, "reauthorize")
}

func TestSub2AutomaticCheckCanceledAfterSwitchOff(t *testing.T) {
	f := newMonitorFixture(t)
	f.service.autoRecovery.Store(true)
	f.service.scanSub2Accounts()
	f.service.autoRecovery.Store(false)
	work, err := f.store.ClaimAccountCheck(context.Background())
	if err != nil || work == nil {
		t.Fatalf("work=%v err=%v", work, err)
	}
	f.service.checker.runProbe = func(context.Context, string, string, string) probe.Result {
		t.Error("disabled automatic check issued network request")
		return probe.Result{}
	}
	f.service.checker.process(work)
	if _, active, err := f.store.GetActiveAccountRecoveryTask(context.Background(), f.account.ID); err != nil || active {
		t.Fatalf("disabled recovery active=%v err=%v", active, err)
	}
}

func TestSub2DisabledAccountCannotRecover(t *testing.T) {
	f := newRecoveryFixture(t, false)
	f.detailStatus = "inactive"
	f.service.process(f.task)
	if f.loginCalls != 0 || f.applyCalls != 0 || f.state(t).State != store.RecoveryFailed {
		t.Fatal("disabled Sub2 account was mutated")
	}
	if _, _, err := f.service.enqueue(context.Background(), f.account.ID); err == nil {
		t.Fatal("disabled account accepted")
	}
}

func TestSub2MonitorSettingsRestoredOverEnvironment(t *testing.T) {
	f := newMonitorFixture(t)
	t.Setenv("AUTH_AUTO_RECOVERY", "true")
	if err := f.store.SetAutoRecoveryEnabled(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	reopened := newSub2RecoveryService(f.store, nil, f.service.sub2)
	defer reopened.Stop()
	if reopened.autoRecovery.Load() {
		t.Fatal("saved disabled setting was replaced by environment default")
	}
}

func TestSub2MonitorLinksExternalAccountAndReportsAction(t *testing.T) {
	f := newMonitorFixture(t)
	if _, err := f.db.Exec(`DELETE FROM sub2_imports`); err != nil {
		t.Fatal(err)
	}
	f.externalAccount = true
	f.service.autoRecovery.Store(true)
	f.service.scanSub2Accounts()
	binding, err := f.store.GetSub2Import(context.Background(), "fixture", f.account.ID)
	if err != nil || binding.Sub2AccountID != 901 || !strings.HasPrefix(binding.OperationID, "linked-") {
		t.Fatalf("binding=%+v err=%v", binding, err)
	}
	work, err := f.store.ClaimAccountCheck(context.Background())
	if err != nil || work == nil {
		t.Fatalf("linked account not checked: work=%v err=%v", work, err)
	}
	f.service.checker.process(work)
	assertCheckAction(t, f, "reauthorize")
	for _, method := range f.methods {
		if method == "POST /admin/accounts" {
			t.Fatal("linked recovery created duplicate remote account")
		}
	}
}

func TestSub2LegacyCheckOnlyRecoveryStopsBeforeRemoteMutation(t *testing.T) {
	for _, retry := range []string{"", "relogin"} {
		t.Run("queued_"+retry, func(t *testing.T) {
			f := newMonitorFixture(t)
			ctx := context.Background()
			id := finishRecoveryCheck(t, f.store, f.db, f.account.ID, "legacy", "unauthorized", "", "", nil)
			task, _, err := f.store.CreateOrGetAccountRecoveryTask(ctx, f.account.ID, id, 901, true, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET retry_action=? WHERE id=?`, retry, task.ID); err != nil {
				t.Fatal(err)
			}
			task, err = f.store.ClaimAccountRecoveryTask(ctx, task.ID)
			if err != nil {
				t.Fatal(err)
			}
			f.service.process(task)
			got, err := f.store.GetAccountRecoveryTaskByID(ctx, task.ID)
			if err != nil || got.State != store.RecoveryFailed || got.ErrorCode != "reauthorize" || !got.RequiresAction || got.NextRetryAt != nil || f.loginCalls != 0 || len(f.events) != 0 {
				t.Fatalf("legacy check-only task=%+v err=%v logins=%d events=%v", got, err, f.loginCalls, f.events)
			}
		})
	}
}

func TestSub2RecoveryDoesNotReopenActiveManualPause(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.detailStatus, f.schedule = "active", false
	f.service.process(f.task)
	if got := f.state(t); got.State != store.RecoveryCanceled || f.loginCalls != 0 || f.applyCalls != 0 || f.schedule {
		t.Fatalf("manual pause was reopened: task=%+v login=%d apply=%d schedule=%v", got, f.loginCalls, f.applyCalls, f.schedule)
	}
}

func TestSub2RecoveryIntentPrefixIsReservedCaseInsensitively(t *testing.T) {
	f := newMonitorFixture(t)
	request := httptest.NewRequest(http.MethodPost, "/api/account-checks", strings.NewReader(`{"request_key":"ReCoVeRy-manual-forged","account_ids":[1],"concurrency":1,"proxy_mode":"direct"}`))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	f.service.checker.handleCollection(response, request)
	if response.Code != http.StatusBadRequest || !strings.Contains(response.Body.String(), "reserved_request_key") {
		t.Fatalf("reserved key accepted: %d %s", response.Code, response.Body.String())
	}
}

func TestSub2MonitorMissingLocalCredentialsRequiresRemoteRefreshCheck(t *testing.T) {
	f := newMonitorFixture(t)
	ctx := context.Background()
	if _, err := f.db.Exec(`DELETE FROM login_attempts`); err != nil {
		t.Fatal(err)
	}
	if _, err := f.db.Exec(`UPDATE accounts SET access_token_cipher=NULL, refresh_token_cipher=NULL, chatgpt_account_id='', status='failed'`); err != nil {
		t.Fatal(err)
	}
	f.service.autoRecovery.Store(true)
	f.service.checker.runProbe = func(context.Context, string, string, string) probe.Result {
		t.Error("missing token issued an upstream request")
		return probe.Result{}
	}
	f.service.scanSub2Accounts()
	work, err := f.store.ClaimAccountCheck(ctx)
	if err != nil || work == nil || work.PrecheckResult == nil || work.PrecheckResult.Outcome != "credential_missing" {
		t.Fatalf("missing credential work=%+v err=%v", work, err)
	}
	f.service.checker.process(work)
	assertCheckAction(t, f, "verify_remote_refresh")
	if f.loginCalls != 0 || len(f.events) != 0 {
		t.Fatalf("logins=%d remote events=%v", f.loginCalls, f.events)
	}
}

func TestSub2MonitorAfterCompletedTaskChecksNewFailure(t *testing.T) {
	for _, purpose := range []string{"recovery", "delivery"} {
		t.Run(purpose, func(t *testing.T) {
			ctx := context.Background()
			var f *recoveryFixture
			if purpose == "delivery" {
				var delivery *accountDeliveryService
				f, delivery, _ = newDeliveryFixture(t)
				delivery.runOnce()
				var err error
				f.task, err = f.store.GetLatestAccountRecoveryTask(ctx, f.account.ID)
				if err != nil || f.task.Purpose != "delivery" {
					t.Fatalf("delivery handoff=%+v err=%v", f.task, err)
				}
			} else {
				f = newRecoveryFixture(t, true)
			}
			f.service.process(f.task)
			if got := f.state(t); got.State != store.RecoveryCompleted {
				t.Fatalf("initial operation=%+v", got)
			}
			forceDueRecheck(t, f)
			if !f.service.processNextRecheck() || currentRecheck(t, f).Round != 2 {
				t.Fatal("first successful recheck did not schedule round two")
			}
			f.service.autoRecovery.Store(true)
			f.service.checker = newAccountCheckService(f.store, "", "")
			t.Cleanup(f.service.checker.Stop)
			checks, err := f.store.ListAccountChecks(ctx, f.account.ID, 20)
			if err != nil || len(checks) != 1 || checks[0].Freshness != "stale" {
				t.Fatalf("old 401 must be stale after login: checks=%+v err=%v", checks, err)
			}
			// A successful operation must not reuse the previous token's 401.
			f.service.scanSub2Accounts()
			if _, active, err := f.store.GetActiveAccountRecoveryTask(ctx, f.account.ID); err != nil || active {
				t.Fatalf("stale 401 queued another recovery: active=%v err=%v", active, err)
			}
			f.mu.Lock()
			f.detailStatus, f.detailError, f.schedule = "error", "Token revoked (401)", false
			f.mu.Unlock()
			f.service.scanSub2Accounts()
			work, err := f.store.ClaimAccountCheck(ctx)
			if err != nil || work == nil || work.Check.ID == checks[0].ID {
				t.Fatalf("new Sub2 failure after success did not queue a fresh AUTH check: work=%+v err=%v summary=%s", work, err, f.service.monitorState.Summary)
			}
			if _, active, err := f.store.GetActiveAccountRecoveryTask(ctx, f.account.ID); err != nil || active {
				t.Fatalf("Sub2 error alone reused stale 401: active=%v err=%v", active, err)
			}
		})
	}
}

func TestSub2MonitorAfterCompletedTaskReportsCurrent401(t *testing.T) {
	for _, path := range []string{"scan_error", "scan_active"} {
		t.Run(path, func(t *testing.T) {
			ctx := context.Background()
			f := newRecoveryFixture(t, true)
			f.service.process(f.task)
			completed := f.state(t)
			if completed.State != store.RecoveryCompleted {
				t.Fatalf("initial recovery=%+v", completed)
			}
			_, _, err := f.store.CreateAccountCheckBatch(ctx, store.AccountCheckInput{
				RequestKey: "after-completed", AccountIDs: []int64{f.account.ID}, Concurrency: 1, ProxyMode: "direct",
			})
			if err != nil {
				t.Fatal(err)
			}
			work, err := f.store.ClaimAccountCheck(ctx)
			if err != nil || work == nil {
				t.Fatalf("new credential check=%+v err=%v", work, err)
			}
			status := http.StatusUnauthorized
			if _, err := f.store.FinishAccountCheck(ctx, work.Check.ID, store.AccountCheckResult{Outcome: "unauthorized", HTTPStatus: &status}); err != nil {
				t.Fatal(err)
			}
			f.service.autoRecovery.Store(true)
			if path == "scan_error" {
				f.mu.Lock()
				f.detailStatus, f.detailError, f.schedule = "error", "Token revoked (401)", false
				f.mu.Unlock()
			}
			f.service.scanSub2Accounts()
			assertCheckAction(t, f, "reauthorize")
		})
	}
}
