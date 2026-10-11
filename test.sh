#!/bin/bash

# 环境、依赖与 CLI 构建检查

set -e

echo "🧪 开始检查 OpenAI 自动登录工具环境..."
echo ""

# 1. 检查 Go 环境
echo "1️⃣  检查 Go 环境..."
if ! command -v go &> /dev/null; then
    echo "❌ Go 未安装，请先安装 Go 1.24+"
    exit 1
fi
go version
echo ""

# 2. 校验模块
echo "2️⃣  校验 Go 模块..."
go mod download
echo "✅ 模块依赖已就绪"
echo ""

# 3. 检查依赖
echo "3️⃣  检查依赖版本..."
go list -m github.com/mxschmitt/playwright-go
echo "✅ 依赖版本与 go.mod 一致"
echo ""

# 4. 编译检查
echo "4️⃣  CLI 编译检查..."
if go build -o /tmp/openai-login-test ./cmd/main.go; then
    echo "✅ 编译成功"
    rm /tmp/openai-login-test
else
    echo "❌ 编译失败"
    exit 1
fi
echo ""

# 5. 安装 Playwright 浏览器
echo "5️⃣  安装 Playwright 浏览器运行依赖..."
echo "   (这可能需要几分钟...)"
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium
echo "✅ 浏览器安装完成"
echo ""

echo "🎉 环境与 CLI 构建检查通过。"
echo "   Web 服务定向测试请运行: ./test_compile.sh"
echo ""
echo "📝 下一步："
echo "   1. 创建 accounts.txt 文件"
echo "   2. 运行: make build"
echo "   3. 运行: ./bin/openai-login -input accounts.txt"
