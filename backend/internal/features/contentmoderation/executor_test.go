package contentmoderation

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fnTask struct {
	runFn   func(ctx context.Context)
	aborted chan string
}

func (f *fnTask) run(ctx context.Context) { f.runFn(ctx) }
func (f *fnTask) abort(code string) {
	if f.aborted != nil {
		f.aborted <- code
	}
}

func waitFor(t testing.TB, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func blockingTask(started *atomic.Int32, release <-chan struct{}) *fnTask {
	return &fnTask{runFn: func(ctx context.Context) {
		started.Add(1)
		select {
		case <-release:
		case <-ctx.Done():
		}
	}}
}

func TestExecutorBoundsConcurrencyAndQueue(t *testing.T) {
	e := newExecutor(2, 3, nil)
	defer e.stop()
	var started atomic.Int32
	release := make(chan struct{})
	for i := 0; i < 5; i++ {
		if code := e.submit(blockingTask(&started, release)); code != "" {
			t.Fatalf("task %d rejected: %s", i, code)
		}
	}
	waitFor(t, "two running", func() bool { return started.Load() == 2 })
	if r, w := e.load(); r != 2 || w != 3 {
		t.Fatalf("running %d waiting %d", r, w)
	}
	// The waiting line is strictly bounded even though two executors are busy.
	if code := e.submit(blockingTask(&started, release)); code != CodeQueueFull {
		t.Fatalf("overflow: %q", code)
	}
	close(release)
	waitFor(t, "all five ran", func() bool { return started.Load() == 5 })
	waitFor(t, "idle", func() bool { r, w := e.load(); return r == 0 && w == 0 })
}

func TestExecutorWithOneSlotAndOneWaitingRejectsThird(t *testing.T) {
	e := newExecutor(1, 1, nil)
	defer e.stop()
	var started atomic.Int32
	release := make(chan struct{})
	defer close(release)
	if e.submit(blockingTask(&started, release)) != "" || e.submit(blockingTask(&started, release)) != "" {
		t.Fatal("first two must be accepted")
	}
	if code := e.submit(blockingTask(&started, release)); code != CodeQueueFull {
		t.Fatalf("third: %q", code)
	}
}

func TestExecutorResizeNeverDropsOrDuplicatesTasks(t *testing.T) {
	e := newExecutor(1, 50, nil)
	defer e.stop()
	var started atomic.Int32
	release := make(chan struct{})
	const n = 20
	for i := 0; i < n; i++ {
		if code := e.submit(blockingTask(&started, release)); code != "" {
			t.Fatalf("submit %d: %s", i, code)
		}
	}
	waitFor(t, "one running", func() bool { return started.Load() == 1 })

	// Raising the concurrency starts waiting tasks at once.
	e.resize(5, 50)
	waitFor(t, "five running", func() bool { return started.Load() == 5 })

	// Lowering the queue below what is waiting drops nothing.
	e.resize(5, 1)
	if _, w := e.load(); w != n-5 {
		t.Fatalf("waiting %d after shrinking the queue", w)
	}
	if code := e.submit(blockingTask(&started, release)); code != CodeQueueFull {
		t.Fatalf("queue above its new limit must refuse: %q", code)
	}

	// Lowering the concurrency never interrupts running tasks.
	e.resize(2, 1)
	if r, _ := e.load(); r != 5 {
		t.Fatalf("running %d", r)
	}
	close(release)
	waitFor(t, "every task ran exactly once", func() bool { return started.Load() == n })
	waitFor(t, "idle", func() bool { r, w := e.load(); return r == 0 && w == 0 })
	time.Sleep(20 * time.Millisecond)
	if started.Load() != n {
		t.Fatalf("started %d, want %d", started.Load(), n)
	}
}

func TestExecutorConcurrentSubmitAndResize(t *testing.T) {
	e := newExecutor(2, 5, nil)
	defer e.stop()
	var ran, rejected atomic.Int32
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				task := &fnTask{runFn: func(context.Context) { ran.Add(1); time.Sleep(time.Millisecond) }}
				if e.submit(task) != "" {
					rejected.Add(1)
				}
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			e.resize(1+i%6, 1+i%9)
			time.Sleep(time.Millisecond)
		}
	}()
	wg.Wait()
	waitFor(t, "drain", func() bool { r, w := e.load(); return r == 0 && w == 0 })
	if int(ran.Load()+rejected.Load()) != 400 {
		t.Fatalf("ran %d + rejected %d != 400", ran.Load(), rejected.Load())
	}
}

func TestExecutorStopResolvesWaitingTasksAndCancelsRunning(t *testing.T) {
	e := newExecutor(1, 5, nil)
	var started atomic.Int32
	running := blockingTask(&started, make(chan struct{}))
	e.submit(running)
	waitFor(t, "running", func() bool { return started.Load() == 1 })
	waiting := &fnTask{runFn: func(context.Context) { t.Error("waiting task must not run") }, aborted: make(chan string, 1)}
	e.submit(waiting)

	e.stop()
	select {
	case code := <-waiting.aborted:
		if code != CodeWorkerLost {
			t.Fatalf("abort code %q", code)
		}
	default:
		t.Fatal("waiting task was not aborted")
	}
	if code := e.submit(&fnTask{runFn: func(context.Context) {}}); code != CodeWorkerLost {
		t.Fatalf("submit after stop: %q", code)
	}
	e.stop() // idempotent
}

func TestExecutorSurvivesPanickingTask(t *testing.T) {
	var panics atomic.Int32
	e := newExecutor(1, 5, func(any) { panics.Add(1) })
	defer e.stop()
	bad := &fnTask{runFn: func(context.Context) { panic("boom") }, aborted: make(chan string, 1)}
	e.submit(bad)
	select {
	case code := <-bad.aborted:
		if code != CodeServiceError {
			t.Fatalf("code %q", code)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("panicking task was not aborted")
	}
	var ok atomic.Bool
	e.submit(&fnTask{runFn: func(context.Context) { ok.Store(true) }})
	waitFor(t, "next task", ok.Load)
	if panics.Load() != 1 {
		t.Fatalf("panics %d", panics.Load())
	}
}
