#!/usr/bin/env python3
"""Regressions for AUTH release safety gates and redacted error counting."""
import importlib.util
import json
from pathlib import Path
import sqlite3
import tempfile
from types import SimpleNamespace
from unittest.mock import patch

path = Path(__file__).with_name('auth-deploy-release.py')
spec = importlib.util.spec_from_file_location('auth_deploy_release', path)
deploy = importlib.util.module_from_spec(spec)
spec.loader.exec_module(deploy)
deploy.call = lambda *args, **kwargs: SimpleNamespace(stdout=(
    'OAuth token: 耗时 574ms\n'
    'OAuth failed HTTP 503\n'
    'OAuth failed status=403\n'
    'OAuth HTTP/1.1 401\n'
))
errors = deploy.journal('unused')
assert errors['oauth_5xx'] == errors['oauth_403'] == errors['oauth_401'] == 1, errors
print('PASS: timing ignored; explicit OAuth HTTP errors counted')

deploy.assert_idle({'login': 0, 'recovery': 0})
for counters in ({'login': None}, {'login': 1}):
    try:
        deploy.assert_idle(counters)
    except RuntimeError:
        pass
    else:
        raise AssertionError(f'unsafe task counts accepted: {counters}')


def failed_release(counts, fail_start=False):
    with tempfile.TemporaryDirectory() as directory:
        root = Path(directory).resolve()
        old, new = root / 'releases' / 'old', root / 'releases' / 'new'
        old.mkdir(parents=True); new.mkdir()
        (root / 'current').symlink_to(old)
        (new / 'openai-login-web').write_bytes(b'test binary')
        (new / 'CANDIDATE_ACCEPTANCE.json').write_text(json.dumps({'rollback_release': str(old)}))
        db = root / 'accounts.db'
        with sqlite3.connect(db) as connection:
            connection.execute('CREATE TABLE test(id INTEGER)')
        commands = []

        def call(argv, **kwargs):
            commands.append(argv)
            if fail_start and argv[:2] == ['systemctl', 'start']:
                raise RuntimeError('simulated new release startup failure')

        argv = ['deploy', '--release-id', 'new', '--source-commit', 'test',
                '--binary-sha256', deploy.sha(new / 'openai-login-web')]
        real_path = Path

        def local_path(value):
            if value == '/var/lib/openai-login/backups':
                return root / 'backups'
            return real_path(value)

        with patch('sys.argv', argv), patch.object(deploy.os, 'geteuid', return_value=0), \
                patch.object(deploy, 'ROOT', root), patch.object(deploy, 'DB', db), \
                patch.object(deploy, 'Path', side_effect=local_path), \
                patch.object(deploy, 'counts', side_effect=counts), \
                patch.object(deploy, 'service_pid', return_value=123), \
                patch.object(deploy, 'runtime_smoke'), patch.object(deploy, 'wait_service'), \
                patch.object(deploy, 'wait_http'), patch.object(deploy, 'call', side_effect=call):
            try:
                deploy.main()
            except RuntimeError:
                pass
            else:
                raise AssertionError('unsafe deployment was accepted')
        return commands, (root / 'current').resolve().name


commands, active = failed_release([{'login': 0}, {'login': 1}])
assert commands == [] and active == 'old', (commands, active)
commands, active = failed_release([{'login': 0}, {'login': 0}, {'login': 1}])
assert commands == [['systemctl', 'stop', deploy.SERVICE], ['systemctl', 'start', deploy.SERVICE]], commands
assert active == 'old', active
commands, active = failed_release([{'login': 0}] * 3, fail_start=True)
assert commands == [['systemctl', 'stop', deploy.SERVICE], ['systemctl', 'start', deploy.SERVICE]], commands
assert active == 'new', active
print('PASS: unknown/busy counters block; fresh work prevents stop; pre-switch failure restarts; no automatic rollback')
