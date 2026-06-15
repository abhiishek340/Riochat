package channel

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// fakeClient is a scriptable protocol adapter.
type fakeClient struct {
	mu        sync.Mutex
	models    []RemoteModel
	listErr   error
	listCalls []UpstreamCall
	probeFn   func(call UpstreamCall, req ProbeRequest) (*ProbeExchange, error)
	probes    []UpstreamCall
	running   int
	maxRun    int
	gate      chan struct{}
}

func (f *fakeClient) ListModels(_ context.Context, call UpstreamCall) ([]RemoteModel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.listCalls = append(f.listCalls, call)
	if f.listErr != nil {
		return nil, f.listErr
	}
	return append([]RemoteModel(nil), f.models...), nil
}

func (f *fakeClient) Probe(_ context.Context, call UpstreamCall, req ProbeRequest) (*ProbeExchange, error) {
	f.mu.Lock()
	f.probes = append(f.probes, call)
	f.running++
	if f.running > f.maxRun {
		f.maxRun = f.running
	}
	fn, gate := f.probeFn, f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	defer func() {
		f.mu.Lock()
		f.running--
		f.mu.Unlock()
	}()
	if fn == nil {
		return &ProbeExchange{}, nil
	}
	return fn(call, req)
}

type fakeFetcher struct {
	mu    sync.Mutex
	body  []byte
	err   error
	calls int
	gate  chan struct{}
}

func (f *fakeFetcher) Fetch(ctx context.Context) ([]byte, error) {
	f.mu.Lock()
	f.calls++
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		select {
		case <-gate:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.body, f.err
}

func (f *fakeFetcher) set(body []byte, err error) {
	f.mu.Lock()
	f.body, f.err = body, err
	f.mu.Unlock()
}

func (f *fakeFetcher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls
}

type fakePricing struct {
	mode   string
	filter bool
	prices map[string]*Pricing
}

func (f *fakePricing) BillingMode(context.Context) string { return f.mode }
func (f *fakePricing) PublicPrices(context.Context) (map[string]*Pricing, bool, error) {
	return f.prices, f.filter, nil
}

type fakePersonal struct {
	models []PublicModel
	err    error
}

func (f *fakePersonal) PersonalModels(context.Context, uint) ([]PublicModel, error) {
	return f.models, f.err
}

type memStore struct {
	mu   sync.Mutex
	objs map[string][]byte
	// failDelete makes Delete fail.
	failDelete bool
}

func newMemStore() *memStore { return &memStore{objs: map[string][]byte{}} }

func (m *memStore) Put(_ context.Context, p, _ string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objs[p] = append([]byte(nil), data...)
	return nil
}

func (m *memStore) Get(_ context.Context, p string) ([]byte, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	b, ok := m.objs[p]
	if !ok {
		return nil, ErrObjectNotFound
	}
	return append([]byte(nil), b...), nil
}

func (m *memStore) Delete(_ context.Context, p string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.failDelete {
		return fmt.Errorf("delete refused")
	}
	delete(m.objs, p)
	return nil
}

func (m *memStore) has(p string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	_, ok := m.objs[p]
	return ok
}

type suite struct {
	t       *testing.T
	env     *testkit.Env
	svc     *Service
	client  *fakeClient
	fetcher *fakeFetcher
	pricing *fakePricing
	store   *memStore
	pers    *fakePersonal
}

// newSuite mounts the channel area (with a stand-in for the settings area's
// table) and returns a harness.
func newSuite(t *testing.T, tweak ...func(*Options)) *suite {
	t.Helper()
	s := &suite{t: t, client: &fakeClient{}, fetcher: &fakeFetcher{err: fmt.Errorf("offline")},
		pricing: &fakePricing{mode: BillingModeSelf}, store: newMemStore(), pers: &fakePersonal{}}
	opts := Options{
		Client: s.client, CatalogFetcher: s.fetcher, Pricing: s.pricing, IconStore: s.store, Personal: s.pers,
		OnReady: func(sv *Service) { s.svc = sv },
	}
	for _, f := range tweak {
		f(&opts)
	}
	feat := features.Feature{
		Name: "channel",
		Migrations: func() []migrate.Step {
			return append([]migrate.Step{migrate.AutoMigrate("test_settings_table", &settingRow{})}, Migrations()...)
		},
		Mount: func(d deps.Deps) error { return MountWith(d, opts) },
	}
	s.env = testkit.New(t, []features.Feature{feat})
	if s.svc == nil {
		t.Fatal("service not published")
	}
	return s
}

func (s *suite) adm() map[string]string { return s.env.Bearer(testkit.Admin(1)) }
func (s *suite) usr(id uint) map[string]string {
	return s.env.Bearer(testkit.User(id))
}

// do performs an admin request.
func (s *suite) do(method, path string, body any) *httptest.ResponseRecorder {
	s.t.Helper()
	return s.env.Do(method, path, s.adm(), body)
}

// ok performs an admin request that must answer 200 and returns the raw data.
func (s *suite) ok(method, path string, body any) json.RawMessage {
	s.t.Helper()
	w := s.do(method, path, body)
	testkit.Expect(s.t, w, 200, "")
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		s.t.Fatal(err)
	}
	return raw.Data
}

func (s *suite) expect(w *httptest.ResponseRecorder, status int, code string) {
	s.t.Helper()
	testkit.Expect(s.t, w, status, code)
}

func decodeInto[T any](t *testing.T, raw json.RawMessage) T {
	t.Helper()
	var v T
	if err := json.Unmarshal(raw, &v); err != nil {
		t.Fatalf("decode %s: %v", raw, err)
	}
	return v
}

type page[T any] struct {
	Total   int64 `json:"total"`
	Results []T   `json:"results"`
}

func keysDoc(keys ...string) string {
	type k struct {
		Key string `json:"key"`
	}
	var ks []k
	for _, x := range keys {
		ks = append(ks, k{x})
	}
	raw, _ := json.Marshal(map[string]any{"strategy": "failover", "keys": ks})
	return string(raw)
}

// mkUpstream creates an upstream through the API and returns its view.
func (s *suite) mkUpstream(name, compatible string, extra map[string]any) UpstreamView {
	s.t.Helper()
	body := map[string]any{"name": name, "baseURL": "https://api." + name + ".example.com/v1", "compatible": compatible,
		"apiKeys": keysDoc("sk-" + name + "-abcdefgh")}
	for k, v := range extra {
		body[k] = v
	}
	data := s.ok("POST", "/api/v1/admin/llm/upstreams", body)
	return decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](s.t, data).Upstream
}

// bind creates a binding through the API and returns the first resulting row.
func (s *suite) bind(upstreamID uint, platform, upstreamModel string, protocols []string, extra map[string]any) UpstreamModelRow {
	s.t.Helper()
	if protocols == nil {
		protocols = []string{}
	}
	body := map[string]any{"platformModelName": platform, "upstreamModelName": upstreamModel, "protocols": protocols}
	for k, v := range extra {
		body[k] = v
	}
	data := s.ok("POST", fmt.Sprintf("/api/v1/admin/llm/upstreams/%d/models", upstreamID), body)
	return decodeInto[struct {
		Binding UpstreamModelRow `json:"binding"`
	}](s.t, data).Binding
}

// mkChat creates an upstream, a chat binding and returns (upstream, row).
func (s *suite) mkChat(name, model string) (UpstreamView, UpstreamModelRow) {
	s.t.Helper()
	up := s.mkUpstream(name, "openai", nil)
	row := s.bind(up.ID, model, model, []string{ProtoOpenAIChat}, nil)
	return up, row
}

func (s *suite) routeIDs(modelID uint) []uint {
	var rs []ModelRoute
	if err := s.env.DB.Where("platform_model_id = ?", modelID).Order("id").Find(&rs).Error; err != nil {
		s.t.Fatal(err)
	}
	var ids []uint
	for _, r := range rs {
		ids = append(ids, r.ID)
	}
	return ids
}

func (s *suite) setBreaker(doc string) {
	s.t.Helper()
	s.ok("PATCH", "/api/v1/admin/llm/settings/circuit_breaker.defaults", map[string]string{"value": doc})
}

func pngBytes(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{uint8(x), uint8(y), 99, 255})
		}
	}
	var b bytes.Buffer
	_ = png.Encode(&b, img)
	return b.Bytes()
}

func multipartFile(t *testing.T, field, name string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	if field != "" {
		fw, err := mw.CreateFormFile(field, name)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = fw.Write(data)
	}
	_ = mw.Close()
	return &buf, mw.FormDataContentType()
}

func (s *suite) upload(data []byte) *httptest.ResponseRecorder {
	s.t.Helper()
	buf, ct := multipartFile(s.t, "file", "icon.png", data)
	h := s.adm()
	h["Content-Type"] = ct
	return s.env.Do("POST", "/api/v1/admin/llm/icon-assets", h, buf)
}

func (s *suite) mkModel(name string, extra map[string]any) ModelView {
	s.t.Helper()
	body := map[string]any{"platformModelName": name}
	for k, v := range extra {
		body[k] = v
	}
	data := s.ok("POST", "/api/v1/admin/llm/models", body)
	return decodeInto[struct {
		Model ModelView `json:"model"`
	}](s.t, data).Model
}

func errCode(s *suite, w *httptest.ResponseRecorder) string {
	return testkit.Envelope(s.t, w).ErrorCode
}

var _ = http.StatusOK

func decodeData[T any](t *testing.T, w *httptest.ResponseRecorder) T {
	t.Helper()
	var raw struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	return decodeInto[T](t, raw.Data)
}
