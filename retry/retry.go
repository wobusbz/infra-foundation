package retry

import (
	"context"
	"time"
)

func Retry(ctx context.Context, minx, maxx time.Duration, cb func() error) {
	backoff := minx
	for {
		if err := cb(); err == nil {
			return
		}
		select {
		case <-ctx.Done():
			return
		default:
			time.Sleep(backoff)
			if backoff >= maxx {
				backoff = maxx
				continue
			}
			backoff *= 2
		}
	}
}
