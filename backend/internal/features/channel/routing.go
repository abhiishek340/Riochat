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
