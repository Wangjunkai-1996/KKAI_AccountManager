#!/bin/bash

# Reuse the supported Debian/Playwright image and persistent Compose volume.
set -euo pipefail
cd "$(dirname "$0")"

if docker container inspect openai-login >/dev/null 2>&1; then
    echo "发现旧版 openai-login 容器。请先按 DOCKER_DEPLOY.md 迁移数据库和密钥，再删除旧容器。" >&2
    exit 1
fi
if docker container inspect openai-login-web >/dev/null 2>&1; then
    volume=$(docker container inspect --format '{{range .Mounts}}{{if eq .Destination "/app/data"}}{{.Name}}{{end}}{{end}}' openai-login-web)
    if [ "$volume" != "openai-login-data" ]; then
        echo "现有容器未使用 openai-login-data 卷。请先按 DOCKER_DEPLOY.md 迁移，避免替换容器丢失数据。" >&2
        exit 1
    fi
fi

docker compose up -d --build
echo "服务已启动：http://127.0.0.1:8080"
echo "状态：docker compose ps；日志：docker compose logs -f"
