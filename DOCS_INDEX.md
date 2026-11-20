# KKAI_AUTH 文档索引

## 本地实现 · 账号退池、终态清理与普通重登（2026-11-20）

普通历史重登只保存到 AUTH。旧协议任务在重启前退役，显式交付、Free 退池及废弃号清理使用既有 Sub2 接口。详见[账号生命周期实施记录](docs/account-lifecycle-20261119.md)。

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

这是新对话的入口文件。新对话不要先依赖旧聊天记录，按下面顺序读取当前文档。

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


## 历史发布：账号生命周期与 Sub2 额度同步（2026-10-29）

sys1 当前 `20261029T082510Z-usage-sync`，Linux amd64 二进制 SHA-256 `7044744c346705772e5a92f7c11b3a724fec9b6249f632adecfe7235d13e9970`，回滚 `20261029T080922Z-account-status-usage`。账号表区分官方删除/停用和 Sub2 删除，展示 Sub2 调度状态、额度窗口、Credits、更新时间及同步未知态；历史同步已接入批量额度接口，详情页保留生命周期事件。线上 293 个账号中，104 个 Sub2 active、171 个明确 Sub2 删除、18 个未知；当前没有明确官方删除/停用事件。候选、回滚兼容、即时及 60 秒延迟验收通过，正式服务 active/running、NRestarts=0、health/ready 正常、公网未认证 401、错误聚合0。定向验证通过，完整套件未运行。详细证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。

## 历史发布：队列暂停恢复与修复（2026-10-14）

sys1 当前 `20261013T174400Z-queue-retry`，二进制 SHA-256 `03850f2ae36968b6ec32f587cb1f11f208c06ac3c8f0c1a50c3eefd298f3cc3a`，回滚 `20261011T154920Z-group-empty-fix`。10条等待交付已恢复并完成，Sub2 16989–16998 均正常可调度；队列安全读取/领取临时等待自动重试、真实暂停提示和同轮停止保护已上线，TasksMax持久提升4096。

候选/生产真实OAuth、服务身份浏览器smoke及01:57:20延迟复核通过，巡检继续、错误聚合0、备份/回滚就绪。定向验证通过，完整套件未运行；精确命令、事故推断边界和验收限制见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。源码与原有工作区改动保留，本轮未提交/推送。

## 历史发布：空分组契约修复（2026-10-12）

当前 sys1 release 为 `20261011T154920Z-group-empty-fix`，源码 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` + working tree，二进制 SHA-256 为 `e0c54200ae34a346faa4bd1f3859bd35d79eacf4090e18c7d64f86e7a7efa3df`，回滚为 `20261011T135659Z-group-diagnostics`。候选、即时和延迟验收通过；active/running、NRestarts=0、health/ready/socket、公网未认证 401 均已复核。备份 `/var/lib/openai-login/backups/accounts-before-20261011T154920Z-group-empty-fix.db`，临时文件已清理。

本轮修复 Sub2 `omitempty` 空分组误判：`group_ids`、`groups`、`account_groups` 全部省略代表合法空分组 `[]`。task `318`、AUTH account `561`、Sub2 account `16964` 已真实完成，Sub2 active、schedulable=true，`group_ids=[44,47,48]` 且 `groups` 字段读回。task `224` 的旧冲突结论仍需在当前版本下重新读取和复核，尚未执行管理员修改或远端分组覆盖。详细部署证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

下面日期更早的条目是历史发布记录。

## 历史发布：浏览器与交付修复（2026-10-10）

当前 release 为 `20261010T104000Z-runtime-delivery-fix`，源码 `f2d13b2` 已推送 `origin/main`。修复 Node 复制权限导致浏览器秒失败，固化真实服务用户的 Node/driver/原生及有头、无头浏览器发布验收；无头模式可使用系统 Chrome，容器保留 bundled Chromium 回退；运行环境权限故障按持久退避自动恢复。

交付普通检测失败改为 30 秒、60 秒，再按 5 分钟起指数退避；仅作用于交付 `probe_failed` 且无 Retry-After，429 与上游等待要求保持。到期后受每 60 秒扫描和 worker 队列影响，不承诺精确 30 秒完成。账号锁忙只延后 2 秒、不计失败，避免队首阻塞；前端不再吞掉完成后的刷新，并显示重试原因及到期状态。

截图中的三条交付已有两条完成；另一条 Sub2 检测通过，但当前分组 [12,13] 与原提交 [12,44] 冲突，保持暂停并等待用户选择。此项不能通过缩短重试或绕过分组保护解决。完整发布、验证命令与证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下为历史记录。

## 历史发布：后续审查整改（2026-10-10）

线上 `20261010T093908Z-lifecycle-migration-hardening`，源码 `f28a11e` 已推送。完成多目标恢复/检测/历史隔离与旧库升级、优雅停机、数据库 readiness、历史缓存、实时监控及 Docker 持久化/浏览器运行环境整改。回滚保留 `20261010T044103Z-destination-isolation`。

09:55:39 UTC 延迟验收通过：active/running、无重启、health/ready/socket 正常、公网未认证401、巡检继续、严重错误聚合0；备份与回滚就绪。定向 Go/race/vet、脚本/Compose、Node 和 Linux 构建通过；未运行全仓套件、Docker 构建/容器浏览器或手动真实 OAuth。线上部分账号仍在等待/冷却或状态暂时未知。完整证据与精确命令见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下日期条目为历史发布记录。

## 历史发布：Challenge 提交竞态与删除清理保护（2026-10-10）

`20261010T013500Z-retry-challenge-cleanup` 已上线，源码 `a61b7b8`，Linux amd64 二进制 SHA-256：`6cb8ffd46140e1c67624da2dbda794d5bfc847be920efd3dbdfb96f135a5a75c`。邮箱/密码/MFA/Consent Challenge 提交进入外层重试；临时网络错误、超大 `Retry-After`、账号删除清理 lease/凭据版本/operation CAS 已修复。`2026-10-10T02:16:03Z` 延迟验收通过，服务、socket、401、自动恢复扫描和错误聚合正常；回滚为 `20261010T001828Z-retry-policy-final`。真实浏览器 Challenge 未执行（缺 Playwright driver），定向单元/race 测试通过。

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

## 新对话阅读顺序

1. `NEW_CHAT_CONTEXT.md`：项目交接上下文、已完成工作、当前阻塞和下一步。
2. `SYS1_DEPLOYMENT.md`：sys1 线上部署的唯一权威记录。
3. `README.md`：当前本地和 sys1 网页版的使用方法。
4. `STATUS.md`：当前状态速览。
5. `GUIDE.md`：CLI 参数和导出格式参考。

## 账号状态检测

- [实施与验收记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)：已上线，包含实际改动、定向测试、sys1 直连/代理与真并发证据。
- [详细设计](ACCOUNT_STATUS_CHECK_PLAN.md)：接口、存储、错误分类与边界设计。

## 当前事实

- 桌面项目目录：`/Users/tokk/Desktop/KKAI_AUTH`
- sys1 服务：`openai-login.service`
- 线上入口：`https://auth.kkrich.ltd`
- Mac 通过 `./open-sys1.sh` 建立 SSH 隧道，浏览器访问 `http://127.0.0.1:18082`
- sys1 本机服务只监听 `127.0.0.1:18082`
- 当前 release：`20261029T082510Z-usage-sync`
- 当前来源：`HEAD + working tree`，未提交/推送
- 当前二进制 SHA-256：`7044744c346705772e5a92f7c11b3a724fec9b6249f632adecfe7235d13e9970`
- 回滚 release：`20261029T080922Z-account-status-usage`
- 数据库备份（当前 release 发布前）：当前 release 对应备份，0600，integrity_check=ok
- 数据库和密钥在 `/var/lib/openai-login/data/`，不放在 release 目录
- 最近一次核验服务为 `active`、`NRestarts=0`，本机 `/health`/`/ready` 正常，公网未认证返回 HTTP 401；正式 release、延迟验收和回滚证据已保留
- AUTH → Sub2 导入已上线，线上 `/api/history` 已确认 `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改。
- 已补充外部账号关联、历史“检测并恢复”、后台立即及每 60 秒巡检；开关持久化并保留用户当前开启选择。仍在 Sub2 池中且未人工暂停的账号，只有 AUTH 当前确认凭据问题后才直接登录、写回原账号并验证调度；Sub2 异常状态本身不单独触发恢复。
- 生产自动恢复 `enabled/running=true`；2026-10-11 22:11 上海时间快照：待恢复 15、提交检测 0、直接提交恢复 0、等待或冷却 15、状态暂时未知 12，巡检持续。
- 当前 release 允许账号 203 在 workspace 和 JWT 用户 ID 变化后恢复，仍核对邮箱、完整凭据和 Sub2 原账号绑定；复活记录浮窗支持 Tab/Shift+Tab。账号 203 的版本 234 曾通过独立 AUTH 检测 719（历史证据，非当前可用证明）；健康、回滚和时间线详见 [部署记录](SYS1_DEPLOYMENT.md)。
- AUTH 账号检测已上线；sys1 IPv4 直连与默认代理模型请求均成功，候选并发 2 的两个请求重叠约 3 秒；正式版正确区分正常与真实 429 额度不足。浏览器 OAuth 登录与模型检测的证据不可混用。
- 历史浏览器 OAuth 验收：sys1 IPv4 直连授权请求在输入邮箱前返回 `403 + cf-mitigated: challenge`；此前 7275 认证代理真实账号验收连续 4 次成功并完成 token 交换；当时默认代理为 `45.39.200.204:7269`，现已按用户要求移除

2026-10-08 已发布 `20261008T053100Z-account-automation`：自动断点续跑、按失败原因持久退避、登录成功后后台交付 Sub2、按账号去重的“需要处理”提醒。取消失败 3 次硬停止，交付不计入复活次数。定向 race/Go、Node、vet、桌面和窄屏 fixture、候选迁移及旧二进制兼容均通过；完整测试套件未运行。发布和延迟验收见 `SYS1_DEPLOYMENT.md`。

2026-10-08 已发布自动恢复运行状态修复：设置接口和页面区分开启选择与实际运行，存储故障等真实停机显示原因，临时巡检错误仍自动重试；不新增账号次数停用策略。发布后 `auto_recovery_enabled=true,running=true`，备份及验证见 `SYS1_DEPLOYMENT.md`。

## 本轮批量导入与检测入口变更

- 批量登录结果导入 Sub2 会等待已受理任务的成功/失败/仍处理中终态，并在批量登录页显示进度摘要；“已受理”不会直接当作“成功”。
- 新建 Sub2 账号名称使用 `前缀_MMDDHHmm_email`，空白前缀使用 `AUTH`；来源标记和账号身份核对仍独立保留。
- AUTH 当前凭据的 HTTP 401 与 Sub2 凭据/调度状态分开记录。历史行可同时显示本地凭据失效待处理和 Sub2 状态。
- Sub2 状态同步读取并保留 `schedulable`、`effective_schedulable`，并展示限流、过载、临时暂停的时间窗口/原因。
- 历史页新增“立即检测”快捷入口；“账号检测”Tab 继续保留批量检测、进度和单账号详情/恢复，作为完整检测页面。

本节描述当前线上行为；线上 release、健康与验收事实以 `SYS1_DEPLOYMENT.md` 为准。

## 文档优先级

发生冲突时按以下优先级处理：

1. `SYS1_DEPLOYMENT.md`（线上事实）
2. `NEW_CHAT_CONTEXT.md`（工作交接状态）
3. `README.md` 和 `STATUS.md`（当前使用说明）
4. 其他文档（CLI、历史优化、安全审查或 Docker 参考）

`DEPLOY.md`、`WEB_GUIDE.md`、`HOW_TO_USE.md`、`OPTIMIZATION_SUMMARY.md`、`SECURITY_REVIEW.md`、`DOCKER_DEPLOY.md` 中可能保留历史命令或通用示例；它们不能覆盖 sys1 专用文档中的路径、端口、release 或验收结论。

## 安全边界

文档不保存账号密码、TOTP、代理凭据、access token 或 refresh token。新对话需要测试账号时，必须由用户在当次会话重新提供，不能从文档推断或恢复。
