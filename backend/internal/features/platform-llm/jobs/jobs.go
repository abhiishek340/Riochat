// Package jobs runs background work for the process: periodic jobs, bounded
// worker queues, batch loops and retry helpers. Every run is panic-safe and
// stops promptly when the worker context is canceled.
package jobs

import (
	"context"
	"errors"
	"fmt"
	"runtime/debug"
	"sort"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Spawner starts a long-running worker that must return when ctx is done.
// *lifecycle.Manager satisfies it.
type Spawner interface {
	Go(name string, fn func(ctx context.Context))
}

// Job describes a periodic task.
type Job struct {
	Name string
	// Every is the interval between the end of one run and the start of the
	// next. Zero or negative runs the job once.
	Every time.Duration
	// RunAtStart runs immediately when the worker starts instead of waiting
	// one interval.
	RunAtStart bool
	// Timeout bounds one run (0 = unbounded).
	Timeout time.Duration
	Run     func(ctx context.Context) error
}

// Status is a snapshot of a job's history.
type Status struct {
	Name     string
	Runs     int
	Failures int
	LastRun  time.Time
	LastErr  string
	Running  bool
}

// Runner owns the registered jobs.
type Runner struct {
	spawn Spawner
	log   *logx.Logger
	now   func() time.Time
	after func(time.Duration) <-chan time.Time

	mu     sync.Mutex
	status map[string]*Status
}

// Options configures a Runner.
type Options struct {
	Now   func() time.Time
	After func(time.Duration) <-chan time.Time
}

// NewRunner returns a Runner that starts workers through spawn.
func NewRunner(spawn Spawner, log *logx.Logger, o Options) *Runner {
	r := &Runner{spawn: spawn, log: log, now: o.Now, after: o.After, status: map[string]*Status{}}
	if r.log == nil {
		r.log = logx.Nop()
	}
	if r.now == nil {
		r.now = time.Now
	}
	if r.after == nil {
		r.after = time.After
	}
	return r
}

func (r *Runner) stat(name string) *Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.status[name]
	if !ok {
		s = &Status{Name: name}
		r.status[name] = s
	}
	return s
}

// Statuses returns the known jobs sorted by name.
func (r *Runner) Statuses() []Status {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Status, 0, len(r.status))
	for _, s := range r.status {
		out = append(out, *s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Status returns one job's snapshot.
func (r *Runner) Status(name string) (Status, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.status[name]; ok {
		return *s, true
	}
	return Status{}, false
}

// Start registers a worker for j. The first run happens immediately when
// RunAtStart is set, otherwise after one interval.
func (r *Runner) Start(j Job) {
	if j.Run == nil || j.Name == "" {
		return
	}
	r.stat(j.Name)
	r.spawn.Go(j.Name, func(ctx context.Context) {
		if j.Every <= 0 {
			r.runOnce(ctx, j)
			return
		}
		if j.RunAtStart {
			r.runOnce(ctx, j)
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-r.after(j.Every):
			}
			if ctx.Err() != nil {
				return
			}
			r.runOnce(ctx, j)
		}
	})
}

// Once runs fn a single time in a worker.
func (r *Runner) Once(name string, timeout time.Duration, fn func(ctx context.Context) error) {
	r.Start(Job{Name: name, Timeout: timeout, Run: fn})
}

// runOnce executes one run: panic-safe, bounded, recorded. Failures are
// logged unless the context was canceled (shutdown is not an error).
func (r *Runner) runOnce(ctx context.Context, j Job) {
	runCtx := ctx
	cancel := func() {}
	if j.Timeout > 0 {
		runCtx, cancel = context.WithTimeout(ctx, j.Timeout)
	}
	defer cancel()
	s := r.stat(j.Name)
	r.mu.Lock()
	s.Running = true
	r.mu.Unlock()
	err := r.safely(runCtx, j.Name, j.Run)
	r.mu.Lock()
	s.Running = false
	s.Runs++
	s.LastRun = r.now()
	if err != nil {
		s.Failures++
		s.LastErr = err.Error()
	} else {
		s.LastErr = ""
	}
	r.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		r.log.Warn(j.Name+"_failed", logx.Err(err))
	}
}

// safely runs fn, converting a panic into an error and a background_task_panic
// log line.
func (r *Runner) safely(ctx context.Context, name string, fn func(context.Context) error) (err error) {
	defer func() {
		if p := recover(); p != nil {
			r.log.Error("background_task_panic", logx.F("task", name), logx.F("panic", fmt.Sprint(p)), logx.F("stack", string(debug.Stack())))
			err = fmt.Errorf("panic: %v", p)
		}
	}()
	return fn(ctx)
}

// Tolerate runs an optional startup step: failures and panics are logged and
// swallowed so optional integrations never block startup.
func (r *Runner) Tolerate(ctx context.Context, name string, timeout time.Duration, fn func(ctx context.Context) error) {
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	if err := r.safely(ctx, name, fn); err != nil && ctx.Err() == nil {
		r.log.Warn(name+"_failed", logx.Err(err))
	}
}

// ---- batches ----------------------------------------------------------------

// Batches calls step repeatedly with the batch size until a call handles fewer
// items than the size. It returns the total handled. A non-positive size runs
// nothing (a disabled job).
func Batches(ctx context.Context, size int, step func(ctx context.Context, limit int) (int, error)) (int, error) {
	if size <= 0 {
		return 0, nil
	}
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		n, err := step(ctx, size)
		total += n
		if err != nil {
			return total, err
		}
		if n < size {
			return total, nil
		}
	}
}

// ---- queue ------------------------------------------------------------------

// ErrQueueFull is returned by Submit when no slot is free.
var ErrQueueFull = errors.New("job queue is full")

// Queue is a bounded in-memory queue drained by a fixed number of workers.
type Queue[T any] struct {
	name    string
	ch      chan T
	workers int
	handle  func(ctx context.Context, item T) error
	runner  *Runner

	mu       sync.Mutex
	inflight int
	done     int
	failed   int
}

// NewQueue builds a queue with the given depth (minimum 1) and worker count
// (minimum 1). handle errors are logged, never fatal.
func NewQueue[T any](r *Runner, name string, depth, workers int, handle func(ctx context.Context, item T) error) *Queue[T] {
	if depth < 1 {
		depth = 1
	}
	if workers < 1 {
		workers = 1
	}
	return &Queue[T]{name: name, ch: make(chan T, depth), workers: workers, handle: handle, runner: r}
}

// Start launches the workers.
func (q *Queue[T]) Start() {
	for i := 0; i < q.workers; i++ {
		wname := fmt.Sprintf("%s-%d", q.name, i+1)
		q.runner.spawn.Go(wname, func(ctx context.Context) {
			for {
				select {
				case <-ctx.Done():
					return
				case it := <-q.ch:
					q.run(ctx, wname, it)
				}
			}
		})
	}
}

func (q *Queue[T]) run(ctx context.Context, wname string, it T) {
	q.mu.Lock()
	q.inflight++
	q.mu.Unlock()
	err := q.runner.safely(ctx, wname, func(c context.Context) error { return q.handle(c, it) })
	q.mu.Lock()
	q.inflight--
	q.done++
	if err != nil {
		q.failed++
	}
	q.mu.Unlock()
	if err != nil && ctx.Err() == nil {
		q.runner.log.Warn(q.name+"_item_failed", logx.Err(err))
	}
}

// Submit enqueues without blocking; it returns ErrQueueFull when the queue is
// at capacity.
func (q *Queue[T]) Submit(item T) error {
	select {
	case q.ch <- item:
		return nil
	default:
		return ErrQueueFull
	}
}

// SubmitWait blocks until there is room or ctx ends.
func (q *Queue[T]) SubmitWait(ctx context.Context, item T) error {
	select {
	case q.ch <- item:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Pending reports queued items.
func (q *Queue[T]) Pending() int { return len(q.ch) }

// Stats reports (in flight, completed, failed).
func (q *Queue[T]) Stats() (inflight, done, failed int) {
	q.mu.Lock()
	defer q.mu.Unlock()
	return q.inflight, q.done, q.failed
}
