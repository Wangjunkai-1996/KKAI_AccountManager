import importlib.util
import json
import sqlite3
import stat
import tempfile
import unittest
from contextlib import closing
from pathlib import Path


spec = importlib.util.spec_from_file_location("undo_policy", Path(__file__).with_name("auth-undo-policy-backfill.py"))
tool = importlib.util.module_from_spec(spec)
spec.loader.exec_module(tool)


class PolicyRepairTest(unittest.TestCase):
    def setUp(self):
        directory = tempfile.TemporaryDirectory()
        self.addCleanup(directory.cleanup)
        root = Path(directory.name)
        self.before, self.current, self.manifest, self.backup = [root / x for x in ("before", "current", "ids", "backup")]
        with closing(sqlite3.connect(self.before)) as db:
            db.executescript("""
                CREATE TABLE accounts(id INTEGER PRIMARY KEY, sub2_policy TEXT, status TEXT, plan_type TEXT, secret BLOB);
                CREATE TABLE store_meta(key TEXT PRIMARY KEY, value_cipher BLOB);
                INSERT INTO store_meta VALUES('encryption_check', X'010203');
            """)
            for name in tool.ACCOUNT_TABLES:
                db.execute(f"CREATE TABLE {name}(id INTEGER PRIMARY KEY, account_id INTEGER, state TEXT)")
            for name in tool.TASK_TABLES:
                db.execute(f"CREATE TABLE {name}(task_id INTEGER, state TEXT)")
            for account_id in range(1, 9):
                db.execute("INSERT INTO accounts VALUES(?,'unknown','active','plus',X'FF')", (account_id,))
            db.execute("INSERT INTO account_recovery_tasks VALUES(41,4,'completed')")
            db.execute("INSERT INTO sub2_imports VALUES(31,3,'imported')")
            db.commit()
            with closing(sqlite3.connect(self.current)) as current:
                db.backup(current)
        self.execute("PRAGMA journal_mode=WAL")
        self.execute("UPDATE accounts SET sub2_policy='eligible'")
        self.ids = {"extra_ids": [1, 2, 3, 4, 5, 6], "target_ids": [7], "before_sha256": tool.file_digest(self.before)}
        self.manifest.write_text(json.dumps(self.ids))

    def execute(self, statement):
        with closing(sqlite3.connect(self.current)) as db, db:
            return db.execute(statement).fetchall()

    def run_tool(self, apply=None):
        return tool.run(self.current, self.before, self.manifest, apply, self.backup if apply else None)

    def test_preview_backup_apply_and_replay(self):
        self.execute("INSERT INTO login_attempts VALUES(21,2,'success')")
        self.execute("UPDATE sub2_imports SET state='remote_missing' WHERE id=31")
        self.execute("INSERT INTO account_recovery_failures VALUES(41,'failed')")
        self.execute("UPDATE accounts SET secret=X'00' WHERE id=5")
        self.execute("UPDATE accounts SET sub2_policy='free_local' WHERE id=6")
        preview = self.run_tool()
        decisions = [i["decision"] for i in preview["items"]]
        self.assertEqual(decisions, ["restore_unknown", "changed_login_attempts", "changed_sub2_imports",
                                     "changed_account_recovery_failures", "account_changed", "policy_changed"])
        self.assertFalse(self.backup.exists())
        self.assertEqual(self.execute("SELECT COUNT(*) FROM sqlite_master WHERE name='auth_policy_repair_runs'"), [(0,)])
        applied = self.run_tool(preview["preview_sha256"])
        self.assertEqual((applied["writes"], applied["skipped"]), (1, 5))
        self.assertEqual(self.execute("SELECT id FROM accounts WHERE sub2_policy='unknown'"), [(1,)])
        self.assertEqual(stat.S_IMODE(self.backup.stat().st_mode), 0o600)
        self.assertEqual(tool.file_digest(self.backup), applied["backup_sha256"])
        with closing(sqlite3.connect(self.backup)) as copy:
            self.assertEqual(copy.execute("SELECT sub2_policy FROM accounts WHERE id=1").fetchone()[0], "eligible")
            self.assertEqual(copy.execute("PRAGMA integrity_check").fetchone(), ("ok",))
        self.assertEqual(self.run_tool(preview["preview_sha256"])["writes"], 0)
        self.assertEqual(json.loads(self.execute("SELECT receipt FROM auth_policy_repair_runs")[0][0]), applied)

    def test_drift_and_wrong_manifest(self):
        preview = self.run_tool()
        self.execute("INSERT INTO account_checks VALUES(51,5,'finished')")
        with self.assertRaisesRegex(ValueError, "fresh preview"):
            self.run_tool(preview["preview_sha256"])
        self.assertFalse(self.backup.exists())
        self.ids["target_ids"].append(1)
        self.manifest.write_text(json.dumps(self.ids))
        with self.assertRaisesRegex(ValueError, "overlap"):
            self.run_tool()

    def test_wrong_snapshot_or_different_store(self):
        self.manifest.write_text(json.dumps({**self.ids, "before_sha256": "0" * 64}))
        with self.assertRaisesRegex(ValueError, "hash differs"):
            self.run_tool()
        self.manifest.write_text(json.dumps(self.ids))
        self.execute("UPDATE store_meta SET value_cipher=X'ABCDEF'")
        with self.assertRaisesRegex(ValueError, "identities differ"):
            self.run_tool()

    def test_failed_write_rolls_back_and_preserves_backup(self):
        self.execute("""CREATE TRIGGER fail_policy BEFORE UPDATE ON accounts WHEN OLD.id=2
                        BEGIN SELECT RAISE(ABORT,'injected failure'); END""")
        preview = self.run_tool()
        with self.assertRaisesRegex(sqlite3.Error, "injected failure"):
            self.run_tool(preview["preview_sha256"])
        self.assertEqual(self.execute("SELECT COUNT(*) FROM accounts WHERE sub2_policy='eligible'"), [(8,)])
        self.assertEqual(self.execute("SELECT COUNT(*) FROM sqlite_master WHERE name='auth_policy_repair_runs'"), [(0,)])
        self.assertTrue(self.backup.is_file())
        with self.assertRaises(FileExistsError):
            self.run_tool(preview["preview_sha256"])
        self.execute("DROP TRIGGER fail_policy")
        self.backup = self.backup.with_name("retry-backup")
        self.assertEqual(self.run_tool(preview["preview_sha256"])["writes"], 6)


if __name__ == "__main__":
    unittest.main()
