package personalprovider

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// fakeLister is a scripted upstream.
type fakeLister struct {
	mu    sync.Mutex
	fn    func(ListRequest) ([]string, error)
	calls []ListRequest
}

func (f *fakeLister) ListModels(_ context.Context, req ListRequest) ([]string, error) {
	f.mu.Lock()
	f.calls = append(f.calls, req)
	fn := f.fn
	f.mu.Unlock()
	if fn == nil {
		return []string{"a", "b"}, nil
	}
	return fn(req)
}

func (f *fakeLister) set(fn func(ListRequest) ([]string, error)) {
	f.mu.Lock()
	f.fn = fn
	f.mu.Unlock()
}

func (f *fakeLister) last() ListRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[len(f.calls)-1]
}

func (f *fakeLister) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

// fakeAudit records audit calls.
type fakeAudit struct {
	mu      sync.Mutex
	entries []auditEntry
}

type auditEntry struct {
	Action, Resource, ResourceID string
	Detail                       map[string]any
}

func (f *fakeAudit) RecordRequest(_ *gin.Context, action, resource, resourceID string, detail any) {
	f.mu.Lock()
	defer f.mu.Unlock()
	raw, _ := json.Marshal(detail)
	var m map[string]any
	_ = json.Unmarshal(raw, &m)
	f.entries = append(f.entries, auditEntry{action, resource, resourceID, m})
}

func (f *fakeAudit) all() []auditEntry {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]auditEntry(nil), f.entries...)
}

// fakeOwners is a scripted owner directory.
type fakeOwners struct {
	byID map[uint]Owner
	fail bool
}

func (f *fakeOwners) Lookup(_ context.Context, ids []uint) (map[uint]Owner, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	out := map[uint]Owner{}
	for _, id := range ids {
		if o, ok := f.byID[id]; ok {
			out[id] = o
		}
	}
	return out, nil
}

func (f *fakeOwners) SearchIDs(_ context.Context, q string) ([]uint, error) {
	if f.fail {
		return nil, context.DeadlineExceeded
	}
	q = strings.ToLower(q)
	var ids []uint
	for id, o := range f.byID {
		if strings.Contains(strings.ToLower(o.Username+" "+o.DisplayName+" "+o.Email), q) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

type harness struct {
	t      *testing.T
	env    *testkit.Env
	svc    *Service
	lister *fakeLister
	audit  *fakeAudit
	owners *fakeOwners
	// strict keeps the fake clock still between requests so the per-user probe
	// limiter can be exercised; otherwise every request advances the clock far
	// enough that tests never trip the limiter by accident.
	strict bool
}

type harnessOpts struct {
	realAudit bool
}

func newHarness(t *testing.T, ho ...harnessOpts) *harness {
	t.Helper()
	h := &harness{t: t, lister: &fakeLister{}, audit: &fakeAudit{}, owners: &fakeOwners{byID: map[uint]Owner{}}}
	opts := Options{Lister: h.lister, Owners: h.owners}
	list := []features.Feature{}
	if len(ho) > 0 && ho[0].realAudit {
		list = append(list, features.Feature{Name: "audit", Migrations: audit.Migrations, Mount: func(deps.Deps) error { return nil }})
	} else {
		opts.Audit = h.audit
	}
	list = append(list, features.Feature{
		Name:       "personalprovider",
		Migrations: Migrations,
		Mount: func(d deps.Deps) error {
			svc, err := MountWith(d, opts)
			h.svc = svc
			return err
		},
	})
	h.env = testkit.New(t, list)
	return h
}

func (h *harness) set(key, value string) {
	h.t.Helper()
	now := h.env.Clock.Now()
	res := h.env.DB.Model(&settingRow{}).Where("namespace = ? AND key = ?", SettingsNamespace, key).
		Updates(map[string]any{"value": value, "updated_at": now})
	if res.Error != nil {
		h.t.Fatal(res.Error)
	}
	if res.RowsAffected == 0 {
		row := settingRow{Namespace: SettingsNamespace, Key: key, Value: value, ValueType: "string", CreatedAt: now, UpdatedAt: now}
		if err := h.env.DB.Create(&row).Error; err != nil {
			h.t.Fatal(err)
		}
	}
}

func (h *harness) enable() *harness {
	h.t.Helper()
	h.set(SettingEnabled, "true")
	return h
}

func (h *harness) user(id uint) map[string]string  { return h.env.Bearer(testkit.User(id)) }
func (h *harness) admin(id uint) map[string]string { return h.env.Bearer(testkit.Admin(id)) }

func (h *harness) do(method, path string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	h.t.Helper()
	w := h.env.Do(method, "/api/v1"+path, hdr, body)
	if !h.strict {
		h.env.Clock.Advance(7 * time.Second)
	}
	return w
}

const mp = "/me/model-providers"

type providerJSON struct {
	ID            string
	Name          string
	Icon          string
	Protocol      string
	BaseURL       string
	Host          string
	KeyHint       string
	Models        []EnabledModel
	Status        string
	Source        string
	LastError     string
	LastCheckedAt *string
	CreatedAt     string
	UpdatedAt     string
}

func (h *harness) createBody(extra map[string]any) map[string]any {
	body := map[string]any{
		"protocol": "openai_chat_completions",
		"baseURL":  "https://api.example.com/v1",
		"apiKey":   "sk-abcdefghijWXYZ",
	}
	for k, v := range extra {
		body[k] = v
	}
	return body
}

// create posts a provider as user uid and returns it, failing on any error.
func (h *harness) create(uid uint, extra map[string]any) providerJSON {
	h.t.Helper()
	w := h.do("POST", mp, h.user(uid), h.createBody(extra))
	testkit.Expect(h.t, w, 200, "")
	var out struct{ Provider providerJSON }
	testkit.Decode(h.t, w, &out)
	return out.Provider
}

func (h *harness) rows() []Provider {
	var rows []Provider
	if err := h.env.DB.Order("id ASC").Find(&rows).Error; err != nil {
		h.t.Fatal(err)
	}
	return rows
}
