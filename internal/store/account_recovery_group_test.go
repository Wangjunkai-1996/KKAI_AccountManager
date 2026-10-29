package store

import (
	"context"
	"testing"
)

func TestRecoveryGroupDecisionRequiresSnapshotAndQueuesCheckpoint(t *testing.T) {
	s, _ := testStore(t)
	ctx := context.Background()
	id := checkTestAccount(t, s, "group-decision")
	task, _, err := s.CreateOrGetAccountRecoveryTask(ctx, id, 0, 100+id, true)
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := &RecoveryDiagnostic{CurrentGroupIDs: []int64{13, 12}, TargetGroupIDs: []int64{44, 12}}
	task, err = s.RecordAccountRecoveryFailure(ctx, task.ID, RecoveryFailure{State: RecoveryUnknown, Stage: RecoveryEnablingSchedule, Code: "group_conflict", Diagnostic: diagnostic, RetryAction: "manual", ManualAction: "分组冲突"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE account_recovery_tasks SET result_credential_attempt_id=?,retry_action='manual' WHERE id=?`, task.SourceCredentialAttemptID, task.ID); err != nil {
		t.Fatal(err)
	}
	task, err = s.GetAccountRecoveryTaskByID(ctx, task.ID)
	if err != nil {
		t.Fatal(err)
	}
	current, target := []int64{12, 13}, []int64{12, 44}
	if _, err = s.BeginAccountRecoveryGroupDecision(ctx, task.ID, "use_target", current, target, task.UpdatedAt.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	queued, err := s.ConfirmAccountRecoveryGroupDecision(ctx, task.ID, "use_target", current, target, task.UpdatedAt.UnixMilli())
	if err != nil || queued.State != RecoveryQueued || queued.RequiresAction {
		t.Fatalf("queued task=%+v err=%v", queued, err)
	}
	decision, err := s.GetAccountRecoveryGroupDecision(ctx, task.ID)
	if err != nil || decision.State != "confirmed" || decision.Action != "use_target" {
		t.Fatalf("decision=%+v err=%v", decision, err)
	}
	if _, err = s.BeginAccountRecoveryGroupDecision(ctx, task.ID, "keep_current", current, target, task.UpdatedAt.UnixMilli()); err == nil {
		// The task is queued after confirmation; any later action must be rejected.
		t.Fatal("queued task accepted a second action")
	}
}
