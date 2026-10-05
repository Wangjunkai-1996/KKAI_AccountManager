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
- 当前 release：`20261005T020014Z-sub2-sync-auto`
- 回滚 release：`20261004T164742Z-sub2-recovery`
- 浏览器运行方式：sys1 上的 Google Chrome + Xvfb，有头模式；浏览器流程实际发生在 sys1

2026-10-05 02:17:40 UTC 延迟复查：服务为 `active/running`，`NRestarts=0`，`/health` 返回 `{"status":"ok","max_concurrent":10}`。当前 AUTH release 已包含批量登录结果导入 Sub2、Sub2 状态同步、账号检测和自动恢复开关；真实 OAuth/401 恢复业务链路本轮尚未验收。历史搜索、分页、多选批量复活、详情时间线、状态筛选/排序、Token 有效期提示、输入预检、Refresh Token 复制、状态列表原地刷新和阶段进度条继续可用；页面代理留空时使用 sys1 的默认认证代理，凭据只保存在 sys1 的 systemd 环境文件中。

## 本轮 Sub2 状态同步与自动恢复（已发布）

2026-10-05 02:13:47 UTC（北京时间 10:13:47）发布 `20261005T020014Z-sub2-sync-auto`，回滚点为 `20261004T164742Z-sub2-recovery`。本次只发布 AUTH，复用 Sub2 既有详情、凭据写回等接口；Sub2 源码、镜像和线上版本未改。

历史列表刷新时读取 Sub2 状态，可见页面每 5 分钟同步一次；404 显示已删除，网络错误、5xx 和身份不一致显示未知。批量登录结果可直接导入 Sub2。“自动检测恢复”开启后，现有 AUTH 检测任务得到明确 401/凭据失效结果时自动入队恢复，沿用账号绑定、身份和任务去重校验；恢复直接调用 AUTH 重新登录，已取消 Refresh Token 优先路径。Sub2 错误与调度状态用于决定恢复后的调度策略，这一判断不能保证识别所有人工暂停情形。

自动恢复默认关闭；页面开关为进程级运行时设置，重启后读取 `AUTH_AUTO_RECOVERY`，尚未持久化。本次没有实现定时 AUTH 模型检测，5 分钟刷新只同步 Sub2 状态。

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

- AUTH 后端自己调用固定模型，页面支持单个/批量、搜索/分页/全选、真并发 1/2、取消/恢复、增量进度与分类详情；每批最多100个，只按本地已确认导入记录筛选。
- 检测只读 AT，不刷新共享 RT，不调用 Sub2 检测/状态接口；新登录后旧结论标过时。检测结果独立于登录状态。
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
