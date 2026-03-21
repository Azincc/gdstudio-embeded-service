package worker

import (
	"context"
	"sync"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"go.uber.org/zap"
)

type runnerRepository interface {
	ClaimNextQueued() (*model.Job, error)
	FindNextByStatuses(statuses []string) (*model.Job, error)
}

type runnerTask interface {
	DownloadTimeout() time.Duration
	ProcessDownloadPayload(ctx context.Context, payload *DownloadPayload) error
	ProcessPostProcessPayload(ctx context.Context, payload *DownloadPayload) error
}

// Runner 基于 SQLite jobs 表轮询任务。
type Runner struct {
	repo         runnerRepository
	task         runnerTask
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
	wg.Add(1)
	go func() {
		defer wg.Done()
		r.postProcessLoop(ctx)
	}()

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
		if timeout := r.task.DownloadTimeout(); timeout > 0 {
			var cancel context.CancelFunc
			taskCtx, cancel = context.WithTimeout(ctx, timeout)
			err = r.task.ProcessDownloadPayload(taskCtx, payload)
			cancel()
		} else {
			err = r.task.ProcessDownloadPayload(taskCtx, payload)
		}

		if err != nil {
			log.Warn("job processing finished with error",
				zap.String("job_id", job.ID),
				zap.Error(err))
		}
	}
}

func (r *Runner) postProcessLoop(ctx context.Context) {
	log := r.logger.With(zap.String("pipeline", "postprocess"))
	statuses := []string{
		model.JobStatusTagging,
		model.JobStatusMoving,
		model.JobStatusScanning,
	}

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.repo.FindNextByStatuses(statuses)
		if err != nil {
			log.Warn("failed to find post-process job", zap.Error(err))
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
			log.Warn("post-process job produced empty payload", zap.String("job_id", job.ID))
			if !sleepWithContext(ctx, r.pollInterval) {
				return
			}
			continue
		}

		log.Info("picked post-process job",
			zap.String("job_id", job.ID),
			zap.String("status", job.Status))

		if err := r.task.ProcessPostProcessPayload(ctx, payload); err != nil {
			log.Warn("post-process finished with error",
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
