package settings

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Item is one setting as the administrator API shows it. Sensitive values are
// never returned: Value is empty and Configured tells whether one is stored.
type Item struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	ValueType   string `json:"valueType"`
	Description string `json:"description"`
	Sensitive   bool   `json:"sensitive"`
	Configured  bool   `json:"configured"`
}

// settingsCacheTTL is the expiry of the auxiliary per-key cache.
const settingsCacheTTL = time.Minute

// Service owns system settings: storage, validation, the runtime projection and
// the user preference store. All methods are safe for concurrent use.
type Service struct {
	db      *gorm.DB
	repo    repo
	users   userRepo
	secrets *secretbox.KeyRing
	cache   cache.Cache
	clock   clock.Clock
	log     *logx.Logger
	cfg     *config.Holder
	caps    capability.Provider

	rt      atomic.Pointer[Runtime]
	writeMu sync.Mutex

	portsMu sync.RWMutex
	ports   Ports
}

// newService builds a service from the area dependencies.
func newService(d deps.Deps, p Ports) *Service {
	s := &Service{
		db: d.DB, repo: repo{d.DB}, users: userRepo{d.DB},
		secrets: d.Secrets, cache: d.Cache, clock: d.Clock, log: d.Logger,
		cfg: d.Config, caps: d.Capabilities, ports: p,
	}
	if s.clock == nil {
		s.clock = clock.System{}
	}
	if s.log == nil {
		s.log = logx.Nop()
	}
	rt := defaultRuntime()
	s.rt.Store(&rt)
	return s
}

// defaultRuntime is the snapshot a fresh install starts from.
func defaultRuntime() Runtime {
	var rt Runtime
	for _, d := range reg.list {
		if d.target != nil {
			applyValue(d.target(&rt), d.def, d.emptyZero)
		}
	}
	rt.normalize()
	return rt
}

// Ports returns a copy of the wired collaborators.
func (s *Service) Ports() Ports {
	s.portsMu.RLock()
	defer s.portsMu.RUnlock()
	p := s.ports
	if s.ports.Runtimes != nil {
		p.Runtimes = make(map[string]ManagedRuntime, len(s.ports.Runtimes))
		for k, v := range s.ports.Runtimes {
			p.Runtimes[k] = v
		}
	}
	return p
}

// WirePorts sets every non-nil member of p, leaving the others as they are. The
// integrator calls it once the collaborating services exist.
func (s *Service) WirePorts(p Ports) {
	s.portsMu.Lock()
	defer s.portsMu.Unlock()
	if p.Audit != nil {
		s.ports.Audit = p.Audit
	}
	if p.SuperAdmins != nil {
		s.ports.SuperAdmins = p.SuperAdmins
	}
	if p.VectorStore != nil {
		s.ports.VectorStore = p.VectorStore
	}
	if p.Embedding != nil {
		s.ports.Embedding = p.Embedding
	}
	if p.NativeTools != nil {
		s.ports.NativeTools = p.NativeTools
	}
	if p.UserSettings != nil {
		s.ports.UserSettings = p.UserSettings
	}
	for name, rtm := range p.Runtimes {
		if s.ports.Runtimes == nil {
			s.ports.Runtimes = map[string]ManagedRuntime{}
		}
		s.ports.Runtimes[name] = rtm
	}
}

func (s *Service) port() Ports { return s.Ports() }

// Runtime returns the current runtime snapshot. The result must be treated as
// read-only; a new snapshot is published after every settings change.
func (s *Service) Runtime() *Runtime {
	if rt := s.rt.Load(); rt != nil {
		return rt
	}
	rt := defaultRuntime()
	return &rt
}

// publish makes rt the current snapshot and projects the platform-owned parts
// into the configuration holder.
func (s *Service) publish(rt *Runtime) {
	s.rt.Store(rt)
	if s.cfg != nil {
		s.cfg.Update(func(c *config.Config) { rt.projectAuth(c) })
	}
}

// Reload re-reads every row and publishes a fresh snapshot.
func (s *Service) Reload(ctx context.Context) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.reloadLocked(ctx)
}

func (s *Service) reloadLocked(ctx context.Context) error {
	rows, err := s.repo.all(ctx)
	if err != nil {
		return err
	}
	rt, err := s.buildRuntime(ctx, rows)
	if err != nil {
		return err
	}
	s.publish(rt)
	return nil
}

// buildRuntime projects stored rows onto a copy of the current snapshot. A
// value that fails to parse keeps the previous value; a sensitive value that
// cannot be decrypted fails the whole load.
func (s *Service) buildRuntime(ctx context.Context, rows []SystemSetting) (*Runtime, error) {
	eff, _ := collapseRows(rows)
	next := *s.Runtime()
	for _, row := range eff {
		d := reg.lookup(row.Namespace, row.Key)
		if d == nil {
			continue
		}
		s.cacheStore(ctx, row.Namespace, row.Key, row.Value)
		if d.target == nil {
			continue
		}
		val := row.Value
		if d.sensitive {
			plain, err := s.decrypt(row.Value)
			if err != nil {
				return nil, fmt.Errorf("settings: decrypt %s/%s: %w", row.Namespace, row.Key, err)
			}
			val = plain
		}
		applyValue(d.target(&next), val, d.emptyZero)
	}
	next.normalize()
	return &next, nil
}

func (s *Service) decrypt(stored string) (string, error) {
	if strings.TrimSpace(stored) == "" {
		return "", nil
	}
	plain, _, err := s.secrets.Decrypt(stored)
	return plain, err
}

func (s *Service) encrypt(plain string) (string, error) {
	if strings.TrimSpace(plain) == "" {
		return "", nil
	}
	return s.secrets.Encrypt(plain)
}

// ---- auxiliary cache -------------------------------------------------------------

func cacheKey(ns, key string) string { return "settings:" + ns + ":" + key }

func (s *Service) cacheStore(ctx context.Context, ns, key, value string) {
	if s.cache == nil {
		return
	}
	_ = s.cache.Set(ctx, cacheKey(ns, key), []byte(value), settingsCacheTTL)
}

func (s *Service) cacheDelete(ctx context.Context, ns, key string) {
	if s.cache == nil {
		return
	}
	_ = s.cache.Delete(ctx, cacheKey(ns, key))
}

// ---- reads -----------------------------------------------------------------------

func (s *Service) toItem(row SystemSetting) Item {
	it := Item{Key: row.Key, ValueType: row.ValueType, Description: row.Description}
	configured := strings.TrimSpace(row.Value) != ""
	if d := reg.lookup(row.Namespace, row.Key); d != nil && d.sensitive {
		it.Sensitive = true
	} else {
		it.Value = row.Value
	}
	it.Configured = configured
	return it
}

func sortRows(rows []SystemSetting) {
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Key != rows[j].Key {
			return rows[i].Key < rows[j].Key
		}
		return rows[i].ID < rows[j].ID
	})
}

// List returns every registered setting grouped by namespace. Rows for keys the
// registry does not know are hidden, and namespaces without rows are absent.
func (s *Service) List(ctx context.Context) (map[string][]Item, error) {
	rows, err := s.repo.all(ctx)
	if err != nil {
		return nil, err
	}
	eff, _ := collapseRows(rows)
	byNS := map[string][]SystemSetting{}
	for _, row := range eff {
		if reg.lookup(row.Namespace, row.Key) == nil {
			continue
		}
		byNS[row.Namespace] = append(byNS[row.Namespace], row)
	}
	out := make(map[string][]Item, len(byNS))
	for ns, list := range byNS {
		sortRows(list)
		items := make([]Item, 0, len(list))
		for _, row := range list {
			items = append(items, s.toItem(row))
		}
		out[ns] = items
	}
	return out, nil
}

// ListNamespace returns the settings of one namespace ordered by key. Unlike
// List it does not hide rows for unregistered keys.
func (s *Service) ListNamespace(ctx context.Context, ns string) ([]Item, error) {
	ns = strings.TrimSpace(ns)
	if !reg.ns[ns] {
		return nil, invalid(ErrInvalidNamespace, "", violation{rule: "invalid_namespace"})
	}
	rows, err := s.repo.byNamespace(ctx, ns)
	if err != nil {
		return nil, err
	}
	eff, _ := collapseRows(rows)
	items := make([]Item, 0, len(eff))
	for _, row := range eff {
		items = append(items, s.toItem(row))
	}
	return items, nil
}

// NamespaceValues returns key -> trimmed, decrypted value for every registered
// key of a namespace. Keys without a stored row report the registry default.
// Billing and the circuit breaker read their settings this way.
func (s *Service) NamespaceValues(ctx context.Context, ns string) (map[string]string, error) {
	ns = strings.TrimSpace(ns)
	if !reg.ns[ns] {
		return nil, invalid(ErrInvalidNamespace, "", violation{rule: "invalid_namespace"})
	}
	rows, err := s.repo.byNamespace(ctx, ns)
	if err != nil {
		return nil, err
	}
	eff, _ := collapseRows(rows)
	out := map[string]string{}
	for _, d := range reg.byNamespace(ns) {
		out[d.key] = strings.TrimSpace(d.def)
	}
	for _, row := range eff {
		d := reg.lookup(ns, row.Key)
		if d == nil {
			continue
		}
		v := row.Value
		if d.sensitive {
			plain, err := s.decrypt(v)
			if err != nil {
				return nil, fmt.Errorf("settings: decrypt %s/%s: %w", ns, row.Key, err)
			}
			v = plain
		}
		out[row.Key] = strings.TrimSpace(v)
	}
	return out, nil
}

// storedValue returns the trimmed stored (not decrypted) value of a setting, or
// its registry default when no row exists.
func (s *Service) storedValue(ctx context.Context, ns, key string) (string, error) {
	rows, err := s.repo.find(ctx, ns, key)
	if err != nil {
		return "", err
	}
	if eff, _ := collapseRows(rows); len(eff) > 0 {
		return strings.TrimSpace(eff[0].Value), nil
	}
	if d := reg.lookup(ns, key); d != nil {
		return strings.TrimSpace(d.def), nil
	}
	return "", nil
}

// LoginDefaultNextPath returns the path opened after sign-in, falling back to
// "/chat" when the stored value is blank or not a local path.
func (s *Service) LoginDefaultNextPath(ctx context.Context) (string, error) {
	v, err := s.storedValue(ctx, "auth", "login_default_next_path")
	if err != nil {
		return "", err
	}
	if v == "" || !validLoginPath(v) {
		return "/chat", nil
	}
	return v, nil
}

// LoadBilling returns the billing namespace as key -> decrypted value. It
// matches the store interface the billing area consumes.
func (s *Service) LoadBilling(ctx context.Context) (map[string]string, error) {
	return s.NamespaceValues(ctx, "billing")
}

// PatchValues applies key -> value changes inside one namespace with the same
// validation as the administrator API, without an actor and without a generic
// audit entry (the caller records its own). Sensitive keys
// with blank values are left alone. It matches the store interface the billing
// area consumes for its own configuration endpoint.
func (s *Service) PatchValues(ctx context.Context, ns string, values map[string]string) error {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]PatchItem, 0, len(keys))
	for _, k := range keys {
		items = append(items, PatchItem{Namespace: ns, Key: k, Value: values[k]})
	}
	_, err := s.patch(ctx, Actor{}, items, false)
	return err
}
