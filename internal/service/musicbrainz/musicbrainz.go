package musicbrainz

import (
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"go.uber.org/zap"
)

const logTag = "MUSICBRAINZ"

// Client MusicBrainz / AcoustID / Cover Art Archive 客户端。
type Client struct {
	cfg        *config.MusicBrainzConfig
	httpClient *http.Client
	logger     *zap.Logger
	lastReqMu  sync.Mutex
	lastReqAt  time.Time
}

// FingerprintMetadata 是通过音频指纹解析出的可写元数据。
type FingerprintMetadata struct {
	Title                     string
	Artist                    string
	AlbumArtist               string
	AlbumArtistSource         string
	Album                     string
	TrackNumber               int
	DiscNumber                int
	Year                      int
	Date                      string
	MusicBrainzRecordingID    string
	MusicBrainzReleaseID      string
	MusicBrainzReleaseGroupID string
	CoverURL                  string
	CoverData                 []byte
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

type recordingSearchResponse struct {
	Recordings []recordingResult `json:"recordings"`
}

type releaseSearchResponse struct {
	Releases []release `json:"releases"`
}

type releaseDetailResponse struct {
	ID              string                `json:"id"`
	Title           string                `json:"title"`
	Date            string                `json:"date"`
	ArtistCredit    []artistCredit        `json:"artist-credit"`
	CoverArtArchive coverArtArchiveStatus `json:"cover-art-archive"`
	Media           []releaseDetailMedium `json:"media"`
}

type coverArtArchiveStatus struct {
	Front bool `json:"front"`
	Count int  `json:"count"`
}

type releaseDetailMedium struct {
	Position int                  `json:"position"`
	Tracks   []releaseDetailTrack `json:"tracks"`
}

type releaseDetailTrack struct {
	Number       string         `json:"number"`
	Title        string         `json:"title"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	Recording    struct {
		ID               string         `json:"id"`
		Title            string         `json:"title"`
		ArtistCredit     []artistCredit `json:"artist-credit"`
		FirstReleaseDate string         `json:"first-release-date"`
	} `json:"recording"`
}

type recordingResult struct {
	ID               string         `json:"id"`
	Title            string         `json:"title"`
	ArtistCredit     []artistCredit `json:"artist-credit"`
	FirstReleaseDate string         `json:"first-release-date"`
	Releases         []release      `json:"releases"`
}

type artistCredit struct {
	Name   string `json:"name"`
	Artist struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"artist"`
}

type release struct {
	ID           string         `json:"id"`
	Title        string         `json:"title"`
	Date         string         `json:"date"`
	Status       string         `json:"status"`
	ArtistCredit []artistCredit `json:"artist-credit"`
	ReleaseGroup releaseGroup   `json:"release-group"`
	Media        []medium       `json:"media"`
}

type releaseGroup struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	PrimaryType string `json:"primary-type"`
}

type medium struct {
	Position    int     `json:"position"`
	Track       []track `json:"track"`
	TrackCount  int     `json:"track-count"`
	TrackOffset int     `json:"track-offset"`
}

type track struct {
	ID     string `json:"id"`
	Number string `json:"number"`
	Title  string `json:"title"`
}

type acoustIDLookupResponse struct {
	Status  string            `json:"status"`
	Results []acoustIDResult  `json:"results"`
	Error   acoustIDErrorBody `json:"error"`
}

type acoustIDErrorBody struct {
	Message string `json:"message"`
	Code    int    `json:"code"`
}

type acoustIDResult struct {
	Score      float64             `json:"score"`
	Recordings []acoustIDRecording `json:"recordings"`
}

type acoustIDRecording struct {
	ID            string                 `json:"id"`
	Title         string                 `json:"title"`
	Artists       []acoustIDArtist       `json:"artists"`
	ReleaseGroups []acoustIDReleaseGroup `json:"releasegroups"`
}

type acoustIDArtist struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

type acoustIDReleaseGroup struct {
	ID    string `json:"id"`
	Title string `json:"title"`
	Type  string `json:"type"`
}

type audioFingerprint struct {
	Duration    int    `json:"duration"`
	Fingerprint string `json:"fingerprint"`
}

// LookupTrackMetadataByFingerprint 通过音频指纹优先解析元数据和封面。
func (c *Client) LookupTrackMetadataByFingerprint(filePath string) (*FingerprintMetadata, error) {
	if c == nil || c.cfg == nil {
		return nil, fmt.Errorf("musicbrainz client is nil")
	}
	if !c.cfg.Enabled {
		return nil, fmt.Errorf("musicbrainz metadata is disabled")
	}
	if strings.TrimSpace(c.cfg.AcoustIDClient) == "" {
		return nil, fmt.Errorf("acoustid client is not configured")
	}

	fp, err := generateFingerprint(filePath)
	if err != nil {
		return nil, err
	}

	match, err := c.lookupAcoustID(fp)
	if err != nil {
		return nil, err
	}

	searchResult, rel, err := c.searchRecordingMetadata(match)
	if err != nil {
		return nil, err
	}

	metadata := c.metadataFromSearch(match, searchResult, rel)
	coverURL, coverData, coverErr := c.lookupCoverArt(metadata.MusicBrainzReleaseID, metadata.MusicBrainzReleaseGroupID)
	if coverErr != nil {
		c.logger.Warn("brainz cover lookup failed",
			zap.String("release_id", metadata.MusicBrainzReleaseID),
			zap.String("release_group_id", metadata.MusicBrainzReleaseGroupID),
			zap.Error(coverErr))
	} else {
		metadata.CoverURL = coverURL
		metadata.CoverData = coverData
	}

	return metadata, nil
}

// LookupAlbumArtist 通过 MusicBrainz 查找 Album Artist。
// 如果查询失败或无匹配，返回空字符串和 nil error（由调用方决定 fallback）。
func (c *Client) LookupAlbumArtist(title, artist, album string) (string, error) {
	if c == nil || c.cfg == nil || !c.cfg.Enabled {
		return "", nil
	}

	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if title == "" && album == "" {
		return "", nil
	}

	if title != "" {
		query := fmt.Sprintf("recording:%s", quoteQuery(title))
		if artist != "" {
			query += fmt.Sprintf(" AND artist:%s", quoteQuery(extractFirstArtist(artist)))
		}

		resp, err := c.searchRecordings(query, 5)
		if err != nil {
			c.logger.Warn("musicbrainz recording request failed", zap.Error(err))
		} else {
			albumArtist := c.extractAlbumArtist(resp.Recordings, title, album)
			if albumArtist != "" {
				c.logger.Info("album artist found via musicbrainz recording",
					zap.String("title", title),
					zap.String("album_artist", albumArtist))
				return albumArtist, nil
			}
		}
	}

	if album != "" {
		albumArtist, err := c.lookupAlbumArtistByRelease(album, artist)
		if err != nil {
			c.logger.Warn("musicbrainz release request failed", zap.Error(err))
		} else if albumArtist != "" {
			c.logger.Info("album artist found via musicbrainz release",
				zap.String("album", album),
				zap.String("album_artist", albumArtist))
			return albumArtist, nil
		}
	}

	c.logger.Debug("no album artist found in musicbrainz",
		zap.String("title", title),
		zap.String("album", album))
	return "", nil
}

// LookupTrackMetadata 通过 MusicBrainz 搜索补全曲目级元数据。
func (c *Client) LookupTrackMetadata(title, artist, album string) (*FingerprintMetadata, error) {
	if c == nil || c.cfg == nil || !c.cfg.Enabled {
		return nil, nil
	}

	title = strings.TrimSpace(title)
	artist = strings.TrimSpace(artist)
	album = strings.TrimSpace(album)
	if title == "" && album == "" {
		return nil, nil
	}

	if album != "" {
		metadata, err := c.lookupTrackMetadataByRelease(title, artist, album)
		if err != nil {
			return nil, err
		}
		if metadata != nil {
			return metadata, nil
		}
	}

	if title == "" {
		return nil, nil
	}

	query := fmt.Sprintf("recording:%s", quoteQuery(title))
	if artist != "" {
		query += fmt.Sprintf(" AND artist:%s", quoteQuery(extractFirstArtist(artist)))
	}
	resp, err := c.searchRecordings(query, 10)
	if err != nil {
		return nil, err
	}

	rec := pickBestRecording(resp.Recordings, "", title, artist, album, "")
	if rec == nil {
		return nil, nil
	}

	metadata := &FingerprintMetadata{
		Title:                  strings.TrimSpace(rec.Title),
		Artist:                 strings.TrimSpace(joinArtistCredits(rec.ArtistCredit)),
		Album:                  album,
		AlbumArtistSource:      model.AlbumArtistSourceMusicBrainz,
		MusicBrainzRecordingID: strings.TrimSpace(rec.ID),
	}
	if rel := pickBestRelease(rec.Releases, album, ""); rel != nil {
		applyReleaseToMetadata(metadata, rel)
	}
	if metadata.Date == "" {
		metadata.Date = rec.FirstReleaseDate
	}
	if metadata.Year == 0 {
		metadata.Year = yearFromDate(metadata.Date)
	}
	if metadata.AlbumArtist == "" {
		if metadata.Artist != "" {
			metadata.AlbumArtist = metadata.Artist
		}
		metadata.AlbumArtistSource = model.AlbumArtistSourceFallbackArtist
	}
	c.attachCover(metadata)
	return metadata, nil
}

func (c *Client) lookupAcoustID(fp *audioFingerprint) (*acoustIDRecording, error) {
	form := url.Values{}
	form.Set("client", strings.TrimSpace(c.cfg.AcoustIDClient))
	form.Set("duration", strconv.Itoa(fp.Duration))
	form.Set("fingerprint", fp.Fingerprint)
	form.Set("meta", "recordings releasegroups compress")

	reqURL := strings.TrimRight(c.cfg.AcoustIDURL, "/") + "/lookup"
	req, err := http.NewRequest("POST", reqURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, fmt.Errorf("create acoustid request failed: %w", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("acoustid request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("acoustid unexpected status: %d", resp.StatusCode)
	}

	var result acoustIDLookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("decode acoustid response failed: %w", err)
	}
	if result.Status != "ok" {
		return nil, fmt.Errorf("acoustid lookup failed: code=%d message=%s", result.Error.Code, result.Error.Message)
	}

	var best *acoustIDRecording
	bestScore := -1.0
	for _, item := range result.Results {
		if len(item.Recordings) == 0 {
			continue
		}
		if item.Score <= bestScore {
			continue
		}
		bestScore = item.Score
		candidate := item.Recordings[0]
		best = &candidate
	}
	if best == nil {
		return nil, fmt.Errorf("acoustid returned no matched recordings")
	}

	c.logger.Info("fingerprint matched via acoustid",
		zap.String("recording_id", best.ID),
		zap.String("title", best.Title),
		zap.Float64("score", bestScore))

	return best, nil
}

func (c *Client) searchRecordingMetadata(match *acoustIDRecording) (*recordingResult, *release, error) {
	title := strings.TrimSpace(match.Title)
	artist := strings.TrimSpace(joinAcoustIDArtists(match.Artists))
	albumHint, rgID := firstReleaseGroup(match.ReleaseGroups)

	query := fmt.Sprintf("recording:%s", quoteQuery(title))
	if artist != "" {
		query += fmt.Sprintf(" AND artist:%s", quoteQuery(extractFirstArtist(artist)))
	}

	resp, err := c.searchRecordings(query, 10)
	if err != nil {
		return nil, nil, err
	}

	rec := pickBestRecording(resp.Recordings, match.ID, title, artist, albumHint, rgID)
	if rec == nil {
		return nil, nil, fmt.Errorf("musicbrainz search returned no suitable recording")
	}

	rel := pickBestRelease(rec.Releases, albumHint, rgID)
	return rec, rel, nil
}

func (c *Client) searchRecordings(query string, limit int) (*recordingSearchResponse, error) {
	c.rateLimit()

	values := url.Values{}
	values.Set("query", query)
	values.Set("limit", strconv.Itoa(limit))
	values.Set("fmt", "json")

	reqURL := fmt.Sprintf("%s/recording/?%s", strings.TrimRight(c.cfg.BaseURL, "/"), values.Encode())
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("searching musicbrainz",
		zap.String("query", query),
		zap.String("url", reqURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz unexpected status: %d", resp.StatusCode)
	}

	var result recordingSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("musicbrainz decode failed: %w", err)
	}

	return &result, nil
}

func (c *Client) searchReleases(query string, limit int) (*releaseSearchResponse, error) {
	c.rateLimit()

	values := url.Values{}
	values.Set("query", query)
	values.Set("limit", strconv.Itoa(limit))
	values.Set("fmt", "json")

	reqURL := fmt.Sprintf("%s/release/?%s", strings.TrimRight(c.cfg.BaseURL, "/"), values.Encode())
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	c.logger.Debug("searching musicbrainz releases",
		zap.String("query", query),
		zap.String("url", reqURL))

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz unexpected status: %d", resp.StatusCode)
	}

	var result releaseSearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("musicbrainz decode failed: %w", err)
	}

	return &result, nil
}

func (c *Client) fetchReleaseDetail(releaseID string) (*releaseDetailResponse, error) {
	if strings.TrimSpace(releaseID) == "" {
		return nil, nil
	}

	c.rateLimit()

	values := url.Values{}
	values.Set("inc", "recordings+artist-credits")
	values.Set("fmt", "json")

	reqURL := fmt.Sprintf("%s/release/%s?%s", strings.TrimRight(c.cfg.BaseURL, "/"), releaseID, values.Encode())
	req, err := http.NewRequest("GET", reqURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create request failed: %w", err)
	}
	req.Header.Set("User-Agent", c.cfg.UserAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("musicbrainz request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("musicbrainz unexpected status: %d", resp.StatusCode)
	}

	var result releaseDetailResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, fmt.Errorf("musicbrainz decode failed: %w", err)
	}

	return &result, nil
}

func (c *Client) metadataFromSearch(match *acoustIDRecording, rec *recordingResult, rel *release) *FingerprintMetadata {
	albumTitle, rgID := firstReleaseGroup(match.ReleaseGroups)
	metadata := &FingerprintMetadata{
		Title:                     strings.TrimSpace(match.Title),
		Artist:                    strings.TrimSpace(joinAcoustIDArtists(match.Artists)),
		Album:                     albumTitle,
		MusicBrainzRecordingID:    strings.TrimSpace(match.ID),
		MusicBrainzReleaseGroupID: rgID,
	}

	if rec != nil {
		if rec.Title != "" {
			metadata.Title = rec.Title
		}
		if artistName := joinArtistCredits(rec.ArtistCredit); artistName != "" {
			metadata.Artist = artistName
		}
		if metadata.Date == "" {
			metadata.Date = rec.FirstReleaseDate
		}
		if metadata.Year == 0 {
			metadata.Year = yearFromDate(rec.FirstReleaseDate)
		}
		if rec.ID != "" {
			metadata.MusicBrainzRecordingID = rec.ID
		}
	}

	if rel != nil {
		if rel.Title != "" {
			metadata.Album = rel.Title
		}
		if metadata.Date == "" {
			metadata.Date = rel.Date
		}
		if metadata.Year == 0 {
			metadata.Year = yearFromDate(rel.Date)
		}
		if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" {
			metadata.AlbumArtist = albumArtist
			metadata.AlbumArtistSource = model.AlbumArtistSourceFingerprint
		}
		if rel.ID != "" {
			metadata.MusicBrainzReleaseID = rel.ID
		}
		if rel.ReleaseGroup.ID != "" {
			metadata.MusicBrainzReleaseGroupID = rel.ReleaseGroup.ID
		}

		trackNumber, discNumber := pickTrackPosition(rel.Media, metadata.Title)
		metadata.TrackNumber = trackNumber
		metadata.DiscNumber = discNumber
	}

	if metadata.AlbumArtist == "" {
		metadata.AlbumArtist = metadata.Artist
		metadata.AlbumArtistSource = model.AlbumArtistSourceFallbackArtist
	}

	return metadata
}

func (c *Client) lookupTrackMetadataByRelease(title, artist, album string) (*FingerprintMetadata, error) {
	resp, err := c.searchReleases(fmt.Sprintf("release:%s", quoteQuery(album)), 5)
	if err != nil {
		return nil, err
	}

	bestRelease := pickBestReleaseForAlbumArtist(resp.Releases, album, artist)
	if bestRelease == nil {
		return nil, nil
	}

	metadata := &FingerprintMetadata{
		AlbumArtistSource: model.AlbumArtistSourceMusicBrainz,
	}
	applyReleaseToMetadata(metadata, bestRelease)

	detail, err := c.fetchReleaseDetail(bestRelease.ID)
	if err != nil {
		return nil, err
	}

	if track := pickBestReleaseDetailTrack(detail, title, artist); track != nil {
		applyReleaseDetailTrackToMetadata(metadata, track)
	} else {
		metadata.Title = firstNonEmptyString(title, metadata.Title)
		metadata.Artist = firstNonEmptyString(artist, metadata.Artist)
	}

	if metadata.Date == "" && detail != nil {
		metadata.Date = detail.Date
	}
	if metadata.Year == 0 {
		metadata.Year = yearFromDate(metadata.Date)
	}
	if metadata.AlbumArtist == "" {
		metadata.AlbumArtist = metadata.Artist
		metadata.AlbumArtistSource = model.AlbumArtistSourceFallbackArtist
	}

	c.attachCover(metadata)
	return metadata, nil
}

func (c *Client) lookupCoverArt(releaseID, releaseGroupID string) (string, []byte, error) {
	baseURL := strings.TrimRight(c.cfg.CoverArtURL, "/")
	var candidates []string
	if releaseID != "" {
		candidates = append(candidates, baseURL+"/release/"+releaseID+"/front")
	}
	if releaseGroupID != "" {
		candidates = append(candidates, baseURL+"/release-group/"+releaseGroupID+"/front")
	}
	if len(candidates) == 0 {
		return "", nil, fmt.Errorf("cover art ids are empty")
	}

	var lastErr error
	for _, candidate := range candidates {
		req, err := http.NewRequest("GET", candidate, nil)
		if err != nil {
			lastErr = fmt.Errorf("create cover request failed: %w", err)
			continue
		}
		req.Header.Set("User-Agent", c.cfg.UserAgent)
		req.Header.Set("Accept", "image/*,*/*;q=0.8")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("cover request failed: %w", err)
			continue
		}

		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil {
			lastErr = fmt.Errorf("read cover response failed: %w", readErr)
			continue
		}
		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("cover unexpected status: %d", resp.StatusCode)
			continue
		}
		if len(body) == 0 {
			lastErr = fmt.Errorf("cover response is empty")
			continue
		}

		return candidate, body, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("cover art not found")
	}
	return "", nil, lastErr
}

// extractAlbumArtist 从搜索结果中提取 Album Artist
func (c *Client) extractAlbumArtist(recordings []recordingResult, title, album string) string {
	normalizedTitle := normalize(title)
	normalizedAlbum := normalize(album)

	for _, rec := range recordings {
		if normalize(rec.Title) != normalizedTitle {
			continue
		}

		for _, rel := range rec.Releases {
			if normalizedAlbum != "" && normalize(rel.Title) != normalizedAlbum {
				continue
			}
			if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" {
				return albumArtist
			}
		}

		if normalizedAlbum != "" {
			for _, rel := range rec.Releases {
				if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" {
					return albumArtist
				}
			}
		}
	}

	for _, rec := range recordings {
		for _, rel := range rec.Releases {
			if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" {
				return albumArtist
			}
		}
	}

	return ""
}

func applyReleaseToMetadata(metadata *FingerprintMetadata, rel *release) {
	if metadata == nil || rel == nil {
		return
	}

	if rel.Title != "" {
		metadata.Album = rel.Title
	}
	if rel.Date != "" {
		metadata.Date = rel.Date
		if metadata.Year == 0 {
			metadata.Year = yearFromDate(rel.Date)
		}
	}
	if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" {
		metadata.AlbumArtist = albumArtist
		metadata.AlbumArtistSource = model.AlbumArtistSourceMusicBrainz
	}
	if rel.ID != "" {
		metadata.MusicBrainzReleaseID = rel.ID
	}
	if rel.ReleaseGroup.ID != "" {
		metadata.MusicBrainzReleaseGroupID = rel.ReleaseGroup.ID
	}
}

func applyReleaseDetailTrackToMetadata(metadata *FingerprintMetadata, track *releaseDetailTrack) {
	if metadata == nil || track == nil {
		return
	}

	if trackTitle := firstNonEmptyString(track.Recording.Title, track.Title); trackTitle != "" {
		metadata.Title = trackTitle
	}
	if trackArtist := firstNonEmptyString(joinArtistCredits(track.Recording.ArtistCredit), joinArtistCredits(track.ArtistCredit)); trackArtist != "" {
		metadata.Artist = trackArtist
	}
	if track.Recording.ID != "" {
		metadata.MusicBrainzRecordingID = track.Recording.ID
	}
	if metadata.TrackNumber == 0 {
		metadata.TrackNumber = toPositiveInt(track.Number)
	}
	if metadata.Date == "" {
		metadata.Date = track.Recording.FirstReleaseDate
	}
	if metadata.Year == 0 {
		metadata.Year = yearFromDate(metadata.Date)
	}
}

func (c *Client) attachCover(metadata *FingerprintMetadata) {
	if metadata == nil {
		return
	}
	if metadata.MusicBrainzReleaseID == "" && metadata.MusicBrainzReleaseGroupID == "" {
		return
	}

	coverURL, coverData, coverErr := c.lookupCoverArt(metadata.MusicBrainzReleaseID, metadata.MusicBrainzReleaseGroupID)
	if coverErr != nil {
		c.logger.Warn("brainz cover lookup failed",
			zap.String("release_id", metadata.MusicBrainzReleaseID),
			zap.String("release_group_id", metadata.MusicBrainzReleaseGroupID),
			zap.Error(coverErr))
		return
	}
	metadata.CoverURL = coverURL
	metadata.CoverData = coverData
}

func (c *Client) lookupAlbumArtistByRelease(album, artist string) (string, error) {
	query := fmt.Sprintf("release:%s", quoteQuery(album))
	resp, err := c.searchReleases(query, 5)
	if err != nil {
		return "", err
	}

	best := pickBestReleaseForAlbumArtist(resp.Releases, album, artist)
	if best == nil {
		return "", nil
	}
	return joinArtistCredits(best.ArtistCredit), nil
}

func pickBestRecording(recordings []recordingResult, recordingID, title, artist, albumHint, releaseGroupID string) *recordingResult {
	bestIdx := -1
	bestScore := -1

	for idx, rec := range recordings {
		score := 0
		if recordingID != "" && rec.ID == recordingID {
			score += 100
		}
		if normalize(rec.Title) == normalize(title) {
			score += 30
		}
		recArtist := joinArtistCredits(rec.ArtistCredit)
		if recArtist != "" && artistMatches(recArtist, artist) {
			score += 20
		}
		for _, rel := range rec.Releases {
			if releaseGroupID != "" && rel.ReleaseGroup.ID == releaseGroupID {
				score += 20
				break
			}
			if albumHint != "" && normalize(rel.Title) == normalize(albumHint) {
				score += 10
				break
			}
		}

		if score > bestScore {
			bestScore = score
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		return nil
	}
	return &recordings[bestIdx]
}

func pickBestRelease(releases []release, albumHint, releaseGroupID string) *release {
	bestIdx := -1
	bestScore := -1

	for idx, rel := range releases {
		score := 0
		if releaseGroupID != "" && rel.ReleaseGroup.ID == releaseGroupID {
			score += 100
		}
		if albumHint != "" && normalize(rel.Title) == normalize(albumHint) {
			score += 40
		}
		if strings.EqualFold(strings.TrimSpace(rel.Status), "official") {
			score += 10
		}
		if yearFromDate(rel.Date) > 0 {
			score += 5
		}
		if score > bestScore {
			bestScore = score
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		return nil
	}
	return &releases[bestIdx]
}

func pickBestReleaseForAlbumArtist(releases []release, albumHint, artistHint string) *release {
	bestIdx := -1
	bestScore := -1
	normalizedAlbum := normalize(albumHint)

	for idx, rel := range releases {
		score := 0
		if normalizedAlbum != "" && normalize(rel.Title) == normalizedAlbum {
			score += 80
		}
		if strings.EqualFold(strings.TrimSpace(rel.Status), "official") {
			score += 10
		}
		if albumArtist := joinArtistCredits(rel.ArtistCredit); albumArtist != "" && artistMatches(albumArtist, artistHint) {
			score += 5
		}
		if yearFromDate(rel.Date) > 0 {
			score += 1
		}
		if score > bestScore {
			bestScore = score
			bestIdx = idx
		}
	}

	if bestIdx < 0 {
		return nil
	}
	return &releases[bestIdx]
}

func pickBestReleaseDetailTrack(detail *releaseDetailResponse, titleHint, artistHint string) *releaseDetailTrack {
	if detail == nil {
		return nil
	}

	var best *releaseDetailTrack
	bestScore := -1
	for _, media := range detail.Media {
		for idx := range media.Tracks {
			track := &media.Tracks[idx]
			score := scoreReleaseDetailTrack(track, titleHint, artistHint)
			if score > bestScore {
				bestScore = score
				best = track
			}
		}
	}

	if bestScore <= 0 {
		return nil
	}
	return best
}

func scoreReleaseDetailTrack(track *releaseDetailTrack, titleHint, artistHint string) int {
	if track == nil {
		return 0
	}

	score := 0
	for _, candidate := range []string{track.Title, track.Recording.Title} {
		if normalizedTitleEquals(candidate, titleHint) {
			score += 100
		}
		if normalizedComparableTitle(candidate) == normalizedComparableTitle(titleHint) && normalizedComparableTitle(titleHint) != "" {
			score += 70
		}
		score += sharedTitleTokenScore(candidate, titleHint)
	}

	trackArtist := firstNonEmptyString(joinArtistCredits(track.Recording.ArtistCredit), joinArtistCredits(track.ArtistCredit))
	if artistMatches(trackArtist, artistHint) {
		score += 10
	}
	return score
}

func normalizedTitleEquals(left, right string) bool {
	return normalize(left) != "" && normalize(left) == normalize(right)
}

func normalizedComparableTitle(value string) string {
	replacer := strings.NewReplacer(
		" ", "",
		"\t", "",
		"～", "",
		"〜", "",
		"~", "",
		":", "",
		"：", "",
		"-", "",
		"－", "",
		"—", "",
		"‐", "",
		"・", "",
		"(", "",
		")", "",
		"（", "",
		"）", "",
		"[", "",
		"]", "",
		"【", "",
		"】", "",
	)
	return replacer.Replace(normalize(value))
}

func sharedTitleTokenScore(left, right string) int {
	leftTokens := titleTokens(left)
	rightTokens := titleTokens(right)
	if len(leftTokens) == 0 || len(rightTokens) == 0 {
		return 0
	}

	rightSet := make(map[string]struct{}, len(rightTokens))
	for _, token := range rightTokens {
		rightSet[token] = struct{}{}
	}

	score := 0
	for _, token := range leftTokens {
		if _, ok := rightSet[token]; ok {
			score += 15
		}
	}
	return score
}

func titleTokens(value string) []string {
	value = normalize(value)
	if value == "" {
		return nil
	}

	replacer := strings.NewReplacer(
		"～", " ",
		"〜", " ",
		"~", " ",
		":", " ",
		"：", " ",
		"-", " ",
		"－", " ",
		"—", " ",
		"‐", " ",
		"/", " ",
		"／", " ",
		"(", " ",
		")", " ",
		"（", " ",
		"）", " ",
		"[", " ",
		"]", " ",
		"【", " ",
		"】", " ",
		"・", " ",
	)
	parts := strings.Fields(replacer.Replace(value))
	seen := make(map[string]struct{}, len(parts))
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		if _, ok := seen[part]; ok {
			continue
		}
		seen[part] = struct{}{}
		result = append(result, part)
	}
	return result
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

func pickTrackPosition(media []medium, title string) (int, int) {
	normalizedTitle := normalize(title)

	for _, m := range media {
		for _, track := range m.Track {
			if normalizedTitle != "" && normalize(track.Title) != normalizedTitle {
				continue
			}
			if number := toPositiveInt(track.Number); number > 0 {
				return number, positiveOrDefault(m.Position, 1)
			}
			if m.TrackOffset >= 0 {
				return m.TrackOffset + 1, positiveOrDefault(m.Position, 1)
			}
		}
	}

	for _, m := range media {
		if len(m.Track) == 0 {
			continue
		}
		number := toPositiveInt(m.Track[0].Number)
		if number == 0 && m.TrackOffset >= 0 {
			number = m.TrackOffset + 1
		}
		return number, positiveOrDefault(m.Position, 1)
	}

	return 0, 0
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

func generateFingerprint(filePath string) (*audioFingerprint, error) {
	if _, err := exec.LookPath("fpcalc"); err != nil {
		return nil, fmt.Errorf("fpcalc not found, cannot generate fingerprint: %w", err)
	}

	out, err := exec.Command("fpcalc", "-json", filePath).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("fpcalc failed: %w: %s", err, strings.TrimSpace(string(out)))
	}

	var jsonFP struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	if err := json.Unmarshal(out, &jsonFP); err == nil && jsonFP.Duration > 0 && jsonFP.Fingerprint != "" {
		return &audioFingerprint{
			Duration:    int(math.Round(jsonFP.Duration)),
			Fingerprint: jsonFP.Fingerprint,
		}, nil
	}

	parsed := parsePlainFingerprintOutput(string(out))
	if parsed == nil {
		return nil, fmt.Errorf("unable to parse fpcalc output")
	}
	return parsed, nil
}

func parsePlainFingerprintOutput(out string) *audioFingerprint {
	fp := &audioFingerprint{}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(line, "DURATION="):
			fp.Duration = toPositiveInt(strings.TrimPrefix(line, "DURATION="))
		case strings.HasPrefix(line, "FINGERPRINT="):
			fp.Fingerprint = strings.TrimSpace(strings.TrimPrefix(line, "FINGERPRINT="))
		}
	}
	if fp.Duration <= 0 || fp.Fingerprint == "" {
		return nil
	}
	return fp
}

func firstReleaseGroup(groups []acoustIDReleaseGroup) (string, string) {
	if len(groups) == 0 {
		return "", ""
	}
	return strings.TrimSpace(groups[0].Title), strings.TrimSpace(groups[0].ID)
}

func joinAcoustIDArtists(artists []acoustIDArtist) string {
	names := make([]string, 0, len(artists))
	for _, artist := range artists {
		name := strings.TrimSpace(artist.Name)
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, " / ")
}

func joinArtistCredits(credits []artistCredit) string {
	names := make([]string, 0, len(credits))
	for _, credit := range credits {
		name := strings.TrimSpace(credit.Name)
		if name == "" {
			name = strings.TrimSpace(credit.Artist.Name)
		}
		if name != "" {
			names = append(names, name)
		}
	}
	return strings.Join(names, " / ")
}

func quoteQuery(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return `""`
	}
	value = strings.ReplaceAll(value, `"`, `\"`)
	return `"` + value + `"`
}

func normalize(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func artistMatches(left, right string) bool {
	left = normalize(left)
	right = normalize(right)
	if left == "" || right == "" {
		return false
	}
	return strings.Contains(left, right) || strings.Contains(right, left)
}

func yearFromDate(date string) int {
	date = strings.TrimSpace(date)
	if len(date) < 4 {
		return 0
	}
	return toPositiveInt(date[:4])
}

func positiveOrDefault(value, fallback int) int {
	if value > 0 {
		return value
	}
	return fallback
}

func toPositiveInt(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if idx := strings.Index(raw, "/"); idx > 0 {
		raw = strings.TrimSpace(raw[:idx])
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
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
