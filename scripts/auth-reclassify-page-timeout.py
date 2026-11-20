#!/usr/bin/env python3
"""Reclassify one verified legacy AUTH page failure for normal recovery retry."""

import argparse
import json
import sqlite3
import time
from contextlib import closing
from pathlib import Path


def positive(value):
    number = int(value)
    if number <= 0:
        raise argparse.ArgumentTypeError("IDs must be positive")
    return number


def reclassify(db_path, account_id, task_id, sub2_id, destination, apply=False):
    if min(account_id, task_id, sub2_id) <= 0 or not destination:
        raise ValueError("positive IDs and destination are required")
    uri = f"{Path(db_path).resolve().as_uri()}?mode={'rw' if apply else 'ro'}"
    with closing(sqlite3.connect(uri, uri=True, timeout=5)) as db:
        db.row_factory = sqlite3.Row
        db.execute("BEGIN IMMEDIATE" if apply else "BEGIN")
        task = db.execute("""
            SELECT id, account_id, sub2_account_id, destination_key, state, purpose,
                   failure_stage, error_code, retry_action, next_retry_at,
                   retry_count, manual_action, source_credential_attempt_id,
                   result_credential_attempt_id, original_schedulable, created_at, updated_at
            FROM account_recovery_tasks WHERE id=? AND account_id=?
        """, (task_id, account_id)).fetchone()
        account = db.execute("""
            SELECT status, last_error_code, last_http_status, updated_at,
                   length(password_cipher) AS password_size
            FROM accounts WHERE id=?
        """, (account_id,)).fetchone()
        binding = db.execute("""
            SELECT sub2_account_id, state FROM sub2_imports
            WHERE destination_key=? AND account_id=?
        """, (destination, account_id)).fetchone()
        latest_task = db.execute("SELECT MAX(id) FROM account_recovery_tasks WHERE account_id=?", (account_id,)).fetchone()[0]
        attempt = db.execute("""
            SELECT id, status, error_code, http_status, started_at, finished_at
            FROM login_attempts WHERE account_id=? ORDER BY id DESC LIMIT 1
        """, (account_id,)).fetchone()
        latest_success = db.execute("""
            SELECT COALESCE(MAX(id), 0) FROM login_attempts
            WHERE account_id=? AND status='success'
        """, (account_id,)).fetchone()[0]
        eligible = all((
            task is not None, account is not None, binding is not None, attempt is not None,
        ))
        if eligible:
            eligible = all((
                task["id"] == latest_task,
                task["sub2_account_id"] == sub2_id,
                task["destination_key"] == destination,
                task["state"] == "failed",
                task["purpose"] == "recovery",
                task["failure_stage"] == "logging_in",
                task["error_code"] == "login_required",
                task["retry_action"] == "manual",
                task["next_retry_at"] is None,
                bool(task["manual_action"]),
                task["result_credential_attempt_id"] == 0,
                task["original_schedulable"] == 1,
                1 <= task["retry_count"] <= 2,
                task["source_credential_attempt_id"] > 0,
                task["source_credential_attempt_id"] == latest_success,
                account["status"] == "failed",
                account["last_error_code"] == "unexpected_auth_page",
                account["last_http_status"] == 0,
                account["password_size"] > 0,
                binding["state"] == "imported",
                binding["sub2_account_id"] == sub2_id,
                attempt["status"] == "failed",
                attempt["error_code"] == "unexpected_auth_page",
                attempt["http_status"] == 0,
                task["created_at"] // 1000 <= attempt["started_at"] <= attempt["finished_at"],
                attempt["finished_at"] <= task["updated_at"] // 1000,
                account["updated_at"] == attempt["finished_at"],
            ))
        if not eligible:
            raise ValueError("task no longer matches the verified legacy page failure")
        result = {"account_id": account_id, "task_id": task_id, "sub2_id": sub2_id,
                  "retry_count": task["retry_count"], "action": "dry_run"}
        if apply:
            now = int(time.time() * 1000)
            changed = db.execute("""
                UPDATE account_recovery_tasks
                SET error_code='auth_page_timeout', retry_action='relogin',
                    manual_action='', last_error='重新核对认证页面，稍后自动重试',
                    next_retry_at=?, updated_at=?
                WHERE id=? AND account_id=? AND updated_at=? AND state='failed'
                  AND error_code='login_required' AND retry_action='manual'
            """, (now, now, task_id, account_id, task["updated_at"])).rowcount
            if changed != 1:
                raise ValueError("task changed during reclassification")
            db.commit()
            result.update(action="reclassified", next_retry_at=now)
        return result


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--db", required=True, type=Path)
    parser.add_argument("--account-id", required=True, type=positive)
    parser.add_argument("--task-id", required=True, type=positive)
    parser.add_argument("--sub2-id", required=True, type=positive)
    parser.add_argument("--destination", required=True)
    parser.add_argument("--apply", action="store_true")
    args = parser.parse_args()
    try:
        print(json.dumps(reclassify(args.db, args.account_id, args.task_id,
                                    args.sub2_id, args.destination, args.apply)))
    except (sqlite3.Error, ValueError) as exc:
        parser.exit(1, f"reclassification refused: {type(exc).__name__}\n")


if __name__ == "__main__":
    main()
