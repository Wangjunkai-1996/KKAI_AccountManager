package login

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mxschmitt/playwright-go"
)

// These fixtures never navigate or contact an authentication service.
type waitingAuthPage struct{ playwright.Page }
type unusedAuthLocator interface{ playwright.Locator }
type waitingAuthLocator struct{ unusedAuthLocator }

func (waitingAuthPage) URL() string            { return "https://auth.openai.com/log-in" }
func (waitingAuthPage) Title() (string, error) { return "Login", nil }
func (waitingAuthPage) Locator(string, ...playwright.PageLocatorOptions) playwright.Locator {
	return waitingAuthLocator{}
}
func (l waitingAuthLocator) First() playwright.Locator { return l }
func (waitingAuthLocator) IsVisible(...playwright.LocatorIsVisibleOptions) (bool, error) {
	return false, nil
}
func (waitingAuthLocator) InnerText(...playwright.LocatorInnerTextOptions) (string, error) {
	return "", nil
}
func (waitingAuthLocator) TextContent(...playwright.LocatorTextContentOptions) (string, error) {
	return "Login", nil
}

func TestAuthPageWaitDeadlinesRetainReason(t *testing.T) {
	for _, wait := range []struct {
		name string
		run  func(context.Context, time.Duration) error
	}{
		{"selector", func(ctx context.Context, timeout time.Duration) error {
			return waitForAuthSelector(ctx, waitingAuthPage{}, "input", timeout, nil, nil)
		}},
		{"callback", func(ctx context.Context, timeout time.Duration) error {
			_, err := waitOAuthCallbackContext(ctx, waitingAuthPage{}, nil, nil, timeout, "", "")
			return err
		}},
	} {
		for _, mode := range []string{"already expired", "parent expires during wait", "page wait expires", "canceled"} {
			t.Run(wait.name+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				timeout := time.Second
				switch mode {
				case "already expired":
					cancel()
					ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
				case "parent expires during wait":
					cancel()
					ctx, cancel = context.WithTimeout(context.Background(), 5*time.Millisecond)
				case "page wait expires":
					timeout = 5 * time.Millisecond
				case "canceled":
					timer := time.AfterFunc(5*time.Millisecond, cancel)
					defer timer.Stop()
				}
				defer cancel()
				err := wait.run(ctx, timeout)
				if mode == "canceled" {
					if !errors.Is(err, context.Canceled) || errors.Is(err, ErrAuthPageTimeout) || retryableLoginError(err) {
						t.Fatalf("cancellation became a retryable page timeout: %v", err)
					}
					return
				}
				if !errors.Is(err, ErrUnexpectedAuthPage) || !errors.Is(err, ErrAuthPageTimeout) || !retryableLoginError(err) {
					t.Fatalf("page timeout lost retry classification: %v", err)
				}
				if mode != "page wait expires" && !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("parent deadline lost: %v", err)
				}
			})
		}
	}
}

func TestAuthPageWaitContextErrorPreservesCancellationAndOtherFailures(t *testing.T) {
	for _, err := range []error{nil, context.Canceled, errors.New("other failure"), errors.Join(context.Canceled, context.DeadlineExceeded)} {
		if got := authPageWaitContextError(err); got != err {
			t.Fatalf("non-deadline error changed: %v -> %v", err, got)
		}
	}
}
