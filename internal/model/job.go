package model

import (
	"strings"
	"time"

	"gorm.io/gorm"
)

// Job 下载任务模型
type Job struct {
	ID             string `gorm:"primaryKey;size:64" json:"id"`
	IdempotencyKey string `gorm:"uniqueIndex;size:255;not null" json:"idempotency_key"`
	Source         string `gorm:"size:32;not null" json:"source"`
	TrackID        string `gorm:"size:64;not null" json:"track_id"`
	PicID          string `gorm:"size:64" json:"pic_id"`
	LyricID        string `gorm:"size:64" json:"lyric_id"`
	LibraryID      string `gorm:"size:64;not null" json:"library_id"`
	Quality        string `gorm:"size:16" json:"quality"`

	// 元数据
	Title       string `gorm:"size:255" json:"title"`
	Artist      string `gorm:"size:255" json:"artist"`
	AlbumArtist string `gorm:"size:255" json:"album_artist"`
	// AlbumArtistSource 标记专辑歌手的来源，用于区分可靠专辑级信息和单曲回退值。
	AlbumArtistSource string `gorm:"size:32" json:"-"`
	Album             string `gorm:"size:255" json:"album"`
	TrackNumber       int    `json:"track_number"`
	Year              int    `json:"year"`

	// 任务状态
	Status  string `gorm:"size:32;not null;index" json:"status"` // queued/resolving/downloading/tagging/moving/scanning/done/failed
	Message string `gorm:"size:512" json:"message"`

	// 进度信息
	Progress       int   `json:"progress"` // 0-100
	TotalBytes     int64 `json:"total_bytes"`
	CompletedBytes int64 `json:"completed_bytes"`

	// 结果信息
	FilePath string `gorm:"size:512" json:"file_path"`
	FileSize int64  `json:"file_size"`
	Duration int    `json:"duration"` // 秒
	Bitrate  int    `json:"bitrate"`  // kbps

	// 错误信息
	Error       string     `gorm:"size:1024" json:"error"`
	RetryCount  int        `json:"retry_count"`
	LastRetryAt *time.Time `json:"last_retry_at"`

	// 时间戳
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// TableName 表名
func (Job) TableName() string {
	return "jobs"
}

// TrackMetadata 曲目元数据
type TrackMetadata struct {
	Title                     string
	Artist                    string
	AlbumArtist               string
	AlbumArtistSource         string
	Album                     string
	TrackNumber               int
	DiscNumber                int
	Year                      int
	Date                      string
	Genre                     string
	Composer                  string
	Label                     string
	Comment                   string
	MusicBrainzRecordingID    string
	MusicBrainzReleaseID      string
	MusicBrainzReleaseGroupID string
	CoverURL                  string
	CoverData                 []byte
	Lyrics                    string
	Translation               string // 翻译歌词
}

// JobStatus 任务状态常量
const (
	AlbumArtistSourceFingerprint         = "fingerprint"
	AlbumArtistSourceMusicBrainz         = "musicbrainz"
	AlbumArtistSourceAlbumShared         = "album_shared"
	AlbumArtistSourceFallbackFirstArtist = "fallback_first_artist"
	AlbumArtistSourceFallbackArtist      = "fallback_artist"
)

func NormalizeAlbumArtistSource(source string) string {
	return strings.TrimSpace(strings.ToLower(source))
}

func IsReliableAlbumArtistSource(source string) bool {
	switch NormalizeAlbumArtistSource(source) {
	case AlbumArtistSourceFingerprint, AlbumArtistSourceMusicBrainz, AlbumArtistSourceAlbumShared:
		return true
	default:
		return false
	}
}

func ReliableAlbumArtistSources() []string {
	return []string{
		AlbumArtistSourceFingerprint,
		AlbumArtistSourceMusicBrainz,
		AlbumArtistSourceAlbumShared,
	}
}

const (
	JobStatusQueued      = "queued"
	JobStatusResolving   = "resolving"
	JobStatusDownloading = "downloading"
	JobStatusTagging     = "tagging"
	JobStatusMoving      = "moving"
	JobStatusScanning    = "scanning"
	JobStatusDone        = "done"
	JobStatusFailed      = "failed"
	JobStatusCancelled   = "cancelled"
)
