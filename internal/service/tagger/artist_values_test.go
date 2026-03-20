package tagger

import "testing"

func TestSplitArtistValuesSeparatesExplicitMultiArtistStrings(t *testing.T) {
	values := splitArtistValues("植松伸夫 / 矢崎早彩")
	if len(values) != 2 {
		t.Fatalf("expected 2 values, got %d", len(values))
	}
	if values[0] != "植松伸夫" || values[1] != "矢崎早彩" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestSplitArtistValuesKeepsSlashInsideSingleArtistName(t *testing.T) {
	values := splitArtistValues("AC/DC")
	if len(values) != 1 || values[0] != "AC/DC" {
		t.Fatalf("unexpected values: %#v", values)
	}
}

func TestEncodeID3v24MultiValueTextUsesNullSeparator(t *testing.T) {
	got := encodeID3v24MultiValueText("Artist A / Artist B")
	want := "Artist A\x00Artist B"
	if got != want {
		t.Fatalf("unexpected encoded text: %q", got)
	}
}

func TestAppendVorbisMultiValueTagsAddsRepeatedPluralTags(t *testing.T) {
	args := appendVorbisMultiValueTags(nil, "ARTISTS", "Artist A / Artist B")
	if len(args) != 2 {
		t.Fatalf("expected 2 args, got %d", len(args))
	}
	if args[0] != "--set-tag=ARTISTS=Artist A" || args[1] != "--set-tag=ARTISTS=Artist B" {
		t.Fatalf("unexpected args: %#v", args)
	}
}
