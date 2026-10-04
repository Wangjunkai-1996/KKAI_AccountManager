#!/usr/bin/env bash
set -euo pipefail

# Build-only helper. It creates a local Linux amd64 artifact and never uploads,
# switches releases, restarts services, or contacts a remote host.

output_path="${1:-bin/openai-login-web-linux}"
output_dir="$(dirname "$output_path")"
mkdir -p "$output_dir"

GOOS=linux GOARCH=amd64 CGO_ENABLED=0 \
    go build -o "$output_path" ./cmd/server

test -s "$output_path"
echo "Linux amd64 构建完成：$output_path"
echo "该脚本只生成本地产物；部署、上传和回滚请按 SYS1_DEPLOYMENT.md 单独执行。"
