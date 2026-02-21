package handlers

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/worker"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

// JobHandler 任务处理器
type JobHandler struct {
	cfg    *config.Config
	repo   *repository.JobRepository
	client *asynq.Client
	logger *zap.Logger
}

// NewJobHandler 创建处理器
func NewJobHandler(
	cfg *config.Config,
	repo *repository.JobRepository,
	client *asynq.Client,
	logger *zap.Logger,
) *JobHandler {
	return &JobHandler{
		cfg:    cfg,
		repo:   repo,
		client: client,
		logger: logger,
	}
}

// CreateJobRequest 创建任务请求
type CreateJobRequest struct {
	Source         string                 `json:"source" binding:"required"`
	TrackID        string                 `json:"track_id" binding:"required"`
	PicID          string                 `json:"pic_id"`
	LyricID        string                 `json:"lyric_id"`
	LibraryID      string                 `json:"library_id" binding:"required"`
	Quality        string                 `json:"quality"`
	IdempotencyKey string                 `json:"idempotency_key"`
	PathPolicy     map[string]interface{} `json:"path_policy"`

	// 可选的元数据（如果客户端已知）
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	TrackNumber int    `json:"track_number"`
	Year        int    `json:"year"`
}

// CreateJobResponse 创建任务响应
type CreateJobResponse struct {
	JobID   string `json:"job_id"`
	Status  string `json:"status"`
	Message string `json:"message,omitempty"`
}

// Create 创建任务
func (h *JobHandler) Create(c *gin.Context) {
	var req CreateJobRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	// 默认值
	if req.Quality == "" {
		req.Quality = "best"
	}

	// 生成幂等键
	idempotencyKey := req.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = fmt.Sprintf("%s:%s:%s", req.Source, req.TrackID, req.LibraryID)
	}

	// 检查是否已存在
	existing, err := h.repo.FindByIdempotencyKey(idempotencyKey)
	if err != nil {
		h.logger.Error("failed to check idempotency", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal error"})
		return
	}

	if existing != nil {
		h.logger.Info("job already exists", zap.String("job_id", existing.ID))
		c.JSON(http.StatusOK, CreateJobResponse{
			JobID:   existing.ID,
			Status:  existing.Status,
			Message: "job already exists",
		})
		return
	}

	// 创建新任务
	job := &model.Job{
		ID:             uuid.New().String(),
		IdempotencyKey: idempotencyKey,
		Source:         req.Source,
		TrackID:        req.TrackID,
		PicID:          req.PicID,
		LyricID:        req.LyricID,
		LibraryID:      req.LibraryID,
		Quality:        req.Quality,
		Title:          req.Title,
		Artist:         req.Artist,
		Album:          req.Album,
		TrackNumber:    req.TrackNumber,
		Year:           req.Year,
		Status:         model.JobStatusQueued,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := h.repo.Create(job); err != nil {
		h.logger.Error("failed to create job", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create job"})
		return
	}

	// 创建任务载荷
	picID := req.PicID
	if picID == "" {
		picID = req.TrackID
	}
	lyricID := req.LyricID
	if lyricID == "" {
		lyricID = req.TrackID
	}

	payload := worker.DownloadPayload{
		JobID:     job.ID,
		Source:    req.Source,
		TrackID:   req.TrackID,
		PicID:     picID,
		LyricID:   lyricID,
		LibraryID: req.LibraryID,
		Quality:   req.Quality,
	}

	payloadBytes, _ := json.Marshal(payload)

	// 入队
	task := asynq.NewTask(worker.TypeDownload, payloadBytes)
	info, err := h.client.Enqueue(task)
	if err != nil {
		h.logger.Error("failed to enqueue task", zap.Error(err))
		h.repo.MarkFailed(job.ID, err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	h.logger.Info("job created and enqueued",
		zap.String("job_id", job.ID),
		zap.String("task_id", info.ID))

	c.JSON(http.StatusOK, CreateJobResponse{
		JobID:   job.ID,
		Status:  model.JobStatusQueued,
		Message: "job created successfully",
	})
}

// Get 查询任务
func (h *JobHandler) Get(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.repo.FindByID(jobID)
	if err != nil {
		h.logger.Error("failed to find job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	c.JSON(http.StatusOK, job)
}

// List 列出任务
func (h *JobHandler) List(c *gin.Context) {
	status := c.Query("status")
	limit := 50

	var jobs []*model.Job
	var err error

	if status != "" {
		jobs, err = h.repo.ListByStatus(status, limit)
	} else {
		jobs, err = h.repo.ListRecent(limit)
	}

	if err != nil {
		h.logger.Error("failed to list jobs", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to list jobs"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"jobs":  jobs,
		"count": len(jobs),
	})
}

// Retry 重试任务
func (h *JobHandler) Retry(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.repo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	// 只能重试失败的任务
	if job.Status != model.JobStatusFailed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "only failed jobs can be retried"})
		return
	}

	// 重置状态
	job.Status = model.JobStatusQueued
	job.Error = ""
	job.Message = "retrying"

	if err := h.repo.Update(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update job"})
		return
	}

	// 重新入队
	picID := job.PicID
	if picID == "" {
		picID = job.TrackID
	}
	lyricID := job.LyricID
	if lyricID == "" {
		lyricID = job.TrackID
	}

	payload := worker.DownloadPayload{
		JobID:     job.ID,
		Source:    job.Source,
		TrackID:   job.TrackID,
		PicID:     picID,
		LyricID:   lyricID,
		LibraryID: job.LibraryID,
		Quality:   job.Quality,
	}

	payloadBytes, _ := json.Marshal(payload)
	task := asynq.NewTask(worker.TypeDownload, payloadBytes)

	if _, err := h.client.Enqueue(task); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to enqueue task"})
		return
	}

	h.repo.IncrementRetry(job.ID)

	c.JSON(http.StatusOK, gin.H{
		"job_id":  job.ID,
		"status":  model.JobStatusQueued,
		"message": "job queued for retry",
	})
}

// Cancel 取消任务
func (h *JobHandler) Cancel(c *gin.Context) {
	jobID := c.Param("id")

	job, err := h.repo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	// 只能取消进行中的任务
	if job.Status == model.JobStatusDone || job.Status == model.JobStatusFailed {
		c.JSON(http.StatusBadRequest, gin.H{"error": "cannot cancel completed or failed job"})
		return
	}

	job.Status = model.JobStatusCancelled
	job.Message = "cancelled by user"

	if err := h.repo.Update(job); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":  job.ID,
		"status":  model.JobStatusCancelled,
		"message": "job cancelled successfully",
	})
}

// Health 健康检查
func (h *JobHandler) Health(c *gin.Context) {
	// 检查数据库
	count, err := h.repo.CountByStatus(model.JobStatusQueued)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "unhealthy",
			"error":  "database connection failed",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"version": "1.0.0-m1",
		"uptime":  time.Since(time.Now()).Seconds(),
		"components": gin.H{
			"database": "healthy",
			"queue":    "healthy",
		},
		"stats": gin.H{
			"queued_jobs": count,
		},
	})
}
