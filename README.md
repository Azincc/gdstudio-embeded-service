# GDStudio 嵌入式下载微服务

[![Docker Publish](https://github.com/Azincc/gdstudio-embeded-service/actions/workflows/docker-publish.yml/badge.svg)](https://github.com/Azincc/gdstudio-embeded-service/actions/workflows/docker-publish.yml)

基于 Go 的音乐下载与元数据刮削服务，为 Navidrome 提供已刮削的音频文件。

**统一服务架构** - API 和 Worker 在同一容器中运行，简化部署。

## 功能特性

- 🎵 支持多音乐源（网易云、酷我、QQ 音乐等）
- 🏷️ 自动写入元数据（ID3v2/FLAC VorbisComment）
- 🖼️ 封面内嵌与歌词处理
- 🔄 异步任务队列（基于 Redis）
- 🎯 幂等性保证（避免重复下载）
- 📊 Prometheus 指标监控
- 🐳 Docker 容器化部署
- ⚡ 统一服务架构（API + Worker 一体化）

## 技术栈

- **Web 框架**: Gin
- **任务队列**: asynq (Redis)
- **音频标签**: taglib
- **数据库**: PostgreSQL/SQLite
- **日志**: zap (结构化日志)

## 快速开始

### 🐳 Docker 部署（推荐）

**使用预构建镜像（生产环境）**:

```bash
# 1. 下载 docker-compose 配置
wget https://raw.githubusercontent.com/Azincc/gdstudio-embeded-service/main/docker-compose.prod.yml

# 2. 创建 .env 文件
cat > .env << EOF
NAVIDROME_BASE_URL=http://your-navidrome:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=your_password
NAVIDROME_MUSIC_DIR=/path/to/music
API_KEY=your-secure-api-key
EOF

# 3. 启动服务
docker-compose -f docker-compose.prod.yml up -d

# 4. 检查状态
curl http://localhost:8080/healthz
```

**本地构建（开发环境）**:

```bash
# 1. 克隆仓库
git clone https://github.com/Azincc/gdstudio-embeded-service.git
cd gdstudio-embeded-service

# 2. 配置环境变量
cp .env.example .env
# 编辑 .env 填入配置

# 3. 构建并启动
docker-compose up -d --build

# 4. 查看日志
docker-compose logs -f api worker
```

📖 **详细部署文档**: [DOCKER_DEPLOYMENT.md](./DOCKER_DEPLOYMENT.md)

### 本地开发

```bash
# 1. 安装依赖 (macOS)
brew install taglib

# 2. 初始化项目
go mod init github.com/azin/gdstudio-embed-service
go mod tidy

# 3. 启动 Redis
docker run -d -p 6379:6379 redis:7-alpine

# 4. 运行 API 服务
go run cmd/api/main.go

# 5. 运行 Worker（另开终端）
go run cmd/worker/main.go
```

### Docker 部署

```bash
# 1. 配置环境变量
cp .env.example .env
# 编辑 .env 填入配置

# 2. 启动服务（API 和 Worker 在同一容器中）
docker-compose up -d

# 3. 查看日志
docker-compose logs -f embed-service

# 4. 检查健康状态
curl http://localhost:8080/healthz
```

📖 **完整部署指南**: [DOCKER_DEPLOYMENT.md](./DOCKER_DEPLOYMENT.md)
📖 **架构说明**: [SERVICE_MERGE.md](./SERVICE_MERGE.md)

## 项目结构

```
.
├── cmd/
│   ├── api/          # API 服务入口
│   └── worker/       # Worker 进程入口
├── internal/
│   ├── api/          # API 层 (Gin handlers)
│   ├── worker/       # 任务执行器
│   ├── service/      # 业务逻辑
│   │   ├── gdstudio/    # GDStudio API 客户端
│   │   ├── navidrome/   # Navidrome API 客户端
│   │   └── tagger/      # 音频标签写入
│   ├── model/        # 数据模型
│   └── repository/   # 数据访问层
├── configs/
│   └── config.yaml
├── docker-compose.yml
└── Dockerfile
```

## API 接口

### 创建下载任务

```bash
POST /v1/jobs
Content-Type: application/json
X-API-Key: your-api-key

{
  "source": "netease",
  "trackId": "5084198",
  "libraryId": "default",
  "quality": "best"
}
```

### 查询任务状态

```bash
GET /v1/jobs/{job_id}
X-API-Key: your-api-key
```

### 健康检查

```bash
GET /healthz
```

## 配置说明

主要配置项（`configs/config.yaml`）：

```yaml
server:
  port: 8080

gdstudio:
  base_url: https://music-api.gdstudio.xyz
  timeout: 15s

navidrome:
  base_url: http://localhost:4533
  username: admin
  password: admin

storage:
  work_dir: /work/tmp
  music_dir: /music/library
  path_template: "{artist}/{album}/{trackNo:02d} - {title}.{ext}"

worker:
  max_concurrent: 3
  download_timeout: 600s
```

## 环境变量

```bash
# Redis
REDIS_URL=redis://localhost:6379

# 数据库
DATABASE_URL=postgres://user:pass@localhost:5432/embed_service

# GDStudio API
GD_API_BASE=https://music-api.gdstudio.xyz

# Navidrome
NAVIDROME_BASE_URL=http://navidrome:4533
NAVIDROME_USER=admin
NAVIDROME_TOKEN=your-token

# Worker 配置
MAX_CONCURRENT_JOBS=3
DOWNLOAD_TIMEOUT=600
LOG_LEVEL=info
```

## 开发路线图

### ✅ M1（最小可用）已完成
- [x] 项目脚手架搭建
- [x] 单曲任务 API
- [x] GDStudio 客户端（search/url/pic/lyric）
- [x] MP3 标签写入
- [x] 文件移动与 Navidrome 扫描
- [x] Docker 容器化部署
- [x] GitHub Actions CI/CD

### M2（增强）
- [ ] FLAC 支持与 .lrc 歌词
- [ ] 幂等性实现
- [ ] 重试/取消 API
- [ ] Prometheus 指标

### M3（生产化）
- [ ] 批量下载 API
- [ ] 高可用部署
- [ ] Grafana Dashboard

## 镜像仓库

所有镜像自动发布到 GitHub Container Registry:

- **统一镜像**: `ghcr.io/azincc/gdstudio-embeded-service:latest`
  - 包含 API 和 Worker
  - 一个容器同时运行两个服务

支持架构: `linux/amd64`, `linux/arm64`

## 许可证

MIT

## 相关文档

- **架构说明**: [SERVICE_MERGE.md](./SERVICE_MERGE.md) - API + Worker 统一服务架构
- **部署指南**: [DOCKER_DEPLOYMENT.md](./DOCKER_DEPLOYMENT.md) - Docker 部署完整指南
- **测试文档**: [TESTING.md](./TESTING.md) - 功能测试指南
- **快速开始**: [QUICKSTART.md](./QUICKSTART.md) - 5 分钟快速上手
- **M1 完成报告**: [M1_COMPLETION.md](./M1_COMPLETION.md) - M1 阶段完成情况
- **项目信息**: [PROJECT_INFO.md](./PROJECT_INFO.md) - 项目架构和技术栈
- **设计文档**: `/Users/azin/echo/docs/gdstudio_embed_service_plan.md` - 详细设计文档
