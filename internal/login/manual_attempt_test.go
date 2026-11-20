package login

import (
	"context"
	"testing"
)

func TestManualSingleAttemptDisablesRetry(t *testing.T) {
	calls := 0
	ctx := WithSingleAttempt(context.Background())
	_, err := loginWithRetries(ctx, 3, func(context.Context) (*LoginResult, error) {
		calls++
		return nil, ErrAuthConnectionReset
	})
	if err == nil || calls != 1 {
		t.Fatalf("manual operation retried: calls=%d err=%v", calls, err)
	}
}
