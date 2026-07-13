package skill_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func newEnv(t *testing.T) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "audit", Migrations: audit.Migrations, Mount: audit.Mount},
		{Name: "skill", Migrations: skill.Migrations, Mount: skill.Mount},
	})
}

// wire mirrors the full JSON shape of a skill.
type wire struct {
	ID              uint   `json:"id"`
	Scope           string `json:"scope"`
	Title           string `json:"title"`
	Trigger         string `json:"trigger"`
	Description     string `json:"description"`
	Markdown        string `json:"markdown"`
	Enabled         bool   `json:"enabled"`
	SortOrder       int    `json:"sortOrder"`
	CreatedByUserID uint   `json:"createdByUserID"`
	UpdatedByUserID uint   `json:"updatedByUserID"`
	CreatedAt       string `json:"createdAt"`
	UpdatedAt       string `json:"updatedAt"`
}

type paged struct {
	Total   int64             `json:"total"`
	Results []json.RawMessage `json:"results"`
}

type suite struct {
	t   *testing.T
	env *testkit.Env
}

func newSuite(t *testing.T) *suite { return &suite{t: t, env: newEnv(t)} }

func (s *suite) as(id uint) map[string]string    { return s.env.Bearer(testkit.User(id)) }
func (s *suite) admin(id uint) map[string]string { return s.env.Bearer(testkit.Admin(id)) }

func (s *suite) do(method, path string, h map[string]string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.env.Do(method, "/api/v1"+path, h, body)
}

// one decodes {skill: ...} from a successful response.
func (s *suite) one(w *httptest.ResponseRecorder) wire {
	s.t.Helper()
	testkit.Expect(s.t, w, 200, "")
	var out struct {
		Skill wire `json:"skill"`
	}
	testkit.Decode(s.t, w, &out)
	return out.Skill
}

func (s *suite) page(w *httptest.ResponseRecorder) (int64, []wire) {
	s.t.Helper()
	testkit.Expect(s.t, w, 200, "")
	var p paged
	testkit.Decode(s.t, w, &p)
	if p.Results == nil {
		s.t.Fatalf("results must be an array: %s", w.Body)
	}
	out := make([]wire, len(p.Results))
	for i, r := range p.Results {
		if err := json.Unmarshal(r, &out[i]); err != nil {
			s.t.Fatal(err)
		}
	}
	return p.Total, out
}

// mine creates a personal skill through the API.
func (s *suite) mine(user uint, title, trigger string) wire {
	s.t.Helper()
	return s.one(s.do("POST", "/skills/mine", s.as(user), map[string]any{
		"title": title, "trigger": trigger, "markdown": "body of " + title,
	}))
}

// builtin creates a built-in skill through the admin API.
func (s *suite) builtin(title, trigger string) wire {
	s.t.Helper()
	return s.one(s.do("POST", "/admin/skills", s.admin(1), map[string]any{
		"title": title, "trigger": trigger, "markdown": "body of " + title,
	}))
}

// setEnabled flips the flag through the API (the only way for personal skills).
func (s *suite) setEnabled(user uint, id uint, on bool) {
	s.t.Helper()
	s.one(s.do("PATCH", fmt.Sprintf("/skills/mine/%d", id), s.as(user), map[string]any{"enabled": on}))
}

func (s *suite) setBuiltinEnabled(id uint, on bool) {
	s.t.Helper()
	s.one(s.do("PATCH", fmt.Sprintf("/admin/skills/%d", id), s.admin(1), map[string]any{"enabled": on}))
}

func ids(list []wire) []uint {
	out := make([]uint, len(list))
	for i, w := range list {
		out[i] = w.ID
	}
	return out
}

func eqIDs(a, b []uint) bool {
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

func repeat(s string, n int) string { return strings.Repeat(s, n) }
