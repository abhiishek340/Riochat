package announcement_test

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/announcement"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const (
	userListPath  = "/api/v1/announcements"
	adminPath     = "/api/v1/admin/announcements"
	feature       = "announcements"
	baseClockText = "2026-03-01T09:00:00Z"
)

// baseTime is the fake clock's initial instant in testkit.
var baseTime = time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)

func newEnv(t *testing.T, opts ...testkit.Option) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{{Name: "announcement", Migrations: announcement.Migrations, Mount: announcement.Mount}}, opts...)
}

func capabilityOff() testkit.Option {
	return testkit.WithConfig(func(c *config.Config) { c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"} })
}

// item is the wire shape of an announcement. Timestamps stay strings because
// the tests echo them back verbatim, as a client does.
type item struct {
	ID              uint    `json:"id"`
	Title           string  `json:"title"`
	ContentMarkdown string  `json:"contentMarkdown"`
	Status          string  `json:"status"`
	Type            string  `json:"type"`
	Pinned          bool    `json:"pinned"`
	Priority        int     `json:"priority"`
	StartsAt        *string `json:"startsAt"`
	ExpiresAt       *string `json:"expiresAt"`
	CreatedByUserID uint    `json:"createdByUserID"`
	CreatedAt       string  `json:"createdAt"`
	UpdatedAt       string  `json:"updatedAt"`
	ClosedAt        *string `json:"closedAt"`
}

type wrapped struct {
	Announcement item `json:"announcement"`
}

type suite struct {
	t   *testing.T
	env *testkit.Env
}

// adm returns fresh administrator credentials; tokens are short lived and the
// tests move the clock.
func (s *suite) adm() map[string]string { return s.env.Bearer(testkit.Admin(12)) }

func newSuite(t *testing.T, opts ...testkit.Option) *suite {
	env := newEnv(t, opts...)
	return &suite{t: t, env: env}
}

func (s *suite) user(id uint) map[string]string { return s.env.Bearer(testkit.User(id)) }

// create posts an announcement as the administrator and returns it.
func (s *suite) create(body map[string]any) item {
	s.t.Helper()
	w := s.env.Do("POST", adminPath, s.adm(), body)
	testkit.Expect(s.t, w, 200, "")
	var out wrapped
	testkit.Decode(s.t, w, &out)
	return out.Announcement
}

func (s *suite) patch(id uint, body any) *httptest.ResponseRecorder {
	return s.env.Do("PATCH", fmt.Sprintf("%s/%d", adminPath, id), s.adm(), body)
}

func (s *suite) patchOK(id uint, body any) item {
	s.t.Helper()
	w := s.patch(id, body)
	testkit.Expect(s.t, w, 200, "")
	var out wrapped
	testkit.Decode(s.t, w, &out)
	return out.Announcement
}

func (s *suite) list(user uint, query string) []item {
	s.t.Helper()
	w := s.env.Do("GET", userListPath+query, s.user(user), nil)
	testkit.Expect(s.t, w, 200, "")
	var out []item
	testkit.Decode(s.t, w, &out)
	return out
}

func ids(items []item) []uint {
	out := make([]uint, len(items))
	for i, it := range items {
		out[i] = it.ID
	}
	return out
}

func sameIDs(t *testing.T, got []item, want ...uint) {
	t.Helper()
	g := ids(got)
	if len(g) != len(want) {
		t.Fatalf("ids = %v, want %v", g, want)
	}
	for i := range g {
		if g[i] != want[i] {
			t.Fatalf("ids = %v, want %v", g, want)
		}
	}
}

func (s *suite) act(user uint, action string, id uint, updatedAt any) *httptest.ResponseRecorder {
	var body any
	if updatedAt != nil {
		body = map[string]any{"updatedAt": updatedAt}
	}
	return s.env.Do("POST", fmt.Sprintf("%s/%d/%s", userListPath, id, action), s.user(user), body)
}

func (s *suite) dismiss(user uint, it item) *httptest.ResponseRecorder {
	return s.act(user, "dismiss-today", it.ID, it.UpdatedAt)
}

func (s *suite) closeIt(user uint, it item) *httptest.ResponseRecorder {
	return s.act(user, "close", it.ID, it.UpdatedAt)
}

func at(d time.Duration) string { return baseTime.Add(d).Format(time.RFC3339Nano) }

func countStates(t *testing.T, env *testkit.Env, announcementID, userID uint) int64 {
	t.Helper()
	var n int64
	if err := env.DB.Unscoped().Model(&announcement.UserState{}).
		Where("announcement_id = ? AND user_id = ?", announcementID, userID).Count(&n).Error; err != nil {
		t.Fatal(err)
	}
	return n
}

func jsonDecode(raw []byte, out any) error { return json.Unmarshal(raw, out) }
