# 多阶段构建
FROM golang:1.22-alpine AS builder

# 构建参数
ARG VERSION=dev
ARG COMMIT_SHA=unknown
ARG BUILD_DATE=unknown

# 安装 taglib 开发库和构建工具
RUN apk add --no-cache gcc musl-dev pkgconfig taglib-dev

WORKDIR /build

# 复制依赖文件并下载
COPY go.mod go.sum ./
RUN go mod download

# 复制源码并构建
COPY . .
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-X main.Version=${VERSION} -X main.CommitSHA=${COMMIT_SHA} -X main.BuildDate=${BUILD_DATE}" \
    -o api ./cmd/api
RUN CGO_ENABLED=1 GOOS=linux go build \
    -ldflags="-X main.Version=${VERSION} -X main.CommitSHA=${COMMIT_SHA} -X main.BuildDate=${BUILD_DATE}" \
    -o worker ./cmd/worker

# 运行阶段 - 使用最小化镜像
FROM alpine:latest

# 安装运行时依赖
# flac 包提供 metaflac，用于写入 FLAC 元数据
RUN apk add --no-cache ca-certificates taglib tzdata bash flac && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

# 从构建阶段复制二进制文件
COPY --from=builder --chown=appuser:appuser /build/api .
COPY --from=builder --chown=appuser:appuser /build/worker .
COPY --chown=appuser:appuser configs/config.yaml ./configs/

# 创建启动脚本
RUN echo '#!/bin/bash' > /app/start.sh && \
    echo 'set -e' >> /app/start.sh && \
    echo 'echo "Starting GDStudio Embed Service..."' >> /app/start.sh && \
    echo './api &' >> /app/start.sh && \
    echo 'API_PID=$!' >> /app/start.sh && \
    echo 'echo "API started with PID $API_PID"' >> /app/start.sh && \
    echo 'sleep 2' >> /app/start.sh && \
    echo './worker &' >> /app/start.sh && \
    echo 'WORKER_PID=$!' >> /app/start.sh && \
    echo 'echo "Worker started with PID $WORKER_PID"' >> /app/start.sh && \
    echo 'wait -n' >> /app/start.sh && \
    echo 'exit $?' >> /app/start.sh && \
    chmod +x /app/start.sh && \
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
