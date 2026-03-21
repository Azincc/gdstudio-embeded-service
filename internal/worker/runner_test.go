package worker

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"go.uber.org/zap"
)

type fakeRunnerRepo struct {
	mu    sync.Mutex
	queue []*model.Job
	post  []*model.Job
}

func (r *fakeRunnerRepo) ClaimNextQueued() (*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.queue) == 0 {
		return nil, nil
	}
	job := r.queue[0]
	r.queue = r.queue[1:]
	return job, nil
}

func (r *fakeRunnerRepo) FindNextByStatuses(statuses []string) (*model.Job, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.post) == 0 {
		return nil, nil
	}
	job := r.post[0]
	for _, status := range statuses {
		if job.Status == status {
			r.post = r.post[1:]
			return job, nil
		}
	}
	return nil, nil
}

func (r *fakeRunnerRepo) enqueuePost(job *model.Job) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.post = append(r.post, job)
}

type fakeRunnerTask struct {
	repo              *fakeRunnerRepo
	firstPostStarted  chan struct{}
	releasePost       chan struct{}
	secondDownloadHit chan struct{}
	postOnce          sync.Once
	downloaded        map[string]int
	mu                sync.Mutex
}

func (t *fakeRunnerTask) DownloadTimeout() time.Duration {
	return 0
}

func (t *fakeRunnerTask) ProcessDownloadPayload(ctx context.Context, payload *DownloadPayload) error {
	t.mu.Lock()
	t.downloaded[payload.JobID]++
	t.mu.Unlock()

	t.repo.enqueuePost(&model.Job{
		ID:     payload.JobID,
		Status: model.JobStatusTagging,
	})

	if payload.JobID == "job-2" {
		select {
		case t.secondDownloadHit <- struct{}{}:
		default:
		}
	}

	return nil
}

func (t *fakeRunnerTask) ProcessPostProcessPayload(ctx context.Context, payload *DownloadPayload) error {
	t.postOnce.Do(func() {
		close(t.firstPostStarted)
		<-t.releasePost
	})
	return nil
}

func TestRunnerPostProcessingDoesNotBlockDownloads(t *testing.T) {
	repo := &fakeRunnerRepo{
		queue: []*model.Job{
			{ID: "job-1", Status: model.JobStatusQueued},
			{ID: "job-2", Status: model.JobStatusQueued},
		},
	}
	task := &fakeRunnerTask{
		repo:              repo,
		firstPostStarted:  make(chan struct{}),
		releasePost:       make(chan struct{}),
		secondDownloadHit: make(chan struct{}, 1),
		downloaded:        make(map[string]int),
	}

	runner := &Runner{
		repo:         repo,
		task:         task,
		logger:       zap.NewNop(),
		concurrency:  1,
		pollInterval: 5 * time.Millisecond,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})
	go func() {
		runner.Run(ctx)
		close(done)
	}()

	select {
	case <-task.firstPostStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for post-process to start")
	}

	select {
	case <-task.secondDownloadHit:
	case <-time.After(2 * time.Second):
		t.Fatal("expected second download to proceed while post-process is blocked")
	}

	close(task.releasePost)
	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runner did not stop in time")
	}
}
