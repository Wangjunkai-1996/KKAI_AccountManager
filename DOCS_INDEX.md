# KKAI_AUTH 文档索引

更新时间：2026-10-05（Asia/Shanghai）

这是新对话的入口文件。新对话不要先依赖旧聊天记录，按下面顺序读取当前文档。

## 新对话阅读顺序

1. `NEW_CHAT_CONTEXT.md`：项目交接上下文、已完成工作、当前阻塞和下一步。
2. `SYS1_DEPLOYMENT.md`：sys1 线上部署的唯一权威记录。
3. `README.md`：当前本地和 sys1 网页版的使用方法。
4. `STATUS.md`：当前状态速览。
5. `GUIDE.md`：CLI 参数和导出格式参考。

## 账号状态检测

- [实施与验收记录](ACCOUNT_STATUS_CHECK_IMPLEMENTATION.md)：已上线，包含实际改动、定向测试、sys1 直连/代理与真并发证据。
- [详细设计](ACCOUNT_STATUS_CHECK_PLAN.md)：接口、存储、错误分类与边界设计。

## 当前事实

- 桌面项目目录：`/Users/tokk/Desktop/KKAI_AUTH`
- sys1 服务：`openai-login.service`
- 线上入口：`https://auth.kkrich.ltd`
- Mac 通过 `./open-sys1.sh` 建立 SSH 隧道，浏览器访问 `http://127.0.0.1:18082`
- sys1 本机服务只监听 `127.0.0.1:18082`
- 当前 release：`20261005T020014Z-sub2-sync-auto`
- 回滚 release：`20261004T164742Z-sub2-recovery`
- 数据库和密钥在 `/var/lib/openai-login/data/`，不放在 release 目录
- 最近一次核验服务为 `active`、`NRestarts=0`，`/health` 返回 `status: ok`、`max_concurrent: 10`
- AUTH → Sub2 导入已上线，线上 `/api/history` 已确认 `sub2_configured=true`；使用 Sub2 既有接口，Sub2 源码和线上版本未改。
- 本次新增批量登录结果导入 Sub2、历史列表读取 Sub2 状态及可见页面每 5 分钟同步。自动恢复开启后，现有 AUTH 检测任务的明确 401/凭据失效结果可自动入队恢复；直接重新登录，不再优先 Refresh Token。调度策略依据 Sub2 状态判断，不能保证识别所有人工暂停情形。
- 自动恢复启动默认关闭，开关只在进程运行期间生效，重启按 `AUTH_AUTO_RECOVERY` 恢复；没有新增定时 AUTH 模型检测。真实 OAuth/401 恢复业务链路本轮尚未验收。
- 本次 AUTH 发布于 2026-10-05 02:13:47 UTC（北京时间 10:13:47）；02:17:40 UTC 延迟健康复核通过，实际入口 Unix socket 健康正常，公网未认证返回 HTTP 401，数据库备份及回滚准备已核对，详见 [部署记录](SYS1_DEPLOYMENT.md)。
- AUTH 账号检测已上线；sys1 IPv4 直连与默认代理模型请求均成功，候选并发 2 的两个请求重叠约 3 秒；正式版正确区分正常与真实 429 额度不足。浏览器 OAuth 登录与模型检测的证据不可混用。
- 历史浏览器 OAuth 验收：sys1 IPv4 直连授权请求在输入邮箱前返回 `403 + cf-mitigated: challenge`；此前 7275 认证代理真实账号验收连续 4 次成功并完成 token 交换；当前默认代理为 `45.39.200.204:7269`，最近代理检查可达

## 文档优先级

发生冲突时按以下优先级处理：

1. `SYS1_DEPLOYMENT.md`（线上事实）
2. `NEW_CHAT_CONTEXT.md`（工作交接状态）
3. `README.md` 和 `STATUS.md`（当前使用说明）
4. 其他文档（CLI、历史优化、安全审查或 Docker 参考）

`DEPLOY.md`、`WEB_GUIDE.md`、`HOW_TO_USE.md`、`OPTIMIZATION_SUMMARY.md`、`SECURITY_REVIEW.md`、`DOCKER_DEPLOY.md` 中可能保留历史命令或通用示例；它们不能覆盖 sys1 专用文档中的路径、端口、release 或验收结论。

## 安全边界

文档不保存账号密码、TOTP、代理凭据、access token 或 refresh token。新对话需要测试账号时，必须由用户在当次会话重新提供，不能从文档推断或恢复。
