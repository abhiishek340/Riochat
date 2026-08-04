package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func (s *suite) userModels(userID uint) []PublicModel {
	s.t.Helper()
	w := s.env.Do("GET", "/api/v1/models", s.usr(userID), nil)
	if w.Code != 200 {
		s.t.Fatalf("GET /models: %d %s", w.Code, w.Body.String())
	}
	return decodeData[[]PublicModel](s.t, w)
}

func modelNames(list []PublicModel) string {
	var out []string
	for _, m := range list {
		out = append(out, m.PlatformModelName)
	}
	return strings.Join(out, ",")
}

// openGroup makes every user a member of a group that matches every model.
func (s *suite) openGroup() *PermissionGroup {
	g, err := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "everyone", IsDefault: true})
	if err != nil {
		s.t.Fatal(err)
	}
	if err := s.svc.SetGroupRules(bg, g.ID, []GroupRule{{Type: RuleAll}}); err != nil {
		s.t.Fatal(err)
	}
	return g
}

func TestUserModelListRequiresAuth(t *testing.T) {
	s := newSuite(t)
	s.expect(s.env.Do("GET", "/api/v1/models", nil, nil), 401, "")
	// An empty catalog is an empty array, never null.
	w := s.env.Do("GET", "/api/v1/models", s.usr(1), nil)
	s.expect(w, 200, "")
	if !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("body: %s", w.Body.String())
	}
}

func TestUserModelListFiltering(t *testing.T) {
	s := newSuite(t)
	s.mkChat("pub", "visible")
	s.mkChat("pub2", "other-visible")
	if got := s.userModels(5); len(got) != 0 {
		t.Fatalf("no model has any group: the list must be empty, got %s", modelNames(got))
	}
	g := s.openGroup()
	if got := modelNames(s.userModels(5)); got != "visible,other-visible" {
		t.Fatalf("default group: %s", got)
	}
	// A model reachable only through a non-default group G.
	_ = s.svc.SetGroupRules(bg, g.ID, nil)
	vip, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "vip"})
	var visible PlatformModel
	s.env.DB.Where("name = ?", "visible").First(&visible)
	_ = s.svc.SetModelGroups(bg, visible.ID, []uint{vip.ID})
	_ = s.svc.SetGroupUsers(bg, vip.ID, []uint{5})
	if got := modelNames(s.userModels(5)); got != "visible" {
		t.Fatalf("member of G: %s", got)
	}
	if got := s.userModels(6); len(got) != 0 {
		t.Fatalf("outsider must not see G's model, and the other model has no group: %s", modelNames(got))
	}
	// Dynamic rules: vendor, protocol, upstream.
	_ = s.svc.SetGroupRules(bg, vip.ID, []GroupRule{{Type: RuleProtocol, Value: ProtoOpenAIChat}})
	if got := modelNames(s.userModels(5)); got != "visible,other-visible" {
		t.Fatalf("protocol rule: %s", got)
	}
	var up2 Upstream
	s.env.DB.Where("name = ?", "pub2").First(&up2)
	_ = s.svc.SetGroupRules(bg, vip.ID, []GroupRule{{Type: RuleUpstream, Value: fmt.Sprint(up2.ID)}})
	if got := modelNames(s.userModels(5)); got != "visible,other-visible" {
		t.Fatalf("upstream rule (plus manual grant): %s", got)
	}
	_ = s.svc.SetModelGroups(bg, visible.ID, nil)
	if got := modelNames(s.userModels(5)); got != "other-visible" {
		t.Fatalf("upstream rule only: %s", got)
	}
	_ = s.svc.SetGroupRules(bg, vip.ID, []GroupRule{{Type: RuleVendor, Value: "unknown"}, {Type: "mystery", Value: "x"}})
	if got := modelNames(s.userModels(5)); got != "visible,other-visible" {
		t.Fatalf("vendor rule: %s", got)
	}
	_ = s.svc.SetGroupRules(bg, vip.ID, []GroupRule{{Type: "mystery", Value: "x"}})
	if got := s.userModels(5); len(got) != 0 {
		t.Fatalf("unknown rule types match nothing: %s", modelNames(got))
	}
	_ = s.svc.SetGroupRules(bg, vip.ID, []GroupRule{{Type: RuleAll}})
	// Subscription plan groups.
	s.svc.opts.Subscriptions = subFn(func(uid uint) (uint, bool) { return vip.ID, uid == 9 })
	_ = s.svc.SetGroupUsers(bg, vip.ID, nil)
	if got := modelNames(s.userModels(9)); got != "visible,other-visible" {
		t.Fatalf("subscription group: %s", got)
	}
	if got := s.userModels(5); len(got) != 0 {
		t.Fatalf("no membership for user 5: %s", modelNames(got))
	}
	// Internal callers (user 0) bypass group filtering.
	list, err := s.svc.ListActiveModels(bg, 0)
	if err != nil || len(list) != 2 {
		t.Fatalf("internal list: %v %v", list, err)
	}
}

type subFn func(uint) (uint, bool)

func (f subFn) SubscriptionGroupID(_ context.Context, uid uint, _ time.Time) (uint, bool, error) {
	g, ok := f(uid)
	return g, ok, nil
}

func TestUserModelListVisibilityRules(t *testing.T) {
	s := newSuite(t)
	s.openGroup()
	upA, rowA := s.mkChat("va", "model-a")
	_, _ = s.mkChat("vb", "model-b")
	internal := s.mkModel("model-internal", map[string]any{"accessScope": "internal"})
	inactive := s.mkModel("model-inactive", map[string]any{"status": "inactive"})
	s.mkModel("model-unrouted", nil)
	for _, m := range []ModelView{internal, inactive} {
		up := s.mkUpstream("u-"+m.PlatformModelName, "openai", nil)
		s.bind(up.ID, m.PlatformModelName, "x", []string{ProtoOpenAIChat}, nil)
	}
	if got := modelNames(s.userModels(3)); got != "model-a,model-b" {
		t.Fatalf("only public, active, routable models: %s", got)
	}
	// Disabling a route / upstream is reflected immediately after the admin write.
	s.ok("PATCH", fmt.Sprintf("%s/%d/models/%d/disable", upPath, upA.ID, rowA.RouteID), nil)
	if got := modelNames(s.userModels(3)); got != "model-b" {
		t.Fatalf("route disabled: %s", got)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d/models/%d/enable", upPath, upA.ID, rowA.RouteID), nil)
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, upA.ID), map[string]any{"status": "inactive"})
	if got := modelNames(s.userModels(3)); got != "model-b" {
		t.Fatalf("upstream inactive: %s", got)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, upA.ID), map[string]any{"status": "active"})
	if got := modelNames(s.userModels(3)); got != "model-a,model-b" {
		t.Fatalf("restored: %s", got)
	}
	// The catalog is cached per billing mode for 30 seconds; direct database writes show up afterwards.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", rowA.RouteID).Update("status", "inactive")
	if got := modelNames(s.userModels(3)); got != "model-a,model-b" {
		t.Fatalf("cache must hold: %s", got)
	}
	s.env.Clock.Advance(31 * time.Second)
	if got := modelNames(s.userModels(3)); got != "model-b" {
		t.Fatalf("cache must expire: %s", got)
	}
	// An explicit invalidation (called by billing on price changes) refreshes at once.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", rowA.RouteID).Update("status", "active")
	s.svc.InvalidateCatalog()
	if got := modelNames(s.userModels(3)); got != "model-a,model-b" {
		t.Fatalf("invalidate: %s", got)
	}
}

func TestUserModelListPricing(t *testing.T) {
	s := newSuite(t)
	s.openGroup()
	s.mkChat("pr1", "priced")
	s.mkChat("pr2", "unpriced")
	// Self billing: everything listed, no prices.
	list := s.userModels(1)
	if len(list) != 2 || list[0].Pricing != nil {
		t.Fatalf("self mode: %+v", list)
	}
	// Priced mode with a filter: only models with a price entry, with the price attached.
	price := &Pricing{Currency: "USD", Mode: "token", InputUSDPerMTokens: 1.5, OutputUSDPerMTokens: 6, Tiers: []PricingTier{}, SchedulePeriods: []PricingPeriod{}}
	s.pricing.mode, s.pricing.filter, s.pricing.prices = "usage", true, map[string]*Pricing{"priced": price}
	s.svc.InvalidateCatalog()
	list = s.userModels(1)
	if modelNames(list) != "priced" || list[0].Pricing == nil || list[0].Pricing.InputUSDPerMTokens != 1.5 || list[0].Pricing.Currency != "USD" {
		t.Fatalf("priced mode: %+v", list)
	}
	// Switching the mode builds a separate cache entry.
	s.pricing.mode = BillingModeSelf
	if got := modelNames(s.userModels(1)); got != "priced,unpriced" {
		t.Fatalf("mode switch: %s", got)
	}
	// Prices without a configured filter keep everything.
	s.pricing.mode, s.pricing.filter = "usage", false
	s.svc.InvalidateCatalog()
	if got := modelNames(s.userModels(1)); got != "priced,unpriced" {
		t.Fatalf("no price filter: %s", got)
	}
	// Returned copies never leak mutations into the cache.
	l1, _ := s.svc.ListActiveModels(bg, 0)
	l1[0].PlatformModelName = "mutated"
	l1[0].InputModalities = append(l1[0].InputModalities, "x")
	l2, _ := s.svc.ListActiveModels(bg, 0)
	if l2[0].PlatformModelName == "mutated" {
		t.Fatal("cache must hand out copies")
	}
}

func TestUserModelListPersonalModels(t *testing.T) {
	s := newSuite(t)
	s.openGroup()
	s.mkChat("pm", "platform-model")
	s.pers.models = []PublicModel{{PlatformModelName: "personal:prov_1/gpt-x", Source: "personal", Description: "gpt-x", ProviderName: "Mine", ProviderHost: "api.mine.test"}}
	list := s.userModels(1)
	if len(list) != 2 || list[0].Source != "platform" || list[1].PlatformModelName != "personal:prov_1/gpt-x" || list[1].Source != "personal" {
		t.Fatalf("personal models are appended: %+v", list)
	}
	s.pers.models, s.pers.err = nil, errors.New("provider store down")
	list = s.userModels(1)
	if len(list) != 1 || list[0].PlatformModelName != "platform-model" {
		t.Fatalf("personal failures must not break the list: %+v", list)
	}
}

func TestPublicModelShape(t *testing.T) {
	s := newSuite(t)
	s.openGroup()
	up := s.mkUpstream("shape", "openai", nil)
	row := s.bind(up.ID, "shape-model", "shape-model", []string{ProtoOpenAIChat}, nil)
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"description": "desc", "capabilitiesJSON": `{"contextWindow":65536,"inputModalities":["image"]}`})
	w := s.env.Do("GET", "/api/v1/models", s.usr(1), nil)
	var raw struct {
		Data []map[string]json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &raw); err != nil || len(raw.Data) != 1 {
		t.Fatalf("decode: %v %s", err, w.Body.String())
	}
	m := raw.Data[0]
	for _, k := range []string{"platformModelName", "vendor", "vendorName", "vendorIcon", "displayGroupID", "displayGroupName", "displayGroupIcon", "kindsJSON",
		"icon", "protocolsJSON", "capabilitiesJSON", "description", "sortOrder", "pricing", "reasoning", "controls", "inputModalities", "inputModalitiesSource",
		"outputModalities", "contextWindow", "source", "providerName", "providerIcon", "providerHost"} {
		if _, ok := m[k]; !ok {
			t.Errorf("public model misses %q", k)
		}
	}
	if len(m) != 24 {
		t.Errorf("unexpected fields: %d", len(m))
	}
	if string(m["displayGroupID"]) != "null" || string(m["pricing"]) != "null" || string(m["reasoning"]) != "null" || string(m["controls"]) != "[]" {
		t.Errorf("nullables: %s %s %s %s", m["displayGroupID"], m["pricing"], m["reasoning"], m["controls"])
	}
	if string(m["contextWindow"]) != "65536" || string(m["inputModalities"]) != `["text","image"]` || string(m["inputModalitiesSource"]) != `"explicit"` {
		t.Errorf("capabilities: %s %s %s", m["contextWindow"], m["inputModalities"], m["inputModalitiesSource"])
	}
	if string(m["source"]) != `"platform"` || string(m["providerName"]) != `""` || string(m["protocolsJSON"]) != `"[\"openai_chat_completions\"]"` {
		t.Errorf("source/protocols: %s %s %s", m["source"], m["providerName"], m["protocolsJSON"])
	}
	// Unknown modalities and windows stay unknown (never a guessed fallback).
	s.mkChat("shape2", "mystery-model")
	s.svc.InvalidateCatalog()
	var mystery PublicModel
	for _, pm := range s.userModels(1) {
		if pm.PlatformModelName == "mystery-model" {
			mystery = pm
		}
	}
	if mystery.ContextWindow != nil || len(mystery.InputModalities) != 0 || mystery.InputModalitiesSource != nil || mystery.InputModalities == nil {
		t.Errorf("unknown facts: %+v", mystery)
	}
}

func TestPublicCatalogConcurrentBuildsAreCoalesced(t *testing.T) {
	s := newSuite(t)
	s.openGroup()
	s.mkChat("co", "coalesced")
	s.svc.InvalidateCatalog()
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := s.svc.ListActiveModels(bg, 1)
			if err == nil && len(l) != 1 {
				err = fmt.Errorf("got %d models", len(l))
			}
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestActiveNamesVideoSupportAndIdentity(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("vid", "xai", nil)
	s.bind(up.ID, "grok-imagine-video-1", "gimv", []string{}, nil)
	s.bind(up.ID, "chat-x", "chat-x", []string{ProtoXAIResponses}, nil)
	s.mkModel("no-sources", nil)
	s.mkModel("inactive-one", map[string]any{"status": "inactive"})
	internal := s.mkModel("internal-vid", map[string]any{"accessScope": "internal", "kindsJSON": `["video_gen"]`})
	s.bind(up.ID, internal.PlatformModelName, "iv", []string{ProtoXAIVideo}, nil)

	names, err := s.svc.ActiveModelNames(bg)
	sort.Strings(names)
	if err != nil || strings.Join(names, ",") != "chat-x,grok-imagine-video-1,internal-vid" {
		t.Fatalf("active names (ignores access scope): %v %v", names, err)
	}
	for name, want := range map[string]bool{"grok-imagine-video-1": true, "chat-x": false, "no-sources": false, "ghost": false, "internal-vid": true, "personal:x/y": false} {
		if got, err := s.svc.SupportsVideoGeneration(bg, name); err != nil || got != want {
			t.Errorf("video support for %s: %v %v", name, got, err)
		}
	}
	// Video support needs a routable source.
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("status", "inactive")
	if got, _ := s.svc.SupportsVideoGeneration(bg, "grok-imagine-video-1"); got {
		t.Error("no routable source")
	}
	// Identity lookup ignores status and scope.
	id, err := s.svc.ResolveModelIdentity(bg, "inactive-one")
	if err != nil || id.Name != "inactive-one" || id.ID == 0 {
		t.Fatalf("identity: %+v %v", id, err)
	}
	if _, err := s.svc.ResolveModelIdentity(bg, "ghost"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("unknown identity: %v", err)
	}
	if _, err := s.svc.ResolveModelIdentity(bg, "bad\tname"); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("invalid identity: %v", err)
	}
}

func TestNativeToolDefinitions(t *testing.T) {
	s := newSuite(t)
	s.mkModel("tools-a", map[string]any{"capabilitiesJSON": `{"nativeTools":[{"name":"web_search","type":"x"},{"name":"code","type":"y"}]}`})
	s.mkModel("tools-b", map[string]any{"capabilitiesJSON": `{"nativeTools":[{"name":"web_search","type":"dup"}]}`})
	s.mkModel("tools-c", map[string]any{"capabilitiesJSON": `{}`})
	defs, err := s.svc.NativeToolDefinitions(bg)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, d := range defs {
		var x struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal(d, &x)
		names[x.Name] = true
	}
	if !names["web_search"] || !names["code"] || len(defs) != 2 {
		t.Fatalf("definitions: %s", defs)
	}
}
