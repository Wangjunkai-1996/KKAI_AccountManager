# OpenAI 自动登录工具

自动登录 OpenAI 账号并生成 sub2api 可导入的 JSON 格式。

> 新对话请先读取 `DOCS_INDEX.md`、`NEW_CHAT_CONTEXT.md` 和 `SYS1_DEPLOYMENT.md`。当前 sys1 已部署 `openai-login.service`；服务健康不等于真实 OAuth 或账号恢复链路验收通过。

## ⚠️ 重要警告

**本工具仅供技术研究和学习使用！**

- ❌ 违反 OpenAI 服务条款
- ❌ 存在账号被封禁的风险
- ❌ 所有风险由使用者自行承担
- ❌ 不建议用于生产环境

**使用本工具即表示你已了解并接受上述风险！**

## 功能特性

- ✅ 自动登录 OpenAI（支持 2FA）
- ✅ 提取 access_token 和 refresh_token
- ✅ 生成 sub2api 标准导入格式
- ✅ 支持批量处理多个账号
- ✅ 批量登录结果导入 Sub2 时等待并展示每个任务的终态
- ✅ 自动重试机制
- ✅ 独立浏览器会话与登录超时控制
- ✅ 支持代理配置

## 安装

### 1. 安装依赖

需要 Go 1.24+、Node.js 20+ 和 Google Chrome。下面的脚本安装与项目依赖匹配的 Playwright driver；运行时使用已安装的 Google Chrome。

```bash
# 进入桌面项目目录
cd /Users/tokk/Desktop/KKAI_AUTH

# 安装 Go 依赖
go mod download

# 安装与当前 Go 依赖匹配的 Playwright driver（使用本机 Google Chrome）
./install-browser.sh
```

### 2. 编译

```bash
# 编译 CLI 兼容二进制
mkdir -p bin
go build -o bin/openai-login cmd/main.go

# 编译当前 Web 服务
go build -o bin/openai-login-web ./cmd/server

# 或者使用 Makefile
make build
```

## 使用方法

### Mac 网页版

在本工具目录运行 `./start-local.sh`，打开 `http://127.0.0.1:8080`。
账号框每行填写 `email---password---totp_secret` 或 `email----password----totp_secret`，命令行账号文件也支持两种格式。默认逐个处理账号；可切换为并发处理，或调整为 1–10 个并发。确认单账号稳定后再提高并发。

- 只使用本机 Clash：HTTP 代理填 `http://127.0.0.1:7897`。
- 使用带用户名密码的 HTTP 节点：HTTP 代理填 `http://user:password@proxy.example.com:8080`。
- 本机无法直接连接该节点时，程序会在检测到连接被重置且 Clash `127.0.0.1:7897` 可用时自动中转；最终出口仍是前一栏的节点。

登录完成会自动确认 Codex 授权，并返回 access token 和 refresh token。成功后可点击“导出结果”，也可从批量登录结果直接导入 Sub2。批量登录结果导入会轮询已受理任务，直到显示成功、失败或仍在队列中的终态摘要，不把“已受理”误报为“已完成”。
需要重新编译时，先在旧服务终端按 Ctrl+C，再运行 `./start-local.sh`；不要同时启动两个占用 8080 端口的服务。

网页端会把账号状态和每次登录尝试保存到本地 `data/accounts.db`。密码、TOTP、代理凭据和 OAuth token 使用独立的 AES-GCM 密钥加密保存；历史接口和页面只展示邮箱、状态、错误摘要、次数和时间。刷新页面后可在“账号历史”中搜索、筛选、排序、分页、查看脱敏详情，也可以通过统一勾选批量复活、导入或删除。全选覆盖当前页所有状态，翻页或筛选后保留选择，并提示不在当前页的已选数量；可一键清空。复活与导入只处理符合条件的账号，跳过项或失败项保留选中。批量删除需确认数量，只清理 AUTH 本地记录和凭据，不删除已导入 Sub2 的账号；正在登录、导入中或导入待核对的记录会保留并提示原因。输入账号会先检查格式、邮箱、密码长度和重复项；存在错误或重复时不会开始处理。明确判定为官方删除的账号会自动从数据库清理，停用或普通 403 会保留以便重试。

可用 `-history-db`、`-history-key` 或环境变量 `OPENAI_LOGIN_DB`、`OPENAI_LOGIN_KEY` 指定稳定的数据目录。线上服务应把它们指向服务用户可写、release 目录之外的路径，例如 `/var/lib/openai-login/data/accounts.db` 和 `/var/lib/openai-login/data/accounts.key`。

认证成功的历史账号可以在页面中选择“导入到 Sub2 未分组”。启用此功能前，在 AUTH 服务进程环境中设置 `SUB2API_BASE_URL`（Sub2 站点根地址）和 `SUB2API_ADMIN_API_KEY`（Sub2 管理员 API Key）；可选的 `AUTH_INSTANCE_ID` 用于区分多个 AUTH 实例。管理密钥只由后端读取，浏览器只提交本地账号 ID。导入和恢复任务调用 Sub2 现有管理接口，不要求修改 Sub2 源码。新建 Sub2 账号名称使用 `AUTH_MMDDHHmm_email`（按服务端 Asia/Shanghai 时间生成），便于从名称识别来源；名称不会替代来源标记或身份核对。

账号历史刷新时读取 Sub2 当前状态；可见页面在任务进行中每 5 秒更新，自动恢复开启且空闲时每 60 秒更新，关闭且空闲时每 5 分钟更新。没有导入绑定时，按 Sub2 凭据中的邮箱精确匹配，并核对已有的 ChatGPT 账号 ID；唯一匹配后保存本地关联，支持从其他工具导入、使用自定义名称的账号，不会重复创建 Sub2 账号或改变其分组。匹配歧义、身份冲突和网络异常显示具体原因；只有已关联账号明确返回 404 才显示已删除。状态同步保留 Sub2 原始 `schedulable` 与 `effective_schedulable` 字段，并显示当前实际可调度性；未来的限流窗口、上游过载窗口和临时暂停截止时间/原因也会在历史行中展示。

开启历史页的“自动检测恢复”后，后端立即巡检，此后每 60 秒读取 Sub2 状态，关闭页面后仍继续。对仍在池中且未人工暂停的账号，只有 AUTH 当前检测确认 HTTP 401、令牌过期、凭据失效或本地缺少令牌后，才直接调用 AUTH 重新登录、写回原 Sub2 账号、验证并恢复调度，不走 Refresh Token 优先路径。Sub2 `status=error` 或 `schedulable=false` 本身不会证明 401；AUTH 检测的 401 来自 AUTH 本地凭据，和 Sub2 自身凭据/调度状态是两条独立证据。即使 Sub2 显示正常，也会保留“本地凭据失效待处理”并允许恢复；正常、禁用、已删除、状态未知和人工暂停的账号不会仅凭 Sub2 状态恢复。页面显示巡检时间、结果摘要、检测和恢复进度。开关保存到 SQLite，重启后保留；尚未保存时才使用 `AUTH_AUTO_RECOVERY` 启动默认值（默认关闭）。

历史账号旁的“检测并恢复”可手动启动同一流程，无需先开启自动恢复；已有可继续的任务显示“继续恢复”。“重新登录”仅更新 AUTH 本地凭据，不写回 Sub2。自动巡检复用检测冷却和任务去重；恢复任务结束后冷却 30 分钟，同一凭据版本在 24 小时内恢复失败 3 次后暂停自动重试，仍可人工处理。Sub2 的异常状态本身不能证明 401，AUTH 检测正常时不会强行重新登录；异常加暂停的原因仍依赖 Sub2 状态，不能完全区分人工暂停与自动暂停。

AUTH 默认只监听 `127.0.0.1`。如果通过反向代理或 `-bind` 暴露到其他网络，必须在网关层为 `/api/history`、`/api/sub2/import*` 和 `/api/account-recovery*` 增加管理认证；CORS 不是访问控制，不能把管理 API Key 放进网页代码。

### sys1 网页版

公网入口为 `https://auth.kkrich.ltd`，使用平台入口用户名和密码进入。当前 sys1 IPv4 直连在 OAuth 授权页仍可能收到 Cloudflare `403 cf-mitigated: challenge`；从 sys1 使用指定 HTTP 代理节点已完成真实账号连续登录验收。需要指定节点时，在页面唯一的 HTTP 代理输入框填写节点地址。开始处理前会按邮箱自动去重，重复账号只执行第一条。线上服务、release、回滚和验收记录见 `SYS1_DEPLOYMENT.md`。

当前线上 release 为 `20261005T124744Z-sub2-auth401-recovery`（commit `4591e80`，二进制 SHA-256 `d869c0c16498a8ab362e0be78c3aff617eec0ab96a469244244cd49099006714`），回滚为 `20261005T105500Z-sub2-status-ui`。发布前备份为 `/var/lib/openai-login/backups/accounts-before-20261005T124744Z-sub2-auth401-recovery.db`。发布后服务保持 `active/running`、`NRestarts=0`，`/health` 返回 `status: ok`，有效入口 socket 返回 HTTP 200，公网未认证返回 HTTP 401。候选验证时自动恢复关闭，生产自动恢复 `enabled` 且开关保持开启；线上快照为 history 31、imports 27、statuses 31。账号 117/118/119 的 AUTH current 检测均为 HTTP 401 `credential_revoked`，Sub2 为 `active` 且 `schedulable=false`、`effective_schedulable=false`，自动恢复任务为 `unknown`，保持停止调度。

sys1 的部署方式是独立运行本工具、Google Chrome 和 Xvfb。Xvfb 提供虚拟显示环境，让服务器运行有头 Chrome；无需打开公开管理端口，也无需安装桌面环境。该工具独立于 Sub2API 服务。

在 Mac 的本工具目录运行：

```bash
./open-sys1.sh
```

保持终端开启，然后在 Mac 浏览器访问 `http://127.0.0.1:18082`。页面和 API 通过 SSH 隧道连接 sys1；Chrome 登录流程在 sys1 上执行。使用完毕可在终端按 Ctrl+C 关闭隧道。

页面可以留空代理，让 Chrome 使用 sys1 的 IPv4 出口；当前验收中该出口在授权阶段收到 challenge。需要稳定完成登录时只填写一个能从 sys1 访问的 HTTP 代理地址；`127.0.0.1` 指 sys1 本身，不是 Mac 上的 Clash。

当前线上页面留空代理时使用 sys1 配置的默认认证代理；如需临时更换出口，直接在页面输入框填写其他 HTTP 代理即可。

本地启动脚本默认使用实际 Chrome 的原生 User-Agent 和 Client Hints；如需复现实验性的 Mac 兼容配置，可显式传入 `-browser-compat=true`。该配置只影响新建浏览器会话，不改变代理地址或账号数据。

每次登录均新建独立 Chrome 和 Playwright `BrowserContext`（无痕上下文），账号之间不共享 Cookie 或本地存储，结束后关闭会话，不持久化 Cookie。无痕上下文支持并发隔离，但 Linux 与 Mac 的浏览器环境和网络出口仍可能不同，不能据此保证两端登录结果相同。

### 网页版并发、超时与错误处理

- 并发模式默认 1 个账号，页面允许 1–10；后端通过 `-max-concurrent` 提供跨页面共享的硬上限，页面会从 `/health` 读取并自动遵守。确认稳定后再提高服务端上限；满额时返回 HTTP `429` 和 `Retry-After: 5`。
- 单个账号默认有 3 分钟总超时，覆盖浏览器操作、重试等待、代理回退和 token 交换；超过总期限后取消操作并清理浏览器，API 返回 HTTP `504`。
- 捕获到 OpenAI 认证请求的 HTTP 错误后，`402`、`403` 等非 `429` 的 `4xx` 按失败结束，API 保留对应状态码。`429` 和 `5xx` 可重试，默认最多重试 2 次，且仍受总超时约束。网络、页面流程及识别到的 challenge 会给出相应错误。
- 每个客户端 IP 默认每 10 分钟最多 30 次请求。经 SSH 隧道访问时，请求共用服务器看到的本地客户端 IP。

### 实时进度、代理测试与失败重试

- 每个账号实时显示当前步骤、步骤耗时和总耗时；失败时显示错误阶段、HTTP 状态和错误码。进度通过 SSE 推送，关闭页面会取消当前请求并清理浏览器。
- 收到明确的账号删除/停用业务码或认证页提示时，显示“账号已删除”“账号已停用”或“账号已删除或停用”，并停止自动重试。保留上游业务码和实际 HTTP 状态；普通 403 不代表账号被删。页面提示没有 HTTP 失败状态时不会编造状态码，`account_unavailable` 是工具对明确页面提示使用的本地分类码。
- 登录成功表示取得 OAuth token，不等于已验证后续模型调用权限。账号检测可手动发起，也可由已开启的自动恢复针对 Sub2 异常账号触发；不会在每次登录后自动发送模型请求。
- 失败账号旁点击“重试”可单独重试；点击“重试全部失败”只重新处理失败账号，保留成功结果。每批开始时固定当前代理设置，执行中修改输入框不会改变该批出口。
- 点击“测试代理”检查当前 HTTP 代理；代理为空时检查直连。结果显示“网络可达”只代表能够连接认证站，HTTP 403 仍可能导致实际登录失败。
- 平台并发已满或平台请求达到上限时，页面显示倒计时并在等待后继续。上游认证错误单独处理：429/5xx 按 Retry-After 或有界退避重试；普通 4xx、浏览器验证和连接重置不会反复自动重试。
- 代理测试使用独立请求额度，不消耗账号登录额度。每个登录请求的重试仍受 3 分钟总超时约束，平台排队时间单独计算。

Web 服务支持 `-max-concurrent`（1–10，默认 `1`）、`-login-timeout`（默认 `3m`）和 `-rate-limit`（默认 `30`）参数。监听地址默认 `127.0.0.1`；Linux 默认不自动打开管理页面。

### 账号状态检测

“账号检测”Tab 保留为批量检测和详情页：可搜索或筛选已导入或已关联 Sub2 的 AUTH 账号，单个点击“检测状态”，或勾选后批量开始。历史页另提供“立即检测”快捷入口，可按当前筛选或选择提交检测，但结果详情和批次进度仍在账号检测 Tab 查看。全选只选当前页，翻页保留选择；每批最多 100 个，检测并发可选 1/2，与登录并发独立。

默认通过 AUTH 服务器代理发送一次固定 `gpt-5.6-luna` 小请求；也可明确选择“sys1 IPv4 直连”。默认代理未配置会拒绝默认模式，代理失败不会转为直连。检测使用 AUTH 保存的当前 AT，不能代表 Sub2 当前凭据/调度状态；不会调用 Sub2 检测接口、刷新共享 RT 或自动删除账号。

页面分别显示正常、实际 HTTP 401、访问令牌过期、限流/额度、模型权限、代理/网络错误等。AT 在本地已经明确过期时不发模型请求；重新登录更换凭据后，上次结论标记过时。HTTP 200 还需完整模型完成事件，才会显示正常。

检测在后台执行，关闭或刷新页面后可以恢复；停止操作会取消未执行和进行中的检测，保留已完成结果。请求已发到上游时，取消不保证没有用量。进度按已结束项计算，不将取消/跳过显示成成功。

实施及验证记录见 [ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。

## CLI 兼容参考

下面的账号文件和 CLI 参数仍可用于本地兼容流程，但当前推荐入口是上面的 Web 版：本地使用 `./start-local.sh`，sys1 使用 `./open-sys1.sh`。CLI 示例不能代表 sys1 的线上端口、release 或当前登录验收状态。

### 1. 准备账号文件

创建 `accounts.txt` 文件，每行一个账号，支持 3 个或 4 个短横线分隔，格式：

```
email----password----totp_secret
```

示例：

```txt
# 这是注释
user1@example.com----Password123----ABCD1234EFGH5678
user2@example.com----SecurePass456----IJKL9012MNOP3456
```

### 2. 运行工具

**基础用法：**

```bash
./openai-login -input accounts.txt -output sub2api-accounts.json
```

**完整参数：**

```bash
./openai-login \
  -input accounts.txt \              # 输入文件
  -output sub2api-accounts.json \    # 输出文件
  -headless=true \                   # 无头模式
  -proxy http://127.0.0.1:7890 \     # 代理（可选）
  -retry 2 \                         # 重试次数
  -delay-min 5 \                     # 最小延时（秒）
  -delay-max 10 \                    # 最大延时（秒）
  -timeout 60                        # 单步骤超时时间（秒），仍受总超时约束
```

**调试模式（显示浏览器）：**

```bash
./openai-login -input accounts.txt -headless=false
```

### 3. 导入到 sub2api

**方法 1：通过管理后台**

1. 登录 sub2api 管理后台
2. 进入"账号管理"页面
3. 点击"导入"按钮
4. 选择生成的 JSON 文件

**方法 2：通过 API**

```bash
curl -X POST http://your-sub2api:8080/api/v1/admin/accounts/import \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer YOUR_TOKEN" \
  -d @sub2api-accounts.json
```

## 输出格式

生成的 JSON 文件格式示例：

下面的密码、TOTP 和 token 只是占位符。真实凭据不要写入文档、日志或未加密文件。

```json
{
  "type": "sub2api-data",
  "version": 1,
  "exported_at": "2026-10-01T15:30:00Z",
  "proxies": [],
  "accounts": [
    {
      "name": "AUTH_10011530_user@example.com",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "eyJhbGc...",
        "refresh_token": "rt.1.AAA...",
        "chatgpt_account_id": "31aea8ba-...",
        "organization_id": "org-...",
        "expires_at": 1791624659,
        "expires_in": 863925,
        "plan_type": "business"
      },
      "extra": {
        "email": "user@example.com",
        "display_name": "AUTH_10011530_user@example.com",
        "openai_passthrough": false,
        "recovery": {
          "email": "user@example.com",
          "login_password": "<redacted>",
          "totp_secret": "<redacted>",
          "credential_line": "user@example.com----<redacted>----<redacted>"
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

## 常见问题

### 1. 登录失败

**可能原因：**
- CAPTCHA 验证
- IP 被限制
- 密码或 2FA 错误
- 网络问题

**解决方法：**
- 使用代理（住宅代理最佳）
- 减少并发和增加延时
- 检查账号凭据是否正确
- 使用 `-headless=false` 查看浏览器窗口

### 2. TOTP 验证码错误

**可能原因：**
- 密钥格式错误
- 系统时间不准确

**解决方法：**
- 确保密钥格式正确（Base32，无空格）
- 同步系统时间：`ntpdate -u time.apple.com`

### 3. 触发 Cloudflare

**一般排查：**
- 使用代理
- 增加延时间隔
- 降低并发数
- 使用住宅 IP

**当前 sys1 实测：**

`auth.openai.com/oauth/authorize` 可能在输入邮箱前返回 HTTP `403`，并带有 `cf-mitigated: challenge`。这时页面提前结束不代表密码错误或账号已删除；单纯增加重试不能保证通过，需要记录出口、浏览器启动参数和授权响应后继续排查。详见 `SYS1_DEPLOYMENT.md`。

### 4. Playwright 安装失败

```bash
# 在本工具目录安装与项目依赖匹配的 driver
./install-browser.sh
```

运行时还需要 Google Chrome。服务器采用有头模式时，同时需要可用的 Xvfb 显示环境。不要另装其他版本的 Playwright driver，否则可能与本项目的 Go 依赖不匹配。

## 安全建议

1. **不要在服务器上存储明文密码**
2. **立即修改已暴露的凭据**
3. **使用独立的 OpenAI 账号（不要用主账号）**
4. **定期轮换 Token**
5. **使用加密存储凭据**

## 开发

```bash
# 定向测试（按变更范围选择；不要把旧文档中的全仓库测试当作默认步骤）
go test ./internal/login ./cmd/server ./internal/store ./cmd -count=1 -timeout=90s

# 格式化代码
gofmt -w .

# 构建
make build

# 清理
make clean
```

## 许可证

本工具仅供学习研究使用，使用本工具产生的任何后果由使用者自行承担。

## 免责声明

- 本工具违反 OpenAI 服务条款
- 可能导致账号被封禁
- 作者不承担任何法律责任
- 使用即表示接受所有风险

---

**再次提醒：仅供技术研究，风险自负！**
