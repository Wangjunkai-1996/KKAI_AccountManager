# Cloudflare Challenge 优化检查清单

## 根因和诊断

- [ ] 在同一授权入口分别验证 sys1 直连、指定代理和本机真实 Chrome。
- [ ] 记录出口地区、HTTP 状态、`cf-mitigated`、`cf-ray`、Chrome 版本和代理模式。
- [ ] 确认诊断数据不包含密码、TOTP、Cookie、token、完整查询参数或响应正文。
- [ ] 确认真实 Chrome 是否能看到邮箱输入框。

## 代码边界

- [ ] challenge 分类保持为 `cloudflare_challenge`，并保留 HTTP 状态。
- [ ] challenge 不标记为账号删除/停用，不进行无限重试。
- [ ] 自动挑战最多进行一次 15–30 秒有限等待；超时返回明确终止错误。
- [ ] token 交换和授权确认阶段也能识别 challenge，避免分类漂移。
- [ ] 输入邮箱、密码和 TOTP 必须保持原文准确，所有等待受 context 取消和总超时约束。
- [ ] 默认配置不启用未经验证的指纹伪装；User-Agent 与实际 Chrome Client Hints 保持一致。

## 定向验证

- [ ] `test_compile.sh` 只读检查格式并执行 `go test ./internal/login ./cmd/server -count=1`，超时 90 秒。
- [ ] `test-optimization.sh` 只读检查格式并执行相同的两个包，不写入 `bin/`，不运行全仓库测试。
- [ ] `build-linux.sh` 只构建本地 Linux amd64 产物，不上传、不切换 release、不重启服务。
- [ ] 真实浏览器回归在候选出口上完成后，再考虑线上验收。

## 发布边界

- [ ] 未完成单账号真实 OAuth 成功验收前，不宣称 challenge 已修复。
- [ ] 未完成线上健康、登录验收和回滚准备前，不切换 sys1 release。
- [ ] 发布和回滚严格遵循 `SYS1_DEPLOYMENT.md`。
