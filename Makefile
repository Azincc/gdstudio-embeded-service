.PHONY: help build run test docker-build docker-up docker-down migrate-up migrate-down

help:
	@echo "可用命令："
	@echo "  make build          - 构建 API 和 Worker 二进制"
	@echo "  make run-api        - 运行 API 服务"
	@echo "  make run-worker     - 运行 Worker"
	@echo "  make test           - 运行测试"
	@echo "  make docker-build   - 构建 Docker 镜像"
	@echo "  make docker-up      - 启动 Docker Compose"
	@echo "  make docker-down    - 停止 Docker Compose"
	@echo "  make migrate-up     - 运行数据库迁移"

build:
	CGO_ENABLED=1 go build -o bin/api ./cmd/api
	CGO_ENABLED=1 go build -o bin/worker ./cmd/worker

run-api:
	go run ./cmd/api/main.go

run-worker:
	go run ./cmd/worker/main.go

test:
	go test -v -race -coverprofile=coverage.out ./...

docker-build:
	docker build -t gdstudio-embed-service:latest .

docker-up:
	docker-compose up -d

docker-down:
	docker-compose down

docker-logs:
	docker-compose logs -f api worker

migrate-up:
	go run ./cmd/migrate/main.go up

migrate-down:
	go run ./cmd/migrate/main.go down

clean:
	rm -rf bin/
	rm -f coverage.out

lint:
	golangci-lint run ./...

init:
	@echo "初始化项目..."
	go mod init github.com/azin/gdstudio-embed-service || true
	go mod tidy
	mkdir -p cmd/api cmd/worker internal/api internal/worker internal/service internal/model internal/repository configs
	@echo "项目初始化完成！"
