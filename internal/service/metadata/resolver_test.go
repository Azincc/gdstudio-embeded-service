package metadata

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/azin/gdstudio-embed-service/internal/config"
	"github.com/azin/gdstudio-embed-service/internal/model"
	"go.uber.org/zap"
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

	resolver := NewResolver(
		&config.Config{
			Storage: config.StorageConfig{
				MusicDir: musicDir,
			},
		},
		nil,
		nil,
		zap.NewNop(),
	)

	var statuses []string
	response, resolvedPath, err := resolver.ResolveCandidates(
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

	wantStatuses := []string{
		model.MetadataCandidatesJobStatusMatchingFingerprint,
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
