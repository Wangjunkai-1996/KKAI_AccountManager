# 🎯 如何使用 OpenAI 自动登录工具

> 历史兼容说明：本文保留早期 CLI/Web 启动示例。当前桌面路径、sys1 部署、端口、账号历史和 Cloudflare 验收状态以 `DOCS_INDEX.md`、`NEW_CHAT_CONTEXT.md`、`README.md` 和 `SYS1_DEPLOYMENT.md` 为准。本文中的旧源码路径和旧参数不要直接用于线上发布。

## 📍 文档定位

本文是早期 CLI 使用稿，保留旧版构建和导出示例。它不反映当前 sys1 的 release、服务健康或 OAuth 验收状态；新对话和线上排查请以 `DOCS_INDEX.md`、`NEW_CHAT_CONTEXT.md`、`README.md` 和 `SYS1_DEPLOYMENT.md` 为准。

## 🚀 快速开始（3步）

### 第 1 步：打开你的终端

打开 macOS 的 **终端（Terminal）** 应用，然后进入工具目录：

```bash
cd /Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/tools/openai-login
```

### 第 2 步：准备账号文件

创建 `accounts.txt` 文件，格式为：

```txt
email----password----totp_secret
```

例如：

```bash
cat > accounts.txt << 'EOF'
your-email@example.com----YourPassword----YOUR_TOTP_SECRET_BASE32
EOF
```

**重要提示：**
- 三个字段用 `----`（4个短横线）分隔
- TOTP 密钥必须是 Base32 格式（大写字母 + 数字2-7）
- 一行一个账号

### 第 3 步：运行工具

```bash
# 方法 1：使用自动化脚本（推荐）
chmod +x run.sh
./run.sh

# 方法 2：手动命令
go mod download
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install chromium
go build -o bin/openai-login ./cmd/main.go
./bin/openai-login -input accounts.txt -output result.json
```

## 📋 完整命令示例

### 基础使用

```bash
# 后台运行（推荐）
./bin/openai-login -input accounts.txt -output result.json

# 显示浏览器窗口（调试用）
./bin/openai-login -input accounts.txt -headless=false
```

### 高级参数

```bash
./bin/openai-login \
  -input accounts.txt \
  -output result.json \
  -headless=true \
  -retry=3 \
  -delay-min=10 \
  -delay-max=20 \
  -timeout=120 \
  -proxy=http://127.0.0.1:7890
```

### 参数说明

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-input` | accounts.txt | 输入文件 |
| `-output` | sub2api-accounts.json | 输出文件 |
| `-headless` | true | 无头模式（false=显示浏览器） |
| `-retry` | 2 | 失败重试次数；`0` 关闭自动重试 |
| `-delay-min` | 5 | 最小延时（秒） |
| `-delay-max` | 10 | 最大延时（秒） |
| `-timeout` | 60 | 超时时间（秒） |
| `-proxy` | "" | 代理地址 |

## 📊 输出结果

运行成功后，会生成 `sub2api-accounts.json` 文件，格式为：

```json
{
  "type": "sub2api-data",
  "version": 1,
  "exported_at": "2026-10-01T...",
  "proxies": [],
  "accounts": [
    {
      "name": "时间戳----email",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "eyJhbGc...",
        "refresh_token": "rt.1.AAA...",
        "chatgpt_account_id": "...",
        "organization_id": "...",
        "expires_at": 1791624659,
        "expires_in": 863925,
        "plan_type": "business"
      },
      "extra": {
        "email": "...",
        "recovery": {
          "email": "...",
          "login_password": "...",
          "totp_secret": "...",
          "credential_line": "..."
        }
      }
    }
  ]
}
```

## 🔧 导入到 sub2api

### 方法 1：API 导入

```bash
curl -X POST http://localhost:8080/api/v1/admin/accounts/import \
  -H "Content-Type: application/json" \
  -d @sub2api-accounts.json
```

### 方法 2：管理后台导入

1. 登录 sub2api 管理后台
2. 进入 "账号管理"
3. 点击 "导入"
4. 选择生成的 JSON 文件
5. 确认导入

## ⚠️ 常见问题

### Q1: Go 命令找不到？

```bash
# 检查 Go 是否安装
which go
go version

# 如果没有安装
brew install go
```

### Q2: 浏览器安装失败？

```bash
# 手动安装 Playwright 浏览器
go run github.com/playwright-community/playwright-go/cmd/playwright@latest install chromium
```

### Q3: TOTP 验证码错误？

```bash
# 检查系统时间是否同步
sudo ntpdate -u time.apple.com

# TOTP 密钥必须是正确的 Base32 格式
```

### Q4: 登录失败（Cloudflare/CAPTCHA）？

```bash
# 使用代理
./bin/openai-login -input accounts.txt -proxy http://proxy:8080

# 增加延时
./bin/openai-login -input accounts.txt -delay-min 15 -delay-max 30

# 显示浏览器窗口调试
./bin/openai-login -input accounts.txt -headless=false
```

## 📁 项目结构

```
tools/openai-login/
├── run.sh                      ← 一键启动脚本
├── accounts.txt                ← 你的账号文件（需要创建）
├── sub2api-accounts.json       ← 输出文件（自动生成）
├── cmd/main.go                 ← 主程序
├── internal/
│   ├── login/                  ← 登录服务
│   └── exporter/               ← JSON 导出器
├── go.mod                      ← Go 模块
├── Makefile                    ← 构建脚本
├── README.md                   ← 基础说明
└── GUIDE.md                    ← 完整指南
```

## 🎯 总结

**工具已经完成，现在可以用了！**

只需要在**你的 macOS 终端**中运行：

```bash
cd /Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/tools/openai-login
chmod +x run.sh
./run.sh
```

脚本会自动：
1. ✅ 检查 Go 环境
2. ✅ 下载依赖
3. ✅ 安装浏览器
4. ✅ 编译程序
5. ✅ 运行工具
6. ✅ 生成 JSON 文件

**一键完成所有步骤！** 🚀
