package settings_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// harness bundles a running test application with the settings service.
type harness struct {
	t     *testing.T
	env   *testkit.Env
	svc   *settings.Service
	admin map[string]string
	user  map[string]string
}

func newHarness(t *testing.T, ports settings.Ports, opts ...testkit.Option) *harness {
	t.Helper()
	h := &harness{t: t}
	list := []features.Feature{{
		Name:       "settings",
		Migrations: settings.Migrations,
		Mount: func(d deps.Deps) error {
			svc, err := settings.MountWith(d, ports)
			h.svc = svc
			return err
		},
	}}
	h.env = testkit.New(t, list, opts...)
	h.admin = h.env.Bearer(testkit.Admin(1))
	h.user = h.env.Bearer(testkit.User(7))
	return h
}

// advance moves the fake clock and refreshes the bearer tokens, which would
// otherwise expire with it.
func (h *harness) advance(d time.Duration) {
	h.t.Helper()
	h.env.Clock.Advance(d)
	h.admin = h.env.Bearer(testkit.Admin(1))
	h.user = h.env.Bearer(testkit.User(7))
}

func localMode() testkit.Option {
	return testkit.WithConfig(func(c *config.Config) { c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"} })
}

type item struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ValueType   string `json:"valueType"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
	Configured  bool   `json:"configured"`
}

func (h *harness) do(method, path string, headers map[string]string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.env.Do(method, path, headers, body)
}

// patch sends a PATCH /admin/settings with the given items.
func (h *harness) patch(items ...map[string]any) *httptest.ResponseRecorder {
	h.t.Helper()
	return h.do("PATCH", "/api/v1/admin/settings", h.admin, map[string]any{"items": items})
}

func set(ns, key, value string) map[string]any {
	return map[string]any{"namespace": ns, "key": key, "value": value}
}

func clearItem(ns, key string) map[string]any {
	return map[string]any{"namespace": ns, "key": key, "clear": true}
}

func (h *harness) mustPatch(items ...map[string]any) {
	h.t.Helper()
	w := h.patch(items...)
	if w.Code != 200 {
		h.t.Fatalf("patch failed: %d %s", w.Code, w.Body)
	}
}

func (h *harness) listAll() map[string][]item {
	h.t.Helper()
	w := h.do("GET", "/api/v1/admin/settings", h.admin, nil)
	testkit.Expect(h.t, w, 200, "")
	var out map[string][]item
	testkit.Decode(h.t, w, &out)
	return out
}

func (h *harness) find(ns, key string) item {
	h.t.Helper()
	for _, it := range h.listAll()[ns] {
		if it.Key == key {
			return it
		}
	}
	h.t.Fatalf("setting %s/%s not listed", ns, key)
	return item{}
}

// stored reads the raw column value straight from the table.
func (h *harness) stored(ns, key string) string {
	h.t.Helper()
	var rows []settings.SystemSetting
	if err := h.env.DB.Where("namespace = ? AND \"key\" = ?", ns, key).Find(&rows).Error; err != nil {
		h.t.Fatal(err)
	}
	if len(rows) != 1 {
		h.t.Fatalf("%s/%s: %d rows", ns, key, len(rows))
	}
	return rows[0].Value
}

func (h *harness) val(ns, key string) string { return h.find(ns, key).Value }

// details decodes the error details of a failed response.
func (h *harness) details(w *httptest.ResponseRecorder) map[string]any {
	h.t.Helper()
	d, _ := testkit.Envelope(h.t, w).Details.(map[string]any)
	if d == nil {
		h.t.Fatalf("no details: %s", w.Body)
	}
	return d
}

// expectInvalid asserts a 400 with the given code and rule (and param when not "-").
func (h *harness) expectInvalid(w *httptest.ResponseRecorder, code, field, rule, param string) {
	h.t.Helper()
	testkit.Expect(h.t, w, 400, code)
	d := h.details(w)
	if field != "" && d["field"] != field {
		h.t.Fatalf("field %v want %q: %s", d["field"], field, w.Body)
	}
	if d["rule"] != rule {
		h.t.Fatalf("rule %v want %q: %s", d["rule"], rule, w.Body)
	}
	if param != "-" {
		got, _ := d["param"].(string)
		if got != param {
			h.t.Fatalf("param %q want %q: %s", got, param, w.Body)
		}
	}
}

func fieldsOf(d map[string]any) []string {
	var out []string
	if raw, ok := d["fields"].([]any); ok {
		for _, f := range raw {
			out = append(out, f.(string))
		}
	}
	return out
}

// ---- fakes ---------------------------------------------------------------------------------

type fakeAudit struct {
	mu      sync.Mutex
	entries []settings.AuditEntry
}

func (f *fakeAudit) Record(_ context.Context, e settings.AuditEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
}

func (f *fakeAudit) all() []settings.AuditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]settings.AuditEntry(nil), f.entries...)
}

type fakeAdmins struct {
	ok  bool
	err error
}

func (f fakeAdmins) HasBoundSuperAdmin(context.Context) (bool, error) { return f.ok, f.err }

type fakeVectors struct {
	ok  bool
	err error
}

func (f fakeVectors) VectorStoreAvailable(context.Context) (bool, error) { return f.ok, f.err }

type fakeEmbedding struct {
	mu             sync.Mutex
	status         settings.EmbeddingStatus
	statusErr      error
	reindex        settings.ReindexOutcome
	reindexErr     error
	reindexFlag    *bool
	tasks          []settings.EmbeddingTask
	total          int64
	tasksErr       error
	lastQuery      settings.TaskQuery
	retry          settings.RetryOutcome
	retryErr       error
	lastRetry      []string
	reconcileCalls int
	reconcileErr   error
	probeErr       error
	probeCalls     int
	probeDeadline  time.Duration
}

func (f *fakeEmbedding) Status(context.Context) (settings.EmbeddingStatus, error) {
	return f.status, f.statusErr
}

func (f *fakeEmbedding) Reindex(_ context.Context, includeEmpty bool) (settings.ReindexOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reindexFlag = &includeEmpty
	return f.reindex, f.reindexErr
}

func (f *fakeEmbedding) Tasks(_ context.Context, q settings.TaskQuery) ([]settings.EmbeddingTask, int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastQuery = q
	return f.tasks, f.total, f.tasksErr
}

func (f *fakeEmbedding) Retry(_ context.Context, ids []string) (settings.RetryOutcome, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastRetry = ids
	return f.retry, f.retryErr
}

func (f *fakeEmbedding) Reconcile(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reconcileCalls++
	return f.reconcileErr
}

func (f *fakeEmbedding) Probe(ctx context.Context, _ string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.probeCalls++
	if dl, ok := ctx.Deadline(); ok {
		f.probeDeadline = time.Until(dl)
	}
	return f.probeErr
}

func (f *fakeEmbedding) reconciles() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.reconcileCalls
}

type fakeRuntime struct {
	status  settings.RuntimeStatus
	err     error
	actions []string
}

func (f *fakeRuntime) Status(context.Context) (settings.RuntimeStatus, error) { return f.status, f.err }

func (f *fakeRuntime) act(name string) (settings.RuntimeStatus, error) {
	f.actions = append(f.actions, name)
	st := f.status
	st.Status = name
	return st, f.err
}
func (f *fakeRuntime) Start(context.Context) (settings.RuntimeStatus, error) { return f.act("start") }
func (f *fakeRuntime) Stop(context.Context) (settings.RuntimeStatus, error)  { return f.act("stop") }
func (f *fakeRuntime) Restart(context.Context) (settings.RuntimeStatus, error) {
	return f.act("restart")
}

var errBoom = errors.New("boom")

func decodeMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	testkit.Decode(t, w, &m)
	return m
}

func toJSON(t *testing.T, v any) string {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func ctxBG() context.Context { return context.Background() }
