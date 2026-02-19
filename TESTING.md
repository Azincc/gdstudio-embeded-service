# M1 测试验证指南

## 前置准备

### 1. 启动依赖服务

```bash
cd /Users/azin/PycharmProjects/gdstudio-embeded-service

# 启动 Redis 和 PostgreSQL
docker-compose up -d redis postgres

# 等待服务启动
sleep 5

# 检查服务状态
docker-compose ps
```

### 2. 配置环境变量

```bash
# 创建 .env 文件
cat > .env << 'EOF'
NAVIDROME_BASE_URL=http://localhost:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=admin
DATABASE_URL=postgres://embed:embed_pass@localhost:5432/embed_service?sslmode=disable
REDIS_URL=redis://localhost:6379
LOG_LEVEL=debug
EOF
```

### 3. 编译项目

```bash
# 下载依赖
go mod tidy

# 编译 API
go build -o bin/api ./cmd/api

# 编译 Worker
go build -o bin/worker ./cmd/worker
```

## 测试流程

### 步骤 1：启动 API 服务

```bash
# 终端 1
./bin/api

# 预期输出：
# {"level":"info","ts":"...","msg":"starting embed-service API","port":8080,"mode":"release"}
# {"level":"info","ts":"...","msg":"server listening","addr":":8080"}
```

### 步骤 2：启动 Worker

```bash
# 终端 2
./bin/worker

# 预期输出：
# {"level":"info","ts":"...","msg":"starting embed-service Worker","concurrency":3}
# {"level":"info","ts":"...","msg":"navidrome connection successful"}
# {"level":"info","ts":"...","msg":"worker started","concurrency":3}
```

### 步骤 3：测试健康检查

```bash
curl http://localhost:8080/healthz

# 预期响应：
{
  "status": "healthy",
  "version": "1.0.0-m1",
  "components": {
    "database": "healthy",
    "queue": "healthy"
  }
}
```

### 步骤 4：提交测试任务

```bash
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dev-api-key-please-change-in-production" \
  -d '{
    "source": "netease",
    "track_id": "5084198",
    "library_id": "default",
    "quality": "best",
    "title": "测试歌曲",
    "artist": "测试歌手",
    "album": "测试专辑"
  }'

# 预期响应：
{
  "job_id": "550e8400-e29b-41d4-a716-446655440000",
  "status": "queued",
  "message": "job created successfully"
}

# 保存 job_id 用于后续查询
export JOB_ID="<返回的job_id>"
```

### 步骤 5：查询任务状态

```bash
# 每隔几秒查询一次
curl http://localhost:8080/v1/jobs/$JOB_ID \
  -H "X-API-Key: dev-api-key-please-change-in-production"

# 状态变化：
# queued -> resolving -> downloading -> tagging -> moving -> scanning -> done
```

### 步骤 6：检查日志

```bash
# API 日志（终端 1）应该显示：
# - Job created and enqueued

# Worker 日志（终端 2）应该显示：
# - processing download task
# - resolving metadata
# - downloading audio
# - writing tags
# - moving to library
# - triggering navidrome scan
# - download task completed
```

### 步骤 7：验证文件

```bash
# 检查文件是否存在
ls -lh /music/library/测试歌手/测试专辑/

# 应该看到：
# 01 - 测试歌曲.mp3
# 01 - 测试歌曲.mp3.nfo  (临时标签文件)
```

## 测试用例

### 测试 1：幂等性测试

```bash
# 提交相同任务两次
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dev-api-key-please-change-in-production" \
  -d '{
    "source": "netease",
    "track_id": "5084198",
    "library_id": "default",
    "idempotency_key": "test-key-1"
  }'

# 第一次返回：job_id = xxx, status = queued
# 第二次返回：job_id = xxx (相同), status = <当前状态>, message = "job already exists"
```

### 测试 2：查询任务列表

```bash
# 查询所有任务
curl http://localhost:8080/v1/jobs \
  -H "X-API-Key: dev-api-key-please-change-in-production"

# 按状态过滤
curl "http://localhost:8080/v1/jobs?status=done" \
  -H "X-API-Key: dev-api-key-please-change-in-production"
```

### 测试 3：失败重试

```bash
# 模拟失败（使用错误的 track_id）
curl -X POST http://localhost:8080/v1/jobs \
  -H "Content-Type: application/json" \
  -H "X-API-Key: dev-api-key-please-change-in-production" \
  -d '{
    "source": "netease",
    "track_id": "invalid_id",
    "library_id": "default"
  }'

# 获取 job_id
export FAILED_JOB_ID="<job_id>"

# 等待任务失败
sleep 10

# 查询状态
curl http://localhost:8080/v1/jobs/$FAILED_JOB_ID \
  -H "X-API-Key: dev-api-key-please-change-in-production"
# 应该显示 status = "failed"

# 重试任务
curl -X POST http://localhost:8080/v1/jobs/$FAILED_JOB_ID/retry \
  -H "X-API-Key: dev-api-key-please-change-in-production"

# 应该返回 status = "queued"
```

### 测试 4：认证测试

```bash
# 无 API Key
curl http://localhost:8080/v1/jobs
# 预期：401 Unauthorized

# 错误的 API Key
curl http://localhost:8080/v1/jobs \
  -H "X-API-Key: wrong-key"
# 预期：401 Unauthorized
```

## 验收标准

- [ ] API 服务启动成功，监听端口 8080
- [ ] Worker 服务启动成功，连接到 Navidrome
- [ ] 健康检查返回 healthy 状态
- [ ] 成功提交任务并获取 job_id
- [ ] 任务状态正确流转：queued -> resolving -> downloading -> tagging -> moving -> scanning -> done
- [ ] 文件成功下载到临时目录
- [ ] 文件成功移动到 Navidrome 音乐目录
- [ ] 歌词文件 (.lrc) 创建成功
- [ ] 元数据标签文件 (.nfo) 创建成功
- [ ] 幂等性测试通过：相同 key 返回已存在任务
- [ ] 失败任务可以成功重试
- [ ] API Key 认证正常工作

## 已知限制（M1 版本）

- ✅ 使用占位标签文件 (.nfo) 而非真实 ID3 标签（taglib 需要后续集成）
- ✅ 封面和歌词解析依赖额外的 picID/lyricID（客户端需要提供）
- ✅ 文件名模板固定为 `{trackNo} - {title}.{ext}`
- ✅ 仅支持 SQLite 和 PostgreSQL（生产建议 PostgreSQL）

## 故障排查

### Redis 连接失败

```bash
# 检查 Redis 是否运行
docker-compose ps redis

# 测试连接
redis-cli -h localhost -p 6379 ping
# 应返回 PONG
```

### 数据库连接失败

```bash
# 检查 PostgreSQL
docker-compose ps postgres

# 测试连接
psql -h localhost -U embed -d embed_service
# 输入密码：embed_pass
```

### Navidrome 连接失败

```bash
# 检查 Navidrome 是否运行
curl http://localhost:4533/rest/ping?u=admin&p=admin&v=1.16.1&c=test&f=json
```

### 任务卡在 queued 状态

```bash
# 检查 Worker 日志
tail -f worker.log

# 检查 asynq 队列
redis-cli -h localhost -p 6379
127.0.0.1:6379> LLEN asynq:queues:default
```

## 下一步

完成 M1 验收后，继续实现：
- M2：完整的 ID3v2 标签写入（集成 taglib 或 id3v2 库）
- M2：FLAC 支持
- M2：Prometheus 指标
- M2：任务取消功能

---

**测试日期**：____________________
**测试人员**：____________________
**测试结果**：[ ] 通过 [ ] 失败
