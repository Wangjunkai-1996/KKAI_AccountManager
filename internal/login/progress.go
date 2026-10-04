package login

import (
	"context"
	"sync"
	"time"
)

// Progress is a safe, request-local update for the login flow.  It deliberately
// contains stage names and timing only; credentials, URLs, tokens, and upstream
// response bodies must never be placed in a progress message.
type Progress struct {
	Stage     string `json:"stage"`
	Message   string `json:"message"`
	ElapsedMS int64  `json:"elapsed_ms"`
	StageMS   int64  `json:"stage_ms"`
}

type progressCallbackKey struct{}
type progressTrackerKey struct{}

type progressTracker struct {
	mu         sync.Mutex
	callback   func(Progress)
	startedAt  time.Time
	stage      string
	message    string
	stageStart time.Time
}

// WithProgress attaches a progress callback to a login context.  A fresh
// tracker is created by LoginWithProxiesContext for each login request, so a
// context can safely be used as a parent for multiple independent requests.
func WithProgress(ctx context.Context, fn func(Progress)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if fn == nil {
		return ctx
	}
	return context.WithValue(ctx, progressCallbackKey{}, fn)
}

func progressCallback(ctx context.Context) func(Progress) {
	if ctx == nil {
		return nil
	}
	callback, _ := ctx.Value(progressCallbackKey{}).(func(Progress))
	return callback
}

// startProgress starts the request timer after the caller has applied its
// request deadline.  The timer is therefore shared by retries and any proxy
// fallback belonging to that request, while remaining isolated per request.
func startProgress(ctx context.Context) context.Context {
	callback := progressCallback(ctx)
	if callback == nil {
		return ctx
	}
	now := time.Now()
	tracker := &progressTracker{
		callback:  callback,
		startedAt: now,
	}
	return context.WithValue(ctx, progressTrackerKey{}, tracker)
}

func trackerFromContext(ctx context.Context) *progressTracker {
	if ctx == nil {
		return nil
	}
	tracker, _ := ctx.Value(progressTrackerKey{}).(*progressTracker)
	return tracker
}

// emitProgress emits the previous stage's final timing before a transition.
// StageMS always belongs to Stage; a new stage begins with zero milliseconds.
func emitProgress(ctx context.Context, stage, message string) {
	tracker := trackerFromContext(ctx)
	if tracker == nil || stage == "" {
		return
	}
	now := time.Now()
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.stage != stage {
		if tracker.stage != "" {
			tracker.report(now)
		}
		tracker.stage = stage
		tracker.stageStart = now
	}
	tracker.message = message
	tracker.report(now)
}

func finishProgress(ctx context.Context) {
	tracker := trackerFromContext(ctx)
	if tracker == nil {
		return
	}
	tracker.mu.Lock()
	defer tracker.mu.Unlock()
	if tracker.stage != "" && tracker.stage != "complete" {
		tracker.report(time.Now())
	}
}

// report is called with the tracker lock held, keeping stage events ordered.
func (tracker *progressTracker) report(now time.Time) {
	progress := Progress{
		Stage:     tracker.stage,
		Message:   tracker.message,
		ElapsedMS: elapsedMilliseconds(now.Sub(tracker.startedAt)),
		StageMS:   elapsedMilliseconds(now.Sub(tracker.stageStart)),
	}
	// A callback panic must not be able to break the login flow. The server-side
	// callback only queues an event and remains non-blocking.
	func() {
		defer func() { _ = recover() }()
		tracker.callback(progress)
	}()
}

func elapsedMilliseconds(duration time.Duration) int64 {
	if duration <= 0 {
		return 0
	}
	return duration.Milliseconds()
}
