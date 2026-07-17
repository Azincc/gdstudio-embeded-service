package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os/signal"
	"syscall"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/api"
	"github.com/azin/gdstudio-embed-service/internal/api/handlers"
	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/database"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/metadata"
	"github.com/azin/gdstudio-embed-service/pkg/logger"
	"go.uber.org/zap"
)

var (
	Version   = "0.2.12"
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
	metadataCandidatesJobRepo := repository.NewMetadataCandidatesJobRepository(db)

	gdClient := gdstudio.NewClient(&cfg.GDStudio, log)
	metadataResolver := metadata.NewResolver(cfg, gdClient, log)

	// 初始化 Handler
	jobHandler := handlers.NewJobHandler(cfg, jobRepo, log, Version)
	metadataHandler := handlers.NewMetadataHandler(
		metadataResolver,
		metadataJobRepo,
		metadataCandidatesJobRepo,
		log,
	)

	// 设置路由
	router := api.SetupRouter(cfg, jobHandler, metadataHandler)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	candidatesDone := make(chan struct{})
	go func() {
		defer close(candidatesDone)
		metadataHandler.RunCandidates(ctx, cfg.Worker.MaxConcurrent, cfg.Worker.PollInterval)
	}()

	// 启动服务器
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Info("server listening", zap.String("addr", addr))
	server := &http.Server{
		Addr:              addr,
		Handler:           router,
		ReadHeaderTimeout: 10 * time.Second,
	}
	serverErr := make(chan error, 1)
	go func() {
		serverErr <- server.ListenAndServe()
	}()

	select {
	case err := <-serverErr:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatal("failed to start server", zap.Error(err))
		}
	case <-ctx.Done():
	}

	log.Info("shutting down server...")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Warn("server shutdown failed", zap.Error(err))
	}
	stop()
	<-candidatesDone
}
