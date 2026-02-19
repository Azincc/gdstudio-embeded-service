package main

import (
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"github.com/azin/gdstudio-embed-service/internal/worker"
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
	log.Info("starting embed-service Worker",
		zap.Int("concurrency", cfg.Worker.MaxConcurrent))

	// 初始化数据库
	db, err := initDatabase(cfg)
	if err != nil {
		log.Fatal("failed to init database", zap.Error(err))
	}

	// 初始化仓库
	jobRepo := repository.NewJobRepository(db)

	// 初始化服务客户端
	gdClient := gdstudio.NewClient(&cfg.GDStudio, log)
	naviClient := navidrome.NewClient(&cfg.Navidrome, log)
	taggerService := tagger.NewTagger(log)

	// 测试 Navidrome 连接
	if err := naviClient.Ping(); err != nil {
		log.Warn("navidrome ping failed", zap.Error(err))
	} else {
		log.Info("navidrome connection successful")
	}

	// 创建工作目录
	if err := os.MkdirAll(cfg.Storage.WorkDir, 0755); err != nil {
		log.Fatal("failed to create work dir", zap.Error(err))
	}

	// 初始化任务处理器
	downloadTask := worker.NewDownloadTask(
		cfg,
		jobRepo,
		gdClient,
		naviClient,
		taggerService,
		log,
	)

	// 初始化 asynq 服务器
	srv := asynq.NewServer(
		asynq.RedisClientOpt{
			Addr: cfg.Redis.URL,
			DB:   cfg.Redis.DB,
		},
		asynq.Config{
			Concurrency: cfg.Worker.MaxConcurrent,
			Queues: map[string]int{
				"default": 10,
			},
			Logger: &asynqLogger{log},
		},
	)

	// 注册任务处理器
	mux := asynq.NewServeMux()
	mux.HandleFunc(worker.TypeDownload, downloadTask.ProcessTask)

	log.Info("worker started", zap.Int("concurrency", cfg.Worker.MaxConcurrent))

	// 启动服务器
	go func() {
		if err := srv.Run(mux); err != nil {
			log.Fatal("failed to start worker", zap.Error(err))
		}
	}()

	// 等待中断信号
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down worker...")
	srv.Shutdown()
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

// asynqLogger asynq 日志适配器
type asynqLogger struct {
	logger *zap.Logger
}

func (l *asynqLogger) Debug(args ...interface{}) {
	l.logger.Sugar().Debug(args...)
}

func (l *asynqLogger) Info(args ...interface{}) {
	l.logger.Sugar().Info(args...)
}

func (l *asynqLogger) Warn(args ...interface{}) {
	l.logger.Sugar().Warn(args...)
}

func (l *asynqLogger) Error(args ...interface{}) {
	l.logger.Sugar().Error(args...)
}

func (l *asynqLogger) Fatal(args ...interface{}) {
	l.logger.Sugar().Fatal(args...)
}
