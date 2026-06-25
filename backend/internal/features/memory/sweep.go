package memory

import (
	"context"
	"time"
)

// sweepLoop removes expired cache entries once per sweepEvery until ctx ends.
func sweepLoop(ctx context.Context, svc *Service) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			svc.SweepCache()
		}
	}
}
