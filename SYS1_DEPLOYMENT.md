# sys1 线上部署说明

这份文件是 `KKAI_AUTH` 的 sys1 线上部署记录。账号密码、TOTP、代理凭据和 access/refresh token 不写入文档。

> 这是 sys1 线上事实的唯一权威文档。新对话先读取 `DOCS_INDEX.md` 和 `NEW_CHAT_CONTEXT.md`，发生冲突时以本文的服务、端口、release、健康检查和验收结论为准。

## 线上架构

- SSH 别名：`sys1`
- 公网入口：`https://auth.kkrich.ltd`
- 服务：`openai-login.service`
- 服务用户：`openai-login`
- 本机监听：`127.0.0.1:18082`
- 浏览器：Google Chrome + Xvfb，有头模式运行在虚拟显示器中
- 数据库：`/var/lib/openai-login/data/accounts.db`
- 加密密钥：`/var/lib/openai-login/data/accounts.key`
- release 根目录：`/opt/openai-login/releases`
- 当前链接：`/opt/openai-login/current`

当前部署基线（2026-10-05 发布后确认）：

- 当前 release：`20261005T035610Z-sub2-monitor-complete`
- 回滚 release：`20261005T033000Z-sub2-monitor-link`
- 当前服务应保持 `active (running)`，且 `NRestarts=0`
- 当前服务端并发硬上限：10；页面选择的并发数由前端 worker 控制，实际不超过该上限。
- 页面代理留空时使用 sys1 默认认证 HTTP 代理（节点地址仅记录为 `45.39.200.204:7269`，凭据保存在 `/etc/openai-login/proxy.env`，不写入文档）。
- 历史列表中的 `expires_at` 表示 OAuth Access Token 有效期；Access Token 过期不等于账号或 Refresh Token 失效。
- AUTH → Sub2 导入已启用；Sub2 地址、管理密钥和实例标识保存在 `/etc/openai-login/sub2api.env`（权限 `0600`），由 systemd drop-in 注入服务进程。
- 本次仅发布 AUTH：外部账号关联、历史恢复入口、持久化后台巡检及缺少令牌恢复；Sub2 源码、镜像和线上版本未改。
- 自动恢复当前开启并保存到 SQLite；开启立即扫描，此后每 60 秒后台检查 Sub2 异常账号。仅未保存选择时读取 `AUTH_AUTO_RECOVERY` 启动默认值。

## 本轮工作区变更记录（2026-10-05）

本节记录本轮 AUTH 工作区对批量导入和状态展示的变更；它不改变上方当前 release 的线上事实。只有在新的 release 完成发布、健康检查和业务验收后，才能把这些行为追加到线上基线。

- 从批量登录结果导入 Sub2 时，前端等待已受理任务进入终态，并展示成功、失败和仍在队列中的数量；超时会保留“仍在处理/待核对”提示。
- 新建 Sub2 账号名称采用 `AUTH_MMDDHHmm_email`（按服务端 Asia/Shanghai 时间生成），来源标记和身份核对仍是关联依据。
- AUTH 检测的 HTTP 401 与 Sub2 自身凭据/调度状态独立保存和展示。Sub2 状态正常但 AUTH 当前凭据 401 时，页面显示本地凭据失效待处理并保留恢复入口。
- 状态同步保留 Sub2 原始 `schedulable`、`effective_schedulable`，并展示限流窗口、上游过载窗口、临时暂停截止时间及原因；有效调度窗口存在时不把账号误报为可立即恢复。
- 历史页增加“立即检测”快捷操作，可按当前筛选或已选账号提交检测；“账号检测”Tab 保留批量选择、进度和单账号详情/恢复。

若当前 release 尚未包含本节改动，新对话应把它们视为待发布工作区行为，继续以当前 release、健康检查和下方已完成验收记录判断线上状态。

## 从 Mac 访问

在本目录运行：

```bash
./open-sys1.sh
```

保持 SSH 隧道终端开启，然后在 Mac 浏览器访问：

```text
http://127.0.0.1:18082
```

页面和 API 通过 SSH 隧道访问，浏览器登录流程实际运行在 sys1。停止访问时，在隧道终端按 `Ctrl+C`。

## 线上检查

```bash
ssh sys1 'systemctl --no-pager --full status openai-login.service'
ssh sys1 'readlink -f /opt/openai-login/current'
ssh sys1 'curl -fsS http://127.0.0.1:18082/health'
```

正常状态应满足：服务为 `active`，`/health` 返回 `status: ok`，监听地址仍为 `127.0.0.1:18082`。

## 发布和回滚原则

1. 在 Mac 构建 Linux amd64 二进制，并同时准备匹配的 Playwright driver、Node 和 Chrome 运行环境。
2. 每次发布使用新的不可变目录 `/opt/openai-login/releases/<release-id>`。
3. 发布前保留当前 `/opt/openai-login/current` 作为回滚点，不覆盖或删除旧 release。
4. 切换 `current` 后执行 `systemctl daemon-reload`、重启服务和 `/health` 检查。
5. 线上登录验收失败时，将 `current` 原子切回上一个已知版本，再重启并复查健康状态。
6. 不把账号凭据、代理密码或 token 写入 release、日志或文档。

本项目是独立的登录工具，不要把它误认为 Sub2API 主服务，也不要用 Sub2API 的 Compose 发布流程替代本服务的 systemd 发布方式。

## 外部关联与后台自动恢复发布（2026-10-05）

- 最终发布时间：2026-10-05 04:02:18 UTC（北京时间 12:02:18）；release：`20261005T035610Z-sub2-monitor-complete`；回滚：`20261005T033000Z-sub2-monitor-link`。回滚版本是本轮已验证的初版；更早的 `20261005T020014Z-sub2-sync-auto` 也完整保留。
- 外部导入的 Sub2 账号按凭据邮箱和已知工作区自动关联；唯一匹配持久化，不重新导入。歧义、身份冲突、查询失败显示具体原因。
- 历史行提供“检测并恢复”和断点“继续恢复”，显示 AUTH 检测、重新登录、凭据写回、调度恢复及失败原因；普通“重新登录”只更新 AUTH。
- 自动恢复开关保存到 SQLite，开启立即扫描，此后每 60 秒在服务器运行，关闭页面后继续。仅检查 Sub2 异常账号；确认 401/失效/缺少本地凭据后直接 AUTH 登录，不走 RT 优先路径。缺少本地工作区时以 Sub2 身份核对新凭据。
- 正常、禁用、删除、未知、正常但人工暂停的账号跳过。任务去重，自动恢复结束后冷却 30 分钟，同一凭据版本 24 小时内失败 3 次暂停自动重试。
- 页面进行中每 5 秒刷新，自动开启且空闲时每 60 秒，关闭且空闲时每 5 分钟；显示上下次巡检与结果摘要。
- Linux amd64 二进制 SHA-256：`601b43cfd4b7ff567571bb27cfbbdeb0ab107f40125337c39a6a863d2de3f522`；3 个嵌入静态资源与本地 SHA 一致。Node/driver 从旧 release 保留；新增设置表兼容旧二进制，回滚不覆盖业务数据库。
- 隔离候选在 SQLite 中显式关闭自动恢复，验证 `/health`、静态资源、历史关联和设置读取，未执行远端恢复。36 个历史账号：15 原导入绑定、16 外部关联、4 未匹配、1 多条匹配。
- 发布前确认无活跃登录、导入、检测和恢复；SQLite 一致性备份 `/var/lib/openai-login/backups/accounts-before-20261005T035610Z-sub2-monitor-complete.db`，0600、`integrity_check=ok`。切换后健康和资源检查通过，再恢复用户此前开启的自动恢复选择。
- 真实业务结果：首次自动扫描确认 3 个账号 AT 已撤销（HTTP401），实际调用 AUTH 重新登录，均被 OpenAI 返回 `account_deactivated`/HTTP403，保持 Sub2 暂停；原未关联账号 63 在其中。另 1 个 Sub2 403 账号 AUTH 检测 HTTP200，未强制重登。原截图三个新导入账号 117/118/119 均正常可调度，未生成恢复任务。
- 无本地令牌的账号 67 在最终修正后也自动完成登录尝试，被 OpenAI 返回 `account_deactivated`/403；本轮共 4 次真实重新登录均被上游拒绝，无成功凭据写回。
- 04:08:56 UTC 延迟复查：服务 `active/running`、`NRestarts=0`，唯一实际进程 `1778321` 运行最终 release，健康正常；数据库中的开关值为 1，已持续按 60 秒执行扫描，4 个失败任务在冷却中。
- 有效 Nginx `auth.kkrich.ltd` 路由仍指向 `/run/tls/auth/login.sock`；实际 Unix socket 健康通过，公网未认证 HTTP401。panic、fatal、数据库锁、存储故障、启动失败日志聚合均为0。旧二进制、Node、driver 111个文件 hash 及0600完整性备份核对通过，回滚就绪。
- 验证命令见 [STATUS.md](STATUS.md)。完整测试套件未运行；自动恢复成功写回及重新调度已通过集成 fixture，真实账号均被上游拒绝，成功写回及恢复调度仍缺少本轮真实账号证据。未验证认证后公网业务。

## Sub2 状态同步与自动恢复发布记录（2026-10-05，前次版本）

- 发布时间：2026-10-05 02:13:47 UTC（北京时间 10:13:47）；release：`20261005T020014Z-sub2-sync-auto`；回滚：`20261004T164742Z-sub2-recovery`。
- 批量登录结果可直接导入 Sub2；账号历史刷新读取 Sub2 现有详情接口，可见页面每 5 分钟同步一次。404 显示已删除，网络异常、5xx 或身份不一致显示状态未知，不据此误报删除。
- 开启“自动检测恢复”后，现有 AUTH 检测任务得到明确 401/凭据失效结果时自动创建恢复任务；仍须满足原 Sub2 账号存在、绑定和身份校验。恢复直接调用 AUTH 重新登录，不再先尝试 Refresh Token。调度恢复沿用 `status=error` 等状态判断；该判断无法保证识别所有人工暂停情形。
- 本次没有新增后台定时 AUTH 模型检测。自动恢复开关启动默认关闭，运行时修改不持久化，重启后按 `AUTH_AUTO_RECOVERY` 恢复默认值。Sub2 源码、镜像和线上版本未改。
- Mac 构建的 Linux amd64 二进制 SHA-256：`c7166a6e999eb2eb3c20f78d98944072a13abaf9ae20246d7c1433d6b2c0cbdc`；3 个嵌入静态资源 SHA 与本地一致。Node `v24.19.0`、Playwright `1.62.1` 和 Chrome `154.0.8037.97` 沿用原运行环境。
- 发布前 SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261005T020014Z-sub2-sync-auto.db`，权限 `0600`，`integrity_check=ok`。切换后即时检查为 `active/running`、`NRestarts=0`、`/health={"status":"ok","max_concurrent":10}`；旧 release 保留。
- 候选使用隔离数据库验证设置接口 GET/PUT（开启后关闭）及只读 Sub2 状态同步：33 个账号、12 项 Sub2 状态（9 正常、3 不存在）。生产切换后只读检查为 33 个账号、12 项状态（8 正常、3 不存在、1 错误）；这些是检查当时的实时快照。
- 02:17:40 UTC 延迟复查仍为 `active/running`、`NRestarts=0`，唯一实际服务进程 `1422128` 运行新 release，健康检查正常；账号 33 条、Sub2 快照 12 项、自动恢复关闭。发布以来 panic、fatal、数据库锁、启动失败和存储故障日志聚合均为 0。
- 有效入口链路为 `kkai-edge` Nginx 的 `auth.kkrich.ltd:8443` TLS → `/run/tls/auth/login.sock` → `openai-login-gateway.socket`（宿主路径 `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock`）→ `socket-proxyd` → `127.0.0.1:18082`。02:17:38 UTC 实际 Unix socket 健康检查通过，公网未认证返回 HTTP 401；未验证认证后公网业务。
- 回滚二进制 SHA 与此前记录一致，Node 完整 hash 与 driver 的 111 个文件树 hash 核对通过，备份再次确认 `0600`、`integrity_check=ok`，回滚准备完成；远端临时上传文件已清理。
- 定向 Go 测试、vet、前端检查及 Linux 构建通过，精确命令见 [STATUS.md](STATUS.md)。完整测试套件未运行；未执行真实 OAuth 登录或 401 账号恢复的业务验收，不能以健康检查替代该验收。

## AUTH 恢复流程发布记录（2026-10-05，此前版本）

- 发布时间：2026-10-04 16:53:09 UTC（北京时间 2026-10-05 00:53:09）；release：`20261004T164742Z-sub2-recovery`；回滚：`20261004T104640Z-tab-ui`。
- 本次只发布 AUTH。检测到明确的 401/凭据失效后，AUTH 可重新登录取得新 Refresh Token，调用 Sub2 既有凭据写回接口，重新检测成功后开启调度；失败或不确定时保持停用。Sub2 源码、镜像和线上版本未改。
- Mac 构建的 Linux amd64 二进制 SHA-256：`2aabc5ad3c767f015ee55b9452ee39620be49c8b8eec2be0575936e4cd62eba4`；线上 release 保留原有 Node、Playwright driver 和 Chrome 运行环境。
- 发布前 SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T164742Z-sub2-recovery.db`，权限 `0600`，`integrity_check=ok`。切换后及延迟复核服务为 `active/running`、`NRestarts=0`、`/health={"status":"ok","max_concurrent":10}`，公网未登录返回 HTTP 401，旧 release 保留可回滚。
- 恢复功能已发布，真实 401 账号的完整重新登录、写回、检测和重新开启调度链路尚未验收。

## Tab UI 发布记录（2026-10-04）

- 发布时间：2026-10-04 10:54:05 UTC（北京时间 18:54:05）。release：`20261004T104640Z-tab-ui`；回滚：`20261004T045750Z-history-actions`。
- 发布三栏 Tab 布局、移动端滚动与底部操作栏、导航和焦点管理，以及统一应用内确认框。前端定向测试及模拟 API 浏览器验证见 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md)；本次功能与真实 OAuth 验收按用户要求由用户自行执行，未声称本轮真实登录通过。
- Mac 本地构建二进制 SHA-256：`59b2c3960dcc9410f95c096e8dc3bdd7f19f190a11219b615deb544817567719`。线上 HTML 和两份 JavaScript 共 3 个静态资源 SHA 与本地一致。Node `v24.19.0`、Playwright `1.62.1` 原样保留，系统 Chrome 为 `154.0.8037.97`。
- 发布前 SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T104640Z-tab-ui.db`，权限 `0600`，`integrity_check=ok`。发布前后账号均为 30 条，导入和检测摘要可用，`sub2_configured=true`；业务数据库未被回滚覆盖。
- 切换后及 10:55:37 UTC 延后复查均为 `active/running`、`NRestarts=0`、`/health={"status":"ok","max_concurrent":10}`；确认唯一服务进程运行新 release 二进制。发布以来 panic、fatal、数据库锁及启动失败日志聚合均为 0。
- 公网未登录返回 HTTP 401，与发布前一致，身份验证保护正常；这不代表认证后公网或业务功能验收。旧版二进制 SHA、Node 与 driver 已核对保留，回滚就绪，临时上传二进制已清理。
- Linux amd64 构建和 Go 定向测试通过，精确命令见 [当前状态](STATUS.md)。沿用本轮已通过的前端验证记录，未运行全仓测试；未执行真实账号登录、模型检测、生产删除或 Sub2 导入验收。

## 历史列表选择与批量删除发布验收（2026-10-04）

- 当前页普通全选覆盖所有账号状态；翻页/筛选保留选择并标明当前页外数量，支持清空选择。复活、导入、删除共用紧凑工具栏，每行只有一个选择框。
- 删除前确认 AUTH 本地记录及数量；逐 ID 执行、15 秒请求超时、明确失败项并保留勾选。不会删除 Sub2 中已导入账号。后台拒绝正在登录或导入中/待核对记录，事务清理本地终态导入快照；在途历史重登和导入不能重建已删除 ID。
- release：`20261004T045750Z-history-actions`；回滚：`20261004T034448Z-account-checks`，原 Node/driver 完整保留。二进制 SHA-256：`562f5a493bd0c322e9fd0cb33f8828b97d04ee9826805bbe929b16878b37f4f0`。
- 发布前无活跃登录、导入或检测，SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T045750Z-history-actions.db`（0600，integrity_check通过）。没有 schema 变更，回滚只切二进制，不覆盖业务数据库。
- 上线后原 OAuth 测试账号通过 sys1 默认代理完整登录成功，12.16 秒，AT/RT 返回；服务 active、NRestarts=0、并发上限10。历史账号总数42保持不变，导入和检测摘要可用；未在生产删除账号或触发 Sub2 导入。
- 切换后再次确认二进制 SHA、回滚运行环境和备份权限；panic、fatal、数据库锁、登录失败、删除失败日志计数均为0，AT/RT已持久化，临时上传文件已清理。
- 定向 Go/race/前端 fixture、真实 Chrome 桌面与手机验收通过，测试覆盖普通全选、跨页/筛选、取消、忙碌409、限定404、超时、部分失败重试、导入部分受理和键盘焦点。精确命令见 [当前状态](STATUS.md)。全量测试未运行。

## 账号状态检测发布验收（2026-10-04）

- 新增 AUTH 自有检测功能，固定 `gpt-5.6-luna`，默认服务器代理/显式 sys1 IPv4 直连，单批最多 100、真并发 1/2，与登录槽独立。不读取/刷新 RT，不调用 Sub2 检测接口。
- sys1 默认代理同测试账号两次完成模型请求，耗时 1,896/1,530ms；sys1 IPv4 直连完成，耗时 1,982ms。
- 候选使用独立数据库快照，两个已导入账号均检测正常，执行窗口重叠 3,035ms；正式版正常账号检测 3,206ms 完成，另一个账号真实 429/usage_limit_reached 正确显示额度不足。
- 原有 OAuth 登录在正式版经默认代理完整成功，18.93秒，AT/RT 已持久化；登录并发硬上限仍为 10。服务 active、NRestarts=0，检测/历史/导入摘要均可读。
- 使用旧版二进制打开新 schema 的隔离副本，健康检查通过；原 release 及 Node/driver 保留为回滚。新增表不要求回滚时覆盖数据库。
- 发布前一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T034448Z-account-checks.db`（0600，integrity_check通过）。
- 完整命令、浏览器验收、真实响应细节见 [实施记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。全仓测试未运行。

## 浏览器 OAuth 的历史限制

sys1 IPv4 `51.81.109.154` 访问 `auth.openai.com/oauth/authorize` 仍可能收到 Cloudflare `403` 和 `cf-mitigated: challenge`；此前 release 已在 sys1 本机用原生 Chrome、无头 Playwright 两条路径复现，且在初始授权文档和表单提交请求中都观察到过。顶层文档 challenge 会等待 25 秒观察浏览器验证；表单的 fetch/XHR challenge 不重放提交，直接保留真实 403 并准确分类。此前使用 `45.39.200.210:7275` 代理时，真实账号已连续完成 4 次 MFA、授权和 token 交换；当前默认代理为 `45.39.200.204:7269`，历史代理检查可达并返回认证站 HTTP 403。页面显示 challenge 时不要把它解释为密码错误或账号已删除。本次 Tab UI 发布未重做真实 OAuth 验收。

需要继续排查时，先记录授权请求的 HTTP 状态、`cf-mitigated`、出口和浏览器启动参数，再决定是否更换出口或调整浏览器验证流程。
