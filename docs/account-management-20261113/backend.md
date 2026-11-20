# 账号管理改进：后端开发说明

日期：2026-11-13。依据 [ACCOUNT_MANAGEMENT_PLAN_20261113.md](/Users/tokk/Desktop/KKAI_AUTH/ACCOUNT_MANAGEMENT_PLAN_20261113.md)。文档经产品、后端与前端审查后作为实现契约；代码与发布验证另记实施记录。

## 目标和质量边界

系统按无人值守运行设计：邮箱/手机验证、明确密码错误、TOTP 错误立即停止该账号当前登录与后续自动恢复，保存具体原因，继续处理其他账号。邮箱验证码和手机验证码目前没有可靠的自动收件或短信通道，不能伪造验证结果，也不能无限等待；没有可信系统事件时，这类账号保持稳定终态。网络、429、5xx、Cloudflare challenge 继续按现有有限退避。

拒收这些实现：登录层、恢复层和前端各维护一套错误规则；用任意页面文本猜验证类型；把秘密放入列表 DTO；用内存布尔值表示“允许重试”；把授权信息塞进 request_key、operation_id 或错误消息；新增并行状态机、重复队列或无必要的新表。

## 现有责任边界

- `internal/login/service.go`：`loginWithRetries`、`waitOAuthCallbackContext`、`waitForAuthSelector` 负责浏览器与内层重试。
- `internal/login/errors.go`：`LoginErrorInfo`、`DescribeError` 负责安全错误投影。
- `cmd/server/sub2_recovery.go`：`loginAgain`、`classifyRecoveryLoginError/Info`、`recordRecoveryFailure`、`automaticRecoveryAllowed`、`retryDueRecoveries`。
- `cmd/server/sub2_monitor.go`：`scanSub2Accounts`、`enqueueAutomatic`。
- `cmd/server/credential_repair.go`：资料更新后的续跑。
- `internal/store/account_recovery.go`：恢复任务、lease、凭据版本和 CAS。
- `internal/store/credential_repair.go`：资料修复 revision。
- `cmd/server/history.go`：历史登录、详情和资料更新接口。
- `cmd/server/sub2_import.go`：身份核对后的 Sub2 detail 和 `sub2AccountStatus`。

登录层只识别并返回结构化结果；恢复层映射 durable task；巡检层筛选入队；Store 负责事务和版本。前端不得解析原始页面或响应。

## Sub2 关联缺失：停止恢复并阻止重建（P0）

当前代码中 `sub2RecoveryPoolCandidate` 要求 `Imported && Exists`，恢复 worker 在登录前也读取远端详情。但 `recordSub2NotFoundLifecycle` 只记录事件，检测队列的资格主要来自本地 `imported` 行；`accountDeliveryService.process → ensureTask` 还存在“无绑定、列表无匹配 → 新建导入”的分支。资料修复成功会调用 `FinishAttemptAndQueueDelivery`。这是需要封住的代码路径；仅凭这些静态证据不能断言某个生产账号的具体复活原因。

**证据和固定契约**：仅使用当前 destination 的管理账号详情接口成功到达 Sub2 后返回的真实 404，且请求对应既有绑定，判定该远端对象缺失；反向代理错误页、用量接口 404、认证错误、超时、5xx 和分页失败不是删除证据。完整分页无可信关联时为 `unmatched`，不参与恢复，也不虚构 HTTP 404。自动请求统一返回固定 `sub2_account_missing` 不可重试原因。后端 DTO 明确使用 `association_status=missing`（既有绑定的远端对象已确认缺失）或 `unmatched`（未找到可信关联），不能根据 `exists=false` 默认值独立判定。

**持久边界**：建议在已有 `sub2_imports.state` 增加正式终态 `remote_missing`，保留原 destination、账号、Sub2 ID 和 operation ID，复用现有 lifecycle 记录来源 `sub2`、`not_found` 和真实 404。它是既有绑定的终态，不新建队列/状态机；`syncAccountStatuses`、历史状态读取、discovery、recovery candidate、usage/cost 查询都必须在看到该状态后直接投影 `association_status=missing` 并跳过 Sub2 GET/list/link，不得因为远端 ID 被复用而重新显示或认领。只有显式 reimport/rebind 的原子转移后才允许再次查询。所有状态读取/更新、启动恢复、任务统计和本地记录删除检查都要识别它为终态，通用 `UpdateSub2Import` 不得用迟到结果把它改回 `imported/queued`。不能只删 binding 丢掉停止依据。若旧版本已清掉 binding，使用同账号、同 destination 的明确生命周期证据和历史任务核对；不得依赖前端事件列表或最近 100/1000 条记录截断。历史无证据时显示 `unmatched`，也不能从旧任务推断新建授权。

**收敛事务和竞态**：在现有账号 lease 下，以 binding ID、destination、account ID、Sub2 ID、operation ID 做 CAS，同时持久化缺失状态/事件并终止旧自动任务。检测任务按实际阶段收敛：尚未 Claim 的 `queued` 任务转 `canceled`；Claim 后、尚未发请求且本地资格检查已跳过的任务才可记 `skipped`（`request_attempted=false`）；已发请求或在途任务只能转 `canceled`，并保留 `request_attempted=true`。未完成交付/恢复/延迟复检/资料修复复用各自 `canceled` 终态，清空下一次重试。worker Claim 后、登录前、写 Sub2 前再次检查该状态；在途请求在下一个检查点停止，迟到结果不能重启交付或覆盖新绑定。写入失败要关闭当前账号派发，不能清 binding 后继续创建。新的成功登录仅更新 AUTH，不能解除 Sub2 缺失状态。

**创建与更新必须分开**：恢复、复检、修复和已有绑定的交付只更新该绑定，绑定消失时不得切换为新建。`ensureTask` 命中 `remote_missing` 必须直接返回固定 `sub2_account_missing/reimport_required` 不可重试结果，不能把该 binding 返回给 `accountDelivery.process` 再走 `reconcile`；`processQueued` 和 `reconcile` 首行也必须拒绝 `remote_missing`，不得调用 `listOAuthAccounts` 或 `/admin/accounts/data`。正常新账号的首次“登录并交付”仍由后台执行，其创建意图必须属于本轮请求并持久化绑定到具体任务；已确认缺失的账号只有明确重新导入/重新绑定才能建立新意图。普通 retry、保存资料、登录成功、自动交付全局开关、扫描或重启均不算重新导入授权。实现先复用现有 import/delivery 身份；若当前字段不能区分首次创建与更新，才给现有任务增加最少的显式意图字段，旧任务默认不获得创建权限，迁移须有默认值/重启/CAS 回归。禁止在每个入口复制一套判断，统一由共享服务门禁和 Store 原子操作负责。

由于 `sub2_imports` 当前按 `(destination_key, account_id)` 唯一，显式重导不能通过删除旧行再插入。Store 需要提供一个原子 `AuthorizeRemoteMissingReimport`（名称可按现有风格调整）：持有账号 lease，以旧 binding ID、destination、account、Sub2 ID、operation ID CAS，校验本轮显式 intent 和当前 credential version，将同一行从 `remote_missing` 原子转为新的 `queued` 导入并写入全新 operation/idempotency/payload；同一事务必须终结或替换该账号该 destination 下尚未完成的旧 delivery intent，使旧 worker 即使迟到也无法继续创建或 reconcile。任一校验、旧 intent 收敛或写入失败时，事务整体回滚，不能把状态改回 `queued`。失败、旧 delivery 和普通登录成功不能执行该转移。旧缺失生命周期证据必须保留，直到新导入得到明确结果。

`processCredentialRepair` 在 `BeginAttempt`/`loginWithProxies` 之前读取当前 destination 的 binding 终态；命中 `remote_missing`（或同账号、同 destination 已有明确历史 binding tombstone）时，必须 CAS 将 repair 收敛为 `canceled/requires_action`、清空下一次重试，并且不启动 OAuth。仅有 `unmatched` 不足以阻断资料修复：它可能表示尚未首次交付的正常账号；`unmatched` 只阻断自动恢复、自动新建和无明确意图的旧 delivery。普通手动登录或 `FinishAttemptAndQueueDelivery` 成功只更新 AUTH，不能清除缺失终态；只有显式重导 API 才能调用上述原子操作。

**关键落点**：`scanSub2Accounts/enqueueAutomatic`，`internal/store/account_checks.go` 的入队/Claim 及实际 probe 前，`sub2RecoveryService.enqueue/process/retryDueRecoveries`，`sub2_recheck.go`，`credential_repair.go`，`accountDeliveryService.process`，`ensureTask/processQueued/reconcile` 和创建请求发送前。尤其不能仅过滤新扫描，而漏掉旧 queued/retry_wait 或服务启动后的任务恢复。

**身份与生命周期**：Sub2 缺失不删除 AUTH 账号、密码/TOTP、套餐或历史，也不据此调用 `FinalizeDeletedAccount`。同邮箱远端账号不能越过缺失终态被自动认领，绑定仍需现有可信身份核对。只有另有官方不可用证据时才走官方清理；该既有清理路径亦须防止 `deleted/deactivated/deleted_or_deactivated` 账号被旧交付重建。目的地 A 的缺失不得影响 B 的有效绑定。

## 1. 单一错误分类来源

扩展现有 `LoginErrorInfo`，由 `NormalizeErrorInfo` 把 `Code` 归一化为稳定原因，保留 `Stage`、`HTTPStatus`、`Retryable`、`Message`；当前响应的 `UpstreamCode` 可保留安全原码，持久历史保存规范 `Code`。不重复增加同义状态字段。规范值：

| 规范 Code | 必须证据 | Retryable |
| --- | --- | --- |
| `email_verification_required` | `auth.openai.com/email-verification` 且存在邮箱验证码表单/结构化 OTP 页面 | false |
| `phone_verification_required` | 认证域名的明确手机/SMS 验证路径和可见验证码控件 | false |
| `password_rejected` | password 阶段的结构化拒绝 | false |
| `totp_rejected` | mfa 阶段的结构化 OTP/TOTP/MFA 拒绝 | false |
| `verification_code_rejected` | 验证码被拒绝但无法可靠区分类型 | false |
| `credentials_rejected` | 凭据被拒绝但阶段不足 | false |

普通邮箱输入页、电话号码说明文字、外域页面不能触发验证原因。`incorrect_code`、`invalid_credentials`、`invalid_mfa` 没有阶段证据时，落到通用规范原因，不能让前端猜。

`waitOAuthCallbackContext` 的页面检测顺序固定为：认证域名 → 结构化 auth 状态/HTTP 拒绝 → 邮箱/手机验证 → TOTP → consent。确认邮箱/手机页面后立即返回结构化终态；deadline 不能覆盖已经确认的原因。`waitForAuthSelector` 和 auth status channel 复用同一检测函数。未知页面仍是 `ErrAuthPageTimeout`，保留退避。

`classifyRecoveryLoginInfo` 必须先归一化并处理确定性规范 `Code`，再处理通用 4xx、timeout 和网络错误。确定性结果保存为现有 `retry_action=manual`、`requires_action=true`、`next_retry_at=NULL`；`manual` 只是历史持久字段名，前端应展示为“自动恢复已停止”，不得生成等待人工的主流程。官方删除/停用继续走生命周期清理，不能混入普通凭据错误。

删除边界必须独立：登录确定性失败、邮箱/手机验证、超时、不可调度和 Sub2 远端 404 都不能写成官方删号；只有上游明确 `account_deleted/account_deactivated` 证据才走官方账号清理。Sub2 404 只表示远端对象缺失，保留其来源和身份核对结果。

## 2. 无人值守保护终态

确定性错误一旦落库为 `retry_action=manual`、`requires_action=true`、`next_retry_at=NULL`，本期不自动释放，也不增加人工放行字段或并行状态机。`manual` 是历史字段名，实际含义是“自动恢复已停止”。刷新页面、服务重启、自动恢复开关、定时扫描、页面访问和失败冷却到期都不得重新入队。

- `automaticRecoveryAllowed`、`retryDueRecoveries`、`process` 和资料修复入口都读取最新确定性终态；命中后释放 lease/浏览器并跳过登录。
- 旧任务、迟到结果和轮询不能覆盖新的成功登录；只有已经持久化且通过现有版本/CAS 校验的新成功结果可以取代旧失败。
- 当前没有邮箱/SMS 自动接收或新密码同步通道，因此外部验证账号保持稳定阻塞，不等待、不催促、不猜测验证已完成。
- 未来若接入可信验证完成事件或凭据自动同步，应另立需求，设计事件身份、attempt、版本和 CAS；不得把未来逻辑预埋成定时重试。

## 3. 单字段凭据接口

现有 Credentials 已 AES-GCM 加密，`Account` 和普通历史接口不返回秘密。新增专用 handler：

```http
POST /api/history/{id}/credentials/reveal
Content-Type: application/json
X-Requested-With: XMLHttpRequest

{"field":"password"}
```

`field` 只允许 `password` 或 `totp_secret`，每次一个账号一个字段。唯一成功契约：

```json
{"success":true,"account_id":780,"field":"password","value":"..."}
```

禁止返回整个 Credentials、proxy、access/refresh token、页面响应体或解密错误。响应 `Cache-Control: no-store`，复用现有管理认证、同源校验、JSON 类型和自定义请求头。CORS 不构成权限控制。日志只记录账号 ID、字段名和成功/失败，不记录值。接口只读，不改变登录、恢复、Sub2 调度或凭据版本。普通详情只返回 `credentials_meta.password.state` 和 `credentials_meta.totp_secret.state`，值为 `saved/missing`，不能泄露秘密。

## 4. 名称和套餐 DTO

`sub2AccountStatus` 增加明确字段：

```json
{"name":"号商 | 截止时间 | email","name_source":"current_remote","name_stale":false}
```

`name_source` 限定为 `current_remote`、`cached_remote`、`historical_import`、`local_delivery`、`email_fallback`。只有同 destination 绑定并通过 `verifySub2RecoveryIdentity` 的 detail 才能作为当前远端名称。名称只展示/搜索，不能参与绑定、去重、删除、凭据版本或恢复授权。

来源顺序：当前远端 detail → 同绑定缓存 → 同 destination 历史导入 payload 的非敏感 Name → 同 destination 交付 options 的历史名称 → 邮箱。payload 只服务端提取 Name，不下发加密内容；缓存键包含 destination、AUTH ID、Sub2 ID。可信旧值可带 `name_stale=true` 展示，没有可信值才回退邮箱。

套餐继续使用 `accounts.plan_type`，空值由前端归为 unknown；后端不把 `self_serve_business_prolite` 等值擅自合并。

## 5. 定向测试与代码评审

实现后增加：

- `internal/login`：邮箱/手机页面正负例、密码/TOTP/未知验证码分类、确定性阻塞原因优先于 deadline、普通登录页不误判。
- `cmd/server`：规范映射、无任务/旧 completed/旧 retry_wait 的自动门禁、Claim 后 CAS、明确重试授权、资料 revision、reveal 方法/字段/权限/no-store/无秘密泄露。
- `internal/store`：迁移默认值、授权水位 CAS、旧任务不自动获授权、凭据加密读取。
- `cmd/server/sub2_import_test.go`：name/source/stale、destination 隔离、名称不参与身份核对。

最小验证命令：

```sh
go test ./internal/login -run 'Test(DescribeError|AuthPageWait|WaitOAuthCallback|LoginWithRetries|AccountRejection)' -count=1 -timeout=90s
go test ./cmd/server -run 'Test(Recovery|Sub2Monitor|HistoryCredentialReveal|Sub2AccountStatus|RunWithHistory)' -count=1 -timeout=120s
go test ./internal/store -run 'Test(AccountRecovery|RecoveryRetry|CredentialRepair|Credentials)' -count=1 -timeout=90s
```

测试报告必须记录实际命令、退出码和行为断言；不能用测试名称存在或编译通过冒充通过。错误分类只有一个后端真源，前端只展示规范值，不复制规则。新增字段必须有迁移、旧数据默认行为、CAS 和回滚测试；不要混入无关历史改动。

## 代码审查后的持久化细化

- 原有 `account_deliveries` 增加 `allow_create`（旧行默认 0）和 `import_operation_id`（旧行默认空）；普通新登录只有无既有 binding/delivery 历史才有首次创建资格。旧任务必须保持更新权限。显式重导替换 intent 时用 operation 防止迟到 worker 修改，并在同事务解除旧终态 recovery 的 delivery 关联，保留任务及身份历史。
- 原有 `account_credential_repairs` 增加 `recovery_task_id`（旧行默认 0）。显式新资料交接使用 `recovery_queued` 状态，绑定具体 recovery task、revision 和原成功版本；开始新 attempt 时在同事务消费。旧 completed 行不会获得新授权，新失败后不重放。
- `accounts.credential_version` 是 DTO 对最新成功 attempt ID 的安全投影，无账号表新列。前端用它与任务 source/result 版本比较，只忽略已被新成功超越的六类确定性登录拒绝；身份、分组、暂停和 Sub2 缺失不受此规则影响。
- 邮箱/手机验证无法自动解决时保持终态，系统继续其他账号。资料更新与显式重导只是已有的可选管理操作，不是无人值守主流程的等待步骤。

## 实现补充（2026-11-14）

`remote_missing` 保留绑定身份，Store 在一次事务内记录生命周期并取消同目的地旧检测、交付、恢复、延迟复检及适用的资料修复。后台详情请求在发出前捕获 operation，迟到 404 只能 CAS 该旧 operation；历史轮询通过这一事务取消在途工作，worker 在后续持久化检查点停止。普通导入完成写和 delivery 更新均携带 operation fence。已 canceled 的 recovery 不接受迟到完成写。

交付任务增加 `allow_create`（旧行默认 false）和 `import_operation_id`（旧行默认空）。只有真正首次登录交付或显式导入授予创建意图；已有同目的地 binding/delivery 历史的普通登录和资料修复仅允许更新。显式重导将原 missing binding 原子转为新 operation，同时替换同凭据版本的 delivery intent。该事务解除被替换 intent 的旧终态 recovery.delivery_id 关联以释放唯一键，保留 recovery task ID、目的地、远端身份和诊断历史；新 handoff 不能复用旧终态 task。

已确认缺失或只有历史缺失证据的账号不再请求 usage/cost，也不会通过列表认领同邮箱新远端对象。Sub2 缺失自身保留 AUTH 凭据；若另有独立官方 deleted 证据，允许既有官方清理流程清除本地秘密，同时保留 missing tombstone。普通状态读取不会产生官方删除证据。

安全列表的 `credential_version` 是最新成功登录 attempt ID；delivery 投影 `source_credential_attempt_id` / `result_credential_attempt_id`，仅用于识别已被新成功结果取代的旧登录错误，不包含密码或令牌。

复审闭环：官方清理 GET/DELETE 的 404 同样必须通过可信管理错误判定；不可信账号详情 404 分类为可退避 `protocol_error`，不得显示成已删除。官方 deleted 清理保留 tombstone 时，原导入 payload 也在同事务清除 OAuth 字段，仅白名单保留账号 Name。未绑定的 update-only 修复交付若已取消，用户显式首次导入可原子替换为有创建资格的新 operation，旧 snapshot 无权覆盖。
