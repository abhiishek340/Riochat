package channel

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

var bg = context.Background()

func (s *suite) resolve(model string, exclude ...uint) (*Route, error) {
	return s.svc.ResolveRoute(bg, RouteRequest{Model: model, Scope: ScopeInternal, Exclude: exclude})
}

func (s *suite) setRoute(routeID uint, col string, v any) {
	s.t.Helper()
	if err := s.env.DB.Model(&ModelRoute{}).Where("id = ?", routeID).Update(col, v).Error; err != nil {
		s.t.Fatal(err)
	}
}

// twoTier creates a model with a priority-1 and a priority-2 route on separate upstreams.
func (s *suite) twoTier(model string) (r1, r2 UpstreamModelRow) {
	u1 := s.mkUpstream("tier-one", "openai", nil)
	u2 := s.mkUpstream("tier-two", "openai", nil)
	r1 = s.bind(u1.ID, model, "real-1", []string{ProtoOpenAIChat}, map[string]any{"priority": 1})
	r2 = s.bind(u2.ID, model, "real-2", []string{ProtoOpenAIChat}, map[string]any{"priority": 2})
	return
}

func TestRoutingPriorityTiersAndExclusion(t *testing.T) {
	s := newSuite(t)
	r1, r2 := s.twoTier("tiered")
	for i := 0; i < 20; i++ {
		r, err := s.resolve("tiered")
		if err != nil || r.RouteID != r1.RouteID {
			t.Fatalf("priority 1 expected: %+v %v", r, err)
		}
	}
	r, err := s.resolve("tiered", r1.RouteID)
	if err != nil || r.RouteID != r2.RouteID {
		t.Fatalf("exclusion must fall to tier 2: %+v %v", r, err)
	}
	if _, err := s.resolve("tiered", r1.RouteID, r2.RouteID); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("all excluded: %v", err)
	}
	// Snapshot content.
	r, _ = s.resolve("tiered")
	if r.PlatformModelName != "tiered" || r.UpstreamModelName != "real-1" || r.UpstreamName != "tier-one" ||
		r.Protocol != ProtoOpenAIChat || r.APIKey != "sk-tier-one-abcdefgh" || r.BaseURL != "https://api.tier-one.example.com/v1" ||
		!strings.HasPrefix(r.BindingCode, "upm_") || r.UpstreamID == 0 || r.UpstreamModelID == 0 {
		t.Fatalf("snapshot: %+v", r)
	}
	if r.ConnectTimeout != 10*time.Second || r.ReadTimeout != 120*time.Second || r.StreamIdleTimeout != 60*time.Second {
		t.Fatalf("timeouts: %+v", r)
	}
	if r.External || r.UntrustedEndpoint {
		t.Fatal("platform routes are trusted")
	}
}

func TestRoutingErrorsAndEligibility(t *testing.T) {
	s := newSuite(t)
	if _, err := s.resolve("ghost"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	if _, err := s.resolve("personal:abc/x"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("personal prefix: %v", err)
	}
	if _, err := s.resolve("  "); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("blank: %v", err)
	}
	m := s.mkModel("lonely", nil)
	if _, err := s.resolve("lonely"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("no routes: %v", err)
	}
	up, row := s.mkChat("elig", "eligible")
	if _, err := s.resolve("eligible"); err != nil {
		t.Fatal(err)
	}
	// Task fitness: a chat route cannot serve an image task.
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "eligible", Task: "image_generation", Scope: ScopeInternal}); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("task mismatch: %v", err)
	}
	// Every layer of the chain must be active.
	s.ok("PATCH", fmt.Sprintf("%s/%d/models/%d/disable", upPath, up.ID, row.RouteID), nil)
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("disabled route: %v", err)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d/models/%d/enable", upPath, up.ID, row.RouteID), nil)
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"status": "inactive"})
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("inactive upstream: %v", err)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"status": "active"})
	s.env.DB.Model(&UpstreamModel{}).Where("id = ?", row.ID).Update("status", "inactive")
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("inactive upstream model: %v", err)
	}
	s.env.DB.Model(&UpstreamModel{}).Where("id = ?", row.ID).Update("status", "active")
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"status": "inactive"})
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("inactive model: %v", err)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"status": "active"})
	_ = m
	// Skipped silently: undecryptable keys, no active key, unsafe base URL, unimplemented protocol.
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", "garbage")
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("undecryptable keys: %v", err)
	}
	enc, _ := s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "k", Status: "disabled"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", enc)
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("no active key: %v", err)
	}
	enc, _ = s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "good-key-12345"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", enc)
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("base_url", "http://169.254.169.254")
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("unsafe url: %v", err)
	}
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("base_url", "https://ok.example.com")
	if r, err := s.resolve("eligible"); err != nil || r.APIKey != "good-key-12345" {
		t.Fatalf("recovered: %+v %v", r, err)
	}
	s.setRoute(row.RouteID, "protocol", ProtoOpenAIVideoGen)
	if _, err := s.resolve("eligible"); !errors.Is(err, ErrRoutesUnavailable) && !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("not implemented protocol: %v", err)
	}
}

func TestRoutingWeightedDistribution(t *testing.T) {
	s := newSuite(t)
	u1 := s.mkUpstream("w-one", "openai", nil)
	u2 := s.mkUpstream("w-two", "openai", nil)
	a := s.bind(u1.ID, "weighted", "a", []string{ProtoOpenAIChat}, map[string]any{"weight": 100})
	b := s.bind(u2.ID, "weighted", "b", []string{ProtoOpenAIChat}, map[string]any{"weight": 300})
	count := map[uint]int{}
	const n = 4000
	for i := 0; i < n; i++ {
		r, err := s.resolve("weighted")
		if err != nil {
			t.Fatal(err)
		}
		count[r.RouteID]++
	}
	if f := float64(count[a.RouteID]) / n; f < 0.20 || f > 0.30 {
		t.Fatalf("weight 100 share %.3f (want about 0.25): %v", f, count)
	}
	// A zero weight counts as 100.
	s.setRoute(a.RouteID, "weight", 0)
	s.setRoute(b.RouteID, "weight", 100)
	count = map[uint]int{}
	for i := 0; i < n; i++ {
		r, _ := s.resolve("weighted")
		count[r.RouteID]++
	}
	if f := float64(count[a.RouteID]) / n; f < 0.44 || f > 0.56 {
		t.Fatalf("zero weight share %.3f (want about 0.5): %v", f, count)
	}
}

func TestRoutingKeySelection(t *testing.T) {
	s := newSuite(t)
	mk := func(name, strategy string) uint {
		raw := fmt.Sprintf(`{"strategy":%q,"keys":[{"key":"k1-aaaaaaaa"},{"key":"k2-bbbbbbbb","status":"disabled"},{"key":"k3-cccccccc"},{"key":"k4-dddddddd","status":"active"}]}`, strategy)
		up := s.mkUpstream(name, "openai", map[string]any{"apiKeys": raw})
		s.bind(up.ID, name, name, []string{ProtoOpenAIChat}, nil)
		return up.ID
	}
	mk("keys-failover", "failover")
	for i := 0; i < 5; i++ {
		if r, _ := s.resolve("keys-failover"); r.APIKey != "k1-aaaaaaaa" {
			t.Fatalf("failover must always take the first active key: %s", r.APIKey)
		}
	}
	mk("keys-rr", "round_robin")
	var got []string
	for i := 0; i < 6; i++ {
		r, _ := s.resolve("keys-rr")
		got = append(got, r.APIKey)
	}
	want := []string{"k3-cccccccc", "k4-dddddddd", "k1-aaaaaaaa"}
	// The rotation starts at an arbitrary offset but cycles through active keys only.
	idx := -1
	for i, k := range want {
		if k == got[0] {
			idx = i
		}
	}
	if idx < 0 {
		t.Fatalf("round robin picked a disabled key: %v", got)
	}
	for i, k := range got {
		if k != want[(idx+i)%3] {
			t.Fatalf("round robin order: %v", got)
		}
	}
	mk("keys-random", "chaos")
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		r, _ := s.resolve("keys-random")
		if r.APIKey == "k2-bbbbbbbb" {
			t.Fatal("disabled key must never be used")
		}
		seen[r.APIKey] = true
	}
	if len(seen) != 3 {
		t.Fatalf("random strategy should reach every active key: %v", seen)
	}
}

func TestRoutingHeaderMergeAndSnapshotFields(t *testing.T) {
	if got := mergeHeaders(`{"X-A":"1","Auth":"u"}`, `{"auth":"r"}`); got != `{"X-A":"1","auth":"r"}` {
		t.Fatalf("merge: %s", got)
	}
	if got := mergeHeaders(`{"A":"1"}`, `[1,2]`); got != `{"A":"1"}` {
		t.Fatalf("non-object route headers ignored: %s", got)
	}
	if got := mergeHeaders(``, ``); got != `{}` && got != `` {
		t.Fatalf("empty merge: %q", got)
	}
	s := newSuite(t)
	up := s.mkUpstream("snap", "openai", map[string]any{"headersJSON": `{"X-A":"1","Auth":"u"}`, "connectTimeoutMS": 1500, "readTimeoutMS": 2500, "streamIdleTimeoutMS": 3500,
		"cbFailureThreshold": 9, "cbModelThreshold": 2, "cbThresholdLogic": "and", "cbDurationMin": 8, "cbWindowMin": 4})
	row := s.bind(up.ID, "snap-model", "snap-up", []string{ProtoOpenAIChat}, map[string]any{"headersJSON": `{"auth":"r"}`, "cbFailureThreshold": 3, "cbDurationMin": 2, "cbWindowMin": 1})
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"systemPrompt": "  be nice  ", "cbPolicyMode": "enforced", "cbFailureThreshold": 6,
		"capabilitiesJSON": `{"contextWindow":50000}`})
	r, err := s.resolve("snap-model")
	if err != nil {
		t.Fatal(err)
	}
	if r.HeadersJSON != `{"X-A":"1","auth":"r"}` || r.SystemPrompt != "be nice" || r.ModelPolicyMode != "enforced" {
		t.Fatalf("snapshot: %+v", r)
	}
	if r.ConnectTimeout != 1500*time.Millisecond || r.ReadTimeout != 2500*time.Millisecond || r.StreamIdleTimeout != 3500*time.Millisecond {
		t.Fatalf("timeouts: %+v", r)
	}
	if r.UpstreamBreaker != (BreakerSettings{FailureThreshold: 9, ModelThreshold: 2, ThresholdLogic: "and", DurationMin: 8, WindowMin: 4}) ||
		r.ModelBreaker.FailureThreshold != 6 || r.RouteBreaker != (BreakerSettings{FailureThreshold: 3, DurationMin: 2, WindowMin: 1}) {
		t.Fatalf("breaker settings: %+v %+v %+v", r.UpstreamBreaker, r.ModelBreaker, r.RouteBreaker)
	}
	if !strings.Contains(r.CapabilitiesJSON, "50000") || r.Vendor == "" {
		t.Fatalf("model fields: %+v", r)
	}
}

func TestRoutingPassbackFlags(t *testing.T) {
	cases := []struct {
		protocol, vendor, model string
		on                      bool
		opts                    bool
	}{
		{ProtoOpenRouterChat, "openai", "gpt-4o", true, false},
		{ProtoOpenAIChat, "deepseek", "deepseek-r1", true, false},
		{ProtoOpenAIChat, "moonshot", "kimi-k2", true, false},
		{ProtoOpenAIChat, "zhipu", "glm-4.5", true, false},
		{ProtoOpenAIChat, "xiaomi", "mimo", true, false},
		{ProtoOpenAIChat, "minimax", "minimax-m2", true, false},
		{ProtoOpenAIChat, "alibaba", "qwen3", true, true},
		{ProtoOpenAIChat, "openai", "gpt-4o", false, false},
		{ProtoOpenAIResponses, "deepseek", "deepseek-r1", false, false},
		{ProtoAnthropicMessages, "anthropic", "claude", false, false},
		{ProtoOpenAIChat, "unknown", "qwen-max", true, true},
	}
	for _, c := range cases {
		on, opts := passbackFor(c.protocol, c.vendor, c.model)
		if on != c.on || (opts != nil) != c.opts {
			t.Errorf("%s/%s/%s: got %v %v", c.protocol, c.vendor, c.model, on, opts)
		}
		if c.opts && opts["preserve_thinking"] != true {
			t.Errorf("alibaba option: %v", opts)
		}
	}
}

func TestRoutingCircuitBreakerSkips(t *testing.T) {
	s := newSuite(t)
	up, row := s.mkChat("cbskip", "cb-model")
	// Disabled breaker: an open key does not matter.
	_ = s.svc.circuits.openManual(bg, upstreamPrefix(up.ID))
	if _, err := s.resolve("cb-model"); err != nil {
		t.Fatalf("breaker disabled must ignore circuits: %v", err)
	}
	s.setBreaker(`{"enabled":true}`) // clears every circuit
	if _, err := s.resolve("cb-model"); err != nil {
		t.Fatalf("closed after global enable: %v", err)
	}
	s.ok("POST", fmt.Sprintf("%s/%d/circuit/open", upPath, up.ID), nil)
	if _, err := s.resolve("cb-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("open upstream circuit: %v", err)
	}
	s.ok("POST", fmt.Sprintf("%s/%d/circuit/reset", upPath, up.ID), nil)
	s.ok("POST", fmt.Sprintf("%s/%d/models/%d/circuit/open", upPath, up.ID, row.RouteID), nil)
	if _, err := s.resolve("cb-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("open binding circuit: %v", err)
	}
	s.ok("POST", fmt.Sprintf("%s/%d/models/%d/circuit/reset", upPath, up.ID, row.RouteID), nil)
	if _, err := s.resolve("cb-model"); err != nil {
		t.Fatalf("after reset: %v", err)
	}
	// A skipped tier falls through to the next one.
	up2 := s.mkUpstream("cbskip2", "openai", nil)
	r2 := s.bind(up2.ID, "cb-model", "other", []string{ProtoOpenAIChat}, map[string]any{"priority": 2})
	s.ok("POST", fmt.Sprintf("%s/%d/circuit/open", upPath, up.ID), nil)
	r, err := s.resolve("cb-model")
	if err != nil || r.RouteID != r2.RouteID {
		t.Fatalf("fall through: %+v %v", r, err)
	}
	// Turning the breaker off and on again clears manual circuits.
	s.setBreaker(`{"enabled":false}`)
	s.setBreaker(`{"enabled":true}`)
	if r, err = s.resolve("cb-model"); err != nil || r.UpstreamID != up.ID {
		t.Fatalf("state cleared on enable: %+v %v", r, err)
	}
}

func failure(status int) error { return &UpstreamError{StatusCode: status, Err: errors.New("boom")} }

func TestFailureClassification(t *testing.T) {
	s := newSuite(t)
	up, row := s.mkChat("cls", "cls-model")
	s.setBreaker(`{"enabled":true,"model_failure_threshold":1}`)
	reset := func() {
		_ = s.svc.circuits.reset(bg, upstreamPrefix(up.ID), true)
		_ = s.svc.circuits.reset(bg, bindingPrefix(up.ID, row.BindingCode), false)
		_ = s.svc.circuits.clearBackoff(bg, up.ID, row.RouteID)
	}
	open := func() bool {
		o, _, _ := s.svc.circuits.peekOpen(bg, bindingPrefix(up.ID, row.BindingCode))
		return o
	}
	backoff := func() time.Duration {
		d, _ := s.svc.circuits.backoffRemaining(bg, up.ID, row.RouteID)
		return d
	}
	route := func() *Route {
		r, err := s.resolve("cls-model")
		if err != nil {
			t.Fatal(err)
		}
		return r
	}
	// 500 trips; 404 and cancellation do not; deadline trips.
	s.svc.MarkFailure(bg, route(), failure(404))
	if open() || backoff() > 0 {
		t.Fatal("404 must be ignored")
	}
	s.svc.MarkFailure(bg, route(), context.Canceled)
	s.svc.MarkFailure(bg, route(), nil)
	if open() {
		t.Fatal("cancellation and nil must be ignored")
	}
	s.svc.MarkFailure(bg, route(), failure(500))
	if !open() {
		t.Fatal("500 must trip the circuit")
	}
	reset()
	s.svc.MarkFailure(bg, route(), context.DeadlineExceeded)
	if !open() {
		t.Fatal("deadline must trip the circuit")
	}
	reset()
	s.svc.MarkFailure(bg, route(), io.ErrUnexpectedEOF)
	if !open() {
		t.Fatal("connection errors trip the circuit")
	}
	reset()
	// 429 backs off, no circuit.
	s.svc.MarkFailure(bg, route(), failure(429))
	if open() || backoff() <= 0 {
		t.Fatalf("429: open=%v backoff=%v", open(), backoff())
	}
	reset()
	// Removing 429 from the rate limit list makes it a circuit failure.
	s.env.DB.Model(&settingRow{}).Where("namespace = ? AND \"key\" = ?", "llm", "circuit_breaker.error_classification").
		Update("value", `{"circuit_errors":["5xx"],"rate_limit_errors":["x"],"ignore_errors":["4xx"]}`)
	s.svc.MarkFailure(bg, route(), failure(429))
	if !open() || backoff() > 0 {
		t.Fatalf("429 without list entry: open=%v backoff=%v", open(), backoff())
	}
	reset()
	// Removing 4xx from the ignore list makes 404 count.
	s.env.DB.Model(&settingRow{}).Where("namespace = ? AND \"key\" = ?", "llm", "circuit_breaker.error_classification").
		Update("value", `{"circuit_errors":["5xx"],"rate_limit_errors":[" 429 "],"ignore_errors":["nothing"]}`)
	s.svc.MarkFailure(bg, route(), failure(404))
	if !open() {
		t.Fatal("404 must count when 4xx is not ignored")
	}
	reset()
	// Matching is trim + case-insensitive.
	s.svc.MarkFailure(bg, route(), failure(429))
	if backoff() <= 0 {
		t.Fatal("padded 429 entry must match")
	}
	reset()
	// Breaker disabled: only a 429 back-off is recorded, no circuit.
	s.setBreaker(`{"enabled":false}`)
	s.svc.MarkFailure(bg, route(), failure(500))
	if open() {
		t.Fatal("disabled breaker never trips")
	}
	// External routes are ignored.
	s.svc.MarkFailure(bg, &Route{UpstreamID: 0}, failure(500))
	s.svc.MarkSuccess(bg, nil)
}

func TestRateLimitBackoff(t *testing.T) {
	s := newSuite(t)
	u1 := s.mkUpstream("bo1", "openai", nil)
	u2 := s.mkUpstream("bo2", "openai", nil)
	a := s.bind(u1.ID, "bo-model", "a", []string{ProtoOpenAIChat}, nil)
	b := s.bind(u2.ID, "bo-model", "b", []string{ProtoOpenAIChat}, nil)
	ra := func() *Route {
		for i := 0; i < 200; i++ {
			r, err := s.resolve("bo-model")
			if err != nil {
				t.Fatal(err)
			}
			if r.RouteID == a.RouteID {
				return r
			}
		}
		t.Fatal("route a never drawn")
		return nil
	}
	err429 := &UpstreamError{StatusCode: 429, Err: errors.New("slow down"), Header: http.Header{"Retry-After": []string{"12"}}}
	s.svc.MarkFailure(bg, ra(), err429)
	// Route a is skipped for at least 12 s; the sibling is still served.
	for i := 0; i < 50; i++ {
		r, err := s.resolve("bo-model")
		if err != nil || r.RouteID != b.RouteID {
			t.Fatalf("sibling expected: %+v %v", r, err)
		}
	}
	if d, _ := s.svc.circuits.backoffRemaining(bg, u1.ID, a.RouteID); d < 12*time.Second || d > 60*time.Second {
		t.Fatalf("backoff %v", d)
	}
	// Everything backing off: the rate-limited error exposes the shortest wait.
	s.svc.MarkFailure(bg, &Route{RouteID: b.RouteID, UpstreamID: u2.ID, BindingCode: b.BindingCode}, &UpstreamError{StatusCode: 429, Err: errors.New("x")})
	_, err := s.resolve("bo-model")
	if !errors.Is(err, ErrUpstreamLimited) {
		t.Fatalf("expected rate limited, got %v", err)
	}
	wait, ok := RetryAfterOf(err)
	if !ok || wait <= 0 || wait > 12*time.Second {
		t.Fatalf("retry after: %v %v", wait, ok)
	}
	// Time passing releases the route.
	s.env.Clock.Advance(61 * time.Second)
	if _, err := s.resolve("bo-model"); err != nil {
		t.Fatalf("after wait: %v", err)
	}
	// Success clears the back-off.
	s.svc.MarkFailure(bg, &Route{RouteID: a.RouteID, UpstreamID: u1.ID, BindingCode: a.BindingCode}, err429)
	s.svc.MarkSuccess(bg, &Route{RouteID: a.RouteID, UpstreamID: u1.ID, BindingCode: a.BindingCode})
	if d, _ := s.svc.circuits.backoffRemaining(bg, u1.ID, a.RouteID); d != 0 {
		t.Fatalf("success must clear back-off: %v", d)
	}
	// Exponential growth, capped by the maximum; Retry-After is bounded by the maximum too.
	cfg := backoffConfig{BaseSec: 5, MaxSec: 60, Multiplier: 2}
	var last time.Duration
	for i := 0; i < 6; i++ {
		d, err := s.svc.circuits.recordBackoff(bg, 900, 901, cfg, 0)
		if err != nil {
			t.Fatal(err)
		}
		if i < 4 && d != time.Duration(5<<i)*time.Second {
			t.Fatalf("step %d: %v", i, d)
		}
		last = d
	}
	if last != 60*time.Second {
		t.Fatalf("cap: %v", last)
	}
	if d, _ := s.svc.circuits.recordBackoff(bg, 902, 903, cfg, 500*time.Second); d != 60*time.Second {
		t.Fatalf("retry-after above max: %v", d)
	}
	if d, _ := s.svc.circuits.recordBackoff(bg, 904, 905, backoffConfig{}, 0); d != 5*time.Second {
		t.Fatalf("normalized defaults: %v", d)
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 3, 1, 9, 0, 0, 0, time.UTC)
	h := func(v string) http.Header { return http.Header{"Retry-After": []string{v}} }
	if got := parseRetryAfter(h("7"), now); got != 7*time.Second {
		t.Errorf("seconds: %v", got)
	}
	if got := parseRetryAfter(h("-5"), now); got != 0 {
		t.Errorf("negative: %v", got)
	}
	if got := parseRetryAfter(h(now.Add(90*time.Second+500*time.Millisecond).UTC().Format(http.TimeFormat)), now); got != 91*time.Second && got != 90*time.Second {
		t.Errorf("http date: %v", got)
	}
	if got := parseRetryAfter(h(now.Add(-time.Hour).UTC().Format(http.TimeFormat)), now); got != 0 {
		t.Errorf("past date: %v", got)
	}
	if got := parseRetryAfter(h("soon"), now); got != 0 {
		t.Errorf("garbage: %v", got)
	}
	if got := parseRetryAfter(http.Header{}, now); got != 0 {
		t.Errorf("absent: %v", got)
	}
	lower := http.Header{}
	lower["retry-after"] = []string{"3"} // non-canonical key
	_ = lower
}

func TestCircuitThresholdMath(t *testing.T) {
	s := newSuite(t)
	u := s.mkUpstream("thr", "openai", nil)
	row := s.bind(u.ID, "thr-model", "thr-up", []string{ProtoOpenAIChat}, nil)
	s.setBreaker(`{"enabled":true}`) // model threshold 5 in 3 minutes, 15 minutes open
	bind := bindingPrefix(u.ID, row.BindingCode)
	fail := func() {
		r, err := s.resolve("thr-model")
		if err != nil {
			t.Fatalf("resolve: %v", err)
		}
		s.svc.MarkFailure(bg, r, failure(503))
	}
	isOpen := func(prefix string) (bool, int64) {
		o, until, _ := s.svc.circuits.peekOpen(bg, prefix)
		return o, until
	}
	for i := 0; i < 4; i++ {
		fail()
		if o, _ := isOpen(bind); o {
			t.Fatalf("opened too early after %d failures", i+1)
		}
	}
	fail()
	o, until := isOpen(bind)
	if !o {
		t.Fatal("5th failure must open the binding circuit")
	}
	if want := s.env.Clock.Now().Add(15 * time.Minute).Unix(); until < want-1 || until > want+1 {
		t.Fatalf("open for 15 minutes: until %d want %d", until, want)
	}
	// Failures outside the window do not accumulate.
	_ = s.svc.circuits.reset(bg, bind, true)
	for i := 0; i < 4; i++ {
		r, _ := s.resolve("thr-model")
		s.svc.MarkFailure(bg, r, failure(503))
	}
	s.env.Clock.Advance(4 * time.Minute)
	r, _ := s.resolve("thr-model")
	s.svc.MarkFailure(bg, r, failure(503))
	if o, _ := isOpen(bind); o {
		t.Fatal("old failures must age out of the window")
	}
}

func TestCircuitPolicyModes(t *testing.T) {
	// Route overrides win in default mode; the platform model wins when enforced.
	for _, mode := range []string{"default", "enforced"} {
		t.Run(mode, func(t *testing.T) {
			s := newSuite(t)
			u := s.mkUpstream("pol", "openai", nil)
			row := s.bind(u.ID, "pol-model", "pol-up", []string{ProtoOpenAIChat}, map[string]any{"cbFailureThreshold": 2, "cbDurationMin": 7})
			s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"cbPolicyMode": mode, "cbFailureThreshold": 3, "cbDurationMin": 20})
			s.setBreaker(`{"enabled":true}`)
			bind := bindingPrefix(u.ID, row.BindingCode)
			trips := 0
			for i := 1; i <= 4; i++ {
				r, _ := s.resolve("pol-model")
				s.svc.MarkFailure(bg, r, failure(500))
				if o, _, _ := s.svc.circuits.peekOpen(bg, bind); o && trips == 0 {
					trips = i
				}
			}
			_, until, _ := s.svc.circuits.peekOpen(bg, bind)
			mins := time.Duration(until-s.env.Clock.Now().Unix()) * time.Second / time.Minute
			if mode == "default" && (trips != 2 || mins < 6 || mins > 7) {
				t.Fatalf("route overrides: tripped at %d for %d min", trips, mins)
			}
			if mode == "enforced" && (trips != 3 || mins < 19 || mins > 20) {
				t.Fatalf("model policy: tripped at %d for %d min", trips, mins)
			}
		})
	}
}

func TestUpstreamCircuitRules(t *testing.T) {
	setup := func(t *testing.T, logic string) (*suite, uint, []UpstreamModelRow) {
		s := newSuite(t)
		u := s.mkUpstream("rule", "openai", map[string]any{"cbThresholdLogic": logic, "cbFailureThreshold": 1000, "cbModelThreshold": 3})
		var rows []UpstreamModelRow
		for i := 0; i < 3; i++ {
			rows = append(rows, s.bind(u.ID, fmt.Sprintf("rule-%d", i), fmt.Sprintf("rule-up-%d", i), []string{ProtoOpenAIChat}, map[string]any{"cbFailureThreshold": 1}))
		}
		s.setBreaker(`{"enabled":true}`)
		return s, u.ID, rows
	}
	failModel := func(s *suite, i int) {
		r, err := s.resolve(fmt.Sprintf("rule-%d", i))
		if err != nil {
			s.t.Fatalf("resolve rule-%d: %v", i, err)
		}
		s.svc.MarkFailure(bg, r, failure(500))
	}
	t.Run("or", func(t *testing.T) {
		s, id, _ := setup(t, "or")
		failModel(s, 0)
		failModel(s, 1)
		if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(id)); o {
			t.Fatal("two open bindings are below the model threshold")
		}
		failModel(s, 2)
		if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(id)); !o {
			t.Fatal("three open bindings must trip the upstream with the or rule")
		}
	})
	t.Run("and", func(t *testing.T) {
		s, id, _ := setup(t, "and")
		failModel(s, 0)
		failModel(s, 1)
		failModel(s, 2)
		if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(id)); o {
			t.Fatal("and needs the upstream failure count too")
		}
	})
	t.Run("and with both satisfied", func(t *testing.T) {
		s := newSuite(t)
		u := s.mkUpstream("rule", "openai", map[string]any{"cbThresholdLogic": "and", "cbFailureThreshold": 3, "cbModelThreshold": 2})
		for i := 0; i < 3; i++ {
			th := 1
			if i == 2 {
				th = 100
			}
			s.bind(u.ID, fmt.Sprintf("rule-%d", i), fmt.Sprintf("rule-up-%d", i), []string{ProtoOpenAIChat}, map[string]any{"cbFailureThreshold": th})
		}
		s.setBreaker(`{"enabled":true}`)
		failModel(s, 0)
		failModel(s, 1)
		if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(u.ID)); o {
			t.Fatal("2 failures are below the upstream threshold of 3")
		}
		failModel(s, 2)
		if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(u.ID)); !o {
			t.Fatal("both conditions hold now")
		}
	})
}

func TestHalfOpenProbe(t *testing.T) {
	s := newSuite(t)
	u := s.mkUpstream("half", "openai", nil)
	row := s.bind(u.ID, "half-model", "half-up", []string{ProtoOpenAIChat}, map[string]any{"cbFailureThreshold": 1, "cbDurationMin": 1})
	s.setBreaker(`{"enabled":true}`)
	r, _ := s.resolve("half-model")
	s.svc.MarkFailure(bg, r, failure(500))
	if _, err := s.resolve("half-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("open: %v", err)
	}
	// After the open period the first request becomes the probe; a concurrent one is refused.
	s.env.Clock.Advance(61 * time.Second)
	probe, err := s.resolve("half-model")
	if err != nil || !probe.BindingProbeGranted {
		t.Fatalf("probe: %+v %v", probe, err)
	}
	if _, err := s.resolve("half-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("second request during probe: %v", err)
	}
	// Success clears the circuit.
	s.svc.MarkSuccess(bg, probe)
	if r, err := s.resolve("half-model"); err != nil || r.BindingProbeGranted {
		t.Fatalf("closed after probe success: %+v %v", r, err)
	}
	// Probe failure re-opens at once.
	s.svc.MarkFailure(bg, r, failure(500))
	s.env.Clock.Advance(61 * time.Second)
	probe, err = s.resolve("half-model")
	if err != nil || !probe.BindingProbeGranted {
		t.Fatalf("second probe: %+v %v", probe, err)
	}
	s.svc.MarkFailure(bg, probe, failure(500))
	if _, err := s.resolve("half-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("probe failure must re-open: %v", err)
	}
	// A probe that was ignored (4xx) is released so the next request can probe.
	s.env.Clock.Advance(61 * time.Second)
	probe, err = s.resolve("half-model")
	if err != nil || !probe.BindingProbeGranted {
		t.Fatalf("third probe: %+v %v", probe, err)
	}
	s.svc.MarkFailure(bg, probe, failure(400))
	if p2, err := s.resolve("half-model"); err != nil || !p2.BindingProbeGranted {
		t.Fatalf("released probe should be grantable again: %+v %v", p2, err)
	}
	_ = row
}

func TestRoutingAccessControl(t *testing.T) {
	s := newSuite(t)
	_, row := s.mkChat("acc", "acc-model")
	// Internal scope vs user scope on an internal model.
	m := s.mkModel("secret-model", map[string]any{"accessScope": "internal"})
	up := s.mkUpstream("acc2", "openai", nil)
	s.bind(up.ID, m.PlatformModelName, "sec", []string{ProtoOpenAIChat}, nil)
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "secret-model", Scope: ScopeUser}); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("internal model, user scope: %v", err)
	}
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "secret-model", Scope: "weird"}); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("unknown scope behaves as user: %v", err)
	}
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "secret-model", Scope: ScopeInternal, UserID: 9}); err != nil {
		t.Fatalf("internal scope: %v", err)
	}
	// User id 0 skips group checks; a user needs a group.
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "acc-model"}); err != nil {
		t.Fatalf("user 0: %v", err)
	}
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "acc-model", UserID: 7}); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("model without groups denies: %v", err)
	}
	g, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "vip"})
	_ = s.svc.SetModelGroups(bg, row.PlatformModelID, []uint{g.ID})
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "acc-model", UserID: 7}); !errors.Is(err, ErrModelAccessDenied) {
		t.Fatalf("user outside the group: %v", err)
	}
	_ = s.svc.SetGroupUsers(bg, g.ID, []uint{7})
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "acc-model", UserID: 7}); err != nil {
		t.Fatalf("user in group: %v", err)
	}
	// Internal scope ignores groups entirely.
	if _, err := s.svc.ResolveRoute(bg, RouteRequest{Model: "acc-model", UserID: 8, Scope: ScopeInternal}); err != nil {
		t.Fatalf("internal scope ignores groups: %v", err)
	}
}

func TestResolveDefaultRoute(t *testing.T) {
	s := newSuite(t)
	if _, err := s.svc.ResolveDefaultRoute(bg, "chat"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("empty catalog: %v", err)
	}
	// First model in display order that can serve the task and resolves.
	u := s.mkUpstream("def", "openai", nil)
	s.bind(u.ID, "gpt-image-1", "img", []string{}, nil)
	s.bind(u.ID, "chatty", "chatty", []string{ProtoOpenAIChat}, nil)
	r, err := s.svc.ResolveDefaultRoute(bg, "chat")
	if err != nil || r.PlatformModelName != "chatty" {
		t.Fatalf("chat default: %+v %v", r, err)
	}
	r, err = s.svc.ResolveDefaultRoute(bg, "image_generation")
	if err != nil || r.PlatformModelName != "gpt-image-1" || r.Protocol != ProtoOpenAIImageGen {
		t.Fatalf("image default: %+v %v", r, err)
	}
	if _, err := s.svc.ResolveDefaultRoute(bg, "video_generation"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("video default: %v", err)
	}
}

func TestShouldFailOver(t *testing.T) {
	type netTimeout struct{ error }
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"503", failure(503), true},
		{"500", failure(500), true},
		{"408", failure(408), true},
		{"429", failure(429), true},
		{"400", failure(400), false},
		{"404", failure(404), false},
		{"422", failure(422), false},
		{"eof", io.EOF, true},
		{"unexpected eof", io.ErrUnexpectedEOF, true},
		{"wrapped eof", fmt.Errorf("read: %w", io.EOF), true},
		{"accepted eof", &UpstreamError{Err: io.EOF, Accepted: true}, false},
		{"accepted 503", &UpstreamError{StatusCode: 503, Accepted: true}, false},
		{"cancel", context.Canceled, false},
		{"wrapped cancel", fmt.Errorf("x: %w", context.Canceled), false},
		{"deadline", context.DeadlineExceeded, true},
		{"generic", errors.New("validation failed"), false},
	}
	for _, c := range cases {
		if got := ShouldFailOver(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
	_ = netTimeout{}
}

func TestTaskHelpers(t *testing.T) {
	for in, want := range map[string]string{"": "chat", "weird": "chat", " Image_Generation ": "image_generation", "image_edit": "image_edit",
		"video_generation": "video_generation", "video_extension": "video_extension", "chat": "chat"} {
		if got := NormalizeTaskType(in); got != want {
			t.Errorf("normalize %q: %s", in, got)
		}
	}
	cases := []struct {
		kinds    []string
		protocol string
		task     string
		want     bool
	}{
		{[]string{"chat"}, ProtoOpenAIChat, "chat", true},
		{[]string{"audio"}, ProtoOpenAIChat, "chat", true},
		{[]string{"audio"}, ProtoGeminiInteractions, "chat", false},
		{[]string{"chat"}, ProtoOpenAIImageGen, "chat", false},
		{[]string{"chat"}, ProtoOpenAIChat, "image_generation", false},
		{[]string{"image_gen"}, ProtoOpenAIImageGen, "image_generation", true},
		{[]string{"image_gen"}, ProtoOpenAIImageEdit, "image_edit", false},
		{[]string{"image_edit"}, ProtoOpenAIImageEdit, "image_edit", true},
		{[]string{"video_gen"}, ProtoXAIVideo, "video_generation", true},
		{[]string{"video_gen", "video_extension"}, ProtoXAIVideoExtension, "video_generation", false},
		{[]string{"video_gen", "video_extension"}, ProtoXAIVideoExtension, "video_extension", true},
		{[]string{"video_extension"}, ProtoXAIVideo, "video_extension", false},
		{nil, ProtoOpenAIChat, "chat", true},
		{nil, ProtoOpenAIImageGen, "chat", false},
		{nil, ProtoOpenAIImageGen, "image_generation", true},
		{[]string{"chat", "image_gen"}, ProtoGeminiInteractions, "image_generation", true},
	}
	for i, c := range cases {
		if got := RouteServesTask(c.kinds, c.protocol, c.task); got != c.want {
			t.Errorf("case %d (%v %s %s): got %v", i, c.kinds, c.protocol, c.task, got)
		}
	}
	if !ModelSupportsTask(nil, "image_edit") || ModelSupportsTask([]string{"chat"}, "image_edit") || !ModelSupportsTask([]string{"chat"}, "") ||
		!ModelSupportsTask([]string{"video_gen"}, "video_generation") || ModelSupportsTask([]string{"video_gen"}, "video_extension") ||
		ModelSupportsTask([]string{"image_gen"}, "chat") {
		t.Error("ModelSupportsTask")
	}
}
