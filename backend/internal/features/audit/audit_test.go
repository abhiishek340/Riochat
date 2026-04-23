package audit

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const listURL = "/api/v1/admin/audit-logs"

// testFeature mounts the listing plus a reference wiring of the shared
// cleanup endpoint built from the exported helpers.
func testFeature() features.Feature {
	return features.Feature{
		Name:       "audit",
		Migrations: Migrations,
		Mount: func(d deps.Deps) error {
			if err := Mount(d); err != nil {
				return err
			}
			rec := NewRecorder(d.DB, d.Logger, d.Clock)
			d.Routes.Admin.POST("/admin/logs/cleanup", func(c *gin.Context) {
				var req struct {
					Type   string `json:"type" binding:"required"`
					Before string `json:"before" binding:"required"`
				}
				if !httpx.BindJSON(c, &req) {
					return
				}
				typ, before, err := ValidateCleanup(req.Type, req.Before, d.Clock.Now())
				if err != nil {
					httpx.FailErr(c, err)
					return
				}
				n, err := PurgeBefore(c.Request.Context(), d.DB, before)
				if err != nil {
					httpx.Fail(c, http.StatusInternalServerError, err)
					return
				}
				rec.RecordCleanup(c, typ, before, n)
				httpx.OK(c, gin.H{"type": typ, "before": req.Before, "deletedCount": n})
			})
			return nil
		},
	}
}

func newEnv(t *testing.T, opts ...testkit.Option) *testkit.Env {
	t.Helper()
	SetActorResolver(nil)
	t.Cleanup(func() { SetActorResolver(nil) })
	return testkit.New(t, []features.Feature{testFeature()}, opts...)
}

type page struct {
	Total   int64
	Results []LogView
}

func list(t *testing.T, env *testkit.Env, query string) page {
	t.Helper()
	w := env.Do("GET", listURL+query, env.Bearer(testkit.Admin(1)), nil)
	testkit.Expect(t, w, 200, "")
	var p page
	testkit.Decode(t, w, &p)
	if p.Results == nil {
		t.Fatalf("results must not be null: %s", w.Body)
	}
	return p
}

func seed(t *testing.T, env *testkit.Env, rows ...Log) {
	t.Helper()
	for i := range rows {
		if err := env.DB.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
}

func ids(p page) []uint {
	out := make([]uint, 0, len(p.Results))
	for _, r := range p.Results {
		out = append(out, r.ID)
	}
	return out
}

func equalIDs(a, b []uint) bool {
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

// ---- recording ----

func TestRecordNormalizesInput(t *testing.T) {
	env := newEnv(t)
	logs := &bytes.Buffer{}
	rec := NewRecorder(env.DB, logx.New(logs, logx.LevelInfo), env.Clock)
	rec.Record(context.Background(), Entry{
		RequestID: " req_1 ", ActorUserID: 42, Action: " user.update ", Resource: " user ",
		ResourceID: " 7 ", IP: " 127.0.0.1 ", UserAgent: " browser ",
		Detail: map[string]string{"field": "status"},
	})
	var row Log
	if err := env.DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.RequestID != "req_1" || row.ActorUserID != 42 || row.Action != "user.update" || row.Resource != "user" ||
		row.ResourceID != "7" || row.IP != "127.0.0.1" || row.UserAgent != "browser" {
		t.Fatalf("not trimmed: %+v", row)
	}
	if row.DetailJSON != `{"field":"status"}` {
		t.Fatalf("detail: %q", row.DetailJSON)
	}
	if !row.CreatedAt.Equal(env.Clock.Now()) {
		t.Fatalf("created_at %v want clock %v", row.CreatedAt, env.Clock.Now())
	}
	if !strings.Contains(logs.String(), "audit") || !strings.Contains(logs.String(), "user.update") {
		t.Fatalf("info line missing: %s", logs)
	}
}

func TestRecordDetailVariants(t *testing.T) {
	env := newEnv(t)
	rec := NewRecorder(env.DB, nil, env.Clock)
	var nilMap map[string]int
	cyc := map[string]any{}
	cyc["self"] = cyc
	cases := []struct {
		name   string
		detail any
		want   string
	}{
		{"nil", nil, `{}`},
		{"typed nil map", nilMap, `{}`},
		{"channel", make(chan int), `{}`},
		{"func", func() {}, `{}`},
		{"cyclic", cyc, `{}`},
		{"html is not escaped", map[string]string{"q": "<a&b>"}, `{"q":"<a&b>"}`},
		{"list", []string{"a", "b"}, `["a","b"]`},
		{"struct", struct {
			N int `json:"n"`
		}{3}, `{"n":3}`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rec.Record(context.Background(), Entry{Action: tc.name, Detail: tc.detail})
			var row Log
			if err := env.DB.Where("action = ?", tc.name).First(&row).Error; err != nil {
				t.Fatal(err)
			}
			if row.DetailJSON != tc.want {
				t.Fatalf("detail %q want %q", row.DetailJSON, tc.want)
			}
		})
	}
}

func TestRecordEmptyAndWhitespaceFields(t *testing.T) {
	env := newEnv(t)
	rec := NewRecorder(env.DB, nil, env.Clock)
	rec.Record(context.Background(), Entry{Action: "   ", Resource: "\t"})
	var row Log
	if err := env.DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Action != "" || row.Resource != "" || row.ResourceID != "" || row.DetailJSON != "{}" {
		t.Fatalf("row: %+v", row)
	}
}

func TestRecordTruncatesLongValues(t *testing.T) {
	env := newEnv(t)
	rec := NewRecorder(env.DB, nil, env.Clock)
	rec.Record(context.Background(), Entry{
		RequestID:  strings.Repeat("r", 100),
		Action:     strings.Repeat("a", 300),
		Resource:   strings.Repeat("s", 300),
		ResourceID: strings.Repeat("x", 255),
		IP:         strings.Repeat("1", 100),
		UserAgent:  strings.Repeat("u", 400),
	})
	var row Log
	if err := env.DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if len(row.RequestID) != 64 || len(row.Action) != 128 || len(row.Resource) != 128 ||
		len(row.ResourceID) != 128 || len(row.IP) != 64 || len(row.UserAgent) != 255 {
		t.Fatalf("lengths: %d %d %d %d %d %d", len(row.RequestID), len(row.Action), len(row.Resource), len(row.ResourceID), len(row.IP), len(row.UserAgent))
	}
}

func TestFitKeepsUTF8Valid(t *testing.T) {
	got := fit(strings.Repeat("é", 200), 128) // limits count characters, not bytes
	if utf8.RuneCountInString(got) != 128 || !utf8.ValidString(got) {
		t.Fatalf("runes %d %q", utf8.RuneCountInString(got), got)
	}
	if got := fit("ab  cd", 4); got != "ab" {
		t.Fatalf("trailing space left after cut: %q", got)
	}
	if got := fit("  abc  ", 10); got != "abc" {
		t.Fatalf("%q", got)
	}
}

func TestRecordSurvivesPanickingDetail(t *testing.T) {
	env := newEnv(t)
	rec := NewRecorder(env.DB, nil, env.Clock)
	rec.Record(context.Background(), Entry{Action: "x", Detail: panicJSON{}})
	var row Log
	if err := env.DB.First(&row).Error; err != nil || row.DetailJSON != "{}" {
		t.Fatalf("%v %+v", err, row)
	}
}

type panicJSON struct{}

func (panicJSON) MarshalJSON() ([]byte, error) { panic("boom") }

func TestListSurvivesPanickingResolver(t *testing.T) {
	env := newEnv(t)
	SetActorResolver(ActorResolverFunc(func(context.Context, uint) (Actor, bool, error) { panic("boom") }))
	seed(t, env, Log{ActorUserID: 9})
	p := list(t, env, "")
	if len(p.Results) != 1 || p.Results[0].ActorLabel != "" || p.Results[0].ActorUserID != 9 {
		t.Fatalf("%+v", p.Results)
	}
}

func TestRecordTruncatesMultibyteByCharacters(t *testing.T) {
	env := newEnv(t)
	rec := NewRecorder(env.DB, nil, env.Clock)
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	rec.Record(context.Background(), Entry{ResourceID: strings.Repeat("\u4e2d", 200), UserAgent: strings.Repeat("é", 300)})
	var row Log
	if err := env.DB.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if utf8.RuneCountInString(row.ResourceID) != 128 || utf8.RuneCountInString(row.UserAgent) != 255 {
		t.Fatalf("%d %d", utf8.RuneCountInString(row.ResourceID), utf8.RuneCountInString(row.UserAgent))
	}
}

func TestRecordPersistFailureIsSwallowed(t *testing.T) {
	env := newEnv(t)
	logs := &bytes.Buffer{}
	rec := NewRecorder(env.DB, logx.New(logs, logx.LevelInfo), env.Clock)
	if err := env.DB.Migrator().DropTable(&Log{}); err != nil {
		t.Fatal(err)
	}
	rec.Record(context.Background(), Entry{RequestID: "r9", Action: "x.y"}) // must not panic
	out := logs.String()
	if !strings.Contains(out, "audit_persist_failed") {
		t.Fatalf("error log missing: %s", out)
	}
	if !strings.Contains(out, "x.y") || !strings.Contains(out, "r9") {
		t.Fatalf("error log lacks action/request id: %s", out)
	}
}

func TestRecordInfoLineEmittedEvenWhenInsertFails(t *testing.T) {
	env := newEnv(t)
	logs := &bytes.Buffer{}
	rec := NewRecorder(env.DB, logx.New(logs, logx.LevelInfo), env.Clock)
	_ = env.DB.Migrator().DropTable(&Log{})
	rec.Record(context.Background(), Entry{Action: "login", Detail: map[string]string{"event": "user_login"}})
	lines := strings.Split(strings.TrimSpace(logs.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("want info + error line, got %d: %s", len(lines), logs)
	}
	if strings.Contains(lines[0], "audit_persist_failed") || !strings.Contains(lines[0], "user_login") {
		t.Fatalf("first line must be the audit info line: %s", lines[0])
	}
	if !strings.Contains(lines[1], "audit_persist_failed") {
		t.Fatalf("second line must be the failure: %s", lines[1])
	}
}

func TestNilRecorderIsNoop(t *testing.T) {
	var rec *Recorder
	rec.Record(context.Background(), Entry{Action: "x"})
	rec.RecordRequest(nil, "x", "y", "z", nil) // must not touch the nil context
	rec.RecordCleanup(nil, "audit", time.Now(), 1)
}

func TestRecordRequestUsesCallerAndRequestMetadata(t *testing.T) {
	env := newEnv(t)
	// RecordRequest needs an authenticated gin context, so exercise it through
	// the cleanup route which calls RecordCleanup -> RecordRequest.
	w := env.Do("POST", "/api/v1/admin/logs/cleanup", mergeHeaders(env.Bearer(testkit.Admin(77)), map[string]string{"User-Agent": " cleaner/1 "}),
		map[string]string{"type": "audit", "before": env.Clock.Now().Add(-time.Hour).Format(time.RFC3339)})
	testkit.Expect(t, w, 200, "")
	var row Log
	if err := env.DB.Where("action = ?", CleanupAction).First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.ActorUserID != 77 || row.UserAgent != "cleaner/1" || row.IP != "203.0.113.5" || row.RequestID == "" {
		t.Fatalf("metadata: %+v", row)
	}
}

func mergeHeaders(a, b map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range a {
		out[k] = v
	}
	for k, v := range b {
		out[k] = v
	}
	return out
}

func TestRedactValue(t *testing.T) {
	if RedactValue("hunter2", true) != "[REDACTED]" {
		t.Fatal("sensitive non-empty must be redacted")
	}
	if RedactValue("", true) != "" {
		t.Fatal("sensitive empty must stay empty")
	}
	if RedactValue("dark", false) != "dark" {
		t.Fatal("non-sensitive passes through")
	}
}

// ---- list endpoint ----

func TestListDefaults(t *testing.T) {
	env := newEnv(t)
	for i := 0; i < 25; i++ {
		seed(t, env, Log{Action: fmt.Sprintf("a%d", i)})
	}
	p := list(t, env, "")
	if p.Total != 25 || len(p.Results) != 20 {
		t.Fatalf("total %d len %d", p.Total, len(p.Results))
	}
	for i := 1; i < len(p.Results); i++ {
		if p.Results[i-1].ID <= p.Results[i].ID {
			t.Fatalf("not id desc: %v", ids(p))
		}
	}
	p2 := list(t, env, "?page=2")
	if len(p2.Results) != 5 || p2.Total != 25 {
		t.Fatalf("page 2: %d / %d", len(p2.Results), p2.Total)
	}
}

func TestListEmptyIsArrayNotNull(t *testing.T) {
	env := newEnv(t)
	w := env.Do("GET", listURL, env.Bearer(testkit.Admin(1)), nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"results":[]`) || !strings.Contains(w.Body.String(), `"total":0`) {
		t.Fatalf("body: %s", w.Body)
	}
}

func TestListPaginationNormalization(t *testing.T) {
	env := newEnv(t)
	for i := 0; i < 30; i++ {
		seed(t, env, Log{Action: "a"})
	}
	if p := list(t, env, "?page_size=5000"); len(p.Results) != 30 || p.Total != 30 {
		t.Fatalf("clamped: %d", len(p.Results))
	}
	for _, q := range []string{"?page=0", "?page_size=-3", "?page=abc", "?page_size=zz&page=-1"} {
		p := list(t, env, q)
		if len(p.Results) != 20 && !(q == "?page_size=-3" && len(p.Results) == 20) {
			t.Fatalf("%s: %d", q, len(p.Results))
		}
		if p.Total != 30 {
			t.Fatalf("%s total %d", q, p.Total)
		}
	}
	if p := list(t, env, "?page=2&page_size=25"); len(p.Results) != 5 {
		t.Fatalf("page 2 of 25: %d", len(p.Results))
	}
	// Beyond the last page, including an absurd page number: empty, correct total.
	for _, q := range []string{"?page=99", "?page=9223372036854775807&page_size=1000"} {
		if p := list(t, env, q); len(p.Results) != 0 || p.Total != 30 {
			t.Fatalf("%s: %d / %d", q, len(p.Results), p.Total)
		}
	}
}

func TestListInvalidParameters(t *testing.T) {
	env := newEnv(t)
	admin := env.Bearer(testkit.Admin(1))
	cases := []struct{ query, msg string }{
		{"?actor_user_id=0", "invalid actor_user_id"},
		{"?actor_user_id=-1", "invalid actor_user_id"},
		{"?actor_user_id=x", "invalid actor_user_id"},
		{"?actor_user_id=99999999999999999999999", "invalid actor_user_id"},
		{"?actor_user_id=1.5", "invalid actor_user_id"},
		{"?created_from=yesterday", "invalid created_from"},
		{"?created_to=2024-13-45T00:00:00Z", "invalid created_to"},
		{"?created_from=2024-01-01", "invalid created_from"},
		// First failure wins: actor, then from, then to.
		{"?actor_user_id=x&created_from=bad&created_to=bad", "invalid actor_user_id"},
		{"?created_from=bad&created_to=bad", "invalid created_from"},
	}
	for _, tc := range cases {
		w := env.Do("GET", listURL+tc.query, admin, nil)
		testkit.Expect(t, w, 400, "request.invalid_query")
		if got := testkit.Envelope(t, w).ErrorMsg; got != tc.msg {
			t.Errorf("%s: message %q want %q", tc.query, got, tc.msg)
		}
	}
}

func TestListFilters(t *testing.T) {
	env := newEnv(t)
	seed(t, env,
		Log{Action: "login", Resource: "user", ResourceID: "ann", IP: "127.0.0.1", ActorUserID: 5, DetailJSON: `{"event":"user_login"}`},
		Log{Action: "settings.update", Resource: "system_settings", ActorUserID: 6, UserAgent: "Mozilla", RequestID: "req-abc", DetailJSON: `[{"key":"smtp.password","value":"[REDACTED]"}]`},
	)
	cases := []struct {
		query string
		want  int
	}{
		{"?action=login", 1},
		{"?action=%20login%20", 1},
		{"?resource=system_settings", 1},
		{"?action=login&resource=system_settings", 0},
		{"?action=log", 0}, // exact match only
		{"?actor_user_id=6", 1},
		{"?actor_user_id=7", 0},
		{"?query=REDACTED", 1},
		{"?query=127.0.0.1", 1},
		{"?query=Mozilla", 1},
		{"?query=req-abc", 1},
		{"?query=user_login", 1},
		{"?query=ann", 1},
		{"?query=", 2},
		{"?query=%20%20%20", 2},
		{"?query=nothing-like-this", 0},
		{"?query=settings&action=settings.update", 1},
		{"?query=settings&action=login", 0},
	}
	for _, tc := range cases {
		p := list(t, env, tc.query)
		if len(p.Results) != tc.want || int(p.Total) != tc.want {
			t.Errorf("%s: got %d (total %d) want %d", tc.query, len(p.Results), p.Total, tc.want)
		}
	}
}

func TestListKeywordDoesNotSearchActorID(t *testing.T) {
	env := newEnv(t)
	seed(t, env, Log{Action: "x", ActorUserID: 424242})
	if p := list(t, env, "?query=424242"); p.Total != 0 {
		t.Fatalf("actor id must not be searched: %d", p.Total)
	}
}

func TestListKeywordWildcardsAreNotEscaped(t *testing.T) {
	env := newEnv(t)
	seed(t, env, Log{Action: "alpha"}, Log{Action: "beta"})
	if p := list(t, env, "?query=a%25a"); p.Total != 1 { // %25 -> literal %, acts as wildcard: "alpha"
		t.Fatalf("'%%' wildcard: %d", p.Total)
	}
	if p := list(t, env, "?query=_eta"); p.Total != 1 {
		t.Fatalf("'_' wildcard: %d", p.Total)
	}
}

func TestListSqlInjectionIsInert(t *testing.T) {
	env := newEnv(t)
	seed(t, env, Log{Action: "a"})
	for _, q := range []string{"?query=%27%20OR%201%3D1%20--", "?action=%27%3B%20DROP%20TABLE%20audit_logs%3B--"} {
		p := list(t, env, q)
		if p.Total != 0 {
			t.Fatalf("%s matched rows", q)
		}
	}
	if p := list(t, env, ""); p.Total != 1 {
		t.Fatal("table damaged")
	}
}

func TestListTimeRangeInclusive(t *testing.T) {
	env := newEnv(t)
	t1 := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)
	t2 := t1.Add(time.Hour)
	t3 := t2.Add(time.Hour)
	for _, ts := range []time.Time{t1, t2, t3} {
		seed(t, env, Log{Model: modelAt(ts), Action: ts.Format("15")})
	}
	q := func(from, to time.Time) string {
		s := "?"
		if !from.IsZero() {
			s += "created_from=" + from.Format(time.RFC3339) + "&"
		}
		if !to.IsZero() {
			s += "created_to=" + to.Format(time.RFC3339)
		}
		return s
	}
	cases := []struct {
		name     string
		from, to time.Time
		want     int
	}{
		{"exact T2", t2, t2, 1},
		{"from T2", t2, time.Time{}, 2},
		{"to T2", time.Time{}, t2, 2},
		{"all", t1, t3, 3},
		{"from after to", t3, t1, 0},
		{"before everything", time.Time{}, t1.Add(-time.Second), 0},
	}
	for _, tc := range cases {
		p := list(t, env, q(tc.from, tc.to))
		if int(p.Total) != tc.want || len(p.Results) != tc.want {
			t.Errorf("%s: total %d len %d want %d", tc.name, p.Total, len(p.Results), tc.want)
		}
	}
	// A non-UTC offset must denote the same instant.
	zone := time.FixedZone("X", 5*3600+1800)
	if p := list(t, env, "?created_from="+t2.In(zone).Format(time.RFC3339)[:19]+"%2B05:30&created_to="+t2.In(zone).Format(time.RFC3339)[:19]+"%2B05:30"); p.Total != 1 {
		t.Fatalf("offset handling: %d", p.Total)
	}
}

func TestListSorting(t *testing.T) {
	env := newEnv(t)
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	// ids 1..4; created order: id2 oldest, then id1 and id3 tie, id4 newest.
	seed(t, env,
		Log{Model: modelAt(base.Add(time.Hour)), Action: "one"},
		Log{Model: modelAt(base), Action: "two"},
		Log{Model: modelAt(base.Add(time.Hour)), Action: "three"},
		Log{Model: modelAt(base.Add(2 * time.Hour)), Action: "four"},
	)
	cases := map[string][]uint{
		"":                   {4, 3, 2, 1},
		"?sort=bogus":        {4, 3, 2, 1},
		"?sort=id_asc":       {1, 2, 3, 4},
		"?sort=%20id_asc%20": {1, 2, 3, 4},
		"?sort=created_asc":  {2, 1, 3, 4},
		"?sort=created_desc": {4, 3, 1, 2},
		"?sort=ID_ASC":       {4, 3, 2, 1},
	}
	for q, want := range cases {
		if got := ids(list(t, env, q)); !equalIDs(got, want) {
			t.Errorf("%q: %v want %v", q, got, want)
		}
	}
}
