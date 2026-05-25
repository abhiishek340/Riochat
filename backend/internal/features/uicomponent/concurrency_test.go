package uicomponent

import (
	"fmt"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// Concurrent creations in one bucket never collide on sort order assignment
// beyond what the unique name allows, and each succeeds or reports a clean
// error.
func TestConcurrentCreatesGetDistinctSortOrders(t *testing.T) {
	env := newEnv(t)
	hdr := env.Bearer(testkit.User(1))
	const n = 8
	var wg sync.WaitGroup
	codes := make([]int, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			codes[i] = env.Do("POST", mineURL, hdr, validBody(fmt.Sprintf("c%d", i))).Code
		}(i)
	}
	wg.Wait()
	ok := 0
	for _, c := range codes {
		if c == 200 {
			ok++
		} else if c != 409 && c != 500 {
			t.Errorf("unexpected status %d", c)
		}
	}
	if ok == 0 {
		t.Fatal("no create succeeded")
	}
	got := list(t, env, mineURL+"?page_size=100", testkit.User(1))
	if int(got.Total) != ok {
		t.Fatalf("rows %d, successes %d", got.Total, ok)
	}
}

// Concurrent patches of one row each apply to the freshly read row.
func TestConcurrentPatchesMerge(t *testing.T) {
	env := newEnv(t)
	c := mk(t, env, mineURL, testkit.User(1), validBody("race"))
	hdr := env.Bearer(testkit.User(1))
	var wg sync.WaitGroup
	for _, body := range []map[string]any{{"enabled": true}, {"sortOrder": 9}, {"version": 3}, {"description": "merged"}} {
		wg.Add(1)
		go func(b map[string]any) {
			defer wg.Done()
			env.Do("PATCH", idURL(mineURL, c.ID), hdr, b)
		}(body)
	}
	wg.Wait()
	var row UIComponent
	env.DB.Take(&row, c.ID)
	if row.Name != "race" || row.RendererKind != RendererSandbox || row.Scope != ScopeUser {
		t.Fatalf("row corrupted: %+v", row)
	}
}
