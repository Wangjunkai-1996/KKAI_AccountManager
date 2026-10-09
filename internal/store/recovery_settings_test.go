package store

import (
	"context"
	"path/filepath"
	"testing"
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
