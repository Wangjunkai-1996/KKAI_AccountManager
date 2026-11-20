# 重导入旧代理故障与错误可见性修复

## 线上证据与根因

AUTH #780 的 attempt 1595（2026-11-14 11:21:52 北京时间）和 1596（11:22:11）各一秒失败，持久化为 `stage=email / invalid_configuration / 代理配置无效`，并未提交 Sub2 导入。更早 attempt 1592 为 `email_verification_required`，不能据此断言本轮是密码或 TOTP 错误。

脱敏只读检查显示保存的 HTTP 代理格式合法、TCP 可达，但到认证站的 HTTPS CONNECT 隧道失败。直接访问认证站能收到 HTTP 响应；这只是连通性证据，并不证明账号登录成功。`accountLoginProxy` 在非 imported 且未显式选择出口时直接复用历史代理，遗漏了备用 IPv4 探测。`navigateAuthorize` 的错误包装带“代理”关键词，导致未知导航失败被笼统归类为配置错误；OAuth URL 的 scope=email 又可能误导阶段分类。

## 最小修复

- 未绑定或 `remote_missing` 且没有显式 Sub2 出口选择时，先探测保存的代理；连接失败后用独立 10 秒预算探测 `direct`，可达才回退 sys1 IPv4。已连通旧代理继续使用；显式出口选择、当前 imported 绑定路线和自动删除保护维持原行为。407、无效地址及取消不会被当成普通连接失败绕过。探测不会提交账号认证表单；显式登录仍只有一次浏览器尝试。
- 隧道失败归类网络错误；代理证书无效、代理认证失败保持具体配置错误且不重试。未知导航错误不再附加误导性的代理配置提示。
- 对 failed/error 账号，在诊断摘要和更多菜单附加持久化的最近登录错误、错误代码及 HTTP 状态，保留 Sub2 缺失说明。失败后前端锁正常释放，不发送 import。未新增缓存、状态机、schema 或依赖。

产品、后端安全、前端完成独立复审；无阻断项。

## 定向验证

下列命令全部通过；未运行全仓库完整测试套件：

```sh
go test ./cmd/server -run 'Test(AccountLoginProxy|Sub2MissingReimportEndpointRequiresExplicitIntentAndFreshLogin|TestSub2Missing)' -count=1 -timeout=120s
go test ./internal/login -run 'TestDescribeErrorClassifiesChromiumProxyFailuresPrecisely|TestRetryableLoginErrorRecognizesTransientBrowserNetworkFailures' -count=1 -timeout=120s
node --check cmd/server/client.js
node cmd/server/client.test.cjs
git diff --check
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /tmp/kkai-auth-reimport-fix-20261114/openai-login-web ./cmd/server
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go test -c ./internal/login -o /tmp/kkai-auth-reimport-fix-20261114/kkai-auth-login-smoke
```

后端角色另执行 `go test ./internal/login`（包内测试）通过；不是全仓套件。上述 server 正则末项有多余 Test，实际主要覆盖 AccountLoginProxy 和明确重导 API 测试；本轮未改 Sub2 missing 存储门禁，也未重复全部 missing 测试。

## 发布

候选 `20261114T033924Z-reimport-proxy`，二进制 SHA-256 `c937988b52d5395e19d0e84a4475cb3593e71089cc75dcb9a1cb623fd85c3f97`，源码为 `33d64a0401599837246f647d4d5fdf554e8a0015` 加现有工作区。构建前后 127 份源码 hash 一致。候选、旧版运行环境及数据库兼容通过；隔离账号 1581 OAuth 成功（11.579 秒、attempt 1597、auto_deliver=false）。正式切换发生于北京时间 11:45:07；即时与 60 秒延迟 gate 通过。生产账号 1581 OAuth 用时 12.573 秒，attempt 1597 成功，auto_deliver=false，未增加 Sub2 导入。11:48:01 最终检查通过：服务 active/running、NRestarts=0、8 类在途均为 0、发布窗口固定错误聚合全为 0、登录后的巡检已推进至 11:47:10；有效 AUTH 路由为 `/run/tls/auth/login.sock`，公网未认证 401。备份 `/var/lib/openai-login/backups/accounts-before-20261114T033924Z-reimport-proxy.db` 权限 0600、完整性 ok；回滚 `20261114T030748Z-reimport` 的二进制/Node/driver 未变且 runtime 已验证。没有自动回滚。隔离副本和上传目录已清理，正式/回滚产物及 operations、验收 JSON 保留。

本轮不通过测试创建 #780 的新 Sub2 账号；真实重导入创建仍以用户明确操作为准。代理备用路线由定向测试和线上脱敏探测验证，正式真实 OAuth 使用授权验收账号 1581。

发布命令（已通过；上传目录已清理，脚本归档在 release/operations）：

```sh
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/kkai-auth-reimport-fix-20261114/auth-stage-release.py --release-id 20261114T033924Z-reimport-proxy --binary /tmp/kkai-auth-reimport-fix-20261114/openai-login-web --smoke /tmp/kkai-auth-reimport-fix-20261114/kkai-auth-login-smoke --verify /tmp/kkai-auth-reimport-fix-20261114/verify-runtime.sh'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /tmp/kkai-auth-reimport-fix-20261114/auth-deploy-release.py --release-id 20261114T033924Z-reimport-proxy --source-commit "33d64a0401599837246f647d4d5fdf554e8a0015+working-tree" --binary-sha256 c937988b52d5395e19d0e84a4475cb3593e71089cc75dcb9a1cb623fd85c3f97 --delay-seconds 60'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n python3 /opt/openai-login/releases/20261114T033924Z-reimport-proxy/operations/release-final-check.py --release-id 20261114T033924Z-reimport-proxy'
```
