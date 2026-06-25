package memory

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// fakeEmbedder is a controllable Embedder.
type fakeEmbedder struct {
	mu      sync.Mutex
	enabled bool
	vec     []float32
	sig     string
	err     error
	delay   time.Duration
	calls   []string
}

func (f *fakeEmbedder) Enabled() bool { return f.enabled }

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, text)
	vec, sig, err, delay := f.vec, f.sig, f.err, f.delay
	f.mu.Unlock()
	if delay > 0 {
		select {
		case <-time.After(delay):
		case <-ctx.Done():
			return nil, "", ctx.Err()
		}
	}
	return vec, sig, err
}

func (f *fakeEmbedder) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// newEnv mounts the memory area (and the audit tables) and returns the service
// the mount created.
func newEnv(t *testing.T, opts Options) (*testkit.Env, *Service) {
	t.Helper()
	var svc *Service
	env := testkit.New(t, []features.Feature{
		{Name: "audit", Migrations: audit.Migrations, Mount: audit.Mount},
		{Name: "memory", Migrations: Migrations, Mount: func(d deps.Deps) error {
			s, err := MountWith(d, opts)
			svc = s
			return err
		}},
	})
	return env, svc
}

func put(env *testkit.Env, uid uint, key, value, scope string) int {
	w := env.Do("PUT", "/api/v1/memories/profile", env.Bearer(testkit.User(uid)),
		map[string]string{"memoryKey": key, "value": value, "scope": scope})
	return w.Code
}

func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

func fill(t *testing.T, env *testkit.Env, uid uint, n int) {
	t.Helper()
	rows := make([]Memory, n)
	for i := range rows {
		rows[i] = Memory{UserID: uid, MemoryKey: fmt.Sprintf("k%d", i), Value: "v", Scope: ScopeCustom, UpdatedBy: "seed"}
	}
	if err := env.DB.CreateInBatches(&rows, 100).Error; err != nil {
		t.Fatal(err)
	}
}
