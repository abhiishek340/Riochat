// Package lifecycle coordinates process startup hooks, background workers,
// detached tasks, the drain signal and ordered shutdown.
//
// Shutdown order (driven by the app): begin drain, stop the HTTP server, cancel
// workers and wait for them, wait (bounded) for detached tasks, run stop hooks
// in reverse registration order, then close the cache and database.
package lifecycle

import (
	"context"
	"fmt"
	"runtime/debug"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// Drain is a process-wide flag that flips once when shutdown begins.
// A nil *Drain is treated as "never draining".
type Drain struct {
	once sync.Once
	ch   chan struct{}
}

// NewDrain returns an unset drain signal.
func NewDrain() *Drain { return &Drain{ch: make(chan struct{})} }

// Begin flips the signal. It is idempotent.
func (d *Drain) Begin() {
	if d != nil {
		d.once.Do(func() { close(d.ch) })
	}
}

// Draining reports whether Begin was called. It never blocks.
func (d *Drain) Draining() bool {
	if d == nil {
		return false
	}
	select {
	case <-d.ch:
		return true
	default:
		return false
	}
}

// Done is closed when draining begins (waiters can select on it). On a nil
// Drain it returns a nil channel, which never fires.
func (d *Drain) Done() <-chan struct{} {
	if d == nil {
		return nil
	}
	return d.ch
}

type hook struct {
	name string
	fn   func(ctx context.Context) error
}

// Manager owns workers, detached tasks and hooks. The zero value is not
// usable; build one with NewManager.
type Manager struct {
	log   *logx.Logger
	drain *Drain

	ctx    context.Context
	cancel context.CancelFunc

	workers  sync.WaitGroup
	detached sync.WaitGroup

	mu         sync.Mutex
	startHooks []hook
	stopHooks  []hook
}

// NewManager builds a manager. Its worker context is canceled by StopWorkers.
func NewManager(log *logx.Logger) *Manager {
	if log == nil {
		log = logx.Nop()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Manager{log: log, drain: NewDrain(), ctx: ctx, cancel: cancel}
}

// Drain returns the process drain signal.
func (m *Manager) Drain() *Drain { return m.drain }

// Context is the worker context; it is canceled when workers must stop.
func (m *Manager) Context() context.Context { return m.ctx }

// Go starts a long-running worker. fn receives the worker context and should
// return when it is canceled. A panic is logged as "background_task_panic" and
// does not affect the process.
func (m *Manager) Go(name string, fn func(ctx context.Context)) {
	m.workers.Add(1)
	go func() {
		defer m.workers.Done()
		m.run(name, m.ctx, fn)
	}()
}

// Detach starts follow-up work that must outlive the originating request. It
// keeps ctx's values (request identifiers) but is not canceled when the request
// ends; timeout > 0 bounds it. In-flight tasks are tracked so shutdown can wait
// for them.
func (m *Manager) Detach(ctx context.Context, name string, timeout time.Duration, fn func(ctx context.Context)) {
	dctx := reqctx.Detach(ctx)
	cancel := func() {}
	if timeout > 0 {
		dctx, cancel = context.WithTimeout(dctx, timeout)
	}
	m.detached.Add(1)
	go func() {
		defer m.detached.Done()
		defer cancel()
		m.run(name, dctx, fn)
	}()
}

func (m *Manager) run(name string, ctx context.Context, fn func(context.Context)) {
	defer func() {
		if r := recover(); r != nil {
			m.log.Error("background_task_panic",
				logx.F("task", name), logx.F("panic", fmt.Sprint(r)), logx.F("stack", string(debug.Stack())))
		}
	}()
	fn(ctx)
}

// OnStart registers work to run after every feature is mounted and before the
// server accepts traffic (bootstrap data, one-shot reconciliation). Hooks run
// in registration order; the first error aborts startup.
func (m *Manager) OnStart(name string, fn func(ctx context.Context) error) {
	m.mu.Lock()
	m.startHooks = append(m.startHooks, hook{name, fn})
	m.mu.Unlock()
}

// OnStop registers a closer to run after workers and detached tasks have
// drained. Hooks run in reverse registration order; errors are logged.
func (m *Manager) OnStop(name string, fn func(ctx context.Context) error) {
	m.mu.Lock()
	m.stopHooks = append(m.stopHooks, hook{name, fn})
	m.mu.Unlock()
}

// RunStartHooks executes the OnStart hooks.
func (m *Manager) RunStartHooks(ctx context.Context) error {
	m.mu.Lock()
	hooks := append([]hook(nil), m.startHooks...)
	m.mu.Unlock()
	for _, h := range hooks {
		if err := h.fn(ctx); err != nil {
			return fmt.Errorf("startup hook %q: %w", h.name, err)
		}
	}
	return nil
}

// StopWorkers cancels the worker context and waits for workers to return, up
// to timeout. It reports whether all workers finished.
func (m *Manager) StopWorkers(timeout time.Duration) bool {
	m.cancel()
	return waitTimeout(&m.workers, timeout)
}

// WaitDetached waits for in-flight detached tasks, up to timeout, and reports
// whether they all finished.
func (m *Manager) WaitDetached(timeout time.Duration) bool {
	return waitTimeout(&m.detached, timeout)
}

// RunStopHooks executes the OnStop hooks in reverse order, logging failures.
func (m *Manager) RunStopHooks(ctx context.Context) {
	m.mu.Lock()
	hooks := append([]hook(nil), m.stopHooks...)
	m.mu.Unlock()
	for i := len(hooks) - 1; i >= 0; i-- {
		if err := hooks[i].fn(ctx); err != nil {
			m.log.Warn("shutdown_hook_failed", logx.F("hook", hooks[i].name), logx.Err(err))
		}
	}
}

func waitTimeout(wg *sync.WaitGroup, timeout time.Duration) bool {
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
		return true
	case <-time.After(timeout):
		return false
	}
}
