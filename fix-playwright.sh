#!/bin/bash

echo "🔧 尝试修复 Playwright 问题..."

cd "$(dirname "$0")"

# 尝试几个可能可用的版本
versions=("v0.4700.0" "v0.4600.0" "v0.4500.0" "v0.4400.0" "v0.4300.0")

for version in "${versions[@]}"; do
    echo ""
    echo "📦 尝试版本 $version..."

    # 更新 go.mod
    sed -i '' "s/github.com\/playwright-community\/playwright-go v[0-9.]*/github.com\/playwright-community\/playwright-go ${version#v}/" go.mod

    # 更新依赖
    go mod tidy 2>&1 | grep -v "go: downloading"

    if [ $? -eq 0 ]; then
        echo "✅ 依赖更新成功，尝试安装浏览器..."

        # 尝试安装浏览器
        go run github.com/playwright-community/playwright-go/cmd/playwright@$version install chromium 2>&1

        if [ $? -eq 0 ]; then
            echo ""
            echo "🎉 成功！使用版本: $version"
            echo ""
            echo "现在重新编译:"
            go build -o bin/openai-login-web ./cmd/server
            echo ""
            echo "✅ 完成！运行 ./bin/openai-login-web 启动服务器"
            exit 0
        fi
    fi
done

echo ""
echo "❌ 所有版本都失败了"
echo "建议：检查网络连接或使用 VPN"
exit 1
