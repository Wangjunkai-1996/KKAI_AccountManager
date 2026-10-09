package login

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"
)

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 10, 2, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		raw  string
		want time.Duration
	}{
		{raw: "5", want: 5 * time.Second},
		{raw: "120", want: 120 * time.Second},
		{raw: "9223372036854775807", want: time.Duration(1<<63 - 1)},
		{raw: "0", want: 0},
		{raw: "nonsense", want: 0},
		{raw: now.Add(7 * time.Second).Format(http.TimeFormat), want: 7 * time.Second},
		{raw: now.Add(-time.Second).Format(http.TimeFormat), want: 0},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			if got := parseRetryAfter(tt.raw, now); got != tt.want {
				t.Fatalf("parseRetryAfter(%q) = %s, want %s", tt.raw, got, tt.want)
			}
		})
	}
}

func TestRetryDelayIsBoundedAndHonorsRetryAfter(t *testing.T) {
	if got := retryDelay(1, 9*time.Second); got != 9*time.Second {
		t.Fatalf("retryDelay() with Retry-After = %s, want 9s", got)
	}
	if got := retryDelay(1, 2*time.Minute); got != 2*time.Minute {
		t.Fatalf("retryDelay() shortened Retry-After to %s, want 2m", got)
	}
	for attempt := 1; attempt <= 10; attempt++ {
		got := retryDelay(attempt, 0)
		minDelay := retryBaseDelay * 3 / 4
		if got < minDelay || got > retryMaxDelay {
			t.Fatalf("retryDelay(%d) = %s, outside [%s, %s]", attempt, got, minDelay, retryMaxDelay)
		}
	}
}

func TestRetryDelayExceedsDeadline(t *testing.T) {
	ctx, cancel := context.WithDeadline(context.Background(), time.Now().Add(100*time.Millisecond))
	defer cancel()
	if !retryDelayExceedsDeadline(ctx, time.Second) {
		t.Fatal("retryDelayExceedsDeadline() = false, want true")
	}

	longContext, longCancel := context.WithDeadline(context.Background(), time.Now().Add(time.Hour))
	defer longCancel()
	if retryDelayExceedsDeadline(longContext, time.Second) {
		t.Fatal("retryDelayExceedsDeadline() = true for ample remaining deadline")
	}
}

func TestSubtractElapsedRetryAfter(t *testing.T) {
	err := &authHTTPStatusError{Status: http.StatusForbidden, RetryAfter: 30 * time.Second}
	adjusted := subtractElapsedRetryAfter(err, 25*time.Second)
	var statusErr *authHTTPStatusError
	if !errors.As(adjusted, &statusErr) || statusErr.RetryAfter != 5*time.Second || statusErr.RetryAfterConsumed {
		t.Fatalf("adjusted Retry-After = %v, want 5s", adjusted)
	}
	adjusted = subtractElapsedRetryAfter(err, 31*time.Second)
	if !errors.As(adjusted, &statusErr) || statusErr.RetryAfter != 0 || !statusErr.RetryAfterConsumed {
		t.Fatalf("expired Retry-After = %v, want 0", adjusted)
	}
}
