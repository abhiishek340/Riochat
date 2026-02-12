package lifecycle

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
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

func TestDrainIdempotentAndNilSafe(t *testing.T) {
	d := NewDrain()
	if d.Draining() {
		t.Fatal("must start unset")
	}
	d.Begin()
	d.Begin()
	if !d.Draining() {
		t.Fatal("must be draining")
	}
	select {
	case <-d.Done():
	default:
		t.Fatal("Done must be closed")
	}
	var nilDrain *Drain
	nilDrain.Begin()
	if nilDrain.Draining() || nilDrain.Done() != nil {
		t.Fatal("nil drain never drains")
	}
}

func TestWorkersStopOnCancelAndPanicsAreContained(t *testing.T) {
	var buf bytes.Buffer
	m := NewManager(logx.New(&buf, logx.LevelDebug))
	var stopped atomic.Bool
	m.Go("loop", func(ctx context.Context) { <-ctx.Done(); stopped.Store(true) })
	m.Go("panicker", func(context.Context) { panic("kaboom") })
	if !m.StopWorkers(2 * time.Second) {
		t.Fatal("workers did not stop in time")
	}
	if !stopped.Load() {
		t.Fatal("worker did not observe cancellation")
	}
	if !strings.Contains(buf.String(), "background_task_panic") || !strings.Contains(buf.String(), "kaboom") {
		t.Fatalf("panic not logged: %s", buf.String())
	}
}

func TestStopWorkersTimesOut(t *testing.T) {
	m := NewManager(nil)
	release := make(chan struct{})
	m.Go("stuck", func(context.Context) { <-release })
	if m.StopWorkers(50 * time.Millisecond) {
		t.Fatal("expected timeout")
	}
	close(release)
}

func TestDetachKeepsValuesButSurvivesCancel(t *testing.T) {
	m := NewManager(nil)
	ctx, cancel := context.WithCancel(reqctx.Attach(context.Background(), reqctx.NewState("rid", "tid")))
	got := make(chan string, 1)
	gate := make(chan struct{})
	m.Detach(ctx, "followup", time.Second, func(c context.Context) {
		<-gate
		if c.Err() != nil {
			got <- "canceled"
			return
		}
		got <- reqctx.From(c).RequestID()
	})
	cancel()
	close(gate)
	if v := <-got; v != "rid" {
		t.Fatalf("detached task saw %q", v)
	}
	if !m.WaitDetached(time.Second) {
		t.Fatal("detached task not drained")
	}
}

func TestDetachTimeoutBoundsTask(t *testing.T) {
	m := NewManager(nil)
	var sawDeadline atomic.Bool
	m.Detach(context.Background(), "slow", 20*time.Millisecond, func(c context.Context) {
		<-c.Done()
		sawDeadline.Store(errors.Is(c.Err(), context.DeadlineExceeded))
	})
	if !m.WaitDetached(time.Second) || !sawDeadline.Load() {
		t.Fatal("detached timeout not applied")
	}
	var buf bytes.Buffer
	m2 := NewManager(logx.New(&buf, logx.LevelDebug))
	m2.Detach(context.Background(), "boom", 0, func(context.Context) { panic("x") })
	m2.WaitDetached(time.Second)
	if !strings.Contains(buf.String(), "background_task_panic") {
		t.Fatal("detached panic must be logged")
	}
}

func TestHooksOrder(t *testing.T) {
	m := NewManager(nil)
	var mu sync.Mutex
	var order []string
	add := func(s string) { mu.Lock(); order = append(order, s); mu.Unlock() }
	m.OnStart("one", func(context.Context) error { add("start1"); return nil })
	m.OnStart("two", func(context.Context) error { add("start2"); return nil })
	m.OnStop("a", func(context.Context) error { add("stopA"); return nil })
	m.OnStop("b", func(context.Context) error { add("stopB"); return errors.New("ignored") })
	if err := m.RunStartHooks(context.Background()); err != nil {
		t.Fatal(err)
	}
	m.RunStopHooks(context.Background())
	if got := strings.Join(order, ","); got != "start1,start2,stopB,stopA" {
		t.Fatalf("order: %s", got)
	}

	m2 := NewManager(nil)
	boom := errors.New("boom")
	m2.OnStart("bad", func(context.Context) error { return boom })
	m2.OnStart("never", func(context.Context) error { t.Fatal("must not run after a failure"); return nil })
	if err := m2.RunStartHooks(context.Background()); !errors.Is(err, boom) || !strings.Contains(err.Error(), `"bad"`) {
		t.Fatalf("start hook error: %v", err)
	}
}
