#!/bin/bash

set -e

echo "🔧 安装 Playwright 浏览器驱动..."

cd "$(dirname "$0")"

# Keep the installer aligned with go.mod. Mixing the old community module and
# an unrelated version can silently replace the module graph or driver.
PLAYWRIGHT_VERSION="v0.6201.1"
echo "📦 使用项目锁定版本 ${PLAYWRIGHT_VERSION}..."
if go run "github.com/mxschmitt/playwright-go/cmd/playwright@${PLAYWRIGHT_VERSION}" install chromium; then
    echo "✅ Playwright 浏览器安装成功！"
    exit 0
fi

echo "❌ 安装失败，请手动运行以下命令："
echo "go run github.com/mxschmitt/playwright-go/cmd/playwright@${PLAYWRIGHT_VERSION} install chromium"
exit 1
