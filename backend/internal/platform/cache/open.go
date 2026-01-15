package cache

import (
	"context"
	"fmt"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

// Open builds the backend selected by cfg.Cache.Driver. The redis driver pings
// the server and fails when it is unreachable.
func Open(ctx context.Context, cfg *config.Config, clk clock.Clock) (Cache, error) {
	switch cfg.Cache.Driver {
	case config.CacheMemory:
		return NewMemory(clk), nil
	case config.CacheRedis:
		return OpenRedis(ctx, cfg.Database.Redis, clk)
	default:
		return nil, fmt.Errorf("unsupported cache driver %q", cfg.Cache.Driver)
	}
}
