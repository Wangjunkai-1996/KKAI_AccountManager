# 账号自动化与 IPv4 直连验证（2026-10-08）

本轮覆盖批量 Sub2 分组/优先级/并发及平台默认、5/30 分钟持久复检、资料修改后的后台续跑、导入不确定结果核对，以及登录 IPv4 直连。发布事实以 SYS1_DEPLOYMENT.md 为准。

## 定向验证

以下命令均通过，未运行完整测试套件。所有 Go 验证都有 60/90 秒超时，未提交真实账号密码或 TOTP 进行手动 OAuth 验收。

```bash
go test -race ./cmd/server ./internal/store -run 'Test(DeliveryOptions|Sub2ManualImport|Sub2Import|AccountDelivery|RunWithHistory|CredentialRepair|HistoryCredentials|AccountCredential|Sub2Recheck|AccountRecoveryRecheck|Sub2Recovery|Recovery|AccountRecovery)' -count=1 -timeout=90s
go test -race ./internal/store ./cmd/server -run 'Test(CredentialRepair|HistoryCredentialPatch|Sub2Recheck|AccountRecoveryRecheck|Sub2Recovery|Recovery|AccountRecovery|History)' -count=1 -timeout=90s
go test -race ./internal/store ./cmd/server -run 'TestCredentialRepair|TestRecoveryRetry|TestHistoryCredentialPatch' -count=1 -timeout=90s
go test -race ./cmd/server -run 'TestSub2RecheckRecovery' -count=1 -timeout=90s
go test -race ./cmd/server -run '^TestDeliveryRecoveryBindsGroupsOnlyAfterSuccessfulProbe$' -count=1 -timeout=60s
go test ./cmd/server -run 'Test(DeliveryOptionsPreserveGroupsAfterEarlierCompletedDelivery|Sub2ManualImportQueuesWithoutRemoteCallsAndFreezesOptions)$' -count=1 -timeout=60s
go test ./internal/login -run 'Test(DirectIPv4|ProxyRelay|CheckProxyURL|ValidateHTTPProxy|DeadlineCanceledLogin|DeadlineCancelsToken)' -count=1 -timeout=60s
go test -race ./internal/login -run '^TestDirectIPv4' -count=1 -timeout=60s
go test ./cmd/server -run '^TestDirectProxyLoginValidation$' -count=1 -timeout=60s
go vet ./cmd/server ./internal/store ./internal/login
node cmd/server/client.test.cjs
node --check cmd/server/client.js
git diff --check
./build-linux.sh /tmp/openai-login-20261008T080500Z-account-workflows
```

验证包含：首次参数冻结、重试幂等、不触发重复创建、已有账号保留配置、真实 worker 顺序 apply→probe→group→enable、探测失败不入组不启用、超时/429/503退避、重启不重复已成功登录、资料移交和恢复排队的原子事务、敏感字段省略/清除/无回显、人工暂停与凭据版本保护。

前端通过合成 fixture 的桌面及 390×844 手机浏览器验收；密码输入主题已修正。客户端测试覆盖跨 100 个账号分批冻结参数、保存默认失败、缺失分组、秘密留空保留、明确清除、后台资料修复状态、direct 参数、交付终态等待。

## 线上诊断与边界

sys1 默认双栈原生 Chrome 本次实际连接 IPv6 并触发 challenge；分别固定到实时 DNS 两个 IPv4 A 记录的独立原生 Chrome 会话都进入邮箱页。代码通过动态 DNS 的 tcp4 临时转发器约束真实出口，未硬编码 Cloudflare IP。当前代理也能进入邮箱页，服务器默认代理继续保留。详见 SYS1_IPV4_OAUTH_DIAGNOSIS.md。

未覆盖完整直连 OAuth、后续表单与 token 交换的线上成功率、长时间稳定性，以及用新真实账号执行带分组的新建导入。Go 集成测试不能替代这些线上业务证据。

隔离候选使用生产 SQLite 的一致性副本；关闭自动恢复、取消 Sub2 配置、清空候选交付/资料修复任务，未用候选修改 Sub2。候选迁移、三个静态资源 hash、数据库完整性、旧二进制读取迁移后副本、Node/driver 运行时 hash 均通过。
