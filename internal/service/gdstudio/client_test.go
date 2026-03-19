package gdstudio

import "testing"

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
