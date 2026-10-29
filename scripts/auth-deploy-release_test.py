#!/usr/bin/env python3
"""Regression: OAuth duration numbers must not become HTTP errors."""
import importlib.util
from pathlib import Path
from types import SimpleNamespace

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
