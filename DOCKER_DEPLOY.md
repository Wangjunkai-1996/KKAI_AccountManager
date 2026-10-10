# Docker 部署指南

> 本文只用于通用 Docker 部署。sys1 使用独立 systemd 服务；线上发布以 `SYS1_DEPLOYMENT.md` 为准。

Docker 路径统一使用主 `Dockerfile` 的 Debian、锁定版本 Playwright driver、配套 Node 和 bundled Chromium。旧 `Dockerfile.local` 的 Alpine 快捷路径已移除；`quick-docker.sh` 复用 Compose。

## 首次启动

需要 Docker Engine 20.10+ 和 Docker Compose v2。在项目根目录准备 `.sub2api.env`，填入实际 Sub2 配置并设为 `0600`；不接入 Sub2 时创建空文件即可。不要将账号密码、密钥或 token 提交到仓库。

```bash
# 首次创建配置文件；已有配置时不要覆盖。
touch .sub2api.env
chmod 600 .sub2api.env

./quick-docker.sh
docker compose ps
docker compose logs -f
```

默认只监听宿主机 `127.0.0.1:8080`。SQLite 数据库与加密密钥共同保存在命名卷 `openai-login-data`，容器内路径为 `/app/data/accounts.db`、`/app/data/accounts.key`。`docker compose down` 或删除容器会保留该卷；`docker compose down -v`、`docker volume rm` 会删除数据，不用于普通升级。不要覆盖 `OPENAI_LOGIN_DB`/`OPENAI_LOGIN_KEY` 指向卷外路径。

同一数据卷只运行一个 AUTH 实例；不要通过多副本共享该 SQLite 卷。

## 手动构建和启动

```bash
./build-docker.sh
docker run -d \
  --name openai-login-web \
  -p 127.0.0.1:8080:8080 \
  --env-file .sub2api.env \
  --mount type=volume,src=openai-login-data,dst=/app/data \
  --restart unless-stopped \
  openai-login-web:latest
```

镜像的 `CMD` 已包含程序名及监听、headless 参数，默认运行无需额外参数。需要覆盖时必须给出可执行程序，例如 `openai-login-web:latest ./openai-login-web -bind=0.0.0.0 -headless=true -open-browser=false -max-concurrent=2`，不能在镜像名后只写 flags。Compose 已运行时不要再同时启动手动容器。

## 旧容器首次迁移

旧版本未挂载数据卷，直接重建会丢失数据库与加密密钥。`quick-docker.sh` 会阻止替换此类容器。先检查原容器实际数据路径；以下示例针对默认 `/app/data`，`legacy_container` 按实际名称填写 `openai-login` 或 `openai-login-web`。

```bash
legacy_container=openai-login
# 先完成新镜像构建，构建失败时旧服务仍可继续运行。
./build-docker.sh

# 等待在途登录/恢复任务结束，再停旧容器取得一致性数据副本。
docker stop "$legacy_container"
umask 077
backup_dir="$PWD/docker-data-backup-$(date +%Y%m%dT%H%M%S)"
mkdir "$backup_dir"
docker cp "$legacy_container:/app/data/." "$backup_dir/"
test -s "$backup_dir/accounts.db"
test -s "$backup_dir/accounts.key"

docker volume create openai-login-data
# 仅允许写入空卷，防止覆盖已有业务数据。
docker run --rm \
  --mount type=volume,src=openai-login-data,dst=/app/data \
  --mount type=bind,src="$backup_dir",dst=/backup,readonly \
  --entrypoint sh openai-login-web:latest \
  -c 'test -z "$(ls -A /app/data)" && cp -a /backup/. /app/data/'

# 保留原容器和本地备份以供回退；不要删除命名卷。
docker rename "$legacy_container" openai-login-legacy
docker compose up -d
docker compose ps
curl -fsS http://127.0.0.1:8080/ready
```

任一步失败都应先处理该步骤，不继续替换旧容器。确认历史账号可读后再自行清理旧容器；数据库与密钥备份必须成对保留，备份目录包含敏感信息。已配置自定义数据路径或 bind mount 的部署，按实际路径迁移，不套用默认目录。

## 后续更新与验收

```bash
./quick-docker.sh
docker compose ps
curl -fsS http://127.0.0.1:8080/health
curl -fsS http://127.0.0.1:8080/ready
docker compose logs --tail=100
```

`/health` 检查 HTTP 存活，`/ready` 检查数据库连接。镜像和 Compose healthcheck 使用 `/ready`。Docker 的 `unhealthy` 状态本身不会触发 `restart: unless-stopped`；该策略用于进程退出后的重启。

健康检查不代表浏览器或上游 OAuth 已验证。容器构建或浏览器依赖变更后，还需验证实际浏览器启动和登录流程。本次整改只做静态检查，尚未执行镜像构建或容器浏览器验收。

## 远程访问与日志

远程访问通过带认证的 HTTPS 反向代理转发至 `127.0.0.1:8080`，不要直接公开管理端口。Nginx 需要保留 SSE 并设置足够的超时，例如：

```nginx
location / {
    auth_basic "Restricted Access";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://127.0.0.1:8080;
    proxy_set_header Host $host;
    proxy_set_header X-Real-IP $remote_addr;
    proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    proxy_set_header X-Forwarded-Proto $scheme;
    proxy_buffering off;
    proxy_read_timeout 240s;
}
```

Compose 日志采用 `json-file`，单文件上限 `10m`，保留 3 个文件。查看日志使用 `docker compose logs -f --tail=100`；停止使用 `docker compose stop`，恢复使用 `docker compose start`。普通更新不删除数据卷。
