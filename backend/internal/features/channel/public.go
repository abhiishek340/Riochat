package channel

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	publicCacheTTL     = 30 * time.Second
	publicBuildTimeout = 10 * time.Second
)

// publicFacts are the per-model facts needed for access evaluation.
type publicFacts struct {
	vendor    string
	protocols []string
	upstreams []uint
}

type publicEntry struct {
	models  []PublicModel
	facts   map[uint]publicFacts
	expires time.Time
	gen     uint64
}

type publicFlight struct {
	done  chan struct{}
	entry *publicEntry
	err   error
}

// publicCache caches the public catalog in memory per billing mode. A
// generation counter makes fills that raced with an invalidation harmless.
type publicCache struct {
	mu      sync.Mutex
	gen     uint64
	entries map[string]*publicEntry
	flights map[string]*publicFlight
}

func newPublicCache() *publicCache {
	return &publicCache{entries: map[string]*publicEntry{}, flights: map[string]*publicFlight{}}
}

func (c *publicCache) invalidate() {
	c.mu.Lock()
	c.gen++
	c.entries = map[string]*publicEntry{}
	c.mu.Unlock()
}

// invalidateCatalog drops the cached public catalog. Every catalog-affecting
// write calls it.
func (s *Service) invalidateCatalog() { s.public.invalidate() }

// InvalidateCatalog is called by the billing area when pricing changes.
func (s *Service) InvalidateCatalog() { s.invalidateCatalog() }

func (s *Service) publicEntry(ctx context.Context) (*publicEntry, error) {
	mode := ""
	if s.opts.Pricing != nil {
		mode = s.opts.Pricing.BillingMode(ctx)
	}
	c := s.public
	now := s.now()
	c.mu.Lock()
	if e, ok := c.entries[mode]; ok && now.Before(e.expires) {
		c.mu.Unlock()
		return e, nil
	}
	if f, ok := c.flights[mode]; ok {
		c.mu.Unlock()
		select {
		case <-f.done:
			return f.entry, f.err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	f := &publicFlight{done: make(chan struct{})}
	c.flights[mode] = f
	gen := c.gen
	c.mu.Unlock()

	// The build outlives the request that triggered it.
	bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), publicBuildTimeout)
	entry, err := s.buildPublic(bctx, mode)
	cancel()

	c.mu.Lock()
	delete(c.flights, mode)
	if err == nil {
		entry.gen = gen
		entry.expires = s.now().Add(publicCacheTTL)
		if c.gen == gen { // discard a fill that raced with an invalidation
			c.entries[mode] = entry
		}
	}
	f.entry, f.err = entry, err
	c.mu.Unlock()
	close(f.done)
	return entry, err
}

func (s *Service) buildPublic(ctx context.Context, mode string) (*publicEntry, error) {
	rows, err := s.loadModelRows(ctx, func(db *gormDB) *gormDB { return db.Where("status = ?", StatusActive) })
	if err != nil {
		return nil, err
	}
	defaultOrder(rows)
	vendors, err := s.vendorMap(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.groupMap(ctx)
	if err != nil {
		return nil, err
	}
	var prices map[string]*Pricing
	filter := false
	if s.opts.Pricing != nil && mode != BillingModeSelf {
		if prices, filter, err = s.opts.Pricing.PublicPrices(ctx); err != nil {
			return nil, err
		}
	}
	view := s.catalog.current()
	e := &publicEntry{facts: map[uint]publicFacts{}}
	for _, r := range rows {
		if r.agg.chainCount == 0 || r.model.AccessScope != ScopePublic {
			continue
		}
		var price *Pricing
		if filter {
			p, ok := prices[r.model.Name]
			if !ok || p == nil {
				continue
			}
			price = p
		}
		pm := s.buildPublicModel(&r.model, r.agg, vendors, groups, view)
		pm.Pricing = price
		e.models = append(e.models, pm)
		facts := publicFacts{vendor: r.model.Vendor, protocols: r.agg.protocols}
		seen := map[uint]bool{}
		for _, rt := range r.agg.routes {
			if rt.activeChain() && !seen[rt.UpstreamID] {
				seen[rt.UpstreamID] = true
				facts.upstreams = append(facts.upstreams, rt.UpstreamID)
			}
		}
		e.facts[r.model.ID] = facts
	}
	return e, nil
}

func nonNilStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return append([]string{}, in...)
}

// buildPublicModel assembles the user-facing view of a platform model.
func (s *Service) buildPublicModel(m *PlatformModel, a modelAgg, vendors map[string]ModelVendor, groups map[uint]ModelDisplayGroup, view *catalogView) PublicModel {
	caps := parseCapsLenient(m.CapabilitiesJSON)
	pm := PublicModel{
		PlatformModelName: m.Name, Vendor: m.Vendor, VendorName: m.Vendor, DisplayGroupID: m.DisplayGroupID,
		KindsJSON: m.KindsJSON, Icon: m.Icon, ProtocolsJSON: jsonStringArray(a.protocols), CapabilitiesJSON: m.CapabilitiesJSON,
		Description: m.Description, SortOrder: m.SortOrder, Source: "platform", modelID: m.ID,
	}
	if vd, ok := vendors[m.Vendor]; ok {
		if vd.Name != "" {
			pm.VendorName = vd.Name
		}
		pm.VendorIcon = vd.Icon
	}
	if m.DisplayGroupID != nil {
		if g, ok := groups[*m.DisplayGroupID]; ok {
			pm.DisplayGroupName, pm.DisplayGroupIcon = g.Name, g.Icon
		}
	}
	s.fillCapabilities(&pm, m.Name, m.Vendor, caps, a.protocols, view, false)
	return pm
}

// fillCapabilities derives reasoning, controls, modalities and the display
// window of a public model.
func (s *Service) fillCapabilities(pm *PublicModel, name, vendor string, caps capsMap, protocols []string, view *catalogView, external bool) {
	pkeys := protocolKeys(protocols)
	rEntry := view.matchReasoning(name, vendor, pkeys)
	var mEntry *catalogEntry
	if external {
		mEntry = view.matchModalityExternal(name, vendor, pkeys)
	} else {
		mEntry = view.matchModality(name, vendor, pkeys)
	}
	rc := resolveReasoning(caps, protocols, vendor, rEntry)
	if rc != nil {
		var cp *string
		if rc.ControlPath != "" {
			cp = ptr(rc.ControlPath)
		}
		pm.Reasoning = &PublicReasoning{Levels: nonNilStrings(rc.Levels), Default: rc.Default, ControlPath: cp, Source: rc.Source, Locked: rc.Locked}
	}
	controls := effectiveControls(caps, rc, protocols, false)
	pm.Controls = make([]Control, 0, len(controls))
	for _, c := range controls {
		cc := *c
		if cc.Options == nil {
			cc.Options = []ControlOption{}
		}
		if cc.Protocols == nil {
			cc.Protocols = []string{}
		}
		pm.Controls = append(pm.Controls, cc)
	}
	pm.InputModalities, pm.OutputModalities = []string{}, []string{}
	explicit, declared, valid := caps.explicitInputModalities()
	switch {
	case declared && valid && len(explicit) > 0:
		pm.InputModalities, pm.InputModalitiesSource = explicit, ptr("explicit")
		if mEntry != nil {
			pm.OutputModalities = nonNilStrings(mEntry.Outputs)
		}
	case mEntry != nil:
		pm.InputModalities, pm.InputModalitiesSource = nonNilStrings(mEntry.Inputs), ptr("catalog")
		pm.OutputModalities = nonNilStrings(mEntry.Outputs)
	}
	window := 0
	if mEntry != nil {
		window = mEntry.Context
	}
	pm.ContextWindow = displayContextWindow(caps, window)
}

func clonePublic(m PublicModel) PublicModel {
	m.Controls = append([]Control{}, m.Controls...)
	m.InputModalities = append([]string{}, m.InputModalities...)
	m.OutputModalities = append([]string{}, m.OutputModalities...)
	if m.Pricing != nil {
		p := *m.Pricing
		p.Tiers = append([]PricingTier{}, p.Tiers...)
		p.SchedulePeriods = append([]PricingPeriod{}, p.SchedulePeriods...)
		m.Pricing = &p
	}
	if m.Reasoning != nil {
		r := *m.Reasoning
		r.Levels = append([]string{}, r.Levels...)
		m.Reasoning = &r
	}
	return m
}

// ListActiveModels lists the public catalog for a user. User id 0 (internal
// callers) skips access-group filtering.
func (s *Service) ListActiveModels(ctx context.Context, userID uint) ([]PublicModel, error) {
	entry, err := s.publicEntry(ctx)
	if err != nil {
		return nil, err
	}
	var allowed func(PublicModel) bool
	if userID > 0 {
		access, err := s.newAccessFilter(ctx, userID, entry)
		if err != nil {
			return nil, err
		}
		allowed = access
	}
	out := make([]PublicModel, 0, len(entry.models))
	for _, m := range entry.models {
		if allowed != nil && !allowed(m) {
			continue
		}
		out = append(out, clonePublic(m))
	}
	return out, nil
}

// UserModels is the response of GET /models: platform models followed by the
// user's personal models. Personal lookup failures never break the list.
func (s *Service) UserModels(ctx context.Context, userID uint) ([]PublicModel, error) {
	list, err := s.ListActiveModels(ctx, userID)
	if err != nil {
		return nil, err
	}
	if s.opts.Personal != nil {
		personal, perr := s.opts.Personal.PersonalModels(ctx, userID)
		if perr != nil {
			s.log.Warn("llm_personal_models_failed", logx.Err(perr))
		} else {
			list = append(list, personal...)
		}
	}
	return list, nil
}

// ActiveModelNames returns the names of active models that have at least one
// active-chain route (access scope and circuit state ignored).
func (s *Service) ActiveModelNames(ctx context.Context) ([]string, error) {
	rows, err := s.loadModelRows(ctx, func(db *gormDB) *gormDB { return db.Where("status = ?", StatusActive) })
	if err != nil {
		return nil, err
	}
	defaultOrder(rows)
	out := []string{}
	for _, r := range rows {
		if r.agg.chainCount > 0 {
			out = append(out, r.model.Name)
		}
	}
	return out, nil
}

// SupportsVideoGeneration is true when a model has a routable source and video kinds.
func (s *Service) SupportsVideoGeneration(ctx context.Context, name string) (bool, error) {
	n, err := normalizePlatformName(name)
	if err != nil {
		return false, nil
	}
	var m PlatformModel
	if err := s.db.WithContext(ctx).Where("name = ?", n).First(&m).Error; err != nil {
		if isNotFound(err) {
			return false, nil
		}
		return false, err
	}
	if m.Status != StatusActive {
		return false, nil
	}
	kinds := parseStoredKinds(m.KindsJSON)
	if !containsString(kinds, KindVideoGen) && !containsString(kinds, KindVideoExtension) {
		return false, nil
	}
	routes, err := s.loadRoutes(ctx, []uint{m.ID})
	if err != nil {
		return false, err
	}
	for _, r := range routes {
		if r.activeChain() {
			return true, nil
		}
	}
	return false, nil
}

// ModelIdentity is the identity of a platform model regardless of its status.
type ModelIdentity struct {
	ID     uint
	Name   string
	Vendor string
	Icon   string
}

// ResolveModelIdentity looks a model up by name; unknown or invalid names give model.not_found.
func (s *Service) ResolveModelIdentity(ctx context.Context, name string) (*ModelIdentity, error) {
	n, err := normalizePlatformName(name)
	if err != nil {
		return nil, ErrModelNotFound
	}
	var m PlatformModel
	if err := s.db.WithContext(ctx).Where("name = ?", n).First(&m).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrModelNotFound
		}
		return nil, err
	}
	return &ModelIdentity{ID: m.ID, Name: m.Name, Vendor: m.Vendor, Icon: m.Icon}, nil
}

// NativeToolDefinitions returns the provider-native tool definitions declared
// in all models' capabilities (the `nativeTools` array), de-duplicated by
// name. Their schema is owned by the native-tool area.
func (s *Service) NativeToolDefinitions(ctx context.Context) ([]json.RawMessage, error) {
	var docs []string
	if err := s.db.WithContext(ctx).Model(&PlatformModel{}).Pluck("capabilities_json", &docs).Error; err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []json.RawMessage
	add := func(raw json.RawMessage) {
		var head struct {
			Name string `json:"name"`
			ID   string `json:"id"`
		}
		_ = json.Unmarshal(raw, &head)
		key := head.ID
		if key == "" {
			key = head.Name
		}
		if key == "" {
			key = string(raw)
		}
		if !seen[key] {
			seen[key] = true
			out = append(out, raw)
		}
	}
	for _, raw := range s.opts.BuiltinNativeTools {
		add(raw)
	}
	for _, d := range docs {
		var doc struct {
			NativeTools []json.RawMessage `json:"nativeTools"`
		}
		if json.Unmarshal([]byte(d), &doc) != nil {
			continue
		}
		for _, raw := range doc.NativeTools {
			add(raw)
		}
	}
	return out, nil
}

// UnpricedForUser reports whether the model would be offered to the user were
// it priced: it is active, public, routable and granted to the user, but the
// billing mode requires prices and it has none.
func (s *Service) UnpricedForUser(ctx context.Context, userID uint, name string) (bool, error) {
	if s.opts.Pricing == nil {
		return false, nil
	}
	mode := s.opts.Pricing.BillingMode(ctx)
	if mode == BillingModeSelf {
		return false, nil
	}
	prices, filter, err := s.opts.Pricing.PublicPrices(ctx)
	if err != nil || !filter {
		return false, err
	}
	if p, ok := prices[name]; ok && p != nil {
		return false, nil
	}
	rows, err := s.loadModelRows(ctx, func(db *gormDB) *gormDB { return db.Where("status = ? AND name = ?", StatusActive, name) })
	if err != nil {
		return false, err
	}
	for _, r := range rows {
		if r.agg.chainCount == 0 || r.model.AccessScope != ScopePublic {
			continue
		}
		pm := s.buildPublicModel(&r.model, r.agg, nil, nil, s.catalog.current())
		facts := publicFacts{vendor: r.model.Vendor, protocols: r.agg.protocols}
		seen := map[uint]bool{}
		for _, rt := range r.agg.routes {
			if rt.activeChain() && !seen[rt.UpstreamID] {
				seen[rt.UpstreamID] = true
				facts.upstreams = append(facts.upstreams, rt.UpstreamID)
			}
		}
		e := &publicEntry{facts: map[uint]publicFacts{r.model.ID: facts}}
		access, err := s.newAccessFilter(ctx, userID, e)
		if err != nil {
			return false, err
		}
		return access(pm), nil
	}
	return false, nil
}

// ModelRouteState says whether a platform model name has a route a send could
// use at all, ignoring who asks: "missing" (no such model, inactive, or
// without an active route) or "present".
func (s *Service) ModelRouteState(ctx context.Context, name string) (string, error) {
	var m PlatformModel
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&m).Error; err != nil {
		if isNotFound(err) {
			return "missing", nil
		}
		return "", err
	}
	if m.Status != StatusActive {
		return "missing", nil
	}
	return "present", nil
}
