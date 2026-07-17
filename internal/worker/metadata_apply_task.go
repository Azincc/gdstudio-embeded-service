package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	metadatasvc "github.com/azin/gdstudio-embed-service/internal/service/metadata"
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"go.uber.org/zap"
)

type MetadataApplyTask struct {
	cfg        *config.Config
	repo       *repository.MetadataJobRepository
	resolver   *metadatasvc.Resolver
	tagger     *tagger.Tagger
	naviClient *navidrome.Client
	logger     *zap.Logger
}

func NewMetadataApplyTask(
	cfg *config.Config,
	repo *repository.MetadataJobRepository,
	resolver *metadatasvc.Resolver,
	tagger *tagger.Tagger,
	naviClient *navidrome.Client,
	logger *zap.Logger,
) *MetadataApplyTask {
	return &MetadataApplyTask{
		cfg:        cfg,
		repo:       repo,
		resolver:   resolver,
		tagger:     tagger,
		naviClient: naviClient,
		logger:     logger,
	}
}

func (t *MetadataApplyTask) ProcessJob(ctx context.Context, job *model.MetadataJob) error {
	if job == nil {
		return fmt.Errorf("metadata job is nil")
	}
	startedAt := time.Now()
	t.logger.Info("metadata apply job started",
		zap.String("job_id", job.ID),
		zap.String("song_id", job.SongID),
		zap.String("path", job.SongPath))

	filePath, err := metadatasvc.ResolveSongPath(t.cfg, job.SongPath)
	if err != nil {
		t.repo.MarkFailed(job.ID, err)
		return err
	}

	var editable model.EditableMetadata
	if err := json.Unmarshal([]byte(job.MetadataJSON), &editable); err != nil {
		t.repo.MarkFailed(job.ID, err)
		return fmt.Errorf("decode metadata json failed: %w", err)
	}

	if err := t.repo.UpdateStatus(job.ID, model.MetadataJobStatusResolvingCover, "resolving cover"); err != nil {
		t.logger.Warn("update metadata job status failed", zap.Error(err))
	}

	var coverData []byte
	if strings.TrimSpace(editable.CoverURL) != "" {
		data, coverErr := t.resolver.DownloadCover(ctx, editable.CoverURL)
		if coverErr != nil {
			t.logger.Warn("download cover failed",
				zap.String("job_id", job.ID),
				zap.String("cover_url", editable.CoverURL),
				zap.Error(coverErr))
		} else {
			coverData = data
		}
	}

	if err := t.repo.UpdateStatus(job.ID, model.MetadataJobStatusResolvingLyrics, "resolving lyrics"); err != nil {
		t.logger.Warn("update metadata job status failed", zap.Error(err))
	}

	if err := t.repo.UpdateStatus(job.ID, model.MetadataJobStatusTagging, "writing tags"); err != nil {
		t.logger.Warn("update metadata job status failed", zap.Error(err))
	}

	trackMetadata := &model.TrackMetadata{
		Title:       editable.Title,
		Artist:      editable.Artist,
		AlbumArtist: firstNonEmptyString(editable.AlbumArtist, editable.Artist),
		Album:       editable.Album,
		TrackNumber: editable.TrackNumber,
		DiscNumber:  editable.DiscNumber,
		Year:        editable.Year,
		Genre:       editable.Genre,
		Comment:     editable.Comment,
		Composer:    editable.Composer,
		Label:       editable.Label,
		Lyrics:      editable.Lyrics,
		CoverURL:    editable.CoverURL,
		CoverData:   coverData,
	}
	t.logger.Info("writing edited metadata tags",
		zap.String("job_id", job.ID),
		zap.String("song_id", job.SongID),
		zap.String("title", editable.Title),
		zap.String("artist", editable.Artist),
		zap.String("album", editable.Album),
		zap.Bool("has_cover", len(coverData) > 0),
		zap.Bool("has_lyrics", editable.Lyrics != ""))

	if err := t.tagger.WriteTags(filePath, trackMetadata); err != nil {
		t.repo.MarkFailed(job.ID, err)
		return fmt.Errorf("write tags failed: %w", err)
	}
	if editable.Lyrics != "" {
		if err := t.tagger.WriteLyricFile(filePath, editable.Lyrics); err != nil {
			t.logger.Warn("write lyric file failed",
				zap.String("job_id", job.ID),
				zap.Error(err))
		}
	}

	scanMessage := "metadata updated"
	if t.naviClient != nil {
		if err := t.repo.UpdateStatus(job.ID, model.MetadataJobStatusScanning, "triggering scan"); err != nil {
			t.logger.Warn("update metadata job status failed", zap.Error(err))
		}

		if err := t.naviClient.StartScan(); err != nil {
			t.logger.Warn("navidrome scan start failed",
				zap.String("job_id", job.ID),
				zap.Error(err))
			scanMessage = "metadata updated, scan trigger failed"
		} else if err := t.naviClient.WaitForScan(t.cfg.Worker.ScanTimeout); err != nil {
			t.logger.Warn("navidrome scan wait failed",
				zap.String("job_id", job.ID),
				zap.Error(err))
			scanMessage = "metadata updated, scan wait failed"
		}
	}

	if err := t.repo.MarkDone(job.ID, filePath, scanMessage); err != nil {
		return fmt.Errorf("mark metadata job done failed: %w", err)
	}
	t.logger.Info("metadata apply job completed",
		zap.String("job_id", job.ID),
		zap.String("song_id", job.SongID),
		zap.String("path", filePath),
		zap.String("message", scanMessage),
		zap.Duration("elapsed", time.Since(startedAt)))
	return nil
}
