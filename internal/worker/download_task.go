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
	"github.com/azin/gdstudio-embed-service/internal/service/navidrome"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
)

const (
	TypeDownload = "download"
)

// DownloadPayload 下载任务载荷
type DownloadPayload struct {
	JobID     string `json:"job_id"`
	Source    string `json:"source"`
	TrackID   string `json:"track_id"`
	LibraryID string `json:"library_id"`
	Quality   string `json:"quality"`
}

// DownloadTask 下载任务处理器
type DownloadTask struct {
	cfg          *config.Config
	repo         *repository.JobRepository
	gdClient     *gdstudio.Client
	naviClient   *navidrome.Client
	tagger       *tagger.Tagger
	logger       *zap.Logger
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
		// 更新状态
		if err := t.repo.UpdateStatus(payload.JobID, stage.name, fmt.Sprintf("executing %s", stage.name)); err != nil {
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
	bitrate := t.getBitrateFromQuality(payload.Quality)
	urlResult, err := t.gdClient.ResolveURL(payload.Source, payload.TrackID, bitrate)
	if err != nil {
		return fmt.Errorf("failed to resolve url: %w", err)
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

	// 解析封面
	var coverData []byte
	if job.Album != "" {
		// 尝试获取封面（这里需要保存 picID，简化实现先跳过）
		t.logger.Debug("cover resolution skipped for now")
	}

	// 解析歌词
	var lyrics string
	// 类似封面，需要 lyricID

	// 构建元数据
	metadata := &model.TrackMetadata{
		Title:       job.Title,
		Artist:      job.Artist,
		Album:       job.Album,
		TrackNumber: job.TrackNumber,
		Year:        job.Year,
		CoverData:   coverData,
		Lyrics:      lyrics,
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

// stageMoving 阶段4：移动到目标目录
func (t *DownloadTask) stageMoving(ctx context.Context, payload *DownloadPayload) error {
	t.logger.Info("moving to library", zap.String("job_id", payload.JobID))

	job, err := t.repo.FindByID(payload.JobID)
	if err != nil {
		return fmt.Errorf("failed to find job: %w", err)
	}

	// 构建目标路径
	targetPath := t.buildTargetPath(job)
	targetDir := filepath.Dir(targetPath)

	// 创建目标目录
	if err := os.MkdirAll(targetDir, 0755); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	// 移动文件（同分区使用 rename，跨分区使用 copy）
	if err := os.Rename(job.FilePath, targetPath); err != nil {
		// Fallback: copy then delete
		if err := t.copyFile(job.FilePath, targetPath); err != nil {
			return fmt.Errorf("failed to copy file: %w", err)
		}
		os.Remove(job.FilePath)
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

// buildTargetPath 构建目标路径
func (t *DownloadTask) buildTargetPath(job *model.Job) string {
	// 清理文件名中的非法字符
	cleanArtist := sanitizeFilename(job.Artist)
	cleanAlbum := sanitizeFilename(job.Album)
	cleanTitle := sanitizeFilename(job.Title)

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
