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

func (r *MetadataJobRepository) UpdateStatus(id, leaseOwner, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	result := r.db.Model(&model.MetadataJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(updates)
	return metadataLeaseUpdateError(result)
}

func (r *MetadataJobRepository) MarkFailed(id, leaseOwner string, err error) error {
	result := r.db.Model(&model.MetadataJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(map[string]interface{}{
			"status":           model.MetadataJobStatusFailed,
			"error":            err.Error(),
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	return metadataLeaseUpdateError(result)
}

func (r *MetadataJobRepository) MarkDone(id, leaseOwner, filePath, message string) error {
	updates := map[string]interface{}{
		"status":           model.MetadataJobStatusDone,
		"file_path":        filePath,
		"lease_owner":      "",
		"lease_expires_at": nil,
		"updated_at":       time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	result := r.db.Model(&model.MetadataJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(updates)
	return metadataLeaseUpdateError(result)
}

func (r *MetadataJobRepository) ClaimNextQueued(owner string, leaseDuration time.Duration) (*model.MetadataJob, error) {
	var claimed *model.MetadataJob
	now := time.Now()
	expiresAt := now.Add(leaseDuration)
	recoverableStatuses := []string{
		model.MetadataJobStatusReading,
		model.MetadataJobStatusResolvingCover,
		model.MetadataJobStatusResolvingLyrics,
		model.MetadataJobStatusTagging,
		model.MetadataJobStatusScanning,
	}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.MetadataJob
		result := tx.Where(
			"status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?))",
			model.MetadataJobStatusQueued,
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

		updateResult := tx.Model(&model.MetadataJob{}).
			Where(
				"id = ? AND (status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
				job.ID,
				model.MetadataJobStatusQueued,
				recoverableStatuses,
				now,
			).
			Updates(map[string]interface{}{
				"status":           model.MetadataJobStatusReading,
				"message":          "",
				"error":            "",
				"lease_owner":      owner,
				"lease_expires_at": expiresAt,
				"updated_at":       now,
			})
		if updateResult.Error != nil {
			return updateResult.Error
		}
		if updateResult.RowsAffected == 0 {
			return errMetadataJobAlreadyClaimed
		}

		job.Status = model.MetadataJobStatusReading
		job.Message = ""
		job.Error = ""
		job.LeaseOwner = owner
		job.LeaseExpiresAt = &expiresAt
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

func (r *MetadataJobRepository) RenewLease(id, owner string, leaseDuration time.Duration) error {
	result := r.db.Model(&model.MetadataJob{}).
		Where("id = ? AND lease_owner = ? AND status NOT IN ?", id, owner, []string{
			model.MetadataJobStatusDone,
			model.MetadataJobStatusFailed,
		}).
		Update("lease_expires_at", time.Now().Add(leaseDuration))
	return metadataLeaseUpdateError(result)
}

func (r *MetadataJobRepository) ReleaseLease(id, owner string) error {
	return r.db.Model(&model.MetadataJob{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Updates(map[string]interface{}{
			"lease_owner":      "",
			"lease_expires_at": nil,
		}).Error
}

func metadataLeaseUpdateError(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}
