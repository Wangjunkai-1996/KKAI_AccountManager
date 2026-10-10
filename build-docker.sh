#!/bin/bash

set -euo pipefail

echo "🐳 构建 Docker 镜像..."

cd "$(dirname "$0")"

# 构建镜像
if ! docker build -t openai-login-web:latest .; then
    echo "❌ 构建失败"
    exit 1
fi

echo "✅ 构建成功！"
echo ""
echo "启动方式："
echo ""
echo "1. 使用 Docker Compose（推荐；升级旧容器前先迁移数据，见 DOCKER_DEPLOY.md）:"
echo "   docker compose up -d"
echo ""
echo "2. 直接使用 docker run:"
echo "   docker run -d -p 127.0.0.1:8080:8080 --name openai-login-web --env-file .sub2api.env --mount type=volume,src=openai-login-data,dst=/app/data --restart unless-stopped openai-login-web:latest"
echo ""
echo "3. 查看日志:"
echo "   docker compose logs -f"
echo "   或"
echo "   docker logs -f openai-login-web"
echo ""
echo "4. 停止服务:"
echo "   docker compose down"
echo "   或"
echo "   docker stop openai-login-web"
