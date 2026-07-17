package repository

import (
	"errors"
	"fmt"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/gorm"
)

// JobRepository 任务仓库
type JobRepository struct {
	db *gorm.DB
}

var errJobAlreadyClaimed = errors.New("job already claimed")

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

// UpdateLeased 更新任务业务字段，但不会覆盖状态和租约。
func (r *JobRepository) UpdateLeased(job *model.Job, leaseOwner string) error {
	updates := map[string]interface{}{
		"pic_id":              job.PicID,
		"lyric_id":            job.LyricID,
		"quality":             job.Quality,
		"title":               job.Title,
		"artist":              job.Artist,
		"album_artist":        job.AlbumArtist,
		"album_artist_source": job.AlbumArtistSource,
		"album":               job.Album,
		"track_number":        job.TrackNumber,
		"year":                job.Year,
		"message":             job.Message,
		"progress":            job.Progress,
		"total_bytes":         job.TotalBytes,
		"completed_bytes":     job.CompletedBytes,
		"file_path":           job.FilePath,
		"file_size":           job.FileSize,
		"duration":            job.Duration,
		"bitrate":             job.Bitrate,
		"updated_at":          time.Now(),
	}
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", job.ID, leaseOwner, model.JobStatusCancelled).
		Updates(updates)
	return r.leasedUpdateError(result, job.ID)
}

// Touch 刷新任务时间戳，不改变创建时间或队列顺序。
func (r *JobRepository) Touch(id string) error {
	return r.db.Model(&model.Job{}).
		Where("id = ?", id).
		Update("updated_at", time.Now()).Error
}

// ResetForRetry 把失败任务重新放回队列。
func (r *JobRepository) ResetForRetry(id string) error {
	now := time.Now()
	return r.db.Model(&model.Job{}).
		Where("id = ? AND status = ?", id, model.JobStatusFailed).
		Updates(map[string]interface{}{
			"status":           model.JobStatusQueued,
			"error":            "",
			"message":          "retrying",
			"progress":         0,
			"completed_bytes":  0,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       now,
		}).Error
}

// Cancel 将非终态任务原子标记为取消，并立即撤销其租约。
func (r *JobRepository) Cancel(id string) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND status NOT IN ?", id, []string{
			model.JobStatusDone,
			model.JobStatusFailed,
			model.JobStatusCancelled,
		}).
		Updates(map[string]interface{}{
			"status":           model.JobStatusCancelled,
			"message":          "cancelled by user",
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	job, err := r.FindByID(id)
	if err != nil {
		return err
	}
	if job.Status == model.JobStatusCancelled {
		return nil
	}
	return fmt.Errorf("cannot cancel job in status %s", job.Status)
}

// IsCancelled 查询任务是否已经被用户取消。
func (r *JobRepository) IsCancelled(id string) (bool, error) {
	var job model.Job
	if err := r.db.Select("status").Where("id = ?", id).First(&job).Error; err != nil {
		return false, err
	}
	return job.Status == model.JobStatusCancelled, nil
}

// UpdateStatus 更新租约持有者所处理任务的状态。
func (r *JobRepository) UpdateStatus(id, leaseOwner, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}

	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", id, leaseOwner, model.JobStatusCancelled).
		Updates(updates)
	return r.leasedUpdateError(result, id)
}

// UpdateProgress 更新任务进度
func (r *JobRepository) UpdateProgress(id, leaseOwner string, progress int, completedBytes, totalBytes int64) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", id, leaseOwner, model.JobStatusCancelled).
		Updates(map[string]interface{}{
			"progress":        progress,
			"completed_bytes": completedBytes,
			"total_bytes":     totalBytes,
			"updated_at":      time.Now(),
		})
	return r.leasedUpdateError(result, id)
}

// MarkFailed 标记任务失败
func (r *JobRepository) MarkFailed(id, leaseOwner string, err error) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", id, leaseOwner, model.JobStatusCancelled).
		Updates(map[string]interface{}{
			"status":           model.JobStatusFailed,
			"error":            err.Error(),
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	return r.leasedUpdateError(result, id)
}

// MarkDone 标记任务完成
func (r *JobRepository) MarkDone(id, leaseOwner, filePath string, fileSize int64) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", id, leaseOwner, model.JobStatusCancelled).
		Updates(map[string]interface{}{
			"status":           model.JobStatusDone,
			"file_path":        filePath,
			"file_size":        fileSize,
			"progress":         100,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	return r.leasedUpdateError(result, id)
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

// ClaimNextPostProcess 原子领取一条可处理或租约已过期的后处理任务。
func (r *JobRepository) ClaimNextPostProcess(owner string, leaseDuration time.Duration, statuses []string) (*model.Job, error) {
	if len(statuses) == 0 {
		return nil, nil
	}

	var claimed *model.Job
	now := time.Now()
	expiresAt := now.Add(leaseDuration)
	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.Job
		result := tx.Where(
			"status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)",
			statuses,
			now,
		).
			Order("created_at ASC").
			Limit(1).
			Find(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		updateResult := tx.Model(&model.Job{}).
			Where(
				"id = ? AND status = ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)",
				job.ID,
				job.Status,
				now,
			).
			Updates(map[string]interface{}{
				"lease_owner":      owner,
				"lease_expires_at": expiresAt,
				"updated_at":       now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errJobAlreadyClaimed
		}

		job.LeaseOwner = owner
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		claimed = &job
		return nil
	})
	if errors.Is(err, errJobAlreadyClaimed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return claimed, nil
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

// ClaimNextQueued 原子领取一条最老的排队任务，或恢复租约已过期的下载任务。
func (r *JobRepository) ClaimNextQueued(owner string, leaseDuration time.Duration) (*model.Job, error) {
	var claimed *model.Job
	now := time.Now()
	expiresAt := now.Add(leaseDuration)
	recoverableStatuses := []string{model.JobStatusResolving, model.JobStatusDownloading}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.Job
		result := tx.Where(
			"status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?))",
			model.JobStatusQueued,
			recoverableStatuses,
			now,
		).
			Order("created_at ASC").
			Limit(1).
			Find(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		updateResult := tx.Model(&model.Job{}).
			Where(
				"id = ? AND (status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
				job.ID,
				model.JobStatusQueued,
				recoverableStatuses,
				now,
			).
			Updates(map[string]interface{}{
				"status":           model.JobStatusResolving,
				"message":          "",
				"error":            "",
				"progress":         0,
				"completed_bytes":  0,
				"lease_owner":      owner,
				"lease_expires_at": expiresAt,
				"updated_at":       now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errJobAlreadyClaimed
		}

		job.Status = model.JobStatusResolving
		job.Message = ""
		job.Error = ""
		job.Progress = 0
		job.CompletedBytes = 0
		job.LeaseOwner = owner
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		claimed = &job
		return nil
	})
	if errors.Is(err, errJobAlreadyClaimed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	return claimed, nil
}

// QueuePostProcess 把下载完成的任务交给独立后处理循环，并释放当前租约。
func (r *JobRepository) QueuePostProcess(id, leaseOwner string) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status <> ?", id, leaseOwner, model.JobStatusCancelled).
		Updates(map[string]interface{}{
			"status":           model.JobStatusTagging,
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	return r.leasedUpdateError(result, id)
}

// RenewLease 延长任务租约。
func (r *JobRepository) RenewLease(id, leaseOwner string, leaseDuration time.Duration) error {
	result := r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ? AND status NOT IN ?", id, leaseOwner, []string{
			model.JobStatusDone,
			model.JobStatusFailed,
			model.JobStatusCancelled,
		}).
		Update("lease_expires_at", time.Now().Add(leaseDuration))
	return r.leasedUpdateError(result, id)
}

// ReleaseLease 主动释放租约，让其他 Worker 可以立即恢复任务。
func (r *JobRepository) ReleaseLease(id, leaseOwner string) error {
	return r.db.Model(&model.Job{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(map[string]interface{}{
			"lease_owner":      "",
			"lease_expires_at": nil,
		}).Error
}

func (r *JobRepository) leasedUpdateError(result *gorm.DB, id string) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected > 0 {
		return nil
	}

	job, err := r.FindByID(id)
	if err != nil {
		return err
	}
	if job.Status == model.JobStatusCancelled {
		return ErrJobCancelled
	}
	return ErrLeaseLost
}

// Delete 永久删除任务（用于 force 重新下载时清除幂等记录）
func (r *JobRepository) Delete(id string) error {
	return r.db.Unscoped().Where("id = ?", id).Delete(&model.Job{}).Error
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
	if err := db.AutoMigrate(&model.Job{}, &model.MetadataJob{}, &model.MetadataCandidatesJob{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// 创建索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs(status, created_at DESC)").Error; err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_metadata_jobs_status_created ON metadata_jobs(status, created_at DESC)").Error; err != nil {
		return fmt.Errorf("failed to create metadata job index: %w", err)
	}
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_metadata_candidate_jobs_status_created ON metadata_candidate_jobs(status, created_at DESC)").Error; err != nil {
		return fmt.Errorf("failed to create metadata candidate job index: %w", err)
	}

	return nil
}
