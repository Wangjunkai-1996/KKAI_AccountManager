# AUTH 与 Sub2API 账号互通方案（按新 RT 重新授权流程修订）

日期：2026-10-03。状态：本地源码审查与方案，尚未实现或联调。

当前先实施单向导入，范围以 [第一阶段详细方案](/Users/tokk/Desktop/KKAI_AUTH/SUB2API_IMPORT_PHASE1_PLAN.md) 为准：只改 AUTH，使用现有 Sub2 导入和查询接口，以本地记录、来源回查及未知结果不重发处理重复提交。本文涉及 Sub2 新增同步/恢复服务的建议保留给后续完整互通阶段。

本地代码的逐项落地缺口、现有实现问题和验证边界见 [代码审查报告](/Users/tokk/Desktop/KKAI_AUTH/SUB2API_INTEGRATION_CODE_REVIEW.md)。其中错误账号检测不能原样使用只处理 active 账号的自动刷新入口；重新授权需补齐新 RT 回填、缓存/凭据版本及并发更新保护。

## 结论

两项需求都可以实现。第二项的核心就是用户提出的流程：

**Sub2API 检测失效账号 → AUTH 创建登录任务 → 登录成功取得新 refresh_token → Sub2API 用新 RT 给原账号重新授权 → 开启调度。**

已确认 Sub2API 现有界面就是用 RT 换取完整凭据，再写回指定账号；开启调度也已有接口。最佳方案是把这些现有操作接成后台任务，复用现有 OAuth 和账号管理逻辑。

前版过度展开了通用恢复机制，容易让人误解为要靠“清除错误”恢复账号。本版以新 RT 重新授权为中心。重新授权接口内部的状态处理属于已有实现细节，无需给用户增加单独“清错”步骤。

## 三角色审查结论

| 角色 | 核实结果 | 本次需要补齐 |
| --- | --- | --- |
| API 与数据一致性 | 未分组导入、RT 验证、原账号重新授权、开启调度均有现成能力 | 两边账号绑定、导入去重、后台串联 |
| OAuth 与安全 | AUTH 登录成功已返回 RT；Sub2 可用 RT 换取 AT 和账号信息 | 核对目标身份、保留换取后的最新 RT、服务间鉴权 |
| 任务与运行可靠性 | AUTH 有 SQLite 和凭据加密；当前登录仍受网页请求生命周期影响 | 持久任务、关页继续、失败重试和明确进度 |

首版面向 OpenAI 普通 OAuth 账号。保持两个现有服务，AUTH 执行登录任务，Sub2API 管理账号和调度；复用 AUTH SQLite，不增加第三个服务。

## 需求一：AUTH 一键导入 Sub2 未分配账号

用户在 AUTH 成功账号列表勾选账号，点击“导入 Sub2API”。

1. AUTH 读取已保存的成功登录结果，检查凭据与账号身份。
2. 没有绑定时，在 Sub2API 新建 OAuth 账号，跳过默认分组绑定。
3. 保存 AUTH 账号 ID 与 Sub2API 账号 ID 的对应关系，返回逐项结果。
4. 再次导入返回已绑定账号，避免重复创建，也不覆盖该账号后来分配的分组或已轮换的 RT。

“未分配”表示没有任何分组关联。新建使用 Sub2API 现有未分组导入能力；不能把普通创建接口的空分组参数直接当作跳过默认分组。

现有接口是 `POST /api/v1/admin/accounts/data`，请求格式：

```json
{
  "data": {
    "type": "sub2api-data",
    "version": 1,
    "proxies": [],
    "accounts": []
  },
  "skip_default_group_bind": true
}
```

通用导入只返回数量和错误，不返回逐账号 ID，也没有永久账号去重。因此建议补一个小的同步入口，复用现有创建逻辑，返回来源账号、目标 ID 和结果。账号创建与唯一来源绑定要一起提交，防止响应丢失后重试产生重复账号。

已有 Sub2API 账号可建立一次性绑定。邮箱用于寻找候选，用户和工作区身份用于确认；同邮箱多个工作区时不能自动选第一条。

## 需求二：检测失效 → AUTH 重登 → 新 RT 重新授权 → 开启调度

### 用户操作

Sub2API 错误账号列表新增“检测并重新授权”按钮，可作用于勾选账号或当前筛选结果。该操作明确包含授权成功后开启调度。

```text
检测选中账号
    ↓
确认认证失效且已绑定 AUTH
    ↓
AUTH 创建后台任务并重新登录
    ↓
取得并加密保存新 RT
    ↓
Sub2API 验证新 RT，取得最新完整凭据
    ↓
重新授权同一个 Sub2API 账号
    ↓
开启调度，回读状态并显示结果
```

如果旧 RT 仍能通过现有刷新机制恢复，可以直接完成；确认需要重登时再交给 AUTH。网络错误、429、平台间接口鉴权 401 不应误识别为上游账号 RT 失效。

### 已核实的现有重新授权路径

当前前端 `ReAuthAccountModal.vue` 的 `handleValidateRefreshToken` 已执行前两步：

| 步骤 | 当前接口 | 作用 |
| --- | --- | --- |
| 用新 RT 获取完整凭据 | `POST /api/v1/admin/openai/refresh-token` | 传入新 RT、匹配的 client_id 和账号出口，取得 AT、有效期、身份等 |
| 重新授权原账号 | `POST /api/v1/admin/accounts/:id/apply-oauth-credentials` | 用 type=oauth 和完整 credentials 更新指定账号，保留原分组及业务配置 |
| 开启调度 | `POST /api/v1/admin/accounts/:id/schedulable` | 传入 `{"schedulable":true}` |

所以不需要重新实现 OAuth 授权协议。推荐把这三步的现有服务逻辑封装为一个供 AUTH 调用的“用 RT 重新授权并启用”操作，返回每一步的结果；Sub2API 内部直接复用服务函数，无需绕 HTTP 调用自己。

重新授权接口已经包含认证状态恢复和 token 缓存失效处理；本需求不增加独立“清除错误”按钮或前置步骤。

### 必须保留的少量保护

- **更新原账号**：任务保存目标 Sub2API ID，登录结果核对用户/工作区；原分组、代理、模型设置、并发保持原值。
- **保存真正最新的 RT**：用 AUTH 新 RT 换取凭据时，如果上游返回轮换后的 RT，以返回值为准；如果没有返回 RT，使用本次提交的 AUTH 新 RT，不能误保留 Sub2API 中已失效的旧 RT。
- **成功才开启调度**：新 RT 验证失败或凭据写入失败，停在对应步骤。用户点击本操作已表达开启调度的意图；若任务执行期间又手动暂停、重新授权或删除账号，则以较新的操作为准。
- **任务可继续**：新 RT 和换取后的凭据按阶段加密保存。关闭页面不取消；Sub2API 暂时不可达时重试同步，不反复重登。同账号不并发跑多个修复任务。
- **不误报结果**：授权成功与调度开启分别记录；接口超时先查任务/账号结果，再决定是否重试。上游刷新响应丢失时标记待确认，不盲目重复消费 RT。
- **服务端传递秘密**：密码/TOTP 留在 AUTH；服务密钥和 RT 不进入浏览器地址、任务日志或公开接口。

当前重新授权代码还有两个需要在接入时做定向调整的细节：部分后处理失败只记录日志，缓存删除失败也可能被吞掉；现有状态恢复会一并处理限流信息。自动任务应准确返回后处理结果，并保留与本次认证故障无关的限制。这些是复用现有重新授权服务时的局部修正，不要求另建一套调度系统。

“重新授权并开启调度”可以作为确定的操作结果；“模型已恢复可用”需有实际请求成功的证据。首轮联调用一个低成本模型请求验证完整闭环，不把额外模型请求默认加到每一次重登中。

## 最小改动清单

| 项目 | 改动 |
| --- | --- |
| AUTH | Sub2API 连接配置；账号 ID 绑定；一键导入；SQLite 后台登录/同步任务；任务进度 |
| Sub2API | 错误账号检测及提交 AUTH 按钮；可返回目标 ID 的幂等导入；复用现有逻辑的“新 RT 重新授权并启用”服务入口 |
| 两边共同 | 服务鉴权；同账号去重；阶段结果查询；身份及并发变更检查 |

任务使用 AUTH 已有常驻服务执行。现有部署文档记录 AUTH 位于 sys1，但本次没有检查线上状态；实际服务地址和连接方式在联调时核对。容器里的 localhost 不能直接当作宿主 AUTH 地址。

## 实施顺序与验收

1. 先完成账号绑定和一键未分组导入。
2. 打通单账号闭环：AUTH 重登取得 RT → 原账号重新授权 → 开启调度。
3. 加入错误账号检测、批量提交、后台持久任务和失败重试，验证重复点击、关页和重启。

验收重点：

- 重复导入不重复创建；新建无分组；已有账号再次导入不改分组、不写回旧 RT。
- 同一账号重登后 ID 不变，新凭据生效，调度开启。
- 新 RT 无效时不进入启用步骤；换取返回的最新 RT 不丢失。
- 任务中断后继续正确阶段；响应丢失不会重复创建或覆盖更新的凭据。
- 身份不匹配、任务期间人工再次暂停或删除时，旧任务停止。
- 一个真实账号完成导入、重登、重新授权、启用及模型请求验证，再扩大批量。

## 代码依据与验证范围

AUTH 主目录：`/Users/tokk/Desktop/KKAI_AUTH`。

Sub2API 主目录：`/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api`。本地分支 `release/v0.2.8-kkai`，HEAD `b583b641d03505ba4b996c3ab733c6b30a6186a1`，不代表本次核验过线上版本。

- [现有 RT 重新授权界面流程](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/frontend/src/components/admin/account/ReAuthAccountModal.vue:642)
- [RT 验证与凭据组装](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/frontend/src/composables/useOpenAIOAuth.ts:157)
- [RT 换取接口](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/openai_oauth_handler.go:273)
- [原账号重新授权接口](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_handler.go:1812)
- [开启调度接口](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_handler.go:3235)
- [AUTH 已返回新 RT](/Users/tokk/Desktop/KKAI_AUTH/internal/login/service.go:607)
- [导入请求结构](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data.go:90)

本地身份核对命令：`git status --short --branch`、`git rev-parse HEAD`，均成功。接口通过源码交叉阅读核对。本次只修改方案文档，未修改业务代码，未运行测试；全量测试跳过。尚未进行真实 RT、网络连通或生产调度验证。
