# KKAI_AUTH 部署说明

更新时间：2026-10-03（Asia/Shanghai）

## 先看权威文档

sys1 的实际部署、当前 release、回滚点、健康状态和已知故障以 `SYS1_DEPLOYMENT.md` 为准。新对话交接以 `NEW_CHAT_CONTEXT.md` 为准。

本文件只保留当前可执行的入口，避免旧版通用部署示例覆盖线上事实。

## 本地 Mac 启动

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./start-local.sh
```

默认监听 `127.0.0.1:8080`，访问 `http://127.0.0.1:8080`。

## 访问 sys1 已部署服务

```bash
cd /Users/tokk/Desktop/KKAI_AUTH
./open-sys1.sh
```

保持 SSH 隧道终端开启，访问 `http://127.0.0.1:18082`。不要在 Mac 直接启动一个本地服务后把它当成 sys1 线上服务。

## 线上只读检查

```bash
ssh sys1 'systemctl --no-pager --full status openai-login.service'
ssh sys1 'readlink -f /opt/openai-login/current'
ssh sys1 'curl -fsS http://127.0.0.1:18082/health'
```

## 发布边界

- 发布前保留当前 release，使用新的不可变目录。
- 同时准备 Linux amd64 二进制、Playwright driver、Node 和 Chrome 运行环境。
- 切换后检查 systemd、`/health`、有效路由和真实登录验收。
- 真实 OAuth 验收失败时恢复到上一已知 release，并复查健康状态。
- 不把账号凭据、代理密码或 token 写入 release、日志或文档。

## 当前已知问题

sys1 当前服务可以健康运行，但 OAuth 授权请求可能在邮箱输入前返回 Cloudflare `403` 和 `cf-mitigated: challenge`。这不是普通密码错误，也不能直接判定账号已删除。排查记录见 `NEW_CHAT_CONTEXT.md` 和 `SYS1_DEPLOYMENT.md`。

Docker 文档和旧版通用部署示例仅供参考；当前 sys1 使用独立 systemd 服务，不使用 Sub2API Compose 发布流程。
