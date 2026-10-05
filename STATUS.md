# KKAI_AUTH 当前状态

更新时间：2026-10-05（Asia/Shanghai）

## 当前结论

项目源码在 `/Users/tokk/Desktop/KKAI_AUTH`。外部账号关联、历史恢复入口、后台 60 秒巡检及无本地令牌恢复已部署到 sys1（`20261005T035610Z-sub2-monitor-complete`）；Sub2 源码和线上版本未改。已观察真实自动检测与登录尝试；上游拒绝登录的账号明确显示失败，不作为恢复成功。完整证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

## 本轮批量导入与状态展示变更（2026-10-05）

以下记录本轮工作区新增行为；sys1 是否已切换到包含这些改动的 release，以 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 当前 release 和验收记录为准。

- 从批量登录结果导入 Sub2 时，前端等待已受理任务进入终态，并持续显示总数、已完成数、成功数、失败数和仍在队列中的数量；超时仍明确标记为待核对，不把“已受理”当作成功。
- 新建 Sub2 账号名称统一为 `AUTH_MMDDHHmm_email`（服务端使用 Asia/Shanghai 时间）；来源仍通过独立来源标记和账号身份核对确认。
- AUTH 检测的 HTTP 401 与 Sub2 返回的凭据/调度状态分开记录。Sub2 显示正常而 AUTH 当前凭据返回 401 时，历史行同时保留两条证据并显示“本地凭据失效待处理”，恢复入口仍可用。
- Sub2 状态同步保留原始 `schedulable` 和 `effective_schedulable`，以有效字段显示当前可调度性，并展示未来限流、过载、临时暂停的截止时间和原因；这些运行时窗口会阻止不适合的自动恢复。
- 历史页增加“立即检测”快捷入口，可对当前筛选或所选的已关联账号提交检测；任务详情、批量进度和单账号详情继续在“账号检测”Tab 查看。
- “账号检测”Tab 保留批量选择、分页、并发控制和详情/恢复操作，历史页快捷入口不替代原有批量与详情页面。

## 历史列表优化（此前已发布）

普通当前页全选、跨页/筛选选择保留、清空选择，以及统一批量复活/导入/删除工具栏。删除按固定账号 ID 执行，确认范围、保留失败项，后端拒绝登录和导入忙碌记录。已部署并通过 sys1 真实 OAuth 登录验收（12.16 秒），42 条账号记录保持不变。

## 线上状态

| 项目 | 当前值 |
|---|---|
| 入口 | `https://auth.kkrich.ltd` |
| 服务 | `openai-login.service` |
| sys1 本机监听 | `127.0.0.1:18082` |
| Mac 隧道 | `./open-sys1.sh` |
| 当前 release | `20261005T035610Z-sub2-monitor-complete` |
| 回滚 release | `20261005T033000Z-sub2-monitor-link` |
| 数据库 | `/var/lib/openai-login/data/accounts.db` |
| 密钥 | `/var/lib/openai-login/data/accounts.key` |
| 最近健康检查 | 2026-10-05 04:08:56 UTC：active/running，NRestarts=0，健康正常，回滚就绪 |
| 发布前备份 | `/var/lib/openai-login/backups/accounts-before-20261005T035610Z-sub2-monitor-complete.db`，0600，`integrity_check=ok` |
| 当前二进制 SHA-256 | `601b43cfd4b7ff567571bb27cfbbdeb0ab107f40125337c39a6a863d2de3f522` |
| 导入配置 | `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改 |
| 公网身份验证 | 有效入口 Unix socket 健康通过，公网未认证 HTTP 401；未验证认证后公网业务 |
| 本轮功能与 OAuth 验收 | 后台扫描与真实 AUTH 重登已发生；4 个账号被上游拒绝（account_deactivated/403）；成功恢复链路有集成 fixture 验证 |
| 自动恢复设置 | 开启并保存到 SQLite，立即及每 60 秒后台扫描，页面关闭后继续 |
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
- 认证成功的历史账号及批量登录结果可从页面导入 Sub2 未分组；批量登录导入等待并展示任务终态，名称采用 `AUTH_MMDDHHmm_email`，任务状态持久化并通过来源标记回查确认。
- 外部导入的 Sub2 账号按凭据邮箱和已知工作区自动关联；唯一匹配持久化，不重新导入。歧义、身份冲突、查询失败显示具体原因。
- 历史行提供“检测并恢复”和断点“继续恢复”，显示 AUTH 检测、重新登录、凭据写回、调度恢复及失败原因；普通“重新登录”只更新 AUTH。
- 自动恢复开关保存到 SQLite，开启立即扫描，此后每 60 秒在服务器运行，关闭页面后继续。仅检查 Sub2 异常账号；确认 401/失效/缺少本地凭据后直接 AUTH 登录，不走 RT 优先路径。缺少本地工作区时以 Sub2 身份核对新凭据。
- 正常、禁用、删除、未知、正常但人工暂停的账号跳过。任务去重，自动恢复结束后冷却 30 分钟，同一凭据版本 24 小时内失败 3 次暂停自动重试。
- 页面进行中每 5 秒刷新，自动开启且空闲时每 60 秒，关闭且空闲时每 5 分钟；显示上下次巡检与结果摘要。
- HTTP 代理输入、代理测试和错误分类。
- AUTH 本地凭据的手动模型检测；检测自身只读 AT、无 RT 刷新；明确区分本地过期、401、额度、权限与网络错误。历史页可立即提交检测，结果仍在保留的“账号检测”Tab 批量/详情页查看；符合条件的检测结果可触发已开启的自动恢复。

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

本轮以下定向验证通过；无本地令牌分支修正后重新执行相关 race 测试及 vet：

```bash
go test ./cmd/server ./internal/store -timeout=90s
go test -race ./cmd/server ./internal/store -run 'Test(Sub2Monitor|Sub2ManualCheck|Sub2AutomaticCheck|Sub2Recovery|AccountCheck|RecoveryRejects)' -timeout=90s
go vet ./cmd/server ./internal/store
node --check cmd/server/client.js
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
./build-linux.sh /tmp/openai-login-web-20261005T035610Z-sub2-monitor-complete
git diff --check
```

完整测试套件未运行。候选隔离数据库、生产资产 hash、实际入口路由、真实任务进展和回滚核对见 [部署记录](SYS1_DEPLOYMENT.md)。浏览器已确认恢复按钮、巡检摘要、关联标签、正常账号禁用原因与实际失败原因。未验证认证后公网业务；不能将 fixture 成功写回视为真实账号成功。

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
