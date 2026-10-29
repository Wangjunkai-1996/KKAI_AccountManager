package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"time"
)

// Only fixed classifications/numeric SQLite codes may reach logs; arbitrary
// database error strings can include credentials or SQL parameter values.
func storageErrorCode(err error) string {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	var sqliteError interface{ Code() int }
	if errors.As(err, &sqliteError) {
		return fmt.Sprintf("sqlite_%d", sqliteError.Code()&255)
	}
	return "storage_error"
}

// Call only for read operations or a claim that never started a transaction.
// Retrying a failed checkpoint/commit could replay an uncertain remote write.
func retryStorageRead(ctx context.Context, err error) bool {
	switch code := storageErrorCode(err); code {
	case "deadline_exceeded", "canceled", "sqlite_5", "sqlite_6":
		if ctx.Err() == nil {
			log.Printf("任务队列暂时繁忙：稍后自动重试 code=%s", code)
		}
		timer := time.NewTimer(2 * time.Second)
		defer timer.Stop()
		select {
		case <-ctx.Done():
		case <-timer.C:
		}
		return true
	default:
		return false
	}
}
