package channel

import (
	"context"
	_ "embed"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

//go:embed builtin_catalog.json.gz
var embeddedCatalog []byte

const (
	catalogFileRel    = "catalogs/models-dev.json"
	legacyCatalogRel  = "admin/models-dev-reasoning-catalog.json"
	maxCatalogFile    = 16 << 20
	catalogStaleAfter = 24 * time.Hour
	catalogRetryAfter = time.Hour
	catalogRefreshTO  = 90 * time.Second
	catalogLoadTO     = 10 * time.Second

	// MinSnapshotEntries is the least number of entries a generated snapshot
	// must contain before it may replace the committed one.
	MinSnapshotEntries = 1000
)

// CatalogStatus is the admin-visible state of the models.dev catalog.
type CatalogStatus struct {
	Origin     *string `json:"origin"`
	FetchedAt  *string `json:"fetchedAt"`
	ModelCount int     `json:"modelCount"`
	LastError  *string `json:"lastError"`
	Refreshing bool    `json:"refreshing"`
}

// catalogManager owns the in-memory catalog, its cache file and its refreshes.
type catalogManager struct {
	svc     *Service
	fetcher CatalogFetcher

	mu          sync.RWMutex
	view        *catalogView
	origin      string
	fetchedAt   time.Time
	lastError   string
	refreshing  bool
	lastAttempt time.Time

	fetchMu sync.Mutex
}

func newCatalogManager(s *Service, f CatalogFetcher) *catalogManager {
	return &catalogManager{svc: s, fetcher: f}
}

func (m *catalogManager) root() string {
	return strings.TrimSpace(m.svc.cfg.Load().Storage.Local.RootDir)
}

func (m *catalogManager) current() *catalogView {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.view
}

func (m *catalogManager) builtinSnapshot() *catalogSnapshot {
	raw := m.svc.opts.BuiltinCatalog
	if raw == nil {
		raw = embeddedCatalog
	}
	if len(raw) == 0 {
		return nil
	}
	snap, err := decodeSnapshot(raw)
	if err != nil {
		m.svc.log.Warn("llm_catalog_builtin_unreadable", logx.Err(err))
		return nil
	}
	snap.Origin = "builtin"
	return snap
}

// readStored reads the cache file, moving the legacy file into place first.
func (m *catalogManager) readStored() (*catalogSnapshot, error) {
	root := m.root()
	if root == "" {
		return nil, os.ErrNotExist
	}
	path := filepath.Join(root, catalogFileRel)
	if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
		legacy := filepath.Join(root, legacyCatalogRel)
		if _, lerr := os.Stat(legacy); lerr == nil {
			if mkerr := os.MkdirAll(filepath.Dir(path), 0o755); mkerr == nil {
				_ = os.Rename(legacy, path)
			}
		}
	}
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if info.Size() > maxCatalogFile {
		return nil, errors.New("catalog file too large")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return decodeSnapshot(raw)
}

// writeStored writes the cache file atomically (temp file + rename).
func (m *catalogManager) writeStored(snap *catalogSnapshot) error {
	root := m.root()
	if root == "" {
		return errors.New("storage root is not configured")
	}
	raw, err := encodeSnapshot(*snap, false)
	if err != nil {
		return err
	}
	path := filepath.Join(root, catalogFileRel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".models-dev-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *catalogManager) apply(snap *catalogSnapshot, origin string) {
	view := buildCatalogView(snap)
	m.mu.Lock()
	m.view, m.origin, m.fetchedAt = view, origin, snap.FetchedAt
	m.mu.Unlock()
}

// load installs the stored catalog or the built-in snapshot. It never touches
// the network.
func (m *catalogManager) load(ctx context.Context) {
	_ = ctx
	builtin := m.builtinSnapshot()
	stored, err := m.readStored()
	if err != nil && !errors.Is(err, os.ErrNotExist) && !errors.Is(err, errSnapshotOutdated) {
		m.svc.log.Warn("llm_catalog_stored_unreadable", logx.Err(err))
	}
	useStored := stored != nil && len(stored.Models) > 0 && (builtin == nil || !builtin.FetchedAt.After(stored.FetchedAt))
	switch {
	case useStored:
		origin := stored.Origin
		if origin == "" {
			origin = "remote"
		}
		m.installIfNewer(stored, origin)
	case builtin != nil:
		m.installIfNewer(builtin, "builtin")
		seed := *builtin
		if werr := m.writeStored(&seed); werr != nil {
			m.svc.log.Warn("llm_catalog_seed_failed", logx.Err(werr))
		}
	}
}

// installIfNewer keeps a catalog loaded by a manual refresh that finished first.
func (m *catalogManager) installIfNewer(snap *catalogSnapshot, origin string) {
	m.mu.RLock()
	has := m.view != nil
	newer := has && m.fetchedAt.After(snap.FetchedAt)
	m.mu.RUnlock()
	if newer {
		return
	}
	m.apply(snap, origin)
}

// status reports the catalog state and, when stale, nudges a background refresh.
func (m *catalogManager) status(ctx context.Context) CatalogStatus {
	m.maybeRefresh(ctx)
	m.mu.RLock()
	defer m.mu.RUnlock()
	st := CatalogStatus{Refreshing: m.refreshing}
	if m.view != nil {
		st.ModelCount = m.view.count()
		o := m.origin
		st.Origin = &o
		f := m.fetchedAt.UTC().Format(time.RFC3339)
		st.FetchedAt = &f
	}
	if m.lastError != "" {
		e := m.lastError
		st.LastError = &e
	}
	return st
}

// maybeRefresh starts a background refresh when the catalog is stale and no
// recent attempt was made.
func (m *catalogManager) maybeRefresh(ctx context.Context) {
	if m.fetcher == nil {
		return
	}
	now := m.svc.now()
	m.mu.Lock()
	stale := m.view == nil || now.Sub(m.fetchedAt) > catalogStaleAfter
	due := m.lastAttempt.IsZero() || now.Sub(m.lastAttempt) > catalogRetryAfter
	if m.refreshing || !stale || !due {
		m.mu.Unlock()
		return
	}
	m.refreshing = true
	m.lastAttempt = now
	m.mu.Unlock()
	m.svc.life.Detach(ctx, "llm-catalog-refresh", catalogRefreshTO, func(dctx context.Context) {
		if err := m.fetchAndApply(dctx); err != nil {
			m.svc.log.Warn("llm_catalog_refresh_failed", logx.Err(err))
		}
	})
}

// refresh performs a synchronous refresh.
func (m *catalogManager) refresh(ctx context.Context) error {
	if m.fetcher == nil {
		m.setError(errors.New("no catalog fetcher configured"))
		return ErrCatalogFetchFailed
	}
	m.mu.Lock()
	m.refreshing = true
	m.lastAttempt = m.svc.now()
	m.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, catalogRefreshTO)
	defer cancel()
	if err := m.fetchAndApply(ctx); err != nil {
		return ErrCatalogFetchFailed.WithCause(err)
	}
	return nil
}

func (m *catalogManager) setError(err error) {
	msg := err.Error()
	if len(msg) > 500 {
		msg = msg[:500]
	}
	m.mu.Lock()
	m.lastError = msg
	m.mu.Unlock()
}

// fetchAndApply downloads, parses and installs the catalog. Concurrent fetches
// are serialized.
func (m *catalogManager) fetchAndApply(ctx context.Context) (err error) {
	m.fetchMu.Lock()
	defer m.fetchMu.Unlock()
	defer func() {
		m.mu.Lock()
		m.refreshing = false
		m.mu.Unlock()
		if err != nil {
			m.setError(err)
		}
	}()
	raw, err := m.fetcher.Fetch(ctx)
	if err != nil {
		return err
	}
	entries, err := parseModelsDev(raw)
	if err != nil {
		return err
	}
	snap := &catalogSnapshot{Version: snapshotVersion, Source: "models.dev", Origin: "remote",
		FetchedAt: m.svc.now().UTC(), Models: entries}
	if werr := m.writeStored(snap); werr != nil {
		m.svc.log.Warn("llm_catalog_write_failed", logx.Err(werr))
	}
	m.apply(snap, "remote")
	m.mu.Lock()
	m.lastError = ""
	m.mu.Unlock()
	return nil
}

// EncodeCatalogSnapshot converts a models.dev api.json document into the gzip
// snapshot form shipped inside the binary. It refuses to produce fewer than
// MinSnapshotEntries entries. It backs the `catalog-snapshot` command.
func EncodeCatalogSnapshot(apiJSON []byte, fetchedAt time.Time) ([]byte, int, error) {
	entries, err := parseModelsDev(apiJSON)
	if err != nil {
		return nil, 0, err
	}
	if len(entries) < MinSnapshotEntries {
		return nil, len(entries), errors.New("catalog snapshot has too few entries")
	}
	data, err := encodeSnapshot(catalogSnapshot{Source: "models.dev", Origin: "builtin", FetchedAt: fetchedAt.UTC(), Models: entries}, true)
	return data, len(entries), err
}

// ---- resolution (admin form helper) -----------------------------------------------

// CatalogResolution is the answer of POST /admin/llm/model-catalog/resolve.
type CatalogResolution struct {
	Matched           bool                `json:"matched"`
	Provider          *string             `json:"provider"`
	ModelID           *string             `json:"modelId"`
	InputModalities   []string            `json:"inputModalities"`
	OutputModalities  []string            `json:"outputModalities"`
	ContextWindow     *int                `json:"contextWindow"`
	Reasoning         *ResolvedReasoning  `json:"reasoning"`
	ReasoningTemplate *ReasoningTemplateV `json:"reasoningTemplate"`
}

// ResolvedReasoning is the reasoning a model would get ignoring any explicit
// declaration.
type ResolvedReasoning struct {
	Format  string   `json:"format"`
	Levels  []string `json:"levels"`
	Default string   `json:"default"`
	Source  string   `json:"source"`
}

// ReasoningTemplateV is the admin form template of a reasoning declaration.
type ReasoningTemplateV struct {
	Format  string         `json:"format"`
	Levels  []string       `json:"levels"`
	Budgets map[string]int `json:"budgets"`
}

// CatalogStatus returns the status and nudges a refresh when stale.
func (s *Service) CatalogStatus(ctx context.Context) CatalogStatus { return s.catalog.status(ctx) }

// RefreshCatalog performs a synchronous refresh.
func (s *Service) RefreshCatalog(ctx context.Context) (CatalogStatus, error) {
	err := s.catalog.refresh(ctx)
	st := s.catalog.status(ctx)
	return st, err
}

// ResolveCatalog matches a model against the catalog. It never fails for
// unknown models.
func (s *Service) ResolveCatalog(ctx context.Context, req resolveCatalogReq) CatalogResolution {
	s.catalog.maybeRefresh(ctx)
	out := CatalogResolution{InputModalities: []string{}, OutputModalities: []string{}}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		return out
	}
	view := s.catalog.current()
	vendor := lower(req.Vendor)
	if v := canonicalVendor(vendor); v != "" {
		vendor = v
	}
	pkeys := protocolKeys(req.Protocols)
	mod := view.matchModality(name, vendor, pkeys)
	rea := view.matchReasoning(name, vendor, pkeys)
	if mod != nil || rea != nil {
		out.Matched = true
		head := mod
		if head == nil {
			head = rea
		}
		out.Provider, out.ModelID = ptr(head.Provider), ptr(head.ID)
	}
	if mod != nil {
		out.InputModalities = append([]string{}, mod.Inputs...)
		out.OutputModalities = append([]string{}, mod.Outputs...)
		if mod.Context >= minContextWindow && mod.Context <= maxContextWindow {
			out.ContextWindow = ptr(mod.Context)
		}
	}
	caps := parseCapsLenient(req.CapabilitiesJSON)
	ignoring := capsMap{}
	for k, v := range caps {
		if k != "reasoning" {
			ignoring[k] = v
		}
	}
	if c := resolveReasoning(ignoring, req.Protocols, vendor, rea); c != nil {
		out.Reasoning = &ResolvedReasoning{Format: c.Format, Levels: c.Levels, Default: c.Default, Source: c.Source}
	}
	effective := resolveReasoning(caps, req.Protocols, vendor, rea)
	if t := buildReasoningTemplate(effective, rea, req.Protocols, vendor); t != nil {
		levels := t.Levels
		if levels == nil {
			levels = []string{}
		}
		out.ReasoningTemplate = &ReasoningTemplateV{Format: t.Format, Levels: levels, Budgets: t.Budgets}
	}
	return out
}
