# 账号管理改进：前端开发说明

日期：2026-11-13
依据：[ACCOUNT_MANAGEMENT_PLAN_20261113.md](/Users/tokk/Desktop/KKAI_AUTH/ACCOUNT_MANAGEMENT_PLAN_20261113.md)。文档审查通过后进入代码实施，实际验证与上线结果见实施记录。

## 实现边界

前端实现覆盖账号表的名称、搜索、套餐筛选、确定性终态错误文案，以及账号详情中的按需凭据查看。后端负责提供已核验的 Sub2 名称、敏感字段 reveal 接口和确定性错误分类；前端不从名称猜测绑定关系，也不把密码或 TOTP 放进 `/api/history` 列表响应。

当前“账号类型”按 `accounts.plan_type` 解释为最近一次成功 OAuth 登录识别的套餐。`free`、`plus`、`pro`、`team`、`business` 等原始值保留；空值为 `unknown`，不能归为 Free。当前导入生成的 Sub2 名称格式是 `merchant | deadline | email`，前端直接显示完整名称，不拆分前缀。

## 当前代码落点

- `cmd/server/index.html:2035` 的 `#historySearchInput`、`#historyStatusFilter`、`#historySort` 是账号表筛选控件；在这组控件中新增 `#historyPlanFilter`。
- `cmd/server/index.html:2092` 的 `#historyDetail` 是账号详情遮罩；`#historyDetailBody` 接收 `renderHistoryDetail` 输出。
- `cmd/server/client.js:80-145` 缓存账号表和详情 DOM 节点；新增 `historyPlanFilter`，以及 reveal 请求状态变量。
- `cmd/server/client.js:1177` 的 `normalizeSub2Status` 读取后端 canonical `name`、`name_source` 和 `name_stale`；标准化为 `displayName`。不增加 `display_name` 别名，避免契约漂移。
- `cmd/server/client.js:1284` 的 `normalizeHistoryAccount` 保留 `planType`，账号名称通过 `sub2Status.displayName` 读取。
- `cmd/server/client.js:2058` 的 `getFilteredHistoryAccounts` 增加套餐条件；搜索同时匹配显示名、邮箱和 AUTH ID。
- `cmd/server/client.js:2125` 的 `compareHistoryAccounts` 保持现有排序契约；本期不新增名称排序。
- `cmd/server/client.js:2179` 的 `updateHistoryControls` 继续负责当前页全选、跨页选中提示、分页和批量按钮；新增筛选后必须沿用这些规则。
- `cmd/server/client.js:2260` 附近的账号卡片把名称放在主行、邮箱放在次行、AUTH/Sub2 ID 与套餐放在第三行。
- `cmd/server/client.js:2450` 附近的 `openHistoryDetails` 管理详情请求；`cmd/server/client.js:2525` 的 `renderHistoryDetail` 增加登录资料区。
- `cmd/server/client.js:3182` 的 `formatLoginError` 和 `cmd/server/client.js:1862` 的终态文案函数统一展示后端规范原因。

## 账号名称契约与界面

`/api/history` 的 `sub2_statuses[account_id]` 增加：

```json
{
  "name": "潇潇 | 9r-29日 | nock.acetic.4t@icloud.com",
  "name_source": "current_remote",
  "name_stale": false,
  "exists": true,
  "imported": true,
  "sub2_account_id": 17075
}
```

`name_source` 的 canonical 值为 `current_remote`、`cached_remote`、`historical_import`、`local_delivery` 或 `email_fallback`；`name_stale` 是后端布尔值。前端只使用当前 destination 下已通过身份核对的名称。`name_stale=true` 表示名称可能不是最新远端值，但可信的历史/本地来源仍可显示；名称旁标记来源和“待同步”，不能因为 stale 就把可信名称隐藏。名称和邮箱相同则只显示一次；名称存在时列表显示：

```text
潇潇 | 9r-29日 | nock.acetic.4t@icloud.com
nock.acetic.4t@icloud.com
AUTH #780 · Sub2 #17075 · Plus
```

实现要点：

1. `displayName = name || ''`；名称缺失时回退邮箱，并可附“名称未同步”提示。
2. 搜索 query 使用 `displayName + email + id` 的规范化文本；名称搜索不改绑定、去重或删除逻辑。
3. 名称只做展示和搜索，不能从 `merchant`、`deadline` 或当前默认配置重建历史名称。
4. 名称长或包含中文时允许换行；手机宽度 390px 下不产生横向溢出。详情中展示完整名称。
5. 后端同步失败、绑定变更或名称被人工修改时，前端显示当前 destination 缓存中的最近可信名称或“名称待同步”，不把其他 destination 的名称套过来。服务端缓存键必须至少包含 `destination_key`、AUTH `account_id` 和 Sub2 `sub2_account_id`；前端不自行合并跨 destination 名称。

Sub2 关联缺失按后端正式契约展示，不根据 `exists=false` 的默认值猜测：

- `association_status=missing`：显示“Sub2 关联账号不存在 · 已停止自动检测和恢复”，保留 Sub2 来源、旧远端 ID 和确认时间。
- `association_status=unmatched`：显示“未找到 Sub2 关联 · 不参与自动恢复”，不宣称曾被删除。
- `unknown/stale`：显示“Sub2 状态待核对”，不能使用缺失文案；这些状态都不生成自动恢复或创建请求。

在 `historyRecoveryAction`、账号检测面板和批量检测/恢复范围中排除上述账号；旧选中项保留可见的跳过说明，不能通过批量操作绕过。缺失后旧任务的 `canceled` 是自动停止结果，不显示“恢复成功”或“等待人工处理”。保留详情、已保存名称/前缀及凭据查看；详情说明来源和时间，不能把 Sub2 缺失标成“官方删除”。后端拦截是最终保障，隐藏按钮不能代替门禁。

已确认缺失时，普通“重新登录”“保存并重试”不得隐含重新交付；现有明确重新导入/重新绑定才可建立新意图，该排障入口不作为默认操作。不新增本期无关的绑定向导。

## 套餐筛选契约

在状态筛选和排序之间新增：

```html
<label for="historyPlanFilter">账号类型
  <select id="historyPlanFilter">
    <option value="all">全部类型</option>
    <!-- 根据当前 historyAccounts 动态生成 -->
  </select>
</label>
```

动态选项由 `historyAccounts.map(account => account.planType || 'unknown')` 去重、稳定排序后生成。已知值使用友好标签，选项 `value` 保留原始小写值；未知值显示 `未知（未识别）`。例如 `self_serve_business_prolite` 不能只凭字符串改成 Pro。

筛选规则：

- `all` 不增加条件；其他值要求 `account.planType || 'unknown'` 精确相等。
- 套餐、状态、搜索三者取交集；任何条件变化都将 `historyPage=1` 并刷新列表。
- 刷新历史后重建选项；若当前值已经不在本次数据集合中，仍保留该选项并标注“当前类型（0 个匹配）”，保持 selected 值与 0 匹配结果，不偷偷回退 `all`。
- `accountTableSummary` 的总数、需处理、可调度、处理中继续统计全量；`historyRefreshStatus` 和分页显示当前组合筛选结果。
- 当前页全选只作用于组合筛选结果；已选但被新条件隐藏的账号继续保留，并在 `historySelectionStatus` 显示隐藏数量。批量操作仍按固定 ID 执行。
- 从“需要处理”区域定位账号时清除套餐和状态筛选，避免目标被筛选隐藏。

## 详情登录资料与 reveal 状态

详情顶部、状态摘要之前新增“登录资料”区：

```text
登录资料
邮箱             nock.acetic.4t@icloud.com       [复制]
密码             ••••••••                        [显示] [复制]
2FA（TOTP）密钥  ••••••••                        [显示] [复制]
                                                   [更新资料]
```

### 后端接口契约

```http
POST /api/history/{id}/credentials/reveal
Content-Type: application/json
X-Requested-With: XMLHttpRequest
Cache-Control: no-store

{"field":"password"}
```

`field` 只允许 `password` 或 `totp_secret`，每次只读取一个字段。成功响应：

```json
{"success":true,"account_id":780,"field":"password","value":"…"}
```

前端只读取 canonical `value`；不增加 `data.value` 等兼容别名。列表、普通详情、localStorage、sessionStorage、URL、日志和错误消息均不得出现秘密。接口错误只显示固定文案，不回显响应体。

普通详情响应只增加不含值的资料元数据：

```json
{"credentials_meta":{"password":{"state":"saved"},"totp_secret":{"state":"missing"}}}
```

`state` 只能是 `saved` 或 `missing`。现有 schema 无法区分“从未保存”和“已清除”，前端对 `missing` 统一显示“未保存/已清除”。reveal 固定错误契约为：字段未保存返回 HTTP 404 `credential_missing`、账号不存在返回 HTTP 404 `account_not_found`、解密失败返回 HTTP 500 `credential_read_failed`；请求格式/字段错误为 400，方法错误为 405。前端按 `code` 显示固定安全文案，不把错误正文放进 DOM，也不根据 HTTP 状态猜测凭据存在性。

### 前端状态与竞态

维护以下状态：

- `historyDetailID`：当前详情账号。
- `historyDetailGeneration`：每次打开、切换、关闭详情自增。
- `historyCredentialValues`：仅当前详情、按字段存储已 reveal 值；关闭即清空。
- `historyCredentialRequests`：字段到 `AbortController` 的映射；关闭或切换时全部 abort。

点击“显示”或“复制”才 POST reveal。请求携带 `accountID` 和 generation；响应回来后只有在账号 ID、generation、详情仍可见且字段仍是当前请求时才更新 DOM 或执行复制。迟到响应直接丢弃，不能写入新账号详情或账号卡片。

显示状态只写入字段专属节点 `data-history-credential-value`，不调用 `renderHistory`，避免轮询重绘把秘密带入卡片。名称、套餐和筛选使用独立的纯函数，不能把新逻辑全部堆进 `renderHistory`。隐藏时恢复掩码并从内存 Map 删除该字段；关闭详情时清空 `#historyDetailBody`，避免秘密残留 DOM。复制复用现有 `copySecret`（`cmd/server/client.js:3395`）的 Clipboard API/回退实现，失败显示“复制失败，请手动复制”，不承诺自动清空系统剪贴板。

密码和 TOTP 不存在时显示“未保存/已清除”，不能尝试恢复历史密码。`2FA（TOTP）密钥`明确表示种子，不生成或展示当前 6 位验证码。无需增加重复确认弹窗；点击字段按钮就是明确动作。

## 错误展示契约

优先使用后端返回的规范确定性阻塞原因 `manual_reason`/`error_code` 和后端安全 `message`；旧的 Sub2 `token_revoked` 只能出现在检测历史，不能覆盖后续登录阻塞。前端只做少量稳定展示映射，不根据 URL、路径、通用 `invalid_mfa` 或任意原始文本自行推断验证类型：

| 错误码/证据 | 主文案 | 操作 |
|---|---|---|
| 后端规范码 `email_verification_required` | 需要邮箱验证码 · 自动恢复已停止 | 没有系统验证完成事件就保持停止 |
| 后端规范码 `phone_verification_required` | 需要手机/短信验证 · 自动恢复已停止 | 没有系统验证完成事件就保持停止 |
| 后端规范码 `password_rejected` | 密码错误 · 自动恢复已停止 | 只有系统收到可信的新凭据版本才允许新一轮 |
| 后端规范码 `totp_rejected` | TOTP 验证码被拒绝 · 自动恢复已停止 | 只有系统收到可信的新凭据版本才允许新一轮 |
| 后端规范码 `verification_code_rejected` | 验证码被拒绝，验证类型待确认 · 自动恢复已停止 | 没有系统放行事件就保持停止 |
| 后端规范码 `auth_page_timeout_exhausted` | 认证页面等待超时 · 自动恢复已停止 | 只有新的可信放行事件才允许新一轮 |

通用 `incorrect_code`、`invalid_credentials`、`invalid_mfa` 等没有规范确定性阻塞原因时，前端展示后端 message 与“原因待确认”，不猜成 TOTP、密码、邮箱或手机。后端若只给 `code=unexpected_auth_page`，前端显示“认证页面未完成 · 原因待确认”，是否停止重试由后端 `retryable/manual_reason` 契约决定。

`loginManualAction`、`renderHistoryAttention`、卡片 `primaryProblem`、详情 attempt 时间线和批量结果使用同一映射。确定性错误不能生成“下次自动重试”或“等待人工处理”；网络、429、5xx 和 Cloudflare challenge 继续显示原有退避时间。后端历史字段 `manual_action` 只作为诊断文本来源，主文案统一为“自动恢复已停止”。

当 `requires_action=true && retryable=false && next_retry_at=null` 时，卡片首行固定显示“自动处理已暂停”和具体原因/时间；主操作只保留“查看详情”。不得显示“下次自动重试”“等待后台执行”或“检测并恢复”。详情中的“修改资料”“开始新一轮”属于显式排障兜底，默认折叠或置于次级区域；它们不能被轮询、服务重启或定时扫描代替，也不能成为无人值守验收的前置条件。

错误展示、名称/套餐筛选和凭据 reveal 必须分别收口为纯函数/小型状态模块，不把逻辑继续堆进 `renderHistory` 或 `renderHistoryDetail`。复用现有脚本加载和事件委托模型，不引入框架依赖，不复制后端错误码翻译表。

## 定向测试计划

在现有 `cmd/server/client.test.cjs` fixture 中增加：

- `displayName`/`name` 显示在账号主行，名称搜索命中，名称缺失回退邮箱。
- `historyPlanFilter` 动态生成 Plus、Free、未知和新套餐值；状态+套餐+搜索交集、零结果、刷新回退、回第一页。
- 组合筛选后当前页全选、跨页隐藏选中提示、批量固定 ID 不变。
- 详情密码/TOTP 默认掩码；显示、隐藏、复制分别 POST 单字段；列表刷新不出现秘密。
- 关闭详情、切换账号、迟到 reveal 响应不会写入新详情或 DOM；请求 abort 被调用。
- 邮箱验证、手机验证、密码错误、TOTP 错误、验证类型不明的错误文案和“停止自动重试”一致。
- 390px 与键盘焦点下名称、套餐控件、显示/复制按钮可用且无横向溢出。

实现后的最小验证命令：

```sh
node --check cmd/server/client.js
node cmd/server/client.test.cjs
```

上面是实现完成后的定向验证要求。后端接口加入后，Node fixture 必须断言请求方法、路径和 `field` 白名单，不能只检查渲染文本。

## 最高风险验收

1. 账号 A 的 reveal 请求迟到于切换到账号 B：A 的密码不能显示在 B 或列表任何节点。
2. 关闭详情后 reveal 响应返回：不能重新打开详情、写 DOM、写缓存或触发复制。
3. `/api/history` 和普通 `/api/history/{id}` 响应、浏览器缓存、错误提示没有密码/TOTP；只允许专用 POST 返回单字段。
4. `name` 来自错误 destination、未核验或 stale 状态时不能显示，避免账号身份串联。
5. `unknown` 套餐不能被筛选为 Free；名称和套餐筛选改变后，隐藏选中账号仍不能被批量误删或误复活。
6. 邮箱验证页面、手机验证页面和密码/TOTP 错误被识别后，内层登录、自动恢复、持久重试均停止；网络超时仍可退避。
