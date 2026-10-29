#!/usr/bin/env python3
"""Stage an isolated AUTH release on sys1.

This script only creates and validates a release/candidate directory.  It does
not stop or restart the production service and never contacts Sub2.
"""
from __future__ import annotations

import argparse
import hashlib
import json
import os
import pwd
import re
import shutil
import signal
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path


ROOT = Path("/opt/openai-login")
DB = Path("/var/lib/openai-login/data/accounts.db")
KEY = Path("/var/lib/openai-login/data/accounts.key")
SERVICE = "openai-login"


def call(argv, *, timeout=120, check=True, capture=False, env=None):
    print("+", " ".join(map(str, argv)), flush=True)
    return subprocess.run(argv, timeout=timeout, check=check, text=True,
                          capture_output=capture, env=env)


def digest(path: Path) -> str:
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def get_json(url: str, port: int, *, timeout=5):
    with urllib.request.urlopen(f"http://127.0.0.1:{port}{url}", timeout=timeout) as r:
        return r.status, json.loads(r.read())


def stop_group(proc):
    if proc is None or proc.poll() is not None:
        return
    try:
        os.killpg(proc.pid, signal.SIGTERM)
        proc.wait(15)
    except (ProcessLookupError, subprocess.TimeoutExpired):
        try:
            os.killpg(proc.pid, signal.SIGKILL)
            proc.wait(5)
        except ProcessLookupError:
            pass


def wait_ready(proc, port: int, timeout=75):
    deadline = time.monotonic() + timeout
    last = "connection refused"
    while time.monotonic() < deadline:
        if proc.poll() is not None:
            raise RuntimeError(f"candidate exited with code {proc.returncode}: {last}")
        try:
            status, ready = get_json("/ready", port)
            if status == 200 and ready.get("status") == "ready" and ready.get("database_ready") is True:
                return ready
            last = f"ready={status} {ready}"
        except (OSError, ValueError, urllib.error.HTTPError) as exc:
            last = str(exc)
        time.sleep(1)
    raise RuntimeError(f"candidate did not become ready within {timeout}s: {last}")


def process_for_release(release: Path):
    expected = str(release / "openai-login-web")
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            exe = os.path.realpath(entry / "exe")
            if exe != expected:
                continue
            env = {}
            for item in (entry / "environ").read_bytes().split(b"\0"):
                if b"=" in item:
                    k, v = item.split(b"=", 1)
                    env[k.decode(errors="replace")] = v.decode(errors="replace")
            return int(entry.name), env
        except (OSError, ValueError):
            continue
    raise RuntimeError(f"no process with /proc/<pid>/exe={expected}")


def copy_tree_permissions(path: Path):
    for item in path.rglob("*"):
        mode = item.stat().st_mode
        if item.is_dir():
            os.chmod(item, 0o755)
        elif item.name in {"openai-login-web", "kkai-auth-login-smoke", "node", "verify-runtime.sh"}:
            os.chmod(item, 0o755)
        else:
            os.chmod(item, 0o644 if not mode & 0o111 else 0o755)


def runtime_check(release: Path, smoke: Path, env: dict[str, str], out: Path):
    required = {"DISPLAY": env.get("DISPLAY", ""), "XAUTHORITY": env.get("XAUTHORITY", "")}
    if not all(required.values()):
        raise RuntimeError("candidate did not expose DISPLAY and XAUTHORITY")
    runtime_env = {
        "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "HOME": "/var/lib/openai-login",
        "DISPLAY": required["DISPLAY"],
        "XAUTHORITY": required["XAUTHORITY"],
        "PLAYWRIGHT_NODEJS_PATH": str(release / "node"),
        "PLAYWRIGHT_DRIVER_PATH": str(release / "driver"),
        "OPENAI_LOGIN_STARTUP_SMOKE": "1",
    }
    cmd = ["runuser", "-u", SERVICE, "--", "env"]
    cmd += [f"{k}={v}" for k, v in runtime_env.items()]
    cmd += [str(release / "verify-runtime.sh"), str(release), str(smoke)]
    result = call(cmd, timeout=180, capture=True, check=False)
    evidence = result.stdout + result.stderr
    out.write_text(evidence, encoding="utf-8")
    if result.returncode:
        raise RuntimeError(f"runtime verification failed for {release}: {evidence[-1200:]}")
    return {"release": str(release), "smoke": str(smoke), "output_file": str(out), "returncode": 0}


def launch(release: Path, db: Path, key: Path, port: int, log: Path):
    env = {
        "PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
        "HOME": "/var/lib/openai-login", "OPENAI_LOGIN_DB": str(db),
        "OPENAI_LOGIN_KEY": str(key), "PLAYWRIGHT_NODEJS_PATH": str(release / "node"),
        "PLAYWRIGHT_DRIVER_PATH": str(release / "driver"), "OPENAI_LOGIN_PROXY": "",
        "SUB2API_BASE_URL": "", "SUB2API_ADMIN_API_KEY": "", "AUTH_INSTANCE_ID": "default",
    }
    command = ["runuser", "-u", SERVICE, "--", "env"] + [f"{k}={v}" for k, v in env.items()]
    command += ["/usr/bin/xvfb-run", "-a", "--server-args=-screen 0 1920x1080x24 -nolisten tcp",
                str(release / "openai-login-web"), "-bind=127.0.0.1", f"-port={port}",
                "-headless=false", "-open-browser=false", "-max-concurrent=1", "-rate-limit=30",
                "-login-timeout=3m", "-browser-compat=false"]
    handle = log.open("w", encoding="utf-8")
    proc = subprocess.Popen(command, stdout=handle, stderr=subprocess.STDOUT, start_new_session=True)
    return proc, handle


def integrity(path: Path):
    with sqlite3.connect(f"file:{path}?mode=ro", uri=True) as db:
        return db.execute("PRAGMA integrity_check").fetchone()[0]


def prepare_candidate(source: Path, target: Path, key_source: Path, key_target: Path):
    source_db = sqlite3.connect(f"file:{source}?mode=ro", uri=True)
    target_db = sqlite3.connect(target)
    try:
        source_db.backup(target_db)
    finally:
        source_db.close(); target_db.close()
    shutil.copy2(key_source, key_target)
    now = int(time.time() * 1000)
    with sqlite3.connect(target) as db:
        db.execute("UPDATE recovery_settings SET enabled=0 WHERE id=1")
        try:
            db.execute("UPDATE login_attempts SET status='interrupted',finished_at=? WHERE status='running'", (now,))
            db.execute("UPDATE accounts SET status='interrupted',updated_at=? WHERE status='running'", (now,))
        except sqlite3.OperationalError:
            pass
        # Candidate-only work must never call production APIs during staging.
        for table, states in (("account_deliveries", ("queued", "checking", "importing")),
                              ("account_credential_repairs", ("queued", "checking")),
                              ("account_recovery_rechecks", ("pending", "checking")),
                              ("account_recovery_tasks", ("queued", "validating", "disabling_schedule", "logging_in", "login_succeeded", "identity_verified", "refreshing_credentials", "applying_credentials", "credentials_applied", "enabling_schedule"))):
            try:
                marks = ",".join("?" for _ in states)
                db.execute(f"UPDATE {table} SET state='canceled',manual_action='candidate_stage',updated_at=? WHERE state IN ({marks})", (now, *states))
            except sqlite3.OperationalError:
                pass
        try:
            db.execute("UPDATE account_check_batches SET state='stopped',finished_at=? WHERE state IN ('active','stopping')", (now,))
            db.execute("UPDATE account_checks SET state='canceled',finished_at=? WHERE state IN ('queued','running')", (now,))
        except sqlite3.OperationalError:
            pass
        try:
            db.execute("UPDATE sub2_imports SET state=CASE WHEN state='queued' THEN 'failed' ELSE 'unknown' END,last_error='candidate staging',updated_at=? WHERE state IN ('queued','sending','confirming')", (now,))
        except sqlite3.OperationalError:
            pass
        db.commit()
    if integrity(target) != "ok":
        raise RuntimeError(f"candidate integrity check failed: {target}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--release-id", required=True)
    p.add_argument("--binary", required=True)
    p.add_argument("--smoke", required=True)
    p.add_argument("--verify", required=True)
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("stage must run as root")
    release_id = args.release_id
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", release_id):
        raise SystemExit("release-id must be a simple directory name")
    release = ROOT / "releases" / release_id
    candidate = ROOT / "candidates" / release_id
    current = Path(os.path.realpath(ROOT / "current"))
    if release.exists() or candidate.exists():
        raise SystemExit(f"release already exists: {release_id}")
    if not current.is_dir() or not DB.is_file() or not KEY.is_file():
        raise SystemExit("current release or production database is missing")
    for supplied in (Path(args.binary), Path(args.smoke), Path(args.verify)):
        if not supplied.is_file() or not os.access(supplied, os.X_OK):
            raise SystemExit(f"executable is missing: {supplied}")

    release.mkdir(mode=0o755, parents=False)
    candidate.mkdir(mode=0o700, parents=True)
    proc = None; log = None; handle = None
    try:
        shutil.copy2(args.binary, release / "openai-login-web")
        shutil.copy2(args.smoke, release / "kkai-auth-login-smoke")
        shutil.copy2(args.verify, release / "verify-runtime.sh")
        shutil.copy2(current / "node", release / "node")
        shutil.copytree(current / "driver", release / "driver")
        account = pwd.getpwnam(SERVICE)
        copy_tree_permissions(release)
        cdb, ckey = candidate / "accounts.db", candidate / "accounts.key"
        prepare_candidate(DB, cdb, KEY, ckey)
        os.chown(candidate, account.pw_uid, account.pw_gid)
        for sidecar in candidate.glob("accounts.db*"):
            os.chown(sidecar, account.pw_uid, account.pw_gid)
            os.chmod(sidecar, 0o600)
        os.chown(ckey, account.pw_uid, account.pw_gid); os.chmod(ckey, 0o600)

        log = candidate / "candidate.log"
        proc, handle = launch(release, cdb, ckey, 28082, log)
        ready = wait_ready(proc, 28082)
        status, health = get_json("/health", 28082)
        if status != 200 or health.get("status") != "ok" or health.get("sub2_configured") is not False:
            raise RuntimeError(f"candidate health mismatch: {health}")
        status, settings = get_json("/api/account-recovery/settings", 28082)
        if status != 200 or settings.get("auto_recovery_enabled") is not False:
            raise RuntimeError(f"candidate recovery setting mismatch: {settings}")
        pid, env = process_for_release(release)
        candidate_runtime = runtime_check(release, release / "kkai-auth-login-smoke", env, release / "CANDIDATE_RUNTIME.txt")
        # Verify the rollback release with the same service identity/display while the
        # candidate X server is alive, before production is ever stopped.
        rollback_smoke = current / "kkai-auth-login-smoke"
        rollback_verify = current / "verify-runtime.sh"
        if not rollback_smoke.is_file() or not rollback_verify.is_file():
            raise RuntimeError("rollback release is missing runtime smoke artifacts")
        old_runtime = runtime_check(current, rollback_smoke, env, release / "ROLLBACK_RUNTIME.txt")

        # Read the migrated candidate with the old binary in another isolated copy.
        compat = candidate / "compat"
        compat.mkdir(mode=0o700)
        compat_db, compat_key = compat / "accounts.db", compat / "accounts.key"
        shutil.copy2(cdb, compat_db); shutil.copy2(ckey, compat_key)
        os.chown(compat, account.pw_uid, account.pw_gid); os.chown(compat_db, account.pw_uid, account.pw_gid); os.chown(compat_key, account.pw_uid, account.pw_gid)
        old_proc, old_handle = launch(current, compat_db, compat_key, 28083, compat / "compat.log")
        try:
            old_ready = wait_ready(old_proc, 28083)
            old_status, old_health = get_json("/health", 28083)
            if old_status != 200 or old_health.get("status") != "ok":
                raise RuntimeError(f"rollback compatibility health mismatch: {old_health}")
        finally:
            stop_group(old_proc); old_handle.close()

        static = {}
        for route in ("/", "/api/client.js"):
            with urllib.request.urlopen(f"http://127.0.0.1:28082{route}", timeout=10) as response:
                static[route] = {"status": response.status, "sha256": hashlib.sha256(response.read()).hexdigest()}
        evidence = {"release_id": release_id, "rollback_release": str(current), "binary_sha256": digest(release / "openai-login-web"),
                    "ready": ready, "health": health, "recovery_settings": settings, "candidate_database_integrity": integrity(cdb),
                    "candidate_runtime": candidate_runtime, "rollback_runtime": old_runtime,
                    "rollback_compatibility": {"ready": old_ready, "health": old_health}, "static_sha256": static,
                    "candidate_pid": pid}
        (release / "CANDIDATE_ACCEPTANCE.json").write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        os.chown(release / "CANDIDATE_ACCEPTANCE.json", 0, 0); os.chmod(release / "CANDIDATE_ACCEPTANCE.json", 0o644)
        print(json.dumps(evidence, ensure_ascii=False, sort_keys=True))
    except BaseException:
        raise
    finally:
        if proc is not None:
            stop_group(proc)
        if handle is not None:
            try: handle.close()
            except Exception: pass
        if not (release / "CANDIDATE_ACCEPTANCE.json").exists():
            shutil.rmtree(candidate, ignore_errors=True); shutil.rmtree(release, ignore_errors=True)


if __name__ == "__main__":
    main()
