package worker

import (
	"context"
	"errors"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type MetadataRunner struct {
	repo         *repository.MetadataJobRepository
	task         *MetadataApplyTask
	logger       *zap.Logger
	pollInterval time.Duration
	owner        string
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
		owner:        uuid.NewString() + "-metadata",
	}
}

func (r *MetadataRunner) Run(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.repo.ClaimNextQueued(r.owner, repository.DefaultLeaseDuration)
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

		if err := r.runLeased(ctx, job); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, repository.ErrLeaseLost) {
				r.logger.Info("metadata job processing interrupted", zap.String("job_id", job.ID))
			} else {
				r.logger.Warn("metadata job processing failed",
					zap.String("job_id", job.ID),
					zap.Error(err))
			}
		}
	}
}

func (r *MetadataRunner) runLeased(ctx context.Context, job *model.MetadataJob) error {
	taskCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		ticker := time.NewTicker(repository.DefaultLeaseHeartbeat)
		defer ticker.Stop()
		for {
			select {
			case <-taskCtx.Done():
				return
			case <-done:
				return
			case <-ticker.C:
				if err := r.repo.RenewLease(job.ID, job.LeaseOwner, repository.DefaultLeaseDuration); err != nil {
					r.logger.Warn("metadata job lease renewal failed; stopping task",
						zap.String("job_id", job.ID),
						zap.Error(err))
					cancel()
					return
				}
			}
		}
	}()

	err := r.task.ProcessJob(taskCtx, job)
	close(done)
	cancel()
	if releaseErr := r.repo.ReleaseLease(job.ID, job.LeaseOwner); releaseErr != nil {
		r.logger.Warn("failed to release metadata job lease",
			zap.String("job_id", job.ID),
			zap.Error(releaseErr))
	}
	return err
}
