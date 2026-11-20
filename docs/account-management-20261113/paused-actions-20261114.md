# 暂停账号的手动操作与 TOTP 页面修复记录

日期：2026-11-14。状态：实现、独立复审、sys1 发布、两个受影响账号的正式恢复及清理均已完成。最终验收为北京时间 10:24:45。前端交互证据来自本地合成数据浏览器；候选和正式恢复均使用真实账号验证。

## 自动暂停与手动单次操作

用户明确要求：自动重试暂停后，账号仍应允许明确发起一次重登录或恢复。邮箱验证、手机验证、密码错误、TOTP 拒绝等原因继续显示；一次手动操作不代表恢复无限自动重试。

| 账号情况 | 允许的显式操作 | 保留的限制 |
| --- | --- | --- |
| 确定性验证/凭据失败、认证页面等待耗尽 | 更新资料、重新登录、符合原有条件时检测并恢复或继续恢复 | 每次点击授权一次；失败继续暂停，不自动排队重试 |
| Sub2 关联已确认不存在（`missing` / `remote_missing`） | 查看详情、仅重新登录 AUTH、删除本地记录 | AUTH 重登录固定 `auto_deliver:false`；不恢复、不导入、不重建远端账号 |
| 未找到 Sub2 关联（`unmatched`） | 更新资料、重登录；登录成功后手动首次导入 | 不参与关联账号的自动恢复 |
| 官方明确删除（`deleted`） | 查看详情、按现有流程清理本地记录 | 不重新登录、不恢复 |
| 官方停用或删除/停用未区分 | 保留原有手动重登录以重新确认状态 | 恢复服务原有 `account_unavailable` 门禁保留 |
| 身份、绑定、分组、调度或配置问题 | 原有核对和处置流程 | 手动标记不能绕过这些门禁 |

账号表的公开按钮使用既有接口，不增加端点：

- `POST /api/history/login`：传 `account_id`、`auto_deliver` 和 `manual_attempt:true`。是否交付仍按当前账号状态决定；缺失关联强制关闭交付。
- `POST /api/account-recovery/check`、`POST /api/account-recovery`：传 `account_id`、`manual_attempt:true`、`expected_task_id`、`expected_retry_count`。继续恢复保留已经完成的检查点，不重放已完成写入。
- 快照取自最近一次后端返回的原始 recovery task，即使该任务的旧错误已被新成功凭据取代，也不能把任务 ID 擅自清成 0。没有任务才传 `0/0`。

后端以任务 ID 和重试次数作比较后更新（CAS）：活动任务重复点击复用；同一旧请求不能在一次失败之后再次授权下一轮，快照过期返回冲突。手动检测的单次授权由服务端批次标记消费。失败和中断持久保存暂停状态，后台巡检或重启不能自动重复本轮尝试；相关定向及竞态测试已通过，命令见下文。

历史重登录在 `BeginAttempt` 创建登录记录时即把单次意图保存到 `login_attempts.single_attempt`，浏览器启动前已落库。该列为 `INTEGER NOT NULL DEFAULT 0`，旧记录迁移后默认不带单次意图。明确的手动尝试写入 `1`，未知错误、OAuth 阶段失败以及进程重启后的中断均保持停止，不由后台自动续跑。原始 `error_code` 和 `Retryable` 保留原值；自动化是否继续依据持久化的单次意图判断，不通过改写原因码或猜测重试分类表达用户意图。

页面已隐藏的旧“立即检测”入口仍使用 `/api/account-checks`，保留原自动资格过滤，本轮不扩大其接口契约。公开的“检测并恢复 / 继续恢复”独立使用上述手动接口，不受该旧入口过滤影响。

## 新成功登录与旧失败的关系

前后端使用同一保守条件判断旧恢复失败是否被新成功凭据取代：

1. `failure_stage=logging_in`，`retry_action=manual`，状态为 `failed` 或 `unknown`。
2. `error_code` 为六类确定性登录错误之一，或 `auth_page_timeout_exhausted`。只认任务规范错误码，不用其他提示字段覆盖身份等错误。
3. 新成功 `credential_version` 严格大于任务的 `source_credential_attempt_id` 和 `result_credential_attempt_id`。

六类确定性码为 `email_verification_required`、`phone_verification_required`、`password_rejected`、`totp_rejected`、`verification_code_rejected`、`credentials_rejected`。缺失阶段、任务状态不符、版本相等以及身份/分组/调度失败均不能被忽略。delivery / recheck 没有所需证据时保持原门禁，不推测其关联恢复任务已成功。

## AUTH #1579、#1580 的故障证据

主任务本轮排查已确认这两个账号均通过密码步骤，之后停留在 MFA 页面，未发生验证码提交。因此当前证据支持“已到达验证器页面但识别/提交未完成”，不能据此宣称密码错误、TOTP 被上游拒绝、邮箱验证或手机验证。

这次真实可见提示为：

> Check your authenticator app
>
> Enter the current 6-digit code for your account from your authenticator app.

上一版已经覆盖的正文是 `Enter the one-time authentication code shown in your app to verify your identity.`；本次补充的是新的 `current 6-digit code` 变体。识别继续要求可信认证页面、当前可见正文和验证码输入框；备用链接中出现相同词句、仅有标题、隐藏内容或未知 MFA 文案不构成 TOTP 自动提交依据。浏览器回归覆盖真实新文案及一次六位验证码提交。

历史失败记录保持原始证据。发布后 #1579、#1580 已各用一次新登录完成真实恢复，具体 attempt 与原 Sub2 账号 ID 见下方发布验收。

## “更多”菜单的证据与修复

已确认的界面问题是：暂停状态曾隐藏重登录和恢复操作；桌面展开内容又被挤在狭窄操作列，使“更多”缺少可用处理入口且难以阅读。本次恢复符合条件的手动入口，并让展开操作区域跨到账号行内宽区。

线上基线已有原生 `details` 展开快照，不能将刷新关闭菜单说成已证实的线上根因。开发草稿曾新增 `Set` 和 click/microtask 同步，并误对真实 `NodeList` 调用 `.map`；该问题发生于未部署草稿，已删除。最终实现直接在重渲染前用 `Array.from` 读取当前打开的 `details`，重渲染后恢复当前页相应节点，不依赖历史 Set 或默认 toggle 前的微任务时序。

## 本地验证与产品复审

以下前端命令实际执行且退出 0；最终阶段只在相关输入改变后重跑对应范围：

```sh
node --check cmd/server/client.js
node cmd/server/client.test.cjs
git diff --check -- cmd/server/client.js cmd/server/index.html cmd/server/client.test.cjs
```

新增覆盖包括：原生 NodeList 无 `.map`、暂停账号显式动作、缺失关联禁止恢复/重建、AUTH-only 请求、官方删除保护、恢复任务快照，以及七类旧登录失败的严格阶段/状态/版本条件。新版成功覆盖旧超时的集成断言和身份、分组、调度、当前凭据版本的负例均通过。

本地 Chromium 实际验证了 1440×1000 和 390×844 两种尺寸，接口使用合成数据，无生产凭据或远端写入：

- 打开“更多”后刷新账号数据、费用数据，仍保持打开；关闭后立即重渲染及刷新两种数据，均不会重开。
- 超时暂停行可点击继续恢复及重新登录；TOTP 暂停行可点击检测并恢复及重新登录。
- 恢复请求实际包含 `manual_attempt:true` 和任务快照；重登录请求包含单次标记。
- 缺失关联行无恢复/导入/核对导入入口，登录请求实际为 `auto_deliver:false`。
- 账号详情可打开，登录资料与暂停原因可读；没有 JavaScript 页面异常或横向溢出。

模拟重登录接口故意返回可识别的测试失败，以验证请求和失败显示；这不是一次真实 OAuth 成功验收。浏览器 fixture 已停止。后续仅修改旧失败覆盖条件时，按验证范围约束没有重复执行已通过且无关联变化的“更多”浏览器测试。

主任务提供的新 TOTP 定向命令及结果：

```sh
PLAYWRIGHT_NODEJS_PATH=/Users/tokk/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node OPENAI_LOGIN_BROWSER_TEST=1 go test ./internal/login -run 'Test(VerificationPageEvidence|VerificationPagesBrowser|WaitOAuthCallbackCurrentCodeBrowser)$' -count=1 -timeout=90s
```

结果：PASS，9.352 秒。

后端最终定向和竞态验证，以及主任务静态检查，均为已执行结果，没有为写入本文重跑：

```sh
go test -race ./cmd/server ./internal/store -run 'TestManual|TestSingleAttemptMigrationAndRestart|TestSub2RecoveryStaleRetryDoesNotOverwriteNewLogin' -count=1 -timeout=90s
go test ./internal/store -run 'TestSingleAttemptMigrationAndRestart|TestRecoveryRetry|TestRecoveryDueSelection|TestAccountRecoveryTaskRecoveryOnRestart|TestRecoveryCheckpointSurvivesRestart|TestAutoRecoverySettingsSurviveRestart|TestRecoverInterrupted' -count=1 -timeout=90s
go test ./internal/login -run '^TestManualSingleAttemptDisablesRetry$' -count=1 -timeout=90s
go test ./cmd/server -run '^TestManualHistoryFailureStopsAutomaticRecovery$' -count=1 -timeout=90s
go vet ./internal/login ./internal/store ./cmd/server
```

竞态命令 PASS：`cmd/server` 6.314 秒、`internal/store` 1.248 秒；Store 迁移、重启和恢复定向命令 PASS，0.145 秒；登录层单次禁重试 PASS，0.011 秒；历史手动失败停止自动恢复 PASS，0.128 秒；`go vet` PASS，0.226 秒。未运行全仓完整测试套件。

独立产品角色已只读复审通过手动动作、missing 保护、请求快照、原生展开快照及桌面展开布局，并采用上述已有定向和浏览器证据，未重复运行验证。后续对严格旧失败覆盖条件和新 TOTP 文案的独立只读复审亦为 PASS：前后端规范码、阶段、状态、手动标记及严格版本条件一致；TOTP 仍要求官方域名、可见输入和明确正文，备用链接或隐藏内容不会授权提交。最终 `single_attempt` 持久化方案和整体代码已获产品最终复审通过；下列候选与正式发布验收也已通过。

## 本轮发布与真实恢复

release：`20261114T021850Z-paused-actions`；来源为 `33d64a0401599837246f647d4d5fdf554e8a0015` 加工作区修改，未提交/推送。125 个源码文件构建前后散列一致，见 release 的 `SOURCE.json`。二进制 SHA-256：`7c9475845d75421f2cccab585f4376f4442e56f0caa3d93dd28aa77eb8e29389`；smoke SHA-256：`88c0492994ce31de9ce228f877b2570d140386f790d0ebe197cc76be6ca9166c`。

Mac 本地构建均退出 0：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/kkai-auth-paused-actions-20261114/openai-login-web ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/login -o /tmp/kkai-auth-paused-actions-20261114/kkai-auth-login-smoke
```

通过 SSH 别名 `sys1`、用户 `tokk` 连接 `ovh-sys1-prod`，仅发布独立 AUTH systemd 服务。上线前现生产版本和散列再次核对，8 类在途任务均为 0，最近 120 秒没有生产登录；先以服务身份验证新旧浏览器 runtime，再验证旧二进制可读取新增 `single_attempt` 列的隔离数据库。候选关闭自动恢复、不配置 Sub2；真实账号 #1579 在 12.167 秒完成密码、TOTP、授权、token 保存，新增成功 attempt，`auto_deliver=false` 且 import 数不变。

02:22:09 UTC 开始正式切换，切换前建立 SQLite 一致性备份。即时及 60 秒延迟检查全部通过后，通过带任务快照的新单次恢复接口分别执行：

| AUTH | 原 Sub2 | 原恢复任务 | 正式新登录 attempt | 结果 |
| --- | --- | --- | --- | --- |
| 1579 | 17353 | 1304 | 1590 | completed；active；schedulable=true；effective_schedulable=true |
| 1580 | 17352 | 1305 | 1591 | completed；active；schedulable=true；effective_schedulable=true |

每个账号恰好一次新增成功登录，`single_attempt=1` 在数据库中读回；凭据已写回原账号，绑定和 import 数量未改变。没有新建远端账号，也没有批量解除其他暂停。正式恢复于 02:24:06 UTC 完成，后续 02:24:12 UTC 巡检成功推进，检查 `last_error` 为空且摘要存在。10:24:45 北京时间最终验收：服务 active/running、NRestarts=0，实际 binary/static 与候选一致，数据库完整性 ok，8 类在途及活跃 manual 轮次均为 0。

固定发布窗口错误聚合（panic、fatal、数据库锁、存储、监听、浏览器权限、登录失败、OAuth 401/403/5xx）全为 0。有效 AUTH 路由仍为 Unix socket，health/ready/socket 均通过，公网未认证及 reveal 接口为 401。凭据单字段查看、普通详情不泄密、跨站 403 均通过。257 个 `remote_missing` 绑定继续阻断，关联活跃恢复为 0；77 个可信名称仍可读，2 个远端未知状态继续保守处理。

以下命令在 sys1 以 `sudo -n` 执行，全部退出 0；上传目录现已清理，操作脚本归档于 release 的 `operations/`：

```sh
python3 /tmp/kkai-auth-20261114T021850Z-paused-actions/auth-stage-release.py --release-id 20261114T021850Z-paused-actions --binary /tmp/kkai-auth-20261114T021850Z-paused-actions/openai-login-web --smoke /tmp/kkai-auth-20261114T021850Z-paused-actions/kkai-auth-login-smoke --verify /tmp/kkai-auth-20261114T021850Z-paused-actions/verify-runtime.sh
python3 /tmp/kkai-auth-20261114T021850Z-paused-actions/release-smoke.py --release-id 20261114T021850Z-paused-actions --phase candidate --account-id 1579
python3 /tmp/kkai-auth-20261114T021850Z-paused-actions/auth-deploy-release.py --release-id 20261114T021850Z-paused-actions --source-commit 33d64a0401599837246f647d4d5fdf554e8a0015 --binary-sha256 7c9475845d75421f2cccab585f4376f4442e56f0caa3d93dd28aa77eb8e29389 --delay-seconds 60
python3 /tmp/kkai-auth-20261114T021850Z-paused-actions/release-feature-check.py --release-id 20261114T021850Z-paused-actions --account-id 1579
python3 /tmp/kkai-auth-20261114T021850Z-paused-actions/release-recovery-check.py --release-id 20261114T021850Z-paused-actions
python3 /opt/openai-login/releases/20261114T021850Z-paused-actions/operations/release-final-check.py --release-id 20261114T021850Z-paused-actions
```

回滚保留 `20261113T163828Z-account-management`，其 binary/Node/driver 散列未变且 runtime 已验证；备份 `/var/lib/openai-login/backups/accounts-before-20261114T021850Z-paused-actions.db` 为 0600、完整性 ok。用户要求禁止自动回滚，本轮没有回滚。旧版本不实施新增的单次标记策略，未来获授权切回前必须重新核对没有活跃单次任务；本轮已确认 0，不能拿这个历史快照代替未来检查。

脱敏证据见 [本轮证据目录](evidence/20261114T021850Z-paused-actions/)，重点为 [正式恢复](evidence/20261114T021850Z-paused-actions/PRODUCTION_RECOVERY_ACCEPTANCE.json)、[最终验收](evidence/20261114T021850Z-paused-actions/FINAL_ACCEPTANCE.json)、[单次标记](evidence/20261114T021850Z-paused-actions/MANUAL_ATTEMPT_ACCEPTANCE.json) 与 [清理记录](evidence/20261114T021850Z-paused-actions/CLEANUP.json)。原始诊断候选、发布候选的数据库/密钥副本及唯一上传目录已清理，正式版本、回滚、备份和 operations 保留。

未运行全仓完整套件。没有在生产故意制造错误密码/TOTP、未知 OAuth 故障或进程崩溃，这些由定向及 race fixture 覆盖；未知上游页面仍保守记录原因。本记录不含密码、TOTP 密钥、验证码、token 或原始上游响应。
