package worker

import (
	"context"
	"encoding/json"
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
	"github.com/azin/gdstudio-embed-service/internal/service/musicbrainz"
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

const (
	TypeDownload = "download"

	// 封面/歌词获取的重试参数
	auxMaxRetries    = 3
	auxRetryBaseWait = 1 * time.Second
	auxMaxWait       = 8 * time.Second
)

// DownloadPayload 下载任务载荷
type DownloadPayload struct {
	JobID     string `json:"job_id"`
	Source    string `json:"source"`
	TrackID   string `json:"track_id"`
	PicID     string `json:"pic_id,omitempty"`
	LyricID   string `json:"lyric_id,omitempty"`
	LibraryID string `json:"library_id"`
	Quality   string `json:"quality"`
}

// DownloadTask 下载任务处理器
type DownloadTask struct {
	cfg        *config.Config
	repo       *repository.JobRepository
	gdClient   *gdstudio.Client
	naviClient *navidrome.Client
	tagger     *tagger.Tagger
	mbClient   *musicbrainz.Client
	logger     *zap.Logger
}

// NewDownloadTask 创建下载任务处理器
func NewDownloadTask(
	cfg *config.Config,
	repo *repository.JobRepository,
	gdClient *gdstudio.Client,
	naviClient *navidrome.Client,
	tagger *tagger.Tagger,
	mbClient *musicbrainz.Client,
	logger *zap.Logger,
) *DownloadTask {
	return &DownloadTask{
		cfg:        cfg,
		repo:       repo,
		gdClient:   gdClient,
		naviClient: naviClient,
		tagger:     tagger,
		mbClient:   mbClient,
		logger:     logger,
	}
}

// ProcessTask 处理任务
func (t *DownloadTask) ProcessTask(ctx context.Context, task *asynq.Task) error {
	var payload DownloadPayload
	if err := json.Unmarshal(task.Payload(), &payload); err != nil {
		return fmt.Errorf("unmarshal payload failed: %w", err)
	}

	t.logger.Info("processing download task",
		zap.String("job_id", payload.JobID),
		zap.String("source", payload.Source),
		zap.String("track_id", payload.TrackID))

	// 执行状态机流程
	stages := []struct {
		name string
		fn   func(context.Context, *DownloadPayload) error
	}{
		{model.JobStatusResolving, t.stageResolve},
		{model.JobStatusDownloading, t.stageDownload},
		{model.JobStatusTagging, t.stageTagging},
		{model.JobStatusMoving, t.stageMoving},
		{model.JobStatusScanning, t.stageScanning},
	}

	for _, stage := range stages {
		// 更新状态。不要覆盖 message，message 字段在当前实现中用于阶段间传递下载 URL。
		if err := t.repo.UpdateStatus(payload.JobID, stage.name, ""); err != nil {
			t.logger.Error("failed to update status", zap.Error(err))
		}

		// 执行阶段
		if err := stage.fn(ctx, &payload); err != nil {
			t.logger.Error("stage failed",
				zap.String("stage", stage.name),
				zap.String("job_id", payload.JobID),
				zap.Error(err))

			if markErr := t.repo.MarkFailed(payload.JobID, err); markErr != nil {
				t.logger.Error("failed to mark job as failed", zap.Error(markErr))
			}

			return fmt.Errorf("%s failed: %w", stage.name, err)
		}
	}

	// 标记完成
	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	if err := t.repo.MarkDone(payload.JobID, job.FilePath, job.FileSize); err != nil {
		return fmt.Errorf("failed to mark job as done: %w", err)
	}

	t.logger.Info("download task completed", zap.String("job_id", payload.JobID))
	return nil
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
		urlResult, lastErr = t.gdClient.ResolveURL(payload.Source, payload.TrackID, bitrate)
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

	if err := t.repo.Update(job); err != nil {
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
	if err := t.downloadFile(ctx, downloadURL, tempFilePath, job.ID); err != nil {
		return fmt.Errorf("failed to download file: %w", err)
	}

	// 更新文件路径
	job.FilePath = tempFilePath
	fileInfo, _ := os.Stat(tempFilePath)
	if fileInfo != nil {
		job.FileSize = fileInfo.Size()
	}

	if err := t.repo.Update(job); err != nil {
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
	var brainzMeta *musicbrainz.FingerprintMetadata
	if t.mbClient != nil {
		err := retryWithBackoff(auxMaxRetries, auxRetryBaseWait, func() error {
			resolved, lookupErr := t.mbClient.LookupTrackMetadataByFingerprint(job.FilePath)
			if lookupErr != nil {
				return lookupErr
			}
			brainzMeta = resolved
			return nil
		}, nil)
		if err != nil {
			t.logger.Warn("brainz metadata lookup failed, falling back to gdstudio",
				zap.String("job_id", payload.JobID),
				zap.Error(err))
		} else if brainzMeta != nil {
			applyFingerprintMetadata(job, brainzMeta)
			coverURL = brainzMeta.CoverURL
			coverData = brainzMeta.CoverData
		}
	}

	var gdMeta *gdstudio.MetadataResult
	if shouldResolveGDMetadata(job, payload.TrackID, coverID, lyricID, brainzMeta) {
		err := retryWithBackoff(auxMaxRetries, auxRetryBaseWait, func() error {
			resolved, lookupErr := t.gdClient.ResolveMetadata(payload.Source, payload.TrackID, job.Title, job.Artist)
			if lookupErr != nil {
				return lookupErr
			}
			gdMeta = resolved
			return nil
		}, nil)
		if err != nil {
			t.logger.Warn("gdstudio metadata lookup failed",
				zap.String("job_id", payload.JobID),
				zap.String("source", payload.Source),
				zap.String("track_id", payload.TrackID),
				zap.Error(err))
		} else if gdMeta != nil {
			applyGDMetadata(job, gdMeta)
			if gdMeta.PicID != "" && (coverID == "" || coverID == payload.TrackID || coverID == payload.PicID) {
				coverID = gdMeta.PicID
			}
			if gdMeta.LyricID != "" && (lyricID == "" || lyricID == payload.TrackID || lyricID == payload.LyricID) {
				lyricID = gdMeta.LyricID
			}
		}
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
			err := retryWithBackoff(auxMaxRetries, auxRetryBaseWait, func() error {
				url, resolveErr := t.gdClient.ResolveCover(payload.Source, resolvedCoverID)
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
				dlErr := retryWithBackoff(auxMaxRetries, auxRetryBaseWait, func() error {
					data, downloadErr := t.gdClient.DownloadCover(payload.Source, resolvedCoverURL)
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
		err := retryWithBackoff(auxMaxRetries, auxRetryBaseWait, func() error {
			result, e := t.gdClient.ResolveLyrics(payload.Source, lyricID)
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

	albumArtist := job.AlbumArtist
	if albumArtist == "" {
		albumArtist = t.resolveAlbumArtist(job.Title, job.Artist, job.Album)
		job.AlbumArtist = albumArtist
	}

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
	if brainzMeta != nil {
		metadata.DiscNumber = brainzMeta.DiscNumber
		metadata.Date = brainzMeta.Date
		metadata.MusicBrainzRecordingID = brainzMeta.MusicBrainzRecordingID
		metadata.MusicBrainzReleaseID = brainzMeta.MusicBrainzReleaseID
		metadata.MusicBrainzReleaseGroupID = brainzMeta.MusicBrainzReleaseGroupID
	}
	if err := t.repo.Update(job); err != nil {
		t.logger.Warn("failed to persist enriched metadata", zap.Error(err))
	}

	// 写入标签
	if err := t.tagger.WriteTags(job.FilePath, metadata); err != nil {
		t.logger.Warn("failed to write tags", zap.Error(err))
		// 非致命错误，继续
	}

	// 写入 .lrc 文件
	if lyrics != "" {
		if err := t.tagger.WriteLyricFile(job.FilePath, lyrics); err != nil {
			t.logger.Warn("failed to write lyric file", zap.Error(err))
		}
	}

	return nil
}

func shouldResolveGDMetadata(job *model.Job, trackID, coverID, lyricID string, brainzMeta *musicbrainz.FingerprintMetadata) bool {
	if brainzMeta == nil {
		return true
	}
	return job.Title == "" ||
		job.Artist == "" ||
		job.Album == "" ||
		job.TrackNumber == 0 ||
		job.Year == 0 ||
		coverID == "" ||
		coverID == trackID ||
		lyricID == "" ||
		lyricID == trackID
}

func applyFingerprintMetadata(job *model.Job, metadata *musicbrainz.FingerprintMetadata) {
	if metadata == nil {
		return
	}
	if metadata.Title != "" {
		job.Title = metadata.Title
	}
	if metadata.Artist != "" {
		job.Artist = metadata.Artist
	}
	if metadata.AlbumArtist != "" {
		job.AlbumArtist = metadata.AlbumArtist
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

func applyGDMetadata(job *model.Job, metadata *gdstudio.MetadataResult) {
	if metadata == nil {
		return
	}
	if job.Title == "" && metadata.Title != "" {
		job.Title = metadata.Title
	}
	if job.Artist == "" && metadata.Artist != "" {
		job.Artist = metadata.Artist
	}
	if job.Album == "" && metadata.Album != "" {
		job.Album = metadata.Album
	}
	if job.TrackNumber == 0 && metadata.TrackNumber > 0 {
		job.TrackNumber = metadata.TrackNumber
	}
	if job.Year == 0 && metadata.Year > 0 {
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
	if err := t.repo.Update(job); err != nil {
		return fmt.Errorf("failed to update job: %w", err)
	}

	t.logger.Info("file moved", zap.String("path", targetPath))
	return nil
}

// stageScanning 阶段5：触发 Navidrome 扫描
func (t *DownloadTask) stageScanning(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("triggering navidrome scan", zap.String("job_id", payload.JobID))

	// 触发扫描
	if err := t.naviClient.StartScan(); err != nil {
		t.logger.Warn("failed to start scan", zap.Error(err))
		// 非致命错误
		return nil
	}

	// 等待扫描完成（带超时）
	if err := t.naviClient.WaitForScan(t.cfg.Worker.ScanTimeout); err != nil {
		t.logger.Warn("scan wait failed", zap.Error(err))
		// 非致命错误
	}

	return nil
}

// downloadFile 下载文件并报告进度
func (t *DownloadTask) downloadFile(ctx context.Context, url, destPath, jobID string) error {
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
				t.repo.UpdateProgress(jobID, progress, completedBytes, totalBytes)
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

// retryWithBackoff 通用重试函数，支持指数退避。
// skipRetry 可选回调：如果返回 true 则不再重试（如 404 not found）。
func retryWithBackoff(maxRetries int, baseWait time.Duration, fn func() error, skipRetry func(error) bool) error {
	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
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
			time.Sleep(wait)
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

// resolveAlbumArtist 查询 Album Artist：MusicBrainz → fallback 第一艺术家
func (t *DownloadTask) resolveAlbumArtist(title, artist, album string) string {
	// 先尝试 MusicBrainz
	if t.mbClient != nil {
		albumArtist, err := t.mbClient.LookupAlbumArtist(title, artist, album)
		if err != nil {
			t.logger.Warn("musicbrainz lookup failed", zap.Error(err))
		} else if albumArtist != "" {
			t.logger.Info("album artist resolved via musicbrainz",
				zap.String("title", title),
				zap.String("album_artist", albumArtist))
			return albumArtist
		}
	}

	// Fallback：从 Artist 字段提取第一个艺术家
	fallback := musicbrainz.ExtractFirstArtist(artist)
	if fallback != artist && fallback != "" {
		t.logger.Info("album artist fallback to first artist",
			zap.String("original", artist),
			zap.String("album_artist", fallback))
		return fallback
	}

	// 如果 Artist 本身就是单人的，直接用
	return artist
}
