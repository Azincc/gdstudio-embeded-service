package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/metadata"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"go.uber.org/zap"
)

type MetadataHandler struct {
	resolver       *metadata.Resolver
	applyRepo      *repository.MetadataJobRepository
	candidatesRepo *repository.MetadataCandidatesJobRepository
	logger         *zap.Logger
}

func NewMetadataHandler(
	resolver *metadata.Resolver,
	applyRepo *repository.MetadataJobRepository,
	candidatesRepo *repository.MetadataCandidatesJobRepository,
	logger *zap.Logger,
) *MetadataHandler {
	return &MetadataHandler{
		resolver:       resolver,
		applyRepo:      applyRepo,
		candidatesRepo: candidatesRepo,
		logger:         logger,
	}
}

type MetadataCandidatesRequest struct {
	Song model.SongMetadataReference `json:"song" binding:"required"`
}

type MetadataApplyRequest struct {
	Song struct {
		ID        string `json:"id"`
		Path      string `json:"path"`
		LibraryID string `json:"libraryId"`
	} `json:"song" binding:"required"`
	Metadata model.EditableMetadata `json:"metadata" binding:"required"`
}

func (h *MetadataHandler) Candidates(c *gin.Context) {
	var req MetadataCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	response, _, err := h.resolver.ResolveCandidates(c.Request.Context(), req.Song, nil)
	if err != nil {
		h.logger.Warn("resolve metadata candidates failed",
			zap.String("song_id", req.Song.ID),
			zap.String("path", req.Song.Path),
			zap.Error(err))
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	c.JSON(http.StatusOK, response)
}

func (h *MetadataHandler) CreateCandidatesJob(c *gin.Context) {
	var req MetadataCandidatesRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	job := &model.MetadataCandidatesJob{
		ID:        uuid.New().String(),
		SongID:    strings.TrimSpace(req.Song.ID),
		LibraryID: strings.TrimSpace(req.Song.LibraryID),
		SongPath:  strings.TrimSpace(req.Song.Path),
		Status:    model.MetadataCandidatesJobStatusQueued,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err := h.candidatesRepo.Create(job); err != nil {
		h.logger.Error("create metadata candidates job failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create metadata candidates job"})
		return
	}
	h.logger.Info("metadata candidates job queued",
		zap.String("job_id", job.ID),
		zap.String("song_id", job.SongID),
		zap.String("library_id", job.LibraryID),
		zap.String("path", job.SongPath),
		zap.String("title", req.Song.Title),
		zap.String("artist", req.Song.Artist))

	go h.processCandidatesJob(job.ID, req.Song)

	c.JSON(http.StatusOK, gin.H{
		"job_id": job.ID,
		"status": job.Status,
	})
}

func (h *MetadataHandler) GetCandidatesJob(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.candidatesRepo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}

	var result *model.MetadataCandidatesResponse
	if strings.TrimSpace(job.ResultJSON) != "" {
		var parsed model.MetadataCandidatesResponse
		if err := json.Unmarshal([]byte(job.ResultJSON), &parsed); err != nil {
			h.logger.Error("decode metadata candidates job result failed",
				zap.String("job_id", job.ID),
				zap.Error(err))
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to decode metadata candidates result"})
			return
		}
		result = &parsed
	}

	c.JSON(http.StatusOK, model.MetadataCandidatesJobResponse{
		JobID:     job.ID,
		Status:    job.Status,
		Message:   job.Message,
		Error:     job.Error,
		Result:    result,
		CreatedAt: job.CreatedAt,
		UpdatedAt: job.UpdatedAt,
	})
}

func (h *MetadataHandler) Apply(c *gin.Context) {
	var req MetadataApplyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}

	if strings.TrimSpace(req.Song.Path) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "song.path is required"})
		return
	}
	if strings.TrimSpace(req.Metadata.Title) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata.title is required"})
		return
	}
	if strings.TrimSpace(req.Metadata.Artist) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "metadata.artist is required"})
		return
	}

	metadataJSON, err := json.Marshal(req.Metadata)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to encode metadata"})
		return
	}

	job := &model.MetadataJob{
		ID:           uuid.New().String(),
		SongID:       strings.TrimSpace(req.Song.ID),
		LibraryID:    strings.TrimSpace(req.Song.LibraryID),
		SongPath:     strings.TrimSpace(req.Song.Path),
		MetadataJSON: string(metadataJSON),
		Status:       model.MetadataJobStatusQueued,
		CreatedAt:    time.Now(),
		UpdatedAt:    time.Now(),
	}
	if err := h.applyRepo.Create(job); err != nil {
		h.logger.Error("create metadata job failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create metadata job"})
		return
	}
	h.logger.Info("metadata apply job queued",
		zap.String("job_id", job.ID),
		zap.String("song_id", job.SongID),
		zap.String("library_id", job.LibraryID),
		zap.String("path", job.SongPath),
		zap.String("title", req.Metadata.Title),
		zap.String("artist", req.Metadata.Artist))

	c.JSON(http.StatusOK, gin.H{
		"job_id": job.ID,
		"status": job.Status,
	})
}

func (h *MetadataHandler) GetJob(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.applyRepo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}

func (h *MetadataHandler) processCandidatesJob(jobID string, song model.SongMetadataReference) {
	startedAt := time.Now()
	h.logger.Info("metadata candidates job started",
		zap.String("job_id", jobID),
		zap.String("song_id", song.ID),
		zap.String("title", song.Title),
		zap.String("artist", song.Artist),
		zap.Strings("sources", []string{"netease", "kuwo"}))

	reportProgress := func(status, message string) {
		if err := h.candidatesRepo.UpdateStatus(jobID, status, message); err != nil {
			h.logger.Warn("update metadata candidates job status failed",
				zap.String("job_id", jobID),
				zap.Error(err))
			return
		}
		h.logger.Info("metadata candidates job status changed",
			zap.String("job_id", jobID),
			zap.String("song_id", song.ID),
			zap.String("status", status))
	}

	response, _, err := h.resolver.ResolveCandidates(context.Background(), song, reportProgress)
	if err != nil {
		h.logger.Warn("process metadata candidates job failed",
			zap.String("job_id", jobID),
			zap.String("song_id", song.ID),
			zap.Duration("elapsed", time.Since(startedAt)),
			zap.Error(err))
		if markErr := h.candidatesRepo.MarkFailed(jobID, err); markErr != nil {
			h.logger.Warn("mark metadata candidates job failed failed",
				zap.String("job_id", jobID),
				zap.Error(markErr))
		}
		return
	}

	resultJSON, err := json.Marshal(response)
	if err != nil {
		marshalErr := fmt.Errorf("encode metadata candidates response failed: %w", err)
		h.logger.Error("encode metadata candidates job result failed",
			zap.String("job_id", jobID),
			zap.String("song_id", song.ID),
			zap.Error(marshalErr))
		if markErr := h.candidatesRepo.MarkFailed(jobID, marshalErr); markErr != nil {
			h.logger.Warn("mark metadata candidates job failed after encode error",
				zap.String("job_id", jobID),
				zap.Error(markErr))
		}
		return
	}

	if err := h.candidatesRepo.MarkDone(jobID, string(resultJSON), "candidates ready"); err != nil {
		h.logger.Warn("mark metadata candidates job done failed",
			zap.String("job_id", jobID),
			zap.Error(err))
		return
	}
	h.logger.Info("metadata candidates job completed",
		zap.String("job_id", jobID),
		zap.String("song_id", song.ID),
		zap.Int("candidate_count", len(response.Candidates)),
		zap.Duration("elapsed", time.Since(startedAt)))
}
