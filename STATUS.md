# KKAI_AUTH 当前状态

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


## 历史发布：账号表视觉改版（2026-10-29）

sys1 当前运行 `20261029T101042Z-account-table-visual`，回滚为 `20261029T082510Z-usage-sync`。账号表视觉改版已上线，保留官方删除/停用与 Sub2 删除的区分、Sub2 调度状态和额度；同步失败仍为未知态，不误判删除。上次数据核对时，293 个账号中 104 个 Sub2 active、171 个明确 Sub2 404 删除、18 个状态未知，没有明确官方删除/停用事件。批量登录仍可中途停止；在途请求取消后先核对持久历史，未确认前禁止重试，已提交交付继续由后台处理。候选/回滚 runtime smoke、数据库完整性、静态资源、即时和延迟验收均通过；正式服务 active/running、NRestarts=0、health/ready 正常、公网未认证 HTTP 401。源码未提交/推送，完整证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。

此前 13 个号均已交付 Sub2；2026-10-29 12:37 左右其中 12 个仍处于临时限流，当前不可有效调度，预计北京时间 16:35-16:49 解限。以下为历史发布记录。

## 历史发布：队列恢复与修复（2026-10-14）

当前线上 `20261013T174400Z-queue-retry`，源码 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` + working tree，二进制SHA-256 `03850f2ae36968b6ec32f587cb1f11f208c06ac3c8f0c1a50c3eefd298f3cc3a`，源码指纹 `a7fccaf26680ecfcb1f029a8f3ba6cda837438589ef34fbf6363652c9834bd6c`，回滚 `20261011T154920Z-group-empty-fix`。本轮未提交/推送源码。

10条等待交付已重启恢复完成：delivery108–117、recovery366–375、Sub2 16989–16998；最终远端均active且可调度。修复临时队列读取/事务未开始的领取超时导致永久暂停，安全重试等待2秒；领取事务原子化，写入/提交失败仍保留暂停保护，资料修复暂停后不再派发同轮复检。前端明确显示后台暂停，日志记录固定错误分类。事故原SQL错误分类缺失，不能确定其一定为超时。

TasksMax由2048持久提高到4096，保留MemoryMax6GB和登录并发10。候选/回滚/生产服务身份浏览器smoke、真实AUTH616候选和生产IPv4 OAuth、数据库完整性及01:57:20延迟复核均通过；active/running、NRestarts0、巡检持续、入口/静态资源/回滚正常，发布窗口错误聚合0。10条首检通过；9条二轮通过，616在OAuth验收更新本地凭据后按原有版本保护取消旧二轮。旧验收账号279被上游返回account_deactivated/403，未混入生产发布故障结论。

定向race、vet、Node、shell和Linux构建通过；完整仓库套件未运行，未在生产故意制造存储失败或重新进行10并发压测。精确验证命令、事故证据、备份、资源回退和发布回滚见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md) 顶部。以下早期发布条目为历史记录。

## 空分组契约修复发布（2026-10-11）

当前线上 release 为 `20261011T154920Z-group-empty-fix`，源码为 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` + working tree，二进制 SHA-256 为 `e0c54200ae34a346faa4bd1f3859bd35d79eacf4090e18c7d64f86e7a7efa3df`，回滚为 `20261011T135659Z-group-diagnostics`。候选、即时和延迟验收通过；服务 `active/running`、`NRestarts=0`，health/ready/socket 正常，公网未认证返回 HTTP 401，临时文件已清理。

本轮修复 Sub2 `omitempty` 空分组误判，三个分组字段全部省略现在表示合法空分组 `[]`。task `318`、AUTH account `561`、Sub2 account `16964` 已真实完成复核，Sub2 为 active、schedulable=true、`group_ids=[44,47,48]`，`groups` 字段也读回。task `224` 仍保留旧结论，需在当前修复版本下重新读取和复核；尚未执行管理员修改或远端分组写入。详细证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

## 历史发布：浏览器与交付修复（2026-10-10）

当前 release 为 `20261010T104000Z-runtime-delivery-fix`，源码 `f2d13b2` 已推送 `origin/main`。修复 Node 复制权限导致浏览器秒失败，固化真实服务用户的 Node/driver/原生及有头、无头浏览器发布验收；无头模式可使用系统 Chrome，容器保留 bundled Chromium 回退；运行环境权限故障按持久退避自动恢复。

交付普通检测失败改为 30 秒、60 秒，再按 5 分钟起指数退避；仅作用于交付 `probe_failed` 且无 Retry-After，429 与上游等待要求保持。到期后受每 60 秒扫描和 worker 队列影响，不承诺精确 30 秒完成。账号锁忙只延后 2 秒、不计失败，避免队首阻塞；前端不再吞掉完成后的刷新，并显示重试原因及到期状态。

截图中的三条交付已有两条完成；另一条 Sub2 检测通过，但当前分组 [12,13] 与原提交 [12,44] 冲突，保持暂停并等待用户选择。此项不能通过缩短重试或绕过分组保护解决。完整发布、验证命令与证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下为历史记录。

## 历史发布：后续审查整改（2026-10-10）

线上 `20261010T093908Z-lifecycle-migration-hardening`，源码 `f28a11e` 已推送。完成多目标恢复/检测/历史隔离与旧库升级、优雅停机、数据库 readiness、历史缓存、实时监控及 Docker 持久化/浏览器运行环境整改。回滚保留 `20261010T044103Z-destination-isolation`。

09:55:39 UTC 延迟验收通过：active/running、无重启、health/ready/socket 正常、公网未认证401、巡检继续、严重错误聚合0；备份与回滚就绪。定向 Go/race/vet、脚本/Compose、Node 和 Linux 构建通过；未运行全仓套件、Docker 构建/容器浏览器或手动真实 OAuth。线上部分账号仍在等待/冷却或状态暂时未知。完整证据与精确命令见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。以下日期条目为历史发布记录。

## 浏览器 challenge 自动重试发布（2026-10-10）

已上线 `20261009T155200Z-challenge-retry`，源码 `b6809f0`，Linux amd64 二进制 SHA-256：`ed2e4565641acc794c4a6057244e1832a98f2a599f098cfdc3e2c530f56b72b5`。Cloudflare challenge 在当前浏览器内最多等待 25 秒，未通过时按 `RetryCount`、退避和总超时重新创建浏览器；账号删除/停用、地区限制和普通 4xx 仍保持终止。

16:04:39 UTC 切换，16:06:16 UTC 延迟验收通过：服务 active/running、`NRestarts=0`、自动恢复首轮扫描推进、错误聚合为 0，路由/静态资源/socket/数据库备份和回滚均核对通过。回滚为 `20261009T054200Z-recovery-final`；本轮未手动触发真实 OAuth。

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

## 当前结论

项目源码在 `/Users/tokk/Desktop/KKAI_AUTH`。当前 sys1 release 为 `20261013T174400Z-queue-retry`，基于本地 `41e6f6d82571afba2d24dcfea3d65e3c6102625e` 及工作区改动；回滚为 `20261011T154920Z-group-empty-fix`。账号交付、分组契约及队列安全重试修复已上线，本轮10条交付已完成。Sub2源码和线上版本未改。真实OAuth、Unix socket、延迟验收和回滚证据均已核对，完整发布证据见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

恢复不再以旧 workspace ID 或旧、新 JWT `chatgpt_user_id` 相等阻断账号 203；仍核对邮箱、完整凭据、Sub2 原账号 ID/邮箱/绑定标记，并在写回后核验新 workspace、organization、plan、expires_at 和凭据状态。历史卡片增加成功复活次数、尝试次数、Sub2 池状态和可悬停/聚焦/点击的最近 100 条复活记录浮窗，键盘可用 Tab/Shift+Tab 进入和退出。

2026-10-08 已发布 `20261008T053100Z-account-automation`：自动断点续跑、按失败原因持久退避、登录成功后后台交付 Sub2、按账号去重的“需要处理”提醒。取消失败 3 次硬停止，交付不计入复活次数。定向 race/Go、Node、vet、桌面和窄屏 fixture、候选迁移及旧二进制兼容均通过；完整测试套件未运行。发布和延迟验收见 `SYS1_DEPLOYMENT.md`。

## 自动恢复运行状态修复（2026-10-08）

已修复后台因存储故障停派发但页面仍报“后台运行”：新增实际运行状态与阻塞原因，保留用户开关；不更改自动复活、写回、验证及恢复调度策略。11:56 北京时间发布后开关和实际运行均为 true，首轮巡检完成。精确验证命令、备份和回滚见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

## 本轮批量导入与状态展示变更（2026-10-05）

以下行为已随 `20261005T124744Z-sub2-auth401-recovery` 上线。

- 从批量登录结果导入 Sub2 时，前端等待已受理任务进入终态，并持续显示总数、已完成数、成功数、失败数和仍在队列中的数量；超时仍明确标记为待核对，不把“已受理”当作成功。
- 新建 Sub2 账号名称统一为 `AUTH_MMDDHHmm_email`（服务端使用 Asia/Shanghai 时间）；来源仍通过独立来源标记和账号身份核对确认。
- AUTH 检测的 HTTP 401 与 Sub2 返回的凭据/调度状态分开记录。Sub2 显示正常而 AUTH 当前凭据返回 401 时，历史行同时保留两条证据并显示“本地凭据失效待处理”，恢复入口仍可用。
- Sub2 状态同步保留原始 `schedulable` 和 `effective_schedulable`，以有效字段显示当前可调度性，并展示未来限流、过载、临时暂停的截止时间和原因；仍在池中且未人工暂停的账号，只有 AUTH 当前确认 401/凭据失效后才进入恢复。
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
| 当前 release | `20261029T082510Z-usage-sync` |
| 回滚 release | `20261029T080922Z-account-status-usage` |
| 当前来源 | `HEAD + working tree`，未提交/推送 |
| 数据库 | `/var/lib/openai-login/data/accounts.db` |
| 密钥 | `/var/lib/openai-login/data/accounts.key` |
| 最近健康检查 | active/running，NRestarts=0，本机 `/health`/`/ready` 正常，公网未认证 HTTP 401 |
| 数据库备份（当前 release 发布前） | 当前 release 对应备份，0600，integrity_check=ok |
| 当前二进制 SHA-256 | `7044744c346705772e5a92f7c11b3a724fec9b6249f632adecfe7235d13e9970` |
| 导入配置 | `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改 |
| 公网身份验证 | Unix socket HTTP 200，公网未认证 HTTP 401；认证后公网业务未验证 |
| 本轮功能验收 | 历史同步已补接 Sub2 批量额度接口；候选、即时和 60 秒延迟验收通过；18 个账号状态暂时未知，等待后续 Sub2 成功同步 |
| 自动恢复设置 | 生产自动恢复 `enabled` 并保存到 SQLite，立即及每 60 秒后台扫描，页面关闭后继续 |
| 线上快照（2026-10-11 22:38 上海时间） | 自动恢复 enabled/running，待恢复 15，提交检测 0，直接提交恢复 0，等待或冷却 15，状态暂时未知 12；任务 224 需人工处理 |
| sys1 IPv4 模型检测（历史） | 此前真实 HTTP200 / 完整模型完成事件，1,982ms |
| 默认代理模型检测（历史） | 此前单账号连续两次成功；候选两个账号并发2正常，重叠3,035ms |
| 浏览器直连历史限制 | 授权流程曾收到 `403 + cf-mitigated: challenge`；不可将该浏览器结论套用到模型检测 |
| sys1 默认线路 | 默认代理已移除，登录/自动检测使用 IPv4 直连，检测页默认 IPv4；历史账号完整 OAuth 已实测成功 |

## `20261007T092840Z-recovery-accessibility` 发布验收

- 09:33 UTC 发布，回滚 release 为 `20261007T090920Z-userid-recovery`；切换前 SQLite 备份为 724992 字节、0600、`integrity_check=ok`，旧 release 和运行时依赖保留。
- 当前二进制与线上 SHA-256 一致；线上 HTML、`client.js`、`account-checks.js` 分别为 `4a856145b05a3dccb9c3d409996b1affc76111ba1b8dc2ca4a428e1eb02d0d82`、`1643496fb3e5fe912ebb3b922862fa1562bb3b57080b7c5581d6d36ac35469c9`、`1c37fe582928c755f935f4e19783443262371c8c33c6b436c37614671ec1bb41`。
- 定向 Go 测试、`go vet`、`node cmd/server/client.test.cjs`、`node --check cmd/server/client.js`、Linux amd64 构建、`git diff --check` 通过；未运行完整测试套件。
- 账号 203 的旧任务 53/56/59 因身份比对失败；task 64 完成后，凭据版本 233 于 09:19 UTC 在独立 AUTH 检测中再次收到 401 `token_revoked`。task 65 已写入版本 234 并恢复 Sub2 调度；09:34 UTC 独立 AUTH 检测 719 对版本 234 返回 HTTP 200、`outcome=ok`。上游撤销版本 233 的原因尚无日志证据。

## `20261007T082500Z-history-status` 历史发布验收

- 发布后服务 `active/running`、`NRestarts=0`，`/health` 返回 `status=ok,max_concurrent=10`；宿主 socket `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock` 返回 HTTP 200，公网未认证返回 HTTP 401。
- 本地静态资源与线上响应 hash 一致：HTML `4a856145b05a3dccb9c3d409996b1affc76111ba1b8dc2ca4a428e1eb02d0d82`、`client.js` `417d7f4ee816dba37c94967152dcf5dbaa33e903149dde6c18601b0286ddc8c7`、`account-checks.js` `1c37fe582928c755f935f4e19783443262371c8c33c6b436c37614671ec1bb41`。
- 数据库 `integrity_check=ok`，自动恢复开关为 `1`；08:49 UTC 延迟复核快照为账号 30、imports 27、checks 717、恢复任务 28（completed 17、unknown 11），复查时所有任务队列均为 0。发布窗口严重错误日志计数为 0。
- 验证通过：定向 Go 测试、`go vet`、Node 语法/客户端回归/账号检查、恢复浮窗 fixture 和 `git diff --check`；本次文案修正后的 Linux amd64 构建 SHA-256 为 `7e4fb6ddef77fb76497fd097c796db7fd81848b95340a0de4fafafb6e2a28ef3`。完整测试套件未运行。
- 延迟线上复核确认账号 216 的 task 62 已真实完成：旧 workspace `43525e7a-5938-4af7-bba5-5ef3e6ab336a` 更新为新 workspace `52e729ef-5bd4-4ce6-83ba-1762c2302664`，Sub2 检测通过并重新开启调度；当前恢复任务为 completed 16、unknown 11。工作区切换恢复已有真实线上成功证据。

## `20261005T124744Z-sub2-auth401-recovery` 发布验收

- release commit：`4591e80`；Linux amd64 二进制 SHA-256：`d869c0c16498a8ab362e0be78c3aff617eec0ab96a469244244cd49099006714`。
- 发布前备份：`/var/lib/openai-login/backups/accounts-before-20261005T124744Z-sub2-auth401-recovery.db`；发布后服务保持 `active/running`、`NRestarts=0`，`/health` 返回 `status: ok`，入口 socket HTTP 200；回滚 release `20261005T105500Z-sub2-status-ui` 保留可用。
- 公网未认证请求返回 HTTP 401；这只证明入口保护，不代表认证后业务验收。
- 生产自动恢复为 `enabled`；117/118/119 的 AUTH current 检测均为 HTTP 401 `credential_revoked`，对应 Sub2 状态为 `active`、`schedulable=false`、`effective_schedulable=false`。自动恢复任务状态为 `unknown`，因此保持停止调度。
- 生产状态快照为 history 31、imports 27、statuses 31；本轮上线事实包括 AUTH 401 恢复判定、批量导入终态展示、Sub2 调度字段与运行时窗口展示、历史页立即检测，以及保留账号检测 Tab 的批量/详情入口。

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
- 自动恢复开关保存到 SQLite，开启立即扫描，此后每 60 秒在服务器运行，关闭页面后继续。仍在 Sub2 池中且未人工暂停的账号，确认 401/失效/缺少本地凭据后直接 AUTH 登录，不走 RT 优先路径；Sub2 `status=error` 或 `schedulable=false` 不单独证明 401。缺少本地工作区时以 Sub2 身份核对新凭据。
- 正常、禁用、删除、未知及人工暂停账号跳过；任务去重。成功交付/恢复不进入失败冷却，临时失败按原因持久退避，不按固定失败次数停止；需人工处理的任务保留门禁。
- 页面进行中每 5 秒刷新，自动开启且空闲时每 60 秒，关闭且空闲时每 5 分钟；显示上下次巡检与结果摘要。
- HTTP 代理输入、代理测试和错误分类。
- AUTH 本地凭据的手动模型检测；检测自身只读 AT、无 RT 刷新；明确区分本地过期、401、额度、权限与网络错误。历史页可立即提交检测，结果仍在保留的“账号检测”Tab 批量/详情页查看；符合条件的检测结果可触发已开启的自动恢复。

## 浏览器登录的历史限制

sys1 IPv4 直连仍可能在授权前收到 Cloudflare `403 challenge`；当前版本会给当前浏览器最多 25 秒完成验证，未通过则按 `RetryCount` 和总超时重新创建浏览器重试，并准确分类该响应，不会把它误判成密码错误或账号删除。需要稳定完成真实登录时，应在页面显式填写可用的 HTTP 代理并先通过代理测试。

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
./build-linux.sh /tmp/openai-login-web-20261005T124744Z-sub2-auth401-recovery
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
