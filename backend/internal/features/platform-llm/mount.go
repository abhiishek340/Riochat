package platformllm

import (
	"context"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns no steps: the area owns no tables (the model catalogue
// tables belong to the channel area).
func Migrations() []migrate.Step { return nil }

// Mount builds the Platform, publishes it through Current and ties its
// lifetime to the application: tracing is flushed and idle connections are
// closed when the process stops.
func Mount(d deps.Deps) error {
	p, err := New(Params{Config: d.Config, Logger: d.Logger, Spawner: d.Lifecycle})
	if err != nil {
		return err
	}
	current.Store(p)
	d.Lifecycle.OnStop("platform-llm-shutdown", func(ctx context.Context) error {
		err := p.Shutdown(ctx)
		current.CompareAndSwap(p, nil)
		return err
	})
	return nil
}
