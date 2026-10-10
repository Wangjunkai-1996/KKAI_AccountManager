# OpenAI 自动登录工具 - 完整使用指南

> CLI 历史参考：当前新对话和 sys1 操作以 `DOCS_INDEX.md`、`NEW_CHAT_CONTEXT.md`、`README.md` 和 `SYS1_DEPLOYMENT.md` 为准。本文中的通用路径、并发示例和旧 Playwright 版本不是线上部署基线。

## 快速开始

### 1. 安装

```bash
# 克隆项目
cd /path/to/sub2api/tools/openai-login

# 安装依赖
make deps

# 构建
make build
```

### 2. 准备账号文件

创建 `accounts.txt`：

```txt
email1@example.com----password1----TOTP_SECRET_1
email2@example.com----password2----TOTP_SECRET_2
```

### 3. 运行

```bash
# 基础运行
./bin/openai-login -input accounts.txt -output result.json

# 使用代理
./bin/openai-login -input accounts.txt -proxy http://127.0.0.1:7890

# 调试模式（显示浏览器）
./bin/openai-login -input accounts.txt -headless=false
```

## 完整参数说明

| 参数 | 默认值 | 说明 |
|------|--------|------|
| `-input` | accounts.txt | 输入文件路径 |
| `-output` | sub2api-accounts.json | 输出文件路径 |
| `-headless` | true | 是否使用无头模式 |
| `-proxy` | "" | 代理地址（可选） |
| `-retry` | 2 | 失败重试次数；`0` 关闭自动重试 |
| `-delay-min` | 5 | 最小延时（秒） |
| `-delay-max` | 10 | 最大延时（秒） |
| `-timeout` | 60 | 超时时间（秒） |

## 使用场景

### 场景 1：批量获取新账号 Token

```bash
# 1. 准备账号列表
cat > new-accounts.txt << EOF
user1@gmail.com----Pass1234----ABCD1234EFGH5678
user2@gmail.com----Pass5678----IJKL9012MNOP3456
EOF

# 2. 运行工具
./bin/openai-login -input new-accounts.txt -output new-tokens.json

# 3. 导入到 sub2api
curl -X POST http://localhost:8080/api/v1/admin/accounts/import \
  -H "Content-Type: application/json" \
  -d @new-tokens.json
```

### 场景 2：使用代理避免 IP 限制

```bash
# 使用本地代理
./bin/openai-login \
  -input accounts.txt \
  -proxy http://127.0.0.1:7890

# 使用远程代理
./bin/openai-login \
  -input accounts.txt \
  -proxy http://proxy-server:8080
```

### 场景 3：调试登录问题

```bash
# 显示浏览器窗口，手动观察登录过程
./bin/openai-login \
  -input single-account.txt \
  -headless=false \
  -timeout 120
```

### 场景 4：大批量处理（降低风险）

```bash
# 增加延时，降低并发
./bin/openai-login \
  -input large-batch.txt \
  -delay-min 10 \
  -delay-max 20 \
  -retry 3 \
  -proxy http://proxy:8080
```

## 常见问题排查

### 问题 1：所有账号都登录失败

**可能原因：**
- Cloudflare 拦截
- IP 被限制
- 网络问题

**排查步骤：**

```bash
# 1. 使用调试模式查看浏览器
./bin/openai-login -input accounts.txt -headless=false

# 2. 检查网络连接
curl -I https://auth.openai.com

# 3. 使用代理
./bin/openai-login -input accounts.txt -proxy http://proxy:8080
```

### 问题 2：TOTP 验证码错误

**可能原因：**
- 密钥格式错误
- 系统时间不准

**解决方法：**

```bash
# 1. 验证密钥格式（必须是 Base32）
echo "KV45MJKJI6C7LGZ7EAEC373CWY2BLVE2" | base32 -d

# 2. 同步系统时间
sudo ntpdate -u time.apple.com
# 或者
sudo timedatectl set-ntp true
```

### 问题 3：浏览器启动失败

```bash
# 重新安装 Playwright 浏览器
go run github.com/mxschmitt/playwright-go/cmd/playwright@v0.6201.1 install --with-deps chromium

# 检查浏览器是否安装成功
ls ~/.cache/ms-playwright/chromium-*/chrome-linux/chrome
```

### 问题 4：部分账号成功，部分失败

这是正常情况，可能原因：
- 密码错误
- 2FA 密钥错误
- 账号已被封禁
- 临时网络问题

**查看失败原因：**

工具会在输出中显示每个账号的失败原因。

## 高级技巧

### 技巧 1：分批处理账号

```bash
# 将大文件拆分成小文件
split -l 10 accounts.txt batch-

# 逐个处理
for file in batch-*; do
    ./bin/openai-login -input "$file" -output "result-$file.json"
    sleep 60  # 每批次之间暂停 60 秒
done

# 合并结果
jq -s '.[0] + {accounts: ([.[].accounts] | add)}' result-*.json > final.json
```

### 技巧 2：从 CSV 转换账号格式

```bash
# 假设 CSV 格式：email,password,totp_secret
awk -F',' '{print $1"----"$2"----"$3}' accounts.csv > accounts.txt
```

### 技巧 3：验证生成的 Token

```bash
# 提取第一个 access_token
ACCESS_TOKEN=$(jq -r '.accounts[0].credentials.access_token' result.json)

# 测试 Token 是否有效
curl https://api.openai.com/v1/models \
  -H "Authorization: Bearer $ACCESS_TOKEN"
```

### 技巧 4：定时自动刷新 Token

```bash
# 创建 cron 任务，每天自动刷新
cat > /etc/cron.daily/refresh-openai-tokens << 'EOF'
#!/bin/bash
cd /path/to/sub2api/tools/openai-login
./bin/openai-login -input accounts.txt -output tokens-$(date +%Y%m%d).json
EOF

chmod +x /etc/cron.daily/refresh-openai-tokens
```

## 输出文件说明

### 成功的输出示例

```json
{
  "type": "sub2api-data",
  "version": 1,
  "exported_at": "2026-10-01T15:30:00Z",
  "proxies": [],
  "accounts": [
    {
      "name": "2026-10-01_15:30:00----user@example.com",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "eyJhbGc...",
        "refresh_token": "rt.1.AAA...",
        "chatgpt_account_id": "31aea8ba-6b5e-434c-b071-3adc3e4ae25a",
        "organization_id": "org-...",
        "expires_at": 1791624659,
        "expires_in": 863925,
        "plan_type": "business"
      },
      "extra": {
        "email": "user@example.com",
        "display_name": "...",
        "openai_passthrough": false,
        "recovery": {
          "email": "user@example.com",
          "login_password": "password",
          "totp_secret": "TOTP_SECRET",
          "credential_line": "user@example.com----password----TOTP_SECRET"
        }
      },
      "concurrency": 100,
      "priority": 1,
      "rate_multiplier": 1,
      "auto_pause_on_expired": true,
      "plan_type": "business",
      "expires_at": 1793443200
    }
  ]
}
```

### 字段说明

| 字段 | 说明 |
|------|------|
| `access_token` | OpenAI API 访问令牌 |
| `refresh_token` | 用于刷新 access_token |
| `chatgpt_account_id` | ChatGPT 账户 ID |
| `organization_id` | 组织 ID |
| `expires_at` | Token 过期时间（Unix 时间戳） |
| `expires_in` | Token 有效期（秒） |
| `plan_type` | 计划类型（free/plus/business） |
| `recovery` | 恢复凭据（用于重新登录） |

## 安全建议

### 1. 凭据管理

```bash
# 不要将账号文件提交到 Git
echo "accounts.txt" >> .gitignore
echo "*.json" >> .gitignore

# 设置严格的文件权限
chmod 600 accounts.txt
chmod 600 *.json
```

### 2. 密钥加密存储

```bash
# 使用 GPG 加密账号文件
gpg -c accounts.txt

# 使用时解密
gpg -d accounts.txt.gpg > accounts.txt
./bin/openai-login -input accounts.txt
rm accounts.txt  # 使用后立即删除
```

### 3. 定期轮换凭据

- 每 30 天更换密码
- 重新生成 2FA 密钥
- 定期检查账号状态

## 性能优化

### 优化 1：使用本地代理池

```bash
# 准备多个代理
proxies=(
    "http://proxy1:8080"
    "http://proxy2:8080"
    "http://proxy3:8080"
)

# 为每批账号使用不同代理
proxy_index=0
for file in batch-*; do
    proxy=${proxies[$proxy_index]}
    ./bin/openai-login -input "$file" -proxy "$proxy"
    proxy_index=$(( (proxy_index + 1) % ${#proxies[@]} ))
done
```

### 优化 2：并行处理（谨慎使用）

```bash
# 最多 3 个并行任务
parallel -j 3 './bin/openai-login -input {} -output {.}.json' ::: batch-*
```

## 故障恢复

### 恢复中断的批处理

```bash
# 检查哪些账号已处理
processed=$(jq -r '.accounts[].extra.email' result-*.json | sort -u)

# 生成未处理的账号列表
comm -23 <(cut -d'-' -f1 accounts.txt | sort) <(echo "$processed") > remaining.txt

# 继续处理
./bin/openai-login -input remaining.txt -output remaining-result.json
```

## 监控和日志

### 保存日志

```bash
# 保存详细日志
./bin/openai-login -input accounts.txt 2>&1 | tee login-$(date +%Y%m%d-%H%M%S).log
```

### 统计分析

```bash
# 统计成功率
total=$(wc -l < accounts.txt)
success=$(jq '.accounts | length' result.json)
echo "成功率: $(( success * 100 / total ))%"

# 统计各计划类型数量
jq '.accounts | group_by(.plan_type) | map({plan: .[0].plan_type, count: length})' result.json
```

## 开发和贡献

### 运行测试

```bash
make test
```

### 代码格式化

```bash
make fmt
```

### 代码检查

```bash
make lint
```

## 许可和免责

⚠️ **重要提醒**

- 本工具违反 OpenAI 服务条款
- 可能导致账号被封禁
- 仅供技术研究使用
- 使用者自行承担所有风险和法律责任

---

**最后提醒：谨慎使用，风险自负！**
