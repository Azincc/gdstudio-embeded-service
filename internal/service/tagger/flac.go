package tagger

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"go.uber.org/zap"
)

// writeFLACTags 使用 metaflac 写入 FLAC VorbisComment/PICTURE。
func (t *Tagger) writeFLACTags(filePath string, metadata *model.TrackMetadata) error {
	if _, err := exec.LookPath("metaflac"); err != nil {
		return fmt.Errorf("metaflac not found, cannot write FLAC tags: %w", err)
	}

	// 先删除当前任务可能覆盖的标签，避免重复值堆积。
	removeArgs := []string{
		"--remove-tag=TITLE",
		"--remove-tag=ARTIST",
		"--remove-tag=ALBUM",
		"--remove-tag=TRACKNUMBER",
		"--remove-tag=DATE",
		"--remove-tag=LYRICS",
		"--remove-tag=LYRICS_TRANSLATED",
		filePath,
	}
	if err := t.runMetaflac(removeArgs...); err != nil {
		return err
	}

	var setArgs []string
	addTag := func(key, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		setArgs = append(setArgs, "--set-tag="+key+"="+value)
	}

	addTag("TITLE", metadata.Title)
	addTag("ARTIST", metadata.Artist)
	addTag("ALBUM", metadata.Album)
	if metadata.TrackNumber > 0 {
		addTag("TRACKNUMBER", strconv.Itoa(metadata.TrackNumber))
	}
	if metadata.Year > 0 {
		addTag("DATE", strconv.Itoa(metadata.Year))
	}
	addTag("LYRICS", metadata.Lyrics)
	addTag("LYRICS_TRANSLATED", metadata.Translation)

	if len(setArgs) > 0 {
		setArgs = append(setArgs, filePath)
		if err := t.runMetaflac(setArgs...); err != nil {
			return err
		}
	}

	if len(metadata.CoverData) > 0 {
		coverFile, err := os.CreateTemp("", "embed-cover-*.img")
		if err != nil {
			return fmt.Errorf("create temp cover file failed: %w", err)
		}
		coverPath := coverFile.Name()
		defer os.Remove(coverPath)

		if _, err := coverFile.Write(metadata.CoverData); err != nil {
			coverFile.Close()
			return fmt.Errorf("write temp cover file failed: %w", err)
		}
		if err := coverFile.Close(); err != nil {
			return fmt.Errorf("close temp cover file failed: %w", err)
		}

		// 清理旧封面并写入新封面。
		if err := t.runMetaflac("--remove", "--block-type=PICTURE", filePath); err != nil {
			return err
		}
		if err := t.runMetaflac("--import-picture-from="+coverPath, filePath); err != nil {
			return err
		}
	}

	t.logger.Info("FLAC tags written successfully",
		zap.String("file", filePath),
		zap.Bool("has_cover", len(metadata.CoverData) > 0),
		zap.Bool("has_lyrics", metadata.Lyrics != ""))

	return nil
}

func (t *Tagger) runMetaflac(args ...string) error {
	cmd := exec.Command("metaflac", args...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		msg := strings.TrimSpace(string(out))
		if msg == "" {
			return fmt.Errorf("metaflac %s failed: %w", strings.Join(args, " "), err)
		}
		return fmt.Errorf("metaflac %s failed: %w: %s", strings.Join(args, " "), err, msg)
	}
	return nil
}
