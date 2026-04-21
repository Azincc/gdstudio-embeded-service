package repository

import (
	"errors"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/gorm"
)

var errMetadataJobAlreadyClaimed = errors.New("metadata job already claimed")

// MetadataJobRepository manages metadata apply jobs.
type MetadataJobRepository struct {
	db *gorm.DB
}

func NewMetadataJobRepository(db *gorm.DB) *MetadataJobRepository {
	return &MetadataJobRepository{db: db}
}

func (r *MetadataJobRepository) Create(job *model.MetadataJob) error {
	return r.db.Create(job).Error
}

func (r *MetadataJobRepository) FindByID(id string) (*model.MetadataJob, error) {
	var job model.MetadataJob
	if err := r.db.Where("id = ?", id).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *MetadataJobRepository) UpdateStatus(id, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	return r.db.Model(&model.MetadataJob{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *MetadataJobRepository) MarkFailed(id string, err error) error {
	return r.db.Model(&model.MetadataJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     model.MetadataJobStatusFailed,
			"error":      err.Error(),
			"updated_at": time.Now(),
		}).Error
}

func (r *MetadataJobRepository) MarkDone(id, filePath, message string) error {
	updates := map[string]interface{}{
		"status":     model.MetadataJobStatusDone,
		"file_path":  filePath,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	return r.db.Model(&model.MetadataJob{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *MetadataJobRepository) ClaimNextQueued() (*model.MetadataJob, error) {
	var claimed *model.MetadataJob

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.MetadataJob
		result := tx.Where("status = ?", model.MetadataJobStatusQueued).
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
		updateResult := tx.Model(&model.MetadataJob{}).
			Where("id = ? AND status = ?", job.ID, model.MetadataJobStatusQueued).
			Updates(map[string]interface{}{
				"status":     model.MetadataJobStatusReading,
				"message":    "",
				"updated_at": now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errMetadataJobAlreadyClaimed
		}

		job.Status = model.MetadataJobStatusReading
		job.Message = ""
		job.UpdatedAt = now
		claimed = &job
		return nil
	})

	if errors.Is(err, errMetadataJobAlreadyClaimed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return claimed, nil
}
