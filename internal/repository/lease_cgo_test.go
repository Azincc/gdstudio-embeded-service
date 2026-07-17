//go:build cgo

package repository

import (
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func openLeaseTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dsn := "file:" + filepath.ToSlash(filepath.Join(t.TempDir(), "lease.db")) + "?_journal_mode=WAL&_busy_timeout=5000"
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&model.Job{}, &model.MetadataJob{}, &model.MetadataCandidatesJob{}); err != nil {
		t.Fatalf("migrate sqlite failed: %v", err)
	}
	return db
}

func TestExpiredDownloadLeaseIsRecoveredAndCancellationIsFenced(t *testing.T) {
	db := openLeaseTestDB(t)
	repo := NewJobRepository(db)
	expiredAt := time.Now().Add(-time.Minute)
	job := &model.Job{
		ID:             "expired-download",
		IdempotencyKey: "expired-download",
		Source:         "netease",
		TrackID:        "track-1",
		LibraryID:      "library-1",
		Status:         model.JobStatusDownloading,
		LeaseOwner:     "dead-worker",
		LeaseExpiresAt: &expiredAt,
	}
	if err := repo.Create(job); err != nil {
		t.Fatalf("create job failed: %v", err)
	}

	claimed, err := repo.ClaimNextQueued("new-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim expired job failed: %v", err)
	}
	if claimed == nil || claimed.ID != job.ID || claimed.LeaseOwner != "new-worker" {
		t.Fatalf("unexpected claimed job: %#v", claimed)
	}
	if claimed.Status != model.JobStatusResolving {
		t.Fatalf("expected recovered job to restart at resolving, got %q", claimed.Status)
	}

	if err := repo.Cancel(job.ID); err != nil {
		t.Fatalf("cancel job failed: %v", err)
	}
	if err := repo.UpdateStatus(job.ID, "new-worker", model.JobStatusDownloading, ""); !errors.Is(err, ErrJobCancelled) {
		t.Fatalf("expected cancelled status fencing, got %v", err)
	}
	stored, err := repo.FindByID(job.ID)
	if err != nil {
		t.Fatalf("find cancelled job failed: %v", err)
	}
	if stored.Status != model.JobStatusCancelled {
		t.Fatalf("cancelled status was overwritten: %q", stored.Status)
	}

	postJob := &model.Job{
		ID:             "expired-postprocess",
		IdempotencyKey: "expired-postprocess",
		Source:         "netease",
		TrackID:        "track-2",
		LibraryID:      "library-1",
		Status:         model.JobStatusMoving,
		LeaseOwner:     "dead-post-worker",
		LeaseExpiresAt: &expiredAt,
	}
	if err := repo.Create(postJob); err != nil {
		t.Fatalf("create post-process job failed: %v", err)
	}
	claimedPost, err := repo.ClaimNextPostProcess(
		"new-post-worker",
		time.Minute,
		[]string{model.JobStatusTagging, model.JobStatusMoving, model.JobStatusScanning},
	)
	if err != nil {
		t.Fatalf("claim expired post-process job failed: %v", err)
	}
	if claimedPost == nil || claimedPost.Status != model.JobStatusMoving || claimedPost.LeaseOwner != "new-post-worker" {
		t.Fatalf("post-process job was not recovered in place: %#v", claimedPost)
	}
}

func TestExpiredMetadataLeasesAreRecovered(t *testing.T) {
	db := openLeaseTestDB(t)
	expiredAt := time.Now().Add(-time.Minute)

	metadataRepo := NewMetadataJobRepository(db)
	metadataJob := &model.MetadataJob{
		ID:             "expired-metadata",
		SongID:         "song-1",
		SongPath:       "song.mp3",
		MetadataJSON:   `{}`,
		Status:         model.MetadataJobStatusTagging,
		LeaseOwner:     "dead-worker",
		LeaseExpiresAt: &expiredAt,
	}
	if err := metadataRepo.Create(metadataJob); err != nil {
		t.Fatalf("create metadata job failed: %v", err)
	}
	claimedMetadata, err := metadataRepo.ClaimNextQueued("metadata-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim metadata job failed: %v", err)
	}
	if claimedMetadata == nil || claimedMetadata.Status != model.MetadataJobStatusReading {
		t.Fatalf("metadata job was not recovered: %#v", claimedMetadata)
	}

	candidatesRepo := NewMetadataCandidatesJobRepository(db)
	candidatesJob := &model.MetadataCandidatesJob{
		ID:             "expired-candidates",
		SongID:         "song-2",
		SongPath:       "song.mp3",
		RequestJSON:    `{"id":"song-2","path":"song.mp3"}`,
		Status:         model.MetadataCandidatesJobStatusSearchingSong,
		LeaseOwner:     "dead-api",
		LeaseExpiresAt: &expiredAt,
	}
	if err := candidatesRepo.Create(candidatesJob); err != nil {
		t.Fatalf("create candidates job failed: %v", err)
	}
	claimedCandidates, err := candidatesRepo.ClaimNext("candidate-worker", time.Minute)
	if err != nil {
		t.Fatalf("claim candidates job failed: %v", err)
	}
	if claimedCandidates == nil || claimedCandidates.LeaseOwner != "candidate-worker" {
		t.Fatalf("candidates job was not recovered: %#v", claimedCandidates)
	}
}
