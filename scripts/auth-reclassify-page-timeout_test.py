#!/usr/bin/env python3
"""Focused checks for the one-account legacy page-timeout reclassification."""

import importlib.util
import sqlite3
import sys
import tempfile
import unittest
from contextlib import closing
from pathlib import Path


script = Path(__file__).with_name("auth-reclassify-page-timeout.py")
sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("auth_page_reclassify", script)
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)
DESTINATION = "https://sub2.example.test/api/v1#mac-auth"
BASE = 1760000000


def fixture(path):
    with closing(sqlite3.connect(path)) as db, db:
        db.executescript("""
            CREATE TABLE accounts (id INTEGER PRIMARY KEY, status TEXT, last_error_code TEXT,
                last_http_status INTEGER, updated_at INTEGER, password_cipher BLOB);
            CREATE TABLE sub2_imports (destination_key TEXT, account_id INTEGER,
                sub2_account_id INTEGER, state TEXT);
            CREATE TABLE login_attempts (id INTEGER PRIMARY KEY, account_id INTEGER,
                status TEXT, error_code TEXT, http_status INTEGER,
                started_at INTEGER, finished_at INTEGER);
            CREATE TABLE account_recovery_tasks (id INTEGER PRIMARY KEY, account_id INTEGER,
                sub2_account_id INTEGER, destination_key TEXT, state TEXT, purpose TEXT,
                failure_stage TEXT, error_code TEXT, retry_action TEXT, next_retry_at INTEGER,
                retry_count INTEGER, manual_action TEXT, source_credential_attempt_id INTEGER,
                result_credential_attempt_id INTEGER, original_schedulable INTEGER,
                created_at INTEGER, updated_at INTEGER, last_error TEXT);
            CREATE TABLE account_recovery_failures (task_id INTEGER, error_code TEXT);
        """)
        db.execute("INSERT INTO accounts VALUES (842,'failed','unexpected_auth_page',0,?,X'01')", (BASE + 62,))
        db.execute("INSERT INTO sub2_imports VALUES (?,842,17133,'imported')", (DESTINATION,))
        db.execute("INSERT INTO login_attempts VALUES (842,842,'success',NULL,0,?,?)", (BASE - 100, BASE - 90))
        db.execute("INSERT INTO login_attempts VALUES (918,842,'failed','unexpected_auth_page',0,?,?)", (BASE, BASE + 62))
        db.execute("""
            INSERT INTO account_recovery_tasks VALUES
            (654,842,17133,?,'failed','recovery','logging_in','login_required',
             'manual',NULL,1,'manual action',842,0,1,?,?, 'previous safe error')
        """, (DESTINATION, BASE * 1000, (BASE + 62) * 1000 + 811))
        db.execute("INSERT INTO account_recovery_failures VALUES (654,'login_required')")


class ReclassifyTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.path = Path(self.directory.name) / "accounts.db"
        fixture(self.path)

    def run_tool(self, apply=False):
        return tool.reclassify(self.path, 842, 654, 17133, DESTINATION, apply)

    def task(self):
        with closing(sqlite3.connect(self.path)) as db:
            return db.execute("SELECT state,error_code,retry_action,next_retry_at,retry_count,manual_action FROM account_recovery_tasks WHERE id=654").fetchone()

    def test_dry_run_apply_and_idempotence(self):
        before = self.task()
        self.assertEqual(self.run_tool()["action"], "dry_run")
        self.assertEqual(self.task(), before)
        result = self.run_tool(apply=True)
        self.assertEqual(result["action"], "reclassified")
        state, code, action, deadline, count, manual = self.task()
        self.assertEqual((state, code, action, count, manual),
                         ("failed", "auth_page_timeout", "relogin", 1, ""))
        self.assertGreater(deadline, 0)
        with closing(sqlite3.connect(self.path)) as db:
            self.assertEqual(db.execute("SELECT error_code FROM account_recovery_failures WHERE task_id=654").fetchone()[0], "login_required")
        with self.assertRaises(ValueError):
            self.run_tool(apply=True)

    def test_mismatched_snapshot_is_rejected_without_write(self):
        changes = [
            ("newer task", "INSERT INTO account_recovery_tasks (id,account_id) VALUES (655,842)", ()),
            ("other destination", "UPDATE account_recovery_tasks SET destination_key='other'", ()),
            ("binding changed", "UPDATE sub2_imports SET sub2_account_id=17134", ()),
            ("binding unavailable", "UPDATE sub2_imports SET state='cleanup_pending'", ()),
            ("credential changed", "INSERT INTO login_attempts VALUES (919,842,'success',NULL,0,?,?)", (BASE + 63, BASE + 64)),
            ("latest failure changed", "UPDATE login_attempts SET error_code='invalid_password' WHERE id=918", ()),
            ("account changed", "UPDATE accounts SET updated_at=updated_at+1", ()),
            ("password cleared", "UPDATE accounts SET password_cipher=X''", ()),
            ("manual pause", "UPDATE account_recovery_tasks SET original_schedulable=0", ()),
            ("retry budget exhausted", "UPDATE account_recovery_tasks SET retry_count=25", ()),
        ]
        for label, sql, args in changes:
            with self.subTest(label=label):
                with tempfile.TemporaryDirectory() as directory:
                    path = Path(directory) / "accounts.db"
                    fixture(path)
                    with closing(sqlite3.connect(path)) as db, db:
                        db.execute(sql, args)
                    with self.assertRaises(ValueError):
                        tool.reclassify(path, 842, 654, 17133, DESTINATION, True)
                    with closing(sqlite3.connect(path)) as db:
                        action = db.execute("SELECT retry_action FROM account_recovery_tasks WHERE id=654").fetchone()[0]
                    self.assertEqual(action, "manual")


if __name__ == "__main__":
    unittest.main()
