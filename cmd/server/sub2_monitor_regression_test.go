package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2MonitorRotatesBeyondBatchLimit(t *testing.T) {
	f := newMonitorFixture(t)
	ctx := context.Background()
	accounts := map[int64]store.Account{901: f.account}
	lastID := f.account.ID
	for i := 0; i < 100; i++ {
		account, err := f.store.UpsertCredentials(ctx, store.Credentials{Email: fmt.Sprintf("rotate-%03d@example.test", i), Password: "password"})
		if err != nil {
			t.Fatal(err)
		}
		attempt, err := f.store.StartAttempt(ctx, account.Email)
		if err != nil {
			t.Fatal(err)
		}
		if err := f.store.FinishAttempt(ctx, attempt.ID, true, &store.Result{AccessToken: "at", RefreshToken: "rt", ChatGPTAccountID: fmt.Sprintf("workspace-%d", i), PlanType: "plus"}, nil); err != nil {
			t.Fatal(err)
		}
		attempt.Release()
		account, err = f.store.GetAccountByID(ctx, account.ID)
		if err != nil {
			t.Fatal(err)
		}
		binding, _, err := f.store.CreateOrGetSub2Import(ctx, "fixture", account.ID, fmt.Sprintf("rotate-%d", i), fmt.Sprintf("rotate-%d", i), []byte(`{}`))
		if err != nil {
			t.Fatal(err)
		}
		remoteID := int64(1000 + i)
		if err := f.store.UpdateSub2Import(ctx, binding.ID, "imported", true, remoteID, ""); err != nil {
			t.Fatal(err)
		}
		accounts[remoteID], lastID = account, account.ID
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		id, _ := strconv.ParseInt(strings.TrimPrefix(r.URL.Path, "/admin/accounts/"), 10, 64)
		account, ok := accounts[id]
		if !ok {
			http.NotFound(w, r)
			return
		}
		writeRecoveryEnvelope(w, map[string]any{
			"id": id, "platform": "openai", "type": "oauth", "status": "error", "schedulable": false,
			"credentials": map[string]any{"email": account.Email, "chatgpt_account_id": account.ChatGPTAccountID},
			"extra":       map[string]any{"kkai_auth_import": map[string]any{"source_account_id": account.ID}},
		})
	}))
	defer server.Close()
	f.service.sub2.baseURL, f.service.sub2.client = server.URL, server.Client()
	f.service.autoRecovery.Store(true)
	f.service.scanSub2Accounts()
	firstID := f.service.monitorState.AutoCheckBatchID
	batch, first, err := f.store.GetAccountCheckBatch(ctx, firstID)
	if err != nil || batch.Counts.Total != 100 || len(first) != 100 {
		t.Fatalf("first scan batch=%+v items=%d err=%v monitor=%+v", batch, len(first), err, f.service.monitorState)
	}
	for _, item := range first {
		if item.AccountID != nil && *item.AccountID == lastID {
			t.Fatal("fixture failed to leave the highest account ID out of the first batch")
		}
	}
	for i := 0; i < 100; i++ {
		work, err := f.store.ClaimAccountCheck(ctx)
		if err != nil || work == nil {
			t.Fatalf("claim %d: %+v, %v", i, work, err)
		}
		if _, err := f.store.FinishAccountCheck(ctx, work.Check.ID, store.AccountCheckResult{Outcome: "forbidden"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := f.db.Exec(`UPDATE account_checks SET finished_at=? WHERE batch_id=?`, time.Now().Add(-time.Minute).UnixMilli(), firstID); err != nil {
		t.Fatal(err)
	}
	f.service.scanSub2Accounts()
	batch, second, err := f.store.GetAccountCheckBatch(ctx, f.service.monitorState.AutoCheckBatchID)
	if err != nil || batch.ID == firstID || batch.Counts.Total != 100 || len(second) != 100 {
		t.Fatalf("second scan batch=%+v items=%d err=%v monitor=%+v", batch, len(second), err, f.service.monitorState)
	}
	for _, item := range second {
		if item.AccountID != nil && *item.AccountID == lastID {
			return
		}
	}
	t.Fatal("the 101st account was starved by repeated inconclusive results")
}

func TestSub2MonitorCheckFailureKeepsSafeClassification(t *testing.T) {
	for _, tc := range []struct{ name, code, message string }{
		{"paused", "checker_paused", "检测服务已暂停"},
		{"stopped", "checker_stopped", "检测服务已停止"},
		{"missing", "checker_unconfigured", "检测服务未配置"},
		{"proxy", "invalid_proxy", "检测代理配置无效"},
		{"upstream", "upstream_proxy_unsupported", "检测暂不支持前置代理"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newMonitorFixture(t)
			switch tc.name {
			case "paused":
				f.service.checker.paused.Store(true)
			case "stopped":
				f.service.checker.cancel()
			case "missing":
				f.service.checker = nil
			case "proxy":
				f.service.checker.proxy = "not-a-proxy"
			case "upstream":
				f.service.checker.proxy, f.service.checker.upstreamProxy = "http://127.0.0.1:8080", "http://127.0.0.1:8081"
			}
			f.service.autoRecovery.Store(true)
			f.service.scanSub2Accounts()
			if !strings.Contains(f.service.monitorState.LastError, tc.message) {
				t.Fatalf("incorrect monitor error %q", f.service.monitorState.LastError)
			}
			request := httptest.NewRequest(http.MethodPost, "/api/account-recovery/check", strings.NewReader(fmt.Sprintf(`{"account_id":%d}`, f.account.ID)))
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			f.service.handleRecoveryCheck(response, request)
			if response.Code != http.StatusServiceUnavailable || !strings.Contains(response.Body.String(), `"code":"`+tc.code+`"`) || !strings.Contains(response.Body.String(), tc.message) {
				t.Fatalf("incorrect API error: %d %s", response.Code, response.Body.String())
			}
		})
	}
}
