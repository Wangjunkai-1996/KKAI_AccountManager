#!/usr/bin/env python3
"""Blue/green deployment gates for the standalone AUTH systemd service."""
from __future__ import annotations

import argparse
import datetime as dt
import hashlib
import json
import os
import re
import shutil
import sqlite3
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

ROOT = Path("/opt/openai-login")
DB = Path("/var/lib/openai-login/data/accounts.db")
SOCKET = "/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock"
SERVICE = "openai-login.service"


def call(argv, *, timeout=120, check=True, capture=False):
    print("+", " ".join(map(str, argv)), flush=True)
    return subprocess.run(argv, timeout=timeout, check=check, text=True,
                          capture_output=capture)


def sha(path: Path):
    h = hashlib.sha256()
    with path.open("rb") as f:
        for block in iter(lambda: f.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def json_get(url: str, *, timeout=8):
    with urllib.request.urlopen(url, timeout=timeout) as r:
        return r.status, json.loads(r.read())


def wait_service(active: bool, timeout=60):
    end = time.monotonic() + timeout
    last = ""
    while time.monotonic() < end:
        r = subprocess.run(["systemctl", "is-active", SERVICE], text=True, capture_output=True)
        last = r.stdout.strip()
        if (last == "active") == active:
            return last
        time.sleep(1)
    raise RuntimeError(f"service active wait timed out (want={active}, last={last})")


def wait_http(timeout=90):
    end = time.monotonic() + timeout
    last = "connection refused"
    while time.monotonic() < end:
        try:
            rs, ready = json_get("http://127.0.0.1:18082/ready")
            hs, health = json_get("http://127.0.0.1:18082/health")
            if rs == hs == 200 and ready.get("status") == "ready" and ready.get("database_ready") is True and health.get("status") == "ok":
                return ready, health
            last = f"ready={rs}/{ready}, health={hs}/{health}"
        except (OSError, ValueError, urllib.error.HTTPError) as exc:
            last = str(exc)
        time.sleep(1)
    raise RuntimeError(f"service did not become ready within {timeout}s: {last}")


def integrity(path: Path):
    with sqlite3.connect(f"file:{path}?mode=ro", uri=True) as db:
        return db.execute("PRAGMA integrity_check").fetchone()[0]


def counts(path: Path):
    queries = {
        "login_attempts_running": "SELECT COUNT(*) FROM login_attempts WHERE status='running'",
        "account_checks_active": "SELECT COUNT(*) FROM account_checks WHERE state IN ('queued','running')",
        "check_batches_active": "SELECT COUNT(*) FROM account_check_batches WHERE state IN ('active','stopping')",
        "sub2_imports_active": "SELECT COUNT(*) FROM sub2_imports WHERE state IN ('queued','sending','confirming')",
        "recovery_active": "SELECT COUNT(*) FROM account_recovery_tasks WHERE state IN ('queued','validating','disabling_schedule','logging_in','login_succeeded','identity_verified','refreshing_credentials','applying_credentials','credentials_applied','enabling_schedule')",
        "deliveries_active": "SELECT COUNT(*) FROM account_deliveries WHERE state IN ('queued','checking','importing')",
        "rechecks_active": "SELECT COUNT(*) FROM account_recovery_rechecks WHERE state='checking'",
        "repairs_active": "SELECT COUNT(*) FROM account_credential_repairs WHERE state='checking'",
    }
    out = {}
    with sqlite3.connect(f"file:{path}?mode=ro", uri=True) as db:
        for key, query in queries.items():
            try:
                out[key] = db.execute(query).fetchone()[0]
            except sqlite3.OperationalError:
                out[key] = None
    return out


def assert_idle(value):
    active = {k: v for k, v in value.items() if isinstance(v, int) and v}
    if active:
        raise RuntimeError(f"in-flight work is not empty: {active}")


def service_pid(release: Path):
    expected = str(release / "openai-login-web")
    for entry in Path("/proc").iterdir():
        if not entry.name.isdigit():
            continue
        try:
            if os.path.realpath(entry / "exe") == expected:
                return int(entry.name)
        except OSError:
            continue
    raise RuntimeError(f"running process does not use {expected}")


def proc_env(pid: int):
    values = {}
    for item in Path(f"/proc/{pid}/environ").read_bytes().split(b"\0"):
        if b"=" in item:
            k, v = item.split(b"=", 1); values[k.decode(errors="replace")] = v.decode(errors="replace")
    return values


def runtime_smoke(release: Path, pid: int, output: Path):
    env = proc_env(pid)
    if not env.get("DISPLAY") or not env.get("XAUTHORITY"):
        raise RuntimeError("service process does not expose DISPLAY/XAUTHORITY")
    smoke = release / "kkai-auth-login-smoke"
    verify = release / "verify-runtime.sh"
    if not smoke.is_file() or not verify.is_file():
        raise RuntimeError(f"runtime artifacts missing from {release}")
    values = {"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
              "HOME": "/var/lib/openai-login", "DISPLAY": env["DISPLAY"], "XAUTHORITY": env["XAUTHORITY"],
              "PLAYWRIGHT_NODEJS_PATH": str(release / "node"), "PLAYWRIGHT_DRIVER_PATH": str(release / "driver"),
              "OPENAI_LOGIN_STARTUP_SMOKE": "1"}
    command = ["nsenter", "-t", str(pid), "-m", "-u", "-i", "-n", "-p", "--", "runuser", "-u", "openai-login", "--", "env"]
    command += [f"{k}={v}" for k, v in values.items()] + [str(verify), str(release), str(smoke)]
    result = call(command, timeout=180, check=False, capture=True)
    evidence = result.stdout + result.stderr
    output.write_text(evidence, encoding="utf-8")
    if result.returncode:
        raise RuntimeError(f"runtime smoke failed for {release}: {evidence[-1200:]}")
    return {"pid": pid, "release": str(release), "output": str(output), "returncode": 0}


def static_hashes():
    result = {}
    for route in ("/", "/api/client.js"):
        with urllib.request.urlopen("http://127.0.0.1:18082" + route, timeout=10) as r:
            body = r.read()
            result[route] = {"status": r.status, "sha256": hashlib.sha256(body).hexdigest()}
    return result


def socket_health():
    result = call(["curl", "--fail", "--silent", "--show-error", "--unix-socket", SOCKET, "http://localhost/health"], timeout=15, capture=True)
    return json.loads(result.stdout)


def public_guard():
    result = call(["curl", "--silent", "--show-error", "--insecure", "--max-time", "10", "-o", "/dev/null", "-w", "%{http_code}", "https://auth.kkrich.ltd/"], timeout=15, check=False, capture=True)
    return result.stdout.strip()


def journal(since):
    result = call(["journalctl", "-u", SERVICE, "--since", since, "--no-pager", "-o", "cat"], timeout=30, capture=True)
    patterns = {
        "panic": r"\bpanic\b", "fatal": r"\bfatal\b", "database_locked": r"database locked",
        "storage_failure": r"storage failure|存储故障", "listen_failure": r"listen.*fail|监听失败",
        "browser_runtime_permission": r"browser_runtime_permission", "login_failed": r"login_failed|登录失败",
        "oauth_401": r"oauth.*(?:HTTP(?:/\d(?:\.\d)?)?\s+|status(?:_code)?[=: ]+|状态码[=:： ]+)401\b",
        "oauth_403": r"oauth.*(?:HTTP(?:/\d(?:\.\d)?)?\s+|status(?:_code)?[=: ]+|状态码[=:： ]+)403\b",
        "oauth_5xx": r"oauth.*(?:HTTP(?:/\d(?:\.\d)?)?\s+|status(?:_code)?[=: ]+|状态码[=:： ]+)5\d\d\b",
    }
    return {name: sum(bool(re.search(expr, line, re.I)) for line in result.stdout.splitlines()) for name, expr in patterns.items()}


def atomic_switch(link: Path, target: Path, name: str):
    temporary = link.parent / f".current-{name}.tmp"
    try:
        temporary.unlink()
    except FileNotFoundError:
        pass
    temporary.symlink_to(target)
    os.replace(temporary, link)


def evidence(release, args, old, backup, before, started, ready, health, smoke, errors, phase):
    settings = {}
    try:
        _, settings = json_get("http://127.0.0.1:18082/api/account-recovery/settings")
    except Exception as exc:
        settings = {"error": type(exc).__name__}
    return {"release_id": args.release_id, "source_commit": args.source_commit, "binary_sha256": sha(release / "openai-login-web"),
            "phase": phase, "started_at": started, "active_release": str(release), "rollback_release": str(old),
            "backup_db": str(backup), "backup_integrity": integrity(backup), "before_counts": before,
            "after_counts": counts(DB), "ready": ready, "health": health, "socket_health": socket_health(),
            "public_unauthenticated_status": public_guard(), "static_hashes": static_hashes(),
            "runtime_smoke": smoke, "recovery_settings": settings, "journal_errors": errors,
            "service_state": subprocess.check_output(["systemctl", "is-active", SERVICE], text=True).strip(),
            "nrestarts": subprocess.check_output(["systemctl", "show", "-p", "NRestarts", "--value", SERVICE], text=True).strip()}


def require_recovery_running(evidence, *, delayed=False):
    settings = evidence.get("recovery_settings", {})
    if settings.get("auto_recovery_enabled") is not True or settings.get("running") is not True:
        raise RuntimeError(f"automatic recovery is not running: {settings}")
    if delayed and not settings.get("last_scan_at"):
        raise RuntimeError(f"automatic recovery has not completed a scan: {settings}")


def main():
    p = argparse.ArgumentParser()
    p.add_argument("--release-id", required=True)
    p.add_argument("--source-commit", required=True)
    p.add_argument("--binary-sha256", required=True)
    p.add_argument("--delay-seconds", type=int, default=120)
    args = p.parse_args()
    if os.geteuid() != 0:
        raise SystemExit("deploy must run as root")
    if not re.fullmatch(r"[A-Za-z0-9][A-Za-z0-9._-]{0,63}", args.release_id):
        raise SystemExit("release-id must be a simple directory name")
    release = ROOT / "releases" / args.release_id
    link = ROOT / "current"
    old = Path(os.path.realpath(link))
    if not release.is_dir() or old == release:
        raise SystemExit("staged release is missing or already active")
    if sha(release / "openai-login-web") != args.binary_sha256:
        raise SystemExit("staged binary hash does not match local build")
    acceptance = json.loads((release / "CANDIDATE_ACCEPTANCE.json").read_text(encoding="utf-8"))
    if Path(acceptance.get("rollback_release", "")) != old:
        raise SystemExit(f"rollback target changed since staging: {old}")
    before = counts(DB); assert_idle(before)
    old_pid = service_pid(old)
    # Rollback runtime is a gate before stopping the only production service.
    rollback_runtime = runtime_smoke(old, old_pid, release / "ROLLBACK_PRODUCTION_RUNTIME.txt")
    started = dt.datetime.now(dt.timezone.utc).replace(microsecond=0).isoformat().replace("+00:00", "Z")
    switched = False
    stopped = False
    try:
        call(["systemctl", "stop", SERVICE], timeout=45); wait_service(False, 45)
        stopped = True
        after_stop = counts(DB); assert_idle(after_stop)
        backup = Path("/var/lib/openai-login/backups") / f"accounts-before-{args.release_id}.db"
        backup.parent.mkdir(mode=0o700, parents=True, exist_ok=True)
        if backup.exists():
            raise RuntimeError(f"backup already exists: {backup}")
        source = sqlite3.connect(f"file:{DB}?mode=ro", uri=True); target = sqlite3.connect(backup)
        try: source.backup(target)
        finally: source.close(); target.close()
        os.chmod(backup, 0o600)
        if integrity(backup) != "ok": raise RuntimeError("production backup integrity failed")
        atomic_switch(link, release, args.release_id); switched = True
        call(["systemctl", "start", SERVICE], timeout=45); wait_service(True, 60)
        ready, health = wait_http(90)
        if Path(os.path.realpath(link)) != release: raise RuntimeError("current symlink changed unexpectedly")
        if health.get("sub2_configured") is not True: raise RuntimeError(f"production Sub2 configuration disappeared: {health}")
        pid = service_pid(release)
        smoke = runtime_smoke(release, pid, release / "PRODUCTION_RUNTIME.txt")
        static = static_hashes()
        expected = acceptance.get("static_sha256", {})
        if expected and any(static.get(k, {}).get("sha256") != v.get("sha256") for k, v in expected.items()):
            raise RuntimeError(f"static resources differ from staged candidate: {static} != {expected}")
        errors = journal(started)
        if any(errors.values()): raise RuntimeError(f"startup journal errors: {errors}")
        if public_guard() != "401": raise RuntimeError("public unauthenticated guard is not HTTP 401")
        immediate = evidence(release, args, old, backup, before, started, ready, health, smoke, errors, "immediate")
        require_recovery_running(immediate)
        if immediate["nrestarts"] != "0": raise RuntimeError(f"service restarted during deployment: {immediate['nrestarts']}")
        (release / "ACCEPTANCE.json").write_text(json.dumps(immediate, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")

        if args.delay_seconds > 0:
            time.sleep(args.delay_seconds)
        delayed_ready, delayed_health = wait_http(45)
        delayed_errors = journal(started)
        delayed = evidence(release, args, old, backup, before, started, delayed_ready, delayed_health, smoke, delayed_errors, "delayed")
        delayed["delay_seconds"] = args.delay_seconds
        require_recovery_running(delayed, delayed=True)
        if delayed["nrestarts"] != "0" or any(delayed_errors.values()): raise RuntimeError(f"delayed acceptance failed: {delayed_errors}")
        (release / "DELAYED_ACCEPTANCE.json").write_text(json.dumps(delayed, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
        for path in (release / "ACCEPTANCE.json", release / "DELAYED_ACCEPTANCE.json"):
            os.chown(path, 0, 0); os.chmod(path, 0o644)
        shutil.rmtree(ROOT / "candidates" / args.release_id, ignore_errors=True)
        print(json.dumps(delayed, ensure_ascii=False, sort_keys=True))
    except BaseException:
        if switched:
            try:
                call(["systemctl", "stop", SERVICE], timeout=45, check=False)
                wait_service(False, 45)
                atomic_switch(link, old, args.release_id + "-rollback")
                call(["systemctl", "start", SERVICE], timeout=45)
                wait_service(True, 60); wait_http(90)
            except BaseException as rollback_error:
                print(f"automatic rollback failed: {rollback_error}", flush=True)
        elif stopped:
            try:
                call(["systemctl", "start", SERVICE], timeout=45)
                wait_service(True, 60); wait_http(90)
            except BaseException as restart_error:
                print(f"service restart after pre-switch failure failed: {restart_error}", flush=True)
        raise


if __name__ == "__main__":
    main()
