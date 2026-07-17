package metadata

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[{"id":"candidate-1","name":"Example Song","artist":"Example Artist","album":"Example Album","trackNumber":1,"year":2024}]`))
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
	response, resolvedPath, err := resolver.ResolveCandidates(
		context.Background(),
		model.SongMetadataReference{
			ID:     "song-1",
			Path:   "song.ogg",
			Title:  "Example Song",
			Artist: "Example Artist",
			Album:  "Example Album",
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
	if len(response.Candidates) != 2 {
		t.Fatalf("expected two source candidates, got %#v", response.Candidates)
	}
	if response.Candidates[0].Source != "gdstudio_netease" {
		t.Fatalf("unexpected first candidate source: %q", response.Candidates[0].Source)
	}
	if response.Candidates[1].Source != "gdstudio_kuwo" {
		t.Fatalf("unexpected second candidate source: %q", response.Candidates[1].Source)
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

func TestResolveCandidatesTreatsNoMatchAsSuccessfulEmptyResult(t *testing.T) {
	musicDir := t.TempDir()
	audioPath := filepath.Join(musicDir, "song.ogg")
	if err := os.WriteFile(audioPath, []byte("stub"), 0600); err != nil {
		t.Fatalf("write temp audio file failed: %v", err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
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

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	response, _, err := resolver.ResolveCandidates(
		ctx,
		model.SongMetadataReference{
			ID:     "song-no-match",
			Path:   "song.ogg",
			Title:  "Missing Song",
			Artist: "Missing Artist",
		},
		nil,
	)
	if err != nil {
		t.Fatalf("expected no-match lookup to succeed, got %v", err)
	}
	if response == nil || len(response.Candidates) != 0 {
		t.Fatalf("expected empty candidates, got %#v", response)
	}
	if got := observedLogs.FilterMessage("metadata candidate source completed without match").Len(); got != 2 {
		t.Fatalf("expected two no-match completion logs, got %d", got)
	}
	if got := observedLogs.FilterMessage("metadata candidate source attempt failed").Len(); got != 0 {
		t.Fatalf("expected no warning attempts for no-match results, got %d", got)
	}
}
