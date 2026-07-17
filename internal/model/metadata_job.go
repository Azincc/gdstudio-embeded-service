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

// MetadataSearchOptions describes the fields explicitly selected by the user
// for a metadata search. Dimensions may contain title, album, and artist.
type MetadataSearchOptions struct {
	Dimensions []string `json:"dimensions"`
	Title      string   `json:"title"`
	Album      string   `json:"album"`
	Artist     string   `json:"artist"`
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
	TrackID    string           `json:"track_id,omitempty"`
	Confidence float64          `json:"confidence"`
	Metadata   EditableMetadata `json:"metadata"`
}

// MetadataCandidatesResponse is returned by /v1/metadata/candidates.
type MetadataCandidatesResponse struct {
	Current    EditableMetadata    `json:"current"`
	Candidates []MetadataCandidate `json:"candidates"`
}

// MetadataCandidatesJob stores async metadata candidate lookup work.
type MetadataCandidatesJob struct {
	ID             string         `gorm:"primaryKey;size:64" json:"job_id"`
	SongID         string         `gorm:"size:128;index" json:"song_id"`
	LibraryID      string         `gorm:"size:64;index" json:"library_id"`
	SongPath       string         `gorm:"size:1024" json:"song_path"`
	RequestJSON    string         `gorm:"type:text" json:"-"`
	Status         string         `gorm:"size:32;index" json:"status"`
	Message        string         `gorm:"size:512" json:"message"`
	Error          string         `gorm:"size:1024" json:"error"`
	ResultJSON     string         `gorm:"type:text" json:"-"`
	LeaseOwner     string         `gorm:"size:128;index" json:"-"`
	LeaseExpiresAt *time.Time     `gorm:"index" json:"-"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (MetadataCandidatesJob) TableName() string {
	return "metadata_candidate_jobs"
}

// MetadataCandidatesJobResponse is returned by the async candidates job API.
type MetadataCandidatesJobResponse struct {
	JobID     string                      `json:"job_id"`
	Status    string                      `json:"status"`
	Message   string                      `json:"message,omitempty"`
	Error     string                      `json:"error,omitempty"`
	Result    *MetadataCandidatesResponse `json:"result,omitempty"`
	CreatedAt time.Time                   `json:"created_at"`
	UpdatedAt time.Time                   `json:"updated_at"`
}

// MetadataJob stores async metadata apply work.
type MetadataJob struct {
	ID             string         `gorm:"primaryKey;size:64" json:"job_id"`
	SongID         string         `gorm:"size:128;index" json:"song_id"`
	LibraryID      string         `gorm:"size:64;index" json:"library_id"`
	SongPath       string         `gorm:"size:1024" json:"song_path"`
	MetadataJSON   string         `gorm:"type:text" json:"-"`
	Status         string         `gorm:"size:32;index" json:"status"`
	Message        string         `gorm:"size:512" json:"message"`
	Error          string         `gorm:"size:1024" json:"error"`
	FilePath       string         `gorm:"size:1024" json:"file_path"`
	LeaseOwner     string         `gorm:"size:128;index" json:"-"`
	LeaseExpiresAt *time.Time     `gorm:"index" json:"-"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
}

func (MetadataJob) TableName() string {
	return "metadata_jobs"
}

const (
	MetadataCandidatesJobStatusQueued        = "queued"
	MetadataCandidatesJobStatusSearchingSong = "searching_song"
	MetadataCandidatesJobStatusMergingData   = "merging_data"
	MetadataCandidatesJobStatusDone          = "done"
	MetadataCandidatesJobStatusFailed        = "failed"

	MetadataJobStatusQueued          = "queued"
	MetadataJobStatusReading         = "reading"
	MetadataJobStatusResolvingCover  = "resolving_cover"
	MetadataJobStatusResolvingLyrics = "resolving_lyrics"
	MetadataJobStatusTagging         = "tagging"
	MetadataJobStatusScanning        = "scanning"
	MetadataJobStatusDone            = "done"
	MetadataJobStatusFailed          = "failed"
)
