package metadata

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"go.uber.org/zap"
	"go.uber.org/zap/zaptest/observer"
)

func TestParseMetaflacTagsPreservesMultilineLyrics(t *testing.T) {
	output := []byte("TITLE=Example Song\nLYRICS=first line\nsecond line\n\nfourth line\nCOMMENT=a note\n")

	values, err := parseMetaflacTags(output)
	if err != nil {
		t.Fatalf("parseMetaflacTags returned error: %v", err)
	}

	if got := values["TITLE"]; got != "Example Song" {
		t.Fatalf("unexpected TITLE: %q", got)
	}

	wantLyrics := "first line\nsecond line\n\nfourth line"
	if got := values["LYRICS"]; got != wantLyrics {
		t.Fatalf("unexpected LYRICS: %q", got)
	}

	if got := values["COMMENT"]; got != "a note" {
		t.Fatalf("unexpected COMMENT: %q", got)
	}
}

func TestPreferCurrentLyricsUsesMoreCompleteSidecar(t *testing.T) {
	current := "first line"
	sidecar := "first line\nsecond line\nthird line"

	got := preferCurrentLyrics(current, sidecar)
	if got != sidecar {
		t.Fatalf("expected sidecar lyrics to win, got %q", got)
	}
}

func TestResolveCandidatesReportsLookupStages(t *testing.T) {
	musicDir := t.TempDir()
	audioPath := filepath.Join(musicDir, "song.ogg")
	if err := os.WriteFile(audioPath, []byte("stub"), 0600); err != nil {
		t.Fatalf("write temp audio file failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.URL.Query().Get("types") != "search" {
			http.Error(w, "unexpected request", http.StatusBadRequest)
			return
		}
		if got := req.URL.Query().Get("name"); got != "Example Song - Example Album - Requested Artist" {
			t.Errorf("unexpected search query: %q", got)
		}
		if got := req.URL.Query().Get("count"); got != "10" {
			t.Errorf("unexpected search count: %q", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"candidate-1","name":"Example Song","artist":"Different Artist","album":"Example Album","trackNumber":1,"year":2024},{"id":"candidate-2","name":"Example Song (Live)","artist":"Another Artist","album":"Live Album"}]`))
	}))
	defer server.Close()

	logCore, observedLogs := observer.New(zap.InfoLevel)
	logger := zap.New(logCore)
	gdClient := gdstudio.NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, logger)

	resolver := NewResolver(
		&config.Config{
			Storage: config.StorageConfig{
				MusicDir: musicDir,
			},
		},
		gdClient,
		logger,
	)

	var statuses []string
	response, resolvedPath, err := resolver.ResolveCandidatesWithSearch(
		context.Background(),
		model.SongMetadataReference{
			ID:     "song-1",
			Path:   "song.ogg",
			Title:  "Example Song",
			Artist: "Example Artist",
			Album:  "Example Album",
		},
		model.MetadataSearchOptions{
			Dimensions: []string{"title", "album", "artist"},
			Title:      "Example Song",
			Album:      "Example Album",
			Artist:     "Requested Artist",
		},
		func(status, _ string) {
			statuses = append(statuses, status)
		},
	)
	if err != nil {
		t.Fatalf("ResolveCandidates returned error: %v", err)
	}
	if resolvedPath != audioPath {
		t.Fatalf("unexpected resolved path: %q", resolvedPath)
	}
	if response == nil {
		t.Fatal("expected response")
	}
	if response.Current.Title != "Example Song" || response.Current.Artist != "Example Artist" {
		t.Fatalf("unexpected current metadata: %+v", response.Current)
	}
	if len(response.Candidates) != 4 {
		t.Fatalf("expected two results from each source, got %#v", response.Candidates)
	}
	if response.Candidates[0].Source != "gdstudio_netease" {
		t.Fatalf("unexpected first candidate source: %q", response.Candidates[0].Source)
	}
	if response.Candidates[0].TrackID != "candidate-1" || response.Candidates[0].Metadata.Artist != "Different Artist" {
		t.Fatalf("explicit search result was filtered or remapped: %#v", response.Candidates[0])
	}
	if response.Candidates[2].Source != "gdstudio_kuwo" {
		t.Fatalf("unexpected third candidate source: %q", response.Candidates[2].Source)
	}
	if got := observedLogs.FilterMessage("metadata candidate source lookup succeeded").Len(); got != 2 {
		t.Fatalf("expected two source success logs, got %d", got)
	}
	if got := observedLogs.FilterMessage("metadata candidate sources resolved").Len(); got != 1 {
		t.Fatalf("expected one candidate summary log, got %d", got)
	}

	wantStatuses := []string{
		model.MetadataCandidatesJobStatusSearchingSong,
		model.MetadataCandidatesJobStatusMergingData,
	}
	if len(statuses) != len(wantStatuses) {
		t.Fatalf("unexpected statuses: %#v", statuses)
	}
	for idx, want := range wantStatuses {
		if statuses[idx] != want {
			t.Fatalf("unexpected status at %d: got %q want %q", idx, statuses[idx], want)
		}
	}
}

func TestBuildMetadataSearchQueryUsesSelectedDimensionsInStableOrder(t *testing.T) {
	tests := []struct {
		name   string
		search model.MetadataSearchOptions
		want   string
	}{
		{
			name: "title only",
			search: model.MetadataSearchOptions{
				Dimensions: []string{"title"},
				Title:      "Slow Down",
			},
			want: "Slow Down",
		},
		{
			name: "title and artist",
			search: model.MetadataSearchOptions{
				Dimensions: []string{"artist", "title"},
				Title:      "Slow Down",
				Artist:     "雷米克斯, Settle一虾子",
			},
			want: "Slow Down - 雷米克斯, Settle一虾子",
		},
		{
			name: "all dimensions",
			search: model.MetadataSearchOptions{
				Dimensions: []string{"artist", "album", "title"},
				Title:      "Slow Down",
				Album:      "Slow Down",
				Artist:     "Keb’ Mo’",
			},
			want: "Slow Down - Slow Down - Keb’ Mo’",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := buildMetadataSearchQuery(tt.search)
			if err != nil {
				t.Fatalf("buildMetadataSearchQuery returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("unexpected query: got %q want %q", got, tt.want)
			}
		})
	}
}

func TestResolveCandidatesWithoutDimensionsOnlyReadsCurrentMetadata(t *testing.T) {
	musicDir := t.TempDir()
	audioPath := filepath.Join(musicDir, "song.ogg")
	if err := os.WriteFile(audioPath, []byte("stub"), 0600); err != nil {
		t.Fatalf("write temp audio file failed: %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		http.Error(w, "search should not be called", http.StatusInternalServerError)
	}))
	defer server.Close()

	logCore, observedLogs := observer.New(zap.InfoLevel)
	logger := zap.New(logCore)
	gdClient := gdstudio.NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, logger)
	resolver := NewResolver(
		&config.Config{Storage: config.StorageConfig{MusicDir: musicDir}},
		gdClient,
		logger,
	)

	response, _, err := resolver.ResolveCandidates(
		context.Background(),
		model.SongMetadataReference{
			ID:     "song-current-only",
			Path:   "song.ogg",
			Title:  "Current Song",
			Artist: "Current Artist",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected current-only lookup to succeed, got %v", err)
	}
	if response == nil || len(response.Candidates) != 0 {
		t.Fatalf("expected empty candidates, got %#v", response)
	}
	if response.Current.Title != "Current Song" || response.Current.Artist != "Current Artist" {
		t.Fatalf("unexpected current metadata: %#v", response.Current)
	}
	if requests.Load() != 0 {
		t.Fatalf("expected no GDMusic requests, got %d", requests.Load())
	}
	if got := observedLogs.FilterMessage("metadata candidate source lookup started").Len(); got != 0 {
		t.Fatalf("expected no source lookup logs, got %d", got)
	}
}

func TestResolveCandidatesRetriesEmptyListsUntilContextExpires(t *testing.T) {
	musicDir := t.TempDir()
	audioPath := filepath.Join(musicDir, "song.ogg")
	if err := os.WriteFile(audioPath, []byte("stub"), 0600); err != nil {
		t.Fatalf("write temp audio file failed: %v", err)
	}

	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	logger := zap.NewNop()
	gdClient := gdstudio.NewClient(&config.GDStudioConfig{
		BaseURL: server.URL,
		Timeout: time.Second,
	}, logger)
	resolver := NewResolver(
		&config.Config{Storage: config.StorageConfig{MusicDir: musicDir}},
		gdClient,
		logger,
	)

	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	response, _, err := resolver.ResolveCandidatesWithSearch(
		ctx,
		model.SongMetadataReference{
			ID:     "song-empty-response",
			Path:   "song.ogg",
			Title:  "Missing Song",
			Artist: "Missing Artist",
		},
		model.MetadataSearchOptions{
			Dimensions: []string{"title", "artist"},
			Title:      "Missing Song",
			Artist:     "Missing Artist",
		},
		nil,
	)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected retries to stop at the context deadline, got %v", err)
	}
	if response != nil {
		t.Fatalf("expected no candidate response for repeated empty lists, got %#v", response)
	}
	if requests.Load() <= 2 {
		t.Fatalf("expected empty lists to trigger another attempt, got %d requests", requests.Load())
	}
}

func TestCollectSourceLookupResultsPreservesSuccessfulSource(t *testing.T) {
	resultCh := make(chan sourceLookupResult, 2)
	resultCh <- sourceLookupResult{
		source: "netease",
		metadata: []*gdstudio.MetadataResult{
			{
				Title:  "Found Song",
				Artist: "Found Artist",
			},
		},
	}
	resultCh <- sourceLookupResult{
		source: "kuwo",
		err:    errors.New("temporary source failure"),
	}

	resolved, lookupErrors, successful := collectSourceLookupResults(resultCh, 2)
	if successful != 1 {
		t.Fatalf("expected one successful source, got %d", successful)
	}
	if len(lookupErrors) != 1 {
		t.Fatalf("expected one source error, got %#v", lookupErrors)
	}
	if len(resolved["netease"]) != 1 || resolved["netease"][0].Title != "Found Song" {
		t.Fatalf("successful source result was not preserved: %#v", resolved)
	}
}
