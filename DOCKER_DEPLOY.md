# 🐳 Docker 部署指南

> 通用/历史参考：sys1 当前使用独立 systemd 服务、Google Chrome、Xvfb 和本机 `127.0.0.1:18082`，不使用本文的 Docker Compose 方案。线上发布请只看 `SYS1_DEPLOYMENT.md`；本文中的 `8080`、`your-server` 和 Docker 示例不能覆盖线上配置。

## 📋 前置要求

在 OVH 物理机上需要安装：
- Docker (>= 20.10)
- Docker Compose (>= 2.0)

安装命令：
```bash
# 安装 Docker
curl -fsSL https://get.docker.com | sh
sudo usermod -aG docker $USER

# 安装 Docker Compose
sudo apt-get update
sudo apt-get install docker-compose-plugin
```

## 🚀 快速开始

### 方式一：使用 docker-compose（推荐）

```bash
# 1. 上传项目到服务器
scp -r openai-login user@your-ovh-server:/opt/

# 2. SSH 登录服务器
ssh user@your-ovh-server

# 3. 进入项目目录
cd /opt/openai-login

# 4. 启动服务
docker-compose up -d

# 5. 查看日志
docker-compose logs -f

# 6. 访问服务
# http://your-server-ip:8080
```

### 方式二：使用构建脚本

```bash
# 1. 构建镜像
chmod +x build-docker.sh
./build-docker.sh

# 2. 启动容器
docker-compose up -d
```

### 方式三：手动构建和运行

```bash
# 1. 构建镜像
docker build -t openai-login-web:latest .

# 2. 运行容器
docker run -d \
  --name openai-login \
  -p 8080:8080 \
  -e HEADLESS=true \
  --restart unless-stopped \
  openai-login-web:latest

# 3. 查看日志
docker logs -f openai-login
```

## ⚙️ 配置选项

### 环境变量

编辑 `docker-compose.yml` 中的 environment 部分：

```yaml
environment:
  - HEADLESS=true          # 无头模式
  - PORT=8080              # 端口
  - HTTP_PROXY=http://...  # 代理（如果需要）
  - HTTPS_PROXY=http://... # HTTPS 代理
```

### 端口映射

修改 `docker-compose.yml` 中的 ports：

```yaml
ports:
  - "8080:8080"  # 改成 "3000:8080" 将服务暴露到 3000 端口
```

### 资源限制

```yaml
deploy:
  resources:
    limits:
      cpus: '2'      # 最多使用 2 个 CPU 核心
      memory: 2G     # 最多使用 2GB 内存
```

## 🔧 常用命令

```bash
# 启动服务
docker-compose up -d

# 停止服务
docker-compose down

# 重启服务
docker-compose restart

# 查看日志
docker-compose logs -f

# 查看容器状态
docker-compose ps

# 更新代码后重新构建
docker-compose up -d --build

# 进入容器调试
docker exec -it openai-login-web bash

# 清理旧镜像
docker image prune -a
```

## 🌐 反向代理配置

### Nginx

```nginx
server {
    listen 80;
    server_name openai-login.yourdomain.com;

    location / {
        proxy_pass http://localhost:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_set_header X-Forwarded-Proto $scheme;
        
        # 超时设置（登录可能需要较长时间）
        proxy_connect_timeout 120s;
        proxy_send_timeout 120s;
        proxy_read_timeout 120s;
    }
}
```

### Traefik

```yaml
labels:
  - "traefik.enable=true"
  - "traefik.http.routers.openai-login.rule=Host(`openai-login.yourdomain.com`)"
  - "traefik.http.services.openai-login.loadbalancer.server.port=8080"
```

## 🔒 安全建议

### 1. 使用 HTTPS

```bash
# 使用 Certbot 获取免费 SSL 证书
sudo apt-get install certbot python3-certbot-nginx
sudo certbot --nginx -d openai-login.yourdomain.com
```

### 2. 限制访问 IP

在 `docker-compose.yml` 中：

```yaml
ports:
  - "127.0.0.1:8080:8080"  # 只允许本地访问
```

然后通过 Nginx 反向代理并设置访问控制。

### 3. 添加基础认证

Nginx 配置：

```nginx
location / {
    auth_basic "Restricted Access";
    auth_basic_user_file /etc/nginx/.htpasswd;
    proxy_pass http://localhost:8080;
}
```

生成密码文件：
```bash
sudo apt-get install apache2-utils
sudo htpasswd -c /etc/nginx/.htpasswd admin
```

## 📊 监控和日志

### 查看实时日志

```bash
docker-compose logs -f --tail=100
```

### 日志持久化

在 `docker-compose.yml` 中已配置：

```yaml
logging:
  driver: "json-file"
  options:
    max-size: "10m"    # 单个日志文件最大 10MB
    max-file: "3"      # 保留 3 个日志文件
```

### 查看容器资源使用

```bash
docker stats openai-login-web
```

## 🐛 故障排查

### 容器无法启动

```bash
# 查看详细日志
docker-compose logs

# 检查容器状态
docker-compose ps

# 查看构建过程
docker-compose build --no-cache
```

### 浏览器驱动问题

```bash
# 进入容器检查
docker exec -it openai-login-web bash

# 检查 Playwright 浏览器
ls -la /ms-playwright/

# 手动安装浏览器
playwright install chromium
```

### 网络连接问题

```bash
# 测试容器网络
docker exec -it openai-login-web curl https://auth.openai.com

# 如果需要代理，在 docker-compose.yml 中添加：
environment:
  - HTTP_PROXY=http://proxy:port
  - HTTPS_PROXY=http://proxy:port
  - NO_PROXY=localhost,127.0.0.1
```

## 🔄 更新部署

```bash
# 1. 停止旧容器
docker-compose down

# 2. 拉取最新代码
git pull

# 或上传新代码
scp -r openai-login user@your-server:/opt/

# 3. 重新构建和启动
docker-compose up -d --build

# 4. 查看日志确认
docker-compose logs -f
```

## 💾 备份和恢复

### 导出镜像

```bash
docker save openai-login-web:latest | gzip > openai-login-web.tar.gz
```

### 导入镜像

```bash
docker load < openai-login-web.tar.gz
```

## 📈 性能优化

### 多副本部署

```yaml
# docker-compose.yml
services:
  openai-login:
    deploy:
      replicas: 3  # 运行 3 个实例
```

### 使用负载均衡

配合 Nginx 或 Traefik 实现负载均衡。

## ✅ 部署完成检查清单

- [ ] Docker 和 Docker Compose 已安装
- [ ] 防火墙已开放 8080 端口（或你的自定义端口）
- [ ] 容器成功启动（`docker-compose ps` 显示 Up）
- [ ] 可以访问 Web 界面
- [ ] 测试批量登录功能正常
- [ ] 日志正常输出
- [ ] （可选）配置了 HTTPS
- [ ] （可选）配置了反向代理
- [ ] （可选）配置了访问控制

## 🎉 部署成功

访问你的服务：
```
http://your-server-ip:8080
```

或通过域名：
```
https://openai-login.yourdomain.com
```

开始批量登录 OpenAI 账号吧！🚀
