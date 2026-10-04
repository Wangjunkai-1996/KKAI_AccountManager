.PHONY: build clean test install run

# 变量定义
BINARY_NAME=openai-login
GO=go
GOFLAGS=-v
BUILD_DIR=bin

# 默认目标
all: build

# 安装依赖
deps:
	@echo "📦 安装 Go 依赖..."
	$(GO) mod download
	@echo "🌐 安装 Playwright 浏览器..."
	$(GO) run github.com/playwright-community/playwright-go/cmd/playwright@latest install chromium
	@echo "✅ 依赖安装完成"

# 构建
build:
	@echo "🔨 构建项目..."
	@mkdir -p $(BUILD_DIR)
	$(GO) build $(GOFLAGS) -o $(BUILD_DIR)/$(BINARY_NAME) cmd/main.go
	@echo "✅ 构建完成: $(BUILD_DIR)/$(BINARY_NAME)"

# 清理
clean:
	@echo "🧹 清理构建文件..."
	@rm -rf $(BUILD_DIR)
	@echo "✅ 清理完成"

# 运行测试
test:
	@echo "🧪 运行测试..."
	$(GO) test ./... -v

# 格式化代码
fmt:
	@echo "📝 格式化代码..."
	$(GO) fmt ./...
	@echo "✅ 格式化完成"

# 检查代码
lint:
	@echo "🔍 检查代码..."
	@command -v golangci-lint >/dev/null 2>&1 || { echo "请先安装 golangci-lint"; exit 1; }
	golangci-lint run
	@echo "✅ 检查完成"

# 运行（开发模式）
run:
	@echo "🚀 运行程序..."
	$(GO) run cmd/main.go -input accounts.txt -headless=false

# 运行（生产模式）
run-prod:
	@echo "🚀 运行程序（生产模式）..."
	$(GO) run cmd/main.go -input accounts.txt -headless=true

# 安装到系统
install: build
	@echo "📥 安装到系统..."
	@cp $(BUILD_DIR)/$(BINARY_NAME) /usr/local/bin/
	@echo "✅ 安装完成: /usr/local/bin/$(BINARY_NAME)"

# 卸载
uninstall:
	@echo "🗑️  卸载..."
	@rm -f /usr/local/bin/$(BINARY_NAME)
	@echo "✅ 卸载完成"

# 打包发布
release:
	@echo "📦 打包发布版本..."
	@mkdir -p release
	# Linux
	GOOS=linux GOARCH=amd64 $(GO) build -o release/$(BINARY_NAME)-linux-amd64 cmd/main.go
	# macOS
	GOOS=darwin GOARCH=amd64 $(GO) build -o release/$(BINARY_NAME)-darwin-amd64 cmd/main.go
	GOOS=darwin GOARCH=arm64 $(GO) build -o release/$(BINARY_NAME)-darwin-arm64 cmd/main.go
	# Windows
	GOOS=windows GOARCH=amd64 $(GO) build -o release/$(BINARY_NAME)-windows-amd64.exe cmd/main.go
	@echo "✅ 打包完成: release/"

# 帮助
help:
	@echo "可用命令:"
	@echo "  make deps       - 安装依赖"
	@echo "  make build      - 构建项目"
	@echo "  make clean      - 清理构建文件"
	@echo "  make test       - 运行测试"
	@echo "  make fmt        - 格式化代码"
	@echo "  make lint       - 检查代码"
	@echo "  make run        - 运行（开发模式）"
	@echo "  make run-prod   - 运行（生产模式）"
	@echo "  make install    - 安装到系统"
	@echo "  make uninstall  - 卸载"
	@echo "  make release    - 打包发布版本"
	@echo "  make help       - 显示帮助"
