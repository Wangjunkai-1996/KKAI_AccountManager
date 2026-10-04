#!/bin/bash

# 安装 Playwright 浏览器驱动
set -e

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 国内网络无法稳定访问 proxy.golang.org 和 registry.npmjs.org 时可覆盖这些变量。
: "${GOPROXY:=https://goproxy.cn,direct}"
: "${PLAYWRIGHT_GO_NPM_REGISTRY:=https://registry.npmmirror.com}"
: "${NODE_MIRROR:=https://npmmirror.com/mirrors/node}"
: "${PLAYWRIGHT_DOWNLOAD_HOST:=https://cdn.npmmirror.com/binaries/playwright}"
: "${PLAYWRIGHT_NODEJS_PATH:=$(command -v node)}"
if [ -z "$PLAYWRIGHT_NODEJS_PATH" ]; then
    echo "❌ 未找到 Node.js，请先安装 Node.js 20+。" >&2
    exit 1
fi
export GOPROXY PLAYWRIGHT_GO_NPM_REGISTRY NODE_MIRROR PLAYWRIGHT_DOWNLOAD_HOST PLAYWRIGHT_NODEJS_PATH

echo "📦 正在安装 Playwright 浏览器驱动..."
echo ""

# 只安装 driver；运行时使用本机已安装的 Google Chrome，不下载 180MB 的 bundled Chromium。
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 --version

echo ""
echo "✅ 浏览器驱动安装完成！"
echo ""
echo "现在可以运行: ./start-local.sh"
