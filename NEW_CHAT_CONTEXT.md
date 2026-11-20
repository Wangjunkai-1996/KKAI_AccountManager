# KKAI_AUTH 新对话交接上下文

## 当前线上：重导入旧代理备用与错误展示修复（2026-11-14）

sys1 已上线 `20261114T033924Z-reimport-proxy`。#780 两次重导操作在登录阶段因历史代理的 HTTPS 隧道失败而停止；先前笼统显示“代理配置无效”，并被 Sub2 缺失提示遮蔽。现在未绑定/关联缺失账号没有显式选择出口时会先探测旧代理，连接失败后尝试 sys1 IPv4；认证、证书、取消与无效配置仍保留各自门禁。账号诊断及“更多”同时显示最近持久化登录错误，失败后按钮恢复可用。

产品、后端安全、前端复审及定向检查通过。北京时间 11:48:01 最终验收：候选/正式账号 1581 OAuth 成功，正式用时 12.573 秒；active/running、NRestarts=0、登录后巡检推进、错误聚合全为 0。路由、数据库、静态 hash、0600 一致性备份及回滚版 `20261114T030748Z-reimport` 均验证通过。没有替 #780 再次登录或创建 Sub2；仍保留 remote_missing。全仓完整套件未运行，未提交/推送源码。精确命令、根因、边界与证据见 [修复记录](docs/account-management-20261113/reimport-proxy-fix-20261114.md)。


## 历史发布：Sub2 缺失账号显式重新导入（2026-11-14）

sys1 已上线 `20261114T030748Z-reimport`。账号表“更多”对仍保留 `remote_missing` 绑定的账号提供“重新登录并导入 Sub2”：用户确认一次交付参数后，系统以 `auto_deliver:false` 登录 AUTH，刷新并校验凭据版本严格增长及原 `operation_id` 未变化，再提交一次 `reimport:true` 导入。取消、登录失败、历史刷新失败、状态变化、版本未增长、旧 operation、超时未知结果都不会自动重发或创建账号。单账号参数不污染批次或平台默认值；无绑定历史记录、官方删除账号和普通 missing 导入仍不自动重建。

最终候选经产品、后端安全、前端独立复审；Node、后端/store 定向测试、Linux amd64 构建、服务用户浏览器 runtime、桌面 1200px 合成页面和 390px 页面验收通过，页面无 JavaScript 异常或横向溢出。生产真实 OAuth 使用授权验收账号 1581，`auto_deliver=false`，attempt 1594 成功，未创建 Sub2。正式切换后即时与 60 秒延迟 gate 通过：active/running、NRestarts=0、health/ready/socket 正常，公网未认证 401，巡检推进，错误聚合全为 0；备份 `/var/lib/openai-login/backups/accounts-before-20261114T030748Z-reimport.db` 完整性为 ok、权限 0600。

截图中的 AUTH #780 / Sub2 #17075 仍为 `remote_missing`，本轮未触发重建。完整方案、参数契约、浏览器验证和限制见 [重新导入记录](docs/account-management-20261113/reimport-20261114.md)。本轮完整测试套件未运行。

## 历史发布：暂停账号操作与 TOTP 修复（2026-11-14）

sys1 已上线 `20261114T021850Z-paused-actions`，经后端、前端与产品独立复审。自动暂停不再隐藏重登录/恢复入口，“更多”展开区域可用；每次显式操作只执行一轮，单次意图在登录开始前持久化，失败或重启不会被后台重放。新版 TOTP 文案漏识别已修复，Sub2 缺失和身份/分组/调度保护保留。

AUTH 1579、1580 已各一次真实登录成功，沿用原任务 1304、1305 写回原 Sub2 17353、17352；均 active 且有效可调度，没有新建账号。北京时间 10:24:45 最终验收通过，登录后的巡检推进、错误聚合全为 0，active/running、NRestarts=0、数据库/入口/静态资源正常；257 个已删除关联仍阻断。

前版 `20261113T163828Z-account-management`、一致性备份和运行环境完整保留，未自动回滚。隔离凭据副本和上传目录已清理。定向 Go/race/Node/浏览器/vet、候选与正式业务验收通过；全仓完整套件未运行，未提交/推送源码。精确命令、限制与脱敏证据见 [本轮修复和发布记录](docs/account-management-20261113/paused-actions-20261114.md)。

## 历史发布：账号管理与删除保护（2026-11-14 00:55）

sys1 已部署 `20261113T163828Z-account-management`，前一版 `20261113T104100Z-ipv4-fallback` 与一致性备份保留。文档经产品、后端安全、前端审查，代码完成后再次交叉复审；首个候选的真实 TOTP 页面遗漏已拦截并修复，最终候选与正式 OAuth 均成功。

邮箱/手机验证及密码/TOTP 错误停止自动重试并显示具体原因；详情可按字段查看密码与 TOTP 密钥，账号表保留名称前缀并支持套餐筛选。可信 Sub2 缺失持久化 `remote_missing`，停止旧检测、恢复、交付与修复；官方删号仍走独立清理。线上确认 257 个缺失关联、活跃恢复 0；77 个现有关联有可信自定义名称，另 2 个状态未知继续保守处理。

北京时间 00:55:57 最终验收：active/running、NRestarts=0，health/ready/socket 与实际路由正常；真实正式 OAuth 12.467 秒，未触发交付；登录后巡检继续，发布窗口固定错误聚合全为 0。凭据单字段接口、无秘密的普通详情、跨站 403、公网未认证 401 均通过，回滚运行环境与备份完整。临时候选凭据副本及上传目录已清理，操作脚本与验收证据归档。定向测试通过，全仓完整套件未运行；未提交/推送源码。精确命令、结果、限制及脱敏证据见 [实施与发布记录](docs/account-management-20261113/implementation.md)。

更新时间：2026-11-14（Asia/Shanghai）

> 新对话先读取 `DOCS_INDEX.md`、本文和 `SYS1_DEPLOYMENT.md`。本文只记录项目状态，不保存账号密码、TOTP、代理凭据或 token。

## 历史发布：认证页面超时复活修复（2026-11-01 11:52 北京时间）

sys1 当前 `20261101T031600Z-auth-page-timeout`；前一版 `20261031T204209Z-recovery-fix` 保留，未回滚。三个角色审查方案、第四角色复审实现后完成：页面等待超时进入自动退避，真正页面结构错误/明确认证拒绝保持人工处理；父级总超时保留页面原因，不能绕过预算。同任务总失败次数到 3 时，仍报页面等待超时则转人工，不无限重试。无 schema/依赖变更，未提交/推送代码。

只将通过任务/凭据版本/绑定/更新时间校验的 AUTH #842 task654 重新纳入原重试流程。11:49:30 登录成功（attempt1103），11:49:33 任务完成；11:51:42 确认 Sub2 17133 active、schedulable=true，出口仍 route199。AUTH #317 已失败25轮，未释放。首轮延迟复检按原策略在12:19:33进行，当前尚未到期。候选、运行环境、兼容、即时/60秒延迟验收和真实#842恢复通过；服务健康、巡检推进、发布窗口错误聚合0。定向race/vet及脚本测试通过，全量未运行。方案、完整验证命令与证据见 [AUTH_PAGE_TIMEOUT_FIX_20261101.md](AUTH_PAGE_TIMEOUT_FIX_20261101.md)。

## 历史发布：自动检测与恢复修复（2026-11-01）

三角色方案商议后完成修复并上线 sys1。正式 release：`20261031T204209Z-recovery-fix`；回滚：`20261031T194600Z-recent-cost`；Linux amd64 二进制 SHA-256：`732ec7cb6e7268d3758fef493ca2cf084917c1076057c3f6312baae4208701ed`；源码为 `33d64a0401599837246f647d4d5fdf554e8a0015` + 当前工作区，未提交/推送。无 schema 迁移，Sub2 服务未修改。

- 已修复：登录写回 fresh 身份门禁、出口临时故障持久退避、检测队列公平轮转、`Retry-After` 冷却、checker 健康状态、目标隔离、空状态查询和延迟复检入口。
- 邮箱 `trunk.earthen-97+split42fb6bb24853@icloud.com` 对应 AUTH account `1055`。候选与生产真实 OAuth 均通过，`auto_deliver=false`；候选约 35.8 秒，生产约 12.6 秒；两次均成功保存凭据，数据库完整性为 `ok`。不记录凭据内容。
- 候选、回滚兼容、服务用户浏览器 smoke、静态资源 hash、数据库完整性均通过。生产切换前后 8 类在途任务均为 0；生产备份 `/var/lib/openai-login/backups/accounts-before-20261031T204209Z-recovery-fix.db`，权限 0600，`integrity_check=ok`。
- 切换后即时及 60 秒延迟验收通过：active/running、`NRestarts=0`、health/ready/socket 正常、公网未认证 HTTP 401、自动恢复运行且巡检推进、panic/fatal/数据库锁/存储/监听/浏览器权限/登录失败/OAuth 401/403/5xx 聚合均为 0。有效路由仍为 `proxy_pass http://unix:/run/tls/auth/login.sock:;`。
- 最终只读复核全部 gate 通过：当前 release、binary、静态资源、数据库、回滚、有效路由、自动恢复、扫描推进、延迟验收和 SOURCE manifest 均为 `true`。临时上传与隔离候选已按精确 SHA-256 清理，正式/回滚 release、备份和验收证据保留。

完整方案、验证命令、证据文件和剩余技术边界见 [AUTO_RECOVERY_FIX_20261101.md](AUTO_RECOVERY_FIX_20261101.md)。全量仓库测试未运行；已完成定向 Go/race/vet、Node、shell、发布脚本和生产 gate 验证。

## 历史发布：近 90 天账号费用（2026-11-01）

sys1 当前 `20261031T194600Z-recent-cost`，前一版 `20261031T173803Z-login-limit-fingerprint` 保留，未回滚。账号表在原额度百分比下显示近 90 天 Sub2 账号口径费用（USD），详情显示统计时间、请求数及 Token；分页独立读取，失败显示待核对或保留上次金额，真实零才显示 $0.00。官方 deleted 账号远端清理后保留安全展示记录和可信费用关联，清空登录凭据及旧后台任务；手动清理仍删除本地行。无 schema 变更，未修改 Sub2，未提交/推送代码。

2026-11-01 03:52 北京时间延迟验收通过：active、NRestarts=0、health/ready/socket、有效 AUTH 路由及公网未认证 401 正常，发布窗口错误聚合为 0；巡检推进至 03:53:29。切换前后 8 类在途任务均为 0，备份完整。线上新接口读到近 90 天费用，包括已有 Sub2 删除记录的 AUTH 682（$366.97327228）；未触发真实账号登录或删除。定向 Go、Node、浏览器 1440/1100/390px 布局、隔离候选和前一版兼容通过，未跑全量套件。旧版已彻底清理的本地行不会自动恢复；滚动 90 天金额受 Sub2 日志保留影响，不是终身累计或官方账单。精确命令、hash 和证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。

保留此前代理多选、复活读取 Sub2 当前出口、完全收敛指纹默认值和登录累计限额移除。不要中断运行中的批次，不得未经用户指示自动切回旧版；瞬时在途为 0 不能证明浏览器整批结束。

## 历史发布：登录累计限额与指纹默认值（2026-11-01）

sys1 当前 `20261031T173803Z-login-limit-fingerprint`，前一版 `20261031T165353Z-egress-sync` 保留，未回滚。移除 AUTH 每 IP 30 次/10 分钟登录限额和整批限流倒计时；保留并发 10、上游限流与账号校验。新交付及网页/CLI 导出默认指纹完全收敛（full），已有账号和旧导入任务不自动覆盖。Sub2 代理多选及复活读取当前出口功能保留；AUTH 不修改 Sub2 出口。源码未提交/推送。

用户确认整批已结束后才切换；8 类在途任务均为 0，备份完整。隔离候选、服务身份浏览器 smoke、即时及 60 秒延迟检查通过，服务 active、NRestarts=0、health/ready/socket 正常、巡检继续、发布窗口错误聚合为 0。完整套件未运行，本次未做真实账号 OAuth/真实 Sub2 创建读回；精确命令和证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。未来发布不能靠瞬时在途为 0 推断浏览器整批结束，也不得未经用户指示自动切回旧版。


## 历史发布（2026-10-29）

- sys1 当前 `20261029T082510Z-usage-sync`；回滚 `20261029T080922Z-account-status-usage`。账号表已上线官方删除/停用与 Sub2 删除来源区分、Sub2 调度状态与 5h/7d/Sonnet/Fable/30d/Credits 额度、更新时间、同步未知态和生命周期详情；历史同步已补接 Sub2 批量额度接口，额度同步失败不会误判删除。本轮未提交/推送代码。
- 当前二进制 SHA-256 `7044744c346705772e5a92f7c11b3a724fec9b6249f632adecfe7235d13e9970`。线上 293 个账号中，104 个 Sub2 active、171 个明确 Sub2 删除、18 个未知；当前没有明确官方删除/停用事件，104 个 active 账号均有额度快照。候选与回滚 runtime smoke、旧版兼容、数据库完整性、即时及 60 秒延迟验收通过；正式服务 active/running、NRestarts=0、health/ready 正常、公网未认证 401、错误聚合0。
- 批量登录停止按钮仍保留：停止在途请求后先核对账号历史再决定可否重试，已提交的 Sub2 交付继续执行。13 个新账号交付记录仍按此前记录处理，交付完成与当前可调度状态不可混称。
- 13 个新账号交付记录均完成。2026-10-29 12:37 左右，12 个被 Sub2 临时限流，`effective_schedulable=false`，约北京时间 16:35-16:49 重置；不得把交付完成等同于当前全部可调度。完整发布证据见 `SYS1_DEPLOYMENT.md` 顶部。

## 历史发布（2026-10-14）

- sys1 当前 `20261013T174400Z-queue-retry`；源码 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` + 原工作区及本轮修复；SHA-256 `03850f2ae36968b6ec32f587cb1f11f208c06ac3c8f0c1a50c3eefd298f3cc3a`；源码指纹 `a7fccaf26680ecfcb1f029a8f3ba6cda837438589ef34fbf6363652c9834bd6c`。未提交/推送，回滚 `20261011T154920Z-group-empty-fix`。
- 用户截图中的 10 条交付已通过安全重启恢复：delivery 108–117、recovery 366–375 均完成，Sub2 16989–16998 均 active、schedulable/effective_schedulable=true。DB delivery 的 verifying 由关联 completed recovery 映射为完成，不要手工改行。
- 队列安全读取与事务尚未开始的领取，遇超时/BUSY/LOCKED 2 秒后重试；写入/提交不确定仍暂停。恢复领取原子化，资料修复暂停后停止同轮复检；前端显示真实暂停原因，日志保留固定错误分类。事故原 SQL 错误分类缺失，不能断言一定是超时。
- 同期 TasksMax2048 确实撞满并导致 Node 线程创建 assertion；已持久调整为4096，MemoryMax6GB、登录并发10。drop-in `/etc/systemd/system.control/openai-login.service.d/50-TasksMax.conf`，已 daemon-reload。
- 隔离候选/回滚兼容、服务身份浏览器 smoke 通过；AUTH616 候选与生产真实 IPv4 OAuth 成功（6,782/7,516ms），auto_deliver=false、AT/RT已加密保存。旧 smoke 账号279被明确返回 account_deactivated/403，隔离证据保留；后续不得再默认使用279验收。
- 01:57:20 上海时间最终验收：active/running、NRestarts0、health/ready/socket、有效AUTH路由、公网401、静态hash、巡检推进及回滚环境均通过；错误聚合0，pids.events max0。10条首检通过，9条二轮通过；616生产OAuth产生新凭据版本，其旧版二轮按保护取消。
- 重启/发布数据库备份均0600、完整性正常；最终源码、候选/生产OAuth、POST_LOGIN_ACCEPTANCE等证据保存在正式release。本轮临时上传与中间候选/版本已清理，操作脚本归档到正式release的 `operations/`，`CLEANUP.json` 留存；正式/回滚版本与备份保留。精确命令、限制、备份和回滚见 `SYS1_DEPLOYMENT.md` 顶部。定向race/vet/Node/构建通过，完整套件未运行，未生产故障注入或重新进行10并发压测。原有用户改动保留，无源代码同步。

## 历史发布：空分组契约修复（2026-10-12）

- sys1 当前 release：`20261011T154920Z-group-empty-fix`；源码 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` + working tree；二进制 SHA-256：`e0c54200ae34a346faa4bd1f3859bd35d79eacf4090e18c7d64f86e7a7efa3df`；回滚：`20261011T135659Z-group-diagnostics`。
- 候选、即时和延迟验收通过；服务 `active/running`、`NRestarts=0`，health/ready/socket 正常，公网未认证 `401`；备份 `/var/lib/openai-login/backups/accounts-before-20261011T154920Z-group-empty-fix.db`，临时文件已清理。
- 修复 Sub2 `omitempty` 空分组误判：三个分组字段全部省略代表合法空分组 `[]`。task `318`、AUTH account `561`、Sub2 account `16964` 已真实完成复核，Sub2 active、schedulable=true、`group_ids=[44,47,48]`，`groups` 字段也读回。
- task `224` 仍保留旧 `group_conflict` 结论，需在当前修复版本下重新读取和复核；没有执行管理员修改、目标分组覆盖或其他远端分组写入。

## 历史发布：浏览器与交付修复（2026-10-10）

当前 release 为 `20261010T104000Z-runtime-delivery-fix`，源码 `f2d13b2` 已推送 `origin/main`。修复 Node 复制权限导致浏览器秒失败，固化真实服务用户的 Node/driver/原生及有头、无头浏览器发布验收；无头模式可使用系统 Chrome，容器保留 bundled Chromium 回退；运行环境权限故障按持久退避自动恢复。

交付普通检测失败改为 30 秒、60 秒，再按 5 分钟起指数退避；仅作用于交付 `probe_failed` 且无 Retry-After，429 与上游等待要求保持。到期后受每 60 秒扫描和 worker 队列影响，不承诺精确 30 秒完成。账号锁忙只延后 2 秒、不计失败，避免队首阻塞；前端不再吞掉完成后的刷新，并显示重试原因及到期状态。

截图中的三条交付已有两条完成；另一条 Sub2 检测通过，但当前分组 [12,13] 与原提交 [12,44] 冲突，保持暂停并等待用户选择。此项不能通过缩短重试或绕过分组保护解决。完整发布、验证命令与证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下为历史记录。

## 历史发布：后续审查整改（2026-10-10）

线上 `20261010T093908Z-lifecycle-migration-hardening`，源码 `f28a11e` 已推送。完成多目标恢复/检测/历史隔离与旧库升级、优雅停机、数据库 readiness、历史缓存、实时监控及 Docker 持久化/浏览器运行环境整改。回滚保留 `20261010T044103Z-destination-isolation`。

09:55:39 UTC 延迟验收通过：active/running、无重启、health/ready/socket 正常、公网未认证401、巡检继续、严重错误聚合0；备份与回滚就绪。定向 Go/race/vet、脚本/Compose、Node 和 Linux 构建通过；未运行全仓套件、Docker 构建/容器浏览器或手动真实 OAuth。线上部分账号仍在等待/冷却或状态暂时未知。完整证据与精确命令见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下日期条目为历史发布记录。

## 浏览器 challenge 自动重试发布（2026-10-10）

当前线上 release 为 `20261009T155200Z-challenge-retry`，源码 `b6809f0`，Linux amd64 二进制 SHA-256：`ed2e4565641acc794c4a6057244e1832a98f2a599f098cfdc3e2c530f56b72b5`。初始导航、表单等待和 OAuth 回调的浏览器 challenge 最多等待 25 秒，未通过则按 `RetryCount` 和总超时重新创建浏览器重试；账号删除/停用、地区限制和普通 4xx 不重试。

16:04:39 UTC 切换，16:06:16 UTC 延迟验收确认 active/running、无重启、巡检推进、路由和数据库完整性通过，启动后错误聚合为 0。回滚 `20261009T054200Z-recovery-final`，本轮未手动触发真实 OAuth。

## 八项审查修复（2026-10-09 13:44:42 上海时间）

最终 release `20261009T054200Z-recovery-final`，来自已推送 `origin/main` 的 `53bde86`。修复恢复并发覆盖、临时登录失败重试、Sub2 字符串 401、重启暂停归属、交付熔断、失效分组纠正、重复开始和资料修复轮询。复现已保留为正式回归测试；远端身份标记、人工暂停、凭据版本与导入幂等保护保留。

13:46:24 延迟验收通过：active/running、NRestarts=0、巡检推进、路由与静态 hash 一致，启动以来错误聚合为零。数据库备份及回滚 `20261009T053050Z-recovery-delivery-races` 就绪。受影响测试、race、Node 和 vet 通过；未运行仓库完整套件，未手动触发真实 OAuth 或生产故障。精确命令、验收窗口与限制见 [部署记录](SYS1_DEPLOYMENT.md)。

## 401 自动恢复冷却修复（2026-10-09 12:25 上海时间）

北京时间 12:25:35 已上线 `20261009T042256Z-401-recovery`，代码来源为已推送 `origin/main` 的 `e96d8b7`。此前本地工作区全部 51 个文件已提交；成功交付/恢复不再进入 30 分钟失败冷却，新的凭据失效可以正常送检和恢复。失败退避、人工暂停、凭据版本与任务去重保护保留。

12:29:34 延迟复核 active/running、NRestarts=0、巡检推进、有效路由与静态资源一致，启动以来错误聚合为 0；数据库备份和回滚 `20261008T182221Z-compact-login` 就绪。定向 Go race、Node、Linux 构建、隔离候选与回滚兼容均通过；完整套件未运行，本轮未人为触发真实 OAuth 或新账号交付验收。精确命令与证据见 [部署记录](SYS1_DEPLOYMENT.md)。

## 批量登录紧凑布局（2026-10-09）

北京时间 02:25:11 已上线 `20261008T182221Z-compact-login`。桌面端双栏适配可用视口，前缀与登录并发并排，代理和处理模式折叠；表单与结果独立滚动，开始按钮固定在卡片底部。空输入不显示检查成功框，完整校验错误和键盘操作保留，手机端自然单列。

1280×650、1366×768、1440×900 默认首屏、390×844 手机和 683×384 窄窗口验收通过；50 条结果独立滚动，8 条错误和展开设置不挤走开始按钮。Node 定向交互、语法和 Linux 构建通过。仅前端布局与提示变化，未运行完整套件或本轮真实 OAuth/Sub2 写入测试；发布与精确命令见 [部署记录](SYS1_DEPLOYMENT.md)。

## 可选账号前缀（2026-10-09）

北京时间 02:03:15 已上线 `20261008T180135Z-account-prefix`。批量登录区新增可选“账号前缀”，新建 Sub2 名称为 `前缀_MMDDHHmm_邮箱`（北京时间）；空白使用 `AUTH`，支持中文，最多 32 字符。前缀可随交付参数保存为平台默认，并随本批任务和重试保留；关闭自动交付后，同页手动导入默认使用原批前缀，也可在确认时修改。已有账号保留原名。

前后端定向测试、窄窗口 UI、候选/回滚兼容和线上健康验收通过；历史账号 279 IPv4 完整登录成功（7,500ms）。未运行完整套件，也未为命名测试创建新的生产 Sub2 账号。精确验证与发布证据见 [部署记录](SYS1_DEPLOYMENT.md)。

## 首次自动复检提速（2026-10-09）

北京时间 01:36:59 已上线 `20261008T173543Z-fast-recheck`：恢复/交付成功后的首次实际 Sub2 复检由 5 分钟提前至 30 秒，第二次仍在完成后 30 分钟。已有最新完成任务中，尚未执行、未失败重试且仍采用旧 5 分钟计划的首检自动提前；保留失败退避。每分钟巡检用于筛选异常，不能代替实际凭据复检。30 秒为到期时间，繁忙时仍可能排队。

定向 race 测试、隔离候选/回滚兼容和线上健康检查通过；历史账号 279 再次完成默认 IPv4 OAuth（8,116ms，AT/RT 已保存）。完整测试套件未运行；本轮没有人为制造新的生产恢复任务来验证 30 秒复检，调度边界与迁移由定向测试覆盖。发布和精确命令见 [部署记录](SYS1_DEPLOYMENT.md)。

## 默认 IPv4 直连（当前配置）

2026-10-08 08:30 UTC（北京时间 16:30）按用户要求移除 sys1 的默认代理：`/etc/openai-login/proxy.env` 中 `OPENAI_LOGIN_PROXY` 为空。当前 `20261009T054200Z-recovery-final` 的登录代理框留空即走服务器 IPv4；Chrome 与 token 交换使用同一 tcp4 出口，检测页面也默认选择 IPv4，自动检测无代理时自行选择直连。账号自己保存或本次明确填写的代理仍优先。

旧配置仅以 0600 权限保存在 `/var/lib/openai-login/backups/proxy.env-before-20261008T082500Z-ipv4-default`，未继续注入运行进程。当前回滚版本为 `20261009T053050Z-recovery-delivery-races`。空代理网络检查返回 `mode=direct,reachable=true`；HTTP 客户端仍收到认证站 403，这不等同于浏览器 OAuth 失败，也不证明完整登录成功。2026-10-08 08:46 UTC 已按用户授权使用历史账号 279 实测默认 IPv4：完整 OAuth 成功，耗时 7,224ms，AT/RT 已加密保存，attempt 290 为 success；日志确认无外部代理回退、无 challenge。先测的账号 278 两次在 MFA 返回 incorrect_code，具体资料/验证方式原因待确认。完整记录见 [IPv4 实测](SYS1_IPV4_OAUTH_DIAGNOSIS.md)。单次成功不代表长期成功率。

## 2026-10-08 账号流程最新发布

北京时间 16:10 发布 `20261008T080500Z-account-workflows`；16:12 延迟复核 active/running、NRestarts=0、实际二进制匹配、静态资源/有效路由/socket 正常、自动恢复开启且巡检继续。备份完整，回滚 `20261008T053100Z-account-automation` 就绪。

新增批量 Sub2 分组/优先级/并发与平台默认；新建账号检测通过再入组，已有账号保留原配置。恢复/交付完成后约 30 秒和 30 分钟持久复检；修改密码/TOTP/代理后后台续跑，移交恢复与消费修改任务原子提交。未知创建只核对原标记，15 分钟仍查不到则明确提示人工核对；不盲目重复创建。

`direct` 显式选择 sys1 IPv4 直连，空代理仍用默认代理。发布前原生 Chrome 默认走 IPv6 被 challenge；两个 IPv4 A 节点均进入邮箱页。发布后 08:46 UTC 已补齐历史账号完整直连 OAuth 实测，见上节。历史 IPv4 challenge 证据仍有效，不能承诺永久免验证。

定向测试/验收命令见 [账号流程验证记录](ACCOUNT_WORKFLOW_VERIFICATION.md)，网络证据见 [IPv4 OAuth 诊断](SYS1_IPV4_OAUTH_DIAGNOSIS.md)。完整测试套件未运行，未手动使用真实账号验收新建分组导入。

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
- 当前 release：`20261029T080922Z-account-status-usage`
- 当前来源：本地提交 `33d64a0401599837246f647d4d5fdf554e8a0015`
- 当前二进制 SHA-256：`39581330bce3a1775522d52168d5f71798422901290ce3f096fd18542c77db4d`
- 回滚 release：`20261029T080812Z-account-lifecycle-quota`
- 浏览器运行方式：sys1 上的 Google Chrome + Xvfb，有头模式；浏览器流程实际发生在 sys1

2026-10-08 已发布 `20261008T053100Z-account-automation`：自动断点续跑、按失败原因持久退避、登录成功后后台交付 Sub2、按账号去重的“需要处理”提醒。取消失败 3 次硬停止，交付不计入复活次数。定向 race/Go、Node、vet、桌面和窄屏 fixture、候选迁移及旧二进制兼容均通过；完整测试套件未运行。发布和延迟验收见 `SYS1_DEPLOYMENT.md`。

2026-10-08 已发布自动恢复运行状态修复：设置接口和页面区分开启选择与实际运行，存储故障等真实停机显示原因，临时巡检错误仍自动重试；不新增账号次数停用策略。发布后 `auto_recovery_enabled=true,running=true`，备份及验证见 `SYS1_DEPLOYMENT.md`。

历史记录：2026-10-07 09:33 UTC 发布 `20261007T092840Z-recovery-accessibility`，回滚为 `20261007T090920Z-userid-recovery`；本轮当前备份为 `/var/lib/openai-login/backups/accounts-before-20261007T092840Z-recovery-accessibility.db`。当前服务 `active/running`、`NRestarts=0`，本机和宿主入口 socket 健康正常，公网未认证返回 HTTP 401。账号 203 的 task 64、65 已真实恢复，task 64 完成后凭据版本 233 曾再次被上游撤销；task 65 写入版本 234 后，09:34 UTC 的独立 AUTH 模型检测 719 返回 HTTP 200、`outcome=ok`。完整时间线、备份和回滚证据见 `SYS1_DEPLOYMENT.md`。

2026-10-08 05:30 UTC 最新复核：最终版服务 active/running、无重启或严重错误、回滚就绪；49 个账号、48 条恢复任务。账号 201 / task 60 已重新排队自动登录；账号 203/222 当前上游明确停用，不能沿用前一天成功记录判断可用。未手动触发真实新账号交付验收。

当前恢复不以旧 workspace ID 或旧、新 JWT `chatgpt_user_id` 的相等作为阻断条件；保留邮箱、完整凭据、Sub2 原账号 ID/邮箱/绑定标记和写回后新元数据核对。历史卡片新增成功复活次数、复活记录浮窗、Sub2 在池/不在池/待核对标记和紧凑操作布局；缺失池状态统一显示“待核对”，浮窗支持 Tab/Shift+Tab 键盘进出。账号 216 与 203 均有真实恢复成功证据；账号 203 还通过独立 AUTH 模型检测。

此前 `20261005T124744Z-sub2-auth401-recovery` 已上线 AUTH 401 恢复判定、批量登录结果导入的终态等待和展示、Sub2 导入名称 `AUTH_MMDDHHmm_email`、AUTH 401 与 Sub2 凭据/调度状态的独立呈现、原始 `schedulable`/`effective_schedulable` 及限流/过载/临时暂停信息展示。历史页已有“立即检测”快捷入口；“账号检测”Tab 仍保留批量检测、进度和单账号详情/恢复。

## 当前账号关联和恢复行为

历史刷新读取 Sub2 状态；无绑定时分页读取 OpenAI OAuth 账号，以凭据中的邮箱精确匹配，并核对 AUTH 已知的 ChatGPT 账号 ID。唯一匹配保存为本地关联，不重新导入，也不改 Sub2 名称、分组或来源标记。自定义名称不影响匹配；歧义、身份冲突、查询失败显示原因，已关联账号明确返回 404 才标记删除。状态展示、检测及恢复共用当前目标站点的绑定。

状态同步保留 Sub2 原始 `schedulable` 与 `effective_schedulable` 字段；历史行按有效值显示可调度性，并显示未来限流、过载或临时暂停的截止时间/原因。AUTH 的 HTTP 401 只说明 AUTH 当前本地凭据鉴权失败，不覆盖 Sub2 自身凭据或调度结论；两者会同时展示，便于决定是否恢复。

“自动检测恢复”保存到 SQLite，服务重启后保留；未保存时使用 `AUTH_AUTO_RECOVERY` 默认值。开启立即扫描，此后每 60 秒在后端巡检，关闭网页不影响运行；页面显示上次/下次扫描、摘要、检测及恢复进度。仍在 Sub2 池中且未人工暂停的账号，只有 AUTH 当前确认 401、令牌过期、凭据失效或缺少本地令牌后才直接 AUTH 重新登录，写回原 Sub2 账号并验证、恢复调度。Sub2 `status=error` 或 `schedulable=false` 不单独证明 401；正常、禁用、删除、未知及人工暂停的账号不会仅凭 Sub2 状态恢复。

历史页提供“检测并恢复”和可恢复任务的“继续恢复”，人工操作不依赖自动开关；普通“重新登录”只更新 AUTH，不写回 Sub2。复用检测冷却和任务去重；临时故障按持久化原因退避，取消原先失败 3 次的硬性停止。已保存的新凭据优先断点续跑，明确失效才重新登录。Sub2 异常不等于 401，AUTH 检测正常时不强制重登；异常加暂停仍不能完全判定人工/自动暂停来源。真实账号业务验收与上线证据以本次部署记录为准，不沿用历史成功结果。

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
