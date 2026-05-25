package uicomponent

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const (
	userBase  = "/api/v1/ui-components"
	mineURL   = userBase + "/mine"
	adminBase = "/api/v1/admin/ui-components"
)

// toggle is a Settings whose value a test can flip.
type toggle struct{ on atomic.Bool }

func newToggle(on bool) *toggle { t := &toggle{}; t.on.Store(on); return t }

func (t *toggle) UIComponentsEnabled(context.Context) bool { return t.on.Load() }

func newEnv(t *testing.T) *testkit.Env {
	t.Helper()
	env, _ := newEnvWithSwitch(t)
	return env
}

func newEnvWithSwitch(t *testing.T) (*testkit.Env, *toggle) {
	t.Helper()
	sw := newToggle(true)
	env := testkit.New(t, []features.Feature{
		{Name: "audit", Migrations: audit.Migrations, Mount: audit.Mount},
		{Name: "uicomponent", Migrations: Migrations, Mount: MountWith(nil, sw)},
	})
	return env, sw
}

// newAuditedEnv uses the default wiring, with audit rows persisted.
func newAuditedEnv(t *testing.T) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "audit", Migrations: audit.Migrations, Mount: audit.Mount},
		{Name: "uicomponent", Migrations: Migrations, Mount: Mount},
	})
}

type compJSON struct {
	ID              uint   `json:"id"`
	Scope           string `json:"scope"`
	Name            string `json:"name"`
	Version         int    `json:"version"`
	Description     string `json:"description"`
	PropsSummary    string `json:"propsSummary"`
	PropsSchema     string `json:"propsSchema"`
	RendererKind    string `json:"rendererKind"`
	RendererSource  string `json:"rendererSource"`
	Enabled         bool   `json:"enabled"`
	SortOrder       int    `json:"sortOrder"`
	CreatedByUserID uint   `json:"createdByUserID"`
	UpdatedByUserID uint   `json:"updatedByUserID"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type listJSON struct {
	Total   int64      `json:"total"`
	Results []compJSON `json:"results"`
}

// validBody is a minimal valid create body for the given name.
func validBody(name string) map[string]any {
	return map[string]any{
		"name":           name,
		"description":    "A test component. Use when: testing. Interaction: click",
		"propsSummary":   "{a, b?}. Convention: keep it short",
		"rendererSource": "<div>hi</div>",
	}
}

func with(m map[string]any, kv ...any) map[string]any {
	out := map[string]any{}
	for k, v := range m {
		out[k] = v
	}
	for i := 0; i+1 < len(kv); i += 2 {
		out[kv[i].(string)] = kv[i+1]
	}
	return out
}

// mk creates a component through the endpoint and returns it.
func mk(t *testing.T, env *testkit.Env, url string, who testkit.Claims, payload map[string]any) compJSON {
	t.Helper()
	w := env.Do("POST", url, env.Bearer(who), payload)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Component compJSON `json:"component"`
	}
	testkit.Decode(t, w, &out)
	return out.Component
}

func list(t *testing.T, env *testkit.Env, url string, who testkit.Claims) listJSON {
	t.Helper()
	w := env.Do("GET", url, env.Bearer(who), nil)
	testkit.Expect(t, w, 200, "")
	var out listJSON
	testkit.Decode(t, w, &out)
	return out
}

func names(l listJSON) []string {
	out := make([]string, len(l.Results))
	for i, r := range l.Results {
		out[i] = r.Name
	}
	return out
}

func sameStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func idURL(base string, id uint) string { return fmt.Sprintf("%s/%d", base, id) }

// builtinID finds the id of a built-in row by name.
func builtinID(t *testing.T, env *testkit.Env, name string) uint {
	t.Helper()
	var row UIComponent
	if err := env.DB.Where("scope = ? AND name = ?", ScopeBuiltin, name).Take(&row).Error; err != nil {
		t.Fatalf("builtin %q: %v", name, err)
	}
	return row.ID
}
