package handlers

import (
	"fmt"
	"net/http"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

// JobHandler 任务处理器
type JobHandler struct {
	cfg     *config.Config
	repo    *repository.JobRepository
	logger  *zap.Logger
	version string
}

// NewJobHandler 创建处理器
func NewJobHandler(
	cfg *config.Config,
	repo *repository.JobRepository,
	logger *zap.Logger,
	version string,
) *JobHandler {
	return &JobHandler{
		cfg:     cfg,
		repo:    repo,
		logger:  logger,
		version: version,
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
	Force          bool                   `json:"force"`
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
		// 支持 force 参数：允许重新下载已完成/已取消/失败的任务
		canForce := existing.Status == model.JobStatusDone ||
			existing.Status == model.JobStatusCancelled ||
			existing.Status == model.JobStatusFailed
		if req.Force && canForce {
			h.logger.Info("force re-download, removing old job",
				zap.String("old_job_id", existing.ID),
				zap.String("old_status", existing.Status))
			if err := h.repo.Delete(existing.ID); err != nil {
				h.logger.Error("failed to delete old job", zap.Error(err))
				c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to remove old job"})
				return
			}
			// 继续创建新任务
		} else {
			h.logger.Info("job already exists", zap.String("job_id", existing.ID))
			if err := h.repo.Touch(existing.ID); err != nil {
				h.logger.Warn("failed to touch existing job",
					zap.String("job_id", existing.ID),
					zap.Error(err))
			}
			c.JSON(http.StatusOK, CreateJobResponse{
				JobID:   existing.ID,
				Status:  existing.Status,
				Message: "job already exists",
			})
			return
		}
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

	h.logger.Info("job created and queued",
		zap.String("job_id", job.ID),
		zap.String("source", job.Source),
		zap.String("track_id", job.TrackID))

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

	if err := h.repo.ResetForRetry(job.ID); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to update job"})
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

	if err := h.repo.Cancel(job.ID); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id":  job.ID,
		"status":  model.JobStatusCancelled,
		"message": "job cancelled successfully",
	})
}

// Delete 删除单个任务
func (h *JobHandler) Delete(c *gin.Context) {
	jobID := c.Param("id")

	_, err := h.repo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	if err := h.repo.Delete(jobID); err != nil {
		h.logger.Error("failed to delete job", zap.String("job_id", jobID), zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to delete job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "job deleted successfully",
	})
}

// BatchDelete 批量删除任务
func (h *JobHandler) BatchDelete(c *gin.Context) {
	var req struct {
		IDs []string `json:"ids" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request: ids is required"})
		return
	}

	if len(req.IDs) == 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "ids cannot be empty"})
		return
	}

	var deleted, failed int
	for _, id := range req.IDs {
		if err := h.repo.Delete(id); err != nil {
			h.logger.Warn("batch delete: failed to delete job", zap.String("job_id", id), zap.Error(err))
			failed++
		} else {
			deleted++
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"deleted": deleted,
		"failed":  failed,
		"message": fmt.Sprintf("deleted %d jobs, %d failed", deleted, failed),
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
		"version": h.version,
		"uptime":  time.Since(time.Now()).Seconds(),
		"components": gin.H{
			"database": "healthy",
			"queue":    "embedded",
		},
		"stats": gin.H{
			"queued_jobs": count,
		},
	})
}
