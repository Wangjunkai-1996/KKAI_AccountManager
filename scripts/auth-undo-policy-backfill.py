#!/usr/bin/env python3
"""Review and undo extra eligible grants from the 20261123T110011Z AUTH release.

Use an offline current database and the standalone SQLite backup taken immediately
before that release. The JSON manifest names the SHA-256 of that backup and the
reviewed AUTH IDs, e.g. {"before_sha256":"<64 hex digits>","extra_ids":[101,102],
"target_ids":[201]}. target_ids lists the original recovery cohort to exclude.

Preview: python3 scripts/auth-undo-policy-backfill.py --db CURRENT --before SNAPSHOT --manifest IDS.json
Apply:   repeat with --apply PREVIEW_SHA256 --backup NEW_FILE
Changed inputs require a fresh preview. A completed run returns its stored receipt.
After interruption, inspect the receipt or retry with a new backup filename.
"""

import argparse
import hashlib
import json
import os
import sqlite3
import time
from contextlib import closing
from pathlib import Path


RUN = "undo-extra-policy-grants-20261123T110011Z-v1"
# This frozen list reproduces the historical SQL selecting the incident cohort.
PAID = {"plus", "team", "business", "self_serve_business", "self_serve_business_prolite",
        "business_pro", "business_prolite", "pro", "enterprise"}
ACCOUNT_TABLES = ("login_attempts", "sub2_imports", "account_recovery_tasks",
                  "account_checks", "account_credential_repairs", "account_deliveries",
                  "account_lifecycle_events", "lifecycle_request_bindings")
TASK_TABLES = ("account_recovery_failures", "account_recovery_rechecks", "account_recovery_group_decisions")


def digest(value):
    raw = json.dumps(value, sort_keys=True, separators=(",", ":")).encode()
    return hashlib.sha256(raw).hexdigest()


def file_digest(path):
    value = hashlib.sha256()
    with path.open("rb") as source:
        for block in iter(lambda: source.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def inputs(manifest, before):
    data = json.loads(manifest.read_text(encoding="utf-8"))
    if not isinstance(data, dict) or set(data) != {"before_sha256", "extra_ids", "target_ids"}:
        raise ValueError("manifest requires before_sha256, extra_ids and target_ids")
    for key in ("extra_ids", "target_ids"):
        ids = data[key]
        if not isinstance(ids, list) or not ids or any(type(i) is not int or not 0 < i < 2**63 for i in ids):
            raise ValueError(f"{key} must contain positive SQLite IDs")
        if len(set(ids)) != len(ids):
            raise ValueError(f"duplicate IDs in {key}")
        data[key] = sorted(ids)
    if set(data["extra_ids"]).intersection(data["target_ids"]):
        raise ValueError("extra and target IDs overlap")
    for suffix in ("-wal", "-journal"):
        sidecar = Path(str(before) + suffix)
        if sidecar.exists() and sidecar.stat().st_size:
            raise ValueError("before must be a standalone SQLite backup")
    if data["before_sha256"] != file_digest(before):
        raise ValueError("before snapshot hash differs from manifest")
    return data


def open_db(path, mode):
    connection = sqlite3.connect(path.resolve().as_uri() + "?" + mode, uri=True, timeout=5)
    connection.row_factory = sqlite3.Row
    return connection


def rows_match(old, current, table, account_id, task_scoped):
    # Both queries use a fixed internal table name. Row equality checks every
    # column, including encrypted payloads, locally without logging row data.
    where = ("task_id IN (SELECT id FROM account_recovery_tasks WHERE account_id=?)"
             if task_scoped else "account_id=?")
    query = f"SELECT * FROM {table} WHERE {where} ORDER BY rowid"
    return old.execute(query, (account_id,)).fetchall() == current.execute(query, (account_id,)).fetchall()


def classify(old, current, account_id):
    query = "SELECT * FROM accounts WHERE id=?"
    original = old.execute(query, (account_id,)).fetchone()
    latest = current.execute(query, (account_id,)).fetchone()
    if original is None or latest is None:
        return "account_missing"
    if (original["sub2_policy"] != "unknown" or original["status"] != "active"
            or (original["plan_type"] or "").strip(" ").lower() not in PAID):
        return "outside_original_backfill"
    if latest["sub2_policy"] != "eligible":
        return "policy_changed"
    comparable = dict(latest)
    comparable["sub2_policy"] = "unknown"
    if dict(original) != comparable:
        return "account_changed"
    for table in ACCOUNT_TABLES + TASK_TABLES:
        if not rows_match(old, current, table, account_id, table in TASK_TABLES):
            return f"changed_{table}"
    return "restore_unknown"


def backup_current(db_path, output):
    # Keep the caller's BEGIN IMMEDIATE on its own connection. The second
    # connection can read the exact pre-write snapshot through SQLite's backup API.
    for suffix in ("-wal", "-shm", "-journal"):
        if Path(str(output) + suffix).is_symlink() or Path(str(output) + suffix).exists():
            raise ValueError("backup name has pre-existing SQLite sidecars")
    descriptor = os.open(output, os.O_CREAT | os.O_EXCL | os.O_WRONLY, 0o600)
    os.close(descriptor)
    try:
        with closing(open_db(db_path, "mode=ro")) as source, closing(open_db(output, "mode=rw")) as backup:
            source.backup(backup)
            backup.execute("PRAGMA journal_mode=DELETE")
            if backup.execute("PRAGMA integrity_check").fetchone()[0] != "ok":
                raise ValueError("new SQLite backup failed integrity_check")
        with output.open("rb") as file:
            os.fsync(file.fileno())
        parent = os.open(output.parent, os.O_RDONLY)
        try:
            os.fsync(parent)
        finally:
            os.close(parent)
        return file_digest(output)
    except BaseException:
        output.unlink(missing_ok=True)
        raise


def run(db_path, before, manifest, apply=None, backup=None):
    if bool(apply) != bool(backup):
        raise ValueError("apply requires both the preview digest and a new backup path")
    if before.samefile(db_path):
        raise ValueError("current and before must be separate databases")
    if backup:
        reserved = {str(path.resolve()) + suffix for path in (db_path, before)
                    for suffix in ("", "-wal", "-shm", "-journal")}
        if str(backup.resolve()) in reserved:
            raise ValueError("backup path overlaps an input database or its SQLite sidecars")
    data = inputs(manifest, before)
    input_hash = digest(data)
    with closing(open_db(before, "mode=ro&immutable=1")) as old, closing(open_db(db_path, "mode=rw" if apply else "mode=ro")) as db:
        db.execute("BEGIN IMMEDIATE" if apply else "BEGIN")
        key_query = "SELECT value_cipher FROM store_meta WHERE key='encryption_check'"
        original_key = old.execute(key_query).fetchone()
        if original_key is None or original_key != db.execute(key_query).fetchone():
            raise ValueError("current and before store identities differ")
        if db.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name='auth_policy_repair_runs'").fetchone():
            stored = db.execute("SELECT receipt FROM auth_policy_repair_runs WHERE run_id=?", (RUN,)).fetchone()
            if stored:
                receipt = json.loads(stored["receipt"])
                if receipt["manifest_sha256"] != input_hash or (apply and receipt["preview_sha256"] != apply):
                    raise ValueError("a different manifest or preview already completed this repair")
                return {**receipt, "state": "already_applied", "writes": 0}
        items = [{"account_id": i, "decision": classify(old, db, i)} for i in data["extra_ids"]]
        restore = [item["account_id"] for item in items if item["decision"] == "restore_unknown"]
        plan = {"run_id": RUN, "manifest_sha256": input_hash, "items": items,
                "selected": len(restore), "skipped": len(items) - len(restore),
                "transition": {"before": "eligible", "after": "unknown"}}
        preview_hash = digest(plan)
        if apply is None:
            return {**plan, "preview_sha256": preview_hash, "state": "preview", "writes": 0}
        if apply != preview_hash:
            raise ValueError("preview differs from current checks; generate and review a fresh preview")
        backup_hash = backup_current(db_path, backup)
        for account_id in restore:
            changed = db.execute("UPDATE accounts SET sub2_policy='unknown' WHERE id=? AND sub2_policy='eligible'",
                                 (account_id,)).rowcount
            if changed != 1:
                raise ValueError("policy changed before write")
        receipt = {**plan, "preview_sha256": preview_hash, "backup_sha256": backup_hash,
                   "backup_path": str(backup.resolve()), "completed_at": int(time.time()),
                   "state": "applied", "writes": len(restore)}
        db.execute("CREATE TABLE IF NOT EXISTS auth_policy_repair_runs (run_id TEXT PRIMARY KEY, receipt TEXT NOT NULL)")
        db.execute("INSERT INTO auth_policy_repair_runs VALUES (?,?)", (RUN, json.dumps(receipt)))
        db.commit()
        return receipt


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    parser.add_argument("--db", type=Path, required=True)
    parser.add_argument("--before", type=Path, required=True)
    parser.add_argument("--manifest", type=Path, required=True)
    parser.add_argument("--apply", metavar="PREVIEW_SHA256")
    parser.add_argument("--backup", type=Path)
    args = parser.parse_args()
    try:
        print(json.dumps(run(args.db, args.before, args.manifest, args.apply, args.backup), indent=2))
    except (ValueError, sqlite3.Error, OSError) as error:
        parser.exit(1, f"repair stopped: {error}\n")
