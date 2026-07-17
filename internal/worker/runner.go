package worker

import (
	"context"
	"errors"
	"strconv"
	"sync"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type runnerRepository interface {
	ClaimNextQueued(owner string, leaseDuration time.Duration) (*model.Job, error)
	ClaimNextPostProcess(owner string, leaseDuration time.Duration, statuses []string) (*model.Job, error)
	RenewLease(id, owner string, leaseDuration time.Duration) error
	ReleaseLease(id, owner string) error
	IsCancelled(id string) (bool, error)
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
	ownerPrefix  string
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
		ownerPrefix:  uuid.NewString(),
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
	owner := r.ownerPrefix + "-download-" + strconv.Itoa(slot)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		job, err := r.repo.ClaimNextQueued(owner, repository.DefaultLeaseDuration)
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

		err = r.runLeased(ctx, job.ID, owner, func(taskCtx context.Context) error {
			if timeout := r.task.DownloadTimeout(); timeout > 0 {
				var cancel context.CancelFunc
				taskCtx, cancel = context.WithTimeout(taskCtx, timeout)
				defer cancel()
			}
			return r.task.ProcessDownloadPayload(taskCtx, payload)
		})

		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, repository.ErrJobCancelled) {
				log.Info("job processing cancelled", zap.String("job_id", job.ID))
			} else {
				log.Warn("job processing finished with error",
					zap.String("job_id", job.ID),
					zap.Error(err))
			}
		}
	}
}

func (r *Runner) postProcessLoop(ctx context.Context) {
	log := r.logger.With(zap.String("pipeline", "postprocess"))
	owner := r.ownerPrefix + "-postprocess"
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

		job, err := r.repo.ClaimNextPostProcess(owner, repository.DefaultLeaseDuration, statuses)
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

		if err := r.runLeased(ctx, job.ID, owner, func(taskCtx context.Context) error {
			return r.task.ProcessPostProcessPayload(taskCtx, payload)
		}); err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, repository.ErrJobCancelled) {
				log.Info("post-process cancelled", zap.String("job_id", job.ID))
			} else {
				log.Warn("post-process finished with error",
					zap.String("job_id", job.ID),
					zap.Error(err))
			}
		}
	}
}

func (r *Runner) runLeased(
	ctx context.Context,
	jobID string,
	owner string,
	process func(context.Context) error,
) error {
	taskCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	go r.watchLease(taskCtx, jobID, owner, cancel, done)
	err := process(taskCtx)
	close(done)
	cancel()

	if releaseErr := r.repo.ReleaseLease(jobID, owner); releaseErr != nil {
		r.logger.Warn("failed to release job lease",
			zap.String("job_id", jobID),
			zap.String("lease_owner", owner),
			zap.Error(releaseErr))
	}
	return err
}

func (r *Runner) watchLease(
	ctx context.Context,
	jobID string,
	owner string,
	cancel context.CancelFunc,
	done <-chan struct{},
) {
	pollTicker := time.NewTicker(500 * time.Millisecond)
	heartbeatTicker := time.NewTicker(repository.DefaultLeaseHeartbeat)
	defer pollTicker.Stop()
	defer heartbeatTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-done:
			return
		case <-pollTicker.C:
			cancelled, err := r.repo.IsCancelled(jobID)
			if err != nil {
				r.logger.Warn("failed to check job cancellation",
					zap.String("job_id", jobID),
					zap.Error(err))
				continue
			}
			if cancelled {
				r.logger.Info("cancelling active job", zap.String("job_id", jobID))
				cancel()
				return
			}
		case <-heartbeatTicker.C:
			if err := r.repo.RenewLease(jobID, owner, repository.DefaultLeaseDuration); err != nil {
				r.logger.Warn("job lease renewal failed; stopping task",
					zap.String("job_id", jobID),
					zap.String("lease_owner", owner),
					zap.Error(err))
				cancel()
				return
			}
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
