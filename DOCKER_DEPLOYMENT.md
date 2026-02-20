# Docker 部署指南

## 概述

本项目提供了完整的 Docker 部署方案，包括：
- **自动化构建**：通过 GitHub Actions 自动构建多架构镜像（amd64/arm64）
- **多种部署方式**：支持本地构建、预构建镜像部署
- **服务分离**：API 和 Worker 独立镜像，可独立扩展

## 镜像仓库

所有镜像都会自动发布到 GitHub Container Registry (GHCR)：

- **完整镜像**（包含 API 和 Worker）：
  - `ghcr.io/azincc/gdstudio-embeded-service:latest`
  - `ghcr.io/azincc/gdstudio-embeded-service:v1.0.0`

- **分离镜像**：
  - API：`ghcr.io/azincc/gdstudio-embeded-service-api:latest`
  - Worker：`ghcr.io/azincc/gdstudio-embeded-service-worker:latest`

## GitHub Actions 工作流

### 1. Docker Publish (docker-publish.yml)

**触发条件**：
- 推送到 `main` 或 `develop` 分支
- 推送标签（如 `v1.0.0`）
- Pull Request 到 `main` 分支
- 手动触发

**功能**：
- 构建多架构镜像（linux/amd64, linux/arm64）
- 推送到 GHCR 和 Docker Hub（如果配置）
- 自动生成版本标签
- 构建缓存优化

**标签策略**：
- `main` 分支 → `latest` 标签
- `develop` 分支 → `develop` 标签
- `v1.2.3` 标签 → `1.2.3`, `1.2`, `1` 标签
- Git commit → `main-abc1234` 标签

### 2. Multi-Service Build (docker-multi-service.yml)

**功能**：
- 分别构建 API 和 Worker 镜像
- 更小的镜像体积
- 独立版本管理
- 支持独立扩展

## 配置 GitHub Secrets

### 必需的 Secrets

无需额外配置！GitHub Actions 会自动使用 `GITHUB_TOKEN` 推送到 GHCR。

### 可选：Docker Hub

如果要同时推送到 Docker Hub，需要配置：

1. 在 GitHub 仓库设置 → Secrets and variables → Actions
2. 添加以下 secrets：
   - `DOCKERHUB_USERNAME`：你的 Docker Hub 用户名
   - `DOCKERHUB_TOKEN`：Docker Hub Access Token

创建 Docker Hub Token：
1. 登录 [Docker Hub](https://hub.docker.com/)
2. Account Settings → Security → New Access Token
3. 创建 token 并复制到 GitHub Secrets

## 部署方式

### 方式 1：本地构建（开发环境）

```bash
# 克隆仓库
git clone https://github.com/Azincc/gdstudio-embeded-service.git
cd gdstudio-embeded-service

# 复制环境变量模板
cp .env.example .env

# 编辑 .env 文件，填写 Navidrome 配置
vim .env

# 构建并启动所有服务
docker-compose up -d --build

# 查看日志
docker-compose logs -f

# 停止服务
docker-compose down
```

### 方式 2：使用预构建镜像（生产环境）

```bash
# 1. 创建工作目录
mkdir gdstudio-embeded-service-prod
cd gdstudio-embeded-service-prod

# 2. 下载生产环境配置
wget https://raw.githubusercontent.com/Azincc/gdstudio-embeded-service/main/docker-compose.prod.yml

# 3. 创建 .env 文件
cat > .env << EOF
NAVIDROME_BASE_URL=http://your-navidrome:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=your_password
NAVIDROME_MUSIC_DIR=/path/to/music
WORK_DIR=/path/to/work

# 数据库配置
POSTGRES_DB=embed_service
POSTGRES_USER=embed
POSTGRES_PASSWORD=your_secure_password

# API 密钥（重要：修改为强密码）
API_KEY=your-secure-api-key-change-this

# Worker 配置
WORKER_REPLICAS=2
MAX_CONCURRENT_JOBS=3
DOWNLOAD_TIMEOUT=600s

# 日志级别
LOG_LEVEL=info
EOF

# 4. 启动服务
docker-compose -f docker-compose.prod.yml up -d

# 5. 查看日志
docker-compose -f docker-compose.prod.yml logs -f api worker

# 6. 检查健康状态
curl http://localhost:8080/healthz
```

### 方式 3：手动拉取镜像

```bash
# 拉取最新镜像
docker pull ghcr.io/azincc/gdstudio-embeded-service-api:latest
docker pull ghcr.io/azincc/gdstudio-embeded-service-worker:latest

# 运行 API
docker run -d \
  --name embed-api \
  -p 8080:8080 \
  -e REDIS_URL=redis:6379 \
  -e DATABASE_URL=postgres://user:pass@postgres:5432/db \
  -e NAVIDROME_BASE_URL=http://navidrome:4533 \
  -e NAVIDROME_USER=admin \
  -e NAVIDROME_PASSWORD=password \
  -v /path/to/music:/music:rw \
  -v /path/to/work:/work:rw \
  ghcr.io/azincc/gdstudio-embeded-service-api:latest

# 运行 Worker
docker run -d \
  --name embed-worker \
  -e REDIS_URL=redis:6379 \
  -e DATABASE_URL=postgres://user:pass@postgres:5432/db \
  -e NAVIDROME_BASE_URL=http://navidrome:4533 \
  -e NAVIDROME_USER=admin \
  -e NAVIDROME_PASSWORD=password \
  -e MAX_CONCURRENT_JOBS=3 \
  -v /path/to/music:/music:rw \
  -v /path/to/work:/work:rw \
  ghcr.io/azincc/gdstudio-embeded-service-worker:latest
```

## 扩展 Worker

### Docker Compose 扩展

```bash
# 扩展到 5 个 Worker 实例
docker-compose up -d --scale worker=5
```

### 生产环境扩展

编辑 `.env` 文件：
```bash
WORKER_REPLICAS=5
```

然后重新部署：
```bash
docker-compose -f docker-compose.prod.yml up -d
```

## 版本管理

### 发布新版本

1. **创建版本标签**：
   ```bash
   git tag -a v1.0.0 -m "Release version 1.0.0"
   git push origin v1.0.0
   ```

2. **GitHub Actions 自动执行**：
   - 构建多架构镜像
   - 推送到 GHCR
   - 生成多个标签（v1.0.0, v1.0, v1, latest）

3. **使用指定版本**：
   ```bash
   # docker-compose.prod.yml 中修改镜像标签
   api:
     image: ghcr.io/azincc/gdstudio-embeded-service-api:v1.0.0
   worker:
     image: ghcr.io/azincc/gdstudio-embeded-service-worker:v1.0.0
   ```

### 回滚版本

```bash
# 停止当前服务
docker-compose -f docker-compose.prod.yml down

# 修改 .env 或直接指定版本
docker-compose -f docker-compose.prod.yml pull
docker-compose -f docker-compose.prod.yml up -d
```

## 监控和维护

### 查看日志

```bash
# 所有服务日志
docker-compose logs -f

# 特定服务日志
docker-compose logs -f api
docker-compose logs -f worker

# 最近 100 行日志
docker-compose logs --tail=100 api
```

### 健康检查

```bash
# API 健康检查
curl http://localhost:8080/healthz

# 查看容器状态
docker-compose ps

# 查看资源使用
docker stats
```

### Asynq 监控 UI

访问 `http://localhost:8090` 查看任务队列状态：
- 队列中的任务数量
- 成功/失败统计
- Worker 状态
- 重试任务

## 备份和恢复

### 备份数据库

```bash
# 备份 PostgreSQL
docker exec embed-postgres pg_dump -U embed embed_service > backup.sql

# 备份 Redis
docker exec embed-redis redis-cli SAVE
docker cp embed-redis:/data/dump.rdb ./redis-backup.rdb
```

### 恢复数据库

```bash
# 恢复 PostgreSQL
docker exec -i embed-postgres psql -U embed embed_service < backup.sql

# 恢复 Redis
docker cp ./redis-backup.rdb embed-redis:/data/dump.rdb
docker restart embed-redis
```

## 故障排除

### 问题 1：镜像拉取失败

**错误**：`failed to pull ghcr.io/azincc/gdstudio-embeded-service-api`

**解决**：
1. 确保仓库是公开的，或者已登录 GHCR：
   ```bash
   echo $GITHUB_TOKEN | docker login ghcr.io -u USERNAME --password-stdin
   ```
2. 检查网络连接
3. 尝试手动拉取测试

### 问题 2：容器启动失败

**检查日志**：
```bash
docker-compose logs api
docker-compose logs worker
```

**常见原因**：
- 环境变量未配置
- 数据库连接失败
- 文件权限问题

### 问题 3：Worker 不处理任务

**检查**：
1. Worker 是否启动：
   ```bash
   docker-compose ps worker
   ```
2. Redis 连接是否正常：
   ```bash
   docker exec embed-redis redis-cli PING
   ```
3. 查看 Worker 日志：
   ```bash
   docker-compose logs -f worker
   ```

## 性能优化

### 1. 调整 Worker 数量

根据 CPU 核心数和任务负载调整：
```bash
# 4 核 CPU 建议 2-4 个 Worker
WORKER_REPLICAS=3
```

### 2. 调整并发任务数

```bash
# 每个 Worker 并发处理任务数
MAX_CONCURRENT_JOBS=3
```

### 3. 资源限制

编辑 `docker-compose.yml` 添加资源限制：
```yaml
services:
  worker:
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 2G
        reservations:
          cpus: '1'
          memory: 512M
```

## 安全建议

1. **修改默认密码**：
   - PostgreSQL 密码
   - API Key
   - Navidrome 密码

2. **使用环境变量**：
   不要在配置文件中硬编码敏感信息

3. **限制网络访问**：
   ```yaml
   api:
     ports:
       - "127.0.0.1:8080:8080"  # 仅本地访问
   ```

4. **使用 HTTPS**：
   在生产环境中使用 Nginx/Traefik 作为反向代理

5. **定期更新镜像**：
   ```bash
   docker-compose pull
   docker-compose up -d
   ```

## 多架构支持

镜像支持以下平台：
- `linux/amd64`（Intel/AMD x86_64）
- `linux/arm64`（ARM v8，如树莓派 4）

Docker 会自动选择适合你系统的架构。

## 参考资源

- [Docker 文档](https://docs.docker.com/)
- [Docker Compose 文档](https://docs.docker.com/compose/)
- [GitHub Container Registry](https://docs.github.com/en/packages/working-with-a-github-packages-registry/working-with-the-container-registry)
- [Asynq 监控 UI](https://github.com/hibiken/asynqmon)

---

**更新日期**：2026-02-19
**维护者**：Azincc
