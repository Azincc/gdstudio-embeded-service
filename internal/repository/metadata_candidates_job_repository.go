package repository

import (
	"errors"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/gorm"
)

var errMetadataCandidatesJobAlreadyClaimed = errors.New("metadata candidates job already claimed")

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

func (r *MetadataCandidatesJobRepository) UpdateStatus(id, leaseOwner, status, message string) error {
	updates := map[string]interface{}{
		"status":     status,
		"updated_at": time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	result := r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(updates)
	return metadataCandidatesLeaseUpdateError(result)
}

func (r *MetadataCandidatesJobRepository) MarkFailed(id, leaseOwner string, err error) error {
	result := r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(map[string]interface{}{
			"status":           model.MetadataCandidatesJobStatusFailed,
			"error":            err.Error(),
			"lease_owner":      "",
			"lease_expires_at": nil,
			"updated_at":       time.Now(),
		})
	return metadataCandidatesLeaseUpdateError(result)
}

func (r *MetadataCandidatesJobRepository) MarkDone(id, leaseOwner, resultJSON, message string) error {
	updates := map[string]interface{}{
		"status":           model.MetadataCandidatesJobStatusDone,
		"result_json":      resultJSON,
		"lease_owner":      "",
		"lease_expires_at": nil,
		"updated_at":       time.Now(),
	}
	if message != "" {
		updates["message"] = message
	}
	result := r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ? AND lease_owner = ?", id, leaseOwner).
		Updates(updates)
	return metadataCandidatesLeaseUpdateError(result)
}

func (r *MetadataCandidatesJobRepository) ClaimNext(owner string, leaseDuration time.Duration) (*model.MetadataCandidatesJob, error) {
	var claimed *model.MetadataCandidatesJob
	now := time.Now()
	expiresAt := now.Add(leaseDuration)
	recoverableStatuses := []string{
		model.MetadataCandidatesJobStatusSearchingSong,
		model.MetadataCandidatesJobStatusMergingData,
	}

	err := r.db.Transaction(func(tx *gorm.DB) error {
		var job model.MetadataCandidatesJob
		result := tx.Where(
			"status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?))",
			model.MetadataCandidatesJobStatusQueued,
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

		updateResult := tx.Model(&model.MetadataCandidatesJob{}).
			Where(
				"id = ? AND (status = ? OR (status IN ? AND (lease_expires_at IS NULL OR lease_expires_at <= ?)))",
				job.ID,
				model.MetadataCandidatesJobStatusQueued,
				recoverableStatuses,
				now,
			).
			Updates(map[string]interface{}{
				"status":           model.MetadataCandidatesJobStatusSearchingSong,
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
			return errMetadataCandidatesJobAlreadyClaimed
		}

		job.Status = model.MetadataCandidatesJobStatusSearchingSong
		job.Message = ""
		job.Error = ""
		job.LeaseOwner = owner
		job.LeaseExpiresAt = &expiresAt
		job.UpdatedAt = now
		claimed = &job
		return nil
	})
	if errors.Is(err, errMetadataCandidatesJobAlreadyClaimed) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return claimed, nil
}

func (r *MetadataCandidatesJobRepository) RenewLease(id, owner string, leaseDuration time.Duration) error {
	result := r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ? AND lease_owner = ? AND status NOT IN ?", id, owner, []string{
			model.MetadataCandidatesJobStatusDone,
			model.MetadataCandidatesJobStatusFailed,
		}).
		Update("lease_expires_at", time.Now().Add(leaseDuration))
	return metadataCandidatesLeaseUpdateError(result)
}

func (r *MetadataCandidatesJobRepository) ReleaseLease(id, owner string) error {
	return r.db.Model(&model.MetadataCandidatesJob{}).
		Where("id = ? AND lease_owner = ?", id, owner).
		Updates(map[string]interface{}{
			"lease_owner":      "",
			"lease_expires_at": nil,
		}).Error
}

func metadataCandidatesLeaseUpdateError(result *gorm.DB) error {
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrLeaseLost
	}
	return nil
}
