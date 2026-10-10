package main

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestSub2RecoveryBrowserPermissionRetriesAfterRuntimeRepair(t *testing.T) {
	f := newRecoveryFixture(t, true)
	f.service.autoRecovery.Store(true)
	workingLogin := f.service.loginWithProxies
	f.service.loginWithProxies = func(context.Context, string, string, string, string, string) (*login.LoginResult, error) {
		f.loginCalls++
		return nil, fmt.Errorf("playwright 启动失败: %w", &os.PathError{Op: "fork/exec", Path: "/private/runtime/node", Err: fs.ErrPermission})
	}
	started := time.Now()
	f.service.process(f.task)
	failed := f.state(t)
	if failed.ErrorCode != login.LoginErrorBrowserRuntimePermission || failed.RetryAction != "relogin" || failed.RequiresAction || failed.ManualAction != "" || failed.NextRetryAt == nil || f.schedule || f.loginCalls != 1 {
		t.Fatalf("runtime permission must pause scheduling and persist a retry: %+v login=%d", failed, f.loginCalls)
	}
	if failed.NextRetryAt.Before(started.Add(5*time.Minute)) || failed.NextRetryAt.After(time.Now().Add(5*time.Minute)) || strings.Contains(failed.LastError, "/private/") || !strings.Contains(failed.LastError, "运行环境权限不足") {
		t.Fatalf("unsafe diagnosis or incorrect runtime retry delay: %+v", failed)
	}
	f.service.loginWithProxies = workingLogin
	recoveryRetryDue(t, f)
	f.service.retryDueRecoveries()
	runQueuedRecovery(t, f)
	if got := f.state(t); got.State != store.RecoveryCompleted || !f.schedule || f.loginCalls != 2 {
		t.Fatalf("runtime repair did not resume account recovery: %+v login=%d", got, f.loginCalls)
	}
}
