package memory

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// stuckEmbedder ignores its context and blocks for a long time.
type stuckEmbedder struct{ release chan struct{} }

func (s *stuckEmbedder) Enabled() bool { return true }
func (s *stuckEmbedder) Embed(ctx context.Context, text string) ([]float32, string, error) {
	<-s.release
	return []float32{1, 0, 0}, testSig, nil
}

func TestSemanticDeadlineHoldsForEmbedderIgnoringContext(t *testing.T) {
	env, seed := newEnv(t, Options{})
	emb := &stuckEmbedder{release: make(chan struct{})}
	defer close(emb.release)
	svc := withEmbedder(env, emb)
	ctx := t.Context()
	for i := 0; i < 8; i++ {
		seed.Upsert(ctx, 1, fmt.Sprintf("k%d", i), "filler", ScopeProfile, "user")
	}
	start := time.Now()
	pc, err := svc.BuildPromptContext(ctx, 1, "hello world", nil)
	if err != nil || len(pc.Injected) != 5 {
		t.Fatalf("%v %v", err, keys(pc.Injected))
	}
	if time.Since(start) > time.Second {
		t.Fatalf("hard deadline not enforced: %v", time.Since(start))
	}
}

func TestUpsertDoesNotWaitForEmbeddingWithoutDetacher(t *testing.T) {
	env, _ := newEnv(t, Options{})
	emb := &stuckEmbedder{release: make(chan struct{})}
	svc := NewService(env.DB, nil, Options{Embedder: emb})
	done := make(chan struct{})
	go func() {
		svc.Upsert(context.Background(), 1, "lang", "Chinese", ScopePreference, "user")
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("upsert blocked on the embedding provider")
	}
	close(emb.release)
	eventually(t, "vector", func() bool {
		got, _ := svc.Search(context.Background(), 1, []float32{1, 0, 0}, testSig, 1, 0.5)
		return len(got) == 1
	})
}

func TestStorageFailuresAre500WithoutLeakingCause(t *testing.T) {
	env, _ := newEnv(t, Options{})
	if err := env.DB.Migrator().DropTable(&Memory{}); err != nil {
		t.Fatal(err)
	}
	auth := env.Bearer(testkit.User(1))
	cases := []struct {
		method, path string
		body         any
	}{
		{"GET", "/api/v1/memories/profile", nil},
		{"PUT", "/api/v1/memories/profile", map[string]string{"memoryKey": "a", "value": "b", "scope": "custom"}},
		{"DELETE", "/api/v1/memories/profile/a", nil},
	}
	for _, c := range cases {
		w := env.Do(c.method, c.path, auth, c.body)
		if w.Code != 500 {
			t.Fatalf("%s %s: %d", c.method, c.path, w.Code)
		}
		body := strings.ToLower(w.Body.String())
		for _, leak := range []string{"user_memories", "sqlite", "no such table", "sql"} {
			if strings.Contains(body, leak) {
				t.Fatalf("%s leaks %q: %s", c.method, leak, body)
			}
		}
	}
}

func TestPutAfterDeleteGetsFreshListing(t *testing.T) {
	env, _ := newEnv(t, Options{})
	put(env, 1, "lang", "a", "profile")
	env.Do("DELETE", "/api/v1/memories/profile/lang", env.Bearer(testkit.User(1)), nil)
	if len(list(t, env, 1)) != 0 {
		t.Fatal("still listed")
	}
	put(env, 1, "lang", "b", "profile")
	got := list(t, env, 1)
	if len(got) != 1 || got[0].Value != "b" {
		t.Fatalf("%v", got)
	}
}
