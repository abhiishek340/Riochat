package ragindex

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// workerPoll is how long an idle queue worker sleeps before polling again.
const workerPoll = 2 * time.Second

// sweepEvery is the interval of the stalled-task sweep.
const sweepEvery = time.Minute

// Mount registers the area with default collaborators: embedding stays off
// until MountWith supplies a configuration source and a provider client.
func Mount(d deps.Deps) error {
	_, err := MountWith(d, Options{})
	return err
}

// MountWith starts the queue workers and the recovery sweep and returns the
// service for the integration layer. The area registers no routes: the
// submission endpoint belongs to the chat-file store and the administrator
// operations to the settings area, which both call this service.
func MountWith(d deps.Deps, o Options) (*Service, error) {
	if d.DB == nil {
		return nil, errors.New("ragindex: database is required")
	}
	svc := New(d, o)
	if !o.DisableWorkers {
		n := o.Workers
		if n <= 0 {
			n = defaultWorkers
		}
		for i := 0; i < n; i++ {
			d.Lifecycle.Go(fmt.Sprintf("rag-embed-worker-%d", i), svc.workerLoop)
		}
		d.Lifecycle.Go("rag-stall-sweep", svc.sweepLoop)
	}
	d.Lifecycle.OnStart("rag-startup-recovery", func(ctx context.Context) error {
		if !d.DB.Migrator().HasTable(&File{}) {
			return nil
		}
		if n, err := svc.Sweep(ctx); err != nil {
			svc.log.Warn("rag_startup_sweep_failed", logx.Err(err))
		} else if n > 0 {
			svc.log.Info("rag_startup_sweep", logx.F("recovered", n))
		}
		if err := svc.Reconcile(ctx); err != nil {
			return fmt.Errorf("ragindex: startup reconciliation: %w", err)
		}
		return nil
	})
	return svc, nil
}

// workerLoop consumes the durable queue until the application stops.
func (s *Service) workerLoop(ctx context.Context) {
	for ctx.Err() == nil {
		if s.processNext(ctx) {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-s.wake:
		case <-time.After(workerPoll):
		}
	}
}

func (s *Service) sweepLoop(ctx context.Context) {
	t := time.NewTicker(sweepEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if _, err := s.Sweep(ctx); err != nil && ctx.Err() == nil {
				s.log.Warn("rag_sweep_failed", logx.Err(err))
			}
		}
	}
}
