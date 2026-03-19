package worker

import (
	"context"
	"sync"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/repository"
	"go.uber.org/zap"
)

// Runner 基于 SQLite jobs 表轮询任务。
type Runner struct {
	repo         *repository.JobRepository
	task         *DownloadTask
	logger       *zap.Logger
	concurrency  int
	pollInterval time.Duration
}

// NewRunner 创建轮询执行器。
func NewRunner(
	repo *repository.JobRepository,
	task *DownloadTask,
	logger *zap.Logger,
	concurrency int,
	pollInterval time.Duration,
) *Runner {
	if concurrency <= 0 {
		concurrency = 1
	}
	if pollInterval <= 0 {
		pollInterval = time.Second
	}

	return &Runner{
		repo:         repo,
		task:         task,
		logger:       logger,
		concurrency:  concurrency,
		pollInterval: pollInterval,
	}
}

// Run 启动内置任务轮询。
func (r *Runner) Run(ctx context.Context) {
	var wg sync.WaitGroup
	for i := 0; i < r.concurrency; i++ {
		wg.Add(1)
		go func(slot int) {
			defer wg.Done()
			r.loop(ctx, slot+1)
		}(i)
	}

	<-ctx.Done()
	wg.Wait()
}

func (r *Runner) loop(ctx context.Context, slot int) {
	log := r.logger.With(zap.Int("slot", slot))

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.repo.ClaimNextQueued()
		if err != nil {
			log.Warn("failed to claim queued job", zap.Error(err))
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

		payload := PayloadFromJob(job)
		if payload == nil {
			log.Warn("claimed job produced empty payload", zap.String("job_id", job.ID))
			if !sleepWithContext(ctx, r.pollInterval) {
				return
			}
			continue
		}

		log.Info("claimed job", zap.String("job_id", job.ID))

		taskCtx := ctx
		if timeout := r.task.cfg.Worker.DownloadTimeout; timeout > 0 {
			var cancel context.CancelFunc
			taskCtx, cancel = context.WithTimeout(ctx, timeout)
			err = r.task.ProcessPayload(taskCtx, payload)
			cancel()
		} else {
			err = r.task.ProcessPayload(taskCtx, payload)
		}

		if err != nil {
			log.Warn("job processing finished with error",
				zap.String("job_id", job.ID),
				zap.Error(err))
		}
	}
}

func sleepWithContext(ctx context.Context, d time.Duration) bool {
	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
		return false
	case <-timer.C:
		return true
	}
}
