# 2026-11-13 四个账号的登录失败定位

本轮只读检查 sys1（ovh-sys1-prod，SSH 用户 tokk）的生产 journal 与 SQLite，并对照本地登录流程。未重新登录、修改生产数据、删除账号或发布代码。时间除命令明确写 UTC 外，均为北京时间。

## 结论

| AUTH / Sub2 | 账号 | 证据与实际阻塞 |
| --- | --- | --- |
| 780 / 17075 | nock.acetic.4t@icloud.com | 18:53:07 密码验证 HTTP 200；18:53:08 MFA 验证 HTTP 200；18:53:09 进入邮箱验证码页面；18:56:03 总超时 |
| 799 / 17099 | smarts_topping.5r+split603e9b3f68db@icloud.com | 19:18:12 密码验证 HTTP 200；19:18:14 MFA 验证 HTTP 200，随后进入邮箱验证码页面；19:21:08 总超时 |
| 992 / 17180 | litchi-pouters.69+bzdznmag@icloud.com | 19:41:59 密码验证 HTTP 200；19:42:01 MFA 验证 HTTP 200；19:42:02 进入邮箱验证码页面；19:44:55 总超时 |
| 1510 / 17337 | cherryopuo@gmail.com | attempt 1514：13:48:14 在 mfa 阶段返回 HTTP 403、incorrect_code；此前 attempt 1511 于 11:55:12 登录成功 |

三个超时账号的 journal 均明确记录回退 sys1 本机 IPv4，并出现同一流程：

1. POST /api/accounts/password/verify → HTTP 200。
2. 输入 TOTP，POST /api/accounts/mfa/verify → HTTP 200。
3. GET /api/accounts/email-otp/send → HTTP 302。
4. GET /email-verification → HTTP 200。
5. 等待不到 OAuth 回调，记录 unexpected_auth_page；内层重试两次仍进入相同页面，最后耗尽 180 秒总预算，数据库只保存 timeout / browser。

因此这三次的阻塞是 **TOTP 通过后的额外邮箱验证码验证**。所检查窗口未出现手机号或短信验证证据，也没有这三个账号的 TOTP 拒绝结果。

数据库时间交叉核对确认三个窗口各只有对应的一个登录 attempt（1537、1554、1562）。799 的窗口起始秒在其出口回退标记之前有上一个账号的 account_deactivated 日志；该条不属于 799，不作为其删号证据。

## 代码为何只显示超时

`internal/login/service.go` 的 `waitOAuthCallbackContext` 处理 TOTP 页面 `/mfa-challenge/` 和 Codex consent 页面，没有 `/email-verification` 的识别、接码或提交分支。邮箱验证码页面因此被留在等待循环中。

`internal/login/errors.go` 的 `safeLoginMessage` 将总 deadline 统一显示为“登录请求超时”，导致最终状态没有呈现邮箱验证这一具体原因。当前自动恢复将耗尽重试预算的页面等待转为人工处理。

正确处理方向是把明确的邮箱验证页面单独识别为“需要邮箱验证码”，停止无效重登，并提供受控的验证码输入/继续机制；不能靠 TOTP 替代邮箱验证码，也不能把这种失败当成官方删号。

## 证据边界

- cherryopuo 的对应历史 journal 已无输出；SQLite 保留明确的 mfa / incorrect_code / HTTP 403。无法进一步确认是 TOTP 秘钥变化、提交时间窗问题，还是验证方式不匹配。
- 日志证明进入了邮箱验证码流程，不证明验证码邮件已实际送达，也不能证明官方为何要求额外验证。
- 本轮未查看邮箱、获取验证码或重新发起登录；三个账号是否能在完成邮箱验证后成功取得新令牌，尚未验证。

## 核验命令与结果

```sh
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n journalctl -u openai-login.service --since "2026-11-13 10:52:40 UTC" --until "2026-11-13 10:56:20 UTC" --no-pager -o short-iso'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n journalctl -u openai-login.service --since "2026-11-13 11:18:08 UTC" --until "2026-11-13 11:21:08 UTC" --no-pager -o short-iso'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n journalctl -u openai-login.service --since "2026-11-13 11:41:55 UTC" --until "2026-11-13 11:44:55 UTC" --no-pager -o short-iso'
ssh -o BatchMode=yes -o ConnectTimeout=8 sys1 'sudo -n journalctl -u openai-login.service --since "2026-11-13 05:48:06 UTC" --until "2026-11-13 05:48:15 UTC" --no-pager -o short-iso'
rg -n 'email-verification|email-otp|phone-verification|phone_number|sms|邮箱验证|手机验证|短信|IMAP|imap' internal cmd/server --glob '!*.sum'
```

前三个 journal 查询成功，证据如上；第四个无历史条目。源码检索仅命中前端通用“邮箱验证”阶段标签，没有邮箱 OTP 或手机接码流程。另使用 sys1 Python sqlite3，以 `mode=ro` 打开 `/var/lib/openai-login/data/accounts.db`，核对上述 attempt、错误代码及时间窗内的重叠任务。

本轮仅新增诊断文档；没有业务代码变更，未运行定向或全量测试。剩余验证缺口为 cherryopuo 的具体验证码拒绝根因及三个邮箱验证完成后的登录结果。
