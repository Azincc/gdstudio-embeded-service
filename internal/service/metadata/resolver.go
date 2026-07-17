package metadata

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	id3v2 "github.com/bogem/id3v2/v2"
	"go.uber.org/zap"
)

// Resolver loads current file tags and external candidates for a song.
type Resolver struct {
	cfg        *config.Config
	gdClient   *gdstudio.Client
	httpClient *http.Client
	logger     *zap.Logger
}

type sourceLookupResult struct {
	source   string
	metadata *gdstudio.MetadataResult
	err      error
}

func NewResolver(
	cfg *config.Config,
	gdClient *gdstudio.Client,
	logger *zap.Logger,
) *Resolver {
	return &Resolver{
		cfg:      cfg,
		gdClient: gdClient,
		httpClient: &http.Client{
			Timeout: 20 * time.Second,
		},
		logger: logger,
	}
}

func ResolveSongPath(cfg *config.Config, rawPath string) (string, error) {
	musicDir := strings.TrimSpace(cfg.Storage.MusicDir)
	if musicDir == "" {
		return "", fmt.Errorf("music_dir is empty")
	}

	rawPath = strings.TrimSpace(rawPath)
	if rawPath == "" {
		return "", fmt.Errorf("song path is empty")
	}

	base := filepath.Clean(musicDir)
	var resolved string
	if filepath.IsAbs(rawPath) {
		resolved = filepath.Clean(rawPath)
	} else {
		resolved = filepath.Join(base, filepath.FromSlash(rawPath))
	}

	rel, err := filepath.Rel(base, resolved)
	if err != nil {
		return "", fmt.Errorf("invalid song path: %w", err)
	}
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("song path escapes music dir")
	}

	if _, err := os.Stat(resolved); err != nil {
		return "", fmt.Errorf("song file not found: %w", err)
	}

	return resolved, nil
}

func (r *Resolver) ResolveCandidates(
	ctx context.Context,
	song model.SongMetadataReference,
	progress func(status, message string),
) (*model.MetadataCandidatesResponse, string, error) {
	reportProgress := func(status, message string) {
		if progress != nil {
			progress(status, message)
		}
	}

	absolutePath, err := ResolveSongPath(r.cfg, song.Path)
	if err != nil {
		return nil, "", err
	}

	currentFallback := song.ToEditableMetadata()
	current := currentFallback
	if readCurrent, readErr := readCurrentMetadata(absolutePath); readErr != nil {
		r.logger.Warn("failed to read current file tags, fallback to request",
			zap.String("path", absolutePath),
			zap.Error(readErr))
	} else {
		current = mergeMetadataFallback(readCurrent, currentFallback)
	}
	if sidecarLyrics, sidecarErr := readSidecarLyrics(absolutePath); sidecarErr == nil {
		current.Lyrics = preferCurrentLyrics(current.Lyrics, sidecarLyrics)
	} else if !os.IsNotExist(sidecarErr) {
		r.logger.Debug("failed to read sidecar lyrics",
			zap.String("path", absolutePath),
			zap.Error(sidecarErr))
	}

	candidates := make([]model.MetadataCandidate, 0, 4)
	seen := map[string]struct{}{}
	addCandidate := func(source string, confidence float64, metadata model.EditableMetadata) {
		metadata = sanitizeEditableMetadata(metadata)
		if metadata.Title == "" && metadata.Artist == "" && metadata.Album == "" {
			return
		}
		key := source + "\x00" + candidateKey(metadata)
		if _, ok := seen[key]; ok {
			return
		}
		seen[key] = struct{}{}
		candidates = append(candidates, model.MetadataCandidate{
			Source:     source,
			Confidence: confidence,
			Metadata:   metadata,
		})
	}

	lookupTitle := firstNonEmpty(current.Title, song.Title)
	lookupArtist := firstNonEmpty(current.Artist, song.Artist)
	lookupAlbum := firstNonEmpty(current.Album, song.Album)
	lookupStartedAt := time.Now()

	reportProgress(model.MetadataCandidatesJobStatusSearchingSong, "")
	if r.gdClient == nil {
		return nil, absolutePath, fmt.Errorf("gdmusic client is unavailable")
	}

	candidateSources := []string{"netease", "kuwo"}
	resultCh := make(chan sourceLookupResult, len(candidateSources))
	for _, source := range candidateSources {
		source := source
		go func() {
			sourceStartedAt := time.Now()
			attempts := 0
			notFound := false
			var resolved *gdstudio.MetadataResult
			r.logger.Info("metadata candidate source lookup started",
				zap.String("song_id", song.ID),
				zap.String("source", source),
				zap.String("title", lookupTitle),
				zap.String("artist", lookupArtist),
				zap.Duration("retry_window", gdstudio.MetadataRetryMaxElapsed))
			err := gdstudio.RetryMetadata(ctx, func(retryCtx context.Context) error {
				attempts++
				attemptStartedAt := time.Now()
				gdMeta, lookupErr := r.gdClient.ResolveMetadataContext(
					retryCtx,
					source,
					"",
					lookupTitle,
					lookupArtist,
				)
				if lookupErr != nil {
					if errors.Is(lookupErr, gdstudio.ErrMetadataNotFound) {
						notFound = true
						return nil
					}
					r.logger.Warn("metadata candidate source attempt failed",
						zap.String("song_id", song.ID),
						zap.String("source", source),
						zap.Int("attempt", attempts),
						zap.Duration("attempt_duration", time.Since(attemptStartedAt)),
						zap.Error(lookupErr))
					return lookupErr
				}
				if gdMeta == nil {
					notFound = true
					return nil
				}
				resolved = gdMeta
				return nil
			})
			if err != nil {
				r.logger.Warn("metadata candidate source lookup exhausted",
					zap.String("song_id", song.ID),
					zap.String("source", source),
					zap.Int("attempts", attempts),
					zap.Duration("elapsed", time.Since(sourceStartedAt)),
					zap.Error(err))
			} else if notFound {
				r.logger.Info("metadata candidate source completed without match",
					zap.String("song_id", song.ID),
					zap.String("source", source),
					zap.Int("attempts", attempts),
					zap.Duration("elapsed", time.Since(sourceStartedAt)))
			} else {
				r.logger.Info("metadata candidate source lookup succeeded",
					zap.String("song_id", song.ID),
					zap.String("source", source),
					zap.Int("attempts", attempts),
					zap.Duration("elapsed", time.Since(sourceStartedAt)),
					zap.String("title", resolved.Title),
					zap.String("artist", resolved.Artist),
					zap.String("album", resolved.Album))
			}
			resultCh <- sourceLookupResult{
				source:   source,
				metadata: resolved,
				err:      err,
			}
		}()
	}

	resolvedBySource, lookupErrors, successfulLookups := collectSourceLookupResults(resultCh, len(candidateSources))
	if err := ctx.Err(); err != nil {
		return nil, absolutePath, err
	}
	if successfulLookups == 0 && len(lookupErrors) > 0 {
		return nil, absolutePath, fmt.Errorf(
			"gdmusic candidate lookup failed after independent retries of up to %s: %w",
			gdstudio.MetadataRetryMaxElapsed,
			errors.Join(lookupErrors...),
		)
	}
	if len(lookupErrors) > 0 {
		r.logger.Warn("metadata candidate lookup completed with partial source failures",
			zap.String("song_id", song.ID),
			zap.Int("successful_sources", successfulLookups),
			zap.Int("failed_sources", len(lookupErrors)),
			zap.Errors("errors", lookupErrors))
	}

	for _, source := range candidateSources {
		gdMeta := resolvedBySource[source]
		if gdMeta == nil {
			continue
		}
		editable := model.EditableMetadata{
			Title:       firstNonEmpty(gdMeta.Title, lookupTitle),
			Artist:      firstNonEmpty(gdMeta.Artist, lookupArtist),
			Album:       firstNonEmpty(gdMeta.Album, lookupAlbum),
			AlbumArtist: firstNonEmpty(gdMeta.Artist, lookupArtist),
			TrackNumber: gdMeta.TrackNumber,
			Year:        gdMeta.Year,
		}
		if gdMeta.PicID != "" {
			if coverURL, coverErr := r.gdClient.ResolveCoverContext(ctx, source, gdMeta.PicID); coverErr == nil {
				editable.CoverURL = coverURL
			}
		}
		if gdMeta.LyricID != "" {
			if lyricResult, lyricErr := r.gdClient.ResolveLyricsContext(ctx, source, gdMeta.LyricID); lyricErr == nil && lyricResult != nil {
				editable.Lyrics = lyricResult.Lyric
			}
		}

		addCandidate("gdstudio_"+source, 0.76, editable)
	}
	r.logger.Info("metadata candidate sources resolved",
		zap.String("song_id", song.ID),
		zap.Int("candidate_count", len(candidates)),
		zap.Duration("elapsed", time.Since(lookupStartedAt)))

	reportProgress(model.MetadataCandidatesJobStatusMergingData, "")
	return &model.MetadataCandidatesResponse{
		Current:    sanitizeEditableMetadata(current),
		Candidates: candidates,
	}, absolutePath, nil
}

func collectSourceLookupResults(
	resultCh <-chan sourceLookupResult,
	count int,
) (map[string]*gdstudio.MetadataResult, []error, int) {
	resolvedBySource := make(map[string]*gdstudio.MetadataResult, count)
	lookupErrors := make([]error, 0, count)
	successfulLookups := 0
	for i := 0; i < count; i++ {
		result := <-resultCh
		if result.err != nil {
			lookupErrors = append(lookupErrors, fmt.Errorf("%s: %w", result.source, result.err))
			continue
		}
		successfulLookups++
		resolvedBySource[result.source] = result.metadata
	}
	return resolvedBySource, lookupErrors, successfulLookups
}

func (r *Resolver) DownloadCover(ctx context.Context, rawURL string) ([]byte, error) {
	rawURL = strings.TrimSpace(rawURL)
	if rawURL == "" {
		return nil, nil
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, fmt.Errorf("create cover request failed: %w", err)
	}
	req.Header.Set("Accept", "image/avif,image/webp,image/apng,image/*,*/*;q=0.8")
	if parsed, parseErr := url.Parse(rawURL); parseErr == nil {
		switch {
		case strings.Contains(parsed.Host, "music.126.net"):
			req.Header.Set("Referer", "https://music.163.com/")
		case strings.Contains(parsed.Host, "qq.com"):
			req.Header.Set("Referer", "https://y.qq.com/")
		case strings.Contains(parsed.Host, "kuwo.cn"):
			req.Header.Set("Referer", "https://www.kuwo.cn/")
		}
	}

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("download cover failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download cover failed: status=%d", resp.StatusCode)
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read cover response failed: %w", err)
	}
	if len(data) == 0 {
		return nil, fmt.Errorf("cover response is empty")
	}
	return data, nil
}

func sanitizeEditableMetadata(metadata model.EditableMetadata) model.EditableMetadata {
	metadata.Title = strings.TrimSpace(metadata.Title)
	metadata.Artist = strings.TrimSpace(metadata.Artist)
	metadata.Album = strings.TrimSpace(metadata.Album)
	metadata.AlbumArtist = strings.TrimSpace(metadata.AlbumArtist)
	metadata.Genre = strings.TrimSpace(metadata.Genre)
	metadata.Lyrics = strings.TrimSpace(metadata.Lyrics)
	metadata.CoverURL = strings.TrimSpace(metadata.CoverURL)
	metadata.Comment = strings.TrimSpace(metadata.Comment)
	metadata.Composer = strings.TrimSpace(metadata.Composer)
	metadata.Label = strings.TrimSpace(metadata.Label)
	return metadata
}

func mergeMetadataFallback(primary, fallback model.EditableMetadata) model.EditableMetadata {
	if primary.Title == "" {
		primary.Title = fallback.Title
	}
	if primary.Artist == "" {
		primary.Artist = fallback.Artist
	}
	if primary.Album == "" {
		primary.Album = fallback.Album
	}
	if primary.AlbumArtist == "" {
		primary.AlbumArtist = fallback.AlbumArtist
	}
	if primary.TrackNumber == 0 {
		primary.TrackNumber = fallback.TrackNumber
	}
	if primary.DiscNumber == 0 {
		primary.DiscNumber = fallback.DiscNumber
	}
	if primary.Year == 0 {
		primary.Year = fallback.Year
	}
	if primary.Genre == "" {
		primary.Genre = fallback.Genre
	}
	return primary
}

func candidateKey(metadata model.EditableMetadata) string {
	return strings.ToLower(strings.Join([]string{
		strings.TrimSpace(metadata.Title),
		strings.TrimSpace(metadata.Artist),
		strings.TrimSpace(metadata.Album),
		strings.TrimSpace(metadata.AlbumArtist),
		strconv.Itoa(metadata.TrackNumber),
		strconv.Itoa(metadata.DiscNumber),
		strconv.Itoa(metadata.Year),
		strings.TrimSpace(metadata.Genre),
		strings.TrimSpace(metadata.CoverURL),
	}, "|"))
}

func readCurrentMetadata(path string) (model.EditableMetadata, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".mp3":
		return readMP3Metadata(path)
	case ".flac":
		return readFLACMetadata(path)
	default:
		return model.EditableMetadata{}, fmt.Errorf("unsupported audio format: %s", filepath.Ext(path))
	}
}

func readMP3Metadata(path string) (model.EditableMetadata, error) {
	tag, err := id3v2.Open(path, id3v2.Options{Parse: true})
	if err != nil {
		return model.EditableMetadata{}, err
	}
	defer tag.Close()

	comment := ""
	for _, frame := range tag.GetFrames(tag.CommonID("Comments")) {
		if typed, ok := frame.(id3v2.CommentFrame); ok {
			comment = typed.Text
			break
		}
	}

	lyrics := ""
	for _, frame := range tag.GetFrames(tag.CommonID("Unsynchronised lyrics/text transcription")) {
		if typed, ok := frame.(id3v2.UnsynchronisedLyricsFrame); ok {
			lyrics = typed.Lyrics
			break
		}
	}

	return model.EditableMetadata{
		Title:       tag.Title(),
		Artist:      tag.Artist(),
		Album:       tag.Album(),
		AlbumArtist: tag.GetTextFrame(tag.CommonID("Band/Orchestra/Accompaniment")).Text,
		TrackNumber: parseTagNumber(tag.GetTextFrame(tag.CommonID("Track number/Position in set")).Text),
		DiscNumber:  parseTagNumber(tag.GetTextFrame(tag.CommonID("Part of a set")).Text),
		Year:        parseTagNumber(tag.Year()),
		Genre:       tag.Genre(),
		Lyrics:      lyrics,
		Comment:     comment,
		Composer:    tag.GetTextFrame(tag.CommonID("Composer")).Text,
		Label:       tag.GetTextFrame(tag.CommonID("Publisher")).Text,
	}, nil
}

func readFLACMetadata(path string) (model.EditableMetadata, error) {
	if _, err := exec.LookPath("metaflac"); err != nil {
		return model.EditableMetadata{}, err
	}

	cmd := exec.Command("metaflac", "--export-tags-to=-", path)
	output, err := cmd.Output()
	if err != nil {
		return model.EditableMetadata{}, fmt.Errorf("metaflac export failed: %w", err)
	}

	values, err := parseMetaflacTags(output)
	if err != nil {
		return model.EditableMetadata{}, err
	}

	return model.EditableMetadata{
		Title:       values["TITLE"],
		Artist:      firstNonEmpty(values["ARTIST"], values["ARTISTS"]),
		Album:       values["ALBUM"],
		AlbumArtist: firstNonEmpty(values["ALBUMARTIST"], values["ALBUMARTISTS"]),
		TrackNumber: parseTagNumber(values["TRACKNUMBER"]),
		DiscNumber:  parseTagNumber(values["DISCNUMBER"]),
		Year:        parseTagNumber(firstNonEmpty(values["DATE"], values["YEAR"])),
		Genre:       values["GENRE"],
		Lyrics:      values["LYRICS"],
		Comment:     values["COMMENT"],
		Composer:    values["COMPOSER"],
		Label:       values["LABEL"],
	}, nil
}

func parseTagNumber(raw string) int {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0
	}
	if idx := strings.Index(raw, "/"); idx > 0 {
		raw = raw[:idx]
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0
	}
	return n
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func parseMetaflacTags(output []byte) (map[string]string, error) {
	values := map[string]string{}
	var currentKey string

	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		rawLine := strings.TrimRight(scanner.Text(), "\r")

		if idx := strings.Index(rawLine, "="); idx > 0 {
			key := strings.ToUpper(strings.TrimSpace(rawLine[:idx]))
			if isMetaflacTagKey(key) {
				if _, exists := values[key]; !exists {
					values[key] = strings.TrimSpace(rawLine[idx+1:])
				}
				currentKey = key
				continue
			}
		}

		if currentKey == "" {
			continue
		}

		// metaflac 导出的多行值会把后续行直接续写到下一行，这里把它们拼回原值。
		values[currentKey] += "\n" + rawLine
	}
	if err := scanner.Err(); err != nil {
		return nil, err
	}

	return values, nil
}

func isMetaflacTagKey(key string) bool {
	if key == "" {
		return false
	}
	for _, r := range key {
		if (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '_' {
			return false
		}
	}
	return true
}

func readSidecarLyrics(audioPath string) (string, error) {
	lrcPath := strings.TrimSuffix(audioPath, filepath.Ext(audioPath)) + ".lrc"
	data, err := os.ReadFile(lrcPath)
	if err != nil {
		return "", err
	}
	return normalizeLyricsText(string(data)), nil
}

func preferCurrentLyrics(currentLyrics, sidecarLyrics string) string {
	currentLyrics = normalizeLyricsText(currentLyrics)
	sidecarLyrics = normalizeLyricsText(sidecarLyrics)

	switch {
	case sidecarLyrics == "":
		return currentLyrics
	case currentLyrics == "":
		return sidecarLyrics
	case currentLyrics == sidecarLyrics:
		return currentLyrics
	case countLyricsLines(sidecarLyrics) > countLyricsLines(currentLyrics):
		return sidecarLyrics
	case len(sidecarLyrics) > len(currentLyrics) && !strings.Contains(currentLyrics, "\n"):
		return sidecarLyrics
	default:
		return currentLyrics
	}
}

func normalizeLyricsText(value string) string {
	value = strings.ReplaceAll(value, "\r\n", "\n")
	value = strings.ReplaceAll(value, "\r", "\n")
	return strings.TrimSpace(value)
}

func countLyricsLines(value string) int {
	value = normalizeLyricsText(value)
	if value == "" {
		return 0
	}
	return strings.Count(value, "\n") + 1
}
