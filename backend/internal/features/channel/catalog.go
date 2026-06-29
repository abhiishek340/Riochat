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
