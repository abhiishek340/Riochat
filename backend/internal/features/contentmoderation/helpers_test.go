package contentmoderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/testkit"
)

var superClaims = testkit.Claims{UserID: 1, Username: "root", Role: "superadmin", SessionID: 1}

// ---- fake provider -----------------------------------------------------------

type fakeProvider struct {
	mu         sync.Mutex
	textCalls  []string
	imageCalls [][]Image
	text       func(text string, scope Scope) (*Response, error)
	image      func(images []Image, scope Scope) (*Response, error)
}

func (f *fakeProvider) CheckText(ctx context.Context, cc ClientConfig, text string, scope Scope) (*Response, error) {
	f.mu.Lock()
	f.textCalls = append(f.textCalls, text)
	fn := f.text
	f.mu.Unlock()
	if fn != nil {
		return fn(text, scope)
	}
	return cleanResp("fake-model"), nil
}

func (f *fakeProvider) CheckImages(ctx context.Context, cc ClientConfig, images []Image, scope Scope) (*Response, error) {
	f.mu.Lock()
	f.imageCalls = append(f.imageCalls, images)
	fn := f.image
	f.mu.Unlock()
	if fn != nil {
		return fn(images, scope)
	}
	return cleanResp("fake-model"), nil
}

func (f *fakeProvider) counts() (int, int) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.textCalls), len(f.imageCalls)
}

func cleanResp(model string) *Response {
	cats := map[string]bool{}
	for _, c := range textCategories {
		cats[c] = false
	}
	return &Response{Model: model, Results: []ModerationResult{{Categories: cats, CategoryScores: map[string]float64{}}}}
}

func hitResp(model string, hits ...string) *Response {
	r := cleanResp(model)
	r.Results[0].Flagged = true
	for _, h := range hits {
		r.Results[0].Categories[h] = true
		r.Results[0].CategoryScores[h] = 0.9
	}
	return r
}

// ---- fake collaborators ----------------------------------------------------------

type memRuns struct {
	mu        sync.Mutex
	states    map[string]string
	stateLog  []string
	blocks    []BlockSpec
	failApply int
	fileIDs   []uint
	stale     []RunRef
	eventIDs  map[string]string
}

func (m *memRuns) eventID(runID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.eventIDs[runID]
}

func newMemRuns() *memRuns { return &memRuns{states: map[string]string{}} }

func (m *memRuns) SetRunState(_ context.Context, runID, state, eventID, cats string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.states[runID] = state
	if eventID != "" {
		if m.eventIDs == nil {
			m.eventIDs = map[string]string{}
		}
		m.eventIDs[runID] = eventID
	}
	m.stateLog = append(m.stateLog, runID+":"+state)
	return nil
}

func (m *memRuns) ApplyBlock(_ context.Context, spec BlockSpec) ([]uint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failApply > 0 {
		m.failApply--
		return nil, errors.New("db down")
	}
	m.blocks = append(m.blocks, spec)
	m.states[spec.RunID] = StateBlocked
	return m.fileIDs, nil
}

func (m *memRuns) StaleRuns(context.Context, time.Time) ([]RunRef, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]RunRef(nil), m.stale...), nil
}

func (m *memRuns) state(runID string) string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.states[runID]
}

func (m *memRuns) blockCount() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.blocks)
}

type memFiles struct {
	mu        sync.Mutex
	data      map[uint][]byte
	mimes     map[uint]string
	revoked   []uint
	deleted   []uint
	retryRuns int
}

func newMemFiles() *memFiles {
	return &memFiles{data: map[uint][]byte{}, mimes: map[uint]string{}}
}

func (m *memFiles) put(id uint, data []byte, mime string) {
	m.mu.Lock()
	m.data[id], m.mimes[id] = data, mime
	m.mu.Unlock()
}

func (m *memFiles) Load(_ context.Context, id uint) ([]byte, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.data[id]
	if !ok {
		return nil, "", ErrFileNotFound
	}
	return d, m.mimes[id], nil
}

func (m *memFiles) Revoke(_ context.Context, id uint) error {
	m.mu.Lock()
	m.revoked = append(m.revoked, id)
	m.mu.Unlock()
	return nil
}

func (m *memFiles) DeleteObjects(_ context.Context, ids []uint) error {
	m.mu.Lock()
	m.deleted = append(m.deleted, ids...)
	m.mu.Unlock()
	return nil
}

func (m *memFiles) RetryBlockedDeletes(context.Context, int) error {
	m.mu.Lock()
	m.retryRuns++
	m.mu.Unlock()
	return nil
}

type memObjects struct {
	mu      sync.Mutex
	objects map[string][]byte
	types   map[string]string
	failDel bool
	failPut func(key string) bool
}

func newMemObjects() *memObjects {
	return &memObjects{objects: map[string][]byte{}, types: map[string]string{}}
}

func (m *memObjects) Put(_ context.Context, key string, data []byte, ct string) error {
	m.mu.Lock()
	if m.failPut != nil && m.failPut(key) {
		m.mu.Unlock()
		return errors.New("store unavailable")
	}
	m.objects[key], m.types[key] = append([]byte(nil), data...), ct
	m.mu.Unlock()
	return nil
}

func (m *memObjects) Get(_ context.Context, key string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	d, ok := m.objects[key]
	if !ok {
		return nil, errors.New("object missing")
	}
	return d, nil
}

func (m *memObjects) Delete(_ context.Context, key string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDel {
		return errors.New("store unavailable")
	}
	delete(m.objects, key)
	return nil
}

func (m *memObjects) count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.objects)
}

type memAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
	fail    bool
}

func (m *memAudit) Record(_ context.Context, e AuditEntry) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.fail {
		return errors.New("audit down")
	}
	m.entries = append(m.entries, e)
	return nil
}

func (m *memAudit) list() []AuditEntry {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([]AuditEntry(nil), m.entries...)
}

type memUsers struct {
	labels map[uint]UserLabel
	names  map[string]uint // lower-cased username -> id
}

func (m *memUsers) Labels(_ context.Context, ids []uint) (map[uint]UserLabel, error) {
	out := map[uint]UserLabel{}
	for _, id := range ids {
		if l, ok := m.labels[id]; ok {
			out[id] = l
		}
	}
	return out, nil
}

func (m *memUsers) Search(_ context.Context, q string) ([]uint, error) {
	if id, ok := m.names[lowerASCII(q)]; ok {
		return []uint{id}, nil
	}
	return nil, nil
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

type memNotifier struct {
	mu        sync.Mutex
	events    []StreamEvent
	discarded []string
}

func (m *memNotifier) Emit(_ context.Context, runID string, ev StreamEvent) {
	m.mu.Lock()
	m.events = append(m.events, ev)
	m.mu.Unlock()
}

func (m *memNotifier) DiscardRetained(_ context.Context, runID string) {
	m.mu.Lock()
	m.discarded = append(m.discarded, runID)
	m.mu.Unlock()
}

func (m *memNotifier) types() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []string
	for _, e := range m.events {
		out = append(out, e.Type)
	}
	return out
}

func (m *memNotifier) last() StreamEvent {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return StreamEvent{}
	}
	return m.events[len(m.events)-1]
}

// ---- environment -------------------------------------------------------------

type harness struct {
	t        *testing.T
	env      *testkit.Env
	svc      *Service
	prov     *fakeProvider
	runs     *memRuns
	files    *memFiles
	objects  *memObjects
	audit    *memAudit
	notifier *memNotifier
	users    *memUsers
}

func newHarness(t *testing.T, opts ...testkit.Option) *harness {
	t.Helper()
	h := &harness{
		t: t, prov: &fakeProvider{}, runs: newMemRuns(), files: newMemFiles(), objects: newMemObjects(),
		audit: &memAudit{}, notifier: &memNotifier{},
		users: &memUsers{labels: map[uint]UserLabel{}, names: map[string]uint{}},
	}
	ports := Ports{
		Provider: h.prov, Runs: h.runs, Files: h.files, Objects: h.objects,
		Audit: h.audit, Notifier: h.notifier, Users: h.users,
	}
	feature := features.Feature{Name: "contentmoderation", Migrations: Migrations, Mount: func(d deps.Deps) error {
		s, err := MountWith(d, ports)
		h.svc = s
		return err
	}}
	h.env = testkit.New(t, []features.Feature{feature}, opts...)
	return h
}

func (h *harness) super() map[string]string { return h.env.Bearer(superClaims) }

func (h *harness) do(method, path string, body any) *httptest.ResponseRecorder {
	return h.env.Do(method, "/api/v1/admin/content-moderation"+path, h.super(), body)
}

// policy used by most tests.
func defaultPolicyBody() map[string]any {
	return map[string]any{
		"inputTextCategories":   []string{"violence", "hate"},
		"outputTextCategories":  []string{"violence", "hate"},
		"inputImageCategories":  []string{"sexual"},
		"outputImageCategories": []string{"sexual"},
	}
}

// enable stores a working configuration through the API.
func (h *harness) enable(extra map[string]any) {
	h.t.Helper()
	body := map[string]any{
		"enabled": true, "baseUrl": "https://moderation.example.test", "apiKey": "sk-test-key-123456",
		"model": "m-test", "policy": defaultPolicyBody(),
	}
	for k, v := range extra {
		body[k] = v
	}
	w := h.do("PUT", "/config", body)
	testkit.Expect(h.t, w, 200, "")
}

func (h *harness) begin(runID string, cancel func()) *Run {
	return h.svc.BeginRun(context.Background(), RunInfo{
		UserID: 5, ConversationID: 9, RunID: runID, MessageID: 11, MessagePublicID: "msg_" + runID,
	}, RunOptions{CancelGeneration: cancel})
}

func (h *harness) eventsFor(runID string) []Event {
	var rows []Event
	if err := h.env.DB.Where("run_id = ?", runID).Order("id ASC").Find(&rows).Error; err != nil {
		h.t.Fatalf("events: %v", err)
	}
	return rows
}

func (h *harness) allEvents() []Event {
	var rows []Event
	if err := h.env.DB.Order("id ASC").Find(&rows).Error; err != nil {
		h.t.Fatalf("events: %v", err)
	}
	return rows
}

func (h *harness) stat(direction, modality, result, category string) DailyStat {
	var s DailyStat
	err := h.env.DB.Where("direction = ? AND modality = ? AND result = ? AND category = ?", direction, modality, result, category).First(&s).Error
	if err != nil {
		return DailyStat{}
	}
	return s
}

// ---- images -------------------------------------------------------------------

func pngBytes(t testing.TB, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 120, 255})
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func b64(b []byte) string { return base64.StdEncoding.EncodeToString(b) }

func codeOf(err error) string {
	if te, ok := apperr.Find(err); ok {
		return te.Code
	}
	return fmt.Sprint(err)
}

// roundTripper turns an http.Handler into an http client for provider tests.
type handlerDoer struct{ h http.Handler }

func (d handlerDoer) Do(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	d.h.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func testkitExpect(t testing.TB, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	testkit.Expect(t, w, status, code)
}
