# 自动检测与恢复修复记录（2026-11-01）

## 发布状态

修复、定向验证、真实 OAuth 和 sys1 生产验收均已完成。正式 release 已切换并通过即时及 60 秒延迟 gate。用户指定邮箱 `trunk.earthen-97+split42fb6bb24853@icloud.com` 对应 account `1055`；候选与生产 OAuth 均成功，`auto_deliver=false`，仅保存脱敏证据。

- 当前正式版：`20261031T204209Z-recovery-fix`，sys1 `/opt/openai-login/releases/20261031T204209Z-recovery-fix`。回滚版本 `20261031T194600Z-recent-cost` 保留。
- 来源：`33d64a0401599837246f647d4d5fdf554e8a0015` + 当前工作区。原有未提交改动保留；本次未提交/推送代码。
- 二进制 SHA-256：`732ec7cb6e7268d3758fef493ca2cf084917c1076057c3f6312baae4208701ed`。
- SOURCE 指纹：`2a06749ed5c137c720253bdf830038bc5ac9caf22c9ebeedbed376a4c24819b8`。与前版相比变更 21 个源码/测试/发布脚本文件；无删除文件、无 schema 迁移。
- 20:46–20:47 UTC：候选 health/ready、数据库 integrity_check、候选与旧版服务用户 Node/Chrome/Playwright smoke、旧版读候选数据库兼容全部通过。02:14 UTC 候选 OAuth 成功；02:19 UTC 切换；02:20 UTC 延迟验收通过；02:21 UTC 生产 OAuth 成功；02:24 UTC 最终只读复核所有 gate 通过。
- HTML SHA-256：`ea26a3e289e608271c09920c863febf4d6aba1ecbc0f56711607bb29c2dffde2`；JS：`93c0181f9e787b1fee91504912d50de8b6d45b16b88cb2d4337f9627f00bc38f`；候选与本地一致。
- 正式 release 已归档 `SOURCE.json`、`STAGE_SCRIPT.py`、`DEPLOY_SCRIPT.py`、候选/即时/延迟验收、生产 runtime、只读 gate 和 OAuth 脱敏证据；生产备份 `/var/lib/openai-login/backups/accounts-before-20261031T204209Z-recovery-fix.db` 为 0600 且完整，临时上传和候选副本已清理。

## 多角色共同方案与实现

恢复安全、监控/SRE、发布运维三角色先完成设计，再并行实施与交叉复审。复用现有持久任务、退避、互斥和候选发布路径，无新增依赖。

| 问题 | 修复与回归保障 |
| --- | --- |
| 登录期间远端身份/禁用状态变化仍被覆盖 | 写入前重读本地 binding、远端身份、禁用/调度状态、import/recovery marker 与凭据版本；存储读取失败暂停服务，不伪装版本冲突；fresh checkpoint 才能跳过 apply。保留正常 workspace/plan 变更。 |
| 出口短时故障永久人工处理 | 429、503、超时、连接失败保留安全分类和 Retry-After，进入持久退避；407/确定配置问题仍人工处理；仅在绑定出口池内回退。 |
| 前 100 个持续故障导致后续饥饿 | 按持久 LatestTask.ID 最旧优先轮转；101 账号两轮回归通过。 |
| 检测忽略 Retry-After | claim 与 summary 使用 max(30秒, Retry-After)，新凭据不继承旧冷却。跨目标共享同一上游凭据冷却，只展示冷却秒数，不泄漏其他目标的检测详情。 |
| checker 暂停仍显示 running、错误提示一律代理配置 | settings 纳入 checker 未配置/暂停/停止；错误安全分类；服务不可用 503，批次冲突 409。 |
| 切换目标后旧任务错误被另一目标任务阻断 | newer 仅同 destination；legacy 空目标只有唯一绑定才归属，无法确认则保守拒绝；active 仍全账号互斥。 |
| 延迟复检 requires_action 无恢复入口 | 新增 POST `/api/account-recovery/{taskID}/recheck` 和“重新复检”按钮。按精确 task ID、当前目标、目标内最新 completed、requires_action 原子更新；人工暂停/取消/旧任务/重复请求拒绝，worker 执行前后仍复核。 |
| 空 state 列表查询跨目标泄漏 | 明确按 state 判断外层 WHERE，不搜索包含子查询的 SQL 文本。 |
| 发布停服竞态及自动回滚约束 | 计数读取失败阻断、runtime smoke 后再核对、停服后有在途则恢复原服务；切换后不自动切回旧版，遵守既有用户约束。 |

剩余技术边界：Sub2 没有条件写接口，fresh GET 与 POST 之间仍有极短竞态；写后身份/状态验证保留。没有在生产注入失败、批量重新登录或修改 Sub2 服务。

## 精确验证命令

以下最终均通过；完整仓库套件未运行。

```bash
go test ./cmd/server ./internal/store -run 'Test(Sub2Recovery|Sub2Recheck|RecoveryRetry|RecoveryDue|RecoveryQueries|AccountRecovery|Sub2Monitor|AutomaticCheck|ManualCheck|AccountCheck)' -count=1 -timeout=90s
go test ./cmd/server -run '^TestSub2Recheck' -count=1 -timeout=90s
# 新增 binding 用例首次预期409，但 legacy binding 改变后已正确拒绝为404；修正断言后单项重跑通过：
go test ./cmd/server -run '^TestSub2RecheckRetryAPI/binding$' -count=1 -timeout=90s
go test ./internal/store -run 'TestAccountRecoveryRecheck' -count=1 -timeout=90s
go test ./internal/store -run 'TestAccountCheckRetryAfter' -count=1 -timeout=90s
go test ./cmd/server -run 'TestSub2RecoveryRechecks|TestSub2RecoveryFreshRead|TestBoundAccountLoginProxy|TestRecoveryLoginUsesCurrentSub2Egress' -count=1 -timeout=90s
go test ./cmd/server -run 'TestResolveEgressRoutes|TestLoadDefaultLoginDeliveryOptions|TestHistoryDeliveryOptions' -count=1 -timeout=90s
go test -race ./cmd/server ./internal/store -run 'Test(Sub2Monitor|Sub2AutomaticCheck|Sub2ManualCheck|Sub2Recovery|Sub2Recheck|Recovery|AccountRecovery|AccountCheck)' -count=1 -timeout=120s
go vet ./cmd/server ./internal/store
go test ./cmd/server ./internal/store ./internal/exporter -run 'Test(Sub2Monitor|Sub2Recovery|Sub2Recheck|Sub2AutomaticCheck|Sub2ManualCheck|AccountCheck|AccountRecovery|RecoveryRetry|RecoveryQueries|DeletedAccount|RecentAccountCost|Sub2Import|SyncSub2|HistoryLogin|PendingCleanup)' -count=1 -timeout=120s
# 最后 fresh gate 存储错误分类改动及新增故障注入的定向验证：
go test -race ./cmd/server -run '^TestSub2Recovery(Fresh|Rechecks|StorageFailure)' -count=1 -timeout=90s
go vet ./cmd/server
node --check cmd/server/client.js
node cmd/server/client.test.cjs
PYTHONDONTWRITEBYTECODE=1 python3 scripts/auth-deploy-release_test.py
bash -n verify-runtime.sh
git diff --check
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/login -o /tmp/kkai-auth-20261031T204209Z-recovery-fix-smoke.test
./build-linux.sh /tmp/openai-login-20261031T204209Z-recovery-fix
```

Race 范围通过（server 36.546秒、store 7.259秒），最后 fresh gate race 2.814秒；最终相邻范围 server 9.402秒、store 0.423秒，exporter 编译通过、无独立测试文件。Node 包含新按钮实际事件、陈旧 task ID、重复/canceled 重试保护。全量套件和真实 401 账号的完整恢复链路未验收；候选和生产真实账号 OAuth 已通过。

## 后续上线步骤

1. 已完成候选真实 OAuth（account 1055）并留存脱敏证据。
2. 已完成停服前后在途检查、0600 数据库备份和原子切换。
3. 已完成生产服务用户 runtime smoke、生产 OAuth、health/ready/socket/路由/认证/静态资源、自动巡检、错误聚合及 60 秒延迟验收。
4. 已完成最终只读 gate 和精确哈希清理；正式/回滚 release、SOURCE manifest、验收证据和备份保留。
