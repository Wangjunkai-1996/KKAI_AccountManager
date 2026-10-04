package login

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestProgressTracksElapsedAndStageTime(t *testing.T) {
	var mu sync.Mutex
	var updates []Progress
	ctx := startProgress(WithProgress(context.Background(), func(update Progress) {
		mu.Lock()
		updates = append(updates, update)
		mu.Unlock()
	}))

	emitProgress(ctx, "browser", "正在启动浏览器")
	time.Sleep(5 * time.Millisecond)
	emitProgress(ctx, "browser", "浏览器已启动")
	time.Sleep(5 * time.Millisecond)
	emitProgress(ctx, "authorize", "正在访问授权页面")

	mu.Lock()
	got := append([]Progress(nil), updates...)
	mu.Unlock()
	if len(got) != 4 {
		t.Fatalf("got %d progress updates, want 4", len(got))
	}
	if got[0].Stage != "browser" || got[0].Message != "正在启动浏览器" {
		t.Fatalf("first update = %#v", got[0])
	}
	if got[1].ElapsedMS < got[0].ElapsedMS || got[1].StageMS < got[0].StageMS {
		t.Fatalf("same-stage timers went backwards: first=%#v second=%#v", got[0], got[1])
	}
	if got[2].ElapsedMS < got[1].ElapsedMS {
		t.Fatalf("elapsed timer went backwards: second=%#v third=%#v", got[1], got[2])
	}
	if got[2].Stage != "browser" || got[2].StageMS < got[1].StageMS {
		t.Fatalf("completed browser timing was not emitted: %#v", got[2])
	}
	if got[3].Stage != "authorize" || got[3].StageMS != 0 {
		t.Fatalf("new stage should start at zero, got %#v", got[3])
	}
}

func TestProgressRequestTimersAreIsolated(t *testing.T) {
	var mu sync.Mutex
	var first, second []Progress
	parent := context.Background()
	firstCtx := startProgress(WithProgress(parent, func(update Progress) {
		mu.Lock()
		first = append(first, update)
		mu.Unlock()
	}))
	secondCtx := startProgress(WithProgress(parent, func(update Progress) {
		mu.Lock()
		second = append(second, update)
		mu.Unlock()
	}))
	firstTracker := trackerFromContext(firstCtx)
	secondTracker := trackerFromContext(secondCtx)
	if firstTracker == secondTracker {
		t.Fatal("requests share a progress tracker")
	}
	firstTracker.startedAt = firstTracker.startedAt.Add(-time.Second)

	time.Sleep(5 * time.Millisecond)
	emitProgress(firstCtx, "browser", "第一请求")
	emitProgress(secondCtx, "browser", "第二请求")

	mu.Lock()
	defer mu.Unlock()
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("isolated callbacks received first=%d second=%d updates", len(first), len(second))
	}
	if first[0].Stage != "browser" || second[0].Stage != "browser" {
		t.Fatalf("unexpected stages: first=%#v second=%#v", first[0], second[0])
	}
	if first[0].ElapsedMS < 0 || second[0].ElapsedMS < 0 {
		t.Fatalf("negative elapsed timer: first=%#v second=%#v", first[0], second[0])
	}
	if first[0].ElapsedMS-second[0].ElapsedMS < 900 {
		t.Fatalf("request timers are not independent: first=%#v second=%#v", first[0], second[0])
	}
}

func TestProgressFinishesLastStage(t *testing.T) {
	var updates []Progress
	ctx := startProgress(WithProgress(context.Background(), func(update Progress) {
		updates = append(updates, update)
	}))
	emitProgress(ctx, "password", "正在输入密码")
	trackerFromContext(ctx).stageStart = time.Now().Add(-time.Second)
	finishProgress(ctx)
	if len(updates) != 2 || updates[1].Stage != "password" || updates[1].StageMS < 900 {
		t.Fatalf("last stage timing was not emitted: %#v", updates)
	}
}

func TestWithProgressPreservesContextAndNilCallback(t *testing.T) {
	key := struct{}{}
	base := context.WithValue(context.Background(), key, "value")
	if got := WithProgress(base, nil); got.Value(key) != "value" {
		t.Fatal("nil callback should preserve the original context")
	}
	ctx, cancel := context.WithCancel(WithProgress(base, func(Progress) {}))
	ctx = startProgress(ctx)
	if ctx.Value(key) != "value" {
		t.Fatal("request tracker did not preserve context values")
	}
	cancel()
	if err := ctx.Err(); err != context.Canceled {
		t.Fatalf("context cancellation was not propagated: %v", err)
	}
}
