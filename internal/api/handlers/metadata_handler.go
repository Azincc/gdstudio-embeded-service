package handlers

import (
	"encoding/json"
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
	resolver *metadata.Resolver
	repo     *repository.MetadataJobRepository
	logger   *zap.Logger
}

func NewMetadataHandler(
	resolver *metadata.Resolver,
	repo *repository.MetadataJobRepository,
	logger *zap.Logger,
) *MetadataHandler {
	return &MetadataHandler{
		resolver: resolver,
		repo:     repo,
		logger:   logger,
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

	response, _, err := h.resolver.ResolveCandidates(req.Song)
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
	if err := h.repo.Create(job); err != nil {
		h.logger.Error("create metadata job failed", zap.Error(err))
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to create metadata job"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"job_id": job.ID,
		"status": job.Status,
	})
}

func (h *MetadataHandler) GetJob(c *gin.Context) {
	jobID := c.Param("id")
	job, err := h.repo.FindByID(jobID)
	if err != nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "job not found"})
		return
	}
	c.JSON(http.StatusOK, job)
}
