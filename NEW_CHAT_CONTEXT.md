# KKAI_AUTH 新对话交接上下文

更新时间：2026-10-05（Asia/Shanghai）

> 新对话先读取 `DOCS_INDEX.md`、本文和 `SYS1_DEPLOYMENT.md`。本文只记录项目状态，不保存账号密码、TOTP、代理凭据或 token。

## 项目位置

- 桌面工作副本：`/Users/tokk/Desktop/KKAI_AUTH`
- 原项目目录：`/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/tools/openai-login`
- 桌面副本是当前继续开发和构建的源码目录；不要默认从父 Sub2API Git 仓库导出本工具。
- sys1 部署权威文档：`/Users/tokk/Desktop/KKAI_AUTH/SYS1_DEPLOYMENT.md`

## 当前线上部署

- 服务：`openai-login.service`
- SSH 别名：`sys1`
- 公网入口：`https://auth.kkrich.ltd`
- sys1 本机监听：`127.0.0.1:18082`
- Mac 访问脚本：`./open-sys1.sh`
- 数据库：`/var/lib/openai-login/data/accounts.db`
- 加密密钥：`/var/lib/openai-login/data/accounts.key`
- release 根目录：`/opt/openai-login/releases`
- 当前 release：`20261005T105500Z-sub2-status-ui`
- 当前 commit：`9a08749`
- 当前二进制 SHA-256：`5bcd2aaa1c82efa35428172c95ded6a6f592e846d7639263403ed129bc5c6462`
- 回滚 release：`20261005T035610Z-sub2-monitor-complete`
- 浏览器运行方式：sys1 上的 Google Chrome + Xvfb，有头模式；浏览器流程实际发生在 sys1

2026-10-05 发布 `20261005T105500Z-sub2-status-ui`（commit `9a08749`）；回滚为 `20261005T035610Z-sub2-monitor-complete`。发布后服务 `active/running`、`NRestarts=0`，`/health` 返回 `status: ok`、`max_concurrent: 10`，有效入口 Unix socket 健康通过，公网未认证返回 HTTP 401。候选验证时自动恢复关闭，生产 SQLite 开关保持开启；生产快照为 history 31、imports 27、statuses 31。账号 117/118/119 均观察到 AUTH HTTP 401 与 Sub2 未来限流窗口，未将其计为恢复成功。完整健康、回滚和业务结果见 `SYS1_DEPLOYMENT.md`，精确验证命令见 `STATUS.md`。

本轮已上线批量登录结果导入的终态等待和展示、Sub2 导入名称 `AUTH_MMDDHHmm_email`、AUTH 401 与 Sub2 凭据/调度状态的独立呈现、原始 `schedulable`/`effective_schedulable` 及限流/过载/临时暂停信息展示。历史页新增“立即检测”快捷入口；“账号检测”Tab 仍保留批量检测、进度和单账号详情/恢复。

## 当前账号关联和恢复行为

历史刷新读取 Sub2 状态；无绑定时分页读取 OpenAI OAuth 账号，以凭据中的邮箱精确匹配，并核对 AUTH 已知的 ChatGPT 账号 ID。唯一匹配保存为本地关联，不重新导入，也不改 Sub2 名称、分组或来源标记。自定义名称不影响匹配；歧义、身份冲突、查询失败显示原因，已关联账号明确返回 404 才标记删除。状态展示、检测及恢复共用当前目标站点的绑定。

状态同步保留 Sub2 原始 `schedulable` 与 `effective_schedulable` 字段；历史行按有效值显示可调度性，并显示未来限流、过载或临时暂停的截止时间/原因。AUTH 的 HTTP 401 只说明 AUTH 当前本地凭据鉴权失败，不覆盖 Sub2 自身凭据或调度结论；两者会同时展示，便于决定是否恢复。

“自动检测恢复”保存到 SQLite，服务重启后保留；未保存时使用 `AUTH_AUTO_RECOVERY` 默认值。开启立即扫描，此后每 60 秒在后端巡检，关闭网页不影响运行；页面显示上次/下次扫描、摘要、检测及恢复进度。仍在 Sub2 池中且未人工暂停的账号，只有 AUTH 当前确认 401、令牌过期、凭据失效或缺少本地令牌后才直接 AUTH 重新登录，写回原 Sub2 账号并验证、恢复调度。Sub2 `status=error` 或 `schedulable=false` 不单独证明 401；正常、禁用、删除、未知及人工暂停的账号不会仅凭 Sub2 状态恢复。

历史页提供“检测并恢复”和可恢复任务的“继续恢复”，人工操作不依赖自动开关；普通“重新登录”只更新 AUTH，不写回 Sub2。复用检测冷却和任务去重；自动恢复结束后冷却 30 分钟，同一凭据版本 24 小时内失败 3 次后停止自动重试。Sub2 异常不等于 401，AUTH 检测正常时不强制重登；异常加暂停仍不能完全判定人工/自动暂停来源。真实账号业务验收与上线证据以本次部署记录为准，不沿用历史成功结果。

## Sub2 状态同步与自动恢复（前次发布记录）

2026-10-05 02:13:47 UTC（北京时间 10:13:47）发布 `20261005T020014Z-sub2-sync-auto`，回滚点为 `20261004T164742Z-sub2-recovery`。本次只发布 AUTH，复用 Sub2 既有详情、凭据写回等接口；Sub2 源码、镜像和线上版本未改。

该次发布加入批量登录结果导入、Sub2 状态显示与自动恢复开关，恢复直接 AUTH 重新登录，取消 Refresh Token 优先路径。当前关联、后台巡检和持久化开关行为见上节。

发布前备份为 `/var/lib/openai-login/backups/accounts-before-20261005T020014Z-sub2-sync-auto.db`（`0600`、`integrity_check=ok`），二进制 SHA-256 为 `c7166a6e999eb2eb3c20f78d98944072a13abaf9ae20246d7c1433d6b2c0cbdc`，3 个静态资源 SHA 与本地一致。沿用 Node `v24.19.0`、Playwright `1.62.1`、Chrome `154.0.8037.97`。

候选使用隔离数据库验证设置接口 GET/PUT（开启后关闭）和只读 Sub2 状态读取：33 个账号、12 项状态（9 正常、3 不存在）。生产切换后仅 GET 验证：33 个账号、12 项状态（8 正常、3 不存在、1 错误），自动恢复关闭；状态会随上游变化。本轮未执行真实 OAuth 登录或 401 恢复业务验收。定向验证和构建通过，完整测试套件未运行，精确命令见 [STATUS.md](STATUS.md)。

02:17:40 UTC 延迟复查确认唯一实际服务进程 `1422128` 运行新 release，服务与健康正常、无重启，33 个账号和 12 项快照可读。发布以来 panic、fatal、数据库锁、启动失败及存储故障日志聚合均为 0。实际入口 Unix socket 健康通过，公网未认证 HTTP 401；回滚二进制、Node 和 driver 文件 hash 已核对，备份再次确认 `0600`、`integrity_check=ok`，远端临时上传文件已清理。有效路由及完整证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

## Tab UI 优化（此前已发布）

2026-10-04 10:54:05 UTC（北京时间 18:54:05）发布 `20261004T104640Z-tab-ui`，回滚点为 `20261004T045750Z-history-actions`。三栏 Tab、移动端布局、导航、焦点管理和应用内确认框已上线，3 个静态资源 SHA 与本地一致。账号总数发布前后均为 30，导入和检测摘要可用，`sub2_configured=true`。Node `v24.19.0`、Playwright `1.62.1` 原样保留，Chrome `154.0.8037.97`。

切换后与 10:55:37 UTC 延后复查均为 `active/running`、`NRestarts=0`、健康检查正常；唯一进程运行新 release，发布以来 panic/fatal/数据库锁/启动失败聚合均为 0。公网未登录 HTTP 401 认证保护与发布前一致，旧二进制及运行环境已保留可回滚。一致性备份为 `/var/lib/openai-login/backups/accounts-before-20261004T104640Z-tab-ui.db`（`0600`、`integrity_check=ok`）。

用户明确本次功能与真实 OAuth 验收由用户自行完成；没有进行认证后公网业务、真实账号登录或模型检测验收，不能沿用历史成功记录作为本轮结果。Linux amd64 构建和 Go 定向测试已通过，前端验证沿用 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md) 本轮记录；全仓测试未运行。精确命令见 [STATUS.md](STATUS.md)，二进制 SHA 和完整发布事实见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

## AUTH 恢复发布（此前已发布）

2026-10-04 16:53:09 UTC 发布 `20261004T164742Z-sub2-recovery`，回滚点为 `20261004T104640Z-tab-ui`。本次只发布 AUTH；Sub2 源码、镜像和线上版本未改，恢复阶段调用 Sub2 既有接口。发布前 SQLite 备份为 `/var/lib/openai-login/backups/accounts-before-20261004T164742Z-sub2-recovery.db`，权限 `0600`，`integrity_check=ok`；二进制 SHA-256 为 `2aabc5ad3c767f015ee55b9452ee39620be49c8b8eec2be0575936e4cd62eba4`。

切换后及延迟复核均为 `active/running`、`NRestarts=0`、健康检查正常，公网未登录仍返回 HTTP 401，旧 release 和运行时依赖均保留。恢复功能已发布，但尚未使用真实 401 账号验收完整的重新登录、写回、检测和重新调度链路。

## 历史列表优化（此前已发布）

已上线普通当前页全选（不限状态）、跨页/筛选保留选择、清空选择、紧凑统一工具栏和批量删除。只删除 AUTH 本地记录，不调用 Sub2 删除。按固定账号 ID 删除，忙碌登录/未完成或待核对导入返回409；失败项保留选中。历史重登要求原 ID 仍存在，避免删除后被在途请求重建。已通过 sys1 OAuth 登录验收（12.16秒），42条账号记录保持不变；精确验证见 STATUS.md，部署基线见 SYS1_DEPLOYMENT.md。

## 账号状态检测（此前已发布）

- AUTH 后端自己调用固定模型，页面支持单个/批量、搜索/分页/全选、真并发 1/2、取消/恢复、增量进度与分类详情；每批最多100个，覆盖本地已确认导入或关联的记录。
- AUTH 检测只读本地 AT，不刷新共享 RT；Sub2 状态由独立同步读取，恢复写回后另有 Sub2 探测验证。新登录后旧检测结论标过时，检测结果独立于登录状态。
- 历史页提供“立即检测”快捷提交；“账号检测”Tab 继续作为批量检测与单账号详情/恢复页面，快捷入口不会替代原有批量进度和详情。
- 已在 sys1 验证默认代理与 IPv4 直连模型请求成功，以及两个账号实际请求重叠；正式版正确记录正常与真实429额度不足。原OAuth登录经代理也完整通过。
- 详见 [实施与验收记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)；后续从已实现功能继续，不要再按未实施方案重复开发。

## 浏览器登录的历史阻塞和证据

sys1 访问 `auth.openai.com/oauth/authorize` 时，授权请求可能在输入邮箱之前收到 HTTP `403`，并带有 `cf-mitigated: challenge`。因此页面会在输入密码前关闭或显示失败。

已做过的对比：

- 账号历史功能优化前后的登录服务内容一致。
- Playwright driver 文件树一致。
- `browser-compat=true` 和 `browser-compat=false` 都能复现 challenge。
- 此前 release 在 sys1 IPv4 `51.81.109.154` 直连时收到授权流程 `403 + cf-mitigated: challenge`；该响应既在初始授权文档、也在表单提交请求中复现。同一 sys1 通过 `45.39.200.210:7275` 代理时完整登录成功。

此前浏览器流程结论对应 `20261003T220628Z-expiry-fix-final`：无认证代理和直连有头登录使用系统 Chrome + CDP；认证代理使用 headed Playwright 并对齐 webdriver、语言和时区；MFA/授权按钮支持中英文；缺少缓存 Node 时自动回退系统 Node。历史账号重登成功后，前端仅在当前页面会话内显示并复制 Refresh Token（rt），历史 API 仍保持脱敏。账号历史支持邮箱搜索、分页、当前页可复活账号全选、详情时间线、状态筛选/排序、有效期提示和顺序批量复活；Access Token 的 `exp` 优先于 ID Token 的 `exp`，列表明确显示“访问令牌已过期”，不把它解释为账号或 Refresh Token 失效；无 JWT 有效期时回退 OAuth `expires_in`；批量复活默认仅允许失败、中断、停用或待确认删除状态，成功账号不会被全选误触；历史接口刷新失败时会禁用全选、批量复活和分页控件。登录成功路径的三处人为随机停顿统一为 100–250ms；Cloudflare challenge、MFA/OAuth 条件等待和失败退避保持不变。处理状态列表复用已有行并按登录阶段显示估计进度，避免刷新频闪。直连 challenge 已在原生 Chrome、无头 Playwright 两条路径复现；页面代理留空时使用当前 sys1 默认认证代理。AUTH → Sub2 导入已启用，线上 `/api/history` 已确认 `sub2_configured=true`。

## 已实现功能

- SQLite 账号历史和逐次登录尝试记录。
- 密码、TOTP、access/refresh token 使用 AES-GCM 加密，密钥与数据库分离。
- 历史重登只提交账号 ID，由服务端读取加密凭据。
- 只有明确的 `account_status == deleted` 才从数据库删除；停用、`deleted_or_deactivated` 和普通 403 保留以便重试。
- JSON 和 SSE 登录入口共用账号级互斥，规范化邮箱后避免同账号并发互相覆盖。
- 重启时将遗留的 running 尝试标记为 interrupted。
- 前端支持账号历史、状态、单个重试和批量重试；失败记录不会覆盖之前的成功记录。
- 支持 `---` 和 `----` 两种账号分隔格式，批处理前按规范化邮箱去重。
- 默认并发 1，页面可选 1–10，实际以服务端硬上限为准；单账号默认总超时 3 分钟。
- 支持 HTTP 代理；登录页面代理为空时使用服务器配置的默认代理。账号检测的 sys1 IPv4 直连须明确选择。
- 失败时保留 HTTP/业务错误信息；明确删除/停用和 Cloudflare challenge 分开分类。

## 本地和线上使用

### 本地 Mac

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./start-local.sh
```

默认访问 `http://127.0.0.1:8080`。首次运行会构建 `cmd/server` 并准备 Playwright driver；需要停止旧实例后再重新启动。

### sys1

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./open-sys1.sh
```

保持隧道终端开启，在浏览器访问 `http://127.0.0.1:18082`。登录流程运行在 sys1，不是在 Mac 浏览器进程中运行。页面只需要一个 HTTP 代理输入框；代理必须能从 sys1 访问，`127.0.0.1` 指 sys1 本机。

## 线上只读检查

```bash
ssh sys1 'systemctl --no-pager --full status openai-login.service'
ssh sys1 'readlink -f /opt/openai-login/current'
ssh sys1 'curl -fsS http://127.0.0.1:18082/health'
```

## 发布和回滚边界

- 使用新的不可变 release 目录，保留现有 current 作为回滚点。
- 发布必须同时匹配 Linux amd64 二进制、Playwright driver、Node 和 Chrome 运行环境。
- 切换后必须检查 systemd 状态、`/health`、有效路由和登录验收结果。
- OAuth 真实账号验收失败时恢复到上一个已知 release，并再次执行健康检查。
- 不执行代码托管网络操作；本项目使用本地源码和手工上传到 sys1 的发布流程。

## 验证记录

已通过的定向检查包括：

```bash
go test ./internal/login ./cmd/server ./internal/store ./cmd -count=1 -timeout=90s
go vet ./internal/login
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/openai-login-web ./cmd/server
```

完整全仓库测试未运行；后续只根据实际变更扩大验证范围。

## 新对话第一步

先读取：

1. `/Users/tokk/Desktop/KKAI_AUTH/DOCS_INDEX.md`
2. `/Users/tokk/Desktop/KKAI_AUTH/NEW_CHAT_CONTEXT.md`
3. `/Users/tokk/Desktop/KKAI_AUTH/SYS1_DEPLOYMENT.md`

然后确认当前任务是继续排查 Cloudflare challenge、修改本地代码，还是进行新的 sys1 发布。不要把旧文档里的 `8080`、旧源码路径或历史“登录成功”记录当作当前线上验收结论。
