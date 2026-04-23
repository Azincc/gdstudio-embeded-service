package metadata

import "testing"

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
