package worker

import (
	"strings"
	"testing"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/service/gdstudio"
	"github.com/azin/gdstudio-embed-service/internal/service/tagger"
	"go.uber.org/zap"
)

func TestResolveAlbumArtistFallsBackToFirstTrackArtist(t *testing.T) {
	task := &DownloadTask{logger: zap.NewNop()}

	albumArtist, source := task.resolveAlbumArtist("Artist B / Artist C")
	if albumArtist != "Artist B" {
		t.Fatalf("expected first artist fallback, got %q", albumArtist)
	}
	if source != model.AlbumArtistSourceFallbackFirstArtist {
		t.Fatalf("expected fallback source, got %q", source)
	}
}

func TestWriteRequiredTagsReturnsTaggerFailure(t *testing.T) {
	task := &DownloadTask{
		tagger: tagger.NewTagger(zap.NewNop()),
	}
	err := task.writeRequiredTags("missing-file.mp3", &model.TrackMetadata{
		Title:  "Song",
		Artist: "Artist",
	})
	if err == nil {
		t.Fatal("expected tag write failure to be returned")
	}
	if !strings.Contains(err.Error(), "failed to write tags") {
		t.Fatalf("unexpected tag write error: %v", err)
	}
}

func TestResolveAlbumArtistKeepsSingleTrackArtist(t *testing.T) {
	task := &DownloadTask{logger: zap.NewNop()}

	albumArtist, source := task.resolveAlbumArtist("Artist A")
	if albumArtist != "Artist A" {
		t.Fatalf("expected single artist to be preserved, got %q", albumArtist)
	}
	if source != model.AlbumArtistSourceFallbackArtist {
		t.Fatalf("expected single artist fallback source, got %q", source)
	}
}

func TestApplyGDMetadataOverridesExistingTrackMetadata(t *testing.T) {
	job := &model.Job{
		Title:       "Source Title",
		Artist:      "Source Artist",
		Album:       "Source Album",
		TrackNumber: 2,
		Year:        2024,
	}
	metadata := &gdstudio.MetadataResult{
		Title:       "Resolved Title",
		Artist:      "Resolved Artist",
		Album:       "Resolved Album",
		TrackNumber: 8,
		Year:        2025,
	}

	applyGDMetadata(job, metadata)
	if job.Title != metadata.Title || job.Artist != metadata.Artist || job.Album != metadata.Album {
		t.Fatalf("expected gdmusic text metadata to override existing values, got %+v", job)
	}
	if job.TrackNumber != metadata.TrackNumber || job.Year != metadata.Year {
		t.Fatalf("expected gdmusic numeric metadata to override existing values, got track=%d year=%d", job.TrackNumber, job.Year)
	}
}

func TestApplyGDMetadataFillsMissingTrackMetadata(t *testing.T) {
	job := &model.Job{}
	metadata := &gdstudio.MetadataResult{
		Title:       "Resolved Title",
		Artist:      "Resolved Artist",
		Album:       "Resolved Album",
		TrackNumber: 8,
		Year:        2025,
	}

	applyGDMetadata(job, metadata)
	if job.Title != metadata.Title || job.Artist != metadata.Artist || job.Album != metadata.Album {
		t.Fatalf("expected missing text metadata to be filled, got %+v", job)
	}
	if job.TrackNumber != metadata.TrackNumber || job.Year != metadata.Year {
		t.Fatalf("expected missing numeric metadata to be filled, got track=%d year=%d", job.TrackNumber, job.Year)
	}
}
