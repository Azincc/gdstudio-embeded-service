package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"sync"
	"syscall"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/database"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/metadata"
	"github.com/azin/gdstudio-embed-service/internal/service/musicbrainz"
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"github.com/azin/gdstudio-embed-service/internal/worker"
	"github.com/azin/gdstudio-embed-service/pkg/logger"
	"go.uber.org/zap"
)

var (
	Version   = "0.2.6"
	CommitSHA = "unknown"
	BuildDate = "unknown"
)

func main() {
	cfg, err := config.Load("")
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

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
		zap.String("version", Version),
		zap.String("commit", CommitSHA),
		zap.String("build_date", BuildDate),
		zap.Int("concurrency", cfg.Worker.MaxConcurrent),
		zap.Duration("poll_interval", cfg.Worker.PollInterval))

	db, err := database.Open(cfg)
	if err != nil {
		log.Fatal("failed to init database", zap.Error(err))
	}

	if err := repository.InitDB(db); err != nil {
		log.Fatal("failed to migrate database", zap.Error(err))
	}

	jobRepo := repository.NewJobRepository(db)
	metadataJobRepo := repository.NewMetadataJobRepository(db)
	gdClient := gdstudio.NewClient(&cfg.GDStudio, log)
	naviClient := navidrome.NewClient(&cfg.Navidrome, log)
	taggerService := tagger.NewTagger(log)

	if err := naviClient.Ping(); err != nil {
		log.Warn("navidrome ping failed", zap.Error(err))
	} else {
		log.Info("navidrome connection successful")
	}

	if err := os.MkdirAll(cfg.Storage.WorkDir, 0755); err != nil {
		log.Fatal("failed to create work dir", zap.Error(err))
	}

	var mbClient *musicbrainz.Client
	if cfg.MusicBrainz.Enabled {
		mbClient = musicbrainz.NewClient(&cfg.MusicBrainz, log)
		log.Info("musicbrainz client enabled",
			zap.String("base_url", cfg.MusicBrainz.BaseURL))
	} else {
		log.Info("musicbrainz client disabled, album artist will use fallback")
	}

	downloadTask := worker.NewDownloadTask(
		cfg,
		jobRepo,
		gdClient,
		naviClient,
		taggerService,
		mbClient,
		log,
	)
	runner := worker.NewRunner(
		jobRepo,
		downloadTask,
		log,
		cfg.Worker.MaxConcurrent,
		cfg.Worker.PollInterval,
	)
	metadataResolver := metadata.NewResolver(cfg, gdClient, mbClient, log)
	metadataTask := worker.NewMetadataApplyTask(
		cfg,
		metadataJobRepo,
		metadataResolver,
		taggerService,
		naviClient,
		log,
	)
	metadataRunner := worker.NewMetadataRunner(
		metadataJobRepo,
		metadataTask,
		log,
		cfg.Worker.PollInterval,
	)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		runner.Run(ctx)
	}()
	go func() {
		defer wg.Done()
		metadataRunner.Run(ctx)
	}()

	log.Info("worker started", zap.Int("concurrency", cfg.Worker.MaxConcurrent))

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Info("shutting down worker...")
	cancel()
	wg.Wait()
}
