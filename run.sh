#!/bin/bash

# OpenAI 自动登录工具 - 快速启动脚本
# 请在你的 macOS 终端中运行此脚本（不是在 Claude 环境中）

set -e

echo "🚀 OpenAI 自动登录工具 - 快速启动"
echo "========================================"
echo ""

# 进入工具目录
cd "$(dirname "$0")"
TOOL_DIR="$(pwd)"
echo "📁 工具目录: $TOOL_DIR"
echo ""

# 检查 Go 环境
echo "1️⃣  检查 Go 环境..."
if ! command -v go &> /dev/null; then
    echo "❌ 错误：Go 未安装或不在 PATH 中"
    echo ""
    echo "请先安装 Go："
    echo "  方法1: brew install go"
    echo "  方法2: https://go.dev/dl/"
    exit 1
fi

GO_VERSION=$(go version)
echo "✅ $GO_VERSION"
echo ""

# 初始化模块（如果需要）
echo "2️⃣  初始化 Go 模块..."
if [ ! -f "go.sum" ]; then
    go mod download
    echo "✅ 依赖下载完成"
else
    echo "✅ 依赖已存在"
fi
echo ""

# 安装 Playwright 浏览器
echo "3️⃣  检查 Playwright 浏览器..."
if [ ! -d "$HOME/Library/Caches/ms-playwright" ]; then
    echo "   正在安装 Chromium 浏览器（首次安装需要几分钟）..."
    go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install chromium
    echo "✅ 浏览器安装完成"
else
    echo "✅ 浏览器已安装"
fi
echo ""

# 编译程序
echo "4️⃣  编译程序..."
mkdir -p bin
if go build -o bin/openai-login ./cmd/main.go; then
    echo "✅ 编译成功: bin/openai-login"
else
    echo "❌ 编译失败"
    exit 1
fi
echo ""

# 检查账号文件
echo "5️⃣  检查账号文件..."
if [ ! -f "accounts.txt" ]; then
    echo "⚠️  accounts.txt 不存在，创建示例文件..."
    cat > accounts.txt.template << 'EOF'
# OpenAI 账号文件
# 格式：email----password----totp_secret
#
# 示例：
# user@example.com----MyPassword123----ABCD1234EFGH5678IJKL
#
# 注意：
# 1. 三个字段用 ---- (4个短横线) 分隔
# 2. TOTP 密钥必须是 Base32 格式（大写字母 + 数字2-7）
# 3. 每行一个账号
# 4. # 开头的行是注释

EOF
    echo "✅ 已创建模板文件: accounts.txt.template"
    echo ""
    echo "📝 请编辑 accounts.txt 文件，添加你的账号信息"
    echo "   格式：email----password----totp_secret"
    exit 0
else
    ACCOUNT_COUNT=$(grep -v "^#" accounts.txt | grep -v "^$" | wc -l | tr -d ' ')
    echo "✅ 找到 accounts.txt，包含 $ACCOUNT_COUNT 个账号"
fi
echo ""

# 运行工具
echo "6️⃣  运行工具..."
echo "========================================"
echo ""

./bin/openai-login \
    -input accounts.txt \
    -output sub2api-accounts.json \
    -headless=true \
    -retry=2 \
    -delay-min=5 \
    -delay-max=10

echo ""
echo "========================================"
echo "🎉 完成！"
echo ""
echo "📊 结果文件: sub2api-accounts.json"
echo ""
echo "📝 下一步："
echo "   1. 检查生成的 JSON 文件"
echo "   2. 导入到 sub2api："
echo "      curl -X POST http://localhost:8080/api/v1/admin/accounts/import \\"
echo "           -H 'Content-Type: application/json' \\"
echo "           -d @sub2api-accounts.json"
echo ""
