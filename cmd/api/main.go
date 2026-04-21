package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/azin/gdstudio-embed-service/internal/api"
	"github.com/azin/gdstudio-embed-service/internal/api/handlers"
	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/database"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/metadata"
	"github.com/azin/gdstudio-embed-service/internal/service/musicbrainz"
	"github.com/azin/gdstudio-embed-service/pkg/logger"
	"go.uber.org/zap"
)

var (
	Version   = "0.2.5"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)

func main() {
	// 加载配置
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 初始化日志
	if err := logger.Init(
		cfg.Logging.Level,
		cfg.Logging.Format,
		cfg.Logging.Output,
		cfg.Logging.FilePath,
	); err != nil {
		log.Fatalf("Failed to init logger: %v", err)
	}
	defer logger.Sync()

	log := logger.Get()
	log.Info("starting embed-service API",
		zap.String("version", Version),
		zap.String("commit", CommitSHA),
		zap.String("build_date", BuildDate),
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode))

	// 初始化数据库
	db, err := database.Open(cfg)
	if err != nil {
		log.Fatal("failed to init database", zap.Error(err))
	}

	// 运行迁移
	if err := repository.InitDB(db); err != nil {
		log.Fatal("failed to migrate database", zap.Error(err))
	}

	// 初始化仓库
	jobRepo := repository.NewJobRepository(db)
	metadataJobRepo := repository.NewMetadataJobRepository(db)

	gdClient := gdstudio.NewClient(&cfg.GDStudio, log)
	var mbClient *musicbrainz.Client
	if cfg.MusicBrainz.Enabled {
		mbClient = musicbrainz.NewClient(&cfg.MusicBrainz, log)
	}
	metadataResolver := metadata.NewResolver(cfg, gdClient, mbClient, log)

	// 初始化 Handler
	jobHandler := handlers.NewJobHandler(cfg, jobRepo, log, Version)
	metadataHandler := handlers.NewMetadataHandler(metadataResolver, metadataJobRepo, log)

	// 设置路由
	router := api.SetupRouter(cfg, jobHandler, metadataHandler)

	// 启动服务器
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Info("server listening", zap.String("addr", addr))

	// 优雅关闭
	go func() {
		if err := router.Run(addr); err != nil && err.Error() != "http: Server closed" {
			log.Fatal("failed to start server", zap.Error(err))
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server...")
}
