package musicbrainz

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"go.uber.org/zap"
)

func TestParsePlainFingerprintOutput(t *testing.T) {
	fp := parsePlainFingerprintOutput("DURATION=212\nFINGERPRINT=abc123\n")
	if fp == nil {
		t.Fatal("expected fingerprint to be parsed")
	}
	if fp.Duration != 212 {
		t.Fatalf("unexpected duration: %d", fp.Duration)
	}
	if fp.Fingerprint != "abc123" {
		t.Fatalf("unexpected fingerprint: %q", fp.Fingerprint)
	}
}

func TestGenerateFingerprintJSONDurationCanBeFloat(t *testing.T) {
	var jsonFP struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	payload := []byte(`{"duration":328.07,"fingerprint":"abc123"}`)
	if err := json.Unmarshal(payload, &jsonFP); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if got := int(math.Round(jsonFP.Duration)); got != 328 {
		t.Fatalf("unexpected rounded duration: %d", got)
	}
	if jsonFP.Fingerprint != "abc123" {
		t.Fatalf("unexpected fingerprint: %q", jsonFP.Fingerprint)
	}
}

func TestPickTrackPositionHandlesSlashNumber(t *testing.T) {
	trackNumber, discNumber := pickTrackPosition([]medium{
		{
			Position: 2,
			Track: []track{
				{Number: "3/20", Title: "Believer"},
			},
		},
	}, "Believer")

	if trackNumber != 3 {
		t.Fatalf("unexpected track number: %d", trackNumber)
	}
	if discNumber != 2 {
		t.Fatalf("unexpected disc number: %d", discNumber)
	}
}

func TestExtractAlbumArtistUsesFullReleaseArtistCredit(t *testing.T) {
	client := &Client{}
	recordings := []recordingResult{
		{
			Title: "Song",
			Releases: []release{
				{
					Title: "Album",
					ArtistCredit: []artistCredit{
						{Name: "Artist A"},
						{Name: "Artist B"},
					},
				},
			},
		},
	}

	albumArtist := client.extractAlbumArtist(recordings, "Song", "Album")
	if albumArtist != "Artist A / Artist B" {
		t.Fatalf("unexpected album artist: %q", albumArtist)
	}
}

func TestLookupAlbumArtistFallsBackToReleaseArtistCredit(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/recording/":
			_, _ = w.Write([]byte(`{"recordings":[]}`))
		case "/release/":
			_, _ = w.Write([]byte(`{"releases":[{"title":"FINAL FANTASY XIV: DAWNTRAIL - EP5","status":"Official","date":"2025-04-23","artist-credit":[{"name":"祖堅正慶","artist":{"name":"祖堅正慶"}}]}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(&config.MusicBrainzConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		UserAgent:   "test-agent",
		RateLimitMs: 0,
		Timeout:     5 * time.Second,
	}, zap.NewNop())

	albumArtist, err := client.LookupAlbumArtist(
		"决行 〜姫をさがして：黄金〜",
		"植松伸夫 / 矢崎早彩",
		"FINAL FANTASY XIV: DAWNTRAIL - EP5",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if albumArtist != "祖堅正慶" {
		t.Fatalf("unexpected album artist: %q", albumArtist)
	}
}

func TestLookupTrackMetadataByReleaseFillsTrackMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.URL.Path == "/release/" && r.URL.Query().Get("query") != "":
			_, _ = w.Write([]byte(`{"releases":[{"id":"rel-1","title":"FINAL FANTASY XIV: DAWNTRAIL - EP5","status":"Official","date":"2025-04-23","artist-credit":[{"name":"祖堅正慶","artist":{"name":"祖堅正慶"}}],"release-group":{"id":"rg-1","title":"FINAL FANTASY XIV: DAWNTRAIL - EP5"}}]}`))
		case r.URL.Path == "/release/rel-1":
			_, _ = w.Write([]byte(`{"id":"rel-1","title":"FINAL FANTASY XIV: DAWNTRAIL - EP5","date":"2025-04-23","artist-credit":[{"name":"祖堅正慶","artist":{"name":"祖堅正慶"}}],"media":[{"position":1,"tracks":[{"number":"5","title":"決行～姫をさがして:黄金～","artist-credit":[{"name":"祖堅正慶","artist":{"name":"祖堅正慶"}}],"recording":{"id":"rec-1","title":"決行～姫をさがして:黄金～","artist-credit":[{"name":"祖堅正慶","artist":{"name":"祖堅正慶"}}],"first-release-date":"2025-04-23"}}]}]}`))
		case strings.HasSuffix(r.URL.Path, "/front"):
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`not found`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	client := NewClient(&config.MusicBrainzConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		CoverArtURL: server.URL,
		UserAgent:   "test-agent",
		RateLimitMs: 0,
		Timeout:     5 * time.Second,
	}, zap.NewNop())

	metadata, err := client.LookupTrackMetadata(
		"决行 〜姫をさがして：黄金〜",
		"植松伸夫 / 矢崎早彩",
		"FINAL FANTASY XIV: DAWNTRAIL - EP5",
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if metadata == nil {
		t.Fatal("expected metadata")
	}
	if metadata.Title != "決行～姫をさがして:黄金～" {
		t.Fatalf("unexpected title: %q", metadata.Title)
	}
	if metadata.Artist != "祖堅正慶" {
		t.Fatalf("unexpected artist: %q", metadata.Artist)
	}
	if metadata.AlbumArtist != "祖堅正慶" {
		t.Fatalf("unexpected album artist: %q", metadata.AlbumArtist)
	}
	if metadata.TrackNumber != 5 {
		t.Fatalf("unexpected track number: %d", metadata.TrackNumber)
	}
	if metadata.Date != "2025-04-23" || metadata.Year != 2025 {
		t.Fatalf("unexpected date/year: %q %d", metadata.Date, metadata.Year)
	}
	if metadata.MusicBrainzRecordingID != "rec-1" || metadata.MusicBrainzReleaseID != "rel-1" || metadata.MusicBrainzReleaseGroupID != "rg-1" {
		t.Fatalf("unexpected musicbrainz ids: recording=%q release=%q release_group=%q",
			metadata.MusicBrainzRecordingID, metadata.MusicBrainzReleaseID, metadata.MusicBrainzReleaseGroupID)
	}
}

func TestSearchRecordingsRetriesEOFAndEventuallySucceeds(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		current := attempts.Add(1)
		if current <= 2 {
			hjacker, ok := w.(http.Hijacker)
			if !ok {
				t.Fatal("response writer does not support hijacking")
			}
			conn, _, err := hjacker.Hijack()
			if err != nil {
				t.Fatalf("hijack failed: %v", err)
			}
			_ = conn.Close()
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"recordings":[{"id":"rec-1","title":"Song"}]}`))
	}))
	defer server.Close()

	client := NewClient(&config.MusicBrainzConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		UserAgent:   "test-agent",
		RateLimitMs: 0,
		Timeout:     5 * time.Second,
		RetryCount:  2,
	}, zap.NewNop())

	result, err := client.searchRecordings(`recording:"Song"`, 10)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result.Recordings) != 1 || result.Recordings[0].ID != "rec-1" {
		t.Fatalf("unexpected result: %+v", result.Recordings)
	}
	if got := attempts.Load(); got != 3 {
		t.Fatalf("unexpected attempts: %d", got)
	}
}

func TestSearchReleasesDoesNotRetryBadRequest(t *testing.T) {
	var attempts atomic.Int32

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		http.Error(w, "bad request", http.StatusBadRequest)
	}))
	defer server.Close()

	client := NewClient(&config.MusicBrainzConfig{
		Enabled:     true,
		BaseURL:     server.URL,
		UserAgent:   "test-agent",
		RateLimitMs: 0,
		Timeout:     5 * time.Second,
		RetryCount:  3,
	}, zap.NewNop())

	_, err := client.searchReleases(`release:"Bad"`, 5)
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "musicbrainz unexpected status: 400") {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("unexpected attempts: %d", got)
	}
}
