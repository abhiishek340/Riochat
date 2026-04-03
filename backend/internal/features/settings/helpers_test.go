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
