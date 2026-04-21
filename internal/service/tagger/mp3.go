package tagger

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/azin/gdstudio-embed-service/internal/model"
	id3v2 "github.com/bogem/id3v2/v2"
	"go.uber.org/zap"
)

// WriteMP3TagsWithID3v2 使用 id3v2 库写入 MP3 标签
func (t *Tagger) WriteMP3TagsWithID3v2(filePath string, metadata *model.TrackMetadata) error {
	t.logger.Info("writing MP3 tags with id3v2",
		zap.String("file", filePath),
		zap.String("title", metadata.Title))

	// 打开 MP3 文件
	tag, err := id3v2.Open(filePath, id3v2.Options{Parse: true})
	if err != nil {
		return fmt.Errorf("failed to open mp3 file: %w", err)
	}
	defer tag.Close()

	// 设置 ID3v2 版本为 v2.4
	tag.SetVersion(4)

	// 写入基础标签
	tag.SetTitle(metadata.Title)
	tag.SetAlbum(metadata.Album)

	artistFrameID := tag.CommonID("Artist")
	tag.DeleteFrames(artistFrameID)
	tag.AddTextFrame(artistFrameID, tag.DefaultEncoding(), encodeID3v24MultiValueText(metadata.Artist))

	// 写入 Album Artist (TPE2)
	albumArtistFrameID := tag.CommonID("Band/Orchestra/Accompaniment")
	tag.DeleteFrames(albumArtistFrameID)
	if metadata.AlbumArtist != "" {
		tag.AddTextFrame(albumArtistFrameID,
			tag.DefaultEncoding(), encodeID3v24MultiValueText(metadata.AlbumArtist))
	}

	if metadata.TrackNumber > 0 {
		tag.AddTextFrame(tag.CommonID("Track number/Position in set"),
			tag.DefaultEncoding(),
			fmt.Sprintf("%d", metadata.TrackNumber))
	}

	if metadata.DiscNumber > 0 {
		tag.AddTextFrame(tag.CommonID("Part of a set"),
			tag.DefaultEncoding(),
			fmt.Sprintf("%d", metadata.DiscNumber))
	}

	if metadata.Year > 0 {
		tag.SetYear(fmt.Sprintf("%d", metadata.Year))
	}
	if metadata.Date != "" {
		tag.AddTextFrame(tag.CommonID("Recording time"),
			tag.DefaultEncoding(), metadata.Date)
	}
	if metadata.Genre != "" {
		tag.AddTextFrame(tag.CommonID("Content type"),
			tag.DefaultEncoding(), metadata.Genre)
	}
	if metadata.Composer != "" {
		tag.AddTextFrame(tag.CommonID("Composer"),
			tag.DefaultEncoding(), metadata.Composer)
	}
	if metadata.Label != "" {
		tag.AddTextFrame(tag.CommonID("Publisher"),
			tag.DefaultEncoding(), metadata.Label)
	}
	if metadata.Comment != "" {
		tag.DeleteFrames(tag.CommonID("Comments"))
		tag.AddCommentFrame(id3v2.CommentFrame{
			Encoding:    id3v2.EncodingUTF8,
			Language:    "eng",
			Description: "Comment",
			Text:        metadata.Comment,
		})
	}

	addUserTextFrame := func(description, value string) {
		if value == "" {
			return
		}
		tag.AddUserDefinedTextFrame(id3v2.UserDefinedTextFrame{
			Encoding:    id3v2.EncodingUTF8,
			Description: description,
			Value:       value,
		})
	}
	addUserTextFrame("MusicBrainz Recording Id", metadata.MusicBrainzRecordingID)
	addUserTextFrame("MusicBrainz Album Id", metadata.MusicBrainzReleaseID)
	addUserTextFrame("MusicBrainz Release Group Id", metadata.MusicBrainzReleaseGroupID)
	addUserTextFrame("Lyrics Translation", metadata.Translation)

	// 写入封面
	if len(metadata.CoverData) > 0 {
		tag.DeleteFrames(tag.CommonID("Attached picture"))
		pic := id3v2.PictureFrame{
			Encoding:    id3v2.EncodingUTF8,
			MimeType:    "image/jpeg",
			PictureType: id3v2.PTFrontCover,
			Description: "Cover",
			Picture:     metadata.CoverData,
		}
		tag.AddAttachedPicture(pic)
		t.logger.Debug("attached cover", zap.Int("size", len(metadata.CoverData)))
	}

	// 写入歌词
	if metadata.Lyrics != "" {
		tag.DeleteFrames(tag.CommonID("Unsynchronised lyrics/text transcription"))
		lyricFrame := id3v2.UnsynchronisedLyricsFrame{
			Encoding:          id3v2.EncodingUTF8,
			Language:          "eng",
			ContentDescriptor: "Lyrics",
			Lyrics:            metadata.Lyrics,
		}
		tag.AddUnsynchronisedLyricsFrame(lyricFrame)
		t.logger.Debug("attached lyrics", zap.Int("length", len(metadata.Lyrics)))
	}

	// 保存标签
	if err := tag.Save(); err != nil {
		return fmt.Errorf("failed to save tags: %w", err)
	}

	t.logger.Info("MP3 tags written successfully", zap.String("file", filePath))
	return nil
}

// WriteCoverToFile 将封面保存为独立文件
func (t *Tagger) WriteCoverToFile(audioPath string, coverData []byte) error {
	if len(coverData) == 0 {
		return nil
	}

	coverPath := audioPath[:len(audioPath)-len(filepath.Ext(audioPath))] + ".jpg"
	if err := os.WriteFile(coverPath, coverData, 0644); err != nil {
		return fmt.Errorf("failed to write cover file: %w", err)
	}

	t.logger.Debug("wrote cover file", zap.String("path", coverPath))
	return nil
}
