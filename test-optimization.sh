#!/usr/bin/env bash
set -euo pipefail

# Read-only verification for the login optimization. It does not format files,
# build binaries, write bin/, or run the repository-wide test suite.

go_files=(internal/login/*.go cmd/server/*.go)
for file in "${go_files[@]}"; do
    test -f "$file"
done

unformatted="$(gofmt -l "${go_files[@]}")"
if [[ -n "$unformatted" ]]; then
    echo "未格式化的 Go 文件：" >&2
    echo "$unformatted" >&2
    exit 1
fi

if command -v timeout >/dev/null 2>&1; then
    timeout 90s go test ./internal/login ./cmd/server -count=1
elif command -v gtimeout >/dev/null 2>&1; then
    gtimeout 90s go test ./internal/login ./cmd/server -count=1
elif command -v python3 >/dev/null 2>&1; then
    python3 - <<'PY'
import subprocess
import sys

try:
    result = subprocess.run(
        ["go", "test", "./internal/login", "./cmd/server", "-count=1"],
        timeout=90,
    )
except subprocess.TimeoutExpired:
    print("定向测试超过 90 秒，已停止。", file=sys.stderr)
    raise SystemExit(124)
raise SystemExit(result.returncode)
PY
else
    echo "需要 timeout、gtimeout 或 python3 才能执行 90 秒超时保护。" >&2
    exit 1
fi

echo "优化相关定向测试通过：internal/login、cmd/server"
