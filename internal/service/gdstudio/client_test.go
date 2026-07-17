package gdstudio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"go.uber.org/zap"
)

func TestPickMetadataPrefersExactTrackID(t *testing.T) {
	items := []map[string]interface{}{
		{
			"id":   "wrong-id",
			"name": "Song",
			"artist": []interface{}{
				map[string]interface{}{"name": "Other Artist"},
			},
			"album":    map[string]interface{}{"name": "Other Album"},
			"pic_id":   "wrong-pic",
			"lyric_id": "wrong-lyric",
		},
		{
			"id":   "123",
			"name": "Song",
			"artist": []interface{}{
				map[string]interface{}{"name": "Artist A"},
				map[string]interface{}{"name": "Artist B"},
			},
			"album":       map[string]interface{}{"name": "Album A"},
			"trackNumber": float64(7),
			"publishTime": "2021-09-01",
			"pic_id":      "pic-123",
			"lyric_id":    "lyric-123",
		},
	}

	metadata, ok := pickMetadata(items, "123", "Song", "Artist A")
	if !ok {
		t.Fatal("expected metadata to be selected")
	}
	if metadata.Title != "Song" {
		t.Fatalf("unexpected title: %q", metadata.Title)
	}
	if metadata.Artist != "Artist A / Artist B" {
		t.Fatalf("unexpected artist: %q", metadata.Artist)
	}
	if metadata.Album != "Album A" {
		t.Fatalf("unexpected album: %q", metadata.Album)
	}
	if metadata.TrackNumber != 7 {
		t.Fatalf("unexpected track number: %d", metadata.TrackNumber)
	}
	if metadata.Year != 2021 {
		t.Fatalf("unexpected year: %d", metadata.Year)
	}
	if metadata.PicID != "pic-123" || metadata.LyricID != "lyric-123" {
		t.Fatalf("unexpected aux ids: pic=%q lyric=%q", metadata.PicID, metadata.LyricID)
	}
}

func TestPickMetadataMatchesNullSeparatedArtistAgainstNestedArtists(t *testing.T) {
	items := []map[string]interface{}{
		{
			"id":   "song-1",
			"name": "Slow Down",
			"artist": []interface{}{
				map[string]interface{}{"name": "Keb’ Mo’"},
			},
			"album": map[string]interface{}{"name": "Slow Down"},
		},
	}

	metadata, ok := pickMetadata(items, "", "Slow Down", "雷米克斯\x00Keb’ Mo’")
	if !ok {
		t.Fatal("expected a nested artist to match one value from a NUL-separated tag")
	}
	if metadata.Artist != "Keb’ Mo’" {
		t.Fatalf("unexpected matched artist: %q", metadata.Artist)
	}
}

func TestSearchMetadataContextReturnsRawResultsWithoutArtistFiltering(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.URL.Query().Get("name"); got != "Slow Down - 雷米克斯, Settle一虾子" {
			t.Errorf("unexpected search query: %q", got)
		}
		if got := req.URL.Query().Get("count"); got != "2" {
			t.Errorf("unexpected result count: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"15228380","name":"Slow Down","artist":"Keb' Mo'","album":"Slow Down"},{"id":"1741454","name":"Slow Down (Live)","artist":[{"name":"Different Artist"}],"album":{"name":"Live"}}]`))
	}))
	defer server.Close()

	client := NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, zap.NewNop())
	results, err := client.SearchMetadataContext(
		context.Background(),
		"kuwo",
		"Slow Down - 雷米克斯, Settle一虾子",
		2,
	)
	if err != nil {
		t.Fatalf("SearchMetadataContext returned error: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("expected both raw results, got %#v", results)
	}
	if results[0].TrackID != "15228380" || results[0].Artist != "Keb' Mo'" {
		t.Fatalf("unexpected first result: %#v", results[0])
	}
	if results[1].TrackID != "1741454" || results[1].Artist != "Different Artist" {
		t.Fatalf("unexpected second result: %#v", results[1])
	}
}

func TestResolveCoverPreviewContextFallsBackFrom500To300(t *testing.T) {
	var sizes []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if got := req.URL.Query().Get("types"); got != "pic" {
			t.Errorf("unexpected request type: %q", got)
		}
		if got := req.URL.Query().Get("id"); got != "pic-1" {
			t.Errorf("unexpected pic id: %q", got)
		}
		size := req.URL.Query().Get("size")
		sizes = append(sizes, size)
		w.Header().Set("Content-Type", "application/json")
		if size == "500" {
			_, _ = w.Write([]byte(`{"url":""}`))
			return
		}
		_, _ = w.Write([]byte(`{"url":"https://img.test/pic-1-300.jpg"}`))
	}))
	defer server.Close()

	client := NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, zap.NewNop())
	coverURL, err := client.ResolveCoverPreviewContext(
		context.Background(), "netease", "pic-1",
	)
	if err != nil {
		t.Fatalf("ResolveCoverPreviewContext returned error: %v", err)
	}
	if coverURL != "https://img.test/pic-1-300.jpg" {
		t.Fatalf("unexpected cover URL: %q", coverURL)
	}
	if got := strings.Join(sizes, ","); got != "500,300" {
		t.Fatalf("unexpected size fallback order: %q", got)
	}
}

func TestEmptySearchListIsRetriedAsTransientFailure(t *testing.T) {
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client := NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, zap.NewNop())

	_, err := client.ResolveMetadataContext(
		context.Background(),
		"netease",
		"",
		"Missing Song",
		"Missing Artist",
	)
	if !errors.Is(err, ErrMetadataEmptyResponse) {
		t.Fatalf("expected empty response error, got %v", err)
	}

	requests.Store(0)
	err = retryMetadataWithPolicy(
		context.Background(),
		50*time.Millisecond,
		time.Millisecond,
		2*time.Millisecond,
		func(ctx context.Context) error {
			_, lookupErr := client.ResolveMetadataContext(
				ctx,
				"netease",
				"",
				"Missing Song",
				"Missing Artist",
			)
			return lookupErr
		},
	)
	if err == nil {
		t.Fatal("expected empty list retries to fail after the retry window")
	}
	if requests.Load() <= 2 {
		t.Fatalf("expected repeated searches for empty lists, got %d requests", requests.Load())
	}
}
