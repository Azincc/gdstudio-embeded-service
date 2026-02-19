# GDStudio 嵌入式下载微服务 - 快速开始指南

## 📋 前置要求

- Go 1.22+ （生产部署可跳过，使用 Docker）
- Docker & Docker Compose（推荐）
- taglib 库（本地开发需要）

## 🚀 快速部署（推荐）

### 1. 使用 Docker Compose（最简单）

```bash
# 1. 克隆/进入项目目录
cd /Users/azin/PycharmProjects/gdstudio-embeded-service

# 2. 配置环境变量
cp .env.example .env

# 编辑 .env 文件，至少配置以下内容：
# NAVIDROME_BASE_URL=http://your-navidrome:4533
# NAVIDROME_USER=admin
# NAVIDROME_PASSWORD=your-password
# NAVIDROME_MUSIC_DIR=/path/to/your/music  # 本机 Navidrome 音乐目录

# 3. 启动服务（首次会自动构建镜像）
docker-compose up -d

# 4. 查看日志
docker-compose logs -f api worker

# 5. 健康检查
curl http://localhost:8080/healthz

# 6. 访问任务队列监控界面（可选）
open http://localhost:8090
```

### 2. 测试提交任务

```bash
# 提交一个下载任务
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dev-api-key-please-change-in-production" \
  -d '{
    "source": "netease",
    "trackId": "5084198",
    "libraryId": "default",
    "quality": "best"
  }'

# 响应示例：
# {"job_id":"job_abc123","status":"queued"}

# 查询任务状态
curl http://localhost:8080/v1/jobs/job_abc123 \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

## 🛠️ 本地开发

如果你想在本地开发而不使用 Docker：

### 1. 安装依赖

```bash
# macOS
brew install taglib go redis

# Ubuntu
sudo apt-get install libtag1-dev golang redis-server

# Arch Linux
sudo pacman -S taglib go redis
```

### 2. 初始化项目

```bash
cd /Users/azin/PycharmProjects/gdstudio-embeded-service

# 初始化 Go 模块（首次需要）
go mod init github.com/azin/gdstudio-embed-service

# TODO: 后续需要创建 go.mod 和代码后执行
# go mod tidy
```

### 3. 启动依赖服务

```bash
# 启动 Redis
docker run -d -p 6379:6379 redis:7-alpine

# 启动 PostgreSQL（或使用 SQLite）
docker run -d -p 5432:5432 \
  -e POSTGRES_DB=embed_service \
  -e POSTGRES_USER=embed \
  -e POSTGRES_PASSWORD=embed_pass \
  postgres:16-alpine
```

### 4. 运行服务

```bash
# 终端 1：运行 API 服务
go run cmd/api/main.go

# 终端 2：运行 Worker
go run cmd/worker/main.go
```

## 📊 监控与管理

### asynq 任务监控

访问 http://localhost:8090 查看：
- 队列中的任务数量
- 任务执行成功率
- 重试任务
- 手动取消/重试任务

### Prometheus 指标

```bash
# 查看指标
curl http://localhost:9091/metrics

# 示例指标：
# embed_jobs_total{status="success"}
# embed_jobs_duration_seconds
# embed_download_bytes_total
```

## 🔧 配置说明

### 关键配置项（.env）

```bash
# Navidrome 配置（必填）
NAVIDROME_BASE_URL=http://localhost:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=admin

# 音乐目录（Docker 挂载路径）
NAVIDROME_MUSIC_DIR=/path/to/navidrome/music  # 本机路径
# 容器内路径固定为 /music/library

# Worker 并发数
MAX_CONCURRENT_JOBS=3  # 根据服务器性能调整

# API 密钥（生产环境必须修改！）
API_KEY=your-secure-random-key-here
```

### 路径模板自定义

编辑 `configs/config.yaml`：

```yaml
storage:
  path_template: "{artist}/{album}/{trackNo:02d} - {title}.{ext}"
  # 变量：
  # {artist}   - 歌手名
  # {album}    - 专辑名
  # {title}    - 歌曲标题
  # {trackNo}  - 曲目号
  # {ext}      - 文件扩展名
  # {year}     - 年份（如果有）
```

## 🐛 常见问题

### 1. Worker 无法访问 Navidrome

**问题**：容器内无法访问 `http://localhost:4533`

**解决**：
- macOS/Windows Docker Desktop：使用 `http://host.docker.internal:4533`
- Linux：使用 `--network host` 或配置实际 IP

### 2. 文件权限问题

**问题**：无法写入 `/music/library`

**解决**：
```bash
# 检查目录权限
ls -la /path/to/navidrome/music

# 修改权限
chmod 755 /path/to/navidrome/music
```

### 3. taglib 构建失败

**问题**：Docker 构建报错 `taglib.h not found`

**解决**：已在 Dockerfile 中配置，如果仍失败：
```dockerfile
# 确保 Dockerfile 有这行
RUN apk add --no-cache taglib-dev
```

### 4. 任务一直卡在 queued

**检查**：
```bash
# 查看 worker 日志
docker-compose logs -f worker

# 检查 Redis 连接
docker-compose exec redis redis-cli ping
```

## 📈 性能调优

### 调整 Worker 并发数

```bash
# .env
MAX_CONCURRENT_JOBS=5  # 增加并发

# docker-compose.yml
deploy:
  replicas: 3  # 增加 worker 实例数
```

### Redis 持久化

编辑 `docker-compose.yml`：

```yaml
redis:
  command: redis-server --appendonly yes
```

## 🔐 生产部署建议

1. **修改默认 API Key**
   ```bash
   # 生成安全密钥
   openssl rand -base64 32
   ```

2. **启用 HTTPS**（使用 Nginx/Caddy 反向代理）

3. **配置 Redis Sentinel**（高可用）

4. **定期备份数据库**

5. **配置监控告警**（Prometheus + Alertmanager）

## 📚 下一步

1. 阅读完整文档：`/Users/azin/echo/docs/gdstudio_embed_service_plan.md`
2. 查看 API 规范：README.md 附录 B
3. 集成到 Echo 客户端：参考 Flutter 集成示例

## 🆘 获取帮助

- GitHub Issues: （待创建）
- 详细设计文档：`/Users/azin/echo/docs/gdstudio_embed_service_plan.md`

---

**当前状态**：项目脚手架已创建，待实现核心代码（M1 阶段）
