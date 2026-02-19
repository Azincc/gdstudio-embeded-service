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
	// 注意：这里需要使用 taglib 或其他 ID3 库
	// 由于需要 CGO，这里提供一个占位实现
	// 在实际部署时，应该使用 github.com/bogem/id3v2 或 taglib

	t.logger.Warn("MP3 tag writing not fully implemented yet - placeholder",
		zap.String("file", filePath))

	// TODO: 实现完整的 ID3v2 标签写入
	// 1. 打开文件
	// 2. 写入基础标签（TIT2=title, TPE1=artist, TALB=album, TRCK=track, TYER=year）
	// 3. 写入封面（APIC frame）
	// 4. 写入歌词（USLT frame）
	// 5. 保存文件

	return t.writePlaceholderTags(filePath, metadata)
}

// writeFLACTags 写入 FLAC VorbisComment 标签
func (t *Tagger) writeFLACTags(filePath string, metadata *model.TrackMetadata) error {
	t.logger.Warn("FLAC tag writing not fully implemented yet - placeholder",
		zap.String("file", filePath))

	// TODO: 实现完整的 FLAC 标签写入
	// 1. 打开文件
	// 2. 写入 VorbisComment（TITLE, ARTIST, ALBUM, TRACKNUMBER, DATE）
	// 3. 写入 PICTURE Block（封面）
	// 4. 写入 LYRICS 字段
	// 5. 保存文件

	return t.writePlaceholderTags(filePath, metadata)
}

// writePlaceholderTags 占位实现（创建 .nfo 文件）
func (t *Tagger) writePlaceholderTags(filePath string, metadata *model.TrackMetadata) error {
	// 临时方案：创建一个 .nfo 文件记录元数据
	nfoPath := filePath + ".nfo"

	content := fmt.Sprintf(`Title: %s
Artist: %s
Album: %s
Track: %d
Year: %d
Cover: %s
Lyrics Length: %d
`,
		metadata.Title,
		metadata.Artist,
		metadata.Album,
		metadata.TrackNumber,
		metadata.Year,
		metadata.CoverURL,
		len(metadata.Lyrics))

	if err := os.WriteFile(nfoPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write nfo file: %w", err)
	}

	t.logger.Info("wrote placeholder tags", zap.String("nfo", nfoPath))
	return nil
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
