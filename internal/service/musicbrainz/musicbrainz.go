package musicbrainz

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"go.uber.org/zap"
)

const logTag = "MUSICBRAINZ"

// Client MusicBrainz API 客户端
type Client struct {
	cfg        *config.MusicBrainzConfig
	httpClient *http.Client
	logger     *zap.Logger
	lastReqMu  sync.Mutex
	lastReqAt  time.Time
}

// NewClient 创建 MusicBrainz 客户端
func NewClient(cfg *config.MusicBrainzConfig, logger *zap.Logger) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
		logger: logger,
	}
}

// recordingSearchResponse MusicBrainz recording search 响应
type recordingSearchResponse struct {
	Recordings []recordingResult `json:"recordings"`
}

type recordingResult struct {
	Title        string         `json:"title"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	Releases     []release      `json:"releases"`
}

type artistCredit struct {
	Artist struct {
		Name string `json:"name"`
	} `json:"artist"`
}

type release struct {
	Title        string         `json:"title"`
	ArtistCredit []artistCredit `json:"artist-credit"`
}

// LookupAlbumArtist 通过 MusicBrainz 查找 Album Artist。
// 如果查询失败或无匹配，返回空字符串和 nil error（由调用方决定 fallback）。
func (c *Client) LookupAlbumArtist(title, artist, album string) (string, error) {
	if !c.cfg.Enabled {
		return "", nil
	}

	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)
	if title == "" {
		return "", nil
	}

	// 速率限制
	c.rateLimit()

	// 构建查询
	query := fmt.Sprintf("recording:%s", url.QueryEscape(title))
	if artist != "" {
		firstArtist := extractFirstArtist(artist)
		query += fmt.Sprintf(" AND artist:%s", url.QueryEscape(firstArtist))
	}

	reqURL := fmt.Sprintf("%s/recording/?query=%s&limit=5&fmt=json", c.cfg.BaseURL, query)

	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return "", fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("searching musicbrainz",
		zap.String("title", title),
		zap.String("artist", artist),
		zap.String("url", reqURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		c.logger.Warn("musicbrainz request failed", zap.Error(err))
		return "", nil // 非致命
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		c.logger.Warn("musicbrainz unexpected status",
			zap.Int("status", resp.StatusCode))
		return "", nil
	}

	var result recordingSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		c.logger.Warn("musicbrainz decode failed", zap.Error(err))
		return "", nil
	}

	// 从匹配结果中提取 Album Artist
	albumArtist := c.extractAlbumArtist(result.Recordings, title, album)
	if albumArtist != "" {
		c.logger.Info("album artist found via musicbrainz",
			zap.String("title", title),
			zap.String("album_artist", albumArtist))
	} else {
		c.logger.Debug("no album artist found in musicbrainz",
			zap.String("title", title))
	}

	return albumArtist, nil
}

// extractAlbumArtist 从搜索结果中提取 Album Artist
func (c *Client) extractAlbumArtist(recordings []recordingResult, title, album string) string {
	normalizedTitle := strings.ToLower(strings.TrimSpace(title))
	normalizedAlbum := strings.ToLower(strings.TrimSpace(album))

	for _, rec := range recordings {
		// 标题匹配检查
		if !strings.EqualFold(strings.TrimSpace(rec.Title), normalizedTitle) {
			continue
		}

		for _, rel := range rec.Releases {
			// 如果有专辑名，优先匹配专辑名一致的 release
			if normalizedAlbum != "" && !strings.EqualFold(strings.TrimSpace(rel.Title), normalizedAlbum) {
				continue
			}

			if len(rel.ArtistCredit) > 0 {
				return rel.ArtistCredit[0].Artist.Name
			}
		}

		// 如果按专辑名没匹配到，取第一个有 ArtistCredit 的 release
		if normalizedAlbum != "" {
			for _, rel := range rec.Releases {
				if len(rel.ArtistCredit) > 0 {
					return rel.ArtistCredit[0].Artist.Name
				}
			}
		}
	}

	// 没有按标题精确匹配的，放宽条件
	for _, rec := range recordings {
		for _, rel := range rec.Releases {
			if len(rel.ArtistCredit) > 0 {
				return rel.ArtistCredit[0].Artist.Name
			}
		}
	}

	return ""
}

// rateLimit 速率限制：确保两次请求间隔不小于 RateLimitMs
func (c *Client) rateLimit() {
	c.lastReqMu.Lock()
	defer c.lastReqMu.Unlock()

	minInterval := time.Duration(c.cfg.RateLimitMs) * time.Millisecond
	elapsed := time.Since(c.lastReqAt)
	if elapsed < minInterval {
		time.Sleep(minInterval - elapsed)
	}
	c.lastReqAt = time.Now()
}

// ExtractFirstArtist 从多人 Artist 字段中提取第一个艺术家名
// 例：「塞壬唱片-MSR / Elvin Shen / ZT」→「塞壬唱片-MSR」
func ExtractFirstArtist(artist string) string {
	return extractFirstArtist(artist)
}

func extractFirstArtist(artist string) string {
	artist = strings.TrimSpace(artist)
	if artist == "" {
		return ""
	}

	for _, sep := range []string{" / ", "/", ",", ";", "、"} {
		if idx := strings.Index(artist, sep); idx >= 0 {
			result := strings.TrimSpace(artist[:idx])
			if result != "" {
				return result
			}
		}
	}

	return artist
}
