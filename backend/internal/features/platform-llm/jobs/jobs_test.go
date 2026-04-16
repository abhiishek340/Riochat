package jobs

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// testSpawner runs workers like the lifecycle manager: one goroutine each,
// stopped by canceling a shared context.
type testSpawner struct {
	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup
}

func newSpawner() *testSpawner {
	ctx, cancel := context.WithCancel(context.Background())
	return &testSpawner{ctx: ctx, cancel: cancel}
}

func (s *testSpawner) Go(name string, fn func(ctx context.Context)) {
	s.wg.Add(1)
	go func() { defer s.wg.Done(); fn(s.ctx) }()
}

func (s *testSpawner) stop() { s.cancel(); s.wg.Wait() }

func newLog() (*logx.Logger, *syncBuf) {
	b := &syncBuf{}
	return logx.New(b, logx.LevelDebug), b
}

type syncBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuf) Write(p []byte) (int, error) { s.mu.Lock(); defer s.mu.Unlock(); return s.b.Write(p) }
func (s *syncBuf) String() string              { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// ticker is a hand-driven replacement for time.After.
type ticker struct {
	mu   sync.Mutex
	reqs []time.Duration
	ch   chan time.Time
}

func newTicker() *ticker { return &ticker{ch: make(chan time.Time)} }

func (t *ticker) after(d time.Duration) <-chan time.Time {
	t.mu.Lock()
	t.reqs = append(t.reqs, d)
	t.mu.Unlock()
	return t.ch
}

func (t *ticker) waits() []time.Duration {
	t.mu.Lock()
	defer t.mu.Unlock()
	return append([]time.Duration(nil), t.reqs...)
}

func eventually(t *testing.T, cond func() bool, what string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func TestPeriodicJobRunsAtStartThenEveryInterval(t *testing.T) {
	sp := newSpawner()
	log, _ := newLog()
	tk := newTicker()
	r := NewRunner(sp, log, Options{After: tk.after})
	var runs atomic.Int32
	r.Start(Job{Name: "cleanup", Every: time.Hour, RunAtStart: true, Run: func(ctx context.Context) error { runs.Add(1); return nil }})
	eventually(t, func() bool { return runs.Load() == 1 }, "the immediate first run")
	tk.ch <- time.Now()
	eventually(t, func() bool { return runs.Load() == 2 }, "the second run")
	sp.stop()
	if w := tk.waits(); len(w) == 0 || w[0] != time.Hour {
		t.Fatalf("%v", w)
	}
	st, ok := r.Status("cleanup")
	if !ok || st.Runs != 2 || st.Failures != 0 || st.Running {
		t.Fatalf("%+v", st)
	}
}

func TestPeriodicJobWaitsFirstWhenNotRunAtStart(t *testing.T) {
	sp := newSpawner()
	tk := newTicker()
	r := NewRunner(sp, nil, Options{After: tk.after})
	var runs atomic.Int32
	r.Start(Job{Name: "sweep", Every: time.Minute, Run: func(ctx context.Context) error { runs.Add(1); return nil }})
	time.Sleep(30 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatal("must wait one interval first")
	}
	tk.ch <- time.Now()
	eventually(t, func() bool { return runs.Load() == 1 }, "run")
	sp.stop()
}

func TestOnceAndStopOnCancel(t *testing.T) {
	sp := newSpawner()
	r := NewRunner(sp, nil, Options{})
	var ran atomic.Int32
	r.Once("one-shot", 0, func(ctx context.Context) error { ran.Add(1); return nil })
	eventually(t, func() bool { return ran.Load() == 1 }, "one-shot")
	r.Start(Job{Name: "forever", Every: time.Hour, Run: func(ctx context.Context) error { return nil }})
	done := make(chan struct{})
	go func() { sp.stop(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("workers must stop when the context is canceled")
	}
	if ran.Load() != 1 {
		t.Fatal("one-shot runs once")
	}
}

func TestFailuresAndPanicsDoNotKillTheLoop(t *testing.T) {
	sp := newSpawner()
	log, buf := newLog()
	tk := newTicker()
	r := NewRunner(sp, log, Options{After: tk.after})
	var n atomic.Int32
	r.Start(Job{Name: "flaky", Every: time.Second, RunAtStart: true, Run: func(ctx context.Context) error {
		switch n.Add(1) {
		case 1:
			return errors.New("boom")
		case 2:
			panic("kaput")
		}
		return nil
	}})
	eventually(t, func() bool { return n.Load() == 1 }, "run 1")
	tk.ch <- time.Now()
	eventually(t, func() bool { return n.Load() == 2 }, "run 2")
	tk.ch <- time.Now()
	eventually(t, func() bool { return n.Load() == 3 }, "run 3 after a failure and a panic")
	sp.stop()
	st, _ := r.Status("flaky")
	if st.Runs != 3 || st.Failures != 2 {
		t.Fatalf("%+v", st)
	}
	out := buf.String()
	if !strings.Contains(out, "flaky_failed") || !strings.Contains(out, "background_task_panic") || !strings.Contains(out, "kaput") {
		t.Fatalf("%s", out)
	}
}

func TestShutdownErrorsAreNotLogged(t *testing.T) {
	sp := newSpawner()
	log, buf := newLog()
	r := NewRunner(sp, log, Options{})
	started := make(chan struct{})
	r.Once("slow", 0, func(ctx context.Context) error {
		close(started)
		<-ctx.Done()
		return ctx.Err()
	})
	<-started
	sp.stop()
	if strings.Contains(buf.String(), "slow_failed") {
		t.Fatalf("a canceled context is not a failure: %s", buf.String())
	}
}

func TestRunTimeout(t *testing.T) {
	sp := newSpawner()
	r := NewRunner(sp, nil, Options{})
	var sawDeadline atomic.Bool
	r.Once("bounded", 30*time.Millisecond, func(ctx context.Context) error {
		<-ctx.Done()
		sawDeadline.Store(errors.Is(ctx.Err(), context.DeadlineExceeded))
		return ctx.Err()
	})
	eventually(t, sawDeadline.Load, "per-run timeout")
	sp.stop()
}

func TestTolerate(t *testing.T) {
	log, buf := newLog()
	r := NewRunner(newSpawner(), log, Options{})
	r.Tolerate(context.Background(), "geoip_refresh", time.Second, func(ctx context.Context) error { return errors.New("offline") })
	r.Tolerate(context.Background(), "catalog", time.Second, func(ctx context.Context) error { panic("x") })
	if !strings.Contains(buf.String(), "geoip_refresh_failed") || !strings.Contains(buf.String(), "background_task_panic") {
		t.Fatal(buf.String())
	}
}

func TestBatches(t *testing.T) {
	var sizes []int
	remaining := 2500
	total, err := Batches(context.Background(), 1000, func(ctx context.Context, limit int) (int, error) {
		sizes = append(sizes, limit)
		n := limit
		if remaining < n {
			n = remaining
		}
		remaining -= n
		return n, nil
	})
	if err != nil || total != 2500 || len(sizes) != 3 {
		t.Fatalf("batches continue until one is smaller than the size: %d %v %v", total, sizes, err)
	}
	// A batch exactly the size forces one more call.
	calls := 0
	Batches(context.Background(), 10, func(ctx context.Context, limit int) (int, error) {
		calls++
		if calls == 1 {
			return 10, nil
		}
		return 0, nil
	})
	if calls != 2 {
		t.Fatal(calls)
	}
	if n, err := Batches(context.Background(), 0, func(ctx context.Context, limit int) (int, error) { t.Fatal("disabled"); return 0, nil }); n != 0 || err != nil {
		t.Fatal("non-positive size disables the job")
	}
	boom := errors.New("db down")
	n, err := Batches(context.Background(), 5, func(ctx context.Context, limit int) (int, error) { return 3, boom })
	if !errors.Is(err, boom) || n != 3 {
		t.Fatalf("errors abort: %d %v", n, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := Batches(ctx, 5, func(context.Context, int) (int, error) { return 5, nil }); !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}

func TestQueueBoundedAndConcurrent(t *testing.T) {
	sp := newSpawner()
	log, buf := newLog()
	r := NewRunner(sp, log, Options{})
	release := make(chan struct{})
	var active, peak atomic.Int32
	var handled atomic.Int32
	q := NewQueue(r, "moderation", 2, 3, func(ctx context.Context, n int) error {
		a := active.Add(1)
		for {
			p := peak.Load()
			if a <= p || peak.CompareAndSwap(p, a) {
				break
			}
		}
		<-release
		active.Add(-1)
		handled.Add(1)
		if n == 99 {
			return errors.New("bad item")
		}
		if n == 98 {
			panic("item panic")
		}
		return nil
	})
	q.Start()
	for i := 0; i < 3; i++ {
		if err := q.SubmitWait(context.Background(), i); err != nil {
			t.Fatal(err)
		}
	}
	eventually(t, func() bool { return active.Load() == 3 }, "three workers busy")
	// Two more fit in the buffer, the next is rejected.
	if err := q.Submit(10); err != nil || q.Submit(11) != nil {
		t.Fatal("buffer should hold two items")
	}
	if err := q.Submit(12); !errors.Is(err, ErrQueueFull) {
		t.Fatalf("%v", err)
	}
	if q.Pending() != 2 {
		t.Fatal(q.Pending())
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	if err := q.SubmitWait(ctx, 13); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("SubmitWait honors ctx: %v", err)
	}
	cancel()
	close(release)
	eventually(t, func() bool { return handled.Load() == 5 }, "all items handled")
	if peak.Load() != 3 {
		t.Fatalf("concurrency is capped by the worker count: %d", peak.Load())
	}
	q.Submit(99)
	q.Submit(98)
	eventually(t, func() bool { _, d, _ := q.Stats(); return d == 7 }, "failing items")
	if _, _, failed := q.Stats(); failed != 2 {
		t.Fatalf("failed=%d", failed)
	}
	if !strings.Contains(buf.String(), "moderation_item_failed") || !strings.Contains(buf.String(), "item panic") {
		t.Fatal(buf.String())
	}
	sp.stop()
	// Minimums.
	q2 := NewQueue[int](r, "x", 0, 0, func(context.Context, int) error { return nil })
	if cap(q2.ch) != 1 || q2.workers != 1 {
		t.Fatal("queue minimums")
	}
}

func TestRetryPolicyDelays(t *testing.T) {
	p := RetryPolicy{MaxAttempts: 5, Base: time.Second, Multiplier: 2, Max: 5 * time.Second}
	for n, want := range map[int]time.Duration{1: time.Second, 2: 2 * time.Second, 3: 4 * time.Second, 4: 5 * time.Second, 9: 5 * time.Second} {
		if got := p.Delay(n); got != want {
			t.Errorf("Delay(%d)=%v want %v", n, got, want)
		}
	}
	if (RetryPolicy{Base: time.Second, Multiplier: 1}).Delay(2) != 2*time.Second {
		t.Fatal("multiplier ≤ 1 becomes 2")
	}
	j := RetryPolicy{Base: time.Second, Jitter: 0.5}
	for i := 0; i < 50; i++ {
		if d := j.Delay(1); d > time.Second || d < 500*time.Millisecond {
			t.Fatalf("jitter out of range: %v", d)
		}
	}
}

func TestRetry(t *testing.T) {
	var slept []time.Duration
	opts := RetryOptions{Sleep: func(ctx context.Context, d time.Duration) error { slept = append(slept, d); return nil }}
	p := RetryPolicy{MaxAttempts: 3, Base: time.Second}
	attempts := 0
	err := Retry(context.Background(), p, opts, func(a int) error {
		attempts++
		if a < 3 {
			return errors.New("transient")
		}
		return nil
	})
	if err != nil || attempts != 3 || len(slept) != 2 || slept[1] != 2*time.Second {
		t.Fatalf("%v %d %v", err, attempts, slept)
	}
	// Exhaustion wraps the last error.
	last := errors.New("still down")
	err = Retry(context.Background(), p, opts, func(int) error { return last })
	var gu *ErrGaveUp
	if !errors.As(err, &gu) || gu.Attempts != 3 || !errors.Is(err, last) {
		t.Fatalf("%v", err)
	}
	// Non-retryable stops at once.
	n := 0
	fatal := errors.New("fatal")
	err = Retry(context.Background(), p, RetryOptions{Retryable: func(e error) bool { return e != fatal }, Sleep: opts.Sleep}, func(int) error { n++; return fatal })
	if n != 1 || err != fatal {
		t.Fatalf("%d %v", n, err)
	}
	// Retry-After style hints raise the delay.
	slept = nil
	Retry(context.Background(), RetryPolicy{MaxAttempts: 2, Base: time.Second}, RetryOptions{Sleep: opts.Sleep, After: func(error) time.Duration { return 9 * time.Second }}, func(int) error { return errors.New("x") })
	if len(slept) != 1 || slept[0] != 9*time.Second {
		t.Fatalf("%v", slept)
	}
	// Canceled contexts end the loop, also during sleeps.
	ctx, cancel := context.WithCancel(context.Background())
	err = Retry(ctx, p, RetryOptions{Sleep: func(ctx context.Context, d time.Duration) error { cancel(); return ctx.Err() }}, func(int) error { return errors.New("x") })
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
	// Default retryability excludes context errors; MaxAttempts < 1 means one try.
	calls := 0
	Retry(context.Background(), RetryPolicy{}, opts, func(int) error { calls++; return context.Canceled })
	if calls != 1 {
		t.Fatal(calls)
	}
}
