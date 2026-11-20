# 账号管理实现与发布记录

状态：代码实现、专业角色交叉审查及 sys1 上线验收全部完成。最终验收时间：2026-11-14 00:55:57（北京时间）。

## 实现

- 登录层以一套规范错误码识别邮箱、手机、密码、TOTP 及不明确的验证码/凭据拒绝。立即停止当前重试和后台恢复，保留阶段及安全原因；普通网络、429、5xx 和浏览器 challenge 保留有限退避。
- 账号表展示可信 Sub2 名称及原前缀；套餐保持原始类型，支持与搜索、状态、分页和选择组合筛选。详情按需读取/复制密码或 TOTP 密钥；普通接口不返回秘密，迟到响应不能写到其他账号详情。
- Sub2 已确认缺失保存 `remote_missing`，终止旧检测、恢复、交付、修复及复检；缺失只停止该关联，不误删 AUTH 资料。历史无绑定的旧交付不获得创建权限。
- 新成功版本只覆盖旧的确定性登录拒绝；新资料交接绑定具体恢复任务并原子消费一次。Sub2 缺失、身份冲突、分组和调度暂停不被绕过。
- 未新增依赖或队列。对已有 delivery 表增加创建许可/operation，对已有 repair 表增加 task ID，旧行默认不获新授权；其余复用现有状态、lease 和事务。

## 专业角色审查

文档先由产品、后端安全和前端交叉审查后进入实现；实现再交叉复审。已修复：旧 404 误伤新绑定、迟到任务覆盖终态、旧交付重建账号、新成功仍被旧失败遮盖、新资料无法继续、跨账号 reveal 迟响应、复制失败残留秘密，以及不可信 404 的官方清理分支。

首个隔离候选 `20261113T160925Z-account-management` 的真实登录验收发现 TOTP 识别遗漏，已拒绝上线。实际 MFA 页面使用 `Check your authenticator app` 与 `Enter the one-time authentication code shown in your app to verify your identity.`，原 fixture 未覆盖这组提示；密码验证已通过，但 TOTP 未被提交。修复仅按真实页面证据补充识别并覆盖浏览器提交流程，不将未知 MFA 一律视作 TOTP。生产旧版本未受影响。验收脚本同时修正 SSE 协议解析，并要求本轮新增成功 attempt，避免以旧 token 误判成功。

最终 DOM 修正由独立产品角色复审通过：从实际可见文本节点读取当前挑战提示，排除备用链接、按钮、隐藏内容和脚本；不对整页文字替换，因此 `Verify`、`Email`、`Authenticator` 短按钮不再破坏正文。检测与填写共用可见 `code/otp` 输入框；真实文案、表单外提示、延迟 11 秒 MFA 与 otp-only 输入框均有浏览器回归，实际断言只提交一次六位验证码。拒绝候选的脱敏证据见 `evidence/rejected-candidate/`。

## 本地验证

以下命令为实际执行记录；全仓完整测试套件未运行。

```sh
go test ./internal/store -run '^TestCredentialFields$' -count=1 -timeout=60s
go test ./cmd/server -run 'Test(HistoryCredentialReveal|LoginAutomationDeterministicFailures|RecoveryLoginHTTP4xx|RecoveryAuthPageTimeout)' -count=1 -timeout=90s
go test ./internal/store ./cmd/server -run 'Test(CredentialRepair|LoginAutomation)' -count=1 -timeout=90s
go test ./internal/login -run 'Test(VerificationError|VerificationPageEvidence|VerificationWait|VerificationHTTP|DescribeError|AuthPageWait|AuthResponse|AuthStage|LoginWithRetries|DeadlineInterruptsCallbackAndRetryDelay)' -count=1 -timeout=90s
PLAYWRIGHT_NODEJS_PATH=/Users/tokk/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node OPENAI_LOGIN_BROWSER_TEST=1 go test ./internal/login -run 'Test(VerificationPagesBrowser|AccountRejectionPageBrowser|ObserveAuthResponsesExtractsNestedMFAJSON|WaitOAuthCallback.*Browser)' -count=1 -timeout=90s
go test ./internal/store -run 'Test(Sub2Missing|AccountDelivery|QueueAccountDelivery|AccountCredentialVersionDTO|DeletedAccount)' -count=1 -timeout=120s
go test ./cmd/server -run 'Test(Sub2Missing|Sub2Import|Sub2NotFound|ListSub2Account|SyncAccount|Sub2Account|AccountDelivery|RecentAccountCost|DeleteDeletedAccount|PendingCleanup|HistoryCredentialReveal)' -count=1 -timeout=120s
go test -race ./internal/store ./cmd/server -run 'Test(Sub2Missing|LoginAutomation|CredentialRepair|HistoryCredentialReveal|AccountDelivery|QueueAccountDelivery|AccountCredentialVersionDTO|Sub2Monitor|Sub2Recovery|RecoveryAuthPageTimeout|RecoveryLoginHTTP4xx|RunWithHistory)' -count=1 -timeout=180s
go test -race ./internal/store ./cmd/server -run 'Test(Sub2Missing|AccountDelivery|QueueAccountDelivery|DeletedAccount|DeleteDeletedAccount|PendingCleanup)' -count=1 -timeout=120s
go test -race ./cmd/server -run 'Test(Sub2MissingUntrustedCleanup404|Sub2NotFound|DeleteDeletedAccount|PendingCleanup)' -count=1 -timeout=90s
go vet ./internal/login ./internal/store ./cmd/server
node --check cmd/server/client.js
node cmd/server/client.test.cjs
node --check cmd/server/account-checks.js
node cmd/server/account-checks.test.cjs
python3 scripts/auth-deploy-release_test.py
bash -n verify-runtime.sh
git diff --check
go test ./internal/login -run 'Test(VerificationPageEvidence|VerificationWaitStopsBeforeTimeoutOrTOTP|VerificationHTTPKeepsStatusAndEvidence|VerificationErrorClassification)$' -count=1 -timeout=90s
PLAYWRIGHT_NODEJS_PATH=/Users/tokk/.cache/codex-runtimes/codex-primary-runtime/dependencies/node/bin/node OPENAI_LOGIN_BROWSER_TEST=1 go test ./internal/login -run 'Test(VerificationPagesBrowser|WaitOAuthCallbackDelayedMFABrowser|WaitOAuthCallbackOTPInputBrowser)$' -count=1 -timeout=90s
go test ./internal/login -run 'Test(Verification|WaitOAuthCallback|AuthPageWait|AuthResponse|AuthStage|DescribeError|LoginWithRetries|DeadlineInterruptsCallbackAndRetryDelay)' -count=1 -timeout=90s
go vet ./internal/login
python3 -m py_compile /tmp/kkai-auth-account-management-20261113/release-feature-check.py
python3 /tmp/kkai-auth-account-management-20261113/release-feature-check.py --self-test
```

以上最终执行均退出 0。首次整合测试发现新测试 fixture 错用了不存在的 `requires_action` SQL 列，已改为真实的 `manual_action` 字段后通过；浏览器测试首次因缺少默认 Node 路径失败，指定已有 bundled Node 后通过，无依赖下载。广义定向竞态测试耗时 Store 4.773 秒、Server 41.319 秒，最终缺失/交付竞态回归 4.176/5.043 秒。

最终 TOTP 分类测试 0.314 秒、浏览器回归 21.235 秒、整合登录定向回归 2.441 秒；最终登录包 vet 通过。验收脚本自检覆盖 JSON 转义、公开元数据碰撞、Nginx server 边界、名称来源与旧任务归属；SSE 解析离线检查覆盖成功、失败及早期 JSON 拒绝且不保存 token。

前端合成数据浏览器验证覆盖 1440px 和 390px，未出现横向溢出；长名称换行、字段请求、关闭详情后迟到请求不复制均通过。真实上游页面变体仍有未知性：证据不足时保留未知原因，不猜成具体验证方式。

## 发布基线

发布前，sys1（SSH 用户 `tokk`，主机 `ovh-sys1-prod`）只读确认为 `20261113T104100Z-ipv4-fallback`，服务身份 `openai-login`，active/running、NRestarts=0。连续检查未发现运行中的登录/交付/检测，最近 60 秒无新登录。有效入口为 `http://unix:/run/tls/auth/login.sock:`。

已按 AUTH systemd 发布流程完成新目录、隔离候选、服务身份浏览器检查、单账号 OAuth、SQLite 一致性备份、原子版本切换、正式接口和巡检后的延迟验收，全部通过。用户已明确授权上线。

纯历史删除证据且旧 binding 已完全清除的账号保持停止；当前显式重导入口要求仍有 `remote_missing` binding，不会为这类旧账号偷偷恢复关联。未执行生产删号或为测试创建 Sub2 账号。

## 最终构建与验收命令

最终候选 `20261113T163828Z-account-management`；源码 `33d64a0401599837246f647d4d5fdf554e8a0015` 加工作区改动，未提交/推送。源码文件散列记录于 release 的 `SOURCE.json`，构建前后核对一致。Linux amd64 二进制 SHA-256：`e308b483a03821d7401c64153e0474f91b1b3bb9a2cc6401144297d64a95afa0`；匹配 smoke SHA-256：`2e4957cc183bca24b289a0a6353bcf97a80aeca8c30e70674f49e1a60c9df926`。

本地构建（均退出 0）：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/kkai-auth-account-management-20261113/final/openai-login-web ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/login -o /tmp/kkai-auth-account-management-20261113/final/kkai-auth-login-smoke
```

以下命令在 sys1 以 `sudo -n` 执行，SSH 使用 `ssh -o BatchMode=yes -o ConnectTimeout=8 sys1`，无代码托管网络操作：

```sh
python3 /tmp/kkai-auth-20261113T163828Z-account-management/auth-stage-release.py --release-id 20261113T163828Z-account-management --binary /tmp/kkai-auth-20261113T163828Z-account-management/openai-login-web --smoke /tmp/kkai-auth-20261113T163828Z-account-management/kkai-auth-login-smoke --verify /tmp/kkai-auth-20261113T163828Z-account-management/verify-runtime.sh
python3 /tmp/kkai-auth-20261113T163828Z-account-management/release-smoke.py --release-id 20261113T163828Z-account-management --phase candidate --account-id 1581
python3 /tmp/kkai-auth-20261113T163828Z-account-management/auth-deploy-release.py --release-id 20261113T163828Z-account-management --source-commit 33d64a0401599837246f647d4d5fdf554e8a0015 --binary-sha256 e308b483a03821d7401c64153e0474f91b1b3bb9a2cc6401144297d64a95afa0 --delay-seconds 60
python3 /tmp/kkai-auth-20261113T163828Z-account-management/release-smoke.py --release-id 20261113T163828Z-account-management --phase production --account-id 1581
python3 /tmp/kkai-auth-20261113T163828Z-account-management/release-feature-check.py --release-id 20261113T163828Z-account-management --account-id 1581
python3 /tmp/kkai-auth-20261113T163828Z-account-management/release-final-check.py --release-id 20261113T163828Z-account-management
```

上述命令最终均退出 0。操作脚本已归档至 `/opt/openai-login/releases/20261113T163828Z-account-management/operations/`；以上是本次实际执行路径，临时上传目录已清理。

## 正式验收结果

- 16:52:22 UTC 开始切换，00:55:57 北京时间最终验收通过。当前实际二进制、SOURCE 散列与候选一致；服务 `active/running`、`NRestarts=0`，数据库与备份完整性均 `ok`。
- 候选真实 OAuth 12.130 秒、正式 12.467 秒，均通过密码、TOTP、授权与 token 保存，核对本轮新成功 attempt；`auto_deliver=false`，Sub2 import 数量不变。凭据内容没有输出或写入证据。
- 正式账号表读到 77 个可信自定义名称；所有记录包含合法凭据版本。单字段密码/TOTP reveal 成功；普通列表和详情不含秘密字段或已揭示值；不支持字段返回 400、跨站返回 403、公网未认证 reveal 返回 401。
- 257 个 Sub2 缺失关联已持久化 `remote_missing`，相关活跃恢复及旧 destination 任务均为 0；2 个未知状态不猜成删除。有效 AUTH 路由仍为 Unix socket。
- 正式登录之后的后台巡检于 16:55:31 UTC 推进。固定发布窗口错误聚合（panic、fatal、存储、数据库、监听、浏览器权限、登录失败、OAuth 401/403/5xx）全部为 0，8 类在途任务最终均为 0。
- 回滚 release `20261113T104100Z-ipv4-fallback` 的 binary/Node/driver 散列未变、服务身份运行环境已验证。备份 `/var/lib/openai-login/backups/accounts-before-20261113T163828Z-account-management.db` 保留，0600、完整性 `ok`。新旧候选的隔离凭据副本和两个上传目录已清理；正式版本、回滚版本、被拒候选证据及操作脚本保留。

脱敏证据保存在 [evidence/20261113T163828Z-account-management](evidence/20261113T163828Z-account-management/)，关键结果为 [FINAL_ACCEPTANCE.json](evidence/20261113T163828Z-account-management/FINAL_ACCEPTANCE.json)、[FEATURE_ACCEPTANCE.json](evidence/20261113T163828Z-account-management/FEATURE_ACCEPTANCE.json) 和 [CLEANUP.json](evidence/20261113T163828Z-account-management/CLEANUP.json)。

未运行全仓完整测试套件；没有在生产故意制造删号、错误验证码或并发删除竞争，这些由定向 fixture/race 验证。未知的新上游页面仍保守记录未知原因。纯历史删除事件且 binding 已清除的旧账号继续安全阻断，当前显式重导入口不直接解除这种记录。未提交或推送源码。
