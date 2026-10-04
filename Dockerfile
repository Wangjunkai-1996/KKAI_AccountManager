# 使用 Golang 官方镜像
FROM golang:1.24-bookworm

# 避免交互式提示
ENV DEBIAN_FRONTEND=noninteractive

# 安装 Playwright 依赖
RUN apt-get update && \
    apt-get install -y \
    libnss3 \
    libnspr4 \
    libatk1.0-0 \
    libatk-bridge2.0-0 \
    libcups2 \
    libdrm2 \
    libdbus-1-3 \
    libxkbcommon0 \
    libxcomposite1 \
    libxdamage1 \
    libxfixes3 \
    libxrandr2 \
    libgbm1 \
    libpango-1.0-0 \
    libcairo2 \
    libasound2 \
    libatspi2.0-0 \
    && rm -rf /var/lib/apt/lists/*

# 设置工作目录
WORKDIR /app

# 复制 go.mod 和 go.sum
COPY go.mod go.sum ./

# 下载依赖
RUN go mod download

# 复制源代码
COPY . .

# 安装 Playwright 浏览器驱动（关键步骤）
RUN go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install --with-deps chromium

# 编译
RUN go build -o openai-login-web ./cmd/server

# 暴露端口
EXPOSE 8080

# 启动服务
CMD ["./openai-login-web"]
