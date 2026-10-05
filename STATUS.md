# KKAI_AUTH 当前状态

更新时间：2026-10-05（Asia/Shanghai）

## 当前结论

项目源码在 `/Users/tokk/Desktop/KKAI_AUTH`。批量登录结果导入 Sub2、Sub2 状态同步和自动恢复开关已于北京时间 2026-10-05 10:13:47 部署到 sys1，即时及延迟健康检查、有效路由和回滚准备均通过；Sub2 源码、镜像和线上版本未改。本轮尚未进行真实 OAuth/401 恢复业务验收。此前 Tab UI 和账号状态检测记录分别见 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md) 和 [ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。

## 历史列表优化（此前已发布）

普通当前页全选、跨页/筛选选择保留、清空选择，以及统一批量复活/导入/删除工具栏。删除按固定账号 ID 执行，确认范围、保留失败项，后端拒绝登录和导入忙碌记录。已部署并通过 sys1 真实 OAuth 登录验收（12.16 秒），42 条账号记录保持不变。

## 线上状态

| 项目 | 当前值 |
|---|---|
| 入口 | `https://auth.kkrich.ltd` |
| 服务 | `openai-login.service` |
| sys1 本机监听 | `127.0.0.1:18082` |
| Mac 隧道 | `./open-sys1.sh` |
| 当前 release | `20261005T020014Z-sub2-sync-auto` |
| 回滚 release | `20261004T164742Z-sub2-recovery` |
| 数据库 | `/var/lib/openai-login/data/accounts.db` |
| 密钥 | `/var/lib/openai-login/data/accounts.key` |
| 最近健康检查 | 2026-10-05 02:17:40 UTC 延迟复查 `active/running`、`NRestarts=0`、`/health = status: ok, max_concurrent: 10` |
| 发布前备份 | `/var/lib/openai-login/backups/accounts-before-20261005T020014Z-sub2-sync-auto.db`，0600，`integrity_check=ok` |
| 当前二进制 SHA-256 | `c7166a6e999eb2eb3c20f78d98944072a13abaf9ae20246d7c1433d6b2c0cbdc` |
| 导入配置 | `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改 |
| 公网身份验证 | 有效入口 Unix socket 健康通过，公网未认证 HTTP 401；未验证认证后公网业务 |
| 本轮功能与 OAuth 验收 | 隔离候选已验证设置 GET/PUT 和只读 Sub2 状态，生产只做 GET 验证；未执行真实 OAuth/401 恢复业务验收 |
| 自动恢复设置 | 当前关闭；运行时开关重启后按 `AUTH_AUTO_RECOVERY` 恢复默认值 |
| sys1 IPv4 模型检测（历史） | 此前真实 HTTP200 / 完整模型完成事件，1,982ms |
| 默认代理模型检测（历史） | 此前单账号连续两次成功；候选两个账号并发2正常，重叠3,035ms |
| 浏览器直连历史限制 | 授权流程曾收到 `403 + cf-mitigated: challenge`；不可将该浏览器结论套用到模型检测 |
| sys1 默认代理 | 节点 `45.39.200.204:7269`；此前完整 OAuth 登录 18.93 秒、模型检测通过，本轮未重测 |

## 目前已实现

- 账号历史、逐次尝试记录和安全重登。
- AES-GCM 加密保存密码、TOTP 和 OAuth token。
- 明确删除账号自动清理；停用、普通 403 和 challenge 保留以便重试。
- `---` / `----` 账号格式、邮箱去重、账号级并发互斥。
- 默认 1 个并发，服务端硬上限可配置为 1–10 个；单账号总超时默认 3 分钟。
- SSE 实时进度、单个失败重试和批量重试。
- 认证成功的历史账号及批量登录结果可从页面导入 Sub2 未分组；任务状态持久化并通过来源标记回查确认。
- 历史列表刷新时从 Sub2 既有详情接口读取状态，可见页面每 5 分钟同步；404 显示已删除，网络异常、5xx 和身份不一致显示未知。
- 自动恢复开启后，现有 AUTH 检测任务得到明确 401/凭据失效结果时自动入队恢复，校验原 Sub2 账号仍存在及绑定身份；直接重新登录取得新凭据，取消 Refresh Token 优先路径。写回与检测成功后的调度策略依据 Sub2 状态判断，不能保证识别所有人工暂停情形。真实 401 恢复链路尚未验收。
- 自动恢复开关默认关闭、仅进程运行时有效，重启后按 `AUTH_AUTO_RECOVERY` 恢复。本次没有新增定时 AUTH 模型检测。
- HTTP 代理输入、代理测试和错误分类。
- AUTH 本地凭据的手动模型检测；检测自身只读 AT、无 RT 刷新；明确区分本地过期、401、额度、权限与网络错误。符合条件的检测结果可触发已开启的自动恢复。

## 浏览器登录的历史限制

sys1 IPv4 直连仍可能在授权前收到 Cloudflare `403 challenge`；当前浏览器路径已确认会有限等待并准确分类该响应，不会把它误判成密码错误或账号删除。需要稳定完成真实登录时，应在页面显式填写可用的 HTTP 代理并先通过代理测试。

## 启动方式

### 本地 Mac

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./start-local.sh
```

访问 `http://127.0.0.1:8080`。

### sys1

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./open-sys1.sh
```

访问 `http://127.0.0.1:18082`。保持隧道终端开启。

## 新对话入口

新对话先读 `DOCS_INDEX.md`、`NEW_CHAT_CONTEXT.md` 和 `SYS1_DEPLOYMENT.md`。旧版文档里的旧路径、旧端口和历史成功率不代表当前状态。

## 本轮精确验证命令与结果

本次 Sub2 状态同步与自动恢复发布的以下命令均通过：

```bash
go test ./cmd/server ./internal/store -timeout=90s
go vet ./cmd/server ./internal/store
node --check cmd/server/client.js
node cmd/server/client.test.cjs
./build-linux.sh /tmp/openai-login-web-20261005T020014Z-sub2-sync-auto
git diff --check
```

完整测试套件未运行。候选使用隔离数据库验证 `/api/account-recovery/settings` GET/PUT（开启后关闭）及 `/api/history` 的 Sub2 实时状态：33 个账号、12 项状态（9 正常、3 不存在）。生产切换后仅通过 GET 确认 33 个账号、12 项状态（8 正常、3 不存在、1 错误），自动恢复关闭；这是检查时的快照，不是固定状态。

二进制和 3 个嵌入静态资源 SHA 已核对；Node `v24.19.0`、Playwright `1.62.1`、Chrome `154.0.8037.97` 沿用原环境，Sub2 代码没有参与构建或部署。02:17:40 UTC 延迟复查确认唯一实际服务进程 `1422128` 运行新 release，健康正常、无重启，33 个账号及 12 项 Sub2 快照可读，自动恢复关闭。panic、fatal、数据库锁、启动失败、存储故障日志聚合均为 0。

02:17:38 UTC 实际入口 Unix socket 健康通过，公网未认证 HTTP 401；有效路由详见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。回滚二进制、Node 及 driver 的 111 个文件 hash 核对通过，备份再次确认 `0600`、`integrity_check=ok`，远端临时上传文件已清理。未执行真实 OAuth 登录或 401 恢复业务验收。

## AUTH 恢复发布验证命令与结果（此前记录）

此前 AUTH 恢复发布的以下命令均通过：

```bash
GOPROXY=off GOSUMDB=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/openai-login-20261004T164742Z-sub2-recovery ./cmd/server
go test ./cmd/server ./internal/store -count=1
go vet ./cmd/server ./internal/store
node cmd/server/account-checks.test.cjs
node --check cmd/server/account-checks.js
```

Sub2 代码没有参与本次构建或部署；其本地工作区和线上版本保持原状。线上 AUTH 二进制 SHA-256 为 `2aabc5ad3c767f015ee55b9452ee39620be49c8b8eec2be0575936e4cd62eba4`，Node、Playwright driver 和 Chrome 运行环境沿用原 release。

发布时间为 2026-10-04 16:53:09 UTC；2026-10-04 21:19:14 UTC 延后复查确认唯一服务进程运行新 release，`active/running`、`NRestarts=0`、健康检查正常。备份 `/var/lib/openai-login/backups/accounts-before-20261004T164742Z-sub2-recovery.db` 权限为 `0600`，`integrity_check=ok`；旧版二进制、Node 与 driver 已核对保留，回滚就绪，远端和本地本次构建临时文件已清理。恢复功能已发布，未执行真实 401 账号的完整恢复验收。

## 历史列表发布验证命令与结果（此前记录）

以下均通过，仅覆盖历史列表、存储删除、登录/导入直接依赖和检测面板回归；未运行全量测试。

```bash
node --check cmd/server/client.js
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
go test ./internal/store ./cmd/server -run 'Test(DeleteAccountByID|History|RunWithHistory|AccountCheckCredentialVersionAndDelete|AccountCheckAttemptedFinalEvidence|OAuthResultAndSub2ImportTaskRoundTrip|DeletedAccountIsRemoved)' -count=1 -timeout=60s
go test -race ./internal/store ./cmd/server -run 'Test(DeleteAccountByID|HistoryDeleteRejectsActiveWork|RunWithHistoryAccountRejectsDeletedIdentity|RunWithHistoryRejectsSameEmailWhileActive)' -count=1 -timeout=60s
go test -race ./cmd/server -run '^TestRunWithHistoryAccountRejectsDeletedIdentity$' -count=1 -timeout=30s
go test ./internal/store ./cmd/server -run '^(TestAttemptHistoryBusySuccessAndFailure|TestSub2ImportPostsAndReconcilesUnGroupedAccount)$' -count=1 -timeout=30s
go vet ./internal/store ./cmd/server
PLAYWRIGHT_NODEJS_PATH=/opt/homebrew/bin/node GOPROXY=off GOSUMDB=off go run /tmp/kkai-auth-history-ui.go
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/kkai-auth-history-actions-linux ./cmd/server
```

Chrome 使用25个虚构账号，20→25→20跨页选择、筛选隐藏范围确认、取消零请求、20项删除中18项成功/2项失败保留、仅重试2项、手机无横向溢出，JavaScript错误0；未触碰真实账号。临时验收脚本在完成后清理，截图 `/tmp/kkai-auth-history-desktop.png`、`/tmp/kkai-auth-history-mobile.png`保留；可重复的 fixture 保存在 `cmd/server/client.test.cjs`。线上受保护的公网入口仍返回预期401（未带平台认证）；功能API和登录在sys1本机完成验收。删除的异常场景用隔离数据验证，没有通过删除生产记录来验证。
