package user

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const ledgerDDL = `CREATE TABLE billing_usage_ledgers (
	id INTEGER PRIMARY KEY AUTOINCREMENT,
	created_at DATETIME, updated_at DATETIME, deleted_at DATETIME,
	user_id INTEGER, usage_date date,
	input_tokens INTEGER, cache_read_tokens INTEGER, cache_write_tokens INTEGER,
	cache_write_5m_tokens INTEGER, cache_write_1h_tokens INTEGER,
	output_tokens INTEGER, reasoning_tokens INTEGER, pricing_snapshot_json TEXT)`

type ledgerSeed struct {
	user     uint
	date     string
	in, cr   int64
	cw, out  int64
	reason   int64
	snapshot string
}

func newActivityEnv(t *testing.T, rows ...ledgerSeed) *testkit.Env {
	t.Helper()
	env := newEnv(t, Ports{})
	if err := env.DB.Exec(ledgerDDL).Error; err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		err := env.DB.Exec(`INSERT INTO billing_usage_ledgers
			(created_at, updated_at, user_id, usage_date, input_tokens, cache_read_tokens, cache_write_tokens, cache_write_5m_tokens, cache_write_1h_tokens, output_tokens, reasoning_tokens, pricing_snapshot_json)
			VALUES (?, ?, ?, ?, ?, ?, ?, 0, 0, ?, ?, ?)`,
			env.Clock.Now(), env.Clock.Now(), r.user, r.date, r.in, r.cr, r.cw, r.out, r.reason, r.snapshot).Error
		if err != nil {
			t.Fatal(err)
		}
	}
	return env
}

type activityRow struct {
	Date         string `json:"date"`
	RequestCount int64  `json:"requestCount"`
	TokenUsage   int64  `json:"tokenUsage"`
}

func fetchActivity(t *testing.T, env *testkit.Env, uid uint, query string) []activityRow {
	t.Helper()
	w := env.Do("GET", "/api/v1/user/stats/activity"+query, env.Bearer(testkit.User(uid)), nil)
	testkit.Expect(t, w, 200, "")
	var rows []activityRow
	testkit.Decode(t, w, &rows)
	return rows
}

func TestActivityDefaultsTo365DaysAscendingEndingToday(t *testing.T) {
	env := newActivityEnv(t)
	rows := fetchActivity(t, env, 1, "")
	if len(rows) != 365 {
		t.Fatalf("len = %d", len(rows))
	}
	if rows[len(rows)-1].Date != "2026-03-01" {
		t.Fatalf("last day %s", rows[len(rows)-1].Date)
	}
	if rows[0].Date != "2025-03-02" {
		t.Fatalf("first day %s", rows[0].Date)
	}
	prev := ""
	for _, r := range rows {
		if r.Date <= prev {
			t.Fatalf("not ascending at %s", r.Date)
		}
		if r.RequestCount != 0 || r.TokenUsage != 0 {
			t.Fatalf("expected zero fill: %+v", r)
		}
		prev = r.Date
	}
	if got := fetchActivity(t, env, 1, "?days="); len(got) != 365 {
		t.Fatalf("blank days: %d", len(got))
	}
}

func TestActivityDaysParameter(t *testing.T) {
	env := newActivityEnv(t)
	for q, want := range map[string]int{"?days=1": 1, "?days=7": 7, "?days=366": 366, "?days=367": 366, "?days=1000": 366, "?days=%2030": 30, "?days=99999999999": 366} {
		if got := fetchActivity(t, env, 1, q); len(got) != want {
			t.Errorf("%s: %d items, want %d", q, len(got), want)
		}
	}
	for _, q := range []string{"?days=0", "?days=-5", "?days=abc", "?days=1.5", "?days=1e2", "?days=--1", "?days=0x10"} {
		w := env.Do("GET", "/api/v1/user/stats/activity"+q, env.Bearer(testkit.User(1)), nil)
		testkit.Expect(t, w, 400, "request.invalid_daily_activity_days")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid daily activity days" {
			t.Errorf("%s: message %q", q, msg)
		}
	}
}

func TestActivityZeroFillsAndSumsTokens(t *testing.T) {
	env := newActivityEnv(t,
		ledgerSeed{user: 1, date: "2026-02-27", in: 7, out: 5},
		ledgerSeed{user: 1, date: "2026-03-01", in: 10, cr: 1, cw: 2, out: 3, reason: 4},
		ledgerSeed{user: 1, date: "2026-03-01", out: 20},
	)
	rows := fetchActivity(t, env, 1, "?days=3")
	want := []activityRow{
		{"2026-02-27", 1, 12},
		{"2026-02-28", 0, 0},
		{"2026-03-01", 2, 40},
	}
	if fmt.Sprint(rows) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v", rows, want)
	}
}

func TestActivityOnlyCountsTheActingUserInsideTheWindow(t *testing.T) {
	env := newActivityEnv(t,
		ledgerSeed{user: 1, date: "2026-03-01", out: 1},
		ledgerSeed{user: 2, date: "2026-03-01", out: 100},
		ledgerSeed{user: 1, date: "2026-02-26", out: 50}, // before a 3-day window
		ledgerSeed{user: 1, date: "2026-03-02", out: 60}, // in the future
	)
	rows := fetchActivity(t, env, 1, "?days=3")
	if rows[2].RequestCount != 1 || rows[2].TokenUsage != 1 || rows[0].RequestCount != 0 || rows[1].RequestCount != 0 {
		t.Fatalf("got %v", rows)
	}
	other := fetchActivity(t, env, 2, "?days=1")
	if other[0].RequestCount != 1 || other[0].TokenUsage != 100 {
		t.Fatalf("other user: %v", other)
	}
}

func TestActivityExcludesServiceOnlyRows(t *testing.T) {
	d := "2026-03-01"
	env := newActivityEnv(t,
		ledgerSeed{user: 1, date: d, out: 1, snapshot: `{"service_only":true}`},
		ledgerSeed{user: 1, date: d, out: 2, snapshot: `{"service_only":true,"service_items":[{"service_code":"x"}]}`},
		ledgerSeed{user: 1, date: d, out: 4, snapshot: `{"service_items":[{"service_code":"title"}]}`},
		ledgerSeed{user: 1, date: d, out: 8, snapshot: `{"service_items":[{"service_code":"labels"},{"service_code":"other"}]}`},
		ledgerSeed{user: 1, date: d, out: 16, snapshot: `{"service_items":[{"service_code":"compact"}]}`},
		// counted
		ledgerSeed{user: 1, date: d, out: 32, snapshot: ``},
		ledgerSeed{user: 1, date: d, out: 64, snapshot: `{}`},
		ledgerSeed{user: 1, date: d, out: 128, snapshot: `{"service_only":false}`},
		ledgerSeed{user: 1, date: d, out: 256, snapshot: `{"service_only":false,"service_items":[{"service_code":"title"}]}`},
		ledgerSeed{user: 1, date: d, out: 512, snapshot: `{"service_items":[{"service_code":"mcp_tool.a.b"},{"service_code":"title"}]}`},
		ledgerSeed{user: 1, date: d, out: 1024, snapshot: `not json`},
	)
	rows := fetchActivity(t, env, 1, "?days=1")
	if rows[0].RequestCount != 6 || rows[0].TokenUsage != 32+64+128+256+512+1024 {
		t.Fatalf("got %+v", rows[0])
	}
}

func TestActivityStorageErrorIs500(t *testing.T) {
	env := newEnv(t, Ports{}) // no ledger table: the default source fails
	w := env.Do("GET", "/api/v1/user/stats/activity", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(w.Body.String(), "billing_usage_ledgers") {
		t.Fatalf("leaked: %s", w.Body)
	}
}

type stubActivity struct {
	gotUser  uint
	from, to string
	rows     []DayActivity
	err      error
}

func (s *stubActivity) DailyActivity(_ context.Context, uid uint, from, to string) ([]DayActivity, error) {
	s.gotUser, s.from, s.to = uid, from, to
	return s.rows, s.err
}

func TestActivityUsesInjectedSourceWithHalfOpenWindow(t *testing.T) {
	src := &stubActivity{rows: []DayActivity{{Date: "2026-03-01", Requests: 3, Tokens: 9}, {Date: "2026-03-01", Requests: 1, Tokens: 1}, {Date: "1999-01-01", Requests: 5}}}
	env := newEnv(t, Ports{Activity: src})
	rows := fetchActivity(t, env, 42, "?days=2")
	if src.gotUser != 42 || src.from != "2026-02-28" || src.to != "2026-03-02" {
		t.Fatalf("source called with %d %s %s", src.gotUser, src.from, src.to)
	}
	if len(rows) != 2 || rows[1].RequestCount != 4 || rows[1].TokenUsage != 10 {
		t.Fatalf("got %v", rows)
	}
	src.err = errors.New("db exploded")
	w := env.Do("GET", "/api/v1/user/stats/activity", env.Bearer(testkit.User(1)), nil)
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(w.Body.String(), "exploded") {
		t.Fatal("cause leaked")
	}
}

func TestActivityWithoutSourceIs500(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", activityHandler(nil, clock.System{}))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	if w.Code != http.StatusInternalServerError || !strings.Contains(w.Body.String(), "internal.error") {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestActivityWindowAcrossMonthAndYearBoundaries(t *testing.T) {
	now := time.Date(2026, 1, 2, 23, 59, 0, 0, time.UTC)
	got := activityWindow(now, 4)
	want := []string{"2025-12-30", "2025-12-31", "2026-01-01", "2026-01-02"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v", got)
	}
	leap := activityWindow(time.Date(2028, 3, 1, 0, 0, 0, 0, time.UTC), 3)
	if leap[0] != "2028-02-28" || leap[1] != "2028-02-29" {
		t.Fatalf("leap: %v", leap)
	}
	// The date is the one in the clock's own location.
	loc := time.FixedZone("x", 8*3600)
	if d := activityWindow(time.Date(2026, 3, 1, 1, 0, 0, 0, loc), 1); d[0] != "2026-03-01" {
		t.Fatalf("zone: %v", d)
	}
}
