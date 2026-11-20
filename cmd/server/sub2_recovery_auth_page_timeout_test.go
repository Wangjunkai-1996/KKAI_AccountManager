package main

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/store"
)

func TestRecoveryAuthPageTimeoutPreservesDeadlineBudget(t *testing.T) {
	err := errors.Join(context.DeadlineExceeded, login.ErrUnexpectedAuthPage, login.ErrAuthPageTimeout)
	failure := classifyRecoveryLoginError(err)
	if failure.Code != "auth_page_timeout" || failure.RequiresAction {
		t.Fatalf("overall login deadline lost page timeout classification: %+v", failure)
	}
	f := newRecoveryFixture(t, true)
	if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET retry_count=2 WHERE id=?`, f.task.ID); err != nil {
		t.Fatal(err)
	}
	f.service.recordRecoveryFailure(f.task, store.RecoveryLoggingIn, failure)
	got := f.state(t)
	if got.ErrorCode != "auth_page_timeout_exhausted" || !got.RequiresAction || got.RetryAction != "manual" || got.NextRetryAt != nil || got.RetryCount != 3 {
		t.Fatalf("overall login deadline escaped page timeout budget: %+v", got)
	}
	canceled := classifyRecoveryLoginError(errors.Join(context.Canceled, login.ErrUnexpectedAuthPage, login.ErrAuthPageTimeout))
	if canceled.Code != "interrupted" {
		t.Fatalf("cancellation was reclassified as a page timeout: %+v", canceled)
	}
}

func TestRecoveryAuthPageTimeoutStopsAfterThreeFailedRounds(t *testing.T) {
	for _, priorFailures := range []int{0, 1, 2, 25} {
		t.Run(strconv.Itoa(priorFailures), func(t *testing.T) {
			f := newRecoveryFixture(t, true)
			if _, err := f.db.Exec(`UPDATE account_recovery_tasks SET retry_count=? WHERE id=?`, priorFailures, f.task.ID); err != nil {
				t.Fatal(err)
			}
			started := time.Now()
			f.service.recordRecoveryFailure(f.task, store.RecoveryLoggingIn, &recoveryOperationError{Code: "auth_page_timeout"})
			got := f.state(t)
			if got.RetryCount != priorFailures+1 || got.FailureStage != store.RecoveryLoggingIn {
				t.Fatalf("failed round was not recorded: %+v", got)
			}
			if priorFailures < recoveryAuthPageTimeoutMaxFailures-1 {
				if got.State != store.RecoveryUnknown || got.RetryAction != "relogin" || got.RequiresAction || got.NextRetryAt == nil || got.ManualAction != "" || got.ErrorCode != "auth_page_timeout" {
					t.Fatalf("transient page timeout was not scheduled: %+v", got)
				}
				delay := recoveryRetryDelay(&recoveryOperationError{Code: "auth_page_timeout"}, priorFailures)
				if got.NextRetryAt.Before(started.Add(delay-time.Second)) || got.NextRetryAt.After(time.Now().Add(delay)) {
					t.Fatalf("page timeout retry deadline = %s, want about %s", got.NextRetryAt, delay)
				}
			} else if got.State != store.RecoveryFailed || got.RetryAction != "manual" || !got.RequiresAction || got.NextRetryAt != nil || got.ManualAction == "" || got.ErrorCode != "auth_page_timeout_exhausted" {
				t.Fatalf("exhausted page timeout kept retrying: %+v", got)
			}
		})
	}
}
