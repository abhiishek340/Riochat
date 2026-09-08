package contentmoderation

import (
	"context"
	"sync"
)

// checkTask is a unit of work for the executor.
type checkTask interface {
	// run performs the check. ctx is canceled when the executor stops.
	run(ctx context.Context)
	// abort resolves a task that will never run, with a failure code.
	abort(code string)
}

// executor runs tasks with at most maxConc executing at once and at most
// queueCap waiting. Both limits can change at any time: lowering them never
// interrupts running or already waiting tasks, raising maxConc immediately
// starts waiting tasks.
type executor struct {
	mu       sync.Mutex
	maxConc  int
	queueCap int
	running  int
	pending  []checkTask
	stopped  bool
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	onPanic  func(any)
}

func newExecutor(maxConc, queueCap int, onPanic func(any)) *executor {
	ctx, cancel := context.WithCancel(context.Background())
	return &executor{maxConc: maxConc, queueCap: queueCap, ctx: ctx, cancel: cancel, onPanic: onPanic}
}

// submit accepts a task. It returns "" on success, CodeQueueFull when the
// waiting line is at capacity and CodeWorkerLost when the executor has stopped.
// A rejected task is not run and not aborted; the caller reports it.
func (e *executor) submit(t checkTask) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.stopped {
		return CodeWorkerLost
	}
	if e.running < e.maxConc {
		e.startLocked(t)
		return ""
	}
	if len(e.pending) >= e.queueCap {
		return CodeQueueFull
	}
	e.pending = append(e.pending, t)
	return ""
}

func (e *executor) startLocked(t checkTask) {
	e.running++
	e.wg.Add(1)
	go e.loop(t)
}

func (e *executor) loop(t checkTask) {
	defer e.wg.Done()
	for t != nil {
		e.safeRun(t)
		e.mu.Lock()
		if e.stopped || e.running > e.maxConc || len(e.pending) == 0 {
			e.running--
			e.mu.Unlock()
			return
		}
		t = e.pending[0]
		e.pending[0] = nil
		e.pending = e.pending[1:]
		e.mu.Unlock()
	}
}

func (e *executor) safeRun(t checkTask) {
	defer func() {
		if r := recover(); r != nil {
			if e.onPanic != nil {
				e.onPanic(r)
			}
			t.abort(CodeServiceError)
		}
	}()
	t.run(e.ctx)
}

// resize applies new limits.
func (e *executor) resize(maxConc, queueCap int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if maxConc > 0 {
		e.maxConc = maxConc
	}
	if queueCap > 0 {
		e.queueCap = queueCap
	}
	for !e.stopped && e.running < e.maxConc && len(e.pending) > 0 {
		t := e.pending[0]
		e.pending[0] = nil
		e.pending = e.pending[1:]
		e.startLocked(t)
	}
}

// limits reports the current limits.
func (e *executor) limits() (maxConc, queueCap int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.maxConc, e.queueCap
}

// load reports how many tasks run and wait.
func (e *executor) load() (running, waiting int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.running, len(e.pending)
}

// stop rejects new work, resolves waiting tasks as lost, cancels running ones
// and waits for them to return.
func (e *executor) stop() {
	e.mu.Lock()
	if e.stopped {
		e.mu.Unlock()
		e.wg.Wait()
		return
	}
	e.stopped = true
	waiting := e.pending
	e.pending = nil
	e.mu.Unlock()
	e.cancel()
	for _, t := range waiting {
		t.abort(CodeWorkerLost)
	}
	e.wg.Wait()
}
