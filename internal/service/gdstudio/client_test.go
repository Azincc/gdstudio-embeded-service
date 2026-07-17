package gdstudio

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
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
