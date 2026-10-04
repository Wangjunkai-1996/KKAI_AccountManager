# sys1 线上部署说明

这份文件是 `KKAI_AUTH` 的 sys1 线上部署记录。账号密码、TOTP、代理凭据和 access/refresh token 不写入文档。

> 这是 sys1 线上事实的唯一权威文档。新对话先读取 `DOCS_INDEX.md` 和 `NEW_CHAT_CONTEXT.md`，发生冲突时以本文的服务、端口、release、健康检查和验收结论为准。

## 线上架构

- SSH 别名：`sys1`
- 公网入口：`https://auth.kkrich.ltd`
- 服务：`openai-login.service`
- 服务用户：`openai-login`
- 本机监听：`127.0.0.1:18082`
- 浏览器：Google Chrome + Xvfb，有头模式运行在虚拟显示器中
- 数据库：`/var/lib/openai-login/data/accounts.db`
- 加密密钥：`/var/lib/openai-login/data/accounts.key`
- release 根目录：`/opt/openai-login/releases`
- 当前链接：`/opt/openai-login/current`

当前部署基线（2026-10-05 发布后确认）：

- 当前 release：`20261004T164742Z-sub2-recovery`
- 回滚 release：`20261004T104640Z-tab-ui`
- 当前服务应保持 `active (running)`，且 `NRestarts=0`
- 当前服务端并发硬上限：10；页面选择的并发数由前端 worker 控制，实际不超过该上限。
- 页面代理留空时使用 sys1 默认认证 HTTP 代理（节点地址仅记录为 `45.39.200.204:7269`，凭据保存在 `/etc/openai-login/proxy.env`，不写入文档）。
- 历史列表中的 `expires_at` 表示 OAuth Access Token 有效期；Access Token 过期不等于账号或 Refresh Token 失效。
- AUTH → Sub2 导入已启用；Sub2 地址、管理密钥和实例标识保存在 `/etc/openai-login/sub2api.env`（权限 `0600`），由 systemd drop-in 注入服务进程。
- 本次仅发布 AUTH 恢复流程；Sub2 源码、镜像和线上版本未改，恢复阶段调用 Sub2 既有接口。

## 从 Mac 访问

在本目录运行：

```bash
./open-sys1.sh
```

保持 SSH 隧道终端开启，然后在 Mac 浏览器访问：

```text
http://127.0.0.1:18082
```

页面和 API 通过 SSH 隧道访问，浏览器登录流程实际运行在 sys1。停止访问时，在隧道终端按 `Ctrl+C`。

## 线上检查

```bash
ssh sys1 'systemctl --no-pager --full status openai-login.service'
ssh sys1 'readlink -f /opt/openai-login/current'
ssh sys1 'curl -fsS http://127.0.0.1:18082/health'
```

正常状态应满足：服务为 `active`，`/health` 返回 `status: ok`，监听地址仍为 `127.0.0.1:18082`。

## 发布和回滚原则

1. 在 Mac 构建 Linux amd64 二进制，并同时准备匹配的 Playwright driver、Node 和 Chrome 运行环境。
2. 每次发布使用新的不可变目录 `/opt/openai-login/releases/<release-id>`。
3. 发布前保留当前 `/opt/openai-login/current` 作为回滚点，不覆盖或删除旧 release。
4. 切换 `current` 后执行 `systemctl daemon-reload`、重启服务和 `/health` 检查。
5. 线上登录验收失败时，将 `current` 原子切回上一个已知版本，再重启并复查健康状态。
6. 不把账号凭据、代理密码或 token 写入 release、日志或文档。

本项目是独立的登录工具，不要把它误认为 Sub2API 主服务，也不要用 Sub2API 的 Compose 发布流程替代本服务的 systemd 发布方式。

## AUTH 恢复流程发布记录（2026-10-05）

- 发布时间：2026-10-04 16:53:09 UTC（北京时间 2026-10-05 00:53:09）；release：`20261004T164742Z-sub2-recovery`；回滚：`20261004T104640Z-tab-ui`。
- 本次只发布 AUTH。检测到明确的 401/凭据失效后，AUTH 可重新登录取得新 Refresh Token，调用 Sub2 既有凭据写回接口，重新检测成功后开启调度；失败或不确定时保持停用。Sub2 源码、镜像和线上版本未改。
- Mac 构建的 Linux amd64 二进制 SHA-256：`2aabc5ad3c767f015ee55b9452ee39620be49c8b8eec2be0575936e4cd62eba4`；线上 release 保留原有 Node、Playwright driver 和 Chrome 运行环境。
- 发布前 SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T164742Z-sub2-recovery.db`，权限 `0600`，`integrity_check=ok`。切换后及延迟复核服务为 `active/running`、`NRestarts=0`、`/health={"status":"ok","max_concurrent":10}`，公网未登录返回 HTTP 401，旧 release 保留可回滚。
- 恢复功能已发布，真实 401 账号的完整重新登录、写回、检测和重新开启调度链路尚未验收。

## Tab UI 发布记录（2026-10-04）

- 发布时间：2026-10-04 10:54:05 UTC（北京时间 18:54:05）。release：`20261004T104640Z-tab-ui`；回滚：`20261004T045750Z-history-actions`。
- 发布三栏 Tab 布局、移动端滚动与底部操作栏、导航和焦点管理，以及统一应用内确认框。前端定向测试及模拟 API 浏览器验证见 [TAB_UI_REDESIGN.md](TAB_UI_REDESIGN.md)；本次功能与真实 OAuth 验收按用户要求由用户自行执行，未声称本轮真实登录通过。
- Mac 本地构建二进制 SHA-256：`59b2c3960dcc9410f95c096e8dc3bdd7f19f190a11219b615deb544817567719`。线上 HTML 和两份 JavaScript 共 3 个静态资源 SHA 与本地一致。Node `v24.19.0`、Playwright `1.62.1` 原样保留，系统 Chrome 为 `154.0.8037.97`。
- 发布前 SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T104640Z-tab-ui.db`，权限 `0600`，`integrity_check=ok`。发布前后账号均为 30 条，导入和检测摘要可用，`sub2_configured=true`；业务数据库未被回滚覆盖。
- 切换后及 10:55:37 UTC 延后复查均为 `active/running`、`NRestarts=0`、`/health={"status":"ok","max_concurrent":10}`；确认唯一服务进程运行新 release 二进制。发布以来 panic、fatal、数据库锁及启动失败日志聚合均为 0。
- 公网未登录返回 HTTP 401，与发布前一致，身份验证保护正常；这不代表认证后公网或业务功能验收。旧版二进制 SHA、Node 与 driver 已核对保留，回滚就绪，临时上传二进制已清理。
- Linux amd64 构建和 Go 定向测试通过，精确命令见 [当前状态](STATUS.md)。沿用本轮已通过的前端验证记录，未运行全仓测试；未执行真实账号登录、模型检测、生产删除或 Sub2 导入验收。

## 历史列表选择与批量删除发布验收（2026-10-04）

- 当前页普通全选覆盖所有账号状态；翻页/筛选保留选择并标明当前页外数量，支持清空选择。复活、导入、删除共用紧凑工具栏，每行只有一个选择框。
- 删除前确认 AUTH 本地记录及数量；逐 ID 执行、15 秒请求超时、明确失败项并保留勾选。不会删除 Sub2 中已导入账号。后台拒绝正在登录或导入中/待核对记录，事务清理本地终态导入快照；在途历史重登和导入不能重建已删除 ID。
- release：`20261004T045750Z-history-actions`；回滚：`20261004T034448Z-account-checks`，原 Node/driver 完整保留。二进制 SHA-256：`562f5a493bd0c322e9fd0cb33f8828b97d04ee9826805bbe929b16878b37f4f0`。
- 发布前无活跃登录、导入或检测，SQLite 一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T045750Z-history-actions.db`（0600，integrity_check通过）。没有 schema 变更，回滚只切二进制，不覆盖业务数据库。
- 上线后原 OAuth 测试账号通过 sys1 默认代理完整登录成功，12.16 秒，AT/RT 返回；服务 active、NRestarts=0、并发上限10。历史账号总数42保持不变，导入和检测摘要可用；未在生产删除账号或触发 Sub2 导入。
- 切换后再次确认二进制 SHA、回滚运行环境和备份权限；panic、fatal、数据库锁、登录失败、删除失败日志计数均为0，AT/RT已持久化，临时上传文件已清理。
- 定向 Go/race/前端 fixture、真实 Chrome 桌面与手机验收通过，测试覆盖普通全选、跨页/筛选、取消、忙碌409、限定404、超时、部分失败重试、导入部分受理和键盘焦点。精确命令见 [当前状态](STATUS.md)。全量测试未运行。

## 账号状态检测发布验收（2026-10-04）

- 新增 AUTH 自有检测功能，固定 `gpt-5.6-luna`，默认服务器代理/显式 sys1 IPv4 直连，单批最多 100、真并发 1/2，与登录槽独立。不读取/刷新 RT，不调用 Sub2 检测接口。
- sys1 默认代理同测试账号两次完成模型请求，耗时 1,896/1,530ms；sys1 IPv4 直连完成，耗时 1,982ms。
- 候选使用独立数据库快照，两个已导入账号均检测正常，执行窗口重叠 3,035ms；正式版正常账号检测 3,206ms 完成，另一个账号真实 429/usage_limit_reached 正确显示额度不足。
- 原有 OAuth 登录在正式版经默认代理完整成功，18.93秒，AT/RT 已持久化；登录并发硬上限仍为 10。服务 active、NRestarts=0，检测/历史/导入摘要均可读。
- 使用旧版二进制打开新 schema 的隔离副本，健康检查通过；原 release 及 Node/driver 保留为回滚。新增表不要求回滚时覆盖数据库。
- 发布前一致性备份：`/var/lib/openai-login/backups/accounts-before-20261004T034448Z-account-checks.db`（0600，integrity_check通过）。
- 完整命令、浏览器验收、真实响应细节见 [实施记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)。全仓测试未运行。

## 浏览器 OAuth 的历史限制

sys1 IPv4 `51.81.109.154` 访问 `auth.openai.com/oauth/authorize` 仍可能收到 Cloudflare `403` 和 `cf-mitigated: challenge`；此前 release 已在 sys1 本机用原生 Chrome、无头 Playwright 两条路径复现，且在初始授权文档和表单提交请求中都观察到过。顶层文档 challenge 会等待 25 秒观察浏览器验证；表单的 fetch/XHR challenge 不重放提交，直接保留真实 403 并准确分类。此前使用 `45.39.200.210:7275` 代理时，真实账号已连续完成 4 次 MFA、授权和 token 交换；当前默认代理为 `45.39.200.204:7269`，历史代理检查可达并返回认证站 HTTP 403。页面显示 challenge 时不要把它解释为密码错误或账号已删除。本次 Tab UI 发布未重做真实 OAuth 验收。

需要继续排查时，先记录授权请求的 HTTP 状态、`cf-mitigated`、出口和浏览器启动参数，再决定是否更换出口或调整浏览器验证流程。
