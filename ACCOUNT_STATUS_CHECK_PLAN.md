# AUTH 账号状态检测实施方案

日期：2026-10-04（Asia/Shanghai）  
状态：设计已实施，并于 2026-10-04 上线。实际改动、验证、协议兼容修正和验收结果见 [实施记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。下文保留设计时的范围与候选验证计划，不能替代实施记录中的实际证据。

## 1. 要交付的功能

在 AUTH 平台中，对 AUTH 本地记录为“已导入 Sub2”的账号进行单个或批量检测。由 **sys1 上的 AUTH 后端**读取自身保存的 Access Token，通过服务器默认代理或用户明确选择的 sys1 IPv4 直连，调用一次 `gpt-5.6-luna`，在 AUTH 页面展示检测结果、时间、耗时和批次进度。

用户操作路径：打开“账号检测” → 搜索/筛选账号 → 勾选或全选当前页 → 点击“开始检测” → 查看进度与分类结果 → 对需要处理的账号手动重新登录，随后重新检测。

验收目标是可靠地完成检测并准确报告结果。上游拒绝、令牌过期、模型无权限、代理故障也是应正确完成的检测结果，不能通过重试到偶然成功、忽略错误或更换模型来制造“全部正常”。

### 检测结论的含义

- “模型调用正常”：在显示的检测时间，AUTH 保存的这一版凭据，通过本次网络出口，完成了指定模型请求。
- “收到 401”：本次请求的鉴权被拒绝；不直接等于账号被封、Refresh Token 永久失效。
- “AUTH 记录已导入”：本地存在导入成功记录，不代表查询了 Sub2 的当前账号状态。
- AUTH 与 Sub2 可能分别持有不同版本的 AT/RT，因此本功能不能声称已验证 Sub2 当前凭据或调度是否可用。页面详情固定注明“检测使用 AUTH 当前保存的凭据”。

所有检测请求、任务、结果都在 AUTH 完成。不调用 Sub2 的测试、刷新、状态查询或账号管理接口，不修改 Sub2。首次上线也只发布 AUTH。

## 2. 首版定稿范围

| 项目 | 首版决定 |
| --- | --- |
| 对象 | 当前仍存在，且本地 `sub2_imports.state='imported'`、`sub2_account_id>0` 的账号 |
| 入口 | 历史账号的单个检测按钮 + 独立“账号检测”面板 |
| 模型 | 固定 `gpt-5.6-luna`；先完成单账号协议验证，不静默换模型 |
| 内容 | 固定短提示“Reply with OK only.”，不允许输入任意聊天内容 |
| 出口 | 默认服务器代理；可显式切到“sys1 IPv4 直连”；首版不加自定义代理输入 |
| 真并发 | 检测单独可选 1 或 2，默认 2；全局最多 2 个请求，与登录 1–10 槽独立 |
| 批次 | 每批 1–100 个唯一账号；整个 AUTH 同时一个活动批次 |
| 超时 | 每项执行总时限 20 秒，包含代理连接、TLS、响应与流读取，不含排队时间 |
| 重试 | 不自动重发模型 POST；用户可手动重试未完成或异常项 |
| 去重 | 提交幂等键、单活动批次、批内账号唯一；同账号同凭据完成后 30 秒冷却 |
| 后台执行 | 关闭/刷新页面继续；返回页面能恢复任务 |
| 取消 | 停止派发并取消进行中请求；保留已经完成的结果 |
| 凭据处理 | 只读取 AT；不读取/刷新 RT，不自动登录或修改 token |
| 存储 | 现有 SQLite 新增两张表，沿用现有 AES-GCM；不引入新依赖或队列中间件 |

30 秒冷却用于避免重复点击造成重复调用，不是给每个账号添加延迟。不同账号可立即占用空闲检测槽；账号成功重登产生新凭据版本后不受旧版本冷却约束。切换出口后仍遵守同版本冷却，页面明确显示剩余秒数。

首版暂不增加定时全量巡检、自动刷新 RT、自动复活、自动删除/禁用账号、自动向 Sub2 回写、自动更换模型、跨服务器任务队列。手动检测稳定且有实际使用数据后再评估。

## 3. 已核实的代码事实与实现约束

| 当前代码 | 对实现的要求 |
| --- | --- |
| `internal/store/store.go` 的 `GetOAuthResultByID` 同时解密 AT/RT，并要求两者都有 | 新增只读 AT 的窄接口；RT 缺失但 AT 有效也应允许检测 |
| `internal/login/service.go` 仍有 AT 没有 exp 时回退 ID Token exp 的逻辑 | 检测重新解析实际 AT 的 exp，不用 `accounts.expires_at` 拒绝请求 |
| `FinishAttempt` 在一个事务内写 AT 并将登录 attempt 标记 success | 用最大成功 `login_attempts.id` 标识当前凭据版本；秒级时间戳不能替代 |
| Store 为 SQLite 单连接，启用 WAL/外键 | SQL 事务必须短；事务里只用 `tx`，不得再次调用使用 `s.db` 的 Store 方法 |
| 登录按邮箱使用进程内锁 | 检测不占登录锁；登录期间跳过检测，后续登录更新通过版本比较保护 |
| 登录失败的 `FinishAttempt` 在明确 deleted 时会删除账号 | 检测结果独立存储，绝不能调用 `FinishAttempt` 来记录检测失败 |
| `sub2_imports` 没有 accounts 外键 | 资格查询必须关联当前账号，忽略已删除账号残留的导入记录 |
| `cmd/server/client.js` 历史列表通过 `innerHTML` 整表渲染 | 检测轮询不能反复调用整表刷新；使用按 ID 缓存的行节点局部更新 |
| 历史行已有复活、导入选择框 | 使用独立检测面板，每行只有一个检测选择框，避免改动既有批量操作语义 |
| `internal/login/proxy_check.go` 含本机 Clash 回退 | 不直接复用该高层检查流程；检测必须使用明确冻结的网络配置 |

引用的是本地源码事实。现有部署文档中的浏览器登录结果，不能替代本功能对 OAuth 模型端点的真实验证。

## 4. 页面设计与交互

### 4.1 独立检测面板

在现有页面增加“账号检测”入口，保持现有账号历史操作。检测面板只列出符合本地导入条件的账号，复用已有邮箱搜索、分页和时间格式化函数，不建设另一套前端框架。

控件与信息顺序：

1. 范围说明：“检测 AUTH 记录已导入的账号，使用 AUTH 当前凭据”。
2. 邮箱搜索、检测结果筛选；每页默认 20 条，沿用现有分页尺寸选项。
3. 出口：“服务器默认代理”（默认）/“sys1 IPv4 直连”；检测并发 1/2；模型只读展示。
4. “全选当前页可检测账号”“清空选择”“开始检测 N 个”。
5. 固定批次进度区，显示已处理/总数、运行中、排队、正常、异常、跳过、取消、中断数及停止按钮。
6. 账号行：选择框、邮箱、最近登录状态、最近检测结果、时间/耗时、检测/详情按钮。

单个账号按钮创建一个只有 1 项的批次，复用全部后台逻辑。存在活动批次时，入口显示“查看当前检测”；其他账号仍可搜索、翻页、查看详情或重新登录。

### 4.2 选择与分页规则

- 初次打开选中 0 个；全选仅作用当前页符合条件且未处于登录/检测中的账号。
- 缺少 AT 或 AT 过期的已导入账号仍可选择，由后台产生明确的本地预检结果，不能悄悄丢弃。
- 翻页保留选择，显示“已选 25 个，本页 5 个，其他页 20 个”；全选支持正确的半选状态。
- 修改搜索词或筛选条件时清空选择并提示；普通轮询不清空、不扩大选择。
- 最多选择 100 个，前后端同时校验。超限整批拒绝，不偷偷截取前 100 个。已经选 90 个、全选当前页会新增 20 个时，这次全选整体不生效，保持原 90 个并提示上限。
- 账号数据或导入资格读取失败时保留旧列表、标明未更新，禁用全选与提交；不将失败读取解释为零账号。
- 重新加载后移除已经删除/不符合资格的选择并提示数量；最终资格始终由服务端检查。

### 4.3 结果展示

登录状态、导入状态、检测结果是三个不同维度，不修改或复用 `accounts.status`。

摘要示例：`最近登录成功 · AUTH 记录已导入 · 上次检测：模型调用正常 · 10:32:05 · 2.1 秒`。

详情展示：模型、出口标签、时间、耗时、实际目标 HTTP 状态、流内错误码、安全错误摘要、凭据是否已更新、是否实际尝试请求。代理 CONNECT 状态与模型 HTTP 状态分列。

筛选提供：从未检测、正常、收到 HTTP 401、流内鉴权失败、凭据待更新、限流/额度、权限/模型不可用、网络/代理错误、其他异常、结果已过时。筛选可以交叉命中，例如“HTTP 401 + token_expired”同时属于收到 401 和凭据待更新；顶部进度计数只按任务终态及主结果分组，避免重复计数。

旧凭据结果加“凭据已更新，待重新检测”，颜色不能继续暗示当前凭据已验证正常。即使版本没变，也只称“上次检测正常”，始终显示检测时间，不把历史观察当持续健康承诺。

### 4.4 进度与防频闪

- 批次完成比例为终态项数/总项数；`finished/skipped/canceled/interrupted` 均进入“已结束”数，同时分别列数。
- API 中 `settled=finished+skipped+canceled+interrupted`，用于进度条；`finished=ok+abnormal` 仅表示形成检测结论的项。100% 表示全部项已收敛，不表示全部成功。停止摘要固定用“已检测 X、跳过 Y、取消 Z、中断 W”，避免把取消算成检测成功。finished 中同时展示本地预检数与进入请求阶段数；正常/异常计数指本批次观察结果，freshness 作为另一个维度，不重复计数。
- 单请求只显示“排队 / 正在检测 / 结果”，不模拟百分比。
- 页面可见且有活动任务时，每 1 秒查询批次；同一页面不重叠发送轮询请求。隐藏时减慢到 5 秒，恢复可见时立即拉取。
- 轮询仅 patch 变化的文本、标签与进度条宽度，按账号/check ID 缓存节点。保留滚动位置、焦点、详情展开状态、选择集合。
- 检测中的行保留上次完成结果供对照，新任务状态单独展示。
- 筛选或排序受结果变化影响时，先更新行内状态，不让行不断跳走；批次结束或用户点击“更新筛选结果”时再应用成员变化。
- 查询失败保留上次结果并显示“连接中断，正在恢复”，不清空表格，不重新提交批次。
- 页面初始化从服务器查询活动/最近批次；localStorage 只能辅助定位，不能成为唯一任务来源。
- 登录完成、检测批次结束、重新进入面板、页面恢复可见时刷新安全账号/导入/检测摘要，重新计算资格与 freshness；有活动批次时除每秒批次查询外，每 5 秒刷新一次安全摘要，以覆盖其他标签页的登录/删除。数据请求复用、避免重叠，收到摘要仍只 patch 已显示的行，不调用原有整表 renderHistory。
- 手动刷新、本页面删除账号后同样刷新安全摘要。用请求序号丢弃迟到旧响应，不能让登录之后的新版本状态被之前发起的慢查询覆盖；刷新失败时保留旧数据并撤去暗示当前已验证的颜色，显示“未更新”。

## 5. 请求协议与完成判据

### 5.1 请求草案

当前本地 OAuth 适配代码参考指向以下端点；这是需要实测的兼容协议，不能当作公开 API 永久稳定的承诺：

```http
POST https://chatgpt.com/backend-api/codex/responses
Authorization: Bearer <AUTH 保存的 Access Token>
ChatGPT-Account-Id: <该凭据对应的账号或工作区 ID>
Content-Type: application/json
Accept: text/event-stream
OpenAI-Beta: responses=experimental
```

```json
{
  "model": "gpt-5.6-luna",
  "instructions": "Reply with OK only.",
  "input": [
    {"role": "user", "content": [{"type": "input_text", "text": "Reply with OK only."}]}
  ],
  "stream": true,
  "store": false
}
```

- 这是 OAuth 账号调用，不把其 AT 当作 `api.openai.com/v1` 的 API Key。
- `ChatGPT-Account-Id` 读取与 AT 同一数据库快照的已保存字段；缺失时记录 `credential_incomplete`，不猜其他工作区、不尝试切换账号。
- User-Agent、originator 等兼容头在单账号协议验证时确认，使用一致的固定配置；不搬入 Sub2 的整个调度、票据或账号健康逻辑。
- 首先验证基础请求。`max_output_tokens`、reasoning、temperature 等参数不能假定 OAuth 端点支持；只有验证兼容后才增加输出上限。
- 短提示降低通常输出量，不能保证只产生 1 token，也不能承诺无用量消耗。20 秒和响应字节限制是客户端资源约束，不是上游计费用量的硬上限。
- 模型不存在/无权限要显示 `model_unavailable`，不靠换模型掩盖结果。若指定模型在代表性账号上无法调用，先解决模型兼容性，再发布该检测功能。

### 5.2 成功判据

首版仅在目标 HTTP 200、响应符合预期 SSE 协议、收到并验证 `response.completed`、嵌套 `response.status == completed`、无错误、至少有一个非空输出文本时记录 `ok`。输出不要求逐字等于 OK，文本只用于内存判定，不保存内容。

文本可以来自累积 delta 或终止事件的 output。只有 refusal、tool output 或空输出不足以证明本次文字调用正常，记为 `protocol_error/no_text_output`，不能标鉴权失败；completed 事件内部仍带 failed/incomplete 状态或 error 时必须按失败处理。

`response.done` 仅在真实兼容样本证明该协议使用它、且嵌套响应明确 completed 时才允许列入实现白名单；首版默认不把它当成功。

以下均不能单独证明成功：HTTP 200、连接建立、`response.created`、收到 delta、出现 `[DONE]`、流关闭、空响应。`response.failed`、`response.incomplete`、`error` 事件按失败原因分类；尚无有效完成事件就 EOF，记录 `incomplete`。明确终态失败不能被此前已输出文本覆盖。

SSE 解析支持 CRLF、多行 data、任意网络分片、注释心跳及最后一帧没有空行的情况。协议错误/不支持的终态保守报错，不推断成功。

错误解析同时覆盖顶层 `type:error` 的 code/type/message、嵌套 error，以及 `response.failed/incomplete` 中的 response.error。HTTP 200 却返回 HTML 或其他非 SSE 格式，保留目标状态并记录协议异常；不能因返回 200 就通过，也不能因错误文字包含“401”就构造 HTTP 401。

### 5.3 网络边界

- 固定 HTTPS 上游地址、验证 TLS；禁止重定向，避免凭据被转发到其他目的地。3xx 记 `request_rejected`。
- 非 2xx 响应读取最多 64 KiB；单个 SSE 事件最多 256 KiB；总响应最多 1 MiB。超限立即停止并记录 `protocol_error/response_too_large`。
- 不记录原始响应流、输出内容、Authorization、RT、完整代理 URL、密码或 TOTP。
- 安全文案使用稳定映射，不直接保存/回传 `err.Error()` 或上游原始 message；错误码限定白名单，未知码收敛为 `unknown_upstream_error`。
- 不添加可导致自动重放 POST 的上游幂等/重试配置。AUTH 创建批次的幂等键仅在 AUTH 内使用，不转发上游。

## 6. 结果分类规则

任务执行状态与检测结论分离；HTTP 状态、明确业务码、网络发生阶段分别保存。不能复用浏览器登录的宽泛错误推断来识别本次模型结果。

| outcome | 判定依据 | 页面说明/处理建议 |
| --- | --- | --- |
| `ok` | 满足第 5 节完整成功判据 | 本次模型调用正常 |
| `credential_missing` | AUTH 没有 AT，未请求上游 | 缺少访问令牌，请重新登录 |
| `credential_incomplete` | 缺少必要账号/工作区 ID | 本地凭据信息不完整 |
| `access_token_expired` | AT 自身 exp 明确过期，或上游明确 token_expired | 访问令牌已过期，凭据待更新；保留有无真实 401 的区别 |
| `unauthorized` | 目标 HTTP 401 或流内明确鉴权失败，且没有更具体业务原因 | 本次 AT 鉴权失败；不推断账号永久失效 |
| `credential_revoked` | 已验证的明确 token_revoked/invalidated 类业务码 | 当前凭据被撤销/失效，不等于账号删除 |
| `account_disabled` | 已验证的明确账号停用码 | 上游报告账号停用；仅记录 |
| `account_deleted` | 已验证的明确账号删除码 | 上游报告账号删除；本功能不删除 AUTH 记录 |
| `account_unavailable` | 上游仅给出 deleted_or_deactivated 等混合结论 | 账号不可用，不能细分为已删除 |
| `upstream_challenge` | `cf-mitigated: challenge` 或验证过的明确挑战标记 | 上游安全验证，无法判断账号是否失效 |
| `forbidden` | 普通目标 HTTP 403，无更具体原因 | 请求被拒绝，原因待确认；不归因于 IP 或封号 |
| `region_restricted` | 已验证的明确地区限制码 | 上游报告地区限制 |
| `rate_limited` | 429/明确速率限制，排除更具体额度码 | 当前限流，记录 Retry-After |
| `quota_exhausted` | 明确额度不足业务码 | 当前额度不足，不是 401 |
| `model_unavailable` | 明确模型不存在/无权限/不支持 | 此账号不能验证指定模型 |
| `request_rejected` | 参数、接口、重定向等请求不兼容 | 优先检查 AUTH 检测实现；不能归责账号 |
| `upstream_error` | 上游 5xx/明确流内服务故障或过载 | 上游暂时异常 |
| `proxy_error` | 代理认证 407、代理连接/中转失败 | 修复代理配置；不是目标服务 401 |
| `network_error` | DNS、TLS、连接重置等，无法获得可靠目标响应 | 网络错误，凭据状态未确定 |
| `timeout` | 单项 20 秒到期且非用户取消 | 请求超时，不能判为成功或失效 |
| `incomplete` | 流提前结束/明确 incomplete，无完整成功终态 | 本次调用未完成 |
| `protocol_error` | 无法解析、响应超限、无法识别的响应格式 | 检测协议不兼容或响应异常 |

分类优先级：先识别本地/代理/传输阶段，再识别目标响应；明确业务码优先于泛化 HTTP 分类，明确挑战标记优先于普通 403；未知信息保留未知，不做猜测。业务码集合由单元 fixture 和真实安全样本共同确定，文案含糊的自由文本不能触发账号删除/封禁判定。

`Server: cloudflare`、`cf-ray`、普通 HTML 或普通 403 均不是 challenge 的充分证据。模型权限错误可能出现在 400/403/404 或 200 流内，按可靠业务码或已验证的有限语义识别，其余保守报请求拒绝。

`http_status` 只记录实际目标 HTTP 响应，没有则为 null；CONNECT 407 放在 `proxy_http_status`。HTTP 200 的 SSE 鉴权错误保留 200，另存 `stream_error_code`，只有流中真的有数值状态才写 `stream_error_status`。本地过期项 `request_attempted=false`、`http_status=null`，严禁伪造 401。

本地 exp 只是对持有 AT 的元数据读取，不是 JWT 签名验证，也不证明 token 有效。仅读取可表示的正整数 exp；缺失、字符串、超范围、opaque token 或无法可靠读取时按有效期未知继续探测。禁止拿 ID Token exp、浏览器本机时间或要求不相关 claims 的 `ValidateJWT()` 来拒绝检测。真实验收同时核对 sys1 时间；临近边界以远端响应为准，保留 30 秒时钟误差窗口，只有 `exp <= sys1 当前 Unix 秒 - 30` 才本地判过期，避免对任意 exp 做加法导致上溢。

## 7. 数据模型与一致性

### 7.1 新增两张表

以下为实现字段契约；迁移仅新增表和索引，不改写现有账号/token/导入数据。

**`account_check_batches`**

| 字段 | 用途 |
| --- | --- |
| `id TEXT PRIMARY KEY` | 随机批次 ID，复用现有安全随机 ID 生成方式 |
| `request_key TEXT UNIQUE NOT NULL` | 浏览器提交幂等键，限制长度/格式 |
| `request_hash TEXT NOT NULL` | 规范化账号集合、出口模式、并发的请求摘要；不包含凭据 |
| `state TEXT NOT NULL` | `active / stopping / completed / stopped` |
| `stop_reason TEXT` | `user_cancel / shared_proxy_failure / internal_error` 等稳定码 |
| `model TEXT NOT NULL`、`protocol_version INTEGER` | 本批固定模型和检测协议版本 |
| `concurrency INTEGER NOT NULL` | 1 或 2 |
| `proxy_mode TEXT NOT NULL`、`route_label TEXT NOT NULL` | `default/direct` 与安全出口说明 |
| `proxy_cipher BLOB` | AES-GCM 加密的代理快照；直连为 null，批次终态清除 |
| `created_at / finished_at INTEGER` | 毫秒时间，未结束时 finished_at 为 null |

**`account_checks`**

| 字段 | 用途 |
| --- | --- |
| `id INTEGER PRIMARY KEY AUTOINCREMENT` | 单项 ID |
| `batch_id TEXT NOT NULL` | 外键引用批次 |
| `account_id INTEGER NULL` | 外键引用 accounts，`ON DELETE SET NULL` |
| `state TEXT NOT NULL` | `queued / running / finished / skipped / canceled / interrupted` |
| `credential_attempt_id INTEGER NULL` | 执行时成功登录版本；无成功 attempt 时为 0，未读取为 null；历史标量，不对 login_attempts 建外键 |
| `outcome TEXT NULL` | 第 6 节结论；没有形成检测结论时为 null |
| `skip_reason TEXT NULL` | `account_removed / no_longer_imported / login_in_progress / cooldown` 等 |
| `request_attempted INTEGER NULL` | false=确定未进入请求阶段；true=已记录进入请求阶段；null=中断恢复后无法确定；不宣称上游已接收/未计费 |
| `http_status / proxy_http_status / stream_error_status INTEGER NULL` | 严格分开真实响应来源 |
| `error_code / stream_error_code / error_message TEXT` | 限长、脱敏、白名单映射 |
| `failure_stage TEXT NULL` | `precheck / proxy_connect / target_http / sse / network` |
| `retry_after_seconds INTEGER NULL` | 解析并限制范围的上游建议 |
| `previous_check_id INTEGER NULL` | 冷却跳过时指向原结果，方便查看 |
| `created_at / started_at / finished_at INTEGER`、`duration_ms INTEGER` | 毫秒时间和本次实际耗时，不含排队 |

约束与索引：

- 批次活动唯一索引：`UNIQUE` 常量表达式，条件为 `state IN ('active','stopping')`，在 SQLite 层保证只有一个活动批次。
- 批内 `UNIQUE(batch_id, account_id)`，创建时去重；queued/running 只能来自当前活动批次，无需再建设跨批调度。
- `account_checks(batch_id,state,id)` 用于领取与聚合；`account_checks(account_id,id DESC)` 用于最新结果。
- 增加 `login_attempts(account_id,status,id DESC)` 索引，支持凭据版本查询，避免按每行账号扫全表。
- CHECK 约束限定状态、并发范围；代码层也验证转换。
- 每批最多 100 项，计数直接聚合，不再增加计数器表或结果汇总表。账号摘要用批量 SQL 获取，不为每个账号单独查询一次。
- 不在检测表复制 AT、RT、邮箱、密码、TOTP或原始上游响应；账号删除后批次保留匿名项，数量不变。

首版手动任务仅保存上述轻量元数据，不复制响应体。列表/详情查询均限量，最近批次最多 20 个、单账号最近记录最多 20 条；不为首版增加自动清理任务。需要长期定时巡检时再定保留期。

### 7.2 凭据版本与读取

新增 `GetProbeCredential` 类 Store 方法，一次 SQL 快照读取 AT 密文、ChatGPT account ID、当前登录状态及 `COALESCE(MAX(successful login_attempts.id),0)`；只解密 AT。资格关联也在同一短事务/快照内完成。

AT 存在但成功版本为 0 时，当前代码不变量已经不成立：记录 `credential_incomplete/credential_version_missing`，提示重新登录，不把 0 当成可复用的已知凭据版本。AT 本身缺失时优先记录 credential_missing。版本只用于同一账号内部比较，不向浏览器发送 AT 摘要或可还原材料。

真正开始执行才读取凭据，排队时不保存 token。当前只有成功 `FinishAttempt` 写 OAuth 凭据，最大成功 attempt ID 因账号级登录互斥可作为单调版本。如果未来新增其他写 token 的入口或清理成功 attempt 的功能，必须同步维护版本策略，不能继续假定该不变量成立。

落库时在短事务中再次比较当前版本；展示时再次比较，返回派生字段 `freshness: current/stale/account_removed`。stale 是独立维度，不覆盖原始 outcome/HTTP 证据。

- 检测进行中成功重登：旧结果保存为旧版本观察，不成为当前凭据的成功/401 结论。
- 检测完成后成功重登：下一次列表/详情立即显示旧结果过时，无需删除历史。
- 登录失败且 AT 没变：旧结果仍是该 AT 的历史观察，保留时间；不把登录失败自动转换成检测失败。
- 账号删除/同邮箱重建：旧 item 的 account_id 被置空，新账号是新 ID，不能继承旧检测结果。
- 最新一次 canceled/skipped/interrupted 不抹掉上一次完成的检测结论；UI 将最新任务状态和上次完成结果分开返回和展示。

## 8. 后台执行、取消与恢复

### 8.1 创建与领取

1. 验证 Origin、Content-Type、请求大小、账号 ID、并发、出口枚举和幂等键。
2. 创建事务中先查 request_key。相同规范化参数返回原批次；不同参数返回 409 `idempotency_conflict`。
3. 不同 key 遇到活动批次返回 409 `batch_active` 和 active_batch_id；不合并账号或偷换代理。
4. 关联本地导入记录检查资格。不存在/从未确认导入的 ID 整批 422 拒绝，返回 ID 与原因，不创建半个批次；重复 ID 去重，规范化后最多 100 个，原始数组也限制最多 100 个。
5. 冻结代理配置并加密，创建批次和 queued items，提交事务后返回 202，唤醒 worker。网络请求不能在 HTTP handler 或 SQL 事务内执行。
6. 服务启动两个固定 worker；领取事务检查批次 active、当前 running 数少于本批 concurrency，然后以条件更新领取一项，并读取凭据密文、资格、账号状态和成功版本的一致快照；提交后解密、执行。选择并发 1 时也只能有一个请求，不能因为有两个 worker 就失效。
7. 检查账号存在、资格、是否正在登录、版本冷却和 AT 本地信息。排队期间已删除/失去资格/开始登录则 skipped，缺失或过期凭据则 finished + 本地 outcome，均不发模型请求。
8. 先持久化进入请求阶段标志，再使用独立后台 context 发起单次模型请求；标志写入失败就不发送。请求结束后用独立、短时限的持久化 context 写结果，不能使用已经取消/超时的 HTTP context 落库。true 只证明进入发送阶段，不证明网络交付成功。
9. 条件更新 `running → terminal`，聚合本批所有项；全部终态才结束批次、清除代理密文、释放活动约束并关闭 transport 的空闲连接。

worker 完成一项立即领取下一项，不加人为 sleep；唤醒信号与短周期恢复扫描只负责没有任务时等待。SQLite 只串行短暂元数据读写，两个 HTTP 请求必须能同时在途。

领取所需的 Store 读取要提供 tx 内部实现，不能拿到事务后调用使用 db 的公开读取方法。列表 SQL 也不能边遍历未关闭的 rows 边查询其他表；使用 JOIN/子查询，或先完整读取并关闭 rows，再发下一次查询。

若读取/写入数据库失败，不伪造账号结果，不把批次标完成。检测服务进入进程内暂停状态，停止领取、拒绝新建，取消并等待所有在途请求退出；记录安全内部故障。首版通过受控重启在数据库恢复后收敛遗留任务，不建设复杂在线恢复器。无法持久化时保持服务不可用，不释放单活动批次。禁止在旧 worker 仍存活时调用启动 Recover，更不能在周期扫描里把在途请求变成 interrupted 后派发新批。API 读取失败返回明确错误，页面保留上次快照。

### 8.2 与登录的关系

检测不拿登录的 active 锁，也不占浏览器登录槽。领取时发现正在登录则跳过；这一检查不能保证随后绝无登录并发，所以版本校验是最终保护，而不是宣称两个功能绝对互斥。

检测开始之后用户可以重新登录；只要新登录写入新凭据，旧检测结果就标过时。无需为一次小请求阻塞恢复账号的操作，也不能靠前端禁用代替后端一致性保护。

### 8.3 取消

- 取消事务仅把 active 批次置 stopping、stop_reason=user_cancel；queued 项直接 canceled。停止原因首次确定后保持不变；对已 stopping/stopped 的批次直接返回原状态，不能覆盖 shared_proxy_failure/internal_error。
- running 项保持 running，通知对应 cancel 函数，等请求退出再落 canceled；这段时间仍占活动批次与检测槽。
- worker 注册 cancel 函数后再检查批次状态，封闭“取消先于注册”的竞争窗口。
- 结果提交事务再次检查停止状态；停止已经提交时，迟到的成功不得覆盖 canceled。若结果已先提交，则保留已完成结果。
- 无未结束项后为 stopped。重复取消返回相同最终状态；取消 completed 批次不修改其已有结果。
- UI 显示“已停止：已检测 X、跳过 Y、取消 Z、中断 W”，不显示“全部成功”。取消请求已经发往上游，不能保证没有用量。

共享代理明确返回 407 时：若批次尚未停止，在同一个终态事务内保存触发项的 proxy_error、将批次置 stopping/shared_proxy_failure、取消 queued 项，提交后通知其他 running 项取消；触发项不能被后续停止逻辑改成 canceled。不能拆成两次事务，让另一 worker 在中间领取新项。若用户取消已先提交，遵守原停止结果及原因。普通单账号 401/429 不停止其他账号；不把账户级限流当全局故障。

### 8.4 重启恢复

启动 worker 之前完成恢复事务：原 running → interrupted；已经停止批次的 queued → canceled；active 批次的 queued 保留并续跑。已完成项绝不再次调用。

恢复项依据持久化标志保留 request_attempted；无法确定进入发送阶段与否时置 null，页面写“是否发出未知”，绝不能默认 false 并声称没有用量。queued/确定未进入请求阶段的本地预检才可明确标 false。

stopping 批次未完成项全部收敛到 canceled/interrupted，不允许重启后复活。active 批次如果只剩终态项，直接完成；最终摘要明确包含中断项。

“重试未完成”创建新 key、新批次，只选择 interrupted/canceled 及用户明确需要的其他项；重新使用当前默认代理配置，不从已清除的历史密文恢复。按钮不重复提交已有活动批次。

## 9. 代理配置与出口证明

`proxy_mode=default` 在创建批次时读取 AUTH 进程已有服务器代理配置，沿用 `-proxy` / `OPENAI_LOGIN_PROXY` 的生效值；不读取 Mac 的 Clash，不使用浏览器登录输入框遗留值，不使用账号历史 proxy_cipher。

- 默认代理未配置时返回 422 `proxy_not_configured`，不能把 default 解释为直连。
- 首版支持当前既有 HTTP 出口代理形式，包括认证。选择 default 且服务器配置了非空前置代理 `-upstream-proxy` 时，首版明确返回 422 `unsupported_proxy_chain`，不忽略它、不隐式走另一条路径；此拒绝不适用于显式 direct。链式代理支持仅在确有生产需求时复用并验证既有 relay。
- 批次保存加密代理快照，重启续跑仍用该配置；新批次采用新的服务配置。对页面仅返回“服务器默认代理”之类安全标签。
- `proxy_mode=direct` 明确使用 `Proxy:nil`、IPv4 拨号 `tcp4`，本批出口/前置代理均置空，不继承 `HTTP_PROXY/HTTPS_PROXY/ALL_PROXY` 或服务器代理链配置，含义固定为 sys1 IPv4 直连。
- 默认代理失败就返回代理错误，绝不 fallback 直连或其他代理。
- 若服务运行在 Mac，本地模式仅用于 fixture/UI；发布验收与用户所谓直连必须在 sys1 实施。测试报告明确执行主机与出口模式。

无需每个账号先请求额外 IP 回显网站。sys1 验收用同一网络配置进行一次独立出口核验，结合代理 fixture 验证实际 CONNECT/拨号路径，保留脱敏证据；不能只凭页面选项文字宣称出口正确。

## 10. HTTP API 契约

新增端点沿用 AUTH 现有访问保护及 Origin 检查；GET 只读，POST 创建/取消。JSON 使用严格 Content-Type、32 KiB 大小限制、单个对象及未知字段校验；响应禁用缓存，不暴露密文或凭据。

### 创建任务

```http
POST /api/account-checks
Content-Type: application/json
```

```json
{
  "request_key": "浏览器为这次点击生成的随机 UUID",
  "account_ids": [101, 102],
  "concurrency": 2,
  "proxy_mode": "default"
}
```

模型、上游 URL、headers、提示词均由服务器固定，API 不接受任意目标地址或请求体。幂等摘要包含排序去重后的账号 ID、concurrency、proxy_mode；不因幂等重发时默认代理已变更而重建任务，相同 key 返回原快照批次。

首次成功返回 202，幂等重放返回 200：

```json
{
  "success": true,
  "reused": false,
  "batch_id": "随机批次 ID",
  "state": "active",
  "total": 2,
  "model": "gpt-5.6-luna",
  "concurrency": 2,
  "route_label": "服务器默认代理"
}
```

错误包括：400 参数错误，413 body 过大，415 Content-Type 错误，422 账号资格/出口无效，409 幂等冲突/已有活动批次，503 历史存储或检测服务不可用。平台 API 自己的错误不写成账号上游鉴权失败。

### 查询与恢复

| API | 响应职责 |
| --- | --- |
| `GET /api/account-checks?active=1` | 活动批次或 null；安全能力信息：固定模型、最大批量/并发、默认出口是否配置/支持 |
| `GET /api/account-checks?limit=20` | 最近最多 20 个批次摘要，不返回每项完整历史 |
| `GET /api/account-checks/{batch_id}` | 本批最多 100 个安全 items、状态、进度计数、出口/模型和终态原因 |
| `POST /api/account-checks/{batch_id}/cancel` | 幂等停止，返回 stopping/stopped/completed；不存在为 404 |
| `GET /api/history` | 增加可选 `checks` 摘要映射；原有字段和登录/导入语义保持兼容 |
| `GET /api/history/{id}` | 增加最多 20 条安全检测记录 |

历史响应新增 `imports_available` 和 `checks_available` 标志；查询成功但没有记录是 true + 空集合，查询失败是 false + 安全错误码，不能用字段省略表示“从未检测”。单账号 checks 摘要契约如下：

```json
{
  "account_id": 101,
  "eligible": true,
  "ineligible_reason": null,
  "cooldown_remaining_seconds": 0,
  "latest_task": {"batch_id": "当前批次", "check_id": 12, "state": "running"},
  "last_result": {
    "check_id": 9,
    "outcome": "ok",
    "freshness": "stale",
    "model": "gpt-5.6-luna",
    "route_label": "服务器默认代理",
    "finished_at": 1791079200000,
    "duration_ms": 2100
  }
}
```

`latest_task` 指最近一次任务（可以是取消/跳过，携带 skip_reason），`last_result` 只取最近一次 state=finished 的结论，并包含实际 http_status/stream_error_code；分别无记录时为 null。freshness 和冷却在服务端按当前凭据版本计算。模型/出口/检测时间必须随上次结果返回，不能用当前选择的出口冒充历史出口。能力不可用、摘要读取失败或旧版缺少 availability 字段时，面板显示“检测记录暂不可用”，保留旧数据显示但禁用新提交。

安全 item 示例：

```json
{
  "id": 12,
  "account_id": 101,
  "state": "finished",
  "outcome": "access_token_expired",
  "freshness": "current",
  "request_attempted": true,
  "http_status": 401,
  "proxy_http_status": null,
  "stream_error_status": null,
  "error_code": "token_expired",
  "message": "上游报告访问令牌已过期，请更新凭据后重试",
  "duration_ms": 820
}
```

若本地预检过期，相同 outcome 下应返回 `request_attempted=false`、`http_status=null`、`failure_stage=precheck`。计数由相同快照的 items 聚合，满足 queued + running + finished + skipped + canceled + interrupted = total。

读取导入记录失败不再静默退化成空列表；新增可用性标志或让相关接口明确失败，前端据此禁用检测。摘要查询异常同样不得伪装“从未检测”。

## 11. 文件改动与交付阶段

| 文件 | 计划改动 |
| --- | --- |
| `internal/probe/client.go`、`client_test.go`（新增） | 固定目标 HTTP 客户端、显式出口、超时、SSE 与错误分类；支持注入 fixture transport |
| `internal/store/account_checks.go`、`account_checks_test.go`（新增） | AT 窄读取、批次/item 存储、幂等、领取/结束/取消/恢复、版本化摘要 |
| `internal/store/store.go` | 调用增量建表与恢复入口；必要的批量版本查询，不改 token 写入和登录语义 |
| `cmd/server/account_checks.go`、`account_checks_test.go`（新增） | API、安全 DTO、两个 worker、cancel 注册与任务生命周期 |
| `cmd/server/main.go` | 初始化检测服务与路由，启动前先恢复 |
| `cmd/server/history.go` | 附加安全摘要/详情，显式处理相关读取失败 |
| `cmd/server/client.js`、`index.html` | 检测面板、选择/筛选/分页、增量状态与进度、恢复/取消 |
| `README.md`、`NEW_CHAT_CONTEXT.md`、`SYS1_DEPLOYMENT.md`、`DOCS_INDEX.md` | 实施验收后记录使用方式与真实上线事实 |

**阶段 A：协议验证。** 先实现可注入 transport 的小请求客户端和 fixture 判定。在 sys1 用一个有效且本地已导入的账号验证指定模型、请求字段、流终态、工作区头和服务器默认代理；明确直连也从 sys1 发出。协议不通先修这里，不先做全量批量调用。交付：脱敏协议证据和不会误报正常的解析器。

**阶段 B：持久任务。** 完成两表、窄凭据读取、幂等、真并发 1/2、取消、恢复、版本/删除处理。交付：fixture 下批次生命周期及并发/竞态检查通过，检测路径从未读取 RT。

**阶段 C：页面。** 完成单个/批量入口与筛选分页、选择计数、进度、防频闪和刷新恢复。交付：浏览器交互验收通过，无真实模型请求参与 UI fixture。

**阶段 D：sys1 小范围验收与发布。** 按 AUTH 专用部署记录完成候选构建、数据库一致性备份、发布/回滚准备；先单账号，再最多 3 个授权测试账号进行并发 2 的小批次。真实测试按已授权账号范围执行，不对所有已导入账号擅自全量调用。交付：第 13 节门禁证据齐全，文档标记实际实现/上线状态。

阶段 A/B 可先在隔离的 AUTH 测试进程与临时数据库实施；不得通过另一个连接同一生产数据库的进程试跑，因为启动恢复逻辑会干扰生产任务。

## 12. 验收矩阵与定向验证

| 编号 | 场景 | 必须观察到的结果 |
| --- | --- | --- |
| P01 | HTTP 200 + SSE completed + status=completed + 无error + 非空文本 | 正常，保存时间/耗时，不存文本 |
| P02 | 200 + created/delta 后断流 | incomplete，不误报成功 |
| P03 | 200 + response.failed 或 error | 按明确错误分类，外层状态仍为 200 |
| P04 | 200 + `[DONE]`/空流/incomplete；completed 但内部 failed/error | 不判正常，不因事件名误判成功 |
| P05 | CRLF、多行 data、分片、心跳、末帧无空行 | 解析一致，无错过终态/无限等待 |
| P06 | HTTP 401、401 token_expired、200 流内鉴权错误 | 三种证据分别保留，不伪造 HTTP |
| P07 | 普通 403、明确 challenge、明确停用/混合删除停用 | 分开显示，不笼统归责 IP，不删除账号 |
| P08 | 429/额度不足/模型不支持/400/5xx | 各有正确结论，不归为 401 |
| P09 | 超时、TLS/DNS/reset、3xx、超大响应 | 有界退出、安全错误、无重定向/自动重放 |
| C01 | ID Token 过期但实际 AT 有效 | 仍发探测，不因旧 expires_at 误拒绝 |
| C02 | AT 超出30秒宽限明确过期/缺失/缺工作区 | 前置结论明确，实际上游请求数为 0；宽限内允许远端判断 |
| C03 | AT 缺exp/字符串exp/超范围exp/opaque，或 RT 缺失但 AT 有效 | 前者允许远端判断，后者可以检测；从未解密 RT |
| C04 | 检测期间/结束后重登替换凭据，包括同秒完成 | 两种情况均标旧结果过时 |
| C05 | 开始领取时账号正在登录；检测后才开始登录 | 前者跳过，后者由版本保护；不锁住登录 |
| C06 | 排队/运行中删除账号，再创建同邮箱 | 旧项匿名保留，不复建账号，不污染新账号 |
| C07 | AT 存在但没有可证明来源的成功版本 | 本地 credential_version_missing，不以版本 0 假装结果仍对应当前凭据 |
| Q01 | 3 个慢 fixture，并发 2；再测并发 1 | 观测在途峰值分别为 2 和 1，不是串行假并发 |
| Q02 | 登录活动同时执行检测 | 检测不消耗登录槽；短 SQL 不造成死锁 |
| Q03 | 相同 key 重发、不同参数同 key、双页面新 key | 分别复用、409冲突、409活动任务，上游无重复 |
| Q04 | 100/101 个、重复 ID、无资格 ID、存储读取失败 | 上限/去重/资格行为符合契约，不半批悄悄执行 |
| Q05 | 同版本 30 秒内重复、新版本再测 | 前者跳过并指原记录，后者可检测，无逐账号固定延迟 |
| Q06 | queued/running 取消、注册窗口取消、完成竞争、重复取消 | 不遗漏取消，不释放过早，不让晚响应覆盖终态，保留首次停止原因 |
| Q07 | 重启时混合完成/running/queued/stopping | 已完成不重播，running 中断，合法 queued 续跑，停止项不复活；发送状态不确定则为null |
| Q08 | 请求超时后写结果、数据库临时失败 | 独立写入context；故障暂停领取/新建，不释放在途项，落库失败不宣称完成 |
| N01 | 默认代理失效/407、默认未配置；407与另一worker领取竞争 | 清晰报错；407同事务停止余批；零直连回退 |
| N02 | 显式 direct 且环境存在 HTTP(S)_PROXY | 仍走 sys1 IPv4，fixture 证明不访问环境代理 |
| N03 | 批次期间更改配置/服务重启 | 同批使用加密快照，新批使用新配置 |
| N04 | 前置代理配置非空、恶意额外请求字段 | 明确拒绝，不忽略链、不接受任意目标 |
| N05 | 检测新增表/响应/日志包含探测数据 | 无 AT/RT/密码/原始响应/明文代理凭据，终态清除代理密文；既有账号加密凭据保留 |
| U01 | 页1选20、页2选5、取消页2全选 | 总数依次20/25/20；正确半选，无跨页误取消 |
| U02 | 改筛选/搜索、过期账号选择、失败刷新 | 选择/资格提示正确，读取失败禁用提交 |
| U03 | 连续轮询30秒，滚动/输入/详情展开 | 无整表替换，焦点/滚动/选择稳定，进度真实 |
| U04 | 中途刷新/关页/清localStorage后重开 | 查询恢复原批次，不新建/重复请求 |
| U05 | 正常后重新登录、活动任务切筛选 | 旧结论标过时，运行中不频繁跳行 |
| U06 | AUTH记录已导入但Sub2已被其他操作改变 | 页面只陈述本地导入事实；零Sub2请求 |
| U07 | 已正常后本次取消/冷却跳过；checks查询失败/旧版缺字段 | 保留last_result；无法读取显示不可用，不改成从未检测 |
| U08 | 20项检测完成、3跳过、77取消 | 进度100%，明确检测仅20、取消77，不暗示全成功 |
| U09 | 重登后切回面板，旧摘要请求比新摘要更晚到达 | 新freshness保留、旧响应丢弃，不回退绿色 |
| U10 | 已选90、当前页全选会新增20 | 整次不改变选择并提示100上限 |

实施后的候选命令如下，**本次方案阶段均未执行**。新测试以 `TestProbe*`、`TestAccountCheck*` 命名，保持作用域可选择：

```bash
go test ./internal/probe ./internal/store ./cmd/server -run 'Test(Probe|AccountCheck)' -count=1 -timeout=90s
go test -race ./internal/store ./cmd/server -run 'TestAccountCheck.*(Concurrent|Cancel|Recover|Credential|Delete)' -count=1 -timeout=90s
node --check cmd/server/client.js
```

如果实现修改既有 history handler/Store 初始化，补跑其直接回归范围，而不是全仓：

```bash
go test ./internal/store ./cmd/server -run 'Test.*(History|Attempt|Sub2Import|Recovery)' -count=1 -timeout=90s
```

执行前核对测试名称，确保表达式确实选中相关测试。race 检查不能代替功能 fixture；JavaScript 语法检查不能代替分页/取消/恢复的浏览器交互验收。依赖未改则不重装、不升级。

本轮是 L0 文档方案，不运行业务测试、构建或全量测试。实施属于凭据/并发/数据库 L3 变更，但仍优先上述定向检查；任何全量或预计超过 5 分钟/耗时未知的命令，在执行前按用户规则说明精确命令、必要性、预期时间并取得明确授权。

## 13. sys1 验收、发布与回滚门禁

上线前重新读取 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)，使用 server-access 技能及 `ssh sys1`，发现实际 current/回滚版本、服务参数与健康状态。不要从本方案推断实时生产版本，不访问或部署 Sub2。

1. **协议与出口：** sys1 默认代理至少完成一次有效 token 的指定模型调用；随后进行第二次独立小请求确认非偶然解析成功，遵守冷却。sys1 IPv4 直连也验证同一请求，真实记录成功或明确失败。直连若失败，必须有正确分类和证据，不以 Mac 直连结果代替。
2. **真实批次：** 2–3 个已授权账号的小批，按并发 2 执行，记录真实请求起止重叠、完成数、成功/异常分类与总耗时。不用一个账号重复两次制造并发证据；授权账号不足时保留 fixture 并发证明，并明确真实多账号验证缺口。
3. **受控异常：** 401、407、429、坏模型、超时、删除、取消、重启等在 fixture/隔离 AUTH 实例验证；不破坏真实账号凭据或故意耗尽生产额度来制造错误。
4. **数据与回滚：** 备份数据库必须使用一致性备份或停服后复制，不能只复制 WAL 模式下正在写入的主 db 文件；数据库和密钥保持原权限。新增表/索引应可被旧版忽略，先在临时数据库验证旧二进制能打开新 schema，不盲目降级或覆盖生产库。
5. **发布方式：** 按 AUTH 现有流程使用新不可变 release、本机构建/手工上传、保留原 current。候选进程不得与正式进程共享生产 SQLite 做启动恢复；不顺带升级 Node、Chrome、Playwright 或 Sub2。
6. **完成条件：** 服务健康、有效入口可访问、原登录/历史/导入受影响范围正常；检测单个/批量、分类、真并发、出口、取消/恢复、无频闪和脱敏均有证据。记录失败项与验证缺口，不把 health 成功当完整验收。
7. **回滚：** 停止派发、尽量让进行中任务完成/取消；按部署记录切回原 release。保留新增历史表和原数据，不为了回滚还原整个数据库造成新登录数据丢失。再次升级时按恢复规则收敛旧未完成检测任务。

最终实施报告列出实际修改、实际验证命令与结果、sys1 真实模型/出口证据、完整测试套件是否跳过、剩余限制。未完成的验证逐项列出，不宣称账号从此永久稳定。

## 14. 专项审查结论与当前待验证项

方案综合了协议与错误分类、后台队列与存储、前端交互三个角色的只读审查。两项意见取舍已经定稿：

- 前端采用独立检测面板，减少对现有复活/导入选择逻辑的联动改动。
- 不建设“登录与检测绝对互斥”；使用登录前置跳过、凭据快照、落库及展示双重版本校验，允许用户及时重新登录。

仍需实施时确认：`gpt-5.6-luna` 在目标 OAuth 账号上的权限、端点实际要求的兼容头/终态、输出上限参数的支持、sys1 当时的模型端点连通性与出口、已授权多账号样本数量。这些不能由代码阅读或 Sub2 历史测试替代。

参考入口：[文档索引](DOCS_INDEX.md)、[项目交接](NEW_CHAT_CONTEXT.md)、[AUTH→Sub2 集成审查](SUB2API_INTEGRATION_CODE_REVIEW.md)。本地协议参考是 `/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/account_test_service.go`，仅用于理解协议，不复用其中设置账号错误/清除错误等副作用，也不调用它执行检测。
