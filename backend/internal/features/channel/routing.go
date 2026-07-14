package channel

import (
	"context"
	"errors"
	"math"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Route scopes.
// ScopeUser is the default routing scope; ScopeInternal (see normalize.go) lets
// internal callers use any model.
const ScopeUser = "user"

// RouteRequest asks for a route to a platform model.
type RouteRequest struct {
	Model          string
	Task           string
	Scope          string // "user" (default) or "internal"
	UserID         uint
	ConversationID uint
	RequestID      string
	Exclude        []uint // route ids that already failed
}

// BreakerSettings are the circuit-breaker numbers attached to one level of a
// route snapshot (0 means "use the next level's value").
type BreakerSettings struct {
	FailureThreshold int
	ModelThreshold   int
	ThresholdLogic   string
	DurationMin      int
	WindowMin        int
}

// Route is the snapshot of a selected route.
type Route struct {
	RouteID           uint
	PlatformModelID   uint
	PlatformModelName string
	UpstreamModelID   uint
	UpstreamID        uint
	UpstreamName      string
	BindingCode       string
	Protocol          string
	BaseURL           string
	APIKey            string
	ConnectTimeout    time.Duration
	ReadTimeout       time.Duration
	StreamIdleTimeout time.Duration
	HeadersJSON       string
	Vendor            string
	Icon              string
	CapabilitiesJSON  string
	SystemPrompt      string
	UpstreamModelName string

	// ReasoningPassback asks the caller to return earlier reasoning content in
	// the history; PassbackOptions are extra request options that go with it.
	ReasoningPassback bool
	PassbackOptions   map[string]any

	// Reasoning is the resolved reasoning-effort capability on this route's
	// protocol and InputModalities the resolved input modalities.
	Reasoning       *ReasoningCapability
	InputModalities []string

	UpstreamBreaker BreakerSettings
	ModelBreaker    BreakerSettings
	ModelPolicyMode string
	RouteBreaker    BreakerSettings

	UpstreamProbeGranted bool
	BindingProbeGranted  bool

	// External marks a user-owned provider route; UntrustedEndpoint tells the
	// caller to use the SSRF-strict, no-redirect HTTP client.
	External          bool
	UntrustedEndpoint bool
}

// RateLimitedError is the error returned when every candidate route is backing
// off; it exposes the shortest remaining wait.
type RateLimitedError struct{ RetryAfter time.Duration }

func (e *RateLimitedError) Error() string { return "all model routes are rate limited" }

// Unwrap makes errors.Is(err, ErrUpstreamLimited) hold.
func (e *RateLimitedError) Unwrap() error { return ErrUpstreamLimited }

// RetryAfterOf extracts the wait of a rate-limited resolution error.
func RetryAfterOf(err error) (time.Duration, bool) {
	var rl *RateLimitedError
	if errors.As(err, &rl) {
		return rl.RetryAfter, true
	}
	return 0, false
}

type candidate struct {
	rec  routeRecord
	up   *Upstream
	keys *keyDoc
}

// ResolveRoute picks a route for a request (see the routing rules in the spec).
func (s *Service) ResolveRoute(ctx context.Context, req RouteRequest) (*Route, error) {
	name, err := normalizePlatformName(req.Model)
	if err != nil {
		return nil, ErrModelNotFound
	}
	var pm PlatformModel
	if err := s.db.WithContext(ctx).Where("name = ?", name).First(&pm).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrModelNotFound
		}
		return nil, err
	}
	if pm.Status != StatusActive {
		return nil, ErrModelNotFound
	}
	userScope := req.Scope != ScopeInternal
	if userScope {
		if pm.AccessScope != ScopePublic {
			return nil, ErrModelAccessDenied
		}
		if req.UserID > 0 {
			if err := s.CheckModelAccess(ctx, req.UserID, &pm); err != nil {
				return nil, err
			}
		}
	}
	all, err := s.loadRoutes(ctx, []uint{pm.ID})
	if err != nil {
		return nil, err
	}
	var chain []routeRecord
	for _, r := range all {
		if r.activeChain() {
			chain = append(chain, r)
		}
	}
	if len(chain) == 0 {
		return nil, ErrRouteNotFound
	}
	task := NormalizeTaskType(req.Task)
	kinds := parseStoredKinds(pm.KindsJSON)
	excluded := map[uint]bool{}
	for _, id := range req.Exclude {
		excluded[id] = true
	}
	var eligible []routeRecord
	for _, r := range chain {
		if !excluded[r.RouteID] && RouteServesTask(kinds, r.Protocol, task) {
			eligible = append(eligible, r)
		}
	}
	if len(eligible) == 0 {
		return nil, ErrRoutesUnavailable
	}
	// Upstream rows (credentials, timeouts, breaker settings) for the candidates.
	upIDs := map[uint]bool{}
	for _, r := range eligible {
		upIDs[r.UpstreamID] = true
	}
	ids := make([]uint, 0, len(upIDs))
	for id := range upIDs {
		ids = append(ids, id)
	}
	var ups []Upstream
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&ups).Error; err != nil {
		return nil, err
	}
	upByID := map[uint]*Upstream{}
	for i := range ups {
		upByID[ups[i].ID] = &ups[i]
	}
	keyCache := map[uint]*keyDoc{}
	breaker := s.BreakerEnabled(ctx)
	var shortestWait time.Duration
	sawBackoff := false

	for i := 0; i < len(eligible); {
		j := i
		for j < len(eligible) && eligible[j].Priority == eligible[i].Priority {
			j++
		}
		group := eligible[i:j]
		i = j
		var cands []candidate
		for _, r := range group {
			up := upByID[r.UpstreamID]
			if up == nil || r.UpstreamModelID == 0 || r.BindingCode == "" || strings.TrimSpace(r.UpstreamModelName) == "" {
				continue
			}
			if !isImplementedProtocol(r.Protocol) {
				continue
			}
			if _, err := validateUpstreamBaseURL(up.BaseURL); err != nil {
				s.log.Warn("llm_route_unsafe_base_url", logx.F("upstream", up.ID))
				continue
			}
			wait, err := s.circuits.backoffRemaining(ctx, up.ID, r.RouteID)
			if err != nil {
				return nil, err
			}
			if wait > 0 {
				if !sawBackoff || wait < shortestWait {
					shortestWait = wait
				}
				sawBackoff = true
				continue
			}
			doc, ok := keyCache[up.ID]
			if !ok {
				doc, err = s.decryptKeys(up)
				if err != nil {
					doc = nil
				}
				keyCache[up.ID] = doc
			}
			if doc == nil || len(doc.activeKeys()) == 0 {
				continue
			}
			cands = append(cands, candidate{rec: r, up: up, keys: doc})
		}
		for len(cands) > 0 {
			idx := pickWeighted(cands)
			c := cands[idx]
			cands = append(cands[:idx], cands[idx+1:]...)
			var upProbe, bindProbe bool
			if breaker {
				st, err := s.circuits.check(ctx, upstreamPrefix(c.up.ID))
				if err != nil {
					return nil, err
				}
				if st.Open || (st.HalfOpen && !st.ProbeGranted) {
					continue
				}
				upProbe = st.HalfOpen && st.ProbeGranted
				bst, err := s.circuits.check(ctx, bindingPrefix(c.up.ID, c.rec.BindingCode))
				if err != nil {
					return nil, err
				}
				if bst.Open || (bst.HalfOpen && !bst.ProbeGranted) {
					if upProbe {
						_ = s.circuits.releaseProbe(ctx, upstreamPrefix(c.up.ID))
					}
					continue
				}
				bindProbe = bst.HalfOpen && bst.ProbeGranted
			}
			return s.snapshot(ctx, &pm, c, upProbe, bindProbe), nil
		}
	}
	if sawBackoff {
		return nil, &RateLimitedError{RetryAfter: shortestWait}
	}
	return nil, ErrRoutesUnavailable
}

func pickWeighted(c []candidate) int {
	total := 0
	for _, x := range c {
		total += effectiveWeight(x.rec.Weight)
	}
	n := rand.IntN(total)
	for i, x := range c {
		n -= effectiveWeight(x.rec.Weight)
		if n < 0 {
			return i
		}
	}
	return len(c) - 1
}

func effectiveWeight(w int) int {
	if w <= 0 {
		return 100
	}
	return w
}

func (s *Service) snapshot(ctx context.Context, pm *PlatformModel, c candidate, upProbe, bindProbe bool) *Route {
	r, up := c.rec, c.up
	key, _ := c.keys.pickKey(func() int64 { return s.circuits.nextKeyCounter(ctx, up.ID) })
	caps := parseCapsLenient(pm.CapabilitiesJSON)
	view := s.catalog.current()
	vendor := firstNonEmpty(pm.Vendor, detectVendor(pm.Name, r.UpstreamModelName, up.Name))
	rEntry := view.matchReasoning(pm.Name, pm.Vendor, protocolKeys([]string{r.Protocol}))
	mEntry := view.matchModality(pm.Name, pm.Vendor, protocolKeys([]string{r.Protocol}))
	route := &Route{
		RouteID: r.RouteID, PlatformModelID: pm.ID, PlatformModelName: pm.Name, UpstreamModelID: r.UpstreamModelID,
		UpstreamID: up.ID, UpstreamName: up.Name, BindingCode: r.BindingCode, Protocol: r.Protocol,
		BaseURL: strings.TrimSpace(up.BaseURL), APIKey: key,
		ConnectTimeout:    time.Duration(timeoutOrDefault(up.ConnectTimeoutMS, defaultConnectTimeoutMS)) * time.Millisecond,
		ReadTimeout:       time.Duration(timeoutOrDefault(up.ReadTimeoutMS, defaultReadTimeoutMS)) * time.Millisecond,
		StreamIdleTimeout: time.Duration(timeoutOrDefault(up.StreamIdleTimeoutMS, defaultStreamIdleMS)) * time.Millisecond,
		HeadersJSON:       mergeHeaders(up.HeadersJSON, r.RouteHeaders),
		Vendor:            pm.Vendor, Icon: pm.Icon, CapabilitiesJSON: pm.CapabilitiesJSON, SystemPrompt: pm.SystemPrompt,
		UpstreamModelName: r.UpstreamModelName,
		Reasoning:         resolveReasoning(caps, []string{r.Protocol}, pm.Vendor, rEntry),
		UpstreamBreaker: BreakerSettings{FailureThreshold: up.CBFailureThreshold, ModelThreshold: up.CBModelThreshold,
			ThresholdLogic: up.CBThresholdLogic, DurationMin: up.CBDurationMin, WindowMin: up.CBWindowMin},
		ModelBreaker:         BreakerSettings{FailureThreshold: pm.CBFailureThreshold, DurationMin: pm.CBDurationMin, WindowMin: pm.CBWindowMin},
		ModelPolicyMode:      pm.CBPolicyMode,
		RouteBreaker:         BreakerSettings{FailureThreshold: r.RouteCBFailure, DurationMin: r.RouteCBDuration, WindowMin: r.RouteCBWindow},
		UpstreamProbeGranted: upProbe, BindingProbeGranted: bindProbe,
	}
	route.ReasoningPassback, route.PassbackOptions = passbackFor(r.Protocol, pm.Vendor, pm.Name, r.UpstreamModelName, up.Name)
	if explicit, declared, valid := caps.explicitInputModalities(); declared && valid && len(explicit) > 0 {
		route.InputModalities = explicit
	} else if mEntry != nil {
		route.InputModalities = append([]string{}, mEntry.Inputs...)
	}
	_ = vendor
	return route
}

// passbackFor decides whether earlier reasoning content must be passed back.
func passbackFor(protocol, vendor string, names ...string) (bool, map[string]any) {
	switch protocol {
	case ProtoOpenRouterChat:
		return true, nil
	case ProtoOpenAIChat:
		v := canonicalVendor(vendor)
		if v == "" || v == "unknown" {
			v = detectVendor(names...)
		}
		switch v {
		case "deepseek", "moonshot", "zhipu", "xiaomi", "minimax":
			return true, nil
		case "alibaba":
			return true, map[string]any{"preserve_thinking": true}
		}
	}
	return false, nil
}

// ResolveDefaultRoute returns a route for the first public model, in display
// order and without user filtering, that can serve the task.
func (s *Service) ResolveDefaultRoute(ctx context.Context, task string) (*Route, error) {
	list, err := s.ListActiveModels(ctx, 0)
	if err != nil {
		return nil, err
	}
	for _, m := range list {
		if !ModelSupportsTask(parseStoredKinds(m.KindsJSON), task) {
			continue
		}
		r, err := s.ResolveRoute(ctx, RouteRequest{Model: m.PlatformModelName, Task: task, Scope: ScopeInternal})
		if err == nil {
			return r, nil
		}
	}
	return nil, ErrRoutesUnavailable
}

// ---- outcome bookkeeping ---------------------------------------------------------------

func summarizeFailure(err error) string {
	var ue *UpstreamError
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return "upstream request timed out"
	case errors.As(err, &ue) && ue.StatusCode > 0:
		return "upstream request failed with status " + strconv.Itoa(ue.StatusCode)
	}
	return "upstream request failed"
}

// parseRetryAfter reads a Retry-After header: whole seconds or an HTTP date.
func parseRetryAfter(h http.Header, now time.Time) time.Duration {
	v := strings.TrimSpace(h.Get("Retry-After"))
	if v == "" {
		return 0
	}
	if n, err := strconv.ParseInt(v, 10, 64); err == nil {
		if n < 0 {
			return 0
		}
		return time.Duration(n) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil {
		d := t.Sub(now)
		if d <= 0 {
			return 0
		}
		return time.Duration(math.Ceil(d.Seconds())) * time.Second
	}
	return 0
}

func (s *Service) bookkeepingCtx(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

// MarkSuccess records a successful use of a route.
func (s *Service) MarkSuccess(ctx context.Context, r *Route) {
	if r == nil || r.UpstreamID == 0 {
		return
	}
	ctx, cancel := s.bookkeepingCtx(ctx)
	defer cancel()
	if err := s.circuits.clearBackoff(ctx, r.UpstreamID, r.RouteID); err != nil {
		s.log.Warn("llm_backoff_clear_failed", logx.Err(err))
	}
	if r.UpstreamProbeGranted {
		if err := s.circuits.clearScope(ctx, upstreamPrefix(r.UpstreamID)); err != nil {
			s.log.Warn("llm_circuit_clear_failed", logx.Err(err))
		}
	}
	if r.BindingProbeGranted {
		if err := s.circuits.clearScope(ctx, bindingPrefix(r.UpstreamID, r.BindingCode)); err != nil {
			s.log.Warn("llm_circuit_clear_failed", logx.Err(err))
		}
	}
	_ = s.circuits.setMeta(ctx, upstreamPrefix(r.UpstreamID), "last_success_at", strconv.FormatInt(s.now().Unix(), 10))
}

func pickPositive(vals ...int) int {
	for _, v := range vals {
		if v > 0 {
			return v
		}
	}
	return 0
}

// MarkFailure records a failed use of a route and decides whether it trips a
// circuit or backs the route off.
func (s *Service) MarkFailure(ctx context.Context, r *Route, cause error) {
	if r == nil || r.UpstreamID == 0 {
		return
	}
	ctx, cancel := s.bookkeepingCtx(ctx)
	defer cancel()
	release := func() {
		if r.UpstreamProbeGranted {
			_ = s.circuits.releaseProbe(ctx, upstreamPrefix(r.UpstreamID))
		}
		if r.BindingProbeGranted {
			_ = s.circuits.releaseProbe(ctx, bindingPrefix(r.UpstreamID, r.BindingCode))
		}
	}
	if cause == nil {
		release()
		return
	}
	// Every failure leaves a trace on the upstream, before it is classified.
	up := upstreamPrefix(r.UpstreamID)
	_ = s.circuits.setMeta(ctx, up, "last_failure_at", strconv.FormatInt(s.now().Unix(), 10))
	_ = s.circuits.setMeta(ctx, up, "last_error", summarizeFailure(cause))
	if errors.Is(cause, context.Canceled) {
		release()
		return
	}

	class := s.classification(ctx)
	var ue *UpstreamError
	status := 0
	if errors.As(cause, &ue) {
		status = ue.StatusCode
	}
	switch {
	case status >= 400 && status < 500 && status != http.StatusTooManyRequests && listHas(class.IgnoreErrors, "4xx"):
		release()
		return
	case status == http.StatusTooManyRequests && listHas(class.RateLimitErrors, "429"):
		release()
		var retry time.Duration
		if ue != nil {
			retry = parseRetryAfter(ue.Header, s.now())
		}
		if _, err := s.circuits.recordBackoff(ctx, r.UpstreamID, r.RouteID, s.backoffSettings(ctx), retry); err != nil {
			s.log.Warn("llm_backoff_record_failed", logx.Err(err))
		}
		return
	}
	defs := s.breakerSettings(ctx)
	if !defs.Enabled {
		release()
		return
	}
	// Effective thresholds.
	bind := BreakerSettings{
		FailureThreshold: pickPositive(r.RouteBreaker.FailureThreshold, r.ModelBreaker.FailureThreshold, defs.ModelFailureThreshold),
		WindowMin:        pickPositive(r.RouteBreaker.WindowMin, r.ModelBreaker.WindowMin, defs.ModelWindowMin),
		DurationMin:      pickPositive(r.RouteBreaker.DurationMin, r.ModelBreaker.DurationMin, defs.ModelDurationMin),
	}
	if r.ModelPolicyMode == "enforced" {
		bind = BreakerSettings{
			FailureThreshold: pickPositive(r.ModelBreaker.FailureThreshold, defs.ModelFailureThreshold),
			WindowMin:        pickPositive(r.ModelBreaker.WindowMin, defs.ModelWindowMin),
			DurationMin:      pickPositive(r.ModelBreaker.DurationMin, defs.ModelDurationMin),
		}
	}
	upl := upstreamLevel{
		breakerLevel: breakerLevel{
			Threshold: pickPositive(r.UpstreamBreaker.FailureThreshold, defs.UpstreamFailureThreshold),
			Window:    time.Duration(pickPositive(r.UpstreamBreaker.WindowMin, defs.UpstreamWindowMin)) * time.Minute,
			Duration:  time.Duration(pickPositive(r.UpstreamBreaker.DurationMin, defs.UpstreamDurationMin)) * time.Minute,
		},
		ModelThreshold: pickPositive(r.UpstreamBreaker.ModelThreshold, defs.UpstreamModelThreshold),
		Logic:          firstNonEmpty(r.UpstreamBreaker.ThresholdLogic, defs.UpstreamThresholdLogic),
	}
	bindPrefix := bindingPrefix(r.UpstreamID, r.BindingCode)
	active := s.activeBindingPrefixes(ctx, r.UpstreamID)
	if len(active) == 0 {
		active = []string{bindPrefix}
	}
	_, err := s.circuits.recordFailure(ctx, up, bindPrefix, breakerLevel{
		Threshold: bind.FailureThreshold, Window: time.Duration(bind.WindowMin) * time.Minute,
		Duration: time.Duration(bind.DurationMin) * time.Minute,
	}, upl, active)
	if err != nil {
		s.log.Warn("llm_circuit_record_failed", logx.Err(err))
	}
}

// activeBindingPrefixes lists the circuit prefixes of the upstream's bindings
// that have an active route, upstream model and platform model.
func (s *Service) activeBindingPrefixes(ctx context.Context, upstreamID uint) []string {
	var codes []string
	err := s.db.WithContext(ctx).Table("llm_model_routes AS r").
		Joins("JOIN llm_upstream_models um ON um.id = r.upstream_model_id").
		Joins("JOIN llm_platform_models pm ON pm.id = r.platform_model_id").
		Where("um.upstream_id = ? AND r.status = ? AND um.status = ? AND pm.status = ?", upstreamID, StatusActive, StatusActive, StatusActive).
		Distinct().Pluck("um.binding_code", &codes).Error
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(codes))
	for _, c := range codes {
		out = append(out, bindingPrefix(upstreamID, c))
	}
	return out
}
