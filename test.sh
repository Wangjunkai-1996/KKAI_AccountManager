#!/bin/bash

# 快速测试脚本

set -e

echo "🧪 开始测试 OpenAI 自动登录工具..."
echo ""

# 1. 检查 Go 环境
echo "1️⃣  检查 Go 环境..."
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装，请先安装 Go 1.21+"
    exit 1
fi
go version
echo ""

# 2. 初始化模块
echo "2️⃣  初始化 Go 模块..."
go mod init github.com/Wei-Shaw/sub2api/tools/openai-login 2>/dev/null || true
go mod tidy
echo "✅ 模块初始化完成"
echo ""

# 3. 下载依赖
echo "3️⃣  下载依赖..."
go get github.com/playwright-community/playwright-go@v0.4501.1
go mod tidy
echo "✅ 依赖下载完成"
echo ""

# 4. 编译检查
echo "4️⃣  编译检查..."
if go build -o /tmp/openai-login-test ./cmd/main.go; then
    echo "✅ 编译成功"
    rm /tmp/openai-login-test
else
    echo "❌ 编译失败"
    exit 1
fi
echo ""

# 5. 安装 Playwright 浏览器
echo "5️⃣  安装 Playwright 浏览器..."
echo "   (这可能需要几分钟...)"
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install chromium
echo "✅ 浏览器安装完成"
echo ""

echo "🎉 所有测试通过！工具已就绪。"
echo ""
echo "📝 下一步："
echo "   1. 创建 accounts.txt 文件"
echo "   2. 运行: make build"
echo "   3. 运行: ./bin/openai-login -input accounts.txt"
