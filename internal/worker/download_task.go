package worker

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/repository"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"go.uber.org/zap"
)

const (
	// 封面/歌词获取的重试参数
	auxMaxRetries    = 3
	auxRetryBaseWait = 1 * time.Second
	auxMaxWait       = 8 * time.Second
)

// DownloadPayload 下载任务载荷
type DownloadPayload struct {
	JobID      string `json:"job_id"`
	Source     string `json:"source"`
	TrackID    string `json:"track_id"`
	PicID      string `json:"pic_id,omitempty"`
	LyricID    string `json:"lyric_id,omitempty"`
	LibraryID  string `json:"library_id"`
	Quality    string `json:"quality"`
	LeaseOwner string `json:"-"`
}

// DownloadTask 下载任务处理器
type DownloadTask struct {
	cfg        *config.Config
	repo       *repository.JobRepository
	gdClient   *gdstudio.Client
	naviClient *navidrome.Client
	tagger     *tagger.Tagger
	logger     *zap.Logger
}

// NewDownloadTask 创建下载任务处理器
func NewDownloadTask(
	cfg *config.Config,
	repo *repository.JobRepository,
	gdClient *gdstudio.Client,
	naviClient *navidrome.Client,
	tagger *tagger.Tagger,
	logger *zap.Logger,
) *DownloadTask {
	return &DownloadTask{
		cfg:        cfg,
		repo:       repo,
		gdClient:   gdClient,
		naviClient: naviClient,
		tagger:     tagger,
		logger:     logger,
	}
}

// ProcessPayload 处理任务。
func (t *DownloadTask) ProcessPayload(ctx context.Context, payload *DownloadPayload) error {
	if err := t.ProcessDownloadPayload(ctx, payload); err != nil {
		return err
	}
	return t.ProcessPostProcessPayload(ctx, payload)
}

// DownloadTimeout 返回下载阶段的超时时间。
func (t *DownloadTask) DownloadTimeout() time.Duration {
	if t == nil || t.cfg == nil {
		return 0
	}
	return t.cfg.Worker.DownloadTimeout
}

// ProcessDownloadPayload 处理解析和下载阶段，完成后把任务推进到 tagging。
func (t *DownloadTask) ProcessDownloadPayload(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("processing download task",
		zap.String("job_id", payload.JobID),
		zap.String("source", payload.Source),
		zap.String("track_id", payload.TrackID))

	stages := []struct {
		name string
		fn   func(context.Context, *DownloadPayload) error
	}{
		{model.JobStatusResolving, t.stageResolve},
		{model.JobStatusDownloading, t.stageDownload},
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return t.handleContextStop(payload, err)
		}
		if err := t.repo.UpdateStatus(payload.JobID, payload.LeaseOwner, stage.name, ""); err != nil {
			return fmt.Errorf("failed to update status to %s: %w", stage.name, err)
		}

		// 执行阶段
		if err := stage.fn(ctx, payload); err != nil {
			t.logger.Error("stage failed",
				zap.String("stage", stage.name),
				zap.String("job_id", payload.JobID),
				zap.Error(err))

			if t.shouldLeaveForRecovery(ctx, err) {
				return fmt.Errorf("%s interrupted: %w", stage.name, err)
			}

			if markErr := t.repo.MarkFailed(payload.JobID, payload.LeaseOwner, err); markErr != nil &&
				!errors.Is(markErr, repository.ErrJobCancelled) &&
				!errors.Is(markErr, repository.ErrLeaseLost) {
				t.logger.Error("failed to mark job as failed", zap.Error(markErr))
			}

			return fmt.Errorf("%s failed: %w", stage.name, err)
		}
		if err := ctx.Err(); err != nil {
			return t.handleContextStop(payload, err)
		}
	}

	if err := ctx.Err(); err != nil {
		return t.handleContextStop(payload, err)
	}
	if err := t.repo.QueuePostProcess(payload.JobID, payload.LeaseOwner); err != nil {
		if markErr := t.repo.MarkFailed(payload.JobID, payload.LeaseOwner, err); markErr != nil &&
			!errors.Is(markErr, repository.ErrJobCancelled) &&
			!errors.Is(markErr, repository.ErrLeaseLost) {
			t.logger.Error("failed to mark job as failed", zap.Error(markErr))
		}
		return fmt.Errorf("failed to queue post-processing: %w", err)
	}

	t.logger.Info("download stages completed",
		zap.String("job_id", payload.JobID),
		zap.String("next_status", model.JobStatusTagging))

	return nil
}

// ProcessPostProcessPayload 处理打标签、移动和扫描阶段。
func (t *DownloadTask) ProcessPostProcessPayload(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("processing post-download stages", zap.String("job_id", payload.JobID))

	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	stages := t.postProcessStages(job.Status)
	if len(stages) == 0 {
		return nil
	}

	for _, stage := range stages {
		if err := ctx.Err(); err != nil {
			return t.handleContextStop(payload, err)
		}
		if err := t.repo.UpdateStatus(payload.JobID, payload.LeaseOwner, stage.name, ""); err != nil {
			return fmt.Errorf("failed to update status to %s: %w", stage.name, err)
		}

		if err := stage.fn(ctx, payload); err != nil {
			t.logger.Error("stage failed",
				zap.String("stage", stage.name),
				zap.String("job_id", payload.JobID),
				zap.Error(err))

			if t.shouldLeaveForRecovery(ctx, err) {
				return fmt.Errorf("%s interrupted: %w", stage.name, err)
			}

			if markErr := t.repo.MarkFailed(payload.JobID, payload.LeaseOwner, err); markErr != nil &&
				!errors.Is(markErr, repository.ErrJobCancelled) &&
				!errors.Is(markErr, repository.ErrLeaseLost) {
				t.logger.Error("failed to mark job as failed", zap.Error(markErr))
			}

			return fmt.Errorf("%s failed: %w", stage.name, err)
		}
		if err := ctx.Err(); err != nil {
			return t.handleContextStop(payload, err)
		}
	}

	// 标记完成
	job, err = t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	if err := t.repo.MarkDone(payload.JobID, payload.LeaseOwner, job.FilePath, job.FileSize); err != nil {
		return fmt.Errorf("failed to mark job as done: %w", err)
	}

	t.logger.Info("download task completed", zap.String("job_id", payload.JobID))
	return nil
}

func (t *DownloadTask) postProcessStages(status string) []struct {
	name string
	fn   func(context.Context, *DownloadPayload) error
} {
	all := []struct {
		name string
		fn   func(context.Context, *DownloadPayload) error
	}{
		{model.JobStatusTagging, t.stageTagging},
		{model.JobStatusMoving, t.stageMoving},
		{model.JobStatusScanning, t.stageScanning},
	}

	start := 0
	switch status {
	case model.JobStatusTagging:
		start = 0
	case model.JobStatusMoving:
		start = 1
	case model.JobStatusScanning:
		start = 2
	default:
		return nil
	}

	return all[start:]
}

// PayloadFromJob 从数据库任务构造处理载荷。
func PayloadFromJob(job *model.Job) *DownloadPayload {
	if job == nil {
		return nil
	}

	picID := job.PicID
	if picID == "" {
		picID = job.TrackID
	}
	lyricID := job.LyricID
	if lyricID == "" {
		lyricID = job.TrackID
	}

	return &DownloadPayload{
		JobID:      job.ID,
		Source:     job.Source,
		TrackID:    job.TrackID,
		PicID:      picID,
		LyricID:    lyricID,
		LibraryID:  job.LibraryID,
		Quality:    job.Quality,
		LeaseOwner: job.LeaseOwner,
	}
}

// stageResolve 阶段1：解析元数据
func (t *DownloadTask) stageResolve(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("resolving metadata", zap.String("job_id", payload.JobID))

	// 解析音频 URL
	bitrates := t.getBitrateCandidates(payload.Quality)
	var (
		urlResult *gdstudio.URLResult
		lastErr   error
	)
	for idx, bitrate := range bitrates {
		urlResult, lastErr = t.gdClient.ResolveURLContext(ctx, payload.Source, payload.TrackID, bitrate)
		if lastErr == nil {
			if idx > 0 {
				t.logger.Warn("resolve url succeeded after bitrate fallback",
					zap.String("job_id", payload.JobID),
					zap.String("source", payload.Source),
					zap.String("track_id", payload.TrackID),
					zap.Int("selected_bitrate", bitrate))
			}
			break
		}
		if err := ctx.Err(); err != nil {
			return err
		}

		// 非最后一次失败时才打印回退提示，避免日志噪音。
		if idx < len(bitrates)-1 {
			t.logger.Warn("resolve url failed, trying fallback bitrate",
				zap.String("job_id", payload.JobID),
				zap.String("source", payload.Source),
				zap.String("track_id", payload.TrackID),
				zap.Int("bitrate", bitrate),
				zap.Error(lastErr))
		}
	}
	if lastErr != nil {
		return fmt.Errorf("failed to resolve url after trying bitrates %v: %w", bitrates, lastErr)
	}

	// 更新任务信息
	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	job.TotalBytes = urlResult.Size
	job.Bitrate = urlResult.Bitrate

	// 存储 URL 到临时字段（可以扩展 model 或使用 message 字段）
	job.Message = urlResult.URL

	if err := t.repo.UpdateLeased(job, payload.LeaseOwner); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	return nil
}

// stageDownload 阶段2：下载文件
func (t *DownloadTask) stageDownload(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("downloading audio", zap.String("job_id", payload.JobID))

	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	downloadURL := job.Message // 从上一阶段获取
	if downloadURL == "" {
		return fmt.Errorf("download url not found")
	}

	// 创建临时目录
	workDir := filepath.Join(t.cfg.Storage.WorkDir, job.ID)
	if err := os.MkdirAll(workDir, 0755); err != nil {
		return fmt.Errorf("failed to create work dir: %w", err)
	}

	// 确定文件扩展名（简化：从 URL 推断）
	ext := ".mp3"
	if strings.Contains(downloadURL, ".flac") {
		ext = ".flac"
	}

	tempFilePath := filepath.Join(workDir, "audio"+ext)

	// 下载文件
	if err := t.downloadFile(ctx, downloadURL, tempFilePath, job.ID, payload.LeaseOwner); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	// 更新文件路径
	job.FilePath = tempFilePath
	fileInfo, _ := os.Stat(tempFilePath)
	if fileInfo != nil {
		job.FileSize = fileInfo.Size()
	}

	if err := t.repo.UpdateLeased(job, payload.LeaseOwner); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	t.logger.Info("download completed",
		zap.String("job_id", payload.JobID),
		zap.Int64("size", job.FileSize))

	return nil
}

// stageTagging 阶段3：写入标签
func (t *DownloadTask) stageTagging(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("writing tags", zap.String("job_id", payload.JobID))

	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	coverID := payload.PicID
	if coverID == "" {
		coverID = payload.TrackID
	}
	lyricID := payload.LyricID
	if lyricID == "" {
		lyricID = payload.TrackID
	}

	var coverURL string
	var coverData []byte

	var gdMeta *gdstudio.MetadataResult
	metadataStartedAt := time.Now()
	metadataAttempts := 0
	t.logger.Info("gdmusic track metadata lookup started",
		zap.String("job_id", payload.JobID),
		zap.String("source", payload.Source),
		zap.String("track_id", payload.TrackID),
		zap.Duration("retry_window", gdstudio.MetadataRetryMaxElapsed))
	err = gdstudio.RetryMetadata(
		ctx,
		func(metadataCtx context.Context) error {
			metadataAttempts++
			attemptStartedAt := time.Now()
			resolved, lookupErr := t.gdClient.ResolveMetadataContext(
				metadataCtx,
				payload.Source,
				payload.TrackID,
				job.Title,
				job.Artist,
			)
			if lookupErr != nil {
				t.logger.Warn("gdmusic track metadata attempt failed",
					zap.String("job_id", payload.JobID),
					zap.String("source", payload.Source),
					zap.String("track_id", payload.TrackID),
					zap.Int("attempt", metadataAttempts),
					zap.Duration("attempt_duration", time.Since(attemptStartedAt)),
					zap.Error(lookupErr))
				return lookupErr
			}
			if resolved == nil {
				emptyErr := fmt.Errorf("gdmusic returned empty metadata")
				t.logger.Warn("gdmusic track metadata attempt failed",
					zap.String("job_id", payload.JobID),
					zap.String("source", payload.Source),
					zap.String("track_id", payload.TrackID),
					zap.Int("attempt", metadataAttempts),
					zap.Duration("attempt_duration", time.Since(attemptStartedAt)),
					zap.Error(emptyErr))
				return emptyErr
			}
			gdMeta = resolved
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("gdmusic metadata lookup failed after retrying for up to %s: %w", gdstudio.MetadataRetryMaxElapsed, err)
	}
	t.logger.Info("gdmusic track metadata lookup succeeded",
		zap.String("job_id", payload.JobID),
		zap.String("source", payload.Source),
		zap.String("track_id", payload.TrackID),
		zap.Int("attempts", metadataAttempts),
		zap.Duration("elapsed", time.Since(metadataStartedAt)),
		zap.String("title", gdMeta.Title),
		zap.String("artist", gdMeta.Artist),
		zap.String("album", gdMeta.Album))

	applyGDMetadata(job, gdMeta)
	if gdMeta.PicID != "" {
		coverID = gdMeta.PicID
	}
	if gdMeta.LyricID != "" {
		lyricID = gdMeta.LyricID
	}

	if len(coverData) == 0 {
		resolvedCoverID := coverID
		if resolvedCoverID == "" && gdMeta != nil {
			resolvedCoverID = gdMeta.PicID
		}
		if resolvedCoverID == "" {
			resolvedCoverID = payload.TrackID
		}
		if resolvedCoverID != "" {
			var resolvedCoverURL string
			err := retryWithBackoffContext(ctx, auxMaxRetries, auxRetryBaseWait, func() error {
				url, resolveErr := t.gdClient.ResolveCoverContext(ctx, payload.Source, resolvedCoverID)
				if resolveErr != nil {
					return resolveErr
				}
				resolvedCoverURL = url
				return nil
			}, nil)
			if err != nil {
				t.logger.Warn("gdstudio cover resolve failed",
					zap.String("source", payload.Source),
					zap.String("pic_id", resolvedCoverID),
					zap.Error(err))
			} else if resolvedCoverURL != "" {
				coverURL = resolvedCoverURL
				dlErr := retryWithBackoffContext(ctx, auxMaxRetries, auxRetryBaseWait, func() error {
					data, downloadErr := t.gdClient.DownloadCoverContext(ctx, payload.Source, resolvedCoverURL)
					if downloadErr != nil {
						return downloadErr
					}
					coverData = data
					return nil
				}, nil)
				if dlErr != nil {
					t.logger.Warn("gdstudio cover download failed",
						zap.String("source", payload.Source),
						zap.String("pic_id", resolvedCoverID),
						zap.String("cover_url", resolvedCoverURL),
						zap.Error(dlErr))
				}
			}
		}
	}

	var lyrics string
	var translation string
	if lyricID != "" {
		var lyricResult *gdstudio.LyricResult
		err := retryWithBackoffContext(ctx, auxMaxRetries, auxRetryBaseWait, func() error {
			result, e := t.gdClient.ResolveLyricsContext(ctx, payload.Source, lyricID)
			if e != nil {
				return e
			}
			lyricResult = result
			return nil
		}, nil)
		if err != nil {
			t.logger.Warn("failed to resolve lyrics after retries", zap.Int("max_retries", auxMaxRetries), zap.Error(err))
		} else if lyricResult != nil {
			lyrics = lyricResult.Lyric
			translation = lyricResult.Translation
		}
	}

	if coverID != "" {
		job.PicID = coverID
	}
	if lyricID != "" {
		job.LyricID = lyricID
	}

	albumArtist, albumArtistSource := t.resolveAlbumArtist(job.Artist)
	job.AlbumArtist = albumArtist
	job.AlbumArtistSource = albumArtistSource

	// 构建元数据
	metadata := &model.TrackMetadata{
		Title:       job.Title,
		Artist:      job.Artist,
		AlbumArtist: albumArtist,
		Album:       job.Album,
		TrackNumber: job.TrackNumber,
		Year:        job.Year,
		CoverURL:    coverURL,
		CoverData:   coverData,
		Lyrics:      lyrics,
		Translation: translation,
	}
	if err := t.repo.UpdateLeased(job, payload.LeaseOwner); err != nil {
		return fmt.Errorf("failed to persist enriched metadata: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return err
	}

	// 写入标签
	if err := t.writeRequiredTags(job.FilePath, metadata); err != nil {
		return err
	}

	// 写入 .lrc 文件
	if lyrics != "" {
		if err := t.tagger.WriteLyricFile(job.FilePath, lyrics); err != nil {
			t.logger.Warn("failed to write lyric file", zap.Error(err))
		}
	}

	return nil
}

func (t *DownloadTask) writeRequiredTags(filePath string, metadata *model.TrackMetadata) error {
	if err := t.tagger.WriteTags(filePath, metadata); err != nil {
		return fmt.Errorf("failed to write tags: %w", err)
	}
	return nil
}

func applyGDMetadata(job *model.Job, metadata *gdstudio.MetadataResult) {
	if metadata == nil {
		return
	}
	if metadata.Title != "" {
		job.Title = metadata.Title
	}
	if metadata.Artist != "" {
		job.Artist = metadata.Artist
	}
	if metadata.Album != "" {
		job.Album = metadata.Album
	}
	if metadata.TrackNumber > 0 {
		job.TrackNumber = metadata.TrackNumber
	}
	if metadata.Year > 0 {
		job.Year = metadata.Year
	}
}

// stageMoving 阶段4：移动到目标目录
func (t *DownloadTask) stageMoving(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("moving to library", zap.String("job_id", payload.JobID))

	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	// 构建目标路径
	sourcePath := job.FilePath
	targetPath := t.buildTargetPath(job)
	targetDir := filepath.Dir(targetPath)

	// 创建目标目录
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	// Worker 可能在文件移动成功、数据库更新前退出。恢复时如果源文件已不存在而
	// 目标文件存在，则继续补齐 sidecar 和数据库状态，而不是把任务误判为失败。
	if _, err := os.Stat(sourcePath); err != nil {
		if !os.IsNotExist(err) {
			return fmt.Errorf("failed to stat source file: %w", err)
		}
		if _, targetErr := os.Stat(targetPath); targetErr != nil {
			return fmt.Errorf("source file missing and target file unavailable: %w", targetErr)
		}
		for _, ext := range []string{".lrc", ".nfo"} {
			if err := t.moveSidecar(sourcePath, targetPath, ext); err != nil {
				t.logger.Warn("failed to recover sidecar move", zap.String("ext", ext), zap.Error(err))
			}
		}
		job.FilePath = targetPath
		if err := t.repo.UpdateLeased(job, payload.LeaseOwner); err != nil {
			return fmt.Errorf("failed to persist recovered target path: %w", err)
		}
		t.logger.Info("recovered previously moved file", zap.String("path", targetPath))
		return nil
	}

	// 移动文件（同分区使用 rename，跨分区使用 copy）
	if err := os.Rename(sourcePath, targetPath); err != nil {
		// Fallback: copy then delete
		if err := t.copyFile(sourcePath, targetPath); err != nil {
			return fmt.Errorf("failed to copy file: %w", err)
		}
		os.Remove(sourcePath)
	}

	// 把同名的 sidecar 文件一并移动到目标目录。
	for _, ext := range []string{".lrc", ".nfo"} {
		if err := t.moveSidecar(sourcePath, targetPath, ext); err != nil {
			t.logger.Warn("failed to move sidecar", zap.String("ext", ext), zap.Error(err))
		}
	}

	// 更新文件路径
	job.FilePath = targetPath
	if err := t.repo.UpdateLeased(job, payload.LeaseOwner); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	t.logger.Info("file moved", zap.String("path", targetPath))
	return nil
}

// stageScanning 阶段5：触发 Navidrome 扫描
func (t *DownloadTask) stageScanning(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("triggering navidrome scan", zap.String("job_id", payload.JobID))

	// 触发扫描
	if err := t.naviClient.StartScanContext(ctx); err != nil {
		t.logger.Warn("failed to start scan", zap.Error(err))
		// 非致命错误
		return nil
	}

	// 等待扫描完成（带超时）
	if err := t.naviClient.WaitForScanContext(ctx, t.cfg.Worker.ScanTimeout); err != nil {
		t.logger.Warn("scan wait failed", zap.Error(err))
		// 非致命错误
	}

	return nil
}

// downloadFile 下载文件并报告进度
func (t *DownloadTask) downloadFile(ctx context.Context, url, destPath, jobID, leaseOwner string) error {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	// 下载并报告进度
	totalBytes := resp.ContentLength
	var completedBytes int64

	buffer := make([]byte, 32*1024)
	lastUpdate := time.Now()

	for {
		n, err := resp.Body.Read(buffer)
		if n > 0 {
			if _, writeErr := out.Write(buffer[:n]); writeErr != nil {
				return writeErr
			}
			completedBytes += int64(n)

			// 每秒更新一次进度
			if time.Since(lastUpdate) > time.Second {
				progress := 0
				if totalBytes > 0 {
					progress = int(float64(completedBytes) / float64(totalBytes) * 100)
				}
				if updateErr := t.repo.UpdateProgress(jobID, leaseOwner, progress, completedBytes, totalBytes); updateErr != nil {
					return updateErr
				}
				lastUpdate = time.Now()
			}
		}

		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
	}

	return nil
}

func (t *DownloadTask) shouldLeaveForRecovery(ctx context.Context, err error) bool {
	return errors.Is(err, repository.ErrJobCancelled) ||
		errors.Is(err, repository.ErrLeaseLost) ||
		errors.Is(ctx.Err(), context.Canceled)
}

func (t *DownloadTask) handleContextStop(payload *DownloadPayload, err error) error {
	if errors.Is(err, context.DeadlineExceeded) {
		if markErr := t.repo.MarkFailed(payload.JobID, payload.LeaseOwner, err); markErr != nil &&
			!errors.Is(markErr, repository.ErrJobCancelled) &&
			!errors.Is(markErr, repository.ErrLeaseLost) {
			t.logger.Warn("failed to mark timed out job as failed",
				zap.String("job_id", payload.JobID),
				zap.Error(markErr))
		}
	}
	return err
}

// copyFile 跨文件系统复制文件
func (t *DownloadTask) copyFile(src, dst string) error {
	source, err := os.Open(src)
	if err != nil {
		return err
	}
	defer source.Close()

	destination, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer destination.Close()

	_, err = io.Copy(destination, source)
	return err
}

func (t *DownloadTask) moveSidecar(srcAudioPath, dstAudioPath, ext string) error {
	srcPath := strings.TrimSuffix(srcAudioPath, filepath.Ext(srcAudioPath)) + ext
	if _, err := os.Stat(srcPath); err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	dstPath := strings.TrimSuffix(dstAudioPath, filepath.Ext(dstAudioPath)) + ext
	if err := os.Rename(srcPath, dstPath); err != nil {
		if err := t.copyFile(srcPath, dstPath); err != nil {
			return err
		}
		return os.Remove(srcPath)
	}

	return nil
}

// buildTargetPath 构建目标路径
func (t *DownloadTask) buildTargetPath(job *model.Job) string {
	// 优先使用 AlbumArtist 作为目录名，保证同一专辑的歌曲在同一目录
	artistDir := firstNonEmptyString(job.AlbumArtist, job.Artist, "Unknown Artist")
	cleanArtist := sanitizeFilename(artistDir)
	cleanAlbum := sanitizeFilename(firstNonEmptyString(job.Album, "Unknown Album"))
	cleanTitle := sanitizeFilename(firstNonEmptyString(job.Title, job.TrackID, job.ID))

	ext := filepath.Ext(job.FilePath)
	filename := fmt.Sprintf("%02d - %s%s", job.TrackNumber, cleanTitle, ext)

	return filepath.Join(
		t.cfg.Storage.MusicDir,
		cleanArtist,
		cleanAlbum,
		filename,
	)
}

// getBitrateFromQuality 从质量获取比特率
func (t *DownloadTask) getBitrateFromQuality(quality string) int {
	switch strings.ToLower(quality) {
	case "best", "lossless":
		return 999
	case "high":
		return 320
	case "medium":
		return 192
	case "low":
		return 128
	default:
		return 320
	}
}

// getBitrateCandidates 根据期望质量返回回退码率列表（高到低）。
func (t *DownloadTask) getBitrateCandidates(quality string) []int {
	primary := t.getBitrateFromQuality(quality)
	candidates := []int{primary}

	switch primary {
	case 999:
		candidates = append(candidates, 320, 192, 128)
	case 320:
		candidates = append(candidates, 192, 128)
	case 192:
		candidates = append(candidates, 128)
	}

	seen := make(map[int]struct{}, len(candidates))
	unique := make([]int, 0, len(candidates))
	for _, br := range candidates {
		if _, ok := seen[br]; ok {
			continue
		}
		seen[br] = struct{}{}
		unique = append(unique, br)
	}

	return unique
}

// retryWithBackoffContext 通用重试函数，支持指数退避和取消。
// skipRetry 可选回调：如果返回 true 则不再重试（如 404 not found）。
func retryWithBackoffContext(
	ctx context.Context,
	maxRetries int,
	baseWait time.Duration,
	fn func() error,
	skipRetry func(error) bool,
) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		lastErr = fn()
		if lastErr == nil {
			return nil
		}
		// 判断是否应跳过重试（不可恢复的错误）
		if skipRetry != nil && skipRetry(lastErr) {
			return lastErr
		}
		// 最后一次失败不再等待
		if attempt < maxRetries-1 {
			wait := baseWait * time.Duration(1<<uint(attempt)) // 1s, 2s, 4s ...
			if wait > auxMaxWait {
				wait = auxMaxWait
			}
			timer := time.NewTimer(wait)
			select {
			case <-ctx.Done():
				if !timer.Stop() {
					<-timer.C
				}
				return ctx.Err()
			case <-timer.C:
			}
		}
	}
	return lastErr
}

// isNotFoundError 判断错误是否为资源不存在（不可恢复），不需要重试。
func isNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	errMsg := strings.ToLower(err.Error())
	return strings.Contains(errMsg, "not found") || strings.Contains(errMsg, "empty or error response")
}

// sanitizeFilename 清理文件名
func sanitizeFilename(name string) string {
	// 移除非法字符
	invalid := []string{"/", "\\", ":", "*", "?", "\"", "<", ">", "|"}
	result := name
	for _, char := range invalid {
		result = strings.ReplaceAll(result, char, "_")
	}
	return strings.TrimSpace(result)
}

func firstNonEmptyString(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// resolveAlbumArtist 从曲目 Artist 中提取 Album Artist，不再依赖外部元数据服务。
func (t *DownloadTask) resolveAlbumArtist(artist string) (string, string) {
	artist = strings.TrimSpace(artist)
	fallback := extractFirstArtist(artist)
	if fallback != artist && fallback != "" {
		t.logger.Info("album artist fallback to first artist",
			zap.String("original", artist),
			zap.String("album_artist", fallback))
		return fallback, model.AlbumArtistSourceFallbackFirstArtist
	}

	// 如果 Artist 本身就是单人的，直接用
	return artist, model.AlbumArtistSourceFallbackArtist
}

func extractFirstArtist(artist string) string {
	artist = strings.TrimSpace(artist)
	if artist == "" {
		return ""
	}

	for _, sep := range []string{" / ", "/", ",", ";", "、"} {
		if idx := strings.Index(artist, sep); idx >= 0 {
			first := strings.TrimSpace(artist[:idx])
			if first != "" {
				return first
			}
		}
	}

	return artist
}
