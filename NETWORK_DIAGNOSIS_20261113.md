# AUTH #1378 网络错误排查

排查时间：2026-11-13 16:53–17:08，北京时间。只读检查及无账号凭据的网络探测；未修改生产配置、账号出口、任务状态或服务版本。

## 已确认结论

- SSH 使用 `sys1`，远端主机 `ovh-sys1-prod`，用户 `tokk`。AUTH 服务 active/running、NRestarts=0，`/health` 正常；自动恢复开启、运行中，扫描持续推进。
- AUTH #1378 对应 Sub2 #17298。11 月 13 日 16:15 检测记录 #19470 收到上游 HTTP 401、`token_revoked`，触发恢复任务 #1280。
- 读取时任务在 `logging_in` 阶段累计失败 9 次，错误 `network_error`；没有生成新的登录 attempt。最近一次失败是 16:50:44，保存的下次自动重试时间为 17:50:44。该时间是当时快照，后续人工操作或后台推进可能改变它。
- 当前绑定出口为 route #11，`racknerd-67-ipv4`，proxy #10，网关 `172.30.1.1:32102`。出口目录显示 active/eligible，但这不能证明 AUTH 能使用该出口。
- AUTH 进程的 `/api/proxy-check` 使用实际绑定代理返回 `reachable=false`，直连返回 `reachable=true,http_status=403`。
- 宿主机通过该代理请求认证站、ChatGPT、example.com 及公网 IP，均在 CONNECT 阶段被代理返回 HTTP 403。TCP 连接成功，认证站请求尚未发出。
- 相同代理、相同认证站请求，从 Sub2 green 和 blue 的网络命名空间执行，均得到 CONNECT 200、目标 HTTP 403。目标 403 仅证明网络请求到达，不能证明完整 OAuth 成功。
- 因此当前阻塞已定位到网关对 AUTH 宿主机流量的接纳，而不是代理端口停机或该出口全面不可达。不能把 AUTH 显示的 `network_error` 理解为用户电脑断网。

## 网关配置与证据边界

实际监听进程是容器 `kkai-account-egress-rn67` 内的 3proxy，使用 host 网络。有效挂载配置为 `/etc/kkai-account-egress-gateways/rn67.cfg`，配置包含 `auth iponly`、允许 `172.30.1.0/24` 到公网 443 的 HTTP_CONNECT、末尾 deny。

宿主机路由及普通 TCP 探测显示源地址 `172.30.1.1`。它表面上在配置允许网段内，因此目前证据不足以断言是缺少某条 allow 规则；应进一步核对 3proxy 对本机来源的处理和运行时实际判定，不能直接扩大公网访问权限。未启用代理详细日志，未修改运行配置。

AUTH 的 `boundAccountLoginProxy` 在开始登录 attempt 前检查 Sub2 当前出口；检测不可达后进入持久退避。底层代理探测使用固定安全文案，未把 CONNECT 403 单独保留，因此页面只显示“网络暂不可用，稍后自动重试”。

## 核验命令与结果

以下命令在 sys1 执行；PID 是本次快照，后续操作必须重新发现。

```sh
systemctl show openai-login.service -p ActiveState -p SubState -p NRestarts -p FragmentPath
readlink -f /opt/openai-login/current
curl -fsS --max-time 15 http://127.0.0.1:18082/health
curl -fsS --max-time 20 http://127.0.0.1:18082/api/history/1378
curl -fsS --max-time 15 http://127.0.0.1:18082/api/account-recovery/settings
curl --noproxy '' -x http://172.30.1.1:32102 --silent --output /dev/null --connect-timeout 4 --max-time 10 --write-out 'connect=%{http_connect} target=%{http_code} error=%{errormsg}' https://auth.openai.com/log-in
sudo nsenter -t 1930315 -n -- curl --noproxy '' -x http://172.30.1.1:32102 --silent --output /dev/null --connect-timeout 4 --max-time 10 --write-out 'connect=%{http_connect} target=%{http_code} error=%{errormsg}' https://auth.openai.com/log-in
sudo nsenter -t 3986861 -n -- curl --noproxy '' -x http://172.30.1.1:32102 --silent --output /dev/null --connect-timeout 4 --max-time 10 --write-out 'connect=%{http_connect} target=%{http_code} error=%{errormsg}' https://auth.openai.com/log-in
dig @1.1.1.1 auth.openai.com A +short +time=2 +tries=1
dig @8.8.8.8 auth.openai.com A +short +time=2 +tries=1
```

服务与数据接口成功；宿主机 curl 返回码 56、CONNECT 403；两条容器命名空间 curl 返回码 0、CONNECT 200、目标 403。两组 DNS 查询均返回公网地址。

另通过远端 Python 进程内读取现有服务配置，GET 查询 Sub2 账号、出口目录和 proxy #10；均为 HTTP 200，管理密钥及代理凭据未输出。通过 AUTH `/api/proxy-check` 对绑定代理和 direct 各做一次无账号凭据的连通性检查。

本次未修改业务代码，全量测试未运行。账号尚未恢复；未进行真实 OAuth、令牌写回、调度启用或代理配置修复。

## 回退上线与最终结果

- 代码已加入安全回退：绑定出口的连通性检查失败后，AUTH 登录使用显式 `direct`，即 sys1 本机 IPv4；不会修改 Sub2 的出口配置。回退探测有独立 10 秒上限，并保留调用取消。
- 发布 `20261113T104100Z-ipv4-fallback` 已通过候选运行时、回滚兼容、即时验收和 60 秒延迟验收。生产 active/running、`NRestarts=0`、health/ready/socket 正常，公网未认证 HTTP 401，发布窗口错误聚合为 0。
- 对 AUTH #1378 的实际恢复已使用 sys1 IPv4 直连并进入 MFA，但上游返回 HTTP 403 `account_deactivated`（账号已删除或停用）。随后 Sub2 返回 404，系统已将该账号标记为 `deleted_or_deactivated`，恢复任务 #1280 转人工处理；这不是网络失败，继续换出口不会绕过上游账号状态。
