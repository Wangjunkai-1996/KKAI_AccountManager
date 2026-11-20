package store

import (
	"context"
	"testing"
	"time"
)

func TestDiscardedHistory(t *testing.T) {
	for _, tc := range []struct {
		name, code, change string
		discard, pending   bool
	}{
		{"confirmed original binding", "invalid_username_or_password", "", true, true},
		{"email verification", "email_verification_required", "", true, true},
		{"old binding version", "password_rejected", "older-version", true, true},
		{"replaced operation", "invalid_username_or_password", "replaced", true, false},
		{"uncorroborated target", "password_rejected", "different-target", true, false},
		{"later login", "password_rejected", "later-success", false, false},
		{"changed credentials", "password_rejected", "edited", false, false},
		{"ambiguous code", "incorrect_code", "", false, false},
		{"temporary upstream error", "password_rejected", "overload", false, false},
		{"generic 401", "http_401", "", false, false},
		{"token endpoint rejection", "invalid_credentials", "token", false, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, _ := testStore(t)
			ctx := context.Background()
			const email = "legacy@example.test"
			newLifecycleAccount(t, s, email)
			finishLifecycleLogin(t, s, email, "plus")
			b := bindLifecycleAccount(t, s, email)
			if tc.change == "older-version" {
				finishLifecycleLogin(t, s, email, "plus")
			}
			version, err := s.GetAccountCredentialVersion(ctx, b.AccountID)
			if err != nil {
				t.Fatal(err)
			}
			exec := func(query string, args ...any) {
				t.Helper()
				if _, err := s.db.Exec(query, args...); err != nil {
					t.Fatal(err)
				}
			}
			start := time.Now().Unix() + 20
			exec(`UPDATE sub2_imports SET created_at=?,updated_at=? WHERE id=?`, start-10, start-8, b.ID)
			exec(`INSERT INTO account_recovery_tasks(account_id,source_credential_attempt_id,sub2_account_id,destination_key,state,created_at,updated_at) VALUES(?,?,42,'test','failed',?,?)`, b.AccountID, version, (start-5)*1000, (start+2)*1000)
			stage := "password"
			if tc.code == "email_verification_required" {
				stage = "email"
			}
			exec(`INSERT INTO login_attempts(account_id,status,stage,error_code,http_status,started_at,finished_at) VALUES(?,'failed',?,?,401,?,?)`, b.AccountID, stage, tc.code, start, start+1)
			exec(`UPDATE accounts SET status='failed',last_error_code=?,updated_at=? WHERE id=?`, tc.code, start+1, b.AccountID)
			switch tc.change {
			case "replaced":
				exec(`UPDATE sub2_imports SET operation_id='replacement',updated_at=? WHERE id=?`, start, b.ID)
			case "overload":
				exec(`UPDATE login_attempts SET http_status=503 WHERE status='failed'`)
			case "token":
				exec(`UPDATE login_attempts SET stage='token' WHERE status='failed'`)
			case "different-target":
				exec(`UPDATE account_recovery_tasks SET sub2_account_id=81`)
			case "later-success":
				exec(`INSERT INTO login_attempts(account_id,status,started_at,finished_at) VALUES(?,'success',?,?)`, b.AccountID, start+2, start+3)
			case "edited":
				exec(`INSERT INTO account_credential_repairs(account_id,source_version,state,updated_at) VALUES(?,?,'queued',?)`, b.AccountID, version, (start+1)*1000)
			}
			for i := 0; i < 2; i++ {
				if err := s.convergeDiscardedAttempts(ctx); err != nil {
					t.Fatal(err)
				}
			}
			a, err := s.GetAccountByID(ctx, b.AccountID)
			if err != nil || (a.Status == "discarded") != tc.discard || a.IsOfficialUnavailable() {
				t.Fatalf("status=%s policy=%s err=%v", a.Status, a.Sub2Policy, err)
			}
			var auditCount int
			if err := s.db.QueryRow(`SELECT COUNT(*) FROM account_lifecycle_events WHERE source='login_discarded' AND status='discarded' AND code=?`, tc.code).Scan(&auditCount); err != nil || auditCount != boolInt(tc.discard) {
				t.Fatalf("events=%d err=%v", auditCount, err)
			}
			if tc.pending {
				assertLifecycleBinding(t, s, b.ID, "cleanup_pending", version)
			} else {
				assertLifecycleBinding(t, s, b.ID, "imported", b.CredentialVersion)
			}
		})
	}
}
