# sys1 OAuth 直连诊断与最小修正

诊断时间：2026-10-08 07:39–07:57 UTC（15:39–15:57 上海时间）。
目标：判断 KKAI_AUTH 在 sys1 的 OAuth 初始登录页是否必须经外部代理；未提交任何账号凭据、未完成 OAuth、未操作 CAPTCHA。

## 结论

当前证据不支持“sys1 的 IPv4 不能直连 OpenAI OAuth”。默认双栈原生 Chrome 实際选择 IPv6 并收到 Cloudflare challenge；两个独立临时 profile 分别使用实时 DNS 的两个 IPv4 A 记录，都进入 OpenAI 邮箱登录页。当前外部代理也能进入邮箱页。

初始诊断仅覆盖邮箱页。用户随后要求移除默认代理，2026-10-08 08:30 UTC 已切换默认 IPv4；08:46 UTC 使用用户授权的历史账号完成真实 OAuth，详见下节。单次完整成功不代表长期成功率或永久免 challenge。

## 默认 IPv4 真实账号实测（2026-10-08 08:42–08:48 UTC）

- 通过生产服务 `POST /api/history/login` 发起，`Content-Type: application/json`、`Accept: text/event-stream`，请求体 `{"account_id":279,"proxy":"","auto_deliver":false}`。账号此前状态 active、最近登录成功、保存的代理为空且无在途任务；密码/TOTP 由服务端读取，未导出凭据。
- 账号 279 于 08:46:12.521–08:46:19.745 UTC（北京时间 16:46）完成浏览器、授权页、邮箱、密码、MFA、授权确认及 token 交换，总耗时 7,224ms，返回 AT/RT 均非空。阶段耗时：browser 1,120ms、authorize 830ms、email 284ms、password 1,224ms、oauth 755ms、mfa 1,747ms、consent 881ms、token 375ms。
- 数据库 attempt 290 为 success，账号仍 active，last_success_at 更新为 08:46:19 UTC，加密 AT/RT 均已保存，账号代理仍为空。数据库按秒记录的 duration_ms 为 7,000，精确耗时来自 SSE 客户端。
- 首选账号 278（此前 active、成功登录、代理为空）两次分别耗时 4,118ms、5,486ms，均通过授权页和密码，在 MFA 返回 `incorrect_code` / HTTP403（attempt 288/289）。未获取新 token；历史页如实保留 failed。服务器 chrony 同步正常、误差低于 1ms；第二次已换验证码时间窗。具体是 TOTP 资料变化还是验证方式不同仍未确认，不能归因于 IPv4。
- 独立日志核验：成功窗口 IPv4 直连、原生 Chrome、MFA、授权确认、token 交换均各 1 次；外部代理回退、前置代理、challenge、结构化错误均为 0。08:47:55 UTC 业务及诊断 Chrome/profile 无残留，`GET /health` 返回 HTTP200 / ok。
- 三次都使用空代理的生产默认路线，无外部代理回退。未自动交付 Sub2；08:48:04 UTC 数据仍为 accounts 53、recovery tasks 58、deliveries/rechecks/repairs 均 0，`GET /health` 为 ok。
- 此轮是生产业务实测及只读核验，没有代码修改、构建、部署或完整测试套件。未验证新账号分组交付、长期成功率，也未因这次成功宣称账号 278 的 MFA 问题已解决。

## 实时基线

- SSH alias：`sys1`；主机 `ovh-sys1-prod`；SSH 用户 `tokk`。
- 诊断时 release：`/opt/openai-login/releases/20261008T053100Z-account-automation`。
- 系统：UTC，`NTPSynchronized=yes`。
- Google Chrome：154.0.8037.97；Node：v24.19.0；playwright-core：1.62.1。
- 有效服务配置：`headless=false`，`browser-compat=false`，`max-concurrent=10`，已配置认证 HTTP 代理。
- 实际公网 IPv4：`51.81.109.154`；IPv6：`2604:2dc0:100:1f9a::`；代理出口：`45.39.200.204`。
- `auth.openai.com` A：`104.18.41.241`、`172.64.146.15`；AAAA：`2606:4700:4400::6812:29f1`、`2a06:98c1:310c::ac40:920f`。
- 浏览器原生 UA：Chrome 154 Linux；语言 en-US/en。生产 compatibility=false，不使用历史 Mac Chrome 131 UA。

## 对照证据

所有 OAuth 测试均使用合法初始 authorize 参数、随机 state 和 PKCE；仅输出 host/path、状态、challenge 标志及 Ray 散列/节点，不保存完整随机 URL、Cookie 或凭据。每组比较复用同一随机 OAuth 初始 URL。

| UTC | 路径 | 结果 |
| --- | --- | --- |
| 07:42 | curl IPv4 直连 | HTTPS 正常可达，OAuth 403、cf-mitigated=challenge、IAD |
| 07:42 | curl IPv6 直连 | HTTPS 正常可达，OAuth 403、cf-mitigated=challenge、IAD |
| 07:42 | curl 当前代理 | CONNECT 成功，OAuth 403、cf-mitigated=challenge、FRA |
| 07:44 | 临时原生 Chrome，默认双栈，CDP port=0 | OAuth 403 challenge，25 秒后仍 Just a moment，无邮箱输入；webdriver=true |
| 07:44 | 临时标准 Playwright Chrome，当前代理 | authorize 302 → /log-in 200；邮箱输入=1；sentinel frame 200；webdriver=true |
| 07:46 | 临时原生 Chrome，非零 CDP/noDefaults，默认双栈 | 实际远端 IPv6 `[2606:4700:4400::6812:29f1]:443`；403 challenge；25 秒后仍无邮箱输入；webdriver=false |
| 07:46 | 相同原生配置，IPv4 `104.18.41.241` | authorize 302 → /log-in 200；邮箱输入=1；webdriver=false |
| 07:56 | 新临时 profile，相同原生配置，IPv4 `172.64.146.15` | authorize 302 → /log-in 200；邮箱输入=1；webdriver=false |

curl 三条路径均返回 challenge，不能单凭 curl 推断浏览器会失败。正常代理 Chrome 使用 webdriver=true 仍进入邮箱页，因此没有依据把失败归因于单一 webdriver 属性。默认 IPv6 与 IPv4 对照是更明确的路径差异；Cloudflare 的内部风险判定不可见。

实验中的固定 A 记录仅用于控制变量，实现没有硬编码这些地址。全部 profile 属于本次临时目录；未访问生产 profile，未修改系统代理、DNS、IPv6 或 Cloudflare 防护。

## IPv4 直连实现

- `internal/login/direct_ipv4.go`：每次直连登录创建仅绑定 127.0.0.1 的临时 HTTPS CONNECT 转发器，使用 Go 标准库 `DialContext(..., "tcp4", hostname:port)`，按连接解析 DNS，无外部代理和固定 IP。
- 浏览器和 token 交换使用同一个临时入口；保留原生 Chrome 的非零 CDP/noDefaults 路径；会话完成或取消关闭监听、活动连接和处理 goroutine。
- 请求 `proxy: "direct"` 显式清除该次继承的出口及前置代理；请求 proxy 留空仍继承原服务配置。服务本身未配置代理时也采用 IPv4 直连。
- `direct` 只允许作为登录出口选择，不能作为 upstream；本次显式 direct 与 upstream 同时提交时拒绝。
- 网络测试直连也使用 tcp4，但 HTTP 403 仍仅报告网络可达，不代表 OAuth 成功。
- 保留明确填写认证 HTTP 代理的能力；服务器默认代理已于 08:30 UTC 按用户要求移除。

## 验证

- `go test ./internal/login -run 'Test(DirectIPv4|ProxyRelay|CheckProxyURL|ValidateHTTPProxy|DeadlineCanceledLogin|DeadlineCancelsToken)' -count=1 -timeout=60s`：通过。
- `go test -race ./internal/login -run '^TestDirectIPv4' -count=1 -timeout=60s`：通过。
- `go test ./cmd/server -run '^TestDirectProxyLoginValidation$' -count=1 -timeout=60s`：通过（首次并行文件未完成导致编译阻断，相关模块修复后仅重跑该项）。
- `git diff --check`：通过。
- 独立代码审查通过；远端确认本次临时 profile 和 Chrome 进程残留均为 0。
- 诊断阶段未运行全套、未部署、未提交真实凭据；实现随后随 `20261008T080500Z-account-workflows` 上线，发布证据见 SYS1_DEPLOYMENT.md。
- 当前验证缺口：后续 API 与 token 的线上直连成功率、长期稳定性和完整浏览器回归；完整 OAuth 已由上方 08:46 UTC 的历史账号实测补齐。
