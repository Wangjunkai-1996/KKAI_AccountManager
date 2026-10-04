# KKAI_AUTH 当前状态

更新时间：2026-10-05（Asia/Shanghai）

## 当前结论

项目源码在 `/Users/tokk/Desktop/KKAI_AUTH`。AUTH 恢复流程已于北京时间 2026-10-05 00:53:09 部署到 sys1，部署健康、数据库备份和回滚准备已确认；Sub2 源码、镜像和线上版本未改。真实 401 账号的完整恢复链路尚未验收。此前 Tab UI 和账号状态检测记录分别见 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md) 和 [ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。

## 历史列表优化（此前已发布）

普通当前页全选、跨页/筛选选择保留、清空选择，以及统一批量复活/导入/删除工具栏。删除按固定账号 ID 执行，确认范围、保留失败项，后端拒绝登录和导入忙碌记录。已部署并通过 sys1 真实 OAuth 登录验收（12.16 秒），42 条账号记录保持不变。

## 线上状态

| 项目 | 当前值 |
|---|---|
| 入口 | `https://auth.kkrich.ltd` |
| 服务 | `openai-login.service` |
| sys1 本机监听 | `127.0.0.1:18082` |
| Mac 隧道 | `./open-sys1.sh` |
| 当前 release | `20261004T164742Z-sub2-recovery` |
| 回滚 release | `20261004T104640Z-tab-ui` |
| 数据库 | `/var/lib/openai-login/data/accounts.db` |
| 密钥 | `/var/lib/openai-login/data/accounts.key` |
| 最近健康检查 | 2026-10-04 21:19:14 UTC 延后复查 `active/running`、`NRestarts=0`、`/health = status: ok, max_concurrent: 10` |
| 发布前备份 | `/var/lib/openai-login/backups/accounts-before-20261004T164742Z-sub2-recovery.db`，0600，`integrity_check=ok` |
| 当前二进制 SHA-256 | `2aabc5ad3c767f015ee55b9452ee39620be49c8b8eec2be0575936e4cd62eba4` |
| 导入配置 | `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改 |
| 公网身份验证 | 未登录 HTTP 401 与发布前一致；本轮未验证认证后业务功能 |
| 本轮功能与 OAuth 验收 | AUTH 恢复功能已发布；未用真实 401 账号完成重新登录、写回、检测和恢复调度验收 |
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
- 认证成功的历史账号可从页面导入 Sub2 未分组；任务状态持久化并通过来源标记回查确认。
- HTTP 代理输入、代理测试和错误分类。
- AUTH 本地凭据的手动模型检测；只读AT、无RT刷新、无Sub2状态调用；明确区分本地过期、401、额度、权限与网络错误。

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

本次 Tab UI 发布的以下命令均通过：

```bash
env GOPROXY=off GOSUMDB=off GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/kkai-auth-release-20261004T104640Z-tab-ui/openai-login-web ./cmd/server
env GOPROXY=off GOSUMDB=off go test ./cmd/server -run '^(TestConcurrentLoginLimitAndRelease|TestLoginRequestGuards|TestSameOriginRequests|TestHistoryListAndDeleteEndpoints|TestHistoryDeleteRejectsActiveWork|TestRunWithHistoryAccountRejectsDeletedIdentity|TestAccountCheckAPIValidationIdempotency|TestAccountCheckHistorySafeDTO)$' -count=1 -timeout=60s
```

前端语法、fixture 和模拟 API 浏览器验证沿用 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md) 本轮已通过记录，没有重复运行全仓测试。线上 3 个静态资源 SHA 与本地一致，二进制 SHA-256 为 `59b2c3960dcc9410f95c096e8dc3bdd7f19f190a11219b615deb544817567719`。Node `v24.19.0`、Playwright `1.62.1` 原样保留，Chrome `154.0.8037.97`。

发布时间为 10:54:05 UTC；10:55:37 UTC 延后复查确认唯一服务进程运行新 release，panic/fatal/数据库锁/启动失败聚合均为 0。备份 `/var/lib/openai-login/backups/accounts-before-20261004T104640Z-tab-ui.db` 权限为 `0600`，`integrity_check=ok`；旧版二进制 SHA、Node 和 driver 已保留核对，回滚就绪，临时上传文件已清理。用户自行执行本次功能与真实 OAuth 验收；未执行认证后公网业务、真实账号登录、模型检测、生产删除或 Sub2 导入验收。

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
