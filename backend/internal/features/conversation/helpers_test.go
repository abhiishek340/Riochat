package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ----- fake collaborators -----

type fakeGen struct {
	mu    sync.Mutex
	fn    func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error)
	calls []GenerateRequest
}

func (g *fakeGen) Generate(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
	g.mu.Lock()
	g.calls = append(g.calls, req)
	fn := g.fn
	g.mu.Unlock()
	if fn != nil {
		return fn(ctx, req, emit)
	}
	return defaultGenerate(ctx, req, emit)
}

func (g *fakeGen) set(fn func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error)) {
	g.mu.Lock()
	g.fn = fn
	g.mu.Unlock()
}

func (g *fakeGen) requests() []GenerateRequest {
	g.mu.Lock()
	defer g.mu.Unlock()
	return append([]GenerateRequest(nil), g.calls...)
}

// lastChat is the newest chat request; background title and label requests are
// recorded too and may arrive after it.
func (g *fakeGen) lastChat() GenerateRequest {
	reqs := g.requests()
	for i := len(reqs) - 1; i >= 0; i-- {
		if reqs[i].Task == taskChat {
			return reqs[i]
		}
	}
	return GenerateRequest{}
}

func (g *fakeGen) tasks() []string {
	var out []string
	for _, r := range g.requests() {
		out = append(out, r.Task)
	}
	return out
}

func lastUserText(req GenerateRequest) string {
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == roleUser {
			return req.Messages[i].Content
		}
	}
	return ""
}

func defaultGenerate(_ context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
	if req.Task == taskTitle || req.Task == taskLabels {
		return GenerateResult{Content: `{"title":"Generated Title"}`, Usage: Usage{Input: 1, Output: 1}}, nil
	}
	emit(GenerateEvent{Type: "delta", Delta: "Hello "})
	emit(GenerateEvent{Type: "delta", Delta: "world"})
	return GenerateResult{
		Content: "Hello world to: " + lastUserText(req),
		Usage:   Usage{Input: 10, Output: 5},
		Route:   RouteInfo{PlatformModelName: "plat-model", RequestedModelName: req.Model, UpstreamModelName: "up-model", ModelVendor: "vend", ModelIcon: "icon"},
	}, nil
}

type fakeBilling struct {
	mu       sync.Mutex
	reserveE error
	settleE  error
	reserved int
	settled  int
	released int
	basic    []BasicServiceCharge
	basicErr error
}

func (b *fakeBilling) Reserve(_ context.Context, _ ReserveRequest) (Reservation, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.reserveE != nil {
		return Reservation{}, b.reserveE
	}
	b.reserved++
	return Reservation{ID: "r", Mode: "balance"}, nil
}

func (b *fakeBilling) Settle(_ context.Context, _ Reservation, req SettleRequest) (Settlement, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.settleE != nil {
		return Settlement{}, b.settleE
	}
	b.settled++
	return Settlement{Mode: "balance", Currency: "USD", Nanousd: 2_000_000_000, PricingSnapshotJSON: `{"billingMode":"balance"}`}, nil
}

func (b *fakeBilling) Release(context.Context, Reservation) {
	b.mu.Lock()
	b.released++
	b.mu.Unlock()
}

func (b *fakeBilling) ChargeBasicService(_ context.Context, c BasicServiceCharge) error {
	b.mu.Lock()
	b.basic = append(b.basic, c)
	b.mu.Unlock()
	return b.basicErr
}

type fakeModeration struct {
	mu    sync.Mutex
	block map[string]bool // direction -> blocked
	seen  []ModerationRequest
}

func (m *fakeModeration) Check(_ context.Context, r ModerationRequest) (ModerationVerdict, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.seen = append(m.seen, r)
	if m.block[r.Direction] {
		return ModerationVerdict{Blocked: true, EventID: "evt_1", Categories: []string{"violence"}}, nil
	}
	return ModerationVerdict{}, nil
}

type fakeSettings struct {
	mu    sync.Mutex
	rs    RuntimeSettings
	users map[string]string
}

func (f *fakeSettings) Runtime(context.Context) RuntimeSettings {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rs
}

func (f *fakeSettings) UserSetting(_ context.Context, uid uint, key string) (string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	v, ok := f.users[fmt.Sprintf("%d:%s", uid, key)]
	return v, ok, nil
}

func (f *fakeSettings) mutate(fn func(*RuntimeSettings)) {
	f.mu.Lock()
	fn(&f.rs)
	f.mu.Unlock()
}

type fakeCatalog struct {
	mu        sync.Mutex
	available map[string]bool
}

func (c *fakeCatalog) ChatModelAvailable(_ context.Context, _ uint, model string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.available[model]
}

type fakeAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (a *fakeAudit) Record(_ context.Context, e AuditEntry) error {
	a.mu.Lock()
	a.entries = append(a.entries, e)
	a.mu.Unlock()
	return nil
}

func (a *fakeAudit) actions() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	var out []string
	for _, e := range a.entries {
		out = append(out, e.Action)
	}
	return out
}

func (a *fakeAudit) find(action string) *AuditEntry {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i := range a.entries {
		if a.entries[i].Action == action {
			e := a.entries[i]
			return &e
		}
	}
	return nil
}

type fakeCaps struct {
	mu       sync.Mutex
	toolErr  error
	skillErr error
	kbErr    error
	inUse    map[string]bool
}

func (c *fakeCaps) ValidateMCPTools(context.Context, uint, []uint) error { return c.toolErr }
func (c *fakeCaps) ValidateSkills(context.Context, uint, []uint) error   { return c.skillErr }
func (c *fakeCaps) ValidateKnowledgeBases(context.Context, uint, []string) error {
	return c.kbErr
}

func (c *fakeCaps) FileInUse(_ context.Context, _ uint, id string) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.inUse[id], nil
}

// ----- environment -----

type tenv struct {
	*testkit.Env
	gen      *fakeGen
	bill     *fakeBilling
	mod      *fakeModeration
	set      *fakeSettings
	cat      *fakeCatalog
	audit    *fakeAudit
	caps     *fakeCaps
	u1, u2   map[string]string
	admin    map[string]string
	t        *testing.T
	basePath string
	svc      *service
}

func newEnv(t *testing.T, opts ...testkit.Option) *tenv {
	t.Helper()
	return newEnvTimed(t, nil, opts...)
}

// newEnvTimed builds the environment with adjusted lifecycle intervals.
func newEnvTimed(t *testing.T, tweak func(*timings), opts ...testkit.Option) *tenv {
	t.Helper()
	return newEnvPorts(t, tweak, nil, opts...)
}

// newEnvPorts also lets a test adjust the collaborators before mounting.
func newEnvPorts(t *testing.T, tweak func(*timings), portsTweak func(*Ports, *tenv), opts ...testkit.Option) *tenv {
	t.Helper()
	te := &tenv{
		gen:   &fakeGen{},
		bill:  &fakeBilling{},
		mod:   &fakeModeration{block: map[string]bool{}},
		set:   &fakeSettings{rs: DefaultRuntimeSettings(), users: map[string]string{}},
		cat:   &fakeCatalog{available: map[string]bool{"gpt-4o": true, "claude-sonnet": true, "plat-model": true}},
		audit: &fakeAudit{},
		caps:  &fakeCaps{inUse: map[string]bool{}},
		t:     t, basePath: "/api/v1",
	}
	ports := Ports{
		Generator: te.gen, Billing: te.bill, Moderation: te.mod, Catalog: te.cat,
		Settings: te.set, Auditor: te.audit, Capabilities: te.caps,
	}
	if portsTweak != nil {
		portsTweak(&ports, te)
	}
	mount := func(d deps.Deps) error {
		s := newService(d, ports)
		if tweak != nil {
			tweak(&s.timing)
		}
		te.svc = s
		registerRoutes(d, &api{s: s})
		return nil
	}
	te.Env = testkit.New(t, []features.Feature{{Name: "conversation", Migrations: Migrations, Mount: mount}}, opts...)
	te.u1 = te.Bearer(testkit.User(1))
	te.u2 = te.Bearer(testkit.User(2))
	te.admin = te.Bearer(testkit.Admin(99))
	return te
}

func localOpt() testkit.Option {
	return testkit.WithConfig(func(c *config.Config) {
		c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"}
	})
}

// call performs a request against /api/v1.
func (e *tenv) call(method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.Do(method, e.basePath+path, hdr, body)
}

func (e *tenv) ok(method, path string, hdr map[string]string, body any) map[string]any {
	e.t.Helper()
	w := e.call(method, path, hdr, body)
	testkit.Expect(e.t, w, 200, "")
	return dataMap(e.t, w)
}

func (e *tenv) expectErr(method, path string, hdr map[string]string, body any, status int, code string) {
	e.t.Helper()
	w := e.call(method, path, hdr, body)
	testkit.Expect(e.t, w, status, code)
}

func dataMap(t *testing.T, w *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	testkit.Decode(t, w, &m)
	return m
}

func dataList(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var m []map[string]any
	testkit.Decode(t, w, &m)
	return m
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func num(m map[string]any, k string) float64 {
	v, _ := m[k].(float64)
	return v
}

func results(t *testing.T, w *httptest.ResponseRecorder) (int, []map[string]any) {
	t.Helper()
	var p struct {
		Total   float64          `json:"total"`
		Results []map[string]any `json:"results"`
	}
	testkit.Decode(t, w, &p)
	return int(p.Total), p.Results
}

func (e *tenv) newConv(hdr map[string]string, title, model string) string {
	e.t.Helper()
	body := map[string]string{}
	if title != "" {
		body["title"] = title
	}
	if model != "" {
		body["model"] = model
	}
	m := e.ok("POST", "/conversations", hdr, body)
	return str(m, "publicID")
}

// sendBody builds a send request body.
func sendBody(content string, extra map[string]any) map[string]any {
	b := map[string]any{"contentType": "text", "content": content}
	for k, v := range extra {
		b[k] = v
	}
	return b
}

// send posts a non-streaming message and returns the result.
func (e *tenv) send(hdr map[string]string, conv, content string, extra map[string]any) map[string]any {
	e.t.Helper()
	return e.ok("POST", "/conversations/"+conv+"/messages", hdr, sendBody(content, extra))
}

func pair(m map[string]any) (user, asst map[string]any) {
	user, _ = m["userMessage"].(map[string]any)
	asst, _ = m["assistantMessage"].(map[string]any)
	return
}

// ndjson parses a response body into JSON lines.
func ndjson(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(w.Body.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad ndjson line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func types(lines []map[string]any) []string {
	var out []string
	for _, l := range lines {
		out = append(out, str(l, "type"))
	}
	return out
}

// seedMsg inserts a message row directly.
func (e *tenv) seedMsg(conv *Conversation, role, content, status string, parent *Message) *Message {
	e.t.Helper()
	m := &Message{
		ConversationID: conv.ID, UserID: conv.UserID, PublicID: newPublicID(), Role: role, ContentType: "text",
		Content: content, BranchReason: branchDefault, Status: status, BilledCurrency: "USD",
		ModerationCategoriesJSON: "[]", KnowledgeSourcesJSON: "[]",
	}
	if parent != nil {
		id := parent.ID
		m.ParentMessageID = &id
	}
	if err := e.DB.Create(m).Error; err != nil {
		e.t.Fatalf("seed message: %v", err)
	}
	e.DB.Model(&Conversation{}).Where("id = ?", conv.ID).UpdateColumn("message_count", gorm.Expr("message_count + 1"))
	return m
}

func (e *tenv) convRow(pub string) *Conversation {
	e.t.Helper()
	var c Conversation
	if err := e.DB.Where("public_id = ?", pub).First(&c).Error; err != nil {
		e.t.Fatalf("conversation row %s: %v", pub, err)
	}
	return &c
}

func (e *tenv) msgRow(pub string) *Message {
	e.t.Helper()
	var m Message
	if err := e.DB.Where("public_id = ?", pub).First(&m).Error; err != nil {
		e.t.Fatalf("message row %s: %v", pub, err)
	}
	return &m
}

// upload sends a multipart file upload.
func (e *tenv) upload(hdr map[string]string, name, mime string, data []byte) *httptest.ResponseRecorder {
	e.t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(map[string][]string)
	h["Content-Disposition"] = []string{fmt.Sprintf(`form-data; name="file"; filename="%s"`, name)}
	h["Content-Type"] = []string{mime}
	part, _ := mw.CreatePart(h)
	_, _ = part.Write(data)
	_ = mw.Close()
	hh := map[string]string{"Content-Type": mw.FormDataContentType()}
	for k, v := range hdr {
		hh[k] = v
	}
	return e.Do(http.MethodPost, e.basePath+"/files", hh, buf.Bytes())
}

func userClaims(id uint) testkit.Claims { return testkit.User(id) }

// eventually polls cond for up to three seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// syncRecorder is a response recorder that can be read while a handler still
// writes to it (streaming tests).
type syncRecorder struct {
	mu     sync.Mutex
	header http.Header
	body   bytes.Buffer
	code   int
}

func newSyncRecorder() *syncRecorder { return &syncRecorder{header: http.Header{}} }

func (r *syncRecorder) Header() http.Header { return r.header }
func (r *syncRecorder) Write(b []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.code == 0 {
		r.code = 200
	}
	return r.body.Write(b)
}
func (r *syncRecorder) WriteHeader(c int) {
	r.mu.Lock()
	if r.code == 0 {
		r.code = c
	}
	r.mu.Unlock()
}
func (r *syncRecorder) Flush() {}
func (r *syncRecorder) text() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

// stream starts a request in the background with a cancellable context.
func (e *tenv) stream(ctx context.Context, method, path string, hdr map[string]string, body any) (*syncRecorder, <-chan struct{}) {
	e.t.Helper()
	var rd io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		rd = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, e.basePath+path, rd).WithContext(ctx)
	req.RemoteAddr = "203.0.113.5:4321"
	if rd != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := newSyncRecorder()
	done := make(chan struct{})
	go func() {
		defer close(done)
		e.Engine.ServeHTTP(rec, req)
	}()
	return rec, done
}

func waitDone(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("request did not finish")
	}
}

func parseLines(t *testing.T, text string) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(text), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("bad ndjson line %q: %v", line, err)
		}
		out = append(out, m)
	}
	return out
}

func jsonUnmarshal(s string, v any) error { return json.Unmarshal([]byte(s), v) }

func (b *fakeBilling) charges() []BasicServiceCharge {
	b.mu.Lock()
	defer b.mu.Unlock()
	return append([]BasicServiceCharge(nil), b.basic...)
}

func init() {
	// Tests expect an upload to be fully processed when the request returns.
	inlineProcessing.Store(true)
}
