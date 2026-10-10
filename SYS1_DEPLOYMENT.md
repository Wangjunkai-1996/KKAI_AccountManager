# sys1 线上部署说明

## 后续审查整改发布（2026-10-10 17:51 上海时间）

- `2026-10-10T09:51:43Z` 切换 release `20261010T093908Z-lifecycle-migration-hardening`，源码 `f28a11ec753ba34f7100b129dfc52a371263c7f0`，已推送 `origin/main`，Mac 干净工作区构建。Linux amd64 SHA-256：`3fe7edd98296f477761306a6af4962de8e289752c3c58728a3c9cbb7ade24e67`。
- 恢复/检测批次持久化目标，latest/due/queued/历史/复检按目标隔离；旧任务仅在唯一 imported 绑定时推断目标，交付任务以交付记录为准，孤儿记录不阻断升级。同远端 ID 跨目标、旧 schema 重开升级、凭据和历史保留有回归覆盖。
- HTTP 增加请求头/空闲超时、数据库 `/ready` 和 SIGINT/SIGTERM 收尾；监听失败返回非零退出码且执行资源清理。停机先取消请求，等待 handler 写入完成，再停止 worker 和关闭数据库；15 秒仅为首轮优雅等待，最终进程硬停止受 systemd 20 秒上限约束。
- 历史页面 Sub2 详情缓存 10 秒，最多 1024 项并清理过期项；检测回调、监控和恢复读取实时状态，不让缓存遮住新 401。
- Playwright 脚本统一模块版本，不再修改依赖清单；headless 使用 bundled Chromium。Docker 统一 Debian 路径，移除不兼容 Alpine 快捷镜像，修复 CMD 覆盖，限制宿主 loopback，数据库/密钥使用持久卷，探针使用 `/ready`，旧容器迁移有数据保护。Docker 改动只做静态验收，不作为本次 sys1 发布方式。
- 隔离候选复制 110 个账号，自动恢复关闭、Sub2 未配置，显式交付/资料任务清空。候选 health、静态资源 hash、SQLite 完整性、Node/driver hash 和旧二进制读取新 schema 均通过；候选阶段未单独调用 `/ready`，生产阶段已验证。
- 停服前后登录、导入、恢复、检测、交付、资料任务、复检在途数均为 0。SQLite 一致性备份 `/var/lib/openai-login/backups/accounts-before-20261010T093908Z-lifecycle-migration-hardening.db`，2,985,984 字节、0600、`integrity_check=ok`。
- `2026-10-10T09:55:39Z` 延迟验收：active/running、`NRestarts=0`，实际二进制 PID 1550791；本机及入口 socket 的 health/readiness 正常，`database_ready=true`；公网未认证 HTTP401，有效 Nginx 路由仍为 `/run/tls/auth/login.sock`。巡检推进到 `09:54:46Z`，`enabled=true,running=true,last_error=""`；启动以来 panic/fatal/数据库锁/存储/监听/OAuth401/403/5xx 聚合均为 0。
- 延迟快照 accounts=113、recovery tasks=188、deliveries=55、rechecks=114、repairs=0；巡检仍报告 10 个等待/冷却账号和 6 个状态暂时未知账号，不能将服务健康解释为所有账号已恢复。
- 回滚版本 `20261010T044103Z-destination-isolation`，SHA-256 `109850dfe0798593af7312135fe191c0605696d6b9be3bf306d9efd5be0665d4`，旧 binary/Node/driver 和备份均核验保留。回滚只切版本，不覆盖业务数据库。
- 正式 release 保留 `DEPLOYMENT.json`、`CANDIDATE_ACCEPTANCE.json`、`ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`；隔离候选数据库/密钥和本轮远端临时上传文件已清理。未手动触发真实 OAuth/Challenge 或新账号 Sub2 交付，未构建 Docker 镜像/运行容器浏览器；未运行仓库完整测试套件。

验证命令（均通过；远端脚本已清理，下列为执行记录）：

```bash
go test ./internal/store ./cmd/server ./internal/login -count=1 -timeout=180s
go test -race ./internal/store ./cmd/server ./internal/login -count=1 -timeout=240s
go vet ./internal/store ./cmd/server ./internal/login
go test ./internal/store -run '^TestDestinationMigrationReopensLegacySchema$' -count=1 -timeout=30s -v
go test -race ./internal/store -run '^TestDestinationMigrationReopensLegacySchema$' -count=1 -timeout=60s
go test ./cmd/server -run 'TestSub2AccountStatusCachesBriefly|TestSub2MonitorAfterCompletedTaskChecksNewFailure' -count=1 -timeout=120s
node --check cmd/server/client.js
node cmd/server/client.test.cjs
bash -n quick-docker.sh build-docker.sh fix-playwright.sh install-playwright.sh run.sh test.sh
shellcheck quick-docker.sh build-docker.sh
docker compose config --quiet
git diff --check
./build-linux.sh /tmp/openai-login-20261010T093908Z-lifecycle-migration-hardening
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-stage-lifecycle-migration-hardening.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-deploy-lifecycle-migration-hardening.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-verify-lifecycle-migration-hardening.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-cleanup-lifecycle-migration-hardening.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 -' < /tmp/auth-verify-lifecycle-migration-hardening.py
```

## 多目标交付隔离修复发布（2026-10-10）

- 切换时间约 `2026-10-10T05:03:34Z`，release `20261010T044103Z-destination-isolation`，源码提交 `da7289f5b84c0e61643254a84b212854b4577048`，Linux amd64 二进制 SHA-256：`109850dfe0798593af7312135fe191c0605696d6b9be3bf306d9efd5be0665d4`。回滚 release 为 `20261010T013500Z-retry-challenge-cleanup`；发布前备份 `/var/lib/openai-login/backups/accounts-before-20261010T044103Z-destination-isolation.db`，权限 `0600`，SQLite `integrity_check=ok`。
- 修复多 destination 交付查询、到期调度、唤醒、凭据修复和导入回调的串目标问题；新登录只取消同一 destination 的旧交付，不再误取消其他目标任务。新增双 destination 回归覆盖 latest/due/wake 和新登录清理。
- 候选使用生产数据库只读副本、关闭 Sub2 环境，候选 `/health` 返回 `status=ok` 且数据库完整性通过。正式切换后服务 `active/running`、`NRestarts=0`，本机及 `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock` `/health` 为 `status=ok`，公网未认证返回 HTTP 401；`/api/account-recovery/settings` 显示 `enabled=true,running=true,last_error=""`，扫描继续推进；切换窗口日志严重错误聚合为 0。旧 release 保留。
- 定向验证通过：`go test ./internal/store ./cmd/server -count=1 -timeout=120s`、对应 `-race`、`go vet ./internal/store ./cmd/server`、`git diff --check`、Linux amd64 构建。未运行仓库完整测试套件，未手动触发真实 OAuth 或真实浏览器 Challenge；健康检查不代表上游长期登录成功率。

这份文件是 `KKAI_AUTH` 的 sys1 线上部署记录。账号密码、TOTP、代理凭据和 access/refresh token 不写入文档。

> 这是 sys1 线上事实的唯一权威文档。新对话先读取 `DOCS_INDEX.md` 和 `NEW_CHAT_CONTEXT.md`，发生冲突时以本文的服务、端口、release、健康检查和验收结论为准。

## Challenge 提交竞态与删除清理保护发布（2026-10-10）

- 切换时间 `2026-10-10T02:10:17Z`，release `20261010T013500Z-retry-challenge-cleanup`，源码提交 `a61b7b847f58d152358ac23da47213c749a9367f`，Linux amd64 二进制 SHA-256：`6cb8ffd46140e1c67624da2dbda794d5bfc847be920efd3dbdfb96f135a5a75c`。回滚 release 为 `20261010T001828Z-retry-policy-final`；发布前备份 `/var/lib/openai-login/backups/accounts-before-20261010T013500Z-retry-challenge-cleanup.db`，完整性通过。
- 修复邮箱、密码、MFA、Consent 提交触发 Challenge 时被旧表单遮蔽而停住的问题；保留 HTTP 状态和 `Retry-After` 进入外层浏览器重试，补齐 Chromium 临时网络错误重试并防止超大 `Retry-After` 溢出。账号删除清理增加账号 lease、凭据版本校验和 `operation_id` CAS，避免旧检测删除新登录或重绑的 Sub2 账号。
- 候选隔离数据库、健康、自动恢复关闭和回滚兼容通过；停服前后在途任务均为 0。延迟验收 `2026-10-10T02:16:03Z`：服务 active/running、`NRestarts=0`，主机及 `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock` `/health` 为 `status=ok`，公网未认证返回 HTTP 401；自动恢复 `enabled=true,running=true`，`last_scan_at=2026-10-10T02:15:17Z`，`last_error=""`；启动以来 panic、fatal、database locked、存储、监听、OAuth 401/403/5xx 聚合均为 0。
- 正式 release、回滚 release、生产备份和 `DEPLOYMENT.json`/`CANDIDATE_ACCEPTANCE.json`/`ACCEPTANCE.json`/`DELAYED_ACCEPTANCE.json` 保留；上传脚本、临时二进制和候选数据库已清理。未手动触发真实 OAuth；本机缺少 Playwright driver，真实浏览器 Challenge 流程未执行，单元、race、定向测试覆盖已通过。

## 重试预算、恢复并发与 Challenge 最终发布（2026-10-10）

- 当前 release：`20261010T001828Z-retry-policy-final`，源码提交 `319f13b428b7733a7521692171cf8be24d4b78f0`，Linux amd64 二进制 SHA-256：`54c1f86f5eafc21eca85b02af8d4235d15ee447b3b93812c2e15b179876ad672`。候选于 `2026-10-10T00:19:51Z` 完成隔离验收；上线前数据库备份为 `/var/lib/openai-login/backups/accounts-before-20261010T001828Z-retry-policy-final.db`。
- 回滚 release：`20261009T155200Z-challenge-retry`。本次修复统一登录总尝试预算，代理 fallback 与浏览器重建共享预算；纳入 HTTP 408/429/5xx 和临时网络错误；保留并扣减 `Retry-After`；Challenge 最多等待 25 秒，耗尽后按剩余预算重试；确定性 OAuth/普通 4xx 不重试。恢复写回增加 checkpoint、`retry_count` 和 marker 轮次门禁，账号删除级联、取消检测、初始验证临时失败和 SSE 建连超时均有边界保护。
- 本地验证通过：`go test ./cmd/server ./internal/login ./internal/store -count=1 -timeout=120s`、对应 `-race`（180 秒）、`go vet ./internal/login ./internal/store ./cmd/server`、`node --check cmd/server/client.js`、`node cmd/server/client.test.cjs`、`git diff --check`；未运行仓库完整套件。
- 延迟验收（`2026-10-10T01:02:15Z`）：`openai-login.service` 为 `active/running`，`NRestarts=0`，当前 release 与回滚点可识别；本机及 `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock` 的 `/health` 均为 `status=ok`，公网未认证请求返回 HTTP 401。`/api/account-recovery/settings` 返回 `enabled=true`、`running=true`、`last_error=""`，扫描从 `00:46:48Z` 推进至 `01:01:50Z`；启动以来 panic、fatal、database locked、存储、监听、OAuth 401/403/5xx 聚合均为 0。
- 本轮临时二进制、部署脚本和候选数据库已清理；正式 release、回滚 release 与生产备份保留。未手动触发真实 OAuth，线上验收证明服务健康和扫描推进，不代表上游长期登录成功率。

## 浏览器 challenge 自动重试发布（2026-10-10）

- 切换时间 `2026-10-09T16:04:39Z`，release `20261009T155200Z-challenge-retry`，源码 `b6809f0dd2ac00893319f392e3395e2477a860f6`，Linux amd64 二进制 SHA-256：`ed2e4565641acc794c4a6057244e1832a98f2a599f098cfdc3e2c530f56b72b5`。
- 初始导航、邮箱/密码表单和 OAuth 回调阶段遇到 Cloudflare challenge 时，当前浏览器最多等待 25 秒；未通过则按 `RetryCount`、有界退避和 3 分钟总超时重新创建浏览器。账号删除/停用、地区限制和普通 4xx 仍保持终止，HTTP 状态和 `retry_wait` 进度保留。
- 候选隔离数据库 86 个账号，自动恢复关闭、Sub2 未配置；候选 health、静态 hash、数据库完整性、Node/driver 和回滚兼容通过。发布前备份 `/var/lib/openai-login/backups/accounts-before-20261009T155200Z-challenge-retry.db`（2,363,392 字节、0600、`integrity_check=ok`）。
- 即时验收通过；延迟验收 `2026-10-09T16:06:16Z` 确认 active/running、`NRestarts=0`、自动恢复首轮扫描推进、有效路由为 `/run/tls/auth/login.sock`、公网未认证 HTTP 401，启动以来 panic/fatal/数据库锁/存储/监听/OAuth 401/403/5xx 聚合均为 0。回滚保留 `20261009T054200Z-recovery-final`。本轮未手动触发真实 OAuth。
- 定向验证：`go test ./internal/login -count=1`、`go test -race ./internal/login -count=1`、`go test ./cmd/server -count=1`、`go vet ./internal/login ./cmd/server`、`git diff --check`；未运行仓库完整套件。

## 八项审查修复最终发布（2026-10-09 13:44:42 上海时间）

- 最终切换时间 `2026-10-09T05:44:42Z`（上海时间 13:44:42），release `20261009T054200Z-recovery-final`。源码 `53bde86f241f9050dda95e5887872621f4882921`，Mac 干净工作区构建，已通过本地 Clash 代理推送 `origin/main`。二进制 SHA-256：`1f8cab2c57223d111797023e9f54074c8d7f9641f4eda6bf29e11cb9a315cbe7`。
- 修复 8 项：旧重试响应覆盖新登录；普通 active 账号临时登录失败后取消重试；Sub2 包装字符串 401 未触发重登；领取任务后重启误认暂停归属；交付批次存储熔断后继续创建；纠正失效分组假报入队；重复开始覆盖运行批次；资料修复未快速轮询。新增正式 Go/Node 回归，复现不再只存在于临时文件。
- 失败写入以状态、毫秒更新时间及已有 retry_count 限制轮次；领取状态改为 validating，只有完成暂停后进入 logging_in。写回后的恢复必须匹配远端 checkpoint。Sub2 包装状态严格解析并保留原精确错误码兼容，非 JSON 正文不丢失上游 401；错误仅返回脱敏固定码。
- 未发送且未移交的交付可纠正配置并重新排队；已有导入意图、需人工处理的移交任务及已取消任务不会假报接受配置变更。资料修复 queued/checking 每 5 秒刷新，等待重试每 60 秒，空闲关闭自动恢复时 5 分钟。
- 初版 `dcb2b5c` 于 13:35:51 发布为 `20261009T053050Z-recovery-delivery-races`，13:37:49 延迟验收通过。最终版补齐错误码兼容、归属标记、同毫秒轮次及正式回归。当前保留该初版为回滚点，同时保留更早 `20261009T042256Z-401-recovery`；不回退覆盖业务数据库。
- 隔离候选复制 71 个账号；关闭自动恢复、不配置 Sub2、清空候选显式交付/资料任务。候选 health、静态 hash、数据库完整性、Node/driver hash 和回滚二进制兼容通过。停服前/后各类在途任务均为 0。
- 发布前备份 `/var/lib/openai-login/backups/accounts-before-20261009T054200Z-recovery-final.db`，1,597,440 字节、0600、integrity_check=ok。回滚 `20261009T053050Z-recovery-delivery-races`，SHA-256 `5a0d9aee1b0faaf026cac97f3601a5e14a9f87f3513a1b06477d3b7c39a5ec02`，二进制及 Node/driver 已核验。
- 延迟验收 `2026-10-09T05:46:24Z`（上海时间 13:46:24）：active/running、NRestarts=0、实际新二进制 PID 61529；自动恢复 enabled/running，扫描推进至 `2026-10-09T05:45:42.70610977Z`，错误和阻塞为空。有效 AUTH 路由 `/run/tls/auth/login.sock`，本机/socket 正常、公网未认证 HTTP401；默认代理环境为空。
- 从服务启动 `Fri 2026-10-09 05:44:42 UTC` 起，panic/fatal/数据库锁/存储故障/监听失败/OAuth拒绝/OAuth 5xx 聚合均为零。快照：accounts 71、recovery tasks 86、deliveries 16、rechecks 26、repairs 0。
- 本轮未手动触发真实 OAuth、新建分组交付或生产故障；无法据此保证长期上游成功率。仓库完整测试套件未运行。最终行为由相关定向 race/Go/Node 验证，生产只读验收不等同于新的真实恢复业务验收。
- 远端证据位于该 release 的 `DEPLOYMENT.json`、`CANDIDATE_ACCEPTANCE.json`、`ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`。候选及上传临时文件按清理步骤删除，正式/回滚版本、备份和验收证据保留。

精确验证命令与结果：以下最终版本相关检查全部通过。初版受影响两包 `go test ./cmd/server ./internal/store -count=1 -timeout=120s` 也通过；最终增补后只重跑受影响范围。Node 正式用例涵盖两项前端新回归。远端临时脚本已清理，下列远端命令为执行记录。

```bash
go test -race ./cmd/server ./internal/store -run 'Test(Sub2Monitor|Sub2AutomaticCheck|Sub2Recovery|Sub2Recheck|AccountRecovery|AccountDelivery|DeliveryOptions|QueueAccountDelivery|Recovery|CredentialRepair)' -count=1 -timeout=120s
go vet ./cmd/server ./internal/store
node --check cmd/server/client.js
node cmd/server/client.test.cjs
./build-linux.sh /tmp/openai-login-20261009T054200Z-recovery-final
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-stage-recovery-final.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-deploy-recovery-final.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-verify-recovery-final.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-cleanup-recovery-final.py'
git diff --check
```

## 401 自动恢复冷却修复（2026-10-09 12:25 上海时间）

- 04:25:35 UTC（北京时间 12:25:35）上线 `20261009T042256Z-401-recovery`，来自 Mac 上干净代码提交 `e96d8b7effb0dab2a9c5ce44a2faf94af72b4583`。用户授权全部工作区提交推送，51 个文件已提交并通过本地 Clash HTTP 代理推送 `origin/main`；发布后另行提交本文等记录。二进制 SHA-256：`dfe80de83925b1db32262190d99fe9685cb55b938905267b941031a6817adf21`。
- 根因是 `automaticRecoveryAllowed` 将成功交付/恢复也按 30 分钟冷却处理，拦住后续巡检和当前 401 回调。现在仅非成功任务保留该冷却；持久失败退避、人工处理、人工暂停、身份/凭据版本和去重门禁保留。五个新增回归场景先失败复现，修复后与相关定向 race 测试全部通过。
- 只发布 AUTH。Mac 构建产物直接上传 sys1，保留现有 Node/driver。隔离候选使用 71 账号的一致性数据库副本，自动恢复关闭、Sub2 未配置、显式交付/资料任务清空；候选健康、静态资源、数据库完整性、运行时 hash 和旧二进制兼容全部通过。两次在途守卫（停服前/后）均为 0。
- 发布前备份 `/var/lib/openai-login/backups/accounts-before-20261009T042256Z-401-recovery.db`，1,597,440 字节、0600、integrity_check=ok。回滚 `20261008T182221Z-compact-login`，SHA-256 `666dd45943cf05c3becf859c7144a6043742fd0e466888cb54c1df08ef853534`；不回退覆盖业务数据库。
- 04:29:34 UTC 延迟复核 active/running、NRestarts=0、实际二进制 PID 3999576、二进制及静态 hash 一致。自动恢复 enabled/running，上次扫描推进到 04:28:35，错误/阻塞为空。AUTH 对应 Nginx server block 有效路由为 `/run/tls/auth/login.sock`，本机/socket health 正常，公网未认证 HTTP401；默认代理环境保持为空。
- 从服务启动 04:25:35 UTC 起审查日志，panic/fatal/数据库锁/存储故障/监听失败/OAuth拒绝/OAuth 5xx 聚合均为 0。生产数据库及备份完整性、回滚二进制/Node/driver 全部核验通过。快照：accounts 71、recovery tasks 86、deliveries 16、rechecks 26、repairs 0。
- 完整测试套件未运行；本轮未人为触发真实 OAuth、新建带分组账号或生产故障。账号 322/323 在本次发布前已经通过用户手动恢复，不作为本次自动恢复修复的线上业务验收证据；本次新的 401 立即入队行为由定向回归验证。未重跑前端视觉验收（静态资源与上一线上版本一致）。
- 远端 release 证据：`DEPLOYMENT.json`、`CANDIDATE_ACCEPTANCE.json`、`ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`。正式/回滚版本及备份保留；临时候选和上传产物按下列清理命令移除。

验证命令如下：第一条在修复前复现预期失败，其覆盖范围随后包含在通过的 race 命令中；其余检查全部通过。远端临时脚本在清理后不再保留，下列命令为执行记录。

```bash
go test ./cmd/server -run '^TestSub2MonitorAfterCompleted' -count=1 -timeout=60s
go test -race ./cmd/server -run 'TestSub2(Monitor|AutomaticCheck|RecoveryDoesNotReopenActiveManualPause|Recheck)' -count=1 -timeout=90s
go test -race ./cmd/server ./internal/store -run '^Test(DeliveryOptions|DeliveryRecovery|Sub2ManualImport|Sub2Import|AccountDelivery|AccountRecovery|AccountCredential|AutoRecovery|RunWithHistory|CredentialRepair|History|DirectProxy|Recovery|Sub2Recovery(ValidateCandidate|AutoSettings|LogsIn|Preserves|Rejects|Cannot|Invalid|DoesNotUse|Changes|AllowsDifferent|AllowsMissing|Resume|Automatic|Retry|Temporary|Delivery|Storage|Checkpoint|Adopts|Rechecks))' -count=1 -timeout=90s
go test -race ./internal/login -run '^Test(DirectIPv4|ProxyRelay|CheckProxyURL|ValidateHTTPProxy|DeadlineCanceledLogin|DeadlineCancelsToken|ParseJWT|ParseOAuthIdentity)' -count=1 -timeout=90s
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
./build-linux.sh /tmp/openai-login-20261009T042256Z-401-recovery
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-stage-401-recovery.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-deploy-401-recovery.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-verify-401-recovery.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-cleanup-401-recovery.py'
git diff --check
```

## 批量登录紧凑布局（2026-10-09 02:25 上海时间）

- 2026-10-08 18:25:11 UTC 切换 `20261008T182221Z-compact-login`，SHA-256 `666dd45943cf05c3becf859c7144a6043742fd0e466888cb54c1df08ef853534`。本地 `main` HEAD `4efcdff` 加未提交改动，Mac 构建、直接上传 sys1；无代码托管同步。本轮仅变更 AUTH 前端布局和提示。
- 桌面端双栏适配可用视口，账号前缀与登录并发并排，代理和处理模式用原生 details 折叠；保留摘要、完整错误与键盘操作。表单和处理结果分别滚动，开始按钮在卡片底部保持可见。低高度桌面缩短输入框与间距，手机自然单列；空白输入不再显示“输入检查通过”。
- 本地合成数据浏览器验收：1366×768 和 1440×900 默认表单与页面无纵向溢出；1280×650 的表单 clientHeight=scrollHeight=419、开始按钮 bottom=617；390×844 手机按钮 bottom=789、无横向溢出；683×384 正确回流单列、无横向溢出。50 条结果仅结果区滚动，8 条输入错误、33 字符前缀错误和展开设置不挤走桌面开始按钮。details 键盘、DIRECT 大小写摘要、顺序模式禁用并发、交付弹窗均通过；JS 错误 0。截图 `/tmp/kkai-auth-compact-login.png`。
- 定向 Node 交互/语法、HTML ID 唯一性检查、独立代码审查和 Linux amd64 构建通过；完整测试套件未运行。本轮未执行真实 OAuth、模型或 Sub2 业务写入验收，不沿用上一版本的登录成功作为本次实测证据。
- 隔离候选复制 60 账号、关闭自动恢复且不配置 Sub2；health、布局标记、三份静态资源 hash、Node/driver hash、数据库完整性与回滚二进制兼容通过。停服前后各类在途任务均为 0。
- 发布前备份 `/var/lib/openai-login/backups/accounts-before-20261008T182221Z-compact-login.db`，1,531,904 字节、0600、integrity_check=ok。
- 18:26:37 UTC 延迟复核 active/running、NRestarts=0、实际 PID 2000702；二进制及资源 hash 一致，自动恢复开启且巡检已推进到 18:26:11，无错误/阻塞。有效 Nginx 路由仍为 `/run/tls/auth/login.sock`；本机/socket health 正常，公网未认证 HTTP401。严重故障、OAuth 拒绝与 5xx 日志聚合均 0，默认代理环境为空。
- 账号 60、恢复任务 67、交付 7、复检 9、资料任务 0。回滚版本 `20261008T180135Z-account-prefix`，SHA-256 `7023dc1060ed8b901b5772e61a5a3a914f574282dbbddd4f38258391ac3192cb`；二进制、Node/driver、数据库和一致性备份已核验。回滚不覆盖业务数据库。
- 隔离候选数据库/密钥和本次远端上传产物已精确清理；正式/回滚 release、备份与证据保留。release 证据：`DEPLOYMENT.json`、`CANDIDATE_ACCEPTANCE.json`、`ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`。

以下命令均通过；远端临时脚本已清理，命令仅为执行记录：

```bash
node cmd/server/client.test.cjs
node --check cmd/server/client.js
./build-linux.sh /tmp/openai-login-compact-login
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-stage-compact-login.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-deploy-compact-login.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-verify-compact-login.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-cleanup-compact-login.py'
git diff --check
```

## 可选账号前缀（2026-10-09 02:03 上海时间）

- 2026-10-08 18:03:15 UTC 切换 `20261008T180135Z-account-prefix`，SHA-256 `7023dc1060ed8b901b5772e61a5a3a914f574282dbbddd4f38258391ac3192cb`。仍来自本地 `main` HEAD `4efcdff` 加未提交改动，Mac 构建、直接上传 sys1，无代码托管同步。
- 批量登录页新增可选账号前缀，交付参数弹窗可编辑并保存平台默认。`delivery_options.name_prefix` 随既有 JSON 快照保存，无 schema 变更；旧请求/空值回退 `AUTH`。前后端统一 Unicode 空白裁剪、最多 32 个码点、拒绝控制字符。新建账号命名为 `前缀_MMDDHHmm_邮箱`，仍限制总名称长度 100 字符；操作标记和身份关联独立于显示名，已有账号不改名。
- 每批前缀冻结，重试沿用；关闭自动交付后，同页手动导入默认采用原批前缀并允许确认时覆盖。默认设置迟到不会覆盖主表单输入；弹窗加载期间前缀禁用，防止返回值覆盖正在输入的内容。
- 定向 Go race、Node 交互/语法、独立代码审查通过。浏览器合成数据验收输入框与交付弹窗同步，536px 窄窗口 scrollWidth=clientWidth=536、JS 错误 0；截图 `/tmp/kkai-auth-account-prefix.png`。没有把虚构账号提交到生产。
- 隔离候选复制 60 个账号，自动恢复关闭且不配置 Sub2；health、静态资源 hash、数据库完整性、Node/driver hash 与回滚二进制兼容均通过。切换前后各类在途任务为 0。
- 发布前备份 `/var/lib/openai-login/backups/accounts-before-20261008T180135Z-account-prefix.db`，1,531,904 字节、0600、integrity_check=ok。
- 18:04:00–18:04:07 UTC 使用历史账号 279、空代理、auto_deliver=false 完整 OAuth 成功，耗时 7,500ms，attempt 302 为 success，AT/RT 已加密保存。日志确认 IPv4、MFA、授权、token 交换成功，外部代理回退与 challenge 均 0。未为前缀验收创建新的生产 Sub2 账号，名称生成、保存默认、快照与重试由定向接口测试覆盖。
- 18:05:19 UTC 延迟复核：active/running、NRestarts=0、实际 PID 1926007、二进制和资源 hash 匹配。自动恢复开启且巡检已推进至 18:05:15，无错误/阻塞；有效 Nginx 路由、socket、本机 health 正常，公网未认证 HTTP401。严重故障/OAuth拒绝/5xx 日志聚合均 0。
- 账号 60、恢复任务 67、交付 7、复检 9、资料任务 0。回滚版本为 `20261008T173543Z-fast-recheck`，SHA-256 `12d23feed5826fed4ab7d568343e49aa45f6ccf3bac2b0a63906a2988fbc2cee`，运行环境和一致性备份已核验保留；回滚不覆盖业务数据库。
- 候选及回滚兼容验收临时单元均已停止；隔离候选数据库/密钥目录、本次远端临时上传二进制、manifest 和四个验收脚本已清理。正式与回滚 release、数据库备份及验收证据保留。
- 证据保存在 release 的 `DEPLOYMENT.json`、`ACCEPTANCE.json`、`LOGIN_ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`。完整测试套件未运行。以下为本次通过的精确命令，远端脚本命令仅为执行记录，脚本已清理：

```bash
go test -race ./internal/store ./cmd/server -run 'TestDeliveryOptions|TestSub2Import|TestAccountDelivery|TestDelivery' -count=1 -timeout=90s
node cmd/server/client.test.cjs
node --check cmd/server/client.js
./build-linux.sh /tmp/openai-login-account-prefix
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-stage-account-prefix.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-deploy-account-prefix.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-real-login-account-prefix.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-verify-account-prefix.py'
git diff --check
```

## 首次自动复检提速（2026-10-09 01:36 上海时间）

- 2026-10-08 17:36:59 UTC 切换 `20261008T173543Z-fast-recheck`，SHA-256 `12d23feed5826fed4ab7d568343e49aa45f6ccf3bac2b0a63906a2988fbc2cee`。来源为本地 `main` HEAD `4efcdff` 加未提交改动，仅修改复检调度和旧计划迁移；未同步代码托管。
- 首检从成功完成后 5 分钟改为 30 秒，二检保持完成后 30 分钟。启动迁移仅提前最新 completed 任务中 pending/round=1/retry_count=0、无错误或人工提示、next_check_at 恰为 completed_at+300000 的旧计划。不修改失败退避、二检、非最新任务或正在执行的复检，不对历史任务补建复检。队列每 2 秒检查一次，恢复/资料修正任务优先，因此 30 秒是到期时间而非繁忙时完成时限。
- Mac 构建并直接上传 sys1。隔离生产数据库副本含 57 个账号，自动恢复关闭且 Sub2 未配置；候选 health、旧计划迁移、静态资源/Node/driver hash、数据库完整性和旧版本读取通过。
- 停服前后登录/导入/恢复/检测/交付/资料修正/复检在途均 0。备份 `/var/lib/openai-login/backups/accounts-before-20261008T173543Z-fast-recheck.db`，1,503,232 字节，0600，integrity_check=ok。
- 17:38:17–17:38:25 UTC，历史账号 279 空代理、auto_deliver=false 完整 OAuth 成功，耗时 8,116ms，attempt 297 为 success，AT/RT 已加密保存。日志独立确认 IPv4、MFA、授权和 token 交换成功；无外部代理回退、无 challenge。普通重登不创建复检任务。
- 17:38:54 UTC 延迟验收 active/running、NRestarts=0、实际 PID 1838477；自动恢复 enabled/running，巡检时间已推进，无阻塞或扫描错误。有效 Nginx 路由仍为 `/run/tls/auth/login.sock`，本机/socket 健康，公网未认证 HTTP401；panic/fatal/存储/锁/监听/OAuth拒绝/5xx 聚合均 0。
- 当前 accounts 57、recovery tasks 64、deliveries 4、rechecks 6、repairs 0。最新 task 97/99 均已进入第二轮，仍精确安排在 completed_at+1800000；两条已被取代的历史首检重试记录保留。回滚保留 `20261008T082500Z-ipv4-default`（SHA-256 `e48f40f8662ea84a4042c38101a80a32eb66de45a80fea2a91e9b38505ed8b07`）及运行环境；回滚不覆盖业务数据库，也无需恢复默认外部代理。隔离候选数据库/密钥与本次远端临时上传产物已清理，以下远端脚本命令是本次执行记录。
- 证据保存在该 release 的 `DEPLOYMENT.json`、`ACCEPTANCE.json`、`LOGIN_ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`。未运行完整测试套件；本轮未人为创建生产恢复任务来验证首检 30 秒实时时序，完成事务、到期边界、迁移排除条件、幂等重启和复检恢复流程由定向测试验证。

以下命令均通过：

```bash
go test -race ./internal/store ./cmd/server -run 'TestAccountRecoveryRecheck|TestSub2Recheck' -count=1 -timeout=90s
./build-linux.sh /tmp/openai-login-fast-recheck
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-stage-fast-recheck.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-deploy-fast-recheck.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-real-login-fast-recheck.py'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/auth-verify-fast-recheck.py'
git diff --check
```

## 默认 IPv4 直连（当前配置）

2026-10-08 08:30 UTC（北京时间 16:30）按用户要求移除 sys1 的默认代理：`/etc/openai-login/proxy.env` 中 `OPENAI_LOGIN_PROXY` 为空。当前 `20261009T054200Z-recovery-final` 的登录代理框留空即走服务器 IPv4；Chrome 与 token 交换使用同一 tcp4 出口，检测页面也默认选择 IPv4，自动检测无代理时自行选择直连。账号自己保存或本次明确填写的代理仍优先。

旧配置仅以 0600 权限保存在 `/var/lib/openai-login/backups/proxy.env-before-20261008T082500Z-ipv4-default`，未继续注入运行进程。当前回滚版本为 `20261009T053050Z-recovery-delivery-races`。空代理网络检查返回 `mode=direct,reachable=true`；HTTP 客户端仍收到认证站 403，这不等同于浏览器 OAuth 失败，也不证明完整登录成功。2026-10-08 08:46 UTC 已按用户授权使用历史账号 279 实测默认 IPv4：完整 OAuth 成功，耗时 7,224ms，AT/RT 已加密保存，attempt 290 为 success；日志确认无外部代理回退、无 challenge。先测的账号 278 两次在 MFA 返回 incorrect_code，具体资料/验证方式原因待确认。完整记录见 [IPv4 实测](SYS1_IPV4_OAUTH_DIAGNOSIS.md)。单次成功不代表长期成功率。

## 默认 IPv4 切换验收（2026-10-08）

- 08:30:27 UTC 切换 `20261008T082500Z-ipv4-default`，SHA-256 `e48f40f8662ea84a4042c38101a80a32eb66de45a80fea2a91e9b38505ed8b07`。仅调整页面默认线路/提示及服务代理配置，无后端逻辑或数据库 schema 变更。
- 候选 health、静态资源 hash、IPv4 默认选项、数据库完整性、旧二进制读取、Node/driver hash 验证通过。停服前后登录、导入、恢复、检测、交付、资料修改和复检在途均 0。
- 发布前 SQLite 备份 `/var/lib/openai-login/backups/accounts-before-20261008T082500Z-ipv4-default.db`，1462272 字节、0600、integrity_check=ok；旧代理配置备份为上节路径，同为0600。回滚需同时考虑旧二进制和代理配置，业务数据库不回退覆盖。
- 08:32:08 UTC 延迟复核 active/running、NRestarts=0，实际二进制 PID 19494；进程 OPENAI_LOGIN_PROXY 和通用 HTTP(S)/ALL_PROXY 均为空，argv 无代理覆盖，默认网络检查 mode=direct/reachable=true，认证站 HTTP403（网络检查不能证明浏览器登录结果）。
- auto_recovery_enabled/running=true，last_scan_at 已推进到08:31:27 UTC，last_error为空；有效路由/本机/入口socket正常，公网未认证HTTP401。严重错误及OAuth拒绝/5xx日志聚合0；数据库、备份和回滚准备通过。账号53，恢复任务58；当前无新交付/复检/资料任务。
- 当前release、回滚release、配置/数据库备份保留；本轮候选目录和远端临时上传文件清理完毕。未手动提交真实账号凭据；完整直连OAuth仍未验收。
- 验证命令如下均通过；未运行完整测试套件：

```bash
node cmd/server/account-checks.test.cjs
./build-linux.sh /tmp/openai-login-20261008T082500Z-ipv4-default
git diff --check
```

## 2026-10-08 账号流程最新发布

北京时间 16:10 发布 `20261008T080500Z-account-workflows`；16:12 延迟复核 active/running、NRestarts=0、实际二进制匹配、静态资源/有效路由/socket 正常、自动恢复开启且巡检继续。备份完整，回滚 `20261008T053100Z-account-automation` 就绪。

新增批量 Sub2 分组/优先级/并发与平台默认；新建账号检测通过再入组，已有账号保留原配置。恢复/交付完成后约 5/30 分钟持久复检；修改密码/TOTP/代理后后台续跑，移交恢复与消费修改任务原子提交。未知创建只核对原标记，15 分钟仍查不到则明确提示人工核对；不盲目重复创建。

`direct` 显式选择 sys1 IPv4 直连，空代理仍用默认代理。发布前原生 Chrome 默认走 IPv6 被 challenge；两个 IPv4 A 节点均进入邮箱页。发布后 08:46 UTC 已补齐历史账号完整直连 OAuth 实测，见上节。历史 IPv4 challenge 证据仍有效，不能承诺永久免验证。

定向测试/验收命令见 [账号流程验证记录](ACCOUNT_WORKFLOW_VERIFICATION.md)，网络证据见 [IPv4 OAuth 诊断](SYS1_IPV4_OAUTH_DIAGNOSIS.md)。完整测试套件未运行，未手动使用真实账号验收新建分组导入。

## 账号流程发布验收（2026-10-08 08:10 UTC）

- release：`20261008T080500Z-account-workflows`；binary SHA-256：`f6fe65c332e63f18b8c91b784f25aef99b533aaf30d105dccd63cf3b7b04dfa3`。
- 来源：本地 `main` HEAD `4efcdff3722b3457831ac9db2e36473aa36f8597` 加工作区改动，Mac 构建、直接上传 sys1；无代码托管同步。
- 回滚：`20261008T053100Z-account-automation`，SHA-256 `9d5872b75714ee5b1e6aee12562226266c134bbd0f68113a8b6ecd155f28b939`；Node/driver 文件 hash 一致，旧二进制已验证能读取迁移后的隔离数据库。
- 发布前数据库备份：`/var/lib/openai-login/backups/accounts-before-20261008T080500Z-account-workflows.db`，1441792 字节、0600、`integrity_check=ok`。停服前与停服后登录/导入/恢复/检测在途数均 0。
- 08:09 UTC 隔离候选迁移 53 个账号，Sub2 未配置且自动恢复关闭，候选显式交付/资料任务清空；健康、三份静态资源 hash、数据库完整性、回滚 schema 兼容均通过。
- 08:10:12 UTC 切换后 health/socket 正常；线上分组接口可用，10 个 OpenAI 分组；初始默认 `group_ids=[]`、priority=1、concurrency=3，未替用户擅选分组。
- 08:12:11 UTC 延迟复核：服务 active/running，NRestarts=0，实际二进制 PID 4147390；自动恢复 enabled/running=true，上次扫描推进到 08:11:12 UTC，last_error 为空。有效 Nginx 仍指向 `/run/tls/auth/login.sock`，宿主 socket 健康正常，公网未认证 HTTP401。
- 数据快照 accounts=53、account_recovery_tasks=58、account_deliveries=0、account_recovery_rechecks=0、account_credential_repairs=0。新复检只为之后成功完成的任务安排，不对历史完成任务批量补跑。
- 发布日志 panic、fatal、database locked、storage failure、listen failure、OAuth 401/403、OAuth 5xx 聚合均为 0；数据库/备份完整性通过，回滚可用。不能把本次没有新任务解释为真实新建交付验收。
- 本轮隔离候选数据库/密钥目录和远端临时上传文件已清理，当前 release、回滚 release 与发布前备份保留。
- 发布记录位于该 release 的 `DEPLOYMENT.json`、`ACCEPTANCE.json`、`DELAYED_ACCEPTANCE.json`；精确测试命令见 [账号流程验证](ACCOUNT_WORKFLOW_VERIFICATION.md)。完整套件未运行；完整直连 OAuth 与真实新账号分组导入尚未执行。

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

当前部署基线（2026-10-10 UTC 发布后确认）：

- 当前 release：`20261010T093908Z-lifecycle-migration-hardening`
- 构建来源：本地 `main` 代码提交 `f28a11ec753ba34f7100b129dfc52a371263c7f0`，已推送 `origin/main`
- 当前二进制 SHA-256：`3fe7edd98296f477761306a6af4962de8e289752c3c58728a3c9cbb7ade24e67`
- 回滚 release：`20261010T044103Z-destination-isolation`
- 回滚二进制 SHA-256：`109850dfe0798593af7312135fe191c0605696d6b9be3bf306d9efd5be0665d4`
- 当前服务应保持 `active (running)`，且 `NRestarts=0`
- 当前服务端并发硬上限：10；页面选择的并发数由前端 worker 控制，实际不超过该上限。
- 页面代理留空时默认走 sys1 IPv4 直连；`/etc/openai-login/proxy.env` 的 `OPENAI_LOGIN_PROXY` 已清空，旧代理仅保留为服务器上的 0600 配置备份。
- 历史列表中的 `expires_at` 表示 OAuth Access Token 有效期；Access Token 过期不等于账号或 Refresh Token 失效。
- AUTH → Sub2 导入已启用；Sub2 地址、管理密钥和实例标识保存在 `/etc/openai-login/sub2api.env`（权限 `0600`），由 systemd drop-in 注入服务进程。
- 本次仅发布 AUTH：外部账号关联、历史恢复入口、持久化后台巡检及缺少令牌恢复；Sub2 源码、镜像和线上版本未改。
- 候选验证时自动恢复显式关闭；生产自动恢复为 `enabled` 并保持 SQLite 开关开启，开启立即扫描，此后每 60 秒后台检查 Sub2 异常账号。仅未保存选择时读取 `AUTH_AUTO_RECOVERY` 启动默认值。

## 旧身份错误兼容补丁（2026-10-08）

- 当前最终 release：`20261008T053100Z-account-automation`；回滚为 `20261008T052000Z-account-automation`，此前 `20261008T040000Z-recovery-runtime-status` 也保留。最终二进制 SHA-256：`9d5872b75714ee5b1e6aee12562226266c134bbd0f68113a8b6ecd155f28b939`。四项功能与下节保持一致，仅修正旧失败分类。
- 初次发布延迟复核发现账号 201 / 旧 task 60 的 `identity_mismatch` 来自旧版本，不能证明当前规则仍会拒绝。兼容层改为重新登录并按当前邮箱/绑定规则核验；真实身份冲突仍会转为人工处理。
- 发布前备份后，仅对 task 60 / account 201 作一次限定修正：要求仍为该账号最新任务、旧身份错误、无新凭据检查点，且当前成功凭据版本仍为 205；清空本轮错误回填的人工策略，让新后台重新分类。没有修改账号凭据或 Sub2 状态。其他停用、删除、版本变化任务不重置。
- 最终候选再次使用 49 账号的隔离副本，关闭自动恢复且不配置 Sub2；schema、health、历史、静态资源、运行时 hash 和回滚二进制读取均通过。最终备份：`/var/lib/openai-login/backups/accounts-before-20261008T053100Z-account-automation.db`。
- 最终切换为 05:28:49 UTC（北京时间 13:28:49），停止前后活动数均为 0；备份 1392640 字节、0600、integrity_check=ok。05:30:03 UTC 延迟复核服务仍 active/running、NRestarts=0，唯一实际二进制进程 PID 3620819 运行最终 release；health status=ok，自动恢复 enabled/running=true、无阻塞原因或巡检错误，last_scan_at 推进至 05:29:49 UTC。
- 有效 Nginx auth.kkrich.ltd 路由仍为 `/run/tls/auth/login.sock`；宿主 socket 健康正常，公网未认证 HTTP 401。panic、fatal、数据库锁、存储故障、监听失败、OAuth 401/403 与 5xx 日志聚合均为 0。当前及回滚 binary/Node/driver hash、生产数据库和备份完整性通过，回滚就绪。
- 延迟快照为 49 账号、48 恢复任务、0 交付任务；每账号最新任务为 completed 21、人工处理 5、等待自动重新登录 1。账号 201 / task 60 为 relogin + login_failed 并有持久下次重试时间，尚未声称本次实际 OAuth 已成功；账号 203/222 已被上游明确停用，保留人工提示。没有执行真实新账号自动交付验收，其完整链路由定向集成测试覆盖。两轮隔离候选和本次远端临时上传产物已清理，正式及回滚 release 保留。
- 新增回归与构建通过；沿用下节未受影响的前端、store 和恢复链路检查，未运行完整测试套件：

```bash
go test -race ./cmd/server -run 'TestSub2Recovery(RechecksLegacyIdentityRules|AdoptsLegacyCheckpoint|RetryRejectsChangedOwnership)' -count=1 -timeout=60s
go vet ./cmd/server
./build-linux.sh /tmp/openai-login-20261008T053100Z-account-automation
git diff --check
```

## 自动恢复、后台交付和人工提醒（2026-10-08）

- 05:22:06 UTC（北京时间 13:22:06）切换 `20261008T052000Z-account-automation`；回滚 `20261008T040000Z-recovery-runtime-status`。只发布 AUTH，来源为本地 HEAD `4efcdff` 加未提交工作区改动，未执行代码托管同步。二进制 SHA-256：`45d1358c153cfce92e02de096a8787714df91ad07e29363183a9793e8d99d5dd`。
- 新增持久化失败原因、阶段、重试动作和下次时间。网络、429、5xx 等自动退避，遵守 Retry-After；新凭据检测明确失效后重新登录。检测或启用中断优先复用已保存的新凭据；解除旧版失败 3 次硬性停止。旧失败任务分类后接入自动续跑，必须核对凭据版本、账号绑定和远端任务标记。
- 成功登录与 `account_deliveries` 任务在同一 SQLite 事务提交。后台关联/导入后复用现有恢复引擎完成写回、检测和启用；交付任务不依赖全局自动巡检开关，关闭页面后继续。用稳定 DeliveryID 保证交接幂等，过期登录任务不覆盖新凭据，首次交付不计入复活次数。
- 页面新增每批冻结的自动交付选项、独立登录/交付状态、自动重试时间及按账号去重的“需要处理”列表。真实人工问题才提示，保留键盘可用性；本地 fixture 在桌面及 390px 宽度验收，窄屏 document scrollWidth=clientWidth=390。
- 隔离候选复制生产数据库（49 个账号），关闭自动恢复且不配置 Sub2；迁移、health、历史接口、三份静态资源 hash 通过。旧二进制也已成功打开迁移后的隔离数据库；Node 与 driver 文件 hash 与回滚版本一致。候选未发真实 OAuth、模型或 Sub2 写请求。
- 发布前和停止服务后再次确认登录、导入、检测、恢复活动数均为 0。SQLite 一致性备份 `/var/lib/openai-login/backups/accounts-before-20261008T052000Z-account-automation.db`，1372160 字节、0600、integrity_check=ok；原 release 保留。
- 即时验收：health status=ok、max_concurrent=10、sub2_configured=true，自动恢复 enabled/running=true、无阻塞；宿主入口 socket 健康正常，三份线上静态资源 hash 与本地产物一致。05:23:27 UTC 初版延迟复核健康和回滚通过；最终补丁验收见上节。
- 边界：未手动触发真实 OAuth/新账号导入验收；自动导入不确定结果仅持续核对，不盲目重复创建。Sub2 接口未提供人工暂停的操作版本，因此无法识别“本任务暂停后用户再次设置同样的暂停值”；保留原调度意图、阶段、任务标记及版本核对，不声称覆盖不可观测的人工操作。

定向验证全部通过，完整测试套件未运行：

```bash
go test -race ./cmd/server ./internal/store ./internal/login -run 'Test(AccountDelivery|RunWithHistory|DeliveryRecovery|AccountRecovery|Recovery|Sub2Recovery|Sub2Monitor|Sub2Import|SyncSub2|AutoRecovery|ParseRetryAfter|RetryDelay)' -count=1 -timeout=90s
go vet ./cmd/server ./internal/store ./internal/login
node cmd/server/client.test.cjs
node --check cmd/server/client.js
./build-linux.sh /tmp/openai-login-20261008T052000Z-account-automation
git diff --check
```

## 自动恢复运行状态修复（2026-10-08）

- 03:56:45 UTC（北京时间 11:56:45）完成切换，release：`20261008T040000Z-recovery-runtime-status`；回滚：`20261007T092840Z-recovery-accessibility`。二进制 SHA-256：`d87fb4205ae5623d20e48629902b8da71c81fdf6f67c9c61c0a02a27d4919871`。本次只修复 AUTH 状态接口与页面：新增 `running`、`blocked_reason`，区分用户开启选择与后台真实运行条件；存储故障、服务停止、缺少配置时显示暂停及原因，不显示虚假的下次巡检。临时巡检错误不误判为永久停止。自动恢复策略、账号重试规则和 Sub2 服务未修改。
- 隔离候选使用独立空数据库且不配置 Sub2；开关置为开启时正确返回 `running=false` 和“Sub2 恢复服务未配置”。候选健康检查及三份静态资源 hash 通过，测试数据与候选进程已清理；未调用真实账号或模型。
- 切换前确认登录、导入、检测和恢复活动数均为 0，停止服务后再次核对，再创建 SQLite 一致性备份 `/var/lib/openai-login/backups/accounts-before-20261008T040000Z-recovery-runtime-status.db`（1347584 字节，0600，`integrity_check=ok`）。用户自动恢复选择保持开启。
- 发布后本机及有效入口 Unix socket 设置响应均为 `auto_recovery_enabled=true,running=true,blocked_reason=""`，首轮巡检完成；服务 `active/running`、`NRestarts=0`，公网未认证 HTTP 401。三份线上静态资源与本地 manifest 一致，client.js SHA-256 为 `fc7c6de6d174292da0a13e53672a1c5d321d9013d94a5f1d62906cb90c84934b`；Node/driver 与回滚 release 一致。
- 03:59:18 UTC（北京时间 11:59:18）延迟复核：服务仍 `active/running`、`NRestarts=0`，唯一实际二进制进程 PID 3314636 运行新 release；上次巡检已推进到 03:58:45 UTC，`running=true`、无阻塞原因和巡检错误。发布以来 panic、fatal、数据库锁、存储故障、监听失败计数均为 0。生产数据库与备份完整性再次通过，回滚二进制、Node、driver hash 核对通过；本次远端临时上传文件已清理。
- 下列定向验证全部通过，完整测试套件未运行；本轮未触发真实 OAuth 复活或模型调用，状态修复由真实数据库故障注入、前端回归和线上接口验证覆盖。

```bash
go test ./cmd/server -run 'Test(Sub2RecoverySettingsRuntimeStatus|Sub2RecoveryAutoSettings|Sub2Monitor|Sub2RecoveryStorageFailure|Sub2RecoveryCheckpointFailure)' -count=1 -timeout=60s
go vet ./cmd/server
node cmd/server/client.test.cjs
node --check cmd/server/client.js
./build-linux.sh /tmp/openai-login-20261008T040000Z-recovery-runtime-status
git diff --check
```

## 身份校验修正与浮窗键盘发布验收（2026-10-07）

- 账号 203 / Sub2 16839 的旧任务 53、56、59 均在登录后身份核对阶段以 `identity_mismatch` 停止，凭据没有写回。它们的问题不只是 workspace ID 变化：旧、新 JWT 的 `chatgpt_user_id` 也不一致。09:13 UTC 的 `20261007T090920Z-userid-recovery` 已移除此阻断核对，仍要求邮箱一致、完整 access/refresh token、原 Sub2 账号 ID/邮箱/绑定标记一致，并核验写回后的新 workspace、organization、plan、expires_at 和凭据状态。
- 当前 `20261007T092840Z-recovery-accessibility` 于 09:33:00 UTC 切换，回滚为 `20261007T090920Z-userid-recovery`。本版补足复活次数按钮到记录浮窗的 Tab/Shift+Tab 焦点路径；历史卡片布局、成功复活次数、最近 100 条记录浮窗和醒目的 Sub2 在池标记均已上线。Linux amd64 二进制 SHA-256 为 `72272f5dde1c00c3e6599ce0a884a044e61a83f17c3f62abe64dc2e23e823e79`，Node/driver 从回滚 release 复用且 driver 文件树一致。
- 切换前 SQLite 一致性备份为 `/var/lib/openai-login/backups/accounts-before-20261007T092840Z-recovery-accessibility.db`（724992 字节、0600、`integrity_check=ok`）。新二进制与线上文件 hash 一致；线上 HTML、`client.js`、`account-checks.js` SHA-256 分别为 `4a856145b05a3dccb9c3d409996b1affc76111ba1b8dc2ca4a428e1eb02d0d82`、`1643496fb3e5fe912ebb3b922862fa1562bb3b57080b7c5581d6d36ac35469c9`、`1c37fe582928c755f935f4e19783443262371c8c33c6b436c37614671ec1bb41`。
- 发布后服务 `active/running`、`NRestarts=0`，本机和有效入口 Unix socket `/health` 正常，公网未认证 HTTP 401。定向 Go 测试、`go vet`、Node 客户端交互与语法检查、Linux amd64 构建、`git diff --check` 通过；未运行完整测试套件。
- 账号 203 的 task 64 于 09:13:26 UTC completed，写入版本 233；09:19:25 UTC 独立 AUTH 检测 718 又收到上游 HTTP 401 `token_revoked`，不能把 task 64 的完成当作持续可用。task 65 于 09:19:59 UTC completed，写入版本 234；Sub2 16839 当前为 active、可调度。09:34 UTC 的独立 AUTH 检测 719 使用版本 234、默认代理和 `gpt-5.6-luna`，返回 HTTP 200、`outcome=ok`。目前无法从现有日志确定版本 233 被撤销的上游原因。
- 09:39 UTC 延迟只读复核：当前 release 仍为 `20261007T092840Z-recovery-accessibility`，服务 `active/running`、`NRestarts=0`，本机 `/health` HTTP 200；09:33 切换后 journal 无新行，严重错误计数为 0。账号 203 保持 active，任务 65/凭据版本 234 为最新恢复结果；Sub2 16839 仍 active 且可调度，管理接口 HTTP 200。检查 719 后没有新的 AUTH 检查，因此没有新 401 记录，也不能据此推断长期有效。当前与回滚二进制 SHA-256、Node/driver、生产数据库和发布前备份完整性均再次核对通过；`/tmp` 中本次上传的二进制及 manifest 已清理。

## 工作区切换恢复与历史卡片发布验收（2026-10-07）

- 发布时间：2026-10-07 07:56:49 UTC；release：`20261007T074431Z-workspace-recovery`；回滚：`20261005T124744Z-sub2-auth401-recovery`。
- 恢复流程不再把 workspace/account ID 当作不可变匹配键。同一稳定 ChatGPT 用户更换 workspace 后，仍会核对邮箱、完整 access/refresh token、稳定 `chatgpt_user_id`（两边 JWT 都提供时）以及原 Sub2 账号 ID、邮箱和绑定标记；写回后再严格核验新 workspace、organization、plan、expires_at 和凭据状态。
- 历史卡片显示成功复活次数、恢复尝试次数、Sub2 在池/不在池/待核对状态；复活记录支持悬停、键盘聚焦、点击和触屏打开浮窗，最多展示最近 100 条并处理加载失败、空态、截断和过期响应。
- Linux amd64 二进制 SHA-256：`a880de92a6ab040a3ea331199542a00e626d74aae4bc817440baa4b2b26f370f`；Node/driver 从回滚 release 复用；三份静态资源 hash 与本地构建一致。
- 发布前 SQLite 备份：`/var/lib/openai-login/backups/accounts-before-20261007T074431Z-workspace-recovery.db`，权限 `0600`，大小 716800 字节，`integrity_check=ok`。旧 release 保留可回滚。
- 发布后服务 `active/running`、`NRestarts=0`，`/health` 返回 `status=ok,max_concurrent=10`；实际宿主 socket `/srv/kkai/secrets/tls/kkrich-ltd/auth/login.sock` 返回 HTTP 200，公网未认证返回 HTTP 401。
- 发布后数据库 `integrity_check=ok`，自动恢复开关仍为 `enabled`；快照为 accounts 30、imports 27、checks 715、recovery tasks 26（completed 15、unknown 11），复查时无运行中登录、检测、恢复或导入任务。发布窗口 panic、fatal、database locked、storage failure、listen tcp 日志计数均为 0。
- 发布切换瞬间没有触发真实 OAuth 复活；跨 workspace、稳定用户核对、断点续跑、写回元数据和历史浮窗由定向 Go/Node/fixture 验证覆盖。延迟复核已补充真实线上复活证据如下。
- 延迟复核（2026-10-07 08:18 UTC）确认服务仍 `active/running`、`NRestarts=0`、本机 `/health` 为 `status=ok`、宿主入口 socket 返回 HTTP 200、公网未认证返回 HTTP 401，发布窗口严重错误计数仍为 0；数据库与发布前备份 `integrity_check=ok`，回滚 release 仍保留。
- 延迟复核期间账号 216 的 task 62 实际完成：账号从旧 workspace `43525e7a-5938-4af7-bba5-5ef3e6ab336a` 更新到新 workspace `52e729ef-5bd4-4ce6-83ba-1762c2302664`，Sub2 检测通过并重新开启调度；当前任务统计为 completed 16、unknown 11。该记录提供了真实跨 workspace 复活成功证据。
- 2026-10-07 08:36:58 UTC 仅为历史卡片状态文案修正切换 `20261007T082500Z-history-status`；未改数据库或 Sub2，沿用上一 release 的数据备份和 Node/driver。新二进制 SHA-256 为 `7e4fb6ddef77fb76497fd097c796db7fd81848b95340a0de4fafafb6e2a28ef3`，上一 release 保留为回滚点。
- 修正后复核服务 `active/running`、`NRestarts=0`、本机和宿主 socket `/health` 均为 `status=ok`，公网未认证 HTTP 401，发布窗口严重错误计数 0；线上 `/api/client.js` 已包含“Sub2 状态待核对”。
- 08:49 UTC 延迟快照：accounts 30、imports 27、checks 717、recovery tasks 28（completed 17、unknown 11），无运行中任务；task 63（账号 216 / Sub2 16842）再次完成，凭据检测通过并重新开启调度。

## AUTH 401 恢复发布验收（2026-10-05）

- release：`20261005T124744Z-sub2-auth401-recovery`；commit：`4591e80`；回滚：`20261005T105500Z-sub2-status-ui`。
- 当前 Linux amd64 二进制 SHA-256：`d869c0c16498a8ab362e0be78c3aff617eec0ab96a469244244cd49099006714`。
- 发布前备份：`/var/lib/openai-login/backups/accounts-before-20261005T124744Z-sub2-auth401-recovery.db`。
- 发布后健康复核：`openai-login.service` 为 `active/running`，`NRestarts=0`，`/health` 返回 `status: ok`；有效入口 socket 返回 HTTP 200，公网未认证请求返回 HTTP 401。旧 release 保留可回滚。
- 候选环境显式关闭自动恢复；生产自动恢复为 `enabled`，SQLite 开关保持开启，发布后立即扫描及每 60 秒后台巡检继续运行。
- 生产快照为 history 31、imports 27、statuses 31。账号 117、118、119 的 AUTH current 检测均为 HTTP 401 `credential_revoked`；对应 Sub2 状态为 `active`、`schedulable=false`、`effective_schedulable=false`。自动恢复任务状态为 `unknown`，保持停止调度。
- 本轮上线 AUTH 401 恢复判定、批量登录结果终态等待与展示、`AUTH_MMDDHHmm_email` 名称、Sub2 调度字段与运行时窗口展示、历史页立即检测，以及账号检测 Tab 的批量/详情入口保留。
- Sub2 源码、镜像和线上版本未改；文档不记录管理密钥、代理凭据或 OAuth token。

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

## 外部关联与后台自动恢复发布（2026-10-05，前次 release）

- 最终发布时间：2026-10-05 04:02:18 UTC（北京时间 12:02:18）；release：`20261005T035610Z-sub2-monitor-complete`；回滚：`20261005T033000Z-sub2-monitor-link`。回滚版本是本轮已验证的初版；更早的 `20261005T020014Z-sub2-sync-auto` 也完整保留。
- 外部导入的 Sub2 账号按凭据邮箱和已知工作区自动关联；唯一匹配持久化，不重新导入。歧义、身份冲突、查询失败显示具体原因。
- 历史行提供“检测并恢复”和断点“继续恢复”，显示 AUTH 检测、重新登录、凭据写回、调度恢复及失败原因；普通“重新登录”只更新 AUTH。
- 自动恢复开关保存到 SQLite，开启立即扫描，此后每 60 秒在服务器运行，关闭页面后继续。仅检查 Sub2 异常账号；确认 401/失效/缺少本地凭据后直接 AUTH 登录，不走 RT 优先路径。缺少本地工作区时以 Sub2 身份核对新凭据。
- 正常、禁用、删除、未知、正常但人工暂停的账号跳过。任务去重，自动恢复结束后冷却 30 分钟，同一凭据版本 24 小时内失败 3 次暂停自动重试。
- 页面进行中每 5 秒刷新，自动开启且空闲时每 60 秒，关闭且空闲时每 5 分钟；显示上下次巡检与结果摘要。
- Linux amd64 二进制 SHA-256：`601b43cfd4b7ff567571bb27cfbbdeb0ab107f40125337c39a6a863d2de3f522`；3 个嵌入静态资源与本地 SHA 一致。Node/driver 从旧 release 保留；新增设置表兼容旧二进制，回滚不覆盖业务数据库。
- 隔离候选在 SQLite 中显式关闭自动恢复，验证 `/health`、静态资源、历史关联和设置读取，未执行远端恢复。36 个历史账号：15 原导入绑定、16 外部关联、4 未匹配、1 多条匹配。
- 发布前确认无活跃登录、导入、检测和恢复；SQLite 一致性备份 `/var/lib/openai-login/backups/accounts-before-20261005T035610Z-sub2-monitor-complete.db`，0600、`integrity_check=ok`。切换后健康和资源检查通过，再恢复用户此前开启的自动恢复选择。
- 真实业务结果（前次 release 快照）：首次自动扫描确认 3 个账号 AT 已撤销（HTTP401），实际调用 AUTH 重新登录，均被 OpenAI 返回 `account_deactivated`/HTTP403，保持 Sub2 暂停；原未关联账号 63 在其中。另 1 个 Sub2 403 账号 AUTH 检测 HTTP200，未强制重登。该快照中的三个新导入账号 117/118/119 均正常可调度，未生成恢复任务；当前 release 的最新快照见上方发布验收。
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

sys1 IPv4 `51.81.109.154` 访问 `auth.openai.com/oauth/authorize` 仍可能收到 Cloudflare `403` 和 `cf-mitigated: challenge`；此前 release 已在 sys1 本机用原生 Chrome、无头 Playwright 两条路径复现，且在初始授权文档和表单提交请求中都观察到过。当前版本对初始导航和表单阶段的 challenge 都先等待当前浏览器最多 25 秒；未通过时按 `RetryCount` 和总超时重新创建浏览器，保留真实 HTTP 状态并准确分类。此前使用 `45.39.200.210:7275` 代理时，真实账号已连续完成 4 次 MFA、授权和 token 交换；当前默认代理为 `45.39.200.204:7269`，历史代理检查可达并返回认证站 HTTP 403。页面显示 challenge 时不要把它解释为密码错误或账号已删除。本次 Tab UI 发布未重做真实 OAuth 验收。

需要继续排查时，先记录授权请求的 HTTP 状态、`cf-mitigated`、出口和浏览器启动参数，再决定是否更换出口或调整浏览器验证流程。
