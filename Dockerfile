# syntax=docker/dockerfile:1.7
# 多阶段构建
FROM golang:1.22-alpine AS builder

# 构建参数
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown

# sqlite 仍然依赖 CGO；音频标签处理本身不需要 taglib 开发头文件。
RUN apk add --no-cache gcc musl-dev

WORKDIR /build

# 复制依赖文件并下载
COPY go.mod go.sum ./
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download

# 复制源码并构建
COPY . .
RUN --mount=type=cache,target=/go/pkg/mod \
    --mount=type=cache,target=/root/.cache/go-build \
    CGO_ENABLED=1 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.CommitSHA=${COMMIT_SHA} -X main.BuildDate=${BUILD_DATE}" \
    -o api ./cmd/api && \
    CGO_ENABLED=1 GOOS=linux go build -trimpath \
    -ldflags="-s -w -X main.Version=${VERSION} -X main.CommitSHA=${COMMIT_SHA} -X main.BuildDate=${BUILD_DATE}" \
    -o worker ./cmd/worker

# 运行阶段 - 使用最小化镜像
FROM alpine:latest

# 安装运行时依赖
# flac 包提供 metaflac，chromaprint 提供 fpcalc 用于生成音频指纹
RUN apk add --no-cache ca-certificates tzdata flac chromaprint && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder --chown=appuser:appuser /build/api .
COPY --from=builder --chown=appuser:appuser /build/worker .
COPY --chown=appuser:appuser configs/config.yaml ./configs/

# 创建启动脚本
RUN cat <<'EOF' > /app/start.sh
#!/bin/sh
set -eu

echo "Starting GDStudio Embed Service..."
./api &
api_pid=$!
echo "API started with PID $api_pid"

sleep 2

./worker &
worker_pid=$!
echo "Worker started with PID $worker_pid"

while :; do
  if ! kill -0 "$api_pid" 2>/dev/null; then
    wait "$api_pid"
    exit $?
  fi
  if ! kill -0 "$worker_pid" 2>/dev/null; then
    wait "$worker_pid"
    exit $?
  fi
  sleep 1
done
EOF
RUN chmod +x /app/start.sh && \
    chown appuser:appuser /app/start.sh

# 创建工作目录
RUN mkdir -p /work/tmp /music/library && \
    chown -R appuser:appuser /work /music /app

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

# 启动脚本会同时运行 API 和 Worker
CMD ["/app/start.sh"]
