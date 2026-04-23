package repository

import (
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/gorm"
)

// MetadataCandidatesJobRepository manages async metadata candidate lookup jobs.
type MetadataCandidatesJobRepository struct {
	db *gorm.DB
}

func NewMetadataCandidatesJobRepository(db *gorm.DB) *MetadataCandidatesJobRepository {
	return &MetadataCandidatesJobRepository{db: db}
}

func (r *MetadataCandidatesJobRepository) Create(job *model.MetadataCandidatesJob) error {
	return r.db.Create(job).Error
}

func (r *MetadataCandidatesJobRepository) FindByID(id string) (*model.MetadataCandidatesJob, error) {
	var job model.MetadataCandidatesJob
	if err := r.db.Where("id = ?", id).First(&job).Error; err != nil {
		return nil, err
	}
	return &job, nil
}

func (r *MetadataCandidatesJobRepository) UpdateStatus(id, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	return r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ?", id).
		Updates(updates).Error
}

func (r *MetadataCandidatesJobRepository) MarkFailed(id string, err error) error {
	return r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ?", id).
		Updates(map[string]interface{}{
			"status":     model.MetadataCandidatesJobStatusFailed,
			"error":      err.Error(),
			"updated_at": time.Now(),
		}).Error
}

func (r *MetadataCandidatesJobRepository) MarkDone(id, resultJSON, message string) error {
	updates := map[string]interface{}{
		"status":      model.MetadataCandidatesJobStatusDone,
		"result_json": resultJSON,
		"updated_at":  time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	return r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ?", id).
		Updates(updates).Error
}
