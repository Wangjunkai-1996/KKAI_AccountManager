#!/bin/bash

# 快速本地构建脚本
set -e

echo "🔧 本地编译中..."

# 编译 Linux 版本的二进制（用于 Docker）
GOOS=linux GOARCH=amd64 go build -o bin/openai-login-web-linux ./cmd/server

echo "✅ 编译完成"
echo ""
echo "🐳 构建 Docker 镜像..."

# 使用轻量级 Dockerfile
docker build -f Dockerfile.local -t openai-login-web:latest .

echo "✅ 镜像构建完成"
echo ""
echo "🚀 启动容器..."

# 停止旧容器（如果存在）
docker stop openai-login 2>/dev/null || true
docker rm openai-login 2>/dev/null || true

# 启动新容器
docker run -d \
  --name openai-login \
  -p 127.0.0.1:8080:8080 \
  --restart unless-stopped \
  --health-cmd='wget -qO- http://127.0.0.1:8080/ready || exit 1' \
  --health-interval=30s --health-timeout=5s --health-start-period=15s --health-retries=3 \
  openai-login-web:latest \
  -bind=0.0.0.0 -headless=true -open-browser=false

echo "✅ 服务启动成功！"
echo ""
echo "📍 访问地址: http://localhost:8080"
echo ""
echo "📊 查看日志: docker logs -f openai-login"
echo "🛑 停止服务: docker stop openai-login"
