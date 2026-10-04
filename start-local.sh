#!/bin/bash

# Mac 本地快速启动脚本
set -e

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

# 本地服务端配置（包含 Sub2API 管理密钥），仅由服务进程读取。
if [ -f "$SCRIPT_DIR/.sub2api.env" ]; then
    set -a
    # shellcheck disable=SC1091
    . "$SCRIPT_DIR/.sub2api.env"
    set +a
fi

: "${PLAYWRIGHT_NODEJS_PATH:=$(command -v node)}"
if [ -z "$PLAYWRIGHT_NODEJS_PATH" ]; then
    echo "❌ 未找到 Node.js，请先安装 Node.js 20+。" >&2
    exit 1
fi
export PLAYWRIGHT_NODEJS_PATH

echo "🔧 编译中..."

# 编译 Mac 版本
mkdir -p bin
go build -o bin/openai-login-web ./cmd/server

echo "✅ 编译完成"
echo ""
echo "🔍 准备 Playwright driver 和系统 Chrome..."
./install-browser.sh
echo "✅ Playwright 已就绪"

PORT=8080
ARGS=("$@")
for ((i = 0; i < ${#ARGS[@]}; i++)); do
    arg="${ARGS[$i]}"
    case "$arg" in
        -port=*) PORT="${arg#-port=}" ;;
        -port)
            if ((i + 1 < ${#ARGS[@]})); then
                PORT="${ARGS[$((i + 1))]}"
            fi
            ;;
    esac
done

echo ""
echo "🚀 启动服务..."
echo "📍 访问地址: http://localhost:$PORT"
echo "🛑 停止服务: Ctrl+C"
echo ""

# 启动服务。默认使用实际 Chrome 的原生 UA/client hints；需要做兼容
# 配置实验时再显式传入 -browser-compat=true。
./bin/openai-login-web "$@"
