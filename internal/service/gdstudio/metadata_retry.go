package gdstudio

import (
	"context"
	"fmt"
	"time"
)

const (
	MetadataRetryMaxElapsed = 3 * time.Minute
	metadataRetryBaseWait   = 1 * time.Second
	metadataRetryMaxWait    = 30 * time.Second
)

// RetryMetadata 在三分钟窗口内指数退避重试 GDMusic 元数据查询。
func RetryMetadata(ctx context.Context, fn func(context.Context) error) error {
	return retryMetadataWithPolicy(
		ctx,
		MetadataRetryMaxElapsed,
		metadataRetryBaseWait,
		metadataRetryMaxWait,
		fn,
	)
}

func retryMetadataWithPolicy(
	ctx context.Context,
	maxElapsed time.Duration,
	baseWait time.Duration,
	maxWait time.Duration,
	fn func(context.Context) error,
) error {
	if fn == nil {
		return fmt.Errorf("metadata retry function is nil")
	}
	if maxElapsed <= 0 || baseWait <= 0 || maxWait <= 0 {
		return fmt.Errorf("metadata retry durations must be positive")
	}

	retryCtx, cancel := context.WithTimeout(ctx, maxElapsed)
	defer cancel()

	startedAt := time.Now()
	var lastErr error
	for attempt := 0; ; attempt++ {
		if err := retryCtx.Err(); err != nil {
			if lastErr != nil {
				return fmt.Errorf("metadata retry stopped: %w; last error: %v", err, lastErr)
			}
			return err
		}

		lastErr = fn(retryCtx)
		if lastErr == nil {
			return nil
		}

		wait := metadataBackoffDelay(baseWait, maxWait, attempt)
		remaining := maxElapsed - time.Since(startedAt)
		if remaining <= 0 || wait >= remaining {
			return lastErr
		}

		timer := time.NewTimer(wait)
		select {
		case <-retryCtx.Done():
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			return fmt.Errorf("metadata retry stopped: %w; last error: %v", retryCtx.Err(), lastErr)
		case <-timer.C:
		}
	}
}

func metadataBackoffDelay(baseWait, maxWait time.Duration, attempt int) time.Duration {
	if attempt < 0 {
		attempt = 0
	}
	wait := baseWait
	for i := 0; i < attempt && wait < maxWait; i++ {
		if wait > maxWait/2 {
			return maxWait
		}
		wait *= 2
	}
	if wait > maxWait {
		return maxWait
	}
	return wait
}
