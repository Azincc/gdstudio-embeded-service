package repository

import (
	"fmt"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/gorm"
)

// JobRepository 任务仓库
type JobRepository struct {
	db *gorm.DB
}

// NewJobRepository 创建任务仓库
func NewJobRepository(db *gorm.DB) *JobRepository {
	return &JobRepository{db: db}
}

// Create 创建任务
func (r *JobRepository) Create(job *model.Job) error {
	return r.db.Create(job).Error
}

// FindByID 根据 ID 查询任务
func (r *JobRepository) FindByID(id string) (*model.Job, error) {
	var job model.Job
	err := r.db.Where("id = ?", id).First(&job).Error
	if err != nil {
		return nil, err
	}
	return &job, nil
}

// FindByIdempotencyKey 根据幂等键查询任务
func (r *JobRepository) FindByIdempotencyKey(key string) (*model.Job, error) {
	var job model.Job
	err := r.db.Where("idempotency_key = ?", key).First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &job, nil
}

// Update 更新任务
func (r *JobRepository) Update(job *model.Job) error {
	job.UpdatedAt = time.Now()
	return r.db.Save(job).Error
}

// UpdateStatus 更新任务状态
func (r *JobRepository) UpdateStatus(id, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}

	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Updates(updates).Error
}

// UpdateProgress 更新任务进度
func (r *JobRepository) UpdateProgress(id string, progress int, completedBytes, totalBytes int64) error {
	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"progress":        progress,
			"completed_bytes": completedBytes,
			"total_bytes":     totalBytes,
			"updated_at":      time.Now(),
		}).Error
}

// MarkFailed 标记任务失败
func (r *JobRepository) MarkFailed(id string, err error) error {
	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     model.JobStatusFailed,
			"error":      err.Error(),
			"updated_at": time.Now(),
		}).Error
}

// MarkDone 标记任务完成
func (r *JobRepository) MarkDone(id, filePath string, fileSize int64) error {
	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     model.JobStatusDone,
			"file_path":  filePath,
			"file_size":  fileSize,
			"progress":   100,
			"updated_at": time.Now(),
		}).Error
}

// ListByStatus 根据状态查询任务列表
func (r *JobRepository) ListByStatus(status string, limit int) ([]*model.Job, error) {
	var jobs []*model.Job
	err := r.db.Where("status = ?", status).
		Order("created_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// ListRecent 查询最近的任务
func (r *JobRepository) ListRecent(limit int) ([]*model.Job, error) {
	var jobs []*model.Job
	err := r.db.Order("created_at DESC").
		Limit(limit).
		Find(&jobs).Error
	return jobs, err
}

// IncrementRetry 增加重试次数
func (r *JobRepository) IncrementRetry(id string) error {
	now := time.Now()
	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"retry_count":   gorm.Expr("retry_count + 1"),
			"last_retry_at": &now,
			"updated_at":    now,
		}).Error
}

// CountByStatus 统计指定状态的任务数量
func (r *JobRepository) CountByStatus(status string) (int64, error) {
	var count int64
	err := r.db.Model(&model.Job{}).Where("status = ?", status).Count(&count).Error
	return count, err
}

// DeleteOldJobs 删除旧任务（已完成或失败超过指定天数）
func (r *JobRepository) DeleteOldJobs(days int) error {
	cutoff := time.Now().AddDate(0, 0, -days)
	return r.db.Where("status IN (?, ?) AND updated_at < ?",
		model.JobStatusDone,
		model.JobStatusFailed,
		cutoff).
		Delete(&model.Job{}).Error
}

// InitDB 初始化数据库
func InitDB(db *gorm.DB) error {
	// 自动迁移表结构
	if err := db.AutoMigrate(&model.Job{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// 创建索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs(status, created_at DESC)").Error; err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}

	return nil
}
