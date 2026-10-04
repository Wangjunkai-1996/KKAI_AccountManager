# 第一阶段：AUTH 成功账号一键导入 Sub2API 未分配账号

日期：2026-10-03。状态：AUTH 侧首版已实现，尚未连接真实 Sub2 环境联调。

## 1. 决策与交付目标

本阶段只修改 AUTH，调用 Sub2API 现有导入、列表、详情接口。Sub2API 无需修改源码、数据库结构或重新发布。

用户操作是：在 AUTH 选择认证成功的账号，点击“导入 Sub2 未分配”，看到每个账号的导入结果。新账号在 Sub2 中没有分组关联，AUTH 保存导入记录和目标账号 ID。关闭页面后已提交的导入继续执行。

本阶段不包含错误账号检测、AUTH 自动重登、原账号重新授权、修改 Sub2 分组或调度开关。之前完整互通方案中为自动修复提出的 Sub2 改造，不是本阶段的前置条件。

## 2. 为什么只改 AUTH 足够

| 所需能力 | 已存在的 Sub2API 接口 / 行为 |
| --- | --- |
| 新建未分配 OAuth 账号 | `POST /api/v1/admin/accounts/data`，显式 `skip_default_group_bind:true` |
| 查找刚导入的账号 | `GET /api/v1/admin/accounts`，按名称中的固定来源标记搜索 |
| 确认目标 ID、来源信息和分组 | `GET /api/v1/admin/accounts/{id}`，返回账号详情 |
| 服务端调用鉴权 | `x-api-key: <Sub2API Admin API Key>` |

已核实，自定义 `extra.kkai_auth_import` 可以保存并从列表/详情读回。导入接口虽然只返回数量和错误，但能用固定名称标记找候选，再核对 extra，补齐目标 ID。

现有操作幂等有时效，业务创建与幂等结果记录也不是同一事务。因此本阶段的可靠性策略是“本地去重、请求固定、来源回查、结果不明不重发”。不承诺在跨客户端操作、AUTH 数据丢失或来源标记被修改时仍能完全自动去重。

## 3. 页面与使用流程

### 3.1 初次配置

部署时在 AUTH 服务端配置一个固定 Sub2API 目标：

```text
SUB2API_BASE_URL=https://<Sub2API 域名>
SUB2API_ADMIN_API_KEY=<管理密钥>
```

BASE_URL 使用站点根地址，后端统一拼接 `/api/v1`，不接受账号级请求传来的目标地址。管理密钥通过受保护的环境文件传入，网页不显示、不保存、不提交该密钥。第一版不增加网页密钥编辑功能。

页面显示 Sub2 是否已配置。首版不提供网页端连接检查，实际连通性在首次导入时验证并显示脱敏错误。

### 3.2 单账号与批量导入

- 每个符合条件的历史账号增加“导入 Sub2”按钮；点击直接提交该账号。
- 历史列表工具栏增加“导入成功账号”，打开一个轻量导入面板；只列符合条件的账号，支持搜索、勾选和当前页全选，点击“导入选中账号”提交。
- 导入面板复用现有历史数据和页面样式，使用独立选择集合。现有“批量复活”只选择失败账号的规则保持原样，避免复活与导入使用同一套选择条件。
- 每行显示“未导入、排队、导入中、已导入、失败、结果待核对”；结果保存在后端，刷新页面后仍可查看。
- 明确失败的记录可在条件修正后重试；结果待核对的记录提供“核对结果”，该动作只查询 Sub2，不再次创建。

首版以历史列表为统一入口。新登录成功后已有历史刷新，因此不必再为临时结果列表实现第二套导入逻辑。

### 3.3 导入资格

由 AUTH 后端计算，前端只显示结果：

1. 当前记录最近一次登录成功，账号不在登录中、失败、停用或删除状态。
2. 数据库中存在成功授权的 AT、RT；邮箱和 ChatGPT account ID 等必要身份信息完整。
3. 能从 AT 的顶层 exp 读取到期时间；不把当前可能来自 ID token 的保存时间盲目当作 AT 到期时间。
4. 该 AUTH 账号对当前目标没有已完成或结果不明的导入记录。重复点击返回已有记录。

AT 已过期但 RT 存在时允许导入，显示“AT 已过期，导入后由 Sub2 刷新”。不在 AUTH 导入流程中兑换 RT，也不为此自动启动浏览器。RT 非空不证明其仍有效，因此“已导入”表示 Sub2 接收并创建账号，不表示已经验证模型调用成功。

没有成功授权结果或关键字段缺失时，提示使用 AUTH 现有登录功能取得完整结果；本阶段不增加自动补登录流程。

## 4. 后端执行流程

```text
点击导入，提交 AUTH 账号 ID
    ↓
后端校验配置、资格与本地已有记录
    ↓
解密成功结果，生成固定导入请求，写入 SQLite
    ↓
返回已接收；AUTH 单个 worker 逐条处理
    ↓
每次 POST 一个账号到 Sub2 导入接口
    ↓
解析创建数量及错误，按来源标记回查账号
    ↓
核对身份、来源和首次分组状态，保存 Sub2 账号 ID
    ↓
页面刷新对应记录
```

每次只发送一个账号，批量由 AUTH 串行处理。这样无需从 Sub2 的总计数反推哪一项成功；某项失败也不回滚其他成功项。

初版采用单个导入 worker，不新增通用任务中心、Redis 或新的服务。worker 使用服务生命周期 context，网页请求仅负责提交和查询。一次提交最多 100 个账号，逐条处理；HTTP 请求设明确超时，禁止重定向及写请求自动重试。

## 5. Sub2API 请求与字段映射

AUTH 后端发送：

```http
POST /api/v1/admin/accounts/data
x-api-key: <仅后端保存的 Admin API Key>
Idempotency-Key: <本次导入操作的固定 UUID>
Content-Type: application/json
```

示意请求，所有 token 均为占位符：

```json
{
  "skip_default_group_bind": true,
  "data": {
    "type": "sub2api-data",
    "version": 1,
    "exported_at": "<本次操作固定时间>",
    "proxies": [],
    "accounts": [{
      "name": "AUTH-<operation_uuid> | <邮箱>",
      "platform": "openai",
      "type": "oauth",
      "credentials": {
        "access_token": "<已保存的 AT>",
        "refresh_token": "<已保存的 RT>",
        "client_id": "app_EMoamEEZ73f0CkXaXp7hrann",
        "email": "<邮箱>",
        "chatgpt_account_id": "<工作区 ID>",
        "expires_at": 1234567890
      },
      "extra": {
        "email": "<邮箱>",
        "kkai_auth_import": {
          "source_instance_id": "<持久 AUTH 实例 UUID>",
          "source_account_id": "<AUTH 账号 ID>",
          "operation_id": "<本次操作 UUID>"
        }
      },
      "concurrency": 3,
      "priority": 1,
      "rate_multiplier": 1,
      "auto_pause_on_expired": true
    }]
  }
}
```

字段规则：

- 名称的 UUID 标记放前面，名称总长限制在 100 字符以内；完整邮箱保留在身份字段。操作记录存下最终名称，不在重试时生成新时间或随机值。
- organization_id、plan_type 有值再传；当前 AUTH 没保存的 id_token、chatgpt_user_id 不伪造。可从本次已保存 token 中读取的身份应与本地身份核对，冲突则停止提交；解析 claims 只用于元数据，不声称完成了在线验证。
- expires_at 只放在 credentials 中，采用 AT 自身 exp。省略非必需的 expires_in；不设置账号顶层 30 天业务到期。
- 明确 concurrency=3，避免当前 Web 导出漏字段落成 0，而 legacy 模式下 0 表示不限并发。继承出口池时服从 Sub2 自身的每出口并发配置，不承诺总并发为 3。
- 不传 AUTH 密码、TOTP、登录代理或 recovery 对象。新账号使用 Sub2 当前默认出口策略；若默认出口配置不满足创建要求，展示其安全错误摘要，不从 AUTH 搬代理补救。
- 未分配是没有分组关联，不是新建一个“未分配”分组。Sub2 的新建账号默认仍可处于 active、schedulable=true，后台刷新/隐私设置等原有行为继续适用。

## 6. 结果回查与目标 ID

Sub2 导入响应必须同时检查 HTTP/业务响应和 `account_created`、`account_failed`、`errors`。HTTP 200 但 account_failed=1 不能显示导入成功。

由于单次仅导入一个账号，account_created=1 可以确认创建已得到服务端承认。随后：

1. 调用 `GET /api/v1/admin/accounts?platform=openai&type=oauth&group=ungrouped&lite=true&search=<AUTH-operation_uuid>`，遍历候选分页；首轮查询要求目标账号仍未分组。
2. 对候选调用 `GET /api/v1/admin/accounts/{id}`，严格核对来源实例、来源账号、operation_id、platform、type 和可用身份信息。名称只用于搜索，不能单凭名称绑定。
3. 唯一匹配时保存 Sub2 账号 ID。首次确认还检查分组关联为空。
4. 零匹配、多个匹配、身份不一致或首次分组状态异常，显示“结果待核对”；已经得到创建确认的记录不能因此再次 POST。

导入成功后，后续查询优先按已保存 ID 读取，并核对来源。用户后来在 Sub2 分组或改名称，不会触发 AUTH 重新创建或清除分组。目标被删、ID 不匹配时显示对应状态，不自动重建。

## 7. 数据与去重策略

复用 AUTH SQLite 和现有 AES-GCM。新增一张专用 `sub2_imports` 表；在现有元数据存储中保存一次生成的 AUTH 实例 ID。

| 字段组 | 内容 |
| --- | --- |
| 本地唯一绑定 | destination_key、auth_account_id，组合唯一 |
| 操作身份 | operation_id、固定 Idempotency-Key、来源名称标记 |
| 固定请求 | encrypted_payload，包括本次 token 和身份；未知/已确认任务固定这份请求，明确失败重试可用同一 operation 更新最新 OAuth 快照 |
| 执行事实 | state、create_acknowledged、sub2_account_id（可空） |
| 展示及排查 | 脱敏 last_error、created_at、updated_at |

destination_key 与规范化的固定目标配置关联，轮换管理密钥不产生新目标。更改目标地址时暂停原目标未完成记录，不能把同一批记录发向新站点；域名迁移到同一实例需明确迁移配置与记录。

状态流转使用 `queued → sending → confirming → imported`，异常为 `failed` 或 `unknown`。发送前先持久化 sending；queued 可以在进程重启后继续，sending 在重启后只能转入只读核对。confirming 包含已创建但尚未取得目标 ID 的情况。

导入期间取得同账号操作保护，避免本次请求准备/发送时发生手动重登或删除。任务结果独立保存，不依赖账号“最近一次结果”。首版在任务表中继续以 AUTH 密钥加密保留 payload，以支持明确失败后的同 operation 最新 token 重试；页面和接口不会返回 payload。后续可增加成功任务的密文清理与保留期限，发送结果不明时只允许查询核对。

已完成导入记录不随 AUTH 账号删除直接级联清空。第一版的重复点击保护针对同一份 AUTH 数据中的稳定账号 ID；删除后重建同邮箱账号、历史手工导入账号、其他系统同时导入的同邮箱账号不做自动合并。

## 8. 失败处理

| 情况 | 行为 |
| --- | --- |
| 未配置、缺必要字段、账号正在登录 | 提交前拒绝该项，说明原因 |
| 重复点击 / 多个页面同时提交 | 返回已有导入记录，不新增远端请求 |
| 管理密钥错误、权限或代理入口拒绝 | 标记配置问题，暂停本批后续写请求；修正配置后处理 |
| 明确发生在写入前的参数校验失败 | 记录失败，修正后建立新操作；旧失败响应可能被幂等缓存，不能永远重用旧操作 key |
| HTTP 200 但逐项创建失败 | 展示安全错误摘要；可确认未创建的情况才允许重新提交，否则转核对 |
| 超时、断连、5xx、格式异常、进程在发送阶段退出 | 结果未知；只查询来源标记，不自动重发 POST |
| 返回创建成功，但关联查询失败 | 显示“已创建，关联待核对”，后续只查询 |
| 回查唯一匹配 | 保存 ID；根据首次分组和身份核验结果完成 |
| 来源标记被改、匹配多个或无法确认 | 保留待核对，用户在 Sub2 检查；第一版不提供“忽略风险强制再次创建”按钮 |

Idempotency-Key 是辅助保护：默认有效期 24 小时，实际可配置；账号创建与幂等回执记录存在分离窗口。因此“同 key 重试一定安全”不作为方案前提。未知写入只查不重发，保证不会因为 AUTH 自己的超时重试主动制造第二个账号。

回查重试有次数和退避上限，不能长期高频扫描。页面“核对结果”仅刷新当前记录的远端结果。

## 9. AUTH 接口与改动文件

建议只增加以下入口，路径为计划命名：

| AUTH 接口 | 用途 |
| --- | --- |
| `POST /api/sub2/import` | 接收 account_ids，校验并写入逐账号记录，返回已接收/已存在/拒绝项 |
| `POST /api/sub2/import/{id}/reconcile` | 对结果未知项只做查询核对 |
| 现有 `GET /api/history` 扩展 | 返回可导入资格、导入状态和目标 ID；仍不返回 token/key |

上述是浏览器到 AUTH 的同源管理操作，不需要 Sub2 回调 AUTH，也不需要为本阶段设计 Sub2→AUTH 机器接口。新增操作应处于 AUTH 受保护的管理访问范围；当前源码的 CORS 不能代替身份认证，部署时需要核验实际管理入口的访问保护。

| 文件 | 计划改动 |
| --- | --- |
| [store.go](/Users/tokk/Desktop/KKAI_AUTH/internal/store/store.go) | 成功 token 内部读取；专用导入表与加密请求；唯一记录和状态更新 |
| [main.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/main.go) | 读取固定配置；注册接口；启动/停止单 worker |
| [history.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/history.go) | 返回安全资格和导入状态；与同账号重登/删除协调 |
| [client.js](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/client.js) | 单行导入、导入面板选择、提交与状态轮询 |
| [index.html](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/index.html) | 目标状态、按钮及轻量导入面板 |
| 拟新增 cmd/server/sub2_import.go | 集中放导入 handler、单 worker、Sub2 HTTP 客户端与结果回查 |
| 对应定向测试 | 存储、HTTP 模拟、重复提交及页面选择行为 |

实现优先使用 Go 标准库 net/http 和已有 SQLite/加密代码，不新增运行时依赖。Sub2API 本阶段不修改文件。

## 10. 验收标准

1. 刚认证成功的账号和刷新页面后的历史成功账号都能导入。
2. Sub2 新账号为 OpenAI OAuth，首次确认没有任何分组关联。
3. 账号显示的身份、AT/RT、有效期正确；密码/TOTP/代理密码不出 AUTH。
4. 单账号和批量均有逐项结果，一项失败不影响其他已成功项。
5. 重复点击、多标签页提交、刷新页面不会重复创建。
6. 关闭页面后继续导入；queued 重启后继续；发送中重启只查结果、不盲目再发。
7. 模拟“Sub2 已创建但 HTTP 响应丢失”，回查能找回 ID；查不到或多条时停在待核对。
8. HTTP 200 但 account_failed=1 不显示成功；5xx 不被简单当作可以重新创建。
9. 导入后在 Sub2 修改名称或分组，再次点击不覆盖、不清分组、不写回旧 RT。
10. AT 过期且 RT 存在时可导入并准确提示；无成功结果/缺关键身份时拒绝。
11. legacy 导入并发明确为 3；出口池继承按 Sub2 现有配置执行。
12. 原有失败账号批量复活、当前页全选和历史分页不受新导入选择影响。
13. 仅向配置目标发送管理 key 和 token，重定向不携带秘密跳转；页面和日志不暴露凭据。

## 11. 实施顺序与验证范围

第一步：实现服务端配置、内部 token 读取、导入记录与固定 payload；使用本机 HTTP 模拟验证契约。

第二步：实现逐条导入和来源回查；重点验证重复提交、响应丢失、幂等过期和重启。

第三步：接入 AUTH 页面，检查单行、批量、资格、状态与原有批量复活行为。

第四步：按 AUTH 自身发布流程部署，使用一个实际账号验收“成功认证 → 导入 → Sub2 未分配列表出现”；再扩大批量。只发布 AUTH，无需 Sub2 镜像构建或发布。

开发后的定向验证范围为 AUTH 的 internal/store 与 cmd/server 相关单测、导入 HTTP 模拟、JavaScript 语法及页面交互检查；不因本功能默认运行 Sub2 或整个工作区全量测试。若涉及真实账号或部署，遵循当次授权及 AUTH 发布门禁。

本次仅制定方案，未执行上述实施/验收步骤，未修改业务代码，未调用真实 Sub2 接口。测试、构建与全量检查均未运行。

## 12. 本地代码依据

- [现有导入请求结构](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data.go:90)
- [默认跳过分组及实际创建](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data.go:285)
- [名称搜索条件](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/repository/account_repo.go:1174)
- [自定义 extra 的读回能力](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/dto/mappers.go:428)
- [业务完成后记录幂等回执的窗口](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/idempotency.go:450)
- [AUTH 已保存加密授权结果](/Users/tokk/Desktop/KKAI_AUTH/internal/store/store.go:626)
- [AUTH 现有批量复活选择规则](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/client.js:187)

本方案按第一阶段范围使用现有 Sub2 接口。只有以后要求跨客户端业务唯一、未知写入可自动重试且永不重复等更强保证时，再评估 Sub2 的原子创建与外部唯一绑定扩展。
