# Docker 部署指南

## 概述

当前版本使用单服务部署：

- API 和 Worker 分别作为两个进程运行
- 任务队列存储在内置 SQLite `jobs` 表中
- 不再依赖 Redis、PostgreSQL、asynq

镜像仓库：

- `ghcr.io/azincc/gdstudio-embeded-service:latest`

## 本地构建部署

```bash
git clone https://github.com/Azincc/gdstudio-embeded-service.git
cd gdstudio-embeded-service

cp .env.example .env
# 按需编辑 .env

docker-compose up -d --build
docker-compose logs -f embed-service
curl http://localhost:8080/healthz
```

默认 `docker-compose.yml` 会挂载：

- `${NAVIDROME_MUSIC_DIR:-/tmp/music}` -> `${NAVIDROME_MUSIC_DIR:-/tmp/music}`
- `${WORK_DIR:-/tmp/embed-work}` -> `/work`

SQLite 数据库默认写入：

- `/work/data/embed.db`

## 生产环境部署

```bash
mkdir gdstudio-embeded-service-prod
cd gdstudio-embeded-service-prod

wget https://raw.githubusercontent.com/Azincc/gdstudio-embeded-service/main/docker-compose.prod.yml

cat > .env << EOF
NAVIDROME_BASE_URL=http://your-navidrome:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=your_password
NAVIDROME_MUSIC_DIR=/path/to/music
WORK_ROOT=/path/to/work-root
API_KEY=your-secure-api-key
MAX_CONCURRENT_JOBS=3
JOB_POLL_INTERVAL=2s
DOWNLOAD_TIMEOUT=600s
LOG_LEVEL=info
EOF

docker-compose -f docker-compose.prod.yml up -d
docker-compose -f docker-compose.prod.yml logs -f embed-service
curl http://localhost:5434/healthz
```

生产环境默认挂载：

- `${NAVIDROME_MUSIC_DIR}` -> `${NAVIDROME_MUSIC_DIR}`
- `${WORK_ROOT}/work` -> `/work`

SQLite 数据文件位于：

- `${WORK_ROOT}/work/data/embed.db`

## 手动运行镜像

```bash
docker pull ghcr.io/azincc/gdstudio-embeded-service:latest

docker run -d \
  --name embed-service \
  -p 8080:8080 \
  -e DATABASE_URL='file:/work/data/embed.db?_journal_mode=WAL&_busy_timeout=5000' \
  -e NAVIDROME_BASE_URL=http://navidrome:4533 \
  -e NAVIDROME_MUSIC_DIR=/path/to/music \
  -e NAVIDROME_USER=admin \
  -e NAVIDROME_PASSWORD=password \
  -e API_KEY=change-me \
  -e MAX_CONCURRENT_JOBS=3 \
  -e JOB_POLL_INTERVAL=2s \
  -e DOWNLOAD_TIMEOUT=600s \
  -v /path/to/music:/path/to/music:rw \
  -v /path/to/work:/work:rw \
  ghcr.io/azincc/gdstudio-embeded-service:latest
```

## 版本发布

发布标签后，GitHub Actions 会自动构建并推送镜像：

```bash
git tag -a v0.1.1 -m "Release version 0.1.1"
git push origin v0.1.1
```

## 日志与检查

```bash
docker-compose logs -f embed-service
docker-compose ps
curl http://localhost:8080/healthz
```

任务列表可以直接通过 API 查看：

```bash
curl http://localhost:8080/v1/jobs \
  -H "X-API-Key: your-api-key"
```

## 备份与恢复

备份 SQLite：

```bash
cp /path/to/work/data/embed.db ./embed.db.backup
```

恢复 SQLite：

```bash
cp ./embed.db.backup /path/to/work/data/embed.db
```

恢复前建议先停止容器，避免写入竞争：

```bash
docker-compose down
docker-compose up -d
```

## 故障排除

容器启动失败时先看日志：

```bash
docker-compose logs --tail=200 embed-service
```

任务长期停留在 `queued` 时重点检查：

- Worker 进程是否正常启动
- `/work/data/embed.db` 是否可写
- `${NAVIDROME_MUSIC_DIR}` 和 `/work` 挂载路径是否存在
- Navidrome 地址和账号是否可访问

## 安全建议

- 修改默认 `API_KEY`
- 不要把 `.env` 提交到仓库
- 给 `/work` 和 `${NAVIDROME_MUSIC_DIR}` 设置明确权限
- 生产环境建议通过反向代理提供 HTTPS

---

更新日期：2026-03-19
