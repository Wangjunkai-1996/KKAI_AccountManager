# AUTH 账号状态检测实施记录

日期：2026-10-04。对应 [详细设计](ACCOUNT_STATUS_CHECK_PLAN.md)。

## 实现范围

AUTH 自己读取加密保存的 Access Token，通过固定 OAuth 模型端点调用 `gpt-5.6-luna`。新增独立账号检测面板、单个/批量检测、搜索筛选、分页与当前页全选、进度与详情、取消、刷新恢复。每批最多 100 个，检测真并发可选 1/2，全局一个活动批次；登录原有 1–10 槽独立。

检测结果和登录状态分开。AT 过期、真实 HTTP 401、流内鉴权错误、403、安全验证、429/额度、模型权限、代理、网络、超时分别记录。新登录更换凭据后旧结论标过时；检测不读取 RT、不刷新 token、不自动登录、不删除/禁用账号，不查询或改写 Sub2。

实现使用 Go 标准库、已有 SQLite/AES-GCM 和原生 JavaScript，没有新增依赖。主要文件：

- `internal/probe/client.go`、`stream.go`：有界 HTTP/SSE 调用与安全分类。
- `internal/store/account_checks.go`：持久任务、原子领取、幂等、版本化结果、取消/重启恢复。
- `cmd/server/account_checks.go`：API 与两个后台 worker。
- `cmd/server/account-checks.js`：独立面板与增量 DOM 更新。
- `cmd/server/history.go`：安全摘要/详情及读取可用性标志。

## 审查中修复的关键问题

1. sys1 实测上游返回 HTTP 200 和有效 SSE，但没有 Content-Type。仅对缺头开放严格 SSE 解析，仍要求 completed、内部 completed 状态、无 error、有文本；HTML/JSON/空流不通过。
2. 取消浏览器请求时，SQLite 可能返回 `sql.ErrTxDone`。API 同时检查请求 context，不把页面关闭误判为存储故障并暂停后台。
3. 代理配置变更后的重复提交先按幂等键读取原批次，不使用新配置重放。
4. 407 触发项落库、停止批次和取消排队项在同一事务，防止另一 worker 在中间派发新请求。
5. 实际客户端没有发出请求时，用最终证据修正 request_attempted；中断时保守保留已知/未知，不伪造 401 或确定零用量。
6. 网络错误或 503 导致创建结果不确定时，页面保留幂等键；数据读取失败禁用提交，不能误重试另一个账号。
7. 详情和列表都更新凭据时效，丢弃迟到读取，不让旧绿色结果覆盖新凭据状态。

协议、存储并发、前端三个角色实施并交叉审查。没有以缩短代码为由省去取消竞争、终态持久化和凭据保护。

## 已完成验证

以下命令均通过，采用本任务及直接受影响范围，未运行 `go test ./...` 或全仓检查。

```bash
go test ./internal/probe -run TestProbe -count=1 -timeout=90s
go test ./internal/probe -run '^TestProbeMissingContentType$' -count=1 -timeout=90s
go test ./internal/store -run '^TestAccountCheck' -count=1 -timeout=90s
go test -race ./internal/store -run '^TestAccountCheck.*(Concurrent|Cancel|Recover|Credential|Delete)' -count=1 -timeout=90s
go test ./internal/store -run '^Test(Credentials|WrongKey|AttemptHistory|DeletedAccount|RecoverInterrupted|OAuthResult)' -count=1 -timeout=90s
go test ./internal/store -run '^TestAccountCheckIdempotencyAndEligibility$' -count=1 -timeout=90s
go test ./internal/store -run '^TestAccountCheckAttemptedFinalEvidence$' -count=1 -timeout=90s
go test ./cmd/server -run '^TestAccountCheck' -count=1 -timeout=90s
go test -race ./cmd/server -run '^TestAccountCheck.*(Concurrent|Cancel)' -count=1 -timeout=90s
go test -race ./cmd/server -run '^TestAccountCheckWorker' -count=1 -timeout=90s
go test ./cmd/server -run '^Test.*(History|Sub2Import)' -count=1 -timeout=90s
go vet ./internal/probe ./internal/store ./cmd/server
node --check cmd/server/account-checks.js
node --check cmd/server/client.js
node cmd/server/account-checks.test.cjs
env GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o /tmp/kkai-auth-account-checks-linux ./cmd/server
```

后续小修只重跑对应范围。race 覆盖真实 worker 并发 1/2、取消后迟到成功、数据库取消/恢复/版本竞争；JS fixture 覆盖跨页 20→25→20、100 上限、稳定 DOM、失败读取、迟到响应、幂等恢复、停止计数。存储测试用损坏 RT 验证检测仍可领取 AT，证明检测没有依赖 RT 解密。

## sys1 真实请求证据

真实浏览器使用系统 Chrome 与缓存的 Playwright 驱动，在本地隔离 Go 服务验证了 25 个账号的 20/5 分页、20→25→20 跨页选择、搜索清空、过期预检、详情、刷新恢复，总创建请求仅 1 次，JavaScript 错误为 0。执行命令为 `GOPROXY=off GOSUMDB=off go run /tmp/kkai-auth-check-ui.go`，退出码 0；临时脚本已清理，截图留在 `/tmp/kkai-auth-check-ui.png`。没有安装新浏览器依赖或用 Mac 出口进行真实模型验收。

所有真实模型调用都在 sys1 运行，Mac 仅负责构建、SSH 和本地隔离 UI fixture。

| 执行路径 | 结果 | 耗时/并发证据 |
| --- | --- | --- |
| 默认代理，指定测试账号，第 1 次最终客户端验证 | HTTP 200，完整 SSE，ok | 1,896 ms |
| 默认代理，同一账号，第 2 次 | HTTP 200，完整 SSE，ok | 1,530 ms |
| sys1 IPv4 直连，同一账号 | HTTP 200，完整 SSE，ok | 1,982 ms |
| 隔离候选 AUTH，两个已导入账号，并发 2 | 两项均 HTTP 200 / ok / current | 3,031 / 4,192 ms；执行窗口重叠 3,035 ms |
| 正式 AUTH，正常账号上线冒烟 | HTTP 200 / ok / current | 3,206 ms |
| 正式 AUTH，另一个账号额度异常 | HTTP 429，业务码 usage_limit_reached，正确归 quota_exhausted | 797 ms；未重复调用该账号 |

候选批次为 `254ac8f677cc1669688ce53e38b73184`。候选使用生产 SQLite 的一致性隔离快照，绑定 `127.0.0.1:18086`，没有连接正式数据库执行启动恢复，也没有运行 Sub2 导入服务。候选新 schema 的另一个隔离副本已用旧版二进制启动，`/health` 正常，确认旧版可忽略新增表/索引。

这组模型请求成功不表示浏览器 OAuth 登录也一定成功；两种网络流程的证据分别记录。有限次抽样不能证明所有账号、所有时间都健康，页面只显示带时间的最近观察。

## 发布状态

已发布 release `20261004T034448Z-account-checks`，回滚版本 `20261003T220628Z-expiry-fix-final`。正式服务 active、NRestarts=0、登录并发硬上限仍为 10；检测服务 available，历史/导入摘要可读，原 Sub2 导入配置仍生效。最终部署事实见 [SYS1_DEPLOYMENT.md](SYS1_DEPLOYMENT.md)。

发布后，原有 OAuth 登录从 sys1 经默认代理完成，耗时 18.93 秒；数据库确认该测试账号为 active，新成功 attempt 已保存且 AT/RT 均存在。初次验收脚本按小写键读取返回值，误输出 token_received=false；实际 LoginResult 使用大写字段名，已通过持久化结果核实，不是 token 丢失。

正式检测正常批次为 `76aca1964d4745f73fa6ba623fde3a13`；429 批次为 `5a4f8f2629cbae478be5ebfac7dbf882`。最初只允许 ok 的冒烟断言因真实 429 退出 1，随后只读核验了持久结果的 HTTP 429、usage_limit_reached、request_attempted=true，分类与计数正确，没有把异常改成成功或重试到变绿。

发布前数据库使用 SQLite backup API 一致性备份，并通过 integrity_check，位置为 `/var/lib/openai-login/backups/accounts-before-20261004T034448Z-account-checks.db`。回滚仅切二进制，不覆盖业务数据库。公网入口未携带平台登录凭据时保持 HTTP 401 认证保护；功能 API 在 sys1 本机验收，该平台认证 401 与账号检测的上游 401 分开。

候选二进制 SHA-256：`ab95fc7193b4c05a5661674aceb9b5f48374a754057974a03418b697f5c7a8a9`。

切换后约 10 分钟再次复核：当前二进制与上述 SHA-256 一致，服务 active、NRestarts=0、检测 available，无活动批次；检测存储错误、panic、登录失败日志均为 0。两个正式验收批次已完成，终态代理密文已清除。候选/回滚测试服务已停止，隔离数据库及服务器临时二进制已删除，回滚版本仍可用。

## 保留边界

- 检测使用 AUTH 当前保存的 AT，不能据此声称查到了 Sub2 当前凭据或调度状态。
- AT 明确过期时显示凭据待更新，不通过自动刷新共享 RT 使检测“变绿”。
- 默认代理失败不回退直连；显式直连固定 IPv4。默认模式配置了前置代理链时明确拒绝，首版没有静默忽略代理链。
- 没有自动重试模型 POST、定时巡检或完整全仓测试；受控异常通过 fixture 验证，没有破坏生产账号来制造 401/407/429。
