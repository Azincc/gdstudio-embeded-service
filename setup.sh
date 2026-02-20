#!/bin/bash

set -e

echo "====================================="
echo "GDStudio Embed Service - Quick Start"
echo "====================================="

# 检查 Go 环境
if ! command -v go &> /dev/null; then
    echo "❌ Error: Go is not installed"
    echo "Please install Go 1.22+ first"
    exit 1
fi

echo "✅ Go version: $(go version)"

# 下载依赖
echo ""
echo "📦 Downloading dependencies..."
go mod tidy

# 启动 Docker 服务
echo ""
echo "🐳 Starting Redis and PostgreSQL..."
docker-compose up -d redis postgres

# 等待服务就绪
echo "⏳ Waiting for services to be ready..."
sleep 5

# 检查 Redis
if docker-compose ps redis | grep -q "Up"; then
    echo "✅ Redis is running"
else
    echo "❌ Redis failed to start"
    exit 1
fi

# 检查 PostgreSQL
if docker-compose ps postgres | grep -q "Up"; then
    echo "✅ PostgreSQL is running"
else
    echo "❌ PostgreSQL failed to start"
    exit 1
fi

# 编译项目
echo ""
echo "🔨 Building..."
mkdir -p bin
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker

echo "✅ Build completed"

# 创建 .env（如果不存在）
if [ ! -f .env ]; then
    echo ""
    echo "📝 Creating .env file..."
    cat > .env << 'EOF'
NAVIDROME_BASE_URL=http://localhost:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=admin
DATABASE_URL=postgres://embed:embed_pass@localhost:5432/embed_service?sslmode=disable
REDIS_URL=localhost:6379
LOG_LEVEL=info
EOF
    echo "✅ .env file created (please edit it with your actual Navidrome credentials)"
fi

echo ""
echo "====================================="
echo "Setup Complete!"
echo "====================================="
echo ""
echo "Next steps:"
echo ""
echo "1. Edit .env file with your Navidrome credentials:"
echo "   vi .env"
echo ""
echo "2. Start API server (Terminal 1):"
echo "   ./bin/api"
echo ""
echo "3. Start Worker (Terminal 2):"
echo "   ./bin/worker"
echo ""
echo "4. Test the service:"
echo "   curl http://localhost:8080/healthz"
echo ""
echo "5. Submit a test job:"
echo "   curl -X POST http://localhost:8080/v1/jobs \\"
echo "     -H 'Content-Type: application/json' \\"
echo "     -H 'X-API-Key: dev-api-key-please-change-in-production' \\"
echo "     -d '{\"source\":\"netease\",\"track_id\":\"5084198\",\"library_id\":\"default\"}'"
echo ""
echo "See TESTING.md for detailed testing guide."
echo ""
