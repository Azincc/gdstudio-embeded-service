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

# 基础运行时镜像
FROM alpine:latest AS base

# 安装运行时依赖
RUN apk add --no-cache ca-certificates taglib tzdata && \
    addgroup -g 1000 appuser && \
    adduser -D -u 1000 -G appuser appuser

WORKDIR /app

# 创建工作目录
RUN mkdir -p /work/tmp /music/library && \
    chown -R appuser:appuser /work /music /app

# 复制配置文件
COPY --chown=appuser:appuser configs/config.yaml ./configs/

# API 镜像
FROM base AS api

# 从构建阶段复制 API 二进制文件
COPY --from=builder --chown=appuser:appuser /build/api .

USER appuser

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD wget --no-verbose --tries=1 --spider http://localhost:8080/healthz || exit 1

CMD ["./api"]

# Worker 镜像
FROM base AS worker

# 从构建阶段复制 Worker 二进制文件
COPY --from=builder --chown=appuser:appuser /build/worker .

USER appuser

# Worker 不需要暴露端口，但可以通过环境变量配置

HEALTHCHECK --interval=30s --timeout=5s --start-period=10s --retries=3 \
  CMD pgrep -f worker || exit 1

CMD ["./worker"]

# 默认镜像（包含两个二进制文件）
FROM base AS default

# 从构建阶段复制两个二进制文件
COPY --from=builder --chown=appuser:appuser /build/api .
COPY --from=builder --chown=appuser:appuser /build/worker .

USER appuser

EXPOSE 8080

# 默认启动 API，可通过 docker run 覆盖命令启动 worker
CMD ["./api"]
