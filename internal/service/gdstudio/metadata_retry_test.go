package gdstudio

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMetadataBackoffDelay(t *testing.T) {
	want := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		30 * time.Second,
	}
	for attempt, expected := range want {
		if got := metadataBackoffDelay(time.Second, 30*time.Second, attempt); got != expected {
			t.Fatalf("attempt %d: got %s want %s", attempt, got, expected)
		}
	}
}

func TestMetadataRetryWindowIsThreeMinutes(t *testing.T) {
	if MetadataRetryMaxElapsed != 3*time.Minute {
		t.Fatalf("unexpected metadata retry window: %s", MetadataRetryMaxElapsed)
	}
}

func TestRetryMetadataRetriesUntilSuccess(t *testing.T) {
	attempts := 0
	err := retryMetadataWithPolicy(
		context.Background(),
		50*time.Millisecond,
		time.Millisecond,
		2*time.Millisecond,
		func(context.Context) error {
			attempts++
			if attempts < 3 {
				return errors.New("temporary failure")
			}
			return nil
		},
	)
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if attempts != 3 {
		t.Fatalf("unexpected attempt count: %d", attempts)
	}
}
