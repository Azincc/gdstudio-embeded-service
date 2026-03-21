package worker

import (
	"testing"

	"github.com/azin/gdstudio-embed-service/internal/model"
	"github.com/azin/gdstudio-embed-service/internal/service/musicbrainz"
	"go.uber.org/zap"
)

func TestChooseAlbumArtistPrefersReliableCurrentValue(t *testing.T) {
	albumArtist, source, ok := chooseAlbumArtist("Artist A", model.AlbumArtistSourceFingerprint, "Artist B")
	if !ok {
		t.Fatal("expected reliable album artist to be selected")
	}
	if albumArtist != "Artist A" || source != model.AlbumArtistSourceFingerprint {
		t.Fatalf("expected current reliable value, got artist=%q source=%q", albumArtist, source)
	}
}

func TestChooseAlbumArtistReusesSharedValueWhenCurrentIsWeak(t *testing.T) {
	albumArtist, source, ok := chooseAlbumArtist("Artist B", model.AlbumArtistSourceFallbackFirstArtist, "Artist A")
	if !ok {
		t.Fatal("expected shared album artist to be selected")
	}
	if albumArtist != "Artist A" || source != model.AlbumArtistSourceAlbumShared {
		t.Fatalf("expected shared album artist, got artist=%q source=%q", albumArtist, source)
	}
}

func TestResolveAlbumArtistFallsBackToFirstTrackArtist(t *testing.T) {
	task := &DownloadTask{logger: zap.NewNop()}

	albumArtist, source := task.resolveAlbumArtist("Track 3", "Artist B / Artist C", "Same Album")
	if albumArtist != "Artist B" {
		t.Fatalf("expected first artist fallback, got %q", albumArtist)
	}
	if source != model.AlbumArtistSourceFallbackFirstArtist {
		t.Fatalf("expected fallback source, got %q", source)
	}
}

func TestApplyFingerprintMetadataPreservesAlbumArtistSource(t *testing.T) {
	job := &model.Job{}
	metadata := &musicbrainz.FingerprintMetadata{
		AlbumArtist:       "Artist A / Artist B",
		AlbumArtistSource: model.AlbumArtistSourceFingerprint,
	}

	applyFingerprintMetadata(job, metadata)
	if job.AlbumArtist != "Artist A / Artist B" {
		t.Fatalf("expected album artist to be applied, got %q", job.AlbumArtist)
	}
	if job.AlbumArtistSource != model.AlbumArtistSourceFingerprint {
		t.Fatalf("expected fingerprint source, got %q", job.AlbumArtistSource)
	}
}

func TestApplyFingerprintMetadataPrefersMusicBrainzTrackTitleAndArtist(t *testing.T) {
	job := &model.Job{
		Title:  "决行 〜姫をさがして：黄金〜",
		Artist: "植松伸夫 / 矢崎早彩",
	}
	metadata := &musicbrainz.FingerprintMetadata{
		Title:             "決行～姫をさがして:黄金～",
		Artist:            "祖堅正慶",
		AlbumArtist:       "祖堅正慶",
		AlbumArtistSource: model.AlbumArtistSourceMusicBrainz,
		TrackNumber:       5,
		Year:              2025,
	}

	applyFingerprintMetadata(job, metadata)
	if job.Title != "決行～姫をさがして:黄金～" {
		t.Fatalf("expected musicbrainz title to override source value, got %q", job.Title)
	}
	if job.Artist != "祖堅正慶" {
		t.Fatalf("expected musicbrainz artist to override source value, got %q", job.Artist)
	}
	if job.AlbumArtist != "祖堅正慶" {
		t.Fatalf("expected album artist to be updated, got %q", job.AlbumArtist)
	}
	if job.AlbumArtistSource != model.AlbumArtistSourceMusicBrainz {
		t.Fatalf("expected musicbrainz album artist source, got %q", job.AlbumArtistSource)
	}
	if job.TrackNumber != 5 || job.Year != 2025 {
		t.Fatalf("expected track number/year to be updated, got track=%d year=%d", job.TrackNumber, job.Year)
	}
}
