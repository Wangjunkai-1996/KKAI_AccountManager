# 认证页面超时导致复活停滞：审查与修复

## 结论与方案

登录流程、恢复调度、存量数据三个角色独立审查，另由第四个角色复审实现。根因是 `unexpected_auth_page` 同时包含页面等待超时和页面结构不兼容，却一律归为 `login_required/manual`，不设置下次重试时间。

1. 仅输入框等待、OAuth 回调等待超时添加 `ErrAuthPageTimeout` 标记，复用原有本地重试；父请求超时也保留这个原因，取消保持取消语义。
2. 恢复层仅对可重试的页面等待错误使用 `auth_page_timeout`，复用 5/10 分钟退避。同一任务总失败次数达到 3 时，本轮页面等待失败转 `auth_page_timeout_exhausted/manual`。这个预算包含任务此前其他失败，采用保守上限，不新增计数或 schema。
3. 密码/MFA、账号删除/停用、明确认证 4xx、非临时页面结构错误继续人工处理。总超时不能抹去页面等待原因、绕过次数限制。
4. 存量只按指定账号与任务重分类，默认 dry-run。单事务校验最新任务、最新失败登录记录、原成功凭据版本、当前目标及 Sub2 绑定、账号未被更新和剩余预算；仅设置 `relogin` 与到期时间，不直接进入 queued。后续仍由原调度器验证远端身份、调度状态、检查点及凭据版本。

## 存量核对

- AUTH 842 / task 654 / Sub2 17133：2026-10-30 10:27（北京时间）首次复活报 `unexpected_auth_page`，随后为 `manual`，无重试时间。成功凭据版本 842、失败 attempt 918；当前绑定未变，dry-run 通过。
- AUTH 317 / task 115：此前已有 25 轮失败，且从未成功登录。保持人工处理，不批量释放。
- 历史记录没有保存页面细分原因，所以对 842 是经过校验的一次恢复尝试，不能断言原故障必为加载超时或保证恢复成功。

## 验证

定向 race 与 vet 通过，存量脚本的通过/拒绝/重复执行测试通过；完整仓库套件未运行。交叉复审发现并修复了两条总超时绕过预算的路径，新增无上游访问的页面等待分支测试。

```bash
go test -race ./internal/login ./cmd/server -run 'TestAuthPageWait|TestDescribeErrorDistinguishesAuthPageWaitFromTerminalPage|TestRecovery(LoginHTTP4xxRequiresAction|AuthPageTimeout.*|RetryBackoff)$|TestLoginWithRetries(KeepsTerminalFailuresTerminal|DoesNotReplayDeterministicFailures)' -count=1 -timeout=90s
go test -race ./cmd/server -run 'TestRecovery(LoginHTTP4xxRequiresAction|AuthPageTimeout.*|RetryBackoff)$|TestSub2Recovery(AutomaticCheckpointRetry|RetryRejectsChangedOwnership|TemporaryLoginFailureRetriesBeyondThree|DeliveryRetryIgnoresAutomaticSwitch)' -count=1 -timeout=90s
go vet ./internal/login ./cmd/server
PYTHONDONTWRITEBYTECODE=1 python3 scripts/auth-reclassify-page-timeout_test.py
PYTHONDONTWRITEBYTECODE=1 python3 scripts/auth-deploy-release_test.py
git diff --check
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/login -o /tmp/kkai-auth-page-timeout-smoke.test
./build-linux.sh /tmp/openai-login-20261101T031600Z-auth-page-timeout
```

## 发布状态

已上线 `20261101T031600Z-auth-page-timeout`，前一版 `20261031T204209Z-recovery-fix` 保留。源码为本地 `33d64a0401599837246f647d4d5fdf554e8a0015` 加工作区；未提交/推送。二进制 SHA-256 为 `cb4b6477dbcc55dc7c87ac509fa546d68bfd0c35136e414b3ed7128a89cb4069`，SOURCE 指纹 `8dbb8a067e1c131d2d66309ce8677bdcde29f7a74db198b0d17e2e269de40ea7`。

只发布 AUTH，不修改 Sub2 代码或出口配置。切换前后核对在途任务，不自动回滚。2026-11-01 11:46:15（北京时间）延迟验收通过；11:49:03 重分类 #842，11:49:30 登录成功（attempt 1103），11:49:33 恢复任务完成。11:51:42 只读确认 Sub2 17133 为 active、schedulable=true，恢复 marker 为 task 654/version 1103，出口仍 route 199。AUTH 状态 active、错误已清空。首轮延迟复检按原策略安排在 12:19:33，尚未到期；即时 Sub2 检测已经通过。#317 的 task115/25 次失败/更新时间未变化。


生产切换前后八类在途任务均为 0，备份 `/var/lib/openai-login/backups/accounts-before-20261101T031600Z-auth-page-timeout.db` 为 0600、integrity_check=ok。候选/前一版兼容、服务用户 Node/Playwright/Chrome smoke、即时和 60 秒延迟检查通过；服务 active、NRestarts=0、health/ready/socket 正常、公网未认证 401、有效 AUTH 路由 `/run/tls/auth/login.sock`。03:51:42 UTC 读取发布以来 panic/fatal/数据库锁/存储/监听/浏览器权限/登录失败/OAuth 401/403/5xx 聚合为 0，巡检继续。真实恢复只针对用户要求修复的 #842，没有额外账号登录或 Sub2 出口修改。

精确发布命令（远端临时上传随后清理，以下为执行记录）：

```bash
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-stage-release.py --release-id 20261101T031600Z-auth-page-timeout --binary /tmp/openai-login-20261101T031600Z-auth-page-timeout --smoke /tmp/kkai-auth-page-timeout-smoke.test --verify /tmp/verify-runtime.sh'
ssh -o BatchMode=yes -o ConnectTimeout=8 -o ServerAliveInterval=10 -o ServerAliveCountMax=3 sys1 'sudo -n python3 /tmp/auth-deploy-release.py --release-id 20261101T031600Z-auth-page-timeout --source-commit 33d64a0401599837246f647d4d5fdf554e8a0015+working-tree --binary-sha256 cb4b6477dbcc55dc7c87ac509fa546d68bfd0c35136e414b3ed7128a89cb4069 --delay-seconds 60'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /opt/openai-login/current/RECLASSIFY_SCRIPT.py --db /var/lib/openai-login/data/accounts.db --account-id 842 --task-id 654 --sub2-id 17133 --destination https://lumen-d7e11.kkrich.ltd/api/v1#mac-auth'
```

预览通过后在远端 Python 中调用同脚本 `reclassify(..., apply=True)`，确认当前 release 和延迟验收后执行，结果留存 `RECLASSIFY_RESULT.json`。随后只读核对 `ACCOUNT_842_RECOVERY_ACCEPTANCE.json`。候选、正式与前一版运行时、SOURCE、路由、即时/延迟验收和重分类脚本均归档到正式 release。
