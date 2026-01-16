package cache

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
)

// backend wires one implementation together with a way to move its time.
type backend struct {
	name    string
	cache   Cache
	advance func(time.Duration)
}

func backends(t *testing.T) []backend {
	t.Helper()
	fake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	mem := NewMemory(fake)

	mr := miniredis.RunT(t)
	rfake := clock.NewFake(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	rc := NewRedis(redis.NewClient(&redis.Options{Addr: mr.Addr()}), rfake)
	t.Cleanup(func() { _ = rc.Close() })

	return []backend{
		{"memory", mem, fake.Advance},
		{"redis", rc, func(d time.Duration) { rfake.Advance(d); mr.FastForward(d) }},
	}
}

func eachBackend(t *testing.T, fn func(t *testing.T, b backend)) {
	for _, b := range backends(t) {
		b := b
		t.Run(b.name, func(t *testing.T) { fn(t, b) })
	}
}

func TestNameAndPing(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		if b.cache.Name() != b.name {
			t.Fatalf("name %q", b.cache.Name())
		}
		if err := b.cache.Ping(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
}

func TestKVBasicsAndExpiry(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		ctx := context.Background()
		if _, found, err := b.cache.Get(ctx, "k"); err != nil || found {
			t.Fatalf("miss: %v %v", found, err)
		}
		if err := b.cache.Set(ctx, "k", []byte("v"), 10*time.Second); err != nil {
			t.Fatal(err)
		}
		if v, found, _ := b.cache.Get(ctx, "k"); !found || string(v) != "v" {
			t.Fatalf("get: %q %v", v, found)
		}
		b.advance(11 * time.Second)
		if _, found, _ := b.cache.Get(ctx, "k"); found {
			t.Fatal("expired key still present")
		}
		// non-positive TTL falls back to one minute
		_ = b.cache.Set(ctx, "d", []byte("x"), 0)
		b.advance(59 * time.Second)
		if _, found, _ := b.cache.Get(ctx, "d"); !found {
			t.Fatal("default ttl should be one minute")
		}
		b.advance(2 * time.Second)
		if _, found, _ := b.cache.Get(ctx, "d"); found {
			t.Fatal("default ttl exceeded")
		}
		_ = b.cache.Set(ctx, "a", []byte("1"), time.Minute)
		_ = b.cache.Set(ctx, "b", []byte("2"), time.Minute)
		if err := b.cache.Delete(ctx, "a", "b", "missing"); err != nil {
			t.Fatal(err)
		}
		if _, found, _ := b.cache.Get(ctx, "a"); found {
			t.Fatal("delete failed")
		}
		if err := b.cache.Delete(ctx); err != nil {
			t.Fatal(err)
		}
	})
}

func TestSetNXAndIncr(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		ctx := context.Background()
		ok, err := b.cache.SetNX(ctx, "lock", []byte("a"), 5*time.Second)
		if err != nil || !ok {
			t.Fatalf("first SetNX: %v %v", ok, err)
		}
		if ok, _ := b.cache.SetNX(ctx, "lock", []byte("b"), 5*time.Second); ok {
			t.Fatal("second SetNX must fail")
		}
		b.advance(6 * time.Second)
		if ok, _ := b.cache.SetNX(ctx, "lock", []byte("c"), 5*time.Second); !ok {
			t.Fatal("SetNX after expiry must succeed")
		}
		for i := int64(1); i <= 3; i++ {
			n, err := b.cache.Incr(ctx, "ctr", 10*time.Second)
			if err != nil || n != i {
				t.Fatalf("incr %d: %d %v", i, n, err)
			}
		}
		b.advance(11 * time.Second)
		if n, _ := b.cache.Incr(ctx, "ctr", 10*time.Second); n != 1 {
			t.Fatalf("counter must restart after expiry, got %d", n)
		}
	})
}

func TestGetDelIsOneShot(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		ctx := context.Background()
		_ = b.cache.Set(ctx, "txn", []byte(`{"a":1}`), 10*time.Second)
		if v, found, err := b.cache.GetDel(ctx, "txn"); err != nil || !found || string(v) != `{"a":1}` {
			t.Fatalf("first consume: %q %v %v", v, found, err)
		}
		if _, found, _ := b.cache.GetDel(ctx, "txn"); found {
			t.Fatal("second consume must miss")
		}
		_ = b.cache.Set(ctx, "old", []byte("x"), time.Second)
		b.advance(2 * time.Second)
		if _, found, _ := b.cache.GetDel(ctx, "old"); found {
			t.Fatal("expired record must miss")
		}
	})
}

func TestConsumeIsAtomicUnderContention(t *testing.T) {
	eachBackend(t, func(t *testing.T, b backend) {
		ctx := context.Background()
		_ = b.cache.Set(ctx, "once", []byte("x"), time.Minute)
		var wg sync.WaitGroup
		var mu sync.Mutex
		wins := 0
		for i := 0; i < 20; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, found, _ := b.cache.GetDel(ctx, "once"); found {
					mu.Lock()
					wins++
					mu.Unlock()
				}
			}()
		}
		wg.Wait()
		if wins != 1 {
			t.Fatalf("exactly one consumer must win, got %d", wins)
		}
	})
}
