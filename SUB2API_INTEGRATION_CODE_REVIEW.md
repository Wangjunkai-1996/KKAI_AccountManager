# AUTH / Sub2API 本地代码落地审查

日期：2026-10-03。范围：本地源码、现有测试源码及部署文档；未执行真实登录、上游请求或生产操作。

## 审查结论

两项需求可实现，没有发现必须更换架构或 OAuth 客户端的硬障碍。用户确认的主流程保持为：AUTH 重登获得新 RT → Sub2API 原账号重新授权 → 开启调度。

但当前代码还不能只串几个 HTTP 请求就作为可靠的批量后台功能交付。主要工作是 AUTH 后台任务和账号绑定，以及 Sub2API 现有导入、诊断、重新授权路径的局部修正。以下区分未实现的集成功能、现有代码缺口和需要联调的外部条件。

审查由任务运行与存储、Sub2API 接口与一致性、OAuth 与诊断三个角色并行完成，并交叉复核了主要发现。

## 本地基线

| 项目 | 核对结果 |
| --- | --- |
| AUTH | 当前开发目录 `/Users/tokk/Desktop/KKAI_AUTH`；不处于 Git 仓库内，因此没有可报告的提交号 |
| Sub2API | `/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api`；分支 `release/v0.2.8-kkai`；提交 `b583b641d03505ba4b996c3ab733c6b30a6186a1` |
| Sub2API 工作区 | 已跟踪文件无未提交修改；原有未跟踪 `FINAL_SUMMARY.md`、`OpenAI_Auto_Login_Design.md`、`OpenAI_Auto_Login_Implementation_Plan.md`、`tools/openai-login/` |
| 源码位置 | AUTH 交接文档明确桌面副本是当前开发源码；Sub2API 内的 `tools/openai-login/` 不是本次 AUTH 修改目标 |
| Go | 默认 `go1.26.5 darwin/arm64`；AUTH 声明 Go 1.24，Sub2API 声明 Go 1.27；本机已缓存可运行的 Go 1.27.0，不是硬阻碍 |

以上是本地代码基线，不代表本次验证了线上版本或健康状态。AUTH 后续实施前应记录源码快照，避免无 Git 提交号时混用历史副本。

## 可以直接复用的能力

- AUTH 已有成功 OAuth 登录及 RT 返回、SQLite、AES-GCM 加密、账号登录互斥、超时与有限重试。
- AUTH 与 Sub2API 当前 OAuth client_id 相同，均使用 `app_EMoamEEZ73f0CkXaXp7hrann`，没有发现新 RT 格式或客户端兼容障碍。
- Sub2API 已有未分组导入、RT 换取完整凭据、向原账号写入重新授权凭据、开启调度四项能力。
- Sub2API 的调度开关已经把数据库更新与调度 outbox 一起提交：[account_repo.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/repository/account_repo.go:3031)。无需另建调度系统。
- Sub2API 后台刷新已有锁、锁内重读和凭据条件更新。可以复用这些底层能力；不同管理接口是否走该协调层必须分别检查，不能根据接口名称推断。

## 必须补齐的集成功能

### 1. AUTH 当前不能接收可脱离页面执行的持久任务

当前 SSE 登录使用请求 context：[main.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/main.go:402)。断连取消是现有明确行为；批量登录仍由网页循环调用：[client.js](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/client.js:709)。进程重启只把遗留 running 尝试标记为 interrupted，不会继续回写 Sub2API。

直接让 Sub2 后端等待这个登录接口，会受请求断开和超时影响，也无法可靠处理“已经登录成功、但 Sub2 暂时不可达”。

最小补齐：AUTH 增加 SQLite 任务记录及单 worker；持久入队后返回任务 ID，worker 调用现有 Go 登录服务。记录登录、已取得新凭据、重新授权、开启调度各阶段；任务专属结果加密保存，成功后只重试尚未完成的步骤。现有交互式 SSE 无需整体重写。

同账号任务、手动重登及删除要共享约束。现在登录结束就释放互斥：[history.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/history.go:197)；账号删除会级联删除尝试记录：[store.go](/Users/tokk/Desktop/KKAI_AUTH/internal/store/store.go:251)。增加后台任务后，需要防止待回写结果被后一次登录替换，或账号删除后旧任务继续回写。

### 2. 历史成功账号的 token 已保存，但缺少服务端读取方法

AT/RT 已加密写入数据库：[store.go](/Users/tokk/Desktop/KKAI_AUTH/internal/store/store.go:626)。但 `GetCredentialsByID` 只读密码、TOTP、代理：[store.go](/Users/tokk/Desktop/KKAI_AUTH/internal/store/store.go:453)。网页导出用的是当前页面 `results`，不能替代历史账号后台导入。

最小补齐：新增仅内部使用的授权结果读取方法；导入按钮提交 AUTH 账号 ID，后端取 token。保持历史列表脱敏，无需增加面向浏览器的明文 token 查询接口。没有成功结果的账号提示先登录。

### 3. 未分组导入具备，但缺少稳定映射和永久去重

`POST /api/v1/admin/accounts/data` 的未分组行为已具备：[account_data.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data.go:285)。问题是导入结果只有数量和错误，没有逐账号目标 ID：[account_data.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data.go:95)。重复请求会继续创建账号；默认 24 小时操作幂等不能代替永久业务绑定。

最小补齐：扩展现有导入服务或增加窄同步入口，返回逐项目标 ID；持久保存来源实例、AUTH 账号与 Sub2 账号的绑定。来源唯一性应由数据库约束保证，创建和绑定一起提交。再次导入返回原目标，不清除其后来分配的分组，不覆盖已轮换的 RT。

普通创建接口空 `group_ids` 仍可能绑定默认组，不能直接替代此入口：[admin_account.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/admin_account.go:568)。

### 4. AUTH 尚无可直接复用的机器接口鉴权

AUTH 路由目前只使用 CORS 检查；没有 Origin 的请求会继续执行：[main.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/main.go:526)。这不能证明公网入口无保护，因为本次没有检查反代，但它确实不能作为新增后台任务接口的服务身份认证。

最小补齐：新增任务接口使用服务端专用密钥与固定目标地址。Sub2 当前已有 `x-api-key` 管理鉴权：[admin_auth.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/server/middleware/admin_auth.go:49)，因此服务调用有现成基础；正式集成宜把权限限定在绑定账号的导入和重新授权操作。密钥仅后端保存。

## 现有代码中需要修正的具体问题

### 5. 新 RT 兑换成功但未返回替代 RT，会留下旧失效 RT

触发顺序：AUTH 得到新 RT → Sub2 兑换成功，但上游本次不返回 refresh_token → 前端 builder 不写 refresh_token → Apply 合并并保留原账号旧 RT。结果是新 AT 暂时能用，下一次刷新又失败。

证据：[openai_oauth_service.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/openai_oauth_service.go:548)、[useOpenAIOAuth.ts](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/frontend/src/composables/useOpenAIOAuth.ts:203)、[account_handler.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_handler.go:1869)。

最小修正：集成流程明确保存 `上游返回的新 RT，否则 AUTH 本次提交的新 RT`。这个修正可以放在集成适配代码内。RT 兑换会产生真实上游副作用，不能当作可无限重复的只读检测；响应超时要标记结果待确认，避免盲目重复消费。

### 6. 重新授权后的缓存与完成结果不能只看 HTTP 200

有三个相关问题：

- Apply 写凭据后，状态恢复失败只记录日志，最终仍返回成功：[account_handler.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_handler.go:1914)。缓存删除也会吞掉错误：[token_cache_invalidator.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/token_cache_invalidator.go:67)。
- Apply 没有像常规刷新一样提升 `_token_version`。旧请求持有旧快照时，可能在清缓存后再次把旧 AT 写回：[openai_token_provider.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/openai_token_provider.go:273)。
- 旧后台刷新失败的 OpenAI 分支仍按账号 ID 直接设置 error；若错误晚于新授权返回，可能把已修复账号再次置错：[token_refresh_service.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/token_refresh_service.go:1015)。需要把这类错误回写也绑定到本次实际使用的凭据。

最小修正：在现有重新授权提交路径维护凭据版本，并使集成调用能区分凭据已保存、后处理未完成、可开启调度。缓存写入需要按版本保护；不能把“加一次版本号”当作已经覆盖检查与写入之间的所有竞态，必须用定向并发用例验证。

这里仍然复用现有重新授权和缓存能力。不是增加一个用户可见的清错步骤，也不要求建立新调度系统。停调同样不能代替凭据同步保护，因为后台刷新明确仍允许 active 且 schedulable=false 的账号：[account_repo.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/repository/account_repo.go:1402)。

### 7. 重新授权会预合并旧配置，存在覆盖并发编辑的窗口

Apply 在 handler 先读取并合并完整旧 credentials；如果此后管理员更新了 model_mapping，Apply 提交的旧映射可能把新设置覆盖回去。仓库层虽然持有行锁，但收到的已经是旧字段组成的完整补丁：[account_configuration_repo.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/repository/account_configuration_repo.go:481)。

最小修正：现有配置写入增加“只更新认证字段”语义，在锁内合并到最新值。同一次写入提升 token 版本。不能只删掉 handler 的预合并，因为通用凭据合并允许删除未带入的非敏感字段。

用户执行“重新授权并开启调度”已经表达启用意图，应当开启；但任务执行期间新的手动暂停、重新授权或删除操作应优先，旧任务需检查后停止。

### 8. 错误账号检测不能原样调用当前自动刷新入口

这是对前版方案的重要补充：统一 `OAuthRefreshAPI` 会检查 `IsActive()`，对 status=error 账号返回未刷新结果或状态变更错误：[oauth_refresh_api.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/oauth_refresh_api.go:265)。因此“错误列表逐个调用现有自动刷新”并不等于完成了检测。

现有管理刷新接口又直接调用平台刷新服务，不等同于上述协调层：[account_handler.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_handler.go:1651)。普通 `/test` 还会影响账号状态，不能当作只读分类接口。

另有三个返回语义需要区分：协调器发现凭据已被别的请求更新时可以直接返回，并不表示本次验证过 RT；管理刷新在缺 RT 但有 AT 时可能返回原 AT 成功；上游 token 刷新失败会被包装成本地 502，而非原样返回 401。因此不能只按 HTTP 状态码决定“正常/重登”。现有 [warmup 认证结果分类](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/openai_window_warmup.go:238) 可复用，补小适配即可。

最小补齐：Sub2 增加一个面向管理员所选错误 OAuth 母账号的检测入口，复用锁、重读和条件更新，允许在保持当前不可用状态的情况下验证认证情况；不先清错把账号变成 active 才检测。当前上游认证失败才交 AUTH，RT 仍可用的走刷新路径，网络/403/429 则返回对应分类。

现有 401 处理区分撤销、缺 RT 和临时冷却：[ratelimit_service.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/ratelimit_service.go:425)。历史错误文字只能筛候选；普通 OAuth 401 也可能仍为 active，因此首版若只处理错误列表，应明确这个范围。影子账号归并到凭据母账号。

### 9. 身份与导入有效期需要规范化

AUTH 按邮箱唯一保存账号，新登录结果按请求邮箱入库，却没有核对返回邮箱是否一致；工作区取默认组织：[history.go](/Users/tokk/Desktop/KKAI_AUTH/cmd/server/history.go:193)、[service.go](/Users/tokk/Desktop/KKAI_AUTH/internal/login/service.go:1097)。Sub2 Apply 本身也不会替集成方确认新凭据就是目标身份。

最小补齐：稳定 ID 绑定并核对用户/工作区信息，冲突时停止覆盖。第一版不承诺自动切换同邮箱多个 workspace。字段不足时显示待人工确认，不按邮箱第一条结果猜测。

AUTH 的 ExpiresAt 还可能取 ID token 的 exp：[service.go](/Users/tokk/Desktop/KKAI_AUTH/internal/login/service.go:1093)。直接导入时应规范为 AT 的有效期；走 Sub2 RT 兑换可使用 token 响应的 expires_in 计算。旧 CLI 导出还包含密码/TOTP、固定并发 100 和 30 天账号到期，不能作为集成 payload 原样复用。当前 Web 导出只含 recovery.email，此处不应混淆两个入口。

## 需要联调才能确认的外部条件

1. **AUTH 登录出口可用性。** 当前本地部署文档记录 sys1 直连曾遇到 Cloudflare challenge，而指定代理完成过真实登录：[SYS1_DEPLOYMENT.md](/Users/tokk/Desktop/KKAI_AUTH/SYS1_DEPLOYMENT.md:65)。这不是本轮实时结论；实施时必须使用当时可用的登录出口。Sub2 模型出口和 AUTH 浏览器登录出口分别处理。
2. **服务网络路径。** 文档记录 AUTH 监听宿主 `127.0.0.1:18082`。Sub2 容器里的 localhost 不是该宿主服务地址，服务鉴权、反代和超时要实际联调。
3. **真实登录与可用性。** AUTH `/health` 只返回固定 ok，不证明 Chrome/Playwright/Node 可完成登录。账号密码、额外验证码、停用、工作区变化仍可能使具体任务失败。

这些不要求现在修改部署或访问生产。最终需要一个账号从导入、重登到重新授权、开启调度、模型请求成功的完整验收。

## 建议实施范围

保持一个 AUTH 任务队列，Sub2API 负责诊断、原账号重新授权和开启调度。先完成服务间连接配置、账号绑定、历史结果读取及任务记录；接入未分组导入；再完成单账号重登回写与上述重新授权局部修正；最后接入批量检测和页面进度。

不需要同时改造全部账号管理、全部缓存服务或部署方式。所有阶段共同交付两项完整用户流程，不能把“只拿到了 RT”当作修复任务完成。

## 测试证据与验证范围

本轮阅读了现有测试源码，没有运行它们：

- AUTH 已覆盖加密存储、同邮箱互斥、重启中断和 SSE 断连取消；需要新增后台任务继续、同步重试、历史 token 读取及身份冲突用例。
- 未分组导入已有 [account_data_handler_test.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/handler/admin/account_data_handler_test.go:416)；需要增加稳定 ID 返回、重复导入和响应丢失重试。
- 后台刷新并发保护已有 [oauth_refresh_api_test.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/oauth_refresh_api_test.go:428)；需要覆盖本次重新授权提交路径和错误账号诊断。
- 缓存删除测试目前明确要求失败时返回 nil：[token_cache_invalidator_test.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/service/token_cache_invalidator_test.go:246)。集成路径要求严格结果时，必须同步设计和测试，不能假定现有 error 返回已经有作用。
- 调度开关有带 integration 构建标签的数据库测试：[account_repo_schedulable_mutation_integration_test.go](/Users/tokk/Documents/Codex/2026-08-09/li/work/sub2api/backend/internal/repository/account_repo_schedulable_mutation_integration_test.go:37)。普通无标签单测不会自动覆盖它。

执行的环境/基线核对命令及结果：

| 命令 | 执行目录或说明 | 结果 |
| --- | --- | --- |
| `git status --short --branch` | Sub2API 根目录 | 成功；分支及未跟踪项见上文 |
| `git rev-parse HEAD` | Sub2API 根目录 | 成功；提交见上文 |
| `git rev-parse --show-toplevel` | AUTH 根目录 | 返回 not a git repository，确认没有 Git 基线 |
| `env GOTOOLCHAIN=local GOPROXY=off go version` | AUTH 根目录；禁用自动工具链切换/下载 | `go1.26.5 darwin/arm64` |
| `/Users/tokk/go/pkg/mod/golang.org/toolchain@v0.0.1-go1.27.0.darwin-arm64/bin/go version` | 直接使用本地已缓存二进制 | `go1.27.0 darwin/arm64` |

其余为 `rg`、`sed`、`nl` 的源码交叉阅读。没有执行测试、lint、typecheck 或构建；全量测试跳过。没有联网或访问生产，没有修改业务代码；本轮仅新增审查报告并在方案中链接本报告。编译通过、真实 token 交换、服务连通和最终模型可用性仍未验证。
