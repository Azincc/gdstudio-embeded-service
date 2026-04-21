package model

import (
	"time"

	"gorm.io/gorm"
)

// SongMetadataReference identifies a library song to inspect or edit.
type SongMetadataReference struct {
	ID          string `json:"id"`
	Path        string `json:"path"`
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	TrackNumber int    `json:"trackNumber"`
	DiscNumber  int    `json:"discNumber"`
	Year        int    `json:"year"`
	Genre       string `json:"genre"`
	Suffix      string `json:"suffix"`
	LibraryID   string `json:"libraryId"`
}

// EditableMetadata is the JSON payload exchanged with Echoes for metadata editing.
type EditableMetadata struct {
	Title       string `json:"title"`
	Artist      string `json:"artist"`
	Album       string `json:"album"`
	AlbumArtist string `json:"albumArtist"`
	TrackNumber int    `json:"trackNumber"`
	DiscNumber  int    `json:"discNumber"`
	Year        int    `json:"year"`
	Genre       string `json:"genre"`
	Lyrics      string `json:"lyrics"`
	CoverURL    string `json:"coverUrl"`
	Comment     string `json:"comment"`
	Composer    string `json:"composer"`
	Label       string `json:"label"`
}

func (r SongMetadataReference) ToEditableMetadata() EditableMetadata {
	return EditableMetadata{
		Title:       r.Title,
		Artist:      r.Artist,
		Album:       r.Album,
		AlbumArtist: r.AlbumArtist,
		TrackNumber: r.TrackNumber,
		DiscNumber:  r.DiscNumber,
		Year:        r.Year,
		Genre:       r.Genre,
	}
}

// MetadataCandidate is a candidate option returned to Echoes.
type MetadataCandidate struct {
	Source     string           `json:"source"`
	Confidence float64          `json:"confidence"`
	Metadata   EditableMetadata `json:"metadata"`
}

// MetadataCandidatesResponse is returned by /v1/metadata/candidates.
type MetadataCandidatesResponse struct {
	Current    EditableMetadata    `json:"current"`
	Candidates []MetadataCandidate `json:"candidates"`
}

// MetadataJob stores async metadata apply work.
type MetadataJob struct {
	ID           string         `gorm:"primaryKey;size:64" json:"job_id"`
	SongID       string         `gorm:"size:128;index" json:"song_id"`
	LibraryID    string         `gorm:"size:64;index" json:"library_id"`
	SongPath     string         `gorm:"size:1024" json:"song_path"`
	MetadataJSON string         `gorm:"type:text" json:"-"`
	Status       string         `gorm:"size:32;index" json:"status"`
	Message      string         `gorm:"size:512" json:"message"`
	Error        string         `gorm:"size:1024" json:"error"`
	FilePath     string         `gorm:"size:1024" json:"file_path"`
	CreatedAt    time.Time      `json:"created_at"`
	UpdatedAt    time.Time      `json:"updated_at"`
	DeletedAt    gorm.DeletedAt `gorm:"index" json:"-"`
}

func (MetadataJob) TableName() string {
	return "metadata_jobs"
}

const (
	MetadataJobStatusQueued          = "queued"
	MetadataJobStatusReading         = "reading"
	MetadataJobStatusResolvingCover  = "resolving_cover"
	MetadataJobStatusResolvingLyrics = "resolving_lyrics"
	MetadataJobStatusTagging         = "tagging"
	MetadataJobStatusScanning        = "scanning"
	MetadataJobStatusDone            = "done"
	MetadataJobStatusFailed          = "failed"
)
