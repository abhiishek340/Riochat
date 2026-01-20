package example_test

import (
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/example"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func newEnv(t *testing.T, opts ...testkit.Option) *testkit.Env {
	return testkit.New(t, []features.Feature{{Name: "example", Migrations: example.Migrations, Mount: example.Mount}}, opts...)
}

func TestPublicAndAuthRequired(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", "/api/v1/examples/public", nil, nil), 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/examples", nil, nil), 401, "auth.invalid_token")
}

func TestCreateListGetIsolation(t *testing.T) {
	env := newEnv(t)
	alice, bob := env.Bearer(testkit.User(1)), env.Bearer(testkit.User(2))

	w := env.Do("POST", "/api/v1/examples", alice, map[string]string{"title": "first"})
	testkit.Expect(t, w, 201, "")
	var created struct {
		ID    uint
		Title string
	}
	testkit.Decode(t, w, &created)
	if created.ID == 0 || created.Title != "first" {
		t.Fatalf("created: %+v", created)
	}

	var page struct {
		Total   int64
		Results []map[string]any
	}
	w = env.Do("GET", "/api/v1/examples?page=1&page_size=10", alice, nil)
	testkit.Expect(t, w, 200, "")
	testkit.Decode(t, w, &page)
	if page.Total != 1 || len(page.Results) != 1 {
		t.Fatalf("alice page: %+v", page)
	}

	w = env.Do("GET", "/api/v1/examples", bob, nil)
	testkit.Decode(t, w, &page)
	if page.Total != 0 || page.Results == nil || len(page.Results) != 0 {
		t.Fatalf("bob must see nothing and a non-null array: %s", w.Body)
	}

	url := fmt.Sprintf("/api/v1/examples/%d", created.ID)
	testkit.Expect(t, env.Do("GET", url, alice, nil), 200, "")
	testkit.Expect(t, env.Do("GET", url, bob, nil), 404, "example.not_found")
	testkit.Expect(t, env.Do("GET", "/api/v1/examples/abc", alice, nil), 400, "request.invalid_id")
}

func TestValidationErrors(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	w := env.Do("POST", "/api/v1/examples", alice, map[string]string{"title": ""})
	testkit.Expect(t, w, 400, "request.invalid_body")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid request body: title is required" {
		t.Fatalf("message: %q", msg)
	}
	testkit.Expect(t, env.Do("POST", "/api/v1/examples", alice, ""), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", "/api/v1/examples", alice, `{"title": 5}`), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", "/api/v1/examples", alice, map[string]string{"title": strings.Repeat("x", 129)}), 400, "request.invalid_body")
}

func TestDomainLimitUsesTypedError(t *testing.T) {
	env := newEnv(t)
	alice := env.Bearer(testkit.User(1))
	for i := 0; i < 50; i++ {
		testkit.Expect(t, env.Do("POST", "/api/v1/examples", alice, map[string]string{"title": "t"}), 201, "")
	}
	testkit.Expect(t, env.Do("POST", "/api/v1/examples", alice, map[string]string{"title": "t"}), 409, "example.too_many")
}

func TestAdminDelete(t *testing.T) {
	env := newEnv(t)
	w := env.Do("POST", "/api/v1/examples", env.Bearer(testkit.User(1)), map[string]string{"title": "x"})
	var created struct{ ID uint }
	testkit.Decode(t, w, &created)
	url := fmt.Sprintf("/api/v1/examples/%d", created.ID)

	testkit.Expect(t, env.Do("DELETE", url, env.Bearer(testkit.User(1)), nil), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("DELETE", url, env.Bearer(testkit.Admin(9)), nil), 200, "")
	testkit.Expect(t, env.Do("DELETE", url, env.Bearer(testkit.Admin(9)), nil), 404, "example.not_found")
}

func TestFeatureGateFollowsCapabilities(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", "/api/v1/examples-shared", env.Bearer(testkit.User(1)), nil), 200, "")

	local := newEnv(t, testkit.WithConfig(func(c *config.Config) { c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"} }))
	testkit.Expect(t, local.Do("GET", "/api/v1/examples-shared", local.Bearer(testkit.User(1)), nil), 404, "feature.disabled")
}

func TestSeedAndStartHookRan(t *testing.T) {
	env := newEnv(t)
	var n int64
	env.DB.Model(&example.Example{}).Where("title = ?", "Welcome").Count(&n)
	if n != 1 {
		t.Fatalf("seed rows: %d", n)
	}
	if !strings.Contains(env.Logs.String(), "example_ready") {
		t.Fatalf("start hook did not run: %s", env.Logs)
	}
}
