#!/bin/bash

set -e

echo "🔧 尝试修复 Playwright 问题..."

cd "$(dirname "$0")"

# Keep repair attempts on the module version declared by this repository.
versions=("v0.6201.1")

for version in "${versions[@]}"; do
    echo ""
    echo "📦 尝试版本 $version..."

    # Do not rewrite go.mod during a repair attempt.
    if go mod download && go run github.com/mxschmitt/playwright-go/cmd/playwright@$version install chromium 2>&1; then
        echo "✅ Playwright 已就绪，使用版本: $version"
		if ! go build -o bin/openai-login-web ./cmd/server; then
			echo "❌ 编译失败"
			exit 1
		fi
        echo "✅ 完成！运行 ./bin/openai-login-web 启动服务器"
        exit 0
    fi
done

echo ""
echo "❌ 所有版本都失败了"
echo "建议：检查网络连接或使用 GOPROXY/Playwright 镜像配置"
exit 1
