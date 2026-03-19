package musicbrainz

import (
	"encoding/json"
	"math"
	"testing"
)

func TestParsePlainFingerprintOutput(t *testing.T) {
	fp := parsePlainFingerprintOutput("DURATION=212\nFINGERPRINT=abc123\n")
	if fp == nil {
		t.Fatal("expected fingerprint to be parsed")
	}
	if fp.Duration != 212 {
		t.Fatalf("unexpected duration: %d", fp.Duration)
	}
	if fp.Fingerprint != "abc123" {
		t.Fatalf("unexpected fingerprint: %q", fp.Fingerprint)
	}
}

func TestGenerateFingerprintJSONDurationCanBeFloat(t *testing.T) {
	var jsonFP struct {
		Duration    float64 `json:"duration"`
		Fingerprint string  `json:"fingerprint"`
	}
	payload := []byte(`{"duration":328.07,"fingerprint":"abc123"}`)
	if err := json.Unmarshal(payload, &jsonFP); err != nil {
		t.Fatalf("unexpected unmarshal error: %v", err)
	}
	if got := int(math.Round(jsonFP.Duration)); got != 328 {
		t.Fatalf("unexpected rounded duration: %d", got)
	}
	if jsonFP.Fingerprint != "abc123" {
		t.Fatalf("unexpected fingerprint: %q", jsonFP.Fingerprint)
	}
}

func TestPickTrackPositionHandlesSlashNumber(t *testing.T) {
	trackNumber, discNumber := pickTrackPosition([]medium{
		{
			Position: 2,
			Track: []track{
				{Number: "3/20", Title: "Believer"},
			},
		},
	}, "Believer")

	if trackNumber != 3 {
		t.Fatalf("unexpected track number: %d", trackNumber)
	}
	if discNumber != 2 {
		t.Fatalf("unexpected disc number: %d", discNumber)
	}
}
