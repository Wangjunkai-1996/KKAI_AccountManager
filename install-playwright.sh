#!/bin/bash

echo "🔧 安装 Playwright 浏览器驱动..."

cd "$(dirname "$0")"

# 尝试方法 1: 使用指定版本
echo "📦 方法 1: 使用 v0.4501.1 版本..."
go run github.com/playwright-community/playwright-go/cmd/playwright@v0.4501.1 install chromium

if [ $? -eq 0 ]; then
    echo "✅ Playwright 浏览器安装成功！"
    exit 0
fi

# 尝试方法 2: 使用 go install
echo "📦 方法 2: 使用 go install..."
go install github.com/playwright-community/playwright-go/cmd/playwright@v0.4501.1
if [ -f ~/go/bin/playwright ]; then
    ~/go/bin/playwright install chromium
    if [ $? -eq 0 ]; then
        echo "✅ Playwright 浏览器安装成功！"
        exit 0
    fi
fi

echo "❌ 安装失败，请手动运行以下命令："
echo "go run github.com/playwright-community/playwright-go/cmd/playwright@v0.4501.1 install chromium"
exit 1
