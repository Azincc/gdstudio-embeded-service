package tagger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"go.uber.org/zap"
)

// Tagger 音频标签写入器
type Tagger struct {
	logger *zap.Logger
}

// NewTagger 创建标签写入器
func NewTagger(logger *zap.Logger) *Tagger {
	return &Tagger{
		logger: logger,
	}
}

// WriteTags 写入标签
func (t *Tagger) WriteTags(filePath string, metadata *model.TrackMetadata) error {
	ext := filepath.Ext(filePath)

	t.logger.Info("writing tags",
		zap.String("file", filePath),
		zap.String("extension", ext),
		zap.String("title", metadata.Title),
		zap.String("artist", metadata.Artist))

	switch ext {
	case ".mp3":
		return t.writeMP3Tags(filePath, metadata)
	case ".flac":
		return t.writeFLACTags(filePath, metadata)
	default:
		return fmt.Errorf("unsupported file format: %s", ext)
	}
}

// writeMP3Tags 写入 MP3 ID3v2 标签
func (t *Tagger) writeMP3Tags(filePath string, metadata *model.TrackMetadata) error {
	return t.WriteMP3TagsWithID3v2(filePath, metadata)
}

// WriteLyricFile 写入 .lrc 歌词文件
func (t *Tagger) WriteLyricFile(audioPath string, lyrics string) error {
	if lyrics == "" {
		return nil
	}

	lrcPath := audioPath[:len(audioPath)-len(filepath.Ext(audioPath))] + ".lrc"

	if err := os.WriteFile(lrcPath, []byte(lyrics), 0644); err != nil {
		return fmt.Errorf("failed to write lyric file: %w", err)
	}

	t.logger.Info("wrote lyric file", zap.String("path", lrcPath))
	return nil
}
