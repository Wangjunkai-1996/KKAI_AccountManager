package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestAutoRecoverySettingsSurviveRestart(t *testing.T) {
	s, dir := testStore(t)
	ctx := context.Background()
	if _, saved, err := s.AutoRecoveryEnabled(ctx); err != nil || saved {
		t.Fatalf("default saved=%v err=%v", saved, err)
	}
	for _, enabled := range []bool{true, false} {
		if err := s.SetAutoRecoveryEnabled(ctx, enabled); err != nil {
			t.Fatal(err)
		}
		reopened, err := Open(filepath.Join(dir, "accounts.db"), filepath.Join(dir, "secret.key"))
		if err != nil {
			t.Fatal(err)
		}
		got, saved, err := reopened.AutoRecoveryEnabled(ctx)
		reopened.Close()
		if err != nil || !saved || got != enabled {
			t.Fatalf("enabled=%v saved=%v err=%v", got, saved, err)
		}
	}
}

func TestAutomaticRecoveryRetryLimit(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	account, err := s.UpsertCredentials(ctx, Credentials{Email: "retry@example.test", Password: "pw"})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if allowed, err := s.AutomaticRecoveryRetryAllowed(ctx, account.ID); err != nil || !allowed {
			t.Fatalf("try %d allowed=%v err=%v", i, allowed, err)
		}
		task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, account.ID, 0, 42, true)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryFailed, "login failed"); err != nil {
			t.Fatal(err)
		}
	}
	if allowed, err := s.AutomaticRecoveryRetryAllowed(ctx, account.ID); err != nil || allowed {
		t.Fatalf("retry limit allowed=%v err=%v", allowed, err)
	}
	if _, err := s.db.Exec(`UPDATE account_recovery_tasks SET created_at=?`, time.Now().Add(-25*time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	if allowed, err := s.AutomaticRecoveryRetryAllowed(ctx, account.ID); err != nil || !allowed {
		t.Fatalf("next day allowed=%v err=%v", allowed, err)
	}
}
