package store

import "time"

// Older writers selected the experimental protocol solely from a frozen
// binding. They did not persist whether the login requested delivery.
func (s *Store) retireLifecycleDeliveries() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const legacy = "original_binding_id > 0 AND bound_handoff_state = ''"
	var found bool
	if err := tx.QueryRow("SELECT EXISTS(SELECT 1 FROM account_deliveries WHERE " + legacy + ")").Scan(&found); err != nil || !found {
		return err
	}
	// A recovery can commit before the delivery records its ID. Descendants
	// have ordinary recovery rows and are identified by their recheck links.
	if _, err := tx.Exec(`CREATE TEMP TABLE retiring_recoveries AS
 WITH RECURSIVE related(id) AS (
   SELECT t.id FROM account_recovery_tasks t JOIN account_deliveries d ON d.id=t.delivery_id
   WHERE t.purpose='delivery' AND t.account_id=d.account_id AND ` + legacy + `
   UNION
   SELECT r.recovery_task_id FROM account_recovery_rechecks r JOIN related ON related.id=r.task_id
   WHERE r.recovery_task_id>0
 ) SELECT id FROM related`); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE account_deliveries AS d SET recovery_task_id=t.id
 FROM account_recovery_tasks t WHERE t.delivery_id=d.id AND t.id IN retiring_recoveries
 AND d.recovery_task_id=0 AND ` + legacy); err != nil {
		return err
	}
	const stopped = "旧交付协议已停用，任务已停止"
	const uncertain = "旧交付协议已停用，远端写入结果未确认，请只读核对 Sub2"
	now := time.Now().UnixMilli()
	if _, err := tx.Exec(`UPDATE account_recovery_tasks SET
 last_error=CASE WHEN state='queued' AND retry_count=0 AND failure_stage='' THEN ? ELSE ? END,
 state='canceled',retry_action='',manual_action='',next_retry_at=NULL,updated_at=?
 WHERE id IN retiring_recoveries AND state NOT IN ('completed','canceled')`, stopped, uncertain, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE account_recovery_rechecks SET state='canceled',last_error=?,manual_action='',next_check_at=NULL,updated_at=?
 WHERE task_id IN retiring_recoveries AND (state IN ('pending','checking','requires_action') OR
 (state='recovery_queued' AND recovery_task_id IN (SELECT id FROM account_recovery_tasks WHERE state='canceled')))`, stopped, now); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE account_credential_repairs SET state='canceled',last_error=?,manual_action='',next_retry_at=NULL,updated_at=?
 WHERE state='recovery_queued' AND recovery_task_id IN retiring_recoveries
 AND recovery_task_id IN (SELECT id FROM account_recovery_tasks WHERE state='canceled')`, stopped, now); err != nil {
		return err
	}
	// Completed handoffs carry their outcome in the recovery record. Preserve
	// that evidence even if an earlier crash left the delivery in "checking".
	if _, err := tx.Exec(`UPDATE account_deliveries AS d SET
 state=COALESCE((SELECT state FROM account_recovery_tasks WHERE id=d.recovery_task_id),'canceled'),
 last_error=COALESCE((SELECT last_error FROM account_recovery_tasks WHERE id=d.recovery_task_id),?),
 manual_action='',next_retry_at=NULL,updated_at=?
 WHERE state NOT IN ('completed','canceled') AND `+legacy, stopped, now); err != nil {
		return err
	}
	if _, err := tx.Exec("UPDATE account_deliveries SET bound_handoff_state='retired' WHERE " + legacy); err != nil {
		return err
	}
	if _, err := tx.Exec("DROP TABLE retiring_recoveries"); err != nil {
		return err
	}
	return tx.Commit()
}
