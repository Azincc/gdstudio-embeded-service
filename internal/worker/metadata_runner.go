package worker

import (
	"context"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/repository"
	"go.uber.org/zap"
)

type MetadataRunner struct {
	repo         *repository.MetadataJobRepository
	task         *MetadataApplyTask
	logger       *zap.Logger
	pollInterval time.Duration
}

func NewMetadataRunner(
	repo *repository.MetadataJobRepository,
	task *MetadataApplyTask,
	logger *zap.Logger,
	pollInterval time.Duration,
) *MetadataRunner {
	if pollInterval <= 0 {
		pollInterval = time.Second
	}
	return &MetadataRunner{
		repo:         repo,
		task:         task,
		logger:       logger,
		pollInterval: pollInterval,
	}
}

func (r *MetadataRunner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.repo.ClaimNextQueued()
		if err != nil {
			r.logger.Warn("failed to claim metadata job", zap.Error(err))
			if !sleepWithContext(ctx, r.pollInterval) {
				return
			}
			continue
		}
		if job == nil {
			if !sleepWithContext(ctx, r.pollInterval) {
				return
			}
			continue
		}

		if err := r.task.ProcessJob(ctx, job); err != nil {
			r.logger.Warn("metadata job processing failed",
				zap.String("job_id", job.ID),
				zap.Error(err))
		}
	}
}
