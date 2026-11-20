# 账号管理改进：验收计划

日期：2026-11-13
范围：`ACCOUNT_MANAGEMENT_PLAN_20261113.md` 中的五项需求。本文是实现后的验收清单，不表示本轮已经实现或上线。

用户已授权文档审查、代码实施、代码独立复审及上线。先执行定向验证，记录实际命令、退出码、耗时和失败摘要；部署遵循 SYS1_DEPLOYMENT.md 的候选、服务身份浏览器、备份、切换及延迟验收门禁。不得为验收删除或重建生产 Sub2 账号。

## 通过门槛

P0 失败时不得进入发布准备：确定性终态错误必须停止所有自动重试，且不能丢失错误阶段与原因。L3 凭据接口若存在未认证、跨站、批量读取或明文泄露，必须阻止合并。

P1 失败时不得宣称用户需求完成：账号名称必须能区分同邮箱前缀，套餐筛选必须与分页、选择和批量操作一致，错误文案必须与证据精度相符。

普通网络、429、5xx、Cloudflare challenge、官方明确删除/停用是回归场景，不能因本次确定性错误改造而改变原有退避或清理规则。

删除误判是阻塞项：邮箱/手机验证、密码/TOTP 拒绝、超时、`schedulable=false` 和 Sub2 404 均不得触发官方删号；必须有上游 `account_deleted/account_deactivated` 证据。Sub2 404 与官方删除来源分开验收。

Sub2 缺失也是 P0 阻塞项：确认远端 404 或完整列表确认无关联后，自动巡检、检测结果、恢复 worker、资料修复成功、旧 delivery/retry_wait 和服务重启都不得登录、创建恢复任务、创建 `sub2_imports`、发送导入请求或重新开启调度；只能保留“Sub2 已删除/关联不存在，自动恢复已跳过”。

旧交付防复活验收：删除 binding 后重启服务、定时扫描、旧 `retry_wait`、旧 credential repair 和迟到 worker 结果均不得发送 Sub2 创建请求；`ensureTask` 自动恢复路径缺 binding 必须返回固定 `sub2_account_missing`/不可重试结果。官方 `deleted/deactivated/deleted_or_deactivated` 账号同样不得因旧 delivery 重新导入。只有本轮显式首次交付或明确重新导入/重新绑定才可生成新 operation/idempotency key；并发旧 delivery 与显式重新导入最多允许一个新 operation。当前首次登录并交付的正常行为必须保留。

自动关联防误认验收：同邮箱候选、新登录成功或凭据更新均不能越过 `remote_missing`；旧关联缺失后不得经 discovery 自动认领另一远端账号。重绑定仍需核对 destination、稳定账号身份和 operation。

## P0：Sub2 缺失后的专项验收

| 场景 | 必须观察 |
| --- | --- |
| 绑定对象管理详情返回可信 404 | 绑定持久化为 `remote_missing`，DTO 为 `association_status=missing`；AUTH 账号、凭据和历史保留；无新令牌检测、OAuth、恢复或导入请求 |
| 完整列表无可信关联 | DTO 为 `unmatched`，退出自动恢复；不伪造 404 或官方删除；古老账号无新建意图时不能靠旧交付创建 |
| 超时、401/403、5xx、代理 404、分页缺失 | 仍为 `unknown/stale`，不写 `remote_missing` 或删除事件；成功同步后可继续正常判断 |
| queued/running/retry_wait、延迟复检和资料修复 | 排队任务自动终结，清空重试时间；已发请求在下一检查点停止；迟到结果不创建新 delivery/import，不恢复调度 |
| 重启、重复扫描、全局开关、更新凭据、新登录成功 | 缺失终态保留，旧任务不能重新入队；缺失后连续扫描两轮和重启均无创建请求 |
| 首次登录并交付 | 具有本轮明确创建意图的新账号仍正常导入；已有绑定的更新任务不能在缺失后切成创建；无可证意图的历史任务不能获得创建权限 |
| 并发旧 404 与新绑定 | binding ID/destination/account ID/Sub2 ID/operation ID 的 CAS 阻止旧结果修改新关联；迟到任务不能覆盖新意图 |
| 绑定清除、超过事件列表截断上限 | 同账号/同 destination 的持久缺失证据仍能拦住旧创建，不能只查 UI 的最近事件 |
| `remote_missing` 连续刷新或 Sub2 ID 复用 | 状态同步、discovery、recovery candidate、usage/cost 不再 GET/list/link；仍显示 Sub2 缺失；只有显式原子 reimport/rebind 后才可重新核对 |
| `remote_missing` 旧 delivery | `ensureTask`、`processQueued`、`reconcile` 均立即返回 `sub2_account_missing/reimport_required`；不调用 `listOAuthAccounts`、reconcile 或 `/admin/accounts/data` |
| `remote_missing` 旧 import task | `RequeueSub2Import`、payload refresh、cleanup/reconcile 和服务重启恢复均拒绝终态；不得改回 `queued/imported` |
| 存储失败与目的地隔离 | 缺失落库失败不继续派发或新建；目的地 A 缺失不终结 B 的有效任务 |
| AUTH 官方不可用状态回归 | `deleted/deactivated/deleted_or_deactivated` 均不能被旧交付重新导入；Sub2 缺失本身不触发官方清理 |

资料修复必须在 `BeginAttempt/loginWithProxies` 前检查 `remote_missing` 或同账号、同 destination 的明确历史 binding tombstone；命中后只允许 CAS 终结 repair，不得产生 OAuth 请求。仅有 `unmatched` 不得阻断尚未首次交付账号的正常资料修复，但必须阻断自动恢复、自动新建和无明确意图的旧 delivery。由于 `(destination_key,account_id)` 唯一，显式重导必须用 Store 原子转移 `remote_missing → queued`，校验旧 binding 全部身份、当前凭据版本和本轮新 operation/idempotency；同一事务还要终结/替换旧 delivery intent，任一步失败整体回滚；旧缺失证据保留，普通登录成功不能替代该转移。

实现后在现有 import/delivery/monitor/recheck/repair/store 测试中新增以上用例，统一使用 `TestSub2Missing` 前缀；不得把零匹配算成通过。断言真实调用计数：probe、OAuth、import POST、apply 和 enable 调度；不能只断言前端文案或某个布尔值。`processQueued` 实际导入端点为 `/admin/accounts/data`，还需覆盖创建请求发送前门禁。

## P0：明确验证错误的分类与停止重试

| 场景 | 测试层 | 必须观察 | 现有覆盖/待补 |
| --- | --- | --- | --- |
| 明确邮箱验证码页面 | `internal/login` 浏览器/错误单元 | 阶段为 `email`，原因码为邮箱验证，停止当前登录重试；不填 TOTP，不设置 `next_retry_at` | 现有 `TestWaitOAuthCallbackBrowser`、`TestDescribeErrorDistinguishesAuthPageWaitFromTerminalPage`；需新增 `/email-verification` 页面 fixture |
| 明确手机号/SMS验证页面 | `internal/login` 浏览器/错误单元 | 阶段为手机验证，固定原因文案；页面类型需有可信 DOM/结构化证据，不能仅因出现 `phone` 文本命中 | 需新增手机验证页面 fixture；当前没有生产样本，必须在报告中标注未覆盖的真实页面变体 |
| 明确密码拒绝 | `internal/login` + `cmd/server` 恢复 | 原始阶段 `password`，密码错误原因；当前轮和恢复任务均进入稳定终态（现有 `manual/requires_action` 投影），不再自动重试 | 现有 `TestLoginWithRetriesKeepsTerminalFailuresTerminal`、`TestLoginWithRetriesDoesNotReplayDeterministicFailures`；需补 `invalid_password`、`incorrect_password` 和 HTTP 4xx 页面路径 |
| 明确 TOTP/MFA 拒绝 | `internal/login` + `cmd/server` 恢复 | 阶段 `mfa`，显示 TOTP/验证码拒绝；不把所有 `incorrect_code` 武断写成 TOTP 密钥错误 | 现有 `TestAuthHTTPStatus`、`TestAuthResponseDetailsCodePrecedence`、`TestAccountRejectionPageBrowser`；需补 `invalid_otp`、`invalid_totp`、`invalid_mfa` 及未知验证码类型 |
| 明确邮箱/手机验证后 deadline | 登录浏览器 | 先保存页面原因，再结束；不能把外部验证覆盖成普通 `timeout` | 现有 `TestAuthPageWaitContextErrorPreservesCancellationAndOtherFailures`；需新增验证页面与 deadline 竞态 |
| 普通邮箱输入页 | 登录浏览器 | 正常继续，不误判“需要邮箱验证码” | 需新增负例：`/log-in` 及邮箱输入控件 |
| 取消、迟到响应、重新登录成功竞态 | `cmd/server` 恢复/存储 | 取消不变成可重试；旧失败不能覆盖新成功；只有可信系统放行事件才能开启新一轮 | 现有 `TestSub2RecoveryStaleRetryDoesNotOverwriteNewLogin`、`TestSub2RecoveryStaleFailureSameMillisecondDoesNotOverwriteNewRound`、`TestRecoveryRetryCheckpointNeverAdoptsNewerLogin` |
| 自动巡检重新拉起 | `cmd/server` monitor/recovery | 明确确定性阻塞后，巡检、恢复 worker、资料修复、服务重启和定时扫描均不自动创建新任务；只有可信验证完成事件、凭据新版本或新成功登录结果才可启动新一轮 | 现有 `TestSub2MonitorEnableScansChecksAndRecovers`、`TestSub2MonitorReusesFresh401AndHonorsFailureCooldown`；需补“无恢复任务、旧任务 completed、旧任务 retry_wait”三个门禁场景 |
| Sub2 关联已删除/不存在 | `cmd/server` import/delivery/recovery/monitor | `association_status=missing/unmatched` 后（不能只依据默认 `exists=false`）不检测、不登录、不恢复、不自动重导入、不恢复调度；AUTH 账号保留；只有显式重新导入/重新绑定才可创建新关联 | 现有 `TestSub2NotFoundRecordsOneLifecycleEventAndTransientFailureDoesNot`、`TestListSub2AccountStatusesReadsDetailAndDistinguishesMissing`；需补旧 delivery、credential repair、`ensureTask` 和服务重启门禁 |
| Sub2 状态暂时未知 | import/status/recovery | timeout、5xx、列表不完整为 `unknown/stale`，不写删除证据、不自动导入、不把它显示成已删除；成功同步后再判断 | 需补列表分页失败、代理失败、状态缓存过期和恢复候选负例 |
| 非人工可恢复错误 | 登录/恢复 | 网络、429、5xx、challenge 仍按原有有界退避；不能被人工规则吞掉 | 现有 `TestLoginWithRetriesRetriesCloudflareChallenge`、`TestLoginWithRetriesRetriesTemporaryHTTPFailures`、`TestRecoveryRetryBackoff`、`TestSub2RecoveryTemporaryLoginFailureRetriesBeyondThree` |

错误契约验收要求：后端共享登录/恢复层是原因码和证据的唯一真源；前端只按固定原因码和阶段映射文案，不能重新根据 HTTP 状态、任意页面文字或 `last_error` 猜分类。原因码、阶段、固定中文文案、HTTP 状态和 `retryable` 必须互相一致；不返回页面全文、验证码、密码、令牌或原始响应体。`incorrect_code` 或 `invalid_credentials` 证据不足时使用“验证码错误，验证类型待确认”或“登录凭据被拒绝”，不得凭猜测写成 TOTP/密码错误。

## P1：账号名称、前缀和套餐筛选

| 场景 | 必须观察 | 现有覆盖/待补 |
| --- | --- | --- |
| 当前 Sub2 名称读取成功 | 账号行显示完整远端 `name`、邮箱、AUTH/Sub2 ID 和套餐；长名、中文可折行，名称与邮箱相同不重复 | 现有 `TestListSub2AccountStatusesReadsDetailAndDistinguishesMissing`；需补 `sub2AccountStatus.name` DTO 断言 |
| 远端名称暂时不可读 | 保留最近可信名称并标记待同步；不能用另一个 destination 的 Merchant/Deadline 拼接冒充当前名称 | 现有状态缓存测试 `TestSub2AccountStatusCachesBriefly`；需补缓存失效、绑定变更、同步错误回退 |
| 历史名称/本地交付名称回退 | 仅使用同一绑定、同一 destination 的历史快照；无可信名称显示“名称未同步/未保存” | 现有 `TestHistoryDeliveryOptionsAvailableBeforeRelogin`、`TestAccountDeliveryQueriesStayWithinDestination`；需补 destination 隔离与无名称空态 |
| 名称搜索 | 搜索完整名称、前缀文本、邮箱、AUTH ID 均能命中；名称只展示/搜索，不参与绑定、删除或去重 | 需新增 `client.test.cjs` 列表搜索 fixture |
| Plus/Free/其他套餐 | `plan_type` 动态生成筛选项；空值为“未知”，绝不能并入 Free；新值可直接筛选 | 当前 `client.js` 已显示 `planType`，需新增筛选 fixture 和未知值断言 |
| 联合筛选、刷新、分页 | 账号类型与状态、搜索取交集；改变筛选回第一页；刷新保留条件；即使刷新后该套餐当前为 0 条，已选套餐选项也保留并显示 0 匹配，不能偷偷恢复“全部” | 需新增 `client.test.cjs`：组合筛选、零结果、刷新、分页和当前页全选 |
| 筛选后的选择/批量操作 | 沿用跨页选择，清楚提示隐藏选中数量；操作按固定 ID；“需处理”定位入口不会被套餐筛选遮挡 | 现有客户端批量选择测试；需补过滤后选择和隐藏选中断言 |

名称来源必须能在测试或接口输出中区分“当前远端名称、缓存名称、历史导入名称、本地交付名称、未同步”，使用明确的 `name`、`name_source` 和名称过期标记；状态快照 stale 与名称缓存过期不是同一语义。接口字段使用正式契约，不兼容未约定的 `display_name`。不得把当前全局默认号商重造旧账号名称，也不得暴露加密 import payload。

## L3：登录资料查看与敏感信息保护

| 场景 | 必须观察 |
| --- | --- |
| 列表/普通详情 | 只显示邮箱和“密码已保存/2FA 已配置”等状态；不返回密码、TOTP、代理、access/refresh token |
| 按需显示/复制密码 | 只请求一个账号、一个白名单字段；成功显示真实值/复制真实值；没有值显示未保存或已清理 |
| 按需显示/复制 TOTP | 与密码分开显示；明确这是 TOTP 密钥，不是当前六位验证码；字段白名单拒绝代理、token、批量 ID |
| 关闭、切号、隐藏、页面隐藏 | 清除前端敏感值；迟到响应不能写入新账号或已关闭面板；不写 localStorage/sessionStorage/URL/日志 |
| 接口边界 | 复用现有管理认证；拒绝未认证、跨站、错误方法、错误 Content-Type、自定义字段和批量读取；响应 `Cache-Control: no-store` |
| 查看行为 | 只读，不启动登录、不解除保护终态、不自动开启 Sub2 调度；页面访问和保存动作本身不能绕过放行门禁 |

确定性终态的统一 UI 断言：`requires_action=true && retryable=false && next_retry_at=null` 时，首行必须显示“自动处理已暂停”和具体原因/时间，主操作只能是“查看详情”；不得出现“下次自动重试”“等待后台执行”或“检测并恢复”。

现有相关安全回归：`internal/store/store_test.go` 的 `TestCredentialsAreEncryptedAndRoundTrip`、`cmd/server/history_test.go` 的 `TestHistoryListAndDeleteEndpoints`、`TestHistoryRecoveryFailureProjectionIsRedacted`、`cmd/server/credential_repair_test.go` 的 `TestHistoryCredentialPatchValidationAndSecrets`、`cmd/server/stream_test.go` 的 `TestServeLoginStreamFailureUsesStructuredSafeResult`，以及 `cmd/server/client.test.cjs` 的 HTML 转义断言。必须新增单账号 reveal handler 的未认证/字段白名单/响应不缓存/日志脱敏测试，以及前端异步切号和复制行为测试。该接口属于 L3，不能用“前端默认掩码”代替后端权限验证。reveal 必须使用约定的固定响应结构（`success/account_id/field/value`），前端不得为了兼容未约定的 `data.value` 而放宽契约。

## 历史三个邮箱验证账号

AUTH #780、#799、#992 已有 journal 证据表明密码和 TOTP 通过后进入邮箱验证码页面。历史 `login_attempts` 的 timeout 仍保留；若实现补充原因，只能增加带账号、attempt、时间、证据来源的受控补注或详情说明，不能全局把历史 timeout 改写为邮箱验证，也不能自动重新入队。AUTH #1510 的 `incorrect_code/403` 保留为 MFA 阶段失败，不凭旧记录断言 TOTP 密钥错误。

## 分层定向命令

以下命令在实现完成后执行；`-run` 名称须按实际新增测试调整。每条失败只重跑对应层，不升级为全量套件。

```sh
# P0 登录分类和浏览器等待
go test ./internal/login -run 'Test(DescribeError|AuthPageWait|WaitOAuthCallback|LoginWithRetries|AccountRejection)' -count=1 -timeout=90s

# P0 Sub2 缺失专项（实现后以实际测试名对齐）
go test ./cmd/server -run 'TestSub2Missing' -count=1 -timeout=90s
go test ./internal/store -run 'TestSub2Missing' -count=1 -timeout=90s

# P0 恢复/巡检/无人值守终态门禁
go test ./cmd/server -run 'Test(Sub2Recovery|Sub2Monitor|Recovery|AccountDelivery|RunWithHistory)' -count=1 -timeout=120s

# P0 存储重试、版本和任务隔离
go test ./internal/store -run 'Test(AccountRecovery|RecoveryRetry|CredentialRepair|AccountCheck|AttemptHistory)' -count=1 -timeout=90s

# P1 名称、状态和生命周期
go test ./cmd/server -run 'Test(Sub2AccountStatus|ListSub2|History|AccountCheckOfficialLifecycle|DeleteDeletedAccount)' -count=1 -timeout=90s

# L3 reveal 权限与前端行为（实现后补充测试名）
go test ./cmd/server -run 'Test(HistoryCredentialReveal|HistoryCredential)' -count=1 -timeout=90s
node --check cmd/server/client.js
node cmd/server/client.test.cjs
```

允许在上述命令之前运行 `git diff --check`、针对变更文件的静态检查和单个测试。不要在本轮执行 `go test ./...`、仓库级 lint/build 或生产部署；若确有全量需求，应先说明预计时长、针对性检查为何不足并等待单独批准。

## 证据包与发布阻塞项

验收完成后应保留：测试命令及结果、P0 错误分类样例（脱敏）、恢复任务状态变化、P1 列表/筛选交互截图或客户端断言、L3 接口权限与无缓存断言、名称来源及 destination 隔离证据。真实生产 OAuth、邮箱/手机验证码接续、接码平台和 Sub2 删除不属于本轮验证，不能以模拟 fixture 结果代称真实成功。

以下任一项存在即阻塞交付：

- Sub2 关联缺失后仍检测/登录/恢复/重新导入，或仅阻止新扫描而遗漏旧交付；
- 邮箱/手机/密码/TOTP 明确错误仍会在任一后台入口重试；
- 错误文案把证据不足的 generic code 猜成具体验证方式；
- reveal 接口可从列表、未认证或跨站请求拿到明文，或明文进入日志/缓存；
- 名称跨 destination 串用、同步失败时丢失唯一前缀、名称参与安全绑定；
- 空套餐被归入 Free，筛选改变后选择范围错误；
- 只验证前端显示，未验证后端数据契约和任务状态；
- 以历史 timeout 的补注替换原始事件，或声称外部验证后后台会自动接续。

## 代码评审明确拒收项

这些不是“后续优化”，出现时应直接拒收实现或要求返工：

- 登录层、恢复层、前端各自维护一套邮箱/手机/密码/TOTP规则，出现重复映射、优先级不同或同一错误在不同入口显示不同原因；分类必须有一个共享来源。
- 用“所有非 2xx 都是密码错误”兜底，或用任意宽泛的 `phone`、`sms`、`email` 文本匹配手机号/邮箱验证；必须有可信认证域名、路径和控件/结构化响应证据。
- 未知验证码框也自动填 TOTP，或把 generic `incorrect_code` 直接宣称为 TOTP 密钥错误。
- 将明文密码/TOTP 放进列表或普通详情响应、全局 `historyAccounts` 缓存、`innerHTML`、DOM 属性、日志、异常消息、localStorage/sessionStorage、URL 或测试输出；前端转义不能替代“明文不进入 HTML”的要求。
- reveal 接口把整个 `Credentials` 结构、代理、access/refresh token 一并返回，允许批量账号/任意字段，或仅依赖 CORS/自定义请求头声称已完成权限保护。
- 以非法状态字符串、任意 map 字段或错误 `operation_id` 偷塞名称/凭据，让前端“看起来有数据”；DTO 必须有明确字段、类型和来源校验。
- 用名称、邮箱或 Sub2 ID 替代凭据版本、目的地、绑定 operation ID 等安全校验；名称只能展示/搜索，不能改变绑定、恢复、删除和幂等判定。
- 为确定性阻塞新增并行状态机、重复重试队列或新表，而现有 `manual/requires_action`、最新登录记录和恢复任务已经足够；若确需放行水位，必须先给出字段不足、迁移、CAS 和回滚证据，并证明它只接受可信系统事件。
- 只在方案中写入尚未实现的测试名称，或用正则“匹配到测试文件”就报告通过；验收报告必须贴实际执行命令、退出码和真实测试摘要，未实现的用例标记为缺口。
