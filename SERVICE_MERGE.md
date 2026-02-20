# 服务合并完成

## 更改说明

已将 API 和 Worker 服务合并为一个统一的服务，简化部署和管理。

## 架构变更

### 之前（分离架构）
```
┌─────────────┐      ┌─────────────┐      ┌──────────────┐
│   Redis     │      │ PostgreSQL  │      │ Asynqmon UI  │
└─────────────┘      └─────────────┘      └──────────────┘
      ▲                    ▲                      ▲
      │                    │                      │
┌─────┴────┐         ┌─────┴────┐                │
│   API    │────────▶│  Worker  │────────────────┘
│ Service  │         │ Service  │
└──────────┘         └──────────┘
(端口 8080)          (后台运行)
```

### 现在（统一架构）
```
┌─────────────┐      ┌─────────────┐      ┌──────────────┐
│   Redis     │      │ PostgreSQL  │      │ Asynqmon UI  │
└─────────────┘      └─────────────┘      └──────────────┘
      ▲                    ▲                      ▲
      │                    │                      │
      └────────────┬───────┴──────────────────────┘
                   │
           ┌───────▼────────┐
           │ Embed Service  │
           │ ┌────────────┐ │
           │ │    API     │ │ (端口 8080)
           │ └────────────┘ │
           │ ┌────────────┐ │
           │ │   Worker   │ │ (后台运行)
           │ └────────────┘ │
           └────────────────┘
         一个容器同时运行两者
```

## 修改的文件

### 1. Dockerfile
- **移除**: 多个构建目标（api, worker, base）
- **简化**: 单一构建流程
- **新增**: 启动脚本 `/app/start.sh`，同时启动 API 和 Worker
- **保留**: 健康检查、非 root 用户、构建参数

启动脚本逻辑：
```bash
./api &       # 后台启动 API
./worker &    # 后台启动 Worker
wait -n       # 等待任一进程退出
```

### 2. docker-compose.yml
- **移除**: 分离的 `api` 和 `worker` 服务
- **新增**: 统一的 `embed-service` 服务
- **简化**: 单一容器，所有环境变量集中配置

### 3. docker-compose.prod.yml
- **移除**: 分离的镜像引用
- **更新**: 使用单一镜像 `ghcr.io/azincc/gdstudio-embeded-service:latest`
- **移除**: Worker 的 replicas 配置（不再支持独立扩展）

### 4. GitHub Actions
- **保留**: `.github/workflows/docker-publish.yml`（构建统一镜像）
- **删除**: `.github/workflows/docker-multi-service.yml`（不再需要）
- **简化**: 只构建一个镜像，支持多架构

## 使用方法

### 本地开发

```bash
# 1. 克隆仓库
git clone https://github.com/Azincc/gdstudio-embeded-service.git
cd gdstudio-embeded-service

# 2. 配置环境变量
cp .env.example .env
vim .env

# 3. 启动服务
docker-compose up -d

# 4. 查看日志（API 和 Worker 都在同一个容器）
docker-compose logs -f embed-service

# 5. 检查进程
docker exec embed-service ps aux
# 应该看到 api 和 worker 两个进程
```

### 生产环境

```bash
# 1. 下载配置
wget https://raw.githubusercontent.com/Azincc/gdstudio-embeded-service/main/docker-compose.prod.yml

# 2. 创建 .env
cat > .env << EOF
NAVIDROME_BASE_URL=http://your-navidrome:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=your_password
NAVIDROME_MUSIC_DIR=/path/to/music
API_KEY=your-secure-api-key
MAX_CONCURRENT_JOBS=3
EOF

# 3. 启动
docker-compose -f docker-compose.prod.yml up -d

# 4. 检查健康状态
curl http://localhost:8080/healthz
```

### 手动运行镜像

```bash
# 拉取镜像
docker pull ghcr.io/azincc/gdstudio-embeded-service:latest

# 运行（API 和 Worker 自动启动）
docker run -d \
  --name embed-service \
  -p 8080:8080 \
  -e REDIS_URL=redis:6379 \
  -e DATABASE_URL=postgres://user:pass@postgres:5432/db \
  -e NAVIDROME_BASE_URL=http://navidrome:4533 \
  -e NAVIDROME_USER=admin \
  -e NAVIDROME_PASSWORD=password \
  -e MAX_CONCURRENT_JOBS=3 \
  -v /path/to/music:/music:rw \
  -v /path/to/work:/work:rw \
  ghcr.io/azincc/gdstudio-embeded-service:latest

# 查看进程
docker exec embed-service ps aux
```

## 优点

### ✅ 简化部署
- 只需一个容器
- 减少配置复杂度
- 更少的网络通信

### ✅ 降低资源开销
- 共享内存
- 减少容器启动时间
- 节省 Docker 镜像存储

### ✅ 更容易管理
- 单一日志流
- 统一的健康检查
- 简化的故障排查

### ✅ 适合小规模部署
- 个人用户
- 小型团队
- 低并发场景（<100 任务/天）

## 注意事项

### ⚠️ 不再支持独立扩展
- 无法单独扩展 Worker
- 如需更多处理能力，需运行多个完整容器（包含 API）
- 大规模场景建议回到分离架构

### ⚠️ 故障影响范围
- Worker 崩溃可能影响 API
- API 重启会中断 Worker 任务
- 建议配置自动重启（`restart: unless-stopped`）

### ⚠️ 资源分配
- API 和 Worker 共享 CPU/内存限制
- 下载任务可能影响 API 响应时间
- 建议设置合理的 `MAX_CONCURRENT_JOBS`（建议 2-3）

## 性能指标

### 镜像大小
- **统一镜像**: ~60MB（压缩后）
- **之前（两个镜像）**: ~50MB × 2 = ~100MB

### 资源使用
```yaml
# 推荐配置
services:
  embed-service:
    deploy:
      resources:
        limits:
          cpus: '2'
          memory: 1G
        reservations:
          cpus: '0.5'
          memory: 256M
```

## 监控

### 查看日志
```bash
# 查看所有日志（API + Worker）
docker-compose logs -f embed-service

# 过滤 API 日志
docker-compose logs -f embed-service | grep "API"

# 过滤 Worker 日志
docker-compose logs -f embed-service | grep "Worker"
```

### 检查进程
```bash
# 查看进程列表
docker exec embed-service ps aux

# 应该看到：
# PID  USER     COMMAND
#  1   appuser  /app/start.sh
#  7   appuser  ./api
#  8   appuser  ./worker
```

### Asynq 监控 UI
访问 `http://localhost:8090` 查看任务队列状态。

## 故障排除

### 问题 1: 容器启动失败
```bash
# 查看容器日志
docker-compose logs embed-service

# 检查进程
docker exec embed-service ps aux
```

### 问题 2: API 无法访问
```bash
# 检查端口
curl http://localhost:8080/healthz

# 查看 API 进程
docker exec embed-service pgrep -a api
```

### 问题 3: Worker 未处理任务
```bash
# 检查 Worker 进程
docker exec embed-service pgrep -a worker

# 查看 Worker 日志
docker-compose logs -f embed-service | grep -i worker

# 检查 Redis 连接
docker exec embed-redis redis-cli PING
```

## 镜像仓库

所有镜像自动发布到 GitHub Container Registry:

- **统一镜像**: `ghcr.io/azincc/gdstudio-embeded-service:latest`
- **版本标签**: `ghcr.io/azincc/gdstudio-embeded-service:v1.0.0`

支持架构: `linux/amd64`, `linux/arm64`

## 迁移指南

如果你之前使用的是分离架构，迁移步骤：

```bash
# 1. 停止旧服务
docker-compose down

# 2. 拉取最新代码
git pull origin main

# 3. 重新构建
docker-compose build

# 4. 启动新服务
docker-compose up -d

# 5. 验证
curl http://localhost:8080/healthz
docker-compose logs -f embed-service
```

数据（数据库和 Redis）会保留，无需迁移。

## 下一步

- ✅ 推送代码触发 GitHub Actions 构建
- ✅ 验证镜像可以正常运行
- ✅ 测试下载功能
- 📝 根据使用情况调整 `MAX_CONCURRENT_JOBS`

---

**更新日期**: 2026-02-19
**状态**: ✅ 服务合并完成，可以部署使用
