#!/usr/bin/env bash
set -euo pipefail

# Run before switching current, as the configured service user and with the
# service's DISPLAY, XAUTHORITY and OPENAI_LOGIN_CHROME_PATH environment.
fail() {
    printf 'Runtime verification failed: %s\n' "$*" >&2
    exit 1
}

[[ $# -eq 2 ]] || fail "usage: $0 <absolute-release-directory> <absolute-smoke-test-binary>"
release_dir=$1
smoke_binary=$2
[[ "$release_dir" = /* && -d "$release_dir" ]] || fail "release directory must exist and be absolute: $release_dir"
[[ "$smoke_binary" = /* && -x "$smoke_binary" ]] || fail "smoke test binary must be executable and absolute: $smoke_binary"

service_user=$(systemctl show -p User --value openai-login.service) || fail "cannot read openai-login.service User"
[[ -n "$service_user" ]] || fail "openai-login.service must have an explicit User"
service_uid=$(id -u "$service_user") || fail "cannot resolve service user: $service_user"
[[ "$(id -u)" = "$service_uid" ]] || fail "must run as service user: $service_user"

export PLAYWRIGHT_NODEJS_PATH="$release_dir/node"
export PLAYWRIGHT_DRIVER_PATH="$release_dir/driver"
unset PLAYWRIGHT_CLI_PATH
test -x "$PLAYWRIGHT_NODEJS_PATH" || fail "Node is not executable by $service_user: $PLAYWRIGHT_NODEJS_PATH"
test -r "$PLAYWRIGHT_DRIVER_PATH/package/cli.js" || fail "Playwright CLI is not readable by $service_user: $PLAYWRIGHT_DRIVER_PATH/package/cli.js"
"$PLAYWRIGHT_NODEJS_PATH" --version || fail "Node execution failed"
"$PLAYWRIGHT_NODEJS_PATH" "$PLAYWRIGHT_DRIVER_PATH/package/cli.js" --version || fail "Playwright CLI execution failed"

smoke_pattern='^Test(NativeChromeStartupSmoke|PlaywrightBrowserStartupSmoke)$'
available_tests=$("$smoke_binary" -test.list "$smoke_pattern") || fail "cannot list browser smoke tests"
for smoke_test in TestNativeChromeStartupSmoke TestPlaywrightBrowserStartupSmoke; do
    printf '%s\n' "$available_tests" | grep -Fxq "$smoke_test" || fail "smoke binary is missing $smoke_test"
done
OPENAI_LOGIN_STARTUP_SMOKE=1 "$smoke_binary" -test.run "$smoke_pattern" -test.v -test.timeout 75s || fail "browser startup smoke test failed"
printf 'Runtime verification passed for %s as %s\n' "$release_dir" "$service_user"
