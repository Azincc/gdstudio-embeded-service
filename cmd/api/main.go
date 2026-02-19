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
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/pkg/logger"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
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
		zap.Int("port", cfg.Server.Port),
		zap.String("mode", cfg.Server.Mode))

	// 初始化数据库
	db, err := initDatabase(cfg)
	if err != nil {
		log.Fatal("failed to init database", zap.Error(err))
	}

	// 运行迁移
	if err := repository.InitDB(db); err != nil {
		log.Fatal("failed to migrate database", zap.Error(err))
	}

	// 初始化仓库
	jobRepo := repository.NewJobRepository(db)

	// 初始化 asynq 客户端
	asynqClient := asynq.NewClient(asynq.RedisClientOpt{
		Addr: cfg.Redis.URL,
		DB:   cfg.Redis.DB,
	})
	defer asynqClient.Close()

	// 初始化 Handler
	jobHandler := handlers.NewJobHandler(cfg, jobRepo, asynqClient, log)

	// 设置路由
	router := api.SetupRouter(cfg, jobHandler)

	// 启动服务器
	addr := fmt.Sprintf(":%d", cfg.Server.Port)
	log.Info("server listening", zap.String("addr", addr))

	// 优雅关闭
	go func() {
		if err := router.Run(addr); err != nil {
			log.Fatal("failed to start server", zap.Error(err))
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down server...")
}

func initDatabase(cfg *config.Config) (*gorm.DB, error) {
	var dialector gorm.Dialector

	switch cfg.Database.Driver {
	case "postgres":
		dialector = postgres.Open(cfg.Database.DSN)
	case "sqlite":
		dialector = sqlite.Open(cfg.Database.DSN)
	default:
		return nil, fmt.Errorf("unsupported database driver: %s", cfg.Database.Driver)
	}

	db, err := gorm.Open(dialector, &gorm.Config{})
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("failed to get sql.DB: %w", err)
	}

	sqlDB.SetMaxIdleConns(cfg.Database.MaxIdleConns)
	sqlDB.SetMaxOpenConns(cfg.Database.MaxOpenConns)
	sqlDB.SetConnMaxLifetime(cfg.Database.ConnMaxLifetime)

	return db, nil
}
