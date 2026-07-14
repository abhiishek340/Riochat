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
