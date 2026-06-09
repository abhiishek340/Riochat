package promptreset

import (
	"fmt"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const (
	userBase  = "/api/v1/prompt-presets"
	mineURL   = userBase + "/mine"
	adminBase = "/api/v1/admin/prompt-presets"
)

func newEnv(t *testing.T) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "audit", Migrations: audit.Migrations, Mount: audit.Mount},
		{Name: "promptreset", Migrations: Migrations, Mount: Mount},
	})
}

type presetJSON struct {
	ID              uint   `json:"id"`
	Scope           string `json:"scope"`
	Title           string `json:"title"`
	Trigger         string `json:"trigger"`
	Description     string `json:"description"`
	Content         string `json:"content"`
	Enabled         bool   `json:"enabled"`
	SortOrder       int    `json:"sortOrder"`
	CreatedByUserID uint   `json:"createdByUserID"`
	UpdatedByUserID uint   `json:"updatedByUserID"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type listJSON struct {
	Total   int64        `json:"total"`
	Results []presetJSON `json:"results"`
}

func body(title, trigger, content string, enabled bool) map[string]any {
	return map[string]any{"title": title, "trigger": trigger, "content": content, "enabled": enabled}
}

// mk creates a preset through the given endpoint and returns it.
func mk(t *testing.T, env *testkit.Env, url string, who testkit.Claims, payload map[string]any) presetJSON {
	t.Helper()
	w := env.Do("POST", url, env.Bearer(who), payload)
	testkit.Expect(t, w, 200, "")
	var out struct {
		PromptPreset presetJSON `json:"promptPreset"`
	}
	testkit.Decode(t, w, &out)
	// Create always yields an enabled preset; disable it with a PATCH.
	if v, ok := payload["enabled"].(bool); ok && !v {
		w = env.Do("PATCH", idURL(url, out.PromptPreset.ID), env.Bearer(who), map[string]any{"enabled": false})
		testkit.Expect(t, w, 200, "")
		var p struct {
			PromptPreset presetJSON `json:"promptPreset"`
		}
		testkit.Decode(t, w, &p)
		return p.PromptPreset
	}
	return out.PromptPreset
}

func list(t *testing.T, env *testkit.Env, url string, who testkit.Claims) listJSON {
	t.Helper()
	w := env.Do("GET", url, env.Bearer(who), nil)
	testkit.Expect(t, w, 200, "")
	var out listJSON
	testkit.Decode(t, w, &out)
	return out
}

func triggers(l listJSON) []string {
	out := make([]string, len(l.Results))
	for i, r := range l.Results {
		out[i] = r.Trigger
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

func newEnvWithoutAudit(t *testing.T) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "promptreset", Migrations: Migrations, Mount: MountWith(nil)},
	})
}

// newEnvWithoutAuditTable mounts the normal recorder but never creates the
// audit table, so every audit write fails.
func newEnvWithoutAuditTable(t *testing.T) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "promptreset", Migrations: Migrations, Mount: Mount},
	})
}
