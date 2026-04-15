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
