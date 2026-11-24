# 订阅账号到期、授权撤销与原绑定处置

## 事故范围

2026-11-23 的历史发布核查记录了 296 个资格变更，其中 58 个属于目标组，238 个超出目标。启动时根据账号表缓存套餐授予资格，使额外账号缺少对应的本次付费观察。这些数字来自 `20261123T110011Z-policy-backfill` 的历史发布核查。

## 处理规则

| 当前证据 | 系统处理 | 操作员可核对的结果 |
|---|---|---|
| AUTH 访问令牌过期、本地凭据缺失或不完整 | 标记为需要核对 Sub2 正常续期，保留原绑定 | 查看原账号的远端状态和最近检测时间。单凭远端状态为 active 不能确认续期成功 |
| 撤销码或缺少更具体原因的 401 | 标记为需要重新授权，普通检测和巡检在待办处停止 | 由账号持有人走官方授权，再刷新历史并核对原绑定。AUTH 的旧检测仍属于当时的凭据版本 |
| 登录或恢复刷新未返回受支持的套餐 | 本地保存新凭据。成功登录缺失套餐时可保留历史套餐用于显示。恢复任务进入人工核实 | 同一版本重启、重试或晚到回调均不能解除人工状态 |
| 明确付费、已有交付意图 | 写入前复核当前版本、目的地、原绑定、远端身份和人工停调状态 | 原绑定写回之后还需通过远端检查，旧页面与重复提交按绑定及凭据版本核对 |
| 明确 Free | 保留本地账号，复用既有付费转 Free 的退池举证规则 | 仅有历史套餐标签不足以清理任何原绑定 |
| 上游明确报告订阅到期、工作区停用、账号删除或停用 | 复用终态事务记录官方事件，原绑定按证据进入清理 | 对照凭据版本和目标身份，清理后读取 404 确认 |
| 裸 HTTP/SSE 402、截断错误、网络失败或限流 | 保留账号和绑定，展示本次异常 | 上游未提供明确官方机器码时，禁止据此判定订阅到期 |

密码拒绝及邮箱验证依原有登录阶段和状态码规则记为本地退役。不同来源的事件保留各自的原因，便于复核。

## 存量纠偏

启动迁移已移除根据缓存套餐授予资格的语句。独立工具在离线 SQLite 副本上生成逐账号预览。输入包括原发布前的一致性备份、该备份的 SHA-256、审核后的额外账号 ID 与排除的目标账号 ID。工具比对实例、账号和关联记录，跳过后来发生变化的账号。应用时需要匹配预览摘要、成功建立独立备份，并将修复结果与回执一起提交。重复执行返回原回执和零写入。[纠偏工具与参数](../scripts/auth-undo-policy-backfill.py)

## 本地验收

使用临时 SQLite、模拟上游及模拟 Sub2。交互式持有人授权、生产续期、真实账号原绑定和存量纠偏都需要生产样本才能验收。本轮未部署或运行全仓测试。以下为本轮通过的定向命令。

```sh
go test ./internal/login ./internal/probe ./internal/store ./cmd/server -run '^Test(AccountStatusForAuthError|DescribeErrorAccountStatus|ProbeClassification|ProbeSSE|ProbeFragmentedStreamAndTruncatedHTTPError|PaymentRequiredIsOfficialDeactivation|LifecycleTerminalCredentialConvergence|RecoveryVerifyErrorClassification|RecoveryVerifyWrappedError|RecoveryProbePaymentRequiredIsOfficialUnavailable|AccountDeactivationConfirmedRecognizesOfficialCodesOnly|OfficialSub2CleanupFence|Sub2RecheckOfficialFailureQueuesCleanup)$' -count=1 -timeout=90s
go test ./internal/store -run 'Test(RequiredActionPrefersExplicitCredentialCodeOverHTTPStatus|LifecycleFreeRules|AccountRecoveryTaskLifecycle|RecoveryCredentialCheckpointIsAtomic|MigrateDoesNotBackfillUnknownPaidPolicy|LifecycleTerminalCredentialConvergence|RecoveryRetryCheckpointNeverAdoptsNewerLogin|RecoveryCheckpointResumeAndCredentialVersionGuard|RecoveryCheckpointSurvivesRestart|RecoveryQueriesStayWithinDestination)' -count=1 -timeout=120s
go test ./internal/store -run '^Test(RecoveryRefreshWaitsForPlanAfterRestart|RecoveryCredentialCheckpointIsAtomic|RecoveryCheckpointResumeAndCredentialVersionGuard|RecoveryCheckpointSurvivesRestart|RecoveryRetryCheckpointNeverAdoptsNewerLogin|LifecycleFreeRules|AccountRecoveryRecheckPersistenceAndSchedule|QueueClaimsRecoverAfterConnectionContention)$' -count=1 -timeout=90s
go test ./cmd/server -run '^Test(Sub2Recovery(ResumedCheckpointRequiresObservedPaidPlan|KeepsUnconfirmedPlanForManualReview|ChangesWorkspaceAndPlan|ResumeUsesCheckpointWithoutRefresh|AutomaticCheckpointRetry|InvalidCheckpointRelogs|DeliveryRetryIgnoresAutomaticSwitch)|PaidManualLoginSupersedesUnconfirmedPlan)$' -count=1 -timeout=90s
go test ./cmd/server -run '^Test(HistoryDeliveryRejectsStaleBindingBeforeLogin|HistoryLoginRejectsIncompleteBindingRequest|HistoryReloginBoundSavesOnlyToAUTH|HistoryReloginDoesNotAdoptChangedBinding|AccountDeliveryFrozenBindingStopsBeforeRemoteWrites|PaidManualLoginSupersedesUnconfirmedPlan|ManualLoginSuccessSupersedesOnlyLoginStage)$' -count=1 -timeout=90s
go test ./cmd/server -run '^TestAccountCheck(RequiredActionAPI|HistorySafeDTO)$' -count=1 -timeout=60s
node cmd/server/client.test.cjs
node cmd/server/account-checks.test.cjs
node --check cmd/server/client.js
node --check cmd/server/account-checks.js
python3 -B scripts/auth-undo-policy-backfill_test.py
git diff --check
```
