package billing

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- fakes for the ports ----

type fakeAudit struct {
	mu      sync.Mutex
	entries []AuditEntry
}

func (f *fakeAudit) Record(_ context.Context, e AuditEntry) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.entries = append(f.entries, e)
}

func (f *fakeAudit) actions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, e := range f.entries {
		out = append(out, e.Action)
	}
	return out
}

func (f *fakeAudit) last(action string) (AuditEntry, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.entries) - 1; i >= 0; i-- {
		if f.entries[i].Action == action {
			return f.entries[i], true
		}
	}
	return AuditEntry{}, false
}

type fakeCatalog struct{ models map[string]ModelInfo }

func (f *fakeCatalog) Model(_ context.Context, name string) (ModelInfo, bool, error) {
	m, ok := f.models[name]
	return m, ok, nil
}

func (f *fakeCatalog) ActiveModels(context.Context) ([]ModelInfo, error) {
	var out []ModelInfo
	for _, m := range f.models {
		out = append(out, m)
	}
	return out, nil
}

type fakeRates struct{ percent map[string]int }

func (f *fakeRates) RatePercent(_ context.Context, _ uint, model string, _ uint) (int, error) {
	if p, ok := f.percent[model]; ok {
		return p, nil
	}
	return 100, nil
}

type fakeGroups struct {
	def     uint
	exist   map[uint]bool
	members map[uint]GroupMembers
}

func (f *fakeGroups) DefaultGroupID(context.Context) (uint, bool, error) {
	return f.def, f.def != 0, nil
}
func (f *fakeGroups) GroupExists(_ context.Context, id uint) (bool, error) {
	return f.exist[id], nil
}
func (f *fakeGroups) Members(_ context.Context, id uint) (GroupMembers, error) {
	if m, ok := f.members[id]; ok {
		return m, nil
	}
	return GroupMembers{Exists: f.exist[id]}, nil
}

type fakeUsers struct{ users map[uint]UserInfo }

func (f *fakeUsers) Users(_ context.Context, ids []uint) (map[uint]UserInfo, error) {
	out := map[uint]UserInfo{}
	for _, id := range ids {
		if u, ok := f.users[id]; ok {
			out[id] = u
		}
	}
	return out, nil
}

// ---- test environment ----

type env struct {
	*testkit.Env
	t        *testing.T
	svc      *Service
	settings *MemorySettings
	audit    *fakeAudit
	catalog  *fakeCatalog
	rates    *fakeRates
	groups   *fakeGroups
	users    *fakeUsers
}

type envOpt func(*envCfg)

type envCfg struct {
	ports   Ports
	kitOpts []testkit.Option
	seed    map[string]string
}

func withSeed(m map[string]string) envOpt { return func(c *envCfg) { c.seed = m } }
func withPorts(f func(*Ports)) envOpt     { return func(c *envCfg) { f(&c.ports) } }
func withKit(o testkit.Option) envOpt     { return func(c *envCfg) { c.kitOpts = append(c.kitOpts, o) } }

func newEnv(t *testing.T, opts ...envOpt) *env {
	t.Helper()
	cfg := &envCfg{seed: map[string]string{}}
	for _, o := range opts {
		o(cfg)
	}
	e := &env{t: t, audit: &fakeAudit{}, rates: &fakeRates{percent: map[string]int{}},
		groups:  &fakeGroups{def: 1, exist: map[uint]bool{1: true, 2: true}, members: map[uint]GroupMembers{1: {Exists: true, Everyone: true}}},
		users:   &fakeUsers{users: map[uint]UserInfo{}},
		catalog: &fakeCatalog{models: map[string]ModelInfo{}}}
	e.settings = NewMemorySettings(cfg.seed)
	ports := cfg.ports
	ports.Settings, ports.Audit, ports.Rates, ports.Groups, ports.Users = e.settings, e.audit, e.rates, e.groups, e.users
	if ports.Models == nil {
		ports.Models = nil // standalone catalog unless a test wires one
	}
	ports.Location = time.UTC
	feat := features.Feature{Name: "billing", Migrations: Migrations, Mount: MountWith(ports, func(s *Service) { e.svc = s })}
	e.Env = testkit.New(t, []features.Feature{feat}, cfg.kitOpts...)
	return e
}

func (e *env) setSettings(kv map[string]string) {
	e.t.Helper()
	if err := e.settings.UpdateBilling(context.Background(), kv); err != nil {
		e.t.Fatalf("settings: %v", err)
	}
}

func (e *env) mode(m string) { e.t.Helper(); e.setSettings(map[string]string{KeyMode: m}) }

func (e *env) user(id uint) map[string]string  { return e.Bearer(testkit.User(id)) }
func (e *env) admin(id uint) map[string]string { return e.Bearer(testkit.Admin(id)) }

func (e *env) do(method, target string, hdr map[string]string, body any) *httptest.ResponseRecorder {
	e.t.Helper()
	return e.Do(method, target, hdr, body)
}

func localEnv(t *testing.T, opts ...envOpt) *env {
	return newEnv(t, append(opts, withKit(testkit.WithConfig(func(c *config.Config) {
		c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"}
	})))...)
}

// price inserts a model price directly.
func (e *env) price(row ModelPrice) {
	e.t.Helper()
	if row.Currency == "" {
		row.Currency = CurrencyUSD
	}
	if row.TieredPricingJSON == "" {
		row.TieredPricingJSON = "{}"
	}
	if row.SchedulePricingJSON == "" {
		row.SchedulePricingJSON = "{}"
	}
	if err := e.DB.Create(&row).Error; err != nil {
		e.t.Fatalf("price: %v", err)
	}
}

func (e *env) fund(userID uint, usd float64) {
	e.t.Helper()
	acc, err := ensureAccount(e.DB, userID)
	if err != nil {
		e.t.Fatal(err)
	}
	e.DB.Model(&Account{}).Where("id = ?", acc.ID).Update("balance_nanousd", USDToNano(usd))
}

func (e *env) balance(userID uint) int64 {
	e.t.Helper()
	var acc Account
	if err := e.DB.Where("user_id = ?", userID).First(&acc).Error; err != nil {
		return 0
	}
	return acc.BalanceNanousd
}

func okStatus(t *testing.T, w *httptest.ResponseRecorder) {
	t.Helper()
	if w.Code != http.StatusOK {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
}
