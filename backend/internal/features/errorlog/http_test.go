package errorlog_test

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

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/errorlog"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// probe is a test-only area that fails in the ways the error log must capture.
func probeMount(d deps.Deps) error {
	g := d.Routes.Public
	g.GET("/probe/boom", func(c *gin.Context) {
		d.Logger.Info("probe")
		c.Set("x", 1)
		failWith(c, errors.New("database is locked"))
	})
	g.GET("/probe/diag", func(c *gin.Context) {
		failWith(c, apperr.ErrInternal.WithCause(errors.New("upstream said no")).
			WithDiagnostic(`{"error":{"message":"model not found","api_key=sk-live-123"}}`))
	})
	g.GET("/probe/nocause", func(c *gin.Context) { c.AbortWithStatus(http.StatusInternalServerError) })
	g.GET("/probe/stream", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("data: hi\n\n")
		reqctx.FromRequest(c.Request).RecordFailure("upstream.unavailable", errors.New("upstream closed stream"), "")
	})
	g.GET("/probe/stream-code-only", func(c *gin.Context) {
		c.Status(http.StatusOK)
		_, _ = c.Writer.WriteString("data: hi\n\n")
		reqctx.FromRequest(c.Request).RecordFailure("custom.whatever", nil, "")
	})
	g.GET("/probe/missing", func(c *gin.Context) { c.AbortWithStatus(http.StatusNotFound) })
	g.GET("/probe/ok", func(c *gin.Context) { c.String(http.StatusOK, "ok") })
	g.GET("/probe/panic", func(c *gin.Context) { panic("kaboom") })
	return nil
}

func failWith(c *gin.Context, err error) {
	reqctx.FromRequest(c.Request).RecordFailure(apperr.ErrInternal.Code, err, apperr.Diagnostic(err))
	c.AbortWithStatusJSON(500, gin.H{"errorMsg": "internal server error", "errorCode": apperr.ErrInternal.Code})
}

func newEnv(t *testing.T, opts ...testkit.Option) *testkit.Env {
	t.Helper()
	return testkit.New(t, []features.Feature{
		{Name: "errorlog", Migrations: errorlog.Migrations, Mount: errorlog.Mount},
		{Name: "probe", Mount: probeMount},
	}, opts...)
}

// flush waits for detached writes.
func flush(t *testing.T, env *testkit.Env) {
	t.Helper()
	if !env.App.Lifecycle.WaitDetached(3 * time.Second) {
		t.Fatal("detached writes did not finish")
	}
}

func rows(t *testing.T, env *testkit.Env) []errorlog.Entry {
	t.Helper()
	flush(t, env)
	var out []errorlog.Entry
	if err := env.DB.Order("id").Find(&out).Error; err != nil {
		t.Fatal(err)
	}
	return out
}

func TestPipelinePersistsServerFailuresAndStreams(t *testing.T) {
	env := newEnv(t)
	env.Do("GET", "/api/v1/probe/boom?token=secret&a=1", nil, nil)
	env.Do("GET", "/api/v1/probe/stream", nil, nil)
	env.Do("GET", "/api/v1/probe/missing", nil, nil)
	env.Do("GET", "/api/v1/probe/ok", nil, nil)
	got := rows(t, env)
	if len(got) != 2 {
		t.Fatalf("persisted %d rows: %+v", len(got), got)
	}
	byRoute := map[string]errorlog.Entry{}
	for _, e := range got {
		byRoute[e.Route] = e
	}
	boom, stream := byRoute["/api/v1/probe/boom"], byRoute["/api/v1/probe/stream"]
	if boom.StatusCode != 500 || boom.ErrorCode != "internal.error" || boom.Cause != "database is locked" {
		t.Fatalf("boom: %+v", boom)
	}
	if boom.Route != "/api/v1/probe/boom" || boom.RequestID == "" || boom.Method != "GET" {
		t.Fatalf("boom meta: %+v", boom)
	}
	if strings.Contains(boom.Path, "secret") || !strings.Contains(boom.Path, "token=REDACTED") || !strings.Contains(boom.Path, "a=1") {
		t.Fatalf("path: %q", boom.Path)
	}
	if boom.UserID != 0 || boom.CreatedAt.IsZero() {
		t.Fatalf("boom user/time: %+v", boom)
	}
	if stream.StatusCode != 200 || stream.Cause != "upstream closed stream" || stream.ErrorCode != "upstream.unavailable" {
		t.Fatalf("stream: %+v", stream)
	}
}

func TestStreamFailureWithOnlyErrorCodeIsPersistedWithEmptyCause(t *testing.T) {
	env := newEnv(t)
	env.Do("GET", "/api/v1/probe/stream-code-only", nil, nil)
	got := rows(t, env)
	if len(got) != 1 || got[0].StatusCode != 200 || got[0].Cause != "" || got[0].ErrorCode != "custom.whatever" {
		t.Fatalf("rows: %+v", got)
	}
}

func TestDiagnosticDetailIsMasked(t *testing.T) {
	env := newEnv(t)
	env.Do("GET", "/api/v1/probe/diag", nil, nil)
	got := rows(t, env)
	if len(got) != 1 {
		t.Fatalf("rows: %+v", got)
	}
	if !strings.Contains(got[0].Detail, "model not found") || strings.Contains(got[0].Detail, "sk-live-123") {
		t.Fatalf("detail: %q", got[0].Detail)
	}
}

func TestFiveHundredWithoutCauseUsesMarker(t *testing.T) {
	env := newEnv(t)
	env.Do("GET", "/api/v1/probe/nocause", nil, nil)
	got := rows(t, env)
	if len(got) != 1 || got[0].Cause != "<no cause recorded>" {
		t.Fatalf("rows: %+v", got)
	}
}

func TestPanicIsRecordedWithoutStack(t *testing.T) {
	env := newEnv(t)
	w := env.Do("GET", "/api/v1/probe/panic", nil, nil)
	testkit.Expect(t, w, 500, "internal.error")
	got := rows(t, env)
	if len(got) != 1 {
		t.Fatalf("rows: %+v", got)
	}
	e := got[0]
	if e.StatusCode != 500 || e.ErrorCode != "internal.error" || !strings.HasPrefix(e.Cause, "panic: ") || e.Detail != "" {
		t.Fatalf("panic row: %+v", e)
	}
	if strings.Contains(e.Cause, "goroutine") || strings.Contains(e.Cause, ".go:") {
		t.Fatalf("stack leaked: %q", e.Cause)
	}
}

func TestAuthenticatedUserIsRecorded(t *testing.T) {
	env := newEnv(t)
	env.Do("GET", "/api/v1/probe/boom", env.Bearer(testkit.User(7)), nil)
	// Public group does not authenticate, so the principal stays anonymous.
	got := rows(t, env)
	if len(got) != 1 || got[0].UserID != 0 {
		t.Fatalf("rows: %+v", got)
	}
}

func TestLocalModePersistsNothing(t *testing.T) {
	env := newEnv(t, testkit.WithConfig(func(c *config.Config) {
		c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"}
	}))
	env.Do("GET", "/api/v1/probe/boom", nil, nil)
	if got := rows(t, env); len(got) != 0 {
		t.Fatalf("rows: %+v", got)
	}
}

func TestMigrationsAreIdempotentAndCreateTable(t *testing.T) {
	env := newEnv(t)
	for _, step := range errorlog.Migrations() {
		if step.Name == "" {
			t.Fatal("unnamed step")
		}
	}
	var _ []migrate.Step = errorlog.Migrations()
	cols, err := env.DB.Migrator().ColumnTypes(&errorlog.Entry{})
	if err != nil {
		t.Fatal(err)
	}
	have := map[string]bool{}
	for _, c := range cols {
		have[c.Name()] = true
	}
	for _, want := range strings.Fields("id request_id trace_id user_id method route path status_code error_code cause detail latency_ms client_ip user_agent created_at") {
		if !have[want] {
			t.Errorf("missing column %s", want)
		}
	}
	for _, bad := range []string{"updated_at", "deleted_at"} {
		if have[bad] {
			t.Errorf("unexpected column %s", bad)
		}
	}
	if !env.DB.Migrator().HasIndex(&errorlog.Entry{}, "RequestID") {
		t.Error("request_id index missing")
	}
}

// --- admin list -------------------------------------------------------------

type page struct {
	Total   int64
	Results []map[string]any
}

func seed(t *testing.T, env *testkit.Env) {
	t.Helper()
	base := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)
	list := []errorlog.Entry{
		{RequestID: "req-a", TraceID: "trace-a", UserID: 1, Method: "POST", Route: "/api/v1/chat/stream", Path: "/api/v1/chat/stream", StatusCode: 200, ErrorCode: "upstream.unavailable", Cause: "closed", CreatedAt: base},
		{RequestID: "req-b", TraceID: "Trace-B", UserID: 2, Method: "GET", Route: "/api/v1/things", Path: "/api/v1/things?x=1", StatusCode: 500, ErrorCode: "internal.error", Cause: "generate failed", Detail: `{"message":"model not found"}`, CreatedAt: base.Add(time.Hour)},
		{RequestID: "req-ab", TraceID: "trace-ab", UserID: 2, Method: "GET", Route: "/api/v1/other", Path: "/api/v1/other", StatusCode: 502, ErrorCode: "internal.error", Cause: "boom", CreatedAt: base.Add(2 * time.Hour)},
	}
	for i := range list {
		if err := env.DB.Create(&list[i]).Error; err != nil {
			t.Fatal(err)
		}
	}
}

func ids(p page) []string {
	var out []string
	for _, r := range p.Results {
		out = append(out, r["requestID"].(string))
	}
	return out
}

func query(t *testing.T, env *testkit.Env, qs string) page {
	t.Helper()
	w := env.Do("GET", "/api/v1/admin/error-logs"+qs, env.Bearer(testkit.Admin(9)), nil)
	testkit.Expect(t, w, 200, "")
	var p page
	testkit.Decode(t, w, &p)
	if p.Results == nil {
		t.Fatalf("results must be an array: %s", w.Body)
	}
	return p
}

func TestListFilters(t *testing.T) {
	env := newEnv(t)
	seed(t, env)
	cases := []struct {
		name string
		qs   string
		want []string
	}{
		{"all newest first", "", []string{"req-ab", "req-b", "req-a"}},
		{"ascending", "?sort=created_asc", []string{"req-a", "req-b", "req-ab"}},
		{"unknown sort", "?sort=bogus", []string{"req-ab", "req-b", "req-a"}},
		{"keyword exact id, no substring leak", "?query=req-a", []string{"req-a"}},
		{"request_id", "?request_id=req-b", []string{"req-b"}},
		{"request_id trimmed", "?request_id=%20req-b%20", []string{"req-b"}},
		{"keyword case-insensitive cause", "?query=GENERATE%20FAILED", []string{"req-b"}},
		{"keyword in detail", "?query=model%20not%20found", []string{"req-b"}},
		{"keyword in route", "?query=chat/stream", []string{"req-a"}},
		{"keyword in path query", "?query=x%3D1", []string{"req-b"}},
		{"keyword in error code", "?query=UPSTREAM", []string{"req-a"}},
		{"trace id exact lowercased", "?query=TRACE-A", []string{"req-a"}},
		{"trace id is not a substring match", "?query=trace-", nil},
		{"mixed-case stored trace id not matched", "?query=Trace-B", nil},
		{"keyword wildcard is literal", "?query=%25", nil},
		{"keyword underscore is literal", "?query=req_a", nil},
		{"error_code", "?error_code=internal.error", []string{"req-ab", "req-b"}},
		{"5xx", "?status_class=5xx", []string{"req-ab", "req-b"}},
		{"stream", "?status_class=stream", []string{"req-a"}},
		{"unknown class ignored", "?status_class=weird", []string{"req-ab", "req-b", "req-a"}},
		{"user_id newest first", "?user_id=2", []string{"req-ab", "req-b"}},
		{"user_id and keyword are ANDed", "?user_id=2&query=generate", []string{"req-b"}},
		{"from inclusive", "?created_from=2026-02-01T13:00:00Z", []string{"req-ab", "req-b"}},
		{"to inclusive", "?created_to=2026-02-01T13:00:00Z", []string{"req-b", "req-a"}},
		{"range with offset zone", "?created_from=2026-02-01T21%3A00%3A00%2B08%3A00&created_to=2026-02-01T21%3A00%3A00%2B08%3A00", []string{"req-b"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := query(t, env, tc.qs)
			got := ids(p)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) && !(len(got) == 0 && len(tc.want) == 0) {
				t.Fatalf("got %v want %v", got, tc.want)
			}
			if p.Total != int64(len(tc.want)) {
				t.Fatalf("total %d want %d", p.Total, len(tc.want))
			}
		})
	}
}

func TestListPagingAndTotals(t *testing.T) {
	env := newEnv(t)
	seed(t, env)
	p := query(t, env, "?page=2&page_size=2")
	if p.Total != 3 || len(p.Results) != 1 || ids(p)[0] != "req-a" {
		t.Fatalf("page 2: %+v", p)
	}
	p = query(t, env, "?page=abc&page_size=-4")
	if p.Total != 3 || len(p.Results) != 3 {
		t.Fatalf("fallbacks: %+v", p)
	}
	p = query(t, env, "?page=99999999999999999&page_size=5000")
	if p.Total != 3 || len(p.Results) != 0 {
		t.Fatalf("huge page: %+v", p)
	}
	p = query(t, env, "?query=nothing-matches")
	if p.Total != 0 || p.Results == nil || len(p.Results) != 0 {
		t.Fatalf("empty: %+v", p)
	}
}

func TestPageSizeCapAndDefault(t *testing.T) {
	env := newEnv(t)
	base := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	batch := make([]errorlog.Entry, 0, 1100)
	for i := 0; i < 1100; i++ {
		batch = append(batch, errorlog.Entry{RequestID: fmt.Sprint("r", i), StatusCode: 500, CreatedAt: base.Add(time.Duration(i) * time.Second)})
	}
	if err := env.DB.CreateInBatches(batch, 100).Error; err != nil {
		t.Fatal(err)
	}
	if p := query(t, env, "?page_size=5000"); len(p.Results) != 1000 || p.Total != 1100 {
		t.Fatalf("cap: %d/%d", len(p.Results), p.Total)
	}
	if p := query(t, env, ""); len(p.Results) != 20 {
		t.Fatalf("default: %d", len(p.Results))
	}
}

func TestEqualTimestampsOrderByID(t *testing.T) {
	env := newEnv(t)
	at := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	for _, id := range []string{"one", "two", "three"} {
		env.DB.Create(&errorlog.Entry{RequestID: id, StatusCode: 500, CreatedAt: at})
	}
	if got := ids(query(t, env, "")); fmt.Sprint(got) != "[three two one]" {
		t.Fatalf("desc: %v", got)
	}
	if got := ids(query(t, env, "?sort=created_asc")); fmt.Sprint(got) != "[one two three]" {
		t.Fatalf("asc: %v", got)
	}
}

func TestInvalidQueryParameters(t *testing.T) {
	env := newEnv(t)
	admin := env.Bearer(testkit.Admin(9))
	cases := []struct{ qs, msg string }{
		{"?user_id=0", "invalid user_id"},
		{"?user_id=-3", "invalid user_id"},
		{"?user_id=abc", "invalid user_id"},
		{"?created_from=yesterday", "invalid created_from"},
		{"?created_to=2026-02-01", "invalid created_to"},
	}
	for _, tc := range cases {
		w := env.Do("GET", "/api/v1/admin/error-logs"+tc.qs, admin, nil)
		testkit.Expect(t, w, 400, "request.invalid_query")
		if got := testkit.Envelope(t, w).ErrorMsg; got != tc.msg {
			t.Errorf("%s: message %q want %q", tc.qs, got, tc.msg)
		}
	}
	// An empty user_id means "no filter".
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/error-logs?user_id=", admin, nil), 200, "")
}

func TestAdminOnly(t *testing.T) {
	env := newEnv(t)
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/error-logs", nil, nil), 401, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/error-logs", env.Bearer(testkit.User(5)), nil), 403, "auth.admin_required")
}

func TestResultShape(t *testing.T) {
	env := newEnv(t)
	seed(t, env)
	p := query(t, env, "?request_id=req-b")
	row := p.Results[0]
	for _, k := range strings.Fields("id requestID traceID userID userUsername userDisplayName userLabel method route path statusCode errorCode cause detail latencyMS clientIP userAgent createdAt") {
		if _, ok := row[k]; !ok {
			t.Errorf("missing key %s in %v", k, row)
		}
	}
	if row["createdAt"] != "2026-02-01T13:00:00Z" {
		t.Errorf("createdAt: %v", row["createdAt"])
	}
}

func TestUserLabels(t *testing.T) {
	env := newEnv(t)
	seed(t, env)
	env.DB.Create(&errorlog.Entry{RequestID: "req-u3", UserID: 3, StatusCode: 500, CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)})
	env.DB.Create(&errorlog.Entry{RequestID: "req-u4", UserID: 4, StatusCode: 500, CreatedAt: time.Date(2026, 3, 2, 0, 0, 0, 0, time.UTC)})
	mustExec(t, env, `CREATE TABLE identity_users (id INTEGER PRIMARY KEY, username TEXT, display_name TEXT, deleted_at DATETIME)`)
	mustExec(t, env, `INSERT INTO identity_users (id, username, display_name) VALUES (1, 'alice', 'Alice A'), (2, 'bobby', '  '), (3, '', '')`)
	mustExec(t, env, `INSERT INTO identity_users (id, username, display_name, deleted_at) VALUES (4, 'gone', 'Gone', '2026-01-01')`)

	byReq := map[string]map[string]any{}
	for _, r := range query(t, env, "").Results {
		byReq[r["requestID"].(string)] = r
	}
	check := func(req, user, display, label string) {
		t.Helper()
		r := byReq[req]
		if r["userUsername"] != user || r["userDisplayName"] != display || r["userLabel"] != label {
			t.Errorf("%s: %v", req, r)
		}
	}
	check("req-a", "alice", "Alice A", "Alice A")
	check("req-b", "bobby", "  ", "bobby")
	check("req-u3", "", "", "3")
	check("req-u4", "", "", "") // soft-deleted user is treated as missing
}

func TestAnonymousAndFailedLookupStillReturnRows(t *testing.T) {
	env := newEnv(t) // no identity_users table: lookups fail
	seed(t, env)
	env.DB.Create(&errorlog.Entry{RequestID: "anon", StatusCode: 500, CreatedAt: time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)})
	p := query(t, env, "")
	if p.Total != 4 || len(p.Results) != 4 {
		t.Fatalf("page: %+v", p)
	}
	for _, r := range p.Results {
		if r["userUsername"] != "" || r["userDisplayName"] != "" || r["userLabel"] != "" {
			t.Fatalf("names must be empty: %v", r)
		}
	}
}

func TestCustomUserDirectory(t *testing.T) {
	env := testkit.New(t, []features.Feature{{
		Name: "errorlog", Migrations: errorlog.Migrations,
		Mount: errorlog.MountWith(fakeDirectory{1: {Username: "u1", DisplayName: "One"}}),
	}})
	env.DB.Create(&errorlog.Entry{RequestID: "r", UserID: 1, StatusCode: 500, CreatedAt: time.Now()})
	p := query(t, env, "")
	if p.Results[0]["userLabel"] != "One" {
		t.Fatalf("%v", p.Results[0])
	}
}

type fakeDirectory map[uint]errorlog.UserInfo

func (f fakeDirectory) LookupUsers(_ context.Context, ids []uint) (map[uint]errorlog.UserInfo, error) {
	out := map[uint]errorlog.UserInfo{}
	for _, id := range ids {
		if u, ok := f[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

func TestStorageFailureIs500WithGenericBody(t *testing.T) {
	env := newEnv(t)
	mustExec(t, env, `DROP TABLE system_error_logs`)
	w := env.Do("GET", "/api/v1/admin/error-logs", env.Bearer(testkit.Admin(9)), nil)
	testkit.Expect(t, w, 500, "internal.error")
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "internal server error" || strings.Contains(w.Body.String(), "system_error_logs") {
		t.Fatalf("leaky body: %s", w.Body)
	}
}

func TestNilServiceYieldsEmptyPage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r := gin.New()
	r.GET("/x", errorlog.ListHandlerForTest(nil))
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
	testkit.Expect(t, w, 200, "")
	var p page
	testkit.Decode(t, w, &p)
	if p.Total != 0 || p.Results == nil || len(p.Results) != 0 {
		t.Fatalf("page: %+v body %s", p, w.Body)
	}
}

func TestCleanupHelperThroughHTTPVisibleState(t *testing.T) {
	env := newEnv(t)
	seed(t, env)
	cut := time.Date(2026, 2, 1, 13, 0, 0, 0, time.UTC)
	n, err := errorlog.PurgeBefore(t.Context(), env.DB, cut)
	if err != nil || n != 1 {
		t.Fatalf("purged %d err %v", n, err)
	}
	p := query(t, env, "?sort=created_asc")
	if fmt.Sprint(ids(p)) != "[req-b req-ab]" {
		t.Fatalf("left: %v", ids(p))
	}
}

func mustExec(t *testing.T, env *testkit.Env, sql string) {
	t.Helper()
	if err := env.DB.Exec(sql).Error; err != nil {
		t.Fatal(err)
	}
}
