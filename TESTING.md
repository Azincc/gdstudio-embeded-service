# 测试验证指南

## 前置准备

```bash
cp .env.example .env
mkdir -p /work/tmp /work/data /music/library
go mod tidy
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker
```

`.env` 最少需要确认这些值：

```bash
NAVIDROME_BASE_URL=http://localhost:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=admin
DATABASE_URL=file:/work/data/embed.db?_journal_mode=WAL&_busy_timeout=5000
MAX_CONCURRENT_JOBS=3
JOB_POLL_INTERVAL=2s
LOG_LEVEL=debug
```

## 基本流程

### 1. 启动 API

```bash
./bin/api
```

预期日志包含：

- `starting embed-service API`
- `server listening`

### 2. 启动 Worker

```bash
./bin/worker
```

预期日志包含：

- `starting embed-service Worker`
- `worker started`

### 3. 健康检查

```bash
curl http://localhost:8080/healthz
```

预期响应包含：

- `status: healthy`
- `components.database: healthy`
- `components.queue: embedded`

### 4. 提交任务

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dev-api-key-please-change-in-production" \
  -d '{
    "source": "netease",
    "track_id": "5084198",
    "library_id": "default",
    "quality": "best"
  }'
```

预期响应：

```json
{
  "job_id": "xxxx",
  "status": "queued",
  "message": "job created successfully"
}
```

### 5. 观察状态流转

```bash
curl http://localhost:8080/v1/jobs/<job_id> \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

正常状态流转：

- `queued`
- `resolving`
- `downloading`
- `tagging`
- `moving`
- `scanning`
- `done`

### 6. 查看任务列表

```bash
curl http://localhost:8080/v1/jobs \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

### 7. 验证输出文件

```bash
find /music/library -maxdepth 3 -type f
```

至少应看到目标音频文件；如果歌词获取成功，还会看到同名 `.lrc` 文件。

## 推荐测试用例

### 幂等性

连续两次提交同一个 `idempotency_key`，第二次应直接返回已存在任务。

### 失败重试

对错误的 `track_id` 提交任务，等状态变为 `failed` 后调用：

```bash
curl -X POST http://localhost:8080/v1/jobs/<job_id>/retry \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

预期返回 `status: queued`。

### 认证

- 不带 `X-API-Key` 请求应返回 `401`
- 错误 API Key 也应返回 `401`

## 故障排查

### 任务卡在 `queued`

检查：

```bash
curl http://localhost:8080/v1/jobs \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

以及 Worker 日志：

```bash
./bin/worker
```

重点确认：

- `/work/data/embed.db` 可写
- Worker 进程已启动
- SQLite 文件被 API 和 Worker 指向同一个路径

### 数据库初始化失败

检查 `DATABASE_URL` 指向的目录是否存在且可写，例如：

```bash
mkdir -p /work/data
ls -ld /work/data
```

### Navidrome 连接失败

```bash
curl "http://localhost:4533/rest/ping?u=admin&p=admin&v=1.16.1&c=test&f=json"
```

## 验收标准

- API 和 Worker 都能独立启动
- 健康检查返回 `queue: embedded`
- 提交任务后能被 Worker 自动领取
- 文件能写入目标音乐目录
- 失败任务可重新排队
- 不再需要 Redis 或 PostgreSQL
