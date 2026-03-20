package repository

import (
	"errors"
	"fmt"
	"strings"
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

// FindReliableAlbumArtist 查找同来源、同库、同专辑下已确认的专辑歌手。
func (r *JobRepository) FindReliableAlbumArtist(source, libraryID, album, excludeID string) (string, string, error) {
	album = strings.TrimSpace(album)
	if album == "" {
		return "", "", nil
	}

	var job model.Job
	query := r.db.
		Where("source = ? AND library_id = ? AND LOWER(TRIM(album)) = LOWER(TRIM(?))",
			source, libraryID, album).
		Where("album_artist <> ''").
		Where("album_artist_source IN ?", model.ReliableAlbumArtistSources())
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}

	err := query.
		Order("CASE album_artist_source " +
			"WHEN 'fingerprint' THEN 0 " +
			"WHEN 'musicbrainz' THEN 1 " +
			"WHEN 'album_shared' THEN 2 " +
			"ELSE 9 END").
		Order("updated_at DESC").
		First(&job).Error
	if err != nil {
		if err == gorm.ErrRecordNotFound {
			return "", "", nil
		}
		return "", "", err
	}

	return strings.TrimSpace(job.AlbumArtist), model.NormalizeAlbumArtistSource(job.AlbumArtistSource), nil
}

// PropagateReliableAlbumArtist 将可靠专辑歌手回填到尚未完成的同专辑任务，避免每首歌各自回退。
func (r *JobRepository) PropagateReliableAlbumArtist(source, libraryID, album, albumArtist, excludeID string) error {
	album = strings.TrimSpace(album)
	albumArtist = strings.TrimSpace(albumArtist)
	if album == "" || albumArtist == "" {
		return nil
	}

	incompleteStatuses := []string{
		model.JobStatusQueued,
		model.JobStatusResolving,
		model.JobStatusDownloading,
		model.JobStatusTagging,
		model.JobStatusMoving,
		model.JobStatusScanning,
	}

	query := r.db.Model(&model.Job{}).
		Where("source = ? AND library_id = ? AND LOWER(TRIM(album)) = LOWER(TRIM(?))",
			source, libraryID, album).
		Where("status IN ?", incompleteStatuses).
		Where("(album_artist = '' OR COALESCE(album_artist_source, '') NOT IN ?)",
			model.ReliableAlbumArtistSources())
	if excludeID != "" {
		query = query.Where("id <> ?", excludeID)
	}

	return query.Updates(map[string]interface{}{
		"album_artist":        albumArtist,
		"album_artist_source": model.AlbumArtistSourceAlbumShared,
		"updated_at":          time.Now(),
	}).Error
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

// ClaimNextQueued 原子领取一条最老的排队任务。
func (r *JobRepository) ClaimNextQueued() (*model.Job, error) {
	var claimed *model.Job

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.Job
		result := tx.Where("status = ?", model.JobStatusQueued).
			Order("created_at ASC").
			Limit(1).
			Find(&job)
		if result.Error != nil {
			return result.Error
		}
		if result.RowsAffected == 0 {
			return nil
		}

		now := time.Now()
		updateResult := tx.Model(&model.Job{}).
			Where("id = ? AND status = ?", job.ID, model.JobStatusQueued).
			Updates(map[string]interface{}{
				"status":     model.JobStatusResolving,
				"message":    "",
				"updated_at": now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errJobAlreadyClaimed
		}

		job.Status = model.JobStatusResolving
		job.Message = ""
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
	if err := db.AutoMigrate(&model.Job{}); err != nil {
		return fmt.Errorf("failed to migrate database: %w", err)
	}

	// 创建索引
	if err := db.Exec("CREATE INDEX IF NOT EXISTS idx_jobs_status_created ON jobs(status, created_at DESC)").Error; err != nil {
		return fmt.Errorf("failed to create index: %w", err)
	}

	return nil
}
