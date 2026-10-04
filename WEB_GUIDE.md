# KKAI_AUTH 网页版使用指南

更新时间：2026-10-03（Asia/Shanghai）

当前网页端有两种使用方式：本地 Mac 运行，或通过 SSH 隧道使用 sys1 上已经部署的服务。线上部署和故障状态以 `SYS1_DEPLOYMENT.md` 为准。

## 本地 Mac

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./start-local.sh
```

启动后访问 `http://127.0.0.1:8080`。脚本会构建 `cmd/server` 并准备 Playwright driver；默认使用实际 Chrome 的原生浏览器标识。需要实验兼容配置时再显式传入 `-browser-compat=true`。

## sys1 线上服务

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./open-sys1.sh
```

保持该终端开启，然后访问 `http://127.0.0.1:18082`。这是 SSH 本地转发地址，浏览器自动化实际在 sys1 上执行。

## 输入账号

账号框每行一个账号，支持：

```text
email---password---totp_secret
email----password----totp_secret
```

开始处理前按规范化邮箱去重，重复账号只执行一次。默认逐个处理，可在页面选择 1–10 个并发；实际上限以服务端 `/health` 返回值为准。

## 代理

页面只填写一个 HTTP 代理地址，例如：

```text
http://user:password@proxy.example.com:8080
```

留空时使用启动命令中的代理；sys1 页面留空则尝试直接使用 sys1 IPv4 出口。代理地址必须能从实际运行浏览器的机器访问，sys1 页面中的 `127.0.0.1` 指 sys1 本机。

## 历史账号

账号状态、每次尝试、阶段、耗时和错误摘要写入 SQLite。密码、TOTP、代理凭据和 OAuth token 使用 AES-GCM 加密；历史接口和页面不返回敏感字段。

- “重新登录”只提交历史账号 ID，由服务端取出加密凭据。
- 历史列表支持邮箱搜索、状态筛选、排序、分页和账号详情时间线；详情只显示脱敏的尝试记录、错误码、HTTP 状态和耗时。
- 批量复活只允许失败、中断、停用或待确认删除状态，按顺序执行并显示结果；成功账号需要单独重新登录。
- 输入账号后会先做格式、邮箱、密码长度和重复检查；存在错误或重复时不会开始处理。
- 明确判定为官方删除的账号会从数据库清理。
- 停用、删除/停用不确定、普通 403 和 challenge 会保留，以便重试。
- 失败账号可以单独重试，也可以点击“重试全部失败”。

## 超时和错误

- 单账号默认总超时 3 分钟，包含浏览器操作、重试等待和代理回退。
- `429` 和 `5xx` 可在总超时内重试；普通 `4xx`、连接重置和浏览器 challenge 不会无限重试。
- Cloudflare `403 cf-mitigated: challenge` 可能发生在邮箱输入前，页面未输入密码就结束并不表示密码错误。

## 当前线上限制

最近一次 sys1 复核中，服务健康检查正常，但 `auth.openai.com/oauth/authorize` 可能返回 Cloudflare challenge。因此“服务可访问”和“真实账号登录成功”是两个不同验收项；当前后者尚未通过。

## 相关文档

- 新对话入口：`DOCS_INDEX.md`
- 项目交接：`NEW_CHAT_CONTEXT.md`
- sys1 部署：`SYS1_DEPLOYMENT.md`
- 当前速览：`STATUS.md`
