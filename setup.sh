#!/bin/bash

set -e

echo "====================================="
echo "GDStudio Embed Service - Quick Start"
echo "====================================="

if ! command -v go &> /dev/null; then
    echo "Error: Go is not installed"
    echo "Please install Go 1.22+ first"
    exit 1
fi

echo "Go version: $(go version)"

echo ""
echo "Downloading dependencies..."
go mod tidy

echo ""
echo "Building..."
mkdir -p bin
go build -o bin/api ./cmd/api
go build -o bin/worker ./cmd/worker

echo "Build completed"

if [ ! -f .env ]; then
    echo ""
    echo "Creating .env file..."
    cat > .env << 'EOF'
NAVIDROME_BASE_URL=http://localhost:4533
NAVIDROME_USER=admin
NAVIDROME_PASSWORD=admin
DATABASE_URL=file:/work/data/embed.db?_journal_mode=WAL&_busy_timeout=5000
MAX_CONCURRENT_JOBS=3
JOB_POLL_INTERVAL=2s
DOWNLOAD_TIMEOUT=600s
LOG_LEVEL=info
EOF
    echo ".env file created (please edit it with your actual Navidrome credentials)"
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
echo "2. Create writable work and music directories if you run locally:"
echo "   mkdir -p /work/tmp /work/data /music/library"
echo ""
echo "3. Start API server (Terminal 1):"
echo "   ./bin/api"
echo ""
echo "4. Start Worker (Terminal 2):"
echo "   ./bin/worker"
echo ""
echo "5. Test the service:"
echo "   curl http://localhost:8080/healthz"
echo ""
echo "6. Submit a test job:"
echo "   curl -X POST http://localhost:8080/v1/jobs \\"
echo "     -H 'Content-Type: application/json' \\"
echo "     -H 'X-API-Key: dev-api-key-please-change-in-production' \\"
echo "     -d '{\"source\":\"netease\",\"track_id\":\"5084198\",\"library_id\":\"default\"}'"
echo ""
echo "See TESTING.md for detailed testing guide."
echo ""
