# AUTH 账号生命周期开发规格

规格日期为 2026-11-19，实施记录更新于 2026-11-20（北京时间）。本地实现及验证结果见文末。适用范围：KKAI_AUTH 管理的 OpenAI OAuth 账号及其已配置的 Sub2 目的地，不涉及 Sub2 的其他账号或平台。

**实施目标**：解决账号表重登后原 Sub2 仍用旧刷新令牌的问题；把新确认的 Plus／Team 降级账号留在 AUTH、退出 Sub2；官方停用、删除及明确拒绝账密或要求邮箱验证的账号从所有自动处理入口永久退出，由后台安全清理，不向用户派发确认任务。以本文规则取代旧版「团队切个人 Free 后写回并恢复调度」的行为；不改变非终态账号原有的 [显式重导协议](account-management-20261113/reimport-20261114.md)。独立 [命令行登录／导出](../cmd/main.go#L90) 不走 Web Store、队列或 Sub2 管理接口，不纳入本期；不继承旧发布记录中的任何生产操作授权。

**当前实现范围（2026-11-20）**　Sub2 沿用现有版本。AUTH 清理使用已有的停调和删除接口，回读确认目标已停调、归属仍一致且没有关联的 Spark 子账号，删除后再回读确认目标不存在。现有删除接口按 ID 执行，外部管理员在最后一次校验与删除之间的并发修改仍有时间窗口。下文的 Sub2 专用接口和回执表属于已收起的本地试作。AUTH 的重登写回分支仍引用试作接口，完整发布尚未就绪。

## 1. 根因与边界

| 问题 | 本地代码证据 | 修复落点 |
| --- | --- | --- |
| 普通重登没有反映到 Sub2 | [账号表](../cmd/server/client.js#L2307) 只在旧交付需要处理时默认设 `auto_deliver`；[历史登录](../cmd/server/history.go#L407) 否则只保存本地。不是所有 401 都走到 Sub2 写回。 | 已核实绑定的账号表重登由后端原子落「只更新原绑定」交付，不再依赖按钮判断、旧交付状态或第二个浏览器请求。 |
| Free 仍写回并恢复调度 | [旧恢复测试](../cmd/server/sub2_recovery_test.go#L538) 明确要求同一账号从 Team/Business 切到个人 Free 后写回原 Sub2 并开启调度；[旧导入任务](../cmd/server/sub2_import.go#L349) 遇到新版本还会重做 payload。 | OAuth 结果落库时统一决策；Free 不能进入导入、写回、启用或重试分支。旧测试改成否定远端写回，保留身份正反例。 |
| 官方终态没有统一清理 | [恢复探测](../cmd/server/sub2_recovery_verify.go#L235) 把官方明确错误归为人工 `account_unavailable`；[检测提交](../internal/store/account_checks.go#L565) 记审计但未统一设置账号终态及清理任务；[清理失败分支](../cmd/server/sub2_import.go#L1564) 遗漏部分 401/403/429，且用重读的最新版本标记重试；[本地凭据清理](../internal/store/store.go#L1280) 仅接受 `deleted`。 | 所有可信信号汇入同一终态事务。先落证据与清理意图，再调用 Sub2；即使第一次请求没有发出去，也能重启续跑。 |
| 恢复完成不等于能调度 | [现有恢复](../cmd/server/sub2_recovery.go#L685) 已有写回、Sub2 探测和调度回读；[状态计算](../cmd/server/sub2_import.go#L947) 才额外检查限流、过载、临停和过期。 | 复用恢复执行器及复检；只有更新本次版本、探测成功且实际可调度，才显示「可用」。 |

**历史基线，非本轮生产查询**：上轮 `sys1` 脱敏只读快照（2026-11-19 21:58，北京时间）中，159 个 OpenAI OAuth 账号有 11 个报错，11 个均停调且均有同版本写回标记，后续重登为 8 个用户名／密码错误、3 个要求邮箱验证。不能归为这一批「没写回」，写回标记也不能证明 RT 后续有效。同期近 7 天 8 次已绑定账号的单次登录成功，7 次未找到同版本交付或恢复任务。另有 6 条已绑定记录当前为 Free，其中 3 条调度开关开启，不能仅凭此推断曾是付费号。188 条 AUTH 终态记录没有有效绑定（162 条无绑定、26 条 `remote_missing`）；均有密码密文、180 条有 RT 密文。实施前重新取证，不将上述数字当作待清理 ID 或现况。

## 2. 业务规则与证据

**同一账号的决策顺序：可信官方终态或本地废弃 > 已确认 Free 仅本地 > 合格付费更新。**「绑定缺失」「暂停」「冷却」是相应目的地的独立事实，不是账号套餐或官方状态；失败、未知、版本过时不升格为任何终态。

| 决策 | 生效条件 | AUTH | Sub2 |
| --- | --- | --- | --- |
| **R1：Plus／Team → Free** | 同一 AUTH 账号本次可信登录明确 `free`，且有此前 Plus／Team（含 Business 别名）的可靠证据。401 后自动恢复、账号表主动重登均适用；切到同一用户的个人工作区不否决。 | 保存本次 OAuth，保留账密、历史，标「Free · 仅本地」。以后失败或空套餐不解除退池门禁。 | 有原可信绑定：先确认停调，再删除该绑定指向的账号；无绑定：不创建、不按邮箱猜绑。严禁写入这版 Free 的 AT/RT 或再次启用。 |
| **R2：官方已删除或停用** | 可信 OAuth、官方检测或当前 Sub2 探测明确返回删号／停用信号，且请求使用的身份、版本、绑定仍匹配。官方明确提示「已删除或停用」也视为终态。 | 永久停止登录、检测、修复、恢复、交付、重导；清除敏感凭据，保留安全账号行、审计及历史费用身份。没有「待核」或「手动复活」。 | 自动停调并删除已核实的原对象；已可信确认不存在则无需再删。清理状态不改变官方终态。 |
| **R2 补充 · 本地废弃** | 当前登录明确拒绝密码、拒绝用户名与密码组合，或明确要求邮箱验证。 | 保留账号表、具体错误及证据，显示「废弃」，清除秘密并停止后续尝试。 | 沿用 R2 的原绑定停调与删除流程。 |
| **R3：已绑定、合格付费** | 同一用户、当前明确受支持的付费套餐、完整同源 AT/RT、原目的地有可信 `imported` 绑定。 | 本次成功版本与只更新原绑定的交付在同一事务提交。 | 更新原 ID，探测后按启用意图恢复调度；保留远端配置。绑定被替换、缺失或身份不符时不降级成建号。 |
| **未知／不在范围** | 套餐为空、未知、同次凭据套餐冲突；或存量显示 Free 但没有可信历史退池证据。 | 未知不显示为 Free，记录原因；存量未证实降级不产生删除意图。新取得的明确 Free 凭据仅本地，不交付这套付费池。 | 不用未知或 Free 版本创建、刷新、启用付费池账号；没有独立可信证据，不批量停调或删除现有绑定。未知账号仍可通过合法登录取得新证据。 |

- **套餐**：统一在后端归一化，保留原值用于展示和审计。沿用 OAuth 的 ID token 优先、缺值回退同次 AT 的规则；校验两者是否确为同一授权结果，出现互相矛盾的显式套餐时保守归未知。枚举至少覆盖 `plus`、`team`、`business`、`self_serve_business_prolite`；现有受支持的其他付费（如 `pro`）继续兼容，但不凭未知别名或 `pro → free` 擅自授权本需求的存量删除。`plan_type` 当前会被新成功覆盖，[登录历史](../internal/store/store.go#L304) 不记录套餐；写入前保存可信旧套餐、工作区、来源和观察版本，不能用当前 Free、名称、付费分组或错误次数倒推。每条成功 attempt 的套餐观察必须关联准确的成功 attempt ID、观察来源和同次工作区。`token_refresh` 没有新的可信套餐 claim 时可以沿用显示套餐，但不能推进最近已确认付费证据。`Plus → 空套餐刷新 → Free` 仍只能引用 Free 之前最后一个明确付费观察，`Plus → Pro → Free` 则按最近的明确 Pro 观察处理，不能用更早的 Plus 证据越过 Pro。新证据只来自可信新登录或确切对应原版本的历史证据。
- **身份**：新登录仍须通过当前 OAuth 信任边界、邮箱一致及身份核对；旧远端以变化前的已核实绑定和工作区快照识别，不能用新 Free 个人工作区去匹配旧 Team 对象。可比较的同一命名空间的稳定用户主体有冲突时不写不删；不同工作区的 `chatgpt_account_id` 或未证明跨工作区稳定的 claim 不是主体冲突。缺可信所有权证据时不凭邮箱、名字或可见分组猜测目标。
- **官方证据**：只接受白名单化的 `account_deleted` / `user_deleted`、`account_deactivated` / `user_deactivated`、`account_disabled` / `user_disabled`，或可信官方来源产生的确切「已删除或停用」结构化结论。保留上游原分类、来源、请求阶段、HTTP 状态、`account_id`、检测的 `credential_attempt_id` 和可回链的登录 attempt ID；不能只传通用 `account_unavailable` 再从文案猜。存量预演必须按 AUTH ID 回查原始生命周期事件及结构化原因，并确认该事件没有被后续成功版本取代。只有无法回链的旧 `account_unavailable`、Sub2 `disabled`、旧报错或截断列表记录时，列入「证据不足」盘点，不转终态、不清理、不发「待核」任务。普通 401、AT/RT 过期／吊销、密码/TOTP 拒绝、邮箱/手机验证、429、网络故障、Sub2 `disabled/error` 与管理接口 404 均不能作为官方终态证据。
- **恢复边界**：可信新付费登录可解除 Free 门禁，但不继承旧创建意图，也不复用已退池的绑定。若旧对象已移除，走原有显式导入／显式重导；若旧清理还未结束，不迁移旧意图到新绑定、不自动建号，用当前证据判断旧工作是否已被取代。官方终态不可由后来的普通登录请求、新填密码、手动重导、批量登录按邮箱 upsert 或开关切换解除。

**最小持久化契约**：可信套餐观察只记一处，在现有 `login_attempts` 保存成功 attempt 的非敏感套餐原值、来源、工作区标识；时间和版本复用该 attempt。读取本次观察和最近一次明确观察计算门禁：本次未知不能写 Sub2，最近一次明确为 Free 则仍保留仅本地标记；最近一次明确观察为 Plus／Team 才可支持随后的 Free 退池。`accounts.sub2_policy` 保存跨请求、跨 worker 的最终写权限，限定为 `eligible`、`free_local`、`official_unavailable`、`discarded`、`unknown`，旧库迁移默认 `unknown`。官方终态同时沿用 `accounts.status` 和独立 lifecycle 证据。可采信升级前仍是付费的快照，前提是能回链到同账号最近的成功登录或核实版本的导入快照，且没有更新的矛盾观察；`linked-*` 的空 payload、单独一个当前 Free 值、分组或名称都不是历史付费证据。旧库缺证据为未知；仍可检测或登录获取新证据，不继承新的远端写入权限。

## 3. 后端实施

### 3.1 先落决定，再执行远端动作

复用现有 Store 事务、按账号/邮箱的 lease、成功 attempt 版本、`operation_id`、`sub2_imports` 清理 worker 及交付／恢复／复检。`sub2_policy` 只作为共享写权限门禁，不引入第二套账号状态机或令牌副本。分类放在共享登录结果提交及可信探测结果提交边界；前端只展示结论。所有入口的任务**创建、认领、续跑及远端写之前**都调用同一资格检查；不可用缓存决定写权限。

- 事务中读取变更前快照，核对账号、证据使用的版本和请求开始时的绑定。R1 原子保存成功凭据、退池证据、清理意图和同目的地旧任务终止；R2 原子写终态、可信证据、清理意图、敏感字段清空，并取消在途任务。没有绑定也落实本地决定。R3 原子保存本次凭据和交付，无成功但没任务的窗口。失败回滚整笔，并用安全错误提示；远端网络调用不得放进 SQLite 事务。
- `accounts.status` 继续表示本地状态；R2 使终态不可被 `running`、迟到成功或资料写覆盖。在事务内保护按邮箱 upsert 与按 ID 开始尝试；本地删除入口也不能丢掉待清理绑定或抹掉官方墓碑，以免用同邮箱重建绕过。R1 用已有套餐及持久化退池决定表达，不把 Free 当登录失败。用于门禁的证据按账号及版本直接读，不使用前端截断列表、错误文案或旧时间戳近似。
- `credential_version` 是最新成功 attempt ID；版本更新不是权限续期。清理任务固定 `(AUTH ID, destination_key, binding ID, Sub2 ID, operation_id, evidence version)`，保留旧身份核验信息；失败登录 attempt ID 仅作证据引用，不是凭据版本。**失败不能用重读的最新版本改写证据。** 普通更新交付也要固定原绑定 ID、远端 ID 与 operation，绑定换代后终止而非重选目标。标记、作业重跑和终结均用这些条件做本地 CAS。
- 数据改动只补现有表缺失的信息：记录可靠套餐快照，并在清理意图保留原因、证据引用及持久重试水位（次数、下次时间）；更新交付补足固定绑定和启用意图。旧行默认**不提供**新证据、创建许可或启用许可。任何迁移只补结构和可证数据，不执行远端动作，不把 `cleanup_pending` 旧行一律当作已授权。

### 3.2 退池和官方清理

1. 在 lease 下按固定目的地核对远端 ID、`openai/oauth` 和旧工作区身份。远端有 `kkai_auth_import` 时核对已知的原实例、AUTH ID 和原 `operation_id`；无可核实标记时，只认可可证明的唯一旧绑定（含合法 `linked-*` 进入 `cleanup_pending` 后的续跑），并匹配冻结的旧工作区。删除校验不能复用忽略工作区的恢复规则。只读验证也有版本护栏；身份冲突、双绑、目标变化一律不执行远端写入，保持本地退出，按原因停止旧意图或报告平台冲突。**不向用户发待核，不追着新绑定删。**
2. 远端存在且仍是原对象时走「停调 → 读回同一对象已停 → DELETE → 确认结果」。有明确强证据时已停调可跳过重复设置。只有真实的管理账号明确不存在（可信 404，按现有路由、响应类型和错误结构核验）可当清理完成；网关 404 或存在性未知不等于已删除。超时未知先读回，不复用名称查找或盲重放操作。
3. 首次或后续 429（遵循 `Retry-After`）、网络、超时、5xx 和不可信 404 都要持久退避；Sub2 管理 401/403 属平台权限故障，聚合告警、低频退避，恢复后续跑。不存在账号级人工恢复步骤。按到期时间推进，不让一个坏目标长期堵住后面的清理或导入。清理不受自动复活开关影响；DB 不可写时停止远端变更并报警。
4. 旧导入 `queued/failed` 永不升级成新凭据重发；对可能已提交的 `sending/confirming/unknown`，允许按**原**操作标记做只读结果核对，不重 POST、不发现/绑定其他同邮箱对象。若证实它确实建出本账号所属对象，补齐该原操作的清理目标；未确认时只保留非敏感证据及重查计划。清理确认后事务内落结果、退役原关联、取消旧任务并保留原 Sub2 ID、operation 以及可信名称；不得伪造 404。对 R1 可保留带退池原因的 `remote_missing` 墓碑，显式重导必须是后续合格付费成功版本。
5. R2 **从本地终态事务提交起即不可取密**。先固化证据版本和原绑定，再同事务清空账密、TOTP、代理和 AT/RT，清除每个目的地可重放的导入快照。核对和重试仅依赖安全元数据；保留无密 attempt，不让派生版本归零。无绑定、未确认导入、已 `remote_missing` 都覆盖；配置不可用仍完成本地擦除，有旧目标则按原目的地保留清理意图，不能改写到当前实例。旧备份按现行保留政策隔离，不声称已擦除所有介质。R1 保留 AUTH 凭据，只清掉已退役导入的秘密快照。两类都保留审计及最后确认的费用关联，仅用于历史披露，不能通过其访问新绑定或认领其他远端。

### 3.3 重登、凭据写回与实际可用

1. **请求契约**：`/api/history/login` 的普通重登（单个、批量）对已绑定的合格付费账号默认只更新原 Sub2；`auto_deliver` 不再控制这类更新是否入队，也不能授予创建权限。首页批量新登录的「关闭自动交付仅保存」保留；对 `remote_missing` 的「仅重新登录 AUTH」保留，显式重导仍需新版本和原 operation。无绑定的普通账号表重登不建 Sub2。手动重登交付沿用明确动作，不因全局自动恢复关闭而丢弃；后台 401 自动登录仍遵守开关和确定性失败门禁。
2. **只更新原 ID**：从现有 `FinishAttemptAndQueueDeliveryUpdate` 和恢复执行器接续。已绑定更新任务必须直接读取/验证冻结的 `imported` 绑定，不能走会刷新、重排或发送旧创建任务的 `ensureDeliveryTask`。用单账号 OAuth 更新路径的条件写模式，补丁仅包含本次 AT、RT 及同源 OAuth 元数据；不以旧 RT 补新 AT。允许可验证的同一用户切换到另一个合格付费工作区，不放宽旧绑定核验。不调用建号接口、不执行已有账号改分组，不改变名称、备注、分组、出站代理/出口池、优先级、并发、模型映射、账号级有效期及其他非 OAuth 配置。
3. **调度意图**：已有正常调度或有本轮可信的 AUTH 停调/恢复授权时，写回并通过探测后恢复开启。明确的人工暂停保留；`error + schedulable=false` 本身不能证明由平台自动暂停。没有可信恢复授权的原停调账号可同步同版本凭据，但不擅自开启；现有操作区提供明确「重新登录并启用调度」或「启用调度」，单账号明确授权并留存，不能取消 Sub2 管理员的禁用状态。批量重登不暗中授予启用许可。已停调、仅同步成功不是「可用」。
4. **顺序**：写前新鲜读、核对资格和原绑定，持久记录阶段；按需临时停调、写本次凭据、回读版本回执和 OAuth 元数据，缓存就绪后用原账号、原出口做不改变调度限制的 Sub2 定向探测。只清可归因于旧凭据的错误，再按最新有效启用意图开启。新鲜回读同一 ID/绑定/版本、`active`、无错误、有完整 AT/RT、`schedulable=true` 及 Sub2 实际调度条件（含限流、过载、临停、到期策略）。暂不可用标为冷却／暂停或等待复查，不报完成，不强清无关限制。复用约 30 秒、30 分钟的持久复检；复检遇到可信新的 401 才按既有门禁决定再次登录，明确官方终态直接进入 R2。
5. **中断和并发**：同账号 AUTH 单 worker 加 lease 串行，事务 CAS 拦住旧版本；Sub2 侧的 OAuth 补丁、启停（包括补偿）和删号用同一所有权/版本条件执行，不能用 GET 后写假装原子。建议给已有管理动作增加 `expected_revision` 或等价条件，应用操作 ID、本次凭据版本和回执与凭据同事务提交，并可无密回读 `account_revision`、`credential_revision` 和 `operation_id`。人工重复设置同一个调度值也推进意图版本；后台 RT 刷新同样不能在新凭据写入后提交旧结果。远端或本地版本冲突则停止旧意图，绝不重选绑定。超时、重启、回读失败先查原操作回执；已应用就只续验，已明确未应用且条件仍成立才可重试，未知不重发 OAuth 或创建；无权补偿时不盲目停调。现有接口若不具备此保障，只允许在隔离环境或能实证无其他写者的受控窗口验证；**不能按该模式上线无人值守启用和无法保护所有权的删除**。官方终态仍在本地立即生效；依赖不满足是平台发布阻塞，不是账号「待核」。

**Sub2 源码证据与最小改动**：集成评审核对本地 Sub2 `ee05e2a1736d`，不代表已验证运行版本。`backend/internal/handler/admin/account_handler.go:1808` 先提交凭据，再尽力写 `extra`、清错和失效缓存；旧 `kkai_auth_recovery` 标记不是原子版本回执。该文件 `:1481` 的 `/test` 成功后有恢复状态的副作用，`backend/internal/service/admin_account.go:1532` 的广义清错会清限流和临停，不能用于本规则。优先在现有单账号接口复用既有 DB 更新、缓存通知和探测逻辑，补条件写、回执及不清无关限制的模式；不引入新队列或通用重构。验收分开验证「凭据已生效」「定向探测通过」「实际可调度」「网关请求命中原 ID」；前两者或开关回读不等于网关命中。网关验收只在隔离账号按正确分组/模型执行，平时不对每个生产账号额外发付费流量。

## 4. 入口、页面与验收矩阵

后台返回规范原因；账号行、详情和「需要处理」共用状态映射，先展示官方终态，再展示 Free 退池，再展示当前凭据版本的交付／恢复。区分「登录成功 · 待同步」「已写回 · 待验证」「已写回 · 调度暂停」「冷却至…」「已同步 · 可调度」。官方账号显示「官方停用/删除 · 自动清理中」或「已清理」，无审核、重登、修资料、重导、恢复按钮。页面收到终态时移除本地缓存的 RT、已揭示账密/TOTP、复制和导出入口；详情已打开也立即清空，迟到的登录/reveal 回包不能恢复。Free 保留套餐筛选、历史和本地登录入口，不显示导入/恢复。历史刷新失败或超时只提示刷新核对，不重发；批量进度分开统计本地成功、退池、终态、同步中及可调度。

诊断用现有详情/巡检与错误事件，支持按 AUTH ID 和 Sub2 ID（含已退役 ID）定位。展示当前安全版本、绑定、最近检测、最后成功/失败步骤、跳过/未入队原因、是否已写回/有效调度、下次检查时间。至少能区分：没绑定、旧版本、历史成功未入队、明确密码拒绝、需要邮箱验证、平台故障、人工暂停、冷却和确认终态。「证据暂不可读」不同于「没有证据」，不得将同邮箱旧账号的审计挂到新 ID。保留已有名称、搜索、费用和套餐筛选；日志、普通管理 DTO、前端缓存及验收材料均不输出真实密码、TOTP、代理密码、AT/RT 或原始响应正文。

### 4.1 入口覆盖清单

| 入口 | 允许的结果 | 统一门禁 |
| --- | --- | --- |
| `/api/login`、`/api/login/stream` | 首页新登录和批量登录 | Free 不进入付费池；官方终态立刻清理并终止；明确关闭自动交付时只保存本地；显式首次交付保留创建许可。 |
| `/api/history/login` | 账号表单个、批量、重登 | 已绑定付费默认只更新原 ID；Free 退池；官方终态拒绝；`auto_deliver` 不得把 update-only 变成 create。 |
| `/api/history/{id}/credentials` | 修改密码、TOTP、代理后的资料修复 | 资料保存不能解除 `free_local` 或 `official_unavailable`；在途修复回包先过 policy、版本和 binding CAS。 |
| `/api/account-checks` 及批次取消 | 直接检测、批量检测、即时 SSE | 只用当前证据；混合批次逐账号结算；官方终态触发清理，普通失败不触发。 |
| `/api/account-recovery`、`/{id}/recheck` | 手动/自动恢复和延迟复检 | 查询 `sub2_policy`、绑定 operation、凭据版本和人工启停意图；旧任务只能续跑原检查点。 |
| `/api/account-deliveries/{id}/retry` | 交付 retry | 只允许原 operation、原 binding、同版本；Free、官方终态、`remote_missing` 和 update-only 不获得建号许可。 |
| `/api/sub2/import`、`/{id}/reconcile` | 首次导入、明确重导、未知结果核对 | 只有显式首次交付或非终态 `remote_missing` 的显式重导能 create；未知结果只 reconcile，禁止重 POST。 |
| 服务启动恢复、`monitor`、`runOnce`、`recheck` | 重启续跑和定时任务 | 所有 worker 入口再次做同一 policy/CAS 校验；不依赖页面刷新或内存布尔值，不让旧任务饿死其他账号。 |

本表之外的本地「删除记录」只删除 AUTH 记录，不能当官方终态，也不能代替 Sub2 清理。独立 CLI 不写 Web 生命周期状态，保持本期范围外。

| 验收 | 入口与风险 | 后端及页面必须同时满足 |
| --- | --- | --- |
| A01 · R1/R3 | 账号表单个和批量重登、直接调用 `/api/history/login`，`auto_deliver=false` 或未传 | 已绑定付费一次 OAuth，新版本与只更新任务一起落库；原 ID 收到同次新 AT/RT，无创建。明确 Free 保留本地并退池；空套餐不误判。 |
| A02 · R1/R2/R3 | 首页 JSON/SSE 登录与整批重试 `/api/login`、`/api/login/stream` | 终态按邮箱复提也拒绝；Free 不发给付费池；真正合格付费的首次显式交付保留。关闭交付仍只保存，不误称同步。 |
| A03 · R1/R2/R3 | 定时巡检、恢复/check/retry、资料 `PATCH` 后续跑、自动开关 | Plus→Free、Team→个人 Free、别名及身份冲突；Plus→空套餐刷新→Free 仍引用原 Plus 观察，Plus→Pro→Free 不借旧 Plus 证据删号。终态不接受资料修改/reveal；Free 不后台复活；修复回包迟到于 `remote_missing` 时不派发交付。 |
| A04 · R2 | 直接/批量检测、OAuth 拒绝、即时和延迟 SSE 探测、手动复检 | 官方明确「已删除或停用」与单独的删除/停用均自动清理。混合批次中普通账号继续，只有通用 `account_unavailable` 不清理。普通 401、Sub2 `error`、管理 401/403、TOTP/通用验证码拒绝、手机验证、网关 404 均无删除；明确密码/账密拒绝或邮箱验证按本地废弃退役；取消或过期结果只留安全历史。 |
| A05 · R1/R2/R3 | 手动导入／显式重导、交付 retry、历史列表自动关联、旧已创建待确认 | 门禁覆盖 UI 外直接打 API；Free/终态无新建、无写回，遗留任务只可读原操作结果。非终态 `remote_missing` 仍只允许一次明确重导；旧 operation、重绑、跨目的地、同邮箱多匹配均不误删、认领或建号。 |
| A06 · R1/R2 | 已绑定、从未绑定、原对象已缺失、停调/删除中断 | 停调－确认－删除，可信 404 幂等；覆盖合法 `linked-*` 续跑、提交后未发 HTTP 就重启、DELETE 回包丢失。远端 429/权限失败仍可重启续跑且不阻塞其他账号；R2 提交即无秘密，R1 保留 AUTH 凭据，原审计和费用关联保留。 |
| A07 · R3 | 写回/探测/启用故障、超时未知、重复操作、重启 | 接收端断言确实存储模拟的新 RT；凭据写成功但缓存/回执失败、旧标记残留都不误报完成或重放。核验写回→有效调度→隔离网关命中。限流/临停/过期不被误清，复检服从最新版本。 |
| A08 · R1/R2/R3 | 并发重登、晚到刷新、同 ID 换 `operation_id`、人工停调与补偿 | 同 ID 同邮箱但 operation 换代，旧任务零停调、零删除；旧 RT 不覆盖新凭据。AUTH 临停期间人工再次设置 `false` 仍产生新版本；旧启用/补偿被拒。没有条件写的外部并发限制按 3.3 上线门槛实测。 |
| A09 · R1/R2/R3 | 历史列表/详情、标签、筛选/分页/跨页选中、批量统计与定位 | Free、终态、AUTH 成功和实际调度准确联动；按旧或当前 Sub2 ID 查原因。读失败不等于无证据，同邮箱不同 ID 不串审计；已打开的详情收到终态后清掉缓存和已揭示值，忽略迟到的登录/reveal 回包。 |
| A10 · R1/R2/R3 | 老库、存量预演、重复迁移、回滚/停写 | 旧数据不会平白授权启用/删除；只有可回链的准确观察版本能参与预演。终态提交后秘密不可取，预演不发变更请求；回滚不再登录，Free 旧队列不复活，已旋转 RT 或已删除对象从不自动恢复为旧快照。 |

## 5. 开发顺序、存量处理和发布门槛

1. **后端门禁优先**：落共享分类、终态/退池事务及固定绑定、旧任务取消，覆盖启动恢复和每条直调 API；补齐核心负例后才接清理 worker。现成的 `retireSub2WorkTx`、导入 CAS 和恢复 lease 要复用；只做需要的迁移。
2. **串通主路径**：先在隔离目标核实 Sub2 的条件写、探测副作用和实际调度条件，补齐第 3.3 节缺口后才开启无人值守远端动作。再接原绑定更新、诊断和客户端 fixture，断言账号表只发一次登录请求且不在浏览器补写。不把老账号「有报错」做成一键复活。
3. **只读盘点后分批**：按目的地、AUTH/Sub2 ID、绑定 operation、可信证据类型及凭据版本生成脱敏预演。分别统计「官方终态可清」「付费降 Free 可退池」「确有同版本缺交付可补」「凭据/身份/授权不足不动作」和既有停调/检测原因。对 188 条历史终态逐条回链官方来源、失败 attempt 与后续成功版本，只有证据未被更新成功取代才进入执行名单；对 6 条 Free 没有可靠前态证据不动远端；对有证据的漏交付，只补固定原 ID、仍新鲜且合格的一次更新，不偷跑新 OAuth；旧报告中的密码错误及邮箱验证须按 R2 补充规则重新核对。批次执行前复查同一证据和远端对象，少量批准后观察退避、错误聚合、在途数、调度、实际探测；新发现的证明不足数据只做诊断，不转为账号待核或批量删除。
4. **发布与回滚**：变更涉及凭据、删除和并发，实施与上线按 L3，采用 [AUTH 的 sys1 发布门禁](../SYS1_DEPLOYMENT.md)，不能用旧文档授权或 Sub2 Compose 工作流代替。候选只接隔离目的地与授权账号；有权限的隔离账号必须验证 Sub2 **正常刷新流程**实际使用新 RT 并继续服务，记录脱敏刷新成功与后续探测。当前 AUTH 重登本身不调用刷新接口，Sub2 的独立 `/openai/refresh-token` 也不能代替按目标账号正常刷新；验证必须走目标账号、匹配客户端和出口的正常刷新路径。若刷新轮换 RT，Sub2 必须保存返回的新 RT；无新 RT 时保留本次提交的 RT；响应丢失转回读状态，不重发兑换。读回 `has_refresh_token`、版本标记、尚有效 AT 的探测成功，都不能单独证明 RT 可续期；不另外提取真实 RT 或在旁路兑换来做断言。回滚先冻结所有旧版本写入与自动任务，保留证据/队列与权限收敛；旧程序不能安全识别新门禁时不得恢复其 worker。备份做权限/一致性核验但不回填旧 RT、不复建远端账号、不恢复官方终态密文；远端删除与令牌轮转不可用回滚二进制撤销。

### 实现后的最小验证

先跑覆盖 A01–A10 的新增定向用例，再跑下列已存在的邻接测试；新增用例名称在实施记录补齐。定向并发用 `-race` 复验关键新用例。记录实际命令、退出码及行为证据；没有运行的不记成通过。全量或预计超过五分钟/耗时未知的命令需事前确认。

```sh
go test ./cmd/server -run 'Test(Sub2RecoveryChangesWorkspaceAndPlan|Sub2RecoveryRechecks.*AfterLogin|DeleteDeletedAccount|PendingCleanup|AccountDeliveryExistingBinding|Sub2Recheck|RunWithHistoryAccountRejectsDeletedIdentity|AccountCheckCanceledLateDeleted|CredentialRepairDeterministic|HistoryLifecycleEvents)' -count=1 -timeout=120s
go test ./internal/store -run 'Test(Sub2Missing|AccountDelivery|CredentialRepair|OfficialLifecycleEvent|AccountCheckOfficialLifecycleEvent)' -count=1 -timeout=90s
go test ./internal/login -run 'Test(ParseOAuthIdentity|OAuthCallbackAccountRejection)' -count=1 -timeout=60s
node --check cmd/server/client.js
node --check cmd/server/account-checks.js
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
```

**拒收线**：任何误删、错绑/建号、未批准启用、旧任务写回或重放、官方终态仍可人工复活/取密、丢失重试意图，以及仅凭 AT 探测冒充 RT 可用。生产健康、备份和实际刷新尚未验证；现场验收结果单独记录。

## 2026-11-20 本地实施记录

账号表的 `/api/history/login` 复用服务端交付队列。登录事务记录当前目的地、AUTH ID、绑定 ID、Sub2 ID、operation 和凭据版本。重登成功后，有原绑定且本次明确为付费的账号进入只更新任务。无绑定且关闭自动交付时只保存 AUTH。临时缺少套餐的 token refresh 可以沿用已有套餐，但完整重登未确认付费或套餐 claim 冲突时，本次凭据仅留在本地。相同 OAuth issuer 下，新旧 subject 冲突会终止更新。

Plus、Team 或 Business 最近一次可信套餐变为 Free 时，AUTH 保存新凭据和历史。后台核实旧工作区和绑定版本，先停调，再删除原对象。官方明确删除和停用共用终态事务，保留账号行及证据，清除本地敏感凭据并取消自动任务。Sub2 暂时不可用时，清理意图保存在本地，后台续跑。

已绑定重登通过 Sub2 管理接口 `lifecycle-oauth` 依次执行暂停、凭据写回、定向探测、按意图启用，并读取回执和实际账号状态。Sub2 迁移 `256_account_lifecycle_oauth_receipts.sql` 保存无密回执。回包丢失可按操作 ID 查询。写入和启用要与原始归属及预期版本匹配，人工暂停保留。Sub2 后台正常刷新也校验凭据版本，防止旧刷新覆盖刚同步的 RT。上线依赖两个仓库的对应版本及该迁移。

以下命令在 AUTH 根目录和 Sub2 的 `backend` 模块执行，均使用临时数据。真实上游 RT 续期、生产网关命中及存量账号盘点属于上线验收，本地开发没有操作生产账号。

### AUTH

```sh
go test ./internal/store -run 'Test(Store|Account|Sub2|Official|Lifecycle|Delivery|CredentialRepair|DeletedAccount)' -count=1 -timeout=120s
go test ./internal/login -run 'Test(ParseOAuthIdentity|OAuthCallbackAccountRejection)' -count=1 -timeout=60s
go test ./cmd/server -run 'Test(AccountDelivery|HistoryRelogin|RunWithHistory|Sub2Recovery|Sub2Recheck|CredentialRepair|DeleteDeletedAccount|PendingCleanup|OfficialSub2CleanupFence|FreeCleanup|AccountCheck|Sub2Missing|DeliveryOptions)' -count=1 -timeout=120s
go test -race ./cmd/server -run '^Test(HistoryRelogin.*|AccountDeliveryBoundHistoryReloginReachesSub2|AccountCheck(Deleted|Deactivated)RemovesSub2Account)$' -count=1 -timeout=90s
node --check cmd/server/client.js
node --check cmd/server/account-checks.js
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
git diff --check
```

### Sub2

```sh
go test ./internal/handler/admin -run TestLifecycleRetire -count=1
go test -tags unit ./internal/service -run '^TestLifecycleOAuth' -count=1 -timeout=45s
go test -tags unit ./internal/service -run '^Test(RefreshIfNeeded_OpenAISuccessCAS|TokenRefreshService_RefreshWithRetry_(UsesCredentialsUpdater|OtherPlatformOAuth))' -count=1 -timeout=40s
bash scripts/test-lifecycle-oauth-postgres.sh
git diff --check
```

PostgreSQL 18 临时容器覆盖同值暂停推进版本、新 RT 与回执同时提交或回滚、保留原账号配置、并发暂停使旧写入失效。前端与 AUTH 采用模拟登录和 Sub2 响应覆盖 Free、官方终态、旧任务晚到、重登写回及调度状态。全仓套件未运行。


## 2026-11-20 废弃号补充

登录的结构化结果明确拒绝密码、拒绝用户名与密码组合，或要求邮箱验证时，账号进入 `discarded`。审计事件来源为 `login_discarded`，保留原始错误码。手机号验证、TOTP、通用验证码错误、普通 401、超时均不触发废弃。账号表保留原因，并阻止后续登录、取密和交付。当前登录结果和原绑定匹配时，后台按同一版本停调并移除 Sub2 账号。

启动时回看旧库中的最后一次已完成失败登录，匹配账号状态、错误码及写入时间，并排除较新成功、资料修改和在途尝试。对旧版本没有冻结快照的绑定，要求已确认的导入记录早于失败登录，有同一 AUTH ID、目的地、远端 ID 和成功凭据版本的既有恢复记录佐证。更换绑定、时间相同无法判定先后、或缺少归属证据时，仅本地退役。后台删除前仍须验证当前远端身份和版本。

本轮只读核对 sys1 上的 AUTH 数据库时，最近一次登录有 9 条 `invalid_username_or_password` 和 3 条 `email_verification_required`，12 条均有原绑定与历史恢复记录相互印证。查询只读取计数，未对远端对象执行停调或删除。生产发布、远端身份复核及删除回执均未执行。

本次定向验证使用临时数据库、模拟 Sub2 接口和前端 fixture，下列命令通过。全仓套件未运行。

```sh
GOPROXY=off go test ./internal/login -run '^Test(DiscardEvidencePreservesStage|VerificationErrorClassification)$' -count=1 -timeout=30s
GOPROXY=off go test ./internal/store -run '^Test(Discarded|LifecycleTerminal|StoreTracksAttempts|LinkSub2AccountIsTerminal)' -count=1 -timeout=45s
GOPROXY=off go test ./cmd/server -run '^Test(OfficialSub2CleanupFence|DeleteDeletedAccount|PendingCleanup|CredentialRepairDeterministic|RunWithHistory)' -count=1 -timeout=45s
node --test cmd/server/client.test.cjs cmd/server/account-checks.test.cjs
git diff --check
```
