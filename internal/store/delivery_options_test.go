package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func TestDeliveryOptionsNamePrefixValidation(t *testing.T) {
	for _, tc := range []struct {
		name, input, want string
		invalid           bool
	}{
		{"legacy_default", "", "", false},
		{"blank", "  ", "", false},
		{"trim", "  测试池_A  ", "测试池_A", false},
		{"unicode_limit", strings.Repeat("池", 32), strings.Repeat("池", 32), false},
		{"too_long", strings.Repeat("池", 33), "", true},
		{"newline", "A\nB", "", true},
		{"control", "A\x00B", "", true},
		{"unicode_control", "A\u009fB", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			options := DefaultDeliveryOptions()
			options.NamePrefix = tc.input
			err := options.Validate()
			if (err != nil) != tc.invalid || err == nil && options.NamePrefix != tc.want {
				t.Fatalf("prefix=%q err=%v", options.NamePrefix, err)
			}
		})
	}
}

func TestQueueAccountDeliveryCorrectsUnsentRequiresAction(t *testing.T) {
	s, _ := testStore(t)
	if err := s.migrateAccountDeliveries(); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	account, attempt, err := s.BeginAttempt(ctx, Credentials{Email: "delivery-options@example.test", Password: "fixture-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer attempt.Release()
	if err := s.FinishAttempt(ctx, attempt.ID, true, &Result{AccessToken: "fixture-at", RefreshToken: "fixture-rt"}, nil); err != nil {
		t.Fatal(err)
	}
	old, err := s.QueueAccountDelivery(ctx, account.ID, "fixture", DeliveryOptions{GroupIDs: []int64{7}, Priority: 1, Concurrency: 3})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.UpdateAccountDelivery(ctx, old.ID, "requires_action", "分组已删除", "重新选择分组", nil, 0); err != nil {
		t.Fatal(err)
	}
	updated, err := s.QueueAccountDelivery(ctx, account.ID, "fixture", DeliveryOptions{GroupIDs: []int64{8}, Priority: 2, Concurrency: 4})
	if err != nil {
		t.Fatal(err)
	}
	if updated.ID != old.ID || updated.State != "queued" || len(updated.Options.GroupIDs) != 1 || updated.Options.GroupIDs[0] != 8 || updated.Options.Priority != 2 || updated.Options.Concurrency != 4 {
		t.Fatalf("unsent manual delivery was not corrected: old=%+v updated=%+v", old, updated)
	}
	due, err := s.ListDueAccountDeliveries(ctx, time.Now(), 10)
	if err != nil || len(due) != 1 || due[0].ID != old.ID {
		t.Fatalf("corrected delivery was not woken: due=%+v err=%v", due, err)
	}
}

func TestQueueAccountDeliveryRejectsBlockedExistingIntent(t *testing.T) {
	for _, scenario := range []string{"canceled", "handoff_requires_action", "handoff_manual", "handoff_canceled", "unknown_import"} {
		t.Run(scenario, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			delivery := deliveryTestLogin(t, s, "blocked-delivery@example.test")
			want := ErrAccountDeliveryRequiresAction
			switch scenario {
			case "canceled":
				want = ErrAccountDeliverySettled
				if _, err := s.UpdateAccountDelivery(ctx, delivery.ID, "canceled", "人工取消", "", nil, 0); err != nil {
					t.Fatal(err)
				}
			case "unknown_import":
				binding, _, err := s.CreateOrGetSub2Import(ctx, delivery.DestinationKey, delivery.AccountID, "existing-op", "existing-key", []byte(`{}`))
				if err != nil {
					t.Fatal(err)
				}
				if err := s.UpdateSub2Import(ctx, binding.ID, "unknown", false, 0, "结果未知"); err != nil {
					t.Fatal(err)
				}
				if _, err := s.UpdateAccountDelivery(ctx, delivery.ID, "requires_action", "需核对远端结果", "核对导入", nil, 0); err != nil {
					t.Fatal(err)
				}
			default:
				task, _, err := s.CreateAccountDeliveryRecoveryTask(ctx, delivery.ID, delivery.AccountID, 42, delivery.CredentialVersion, true)
				if err != nil {
					t.Fatal(err)
				}
				state := "verifying"
				if scenario == "handoff_requires_action" {
					state = "requires_action"
				}
				if _, err := s.UpdateAccountDelivery(ctx, delivery.ID, state, "", "", nil, task.ID); err != nil {
					t.Fatal(err)
				}
				if scenario == "handoff_canceled" {
					want = ErrAccountDeliverySettled
					if _, err := s.UpdateAccountRecoveryTask(ctx, task.ID, RecoveryCanceled, "人工暂停"); err != nil {
						t.Fatal(err)
					}
				} else if scenario == "handoff_manual" {
					if _, err := s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryUnknown, RetryAction: "manual", ManualAction: "核对分组"}); err != nil {
						t.Fatal(err)
					}
				}
			}
			before, err := s.GetAccountDelivery(ctx, delivery.ID)
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.QueueAccountDelivery(ctx, delivery.AccountID, delivery.DestinationKey, DeliveryOptions{GroupIDs: []int64{8}, Priority: 2, Concurrency: 4})
			if !errors.Is(err, want) {
				t.Fatalf("blocked delivery falsely accepted: err=%v want=%v", err, want)
			}
			after, err := s.GetAccountDelivery(ctx, delivery.ID)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("blocked intent mutated: before=%+v after=%+v err=%v", before, after, err)
			}
		})
	}
}
