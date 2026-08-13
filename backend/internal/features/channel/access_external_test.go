package channel

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"
)

type userCount int64

func (u userCount) CountAllUsers(_ context.Context) (int64, error) { return int64(u), nil }

func TestRateMultiplier(t *testing.T) {
	s := newSuite(t)
	_, row := s.mkChat("rm", "rm-model")
	mid := row.PlatformModelID
	cheap, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "cheap", RateMultiplierPercent: 50})
	pricey, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "pricey", RateMultiplierPercent: 200})
	zero, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "zero", RateMultiplierPercent: 0})
	if zero.RateMultiplierPercent != 100 {
		t.Fatalf("<= 0 is stored as 100: %+v", zero)
	}
	rm := func(user, model uint) int {
		t.Helper()
		v, err := s.svc.RateMultiplier(bg, user, model)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if rm(1, mid) != 100 || rm(0, mid) != 100 || rm(1, 0) != 100 || rm(1, 9999) != 100 {
		t.Fatal("no user groups / no model / unknown model -> 100")
	}
	_ = s.svc.SetModelGroups(bg, mid, []uint{cheap.ID, pricey.ID})
	if rm(1, mid) != 100 {
		t.Fatal("empty intersection -> 100")
	}
	_ = s.svc.SetGroupUsers(bg, pricey.ID, []uint{1})
	if rm(1, mid) != 200 {
		t.Fatalf("single shared group: %d", rm(1, mid))
	}
	_ = s.svc.SetGroupUsers(bg, cheap.ID, []uint{1})
	if rm(1, mid) != 50 {
		t.Fatalf("minimum among shared groups: %d", rm(1, mid))
	}
	// Groups the model is not in do not count, even if cheaper.
	cheaper, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "cheaper", RateMultiplierPercent: 10})
	_ = s.svc.SetGroupUsers(bg, cheaper.ID, []uint{1})
	if rm(1, mid) != 50 {
		t.Fatalf("model groups only: %d", rm(1, mid))
	}
	// Dynamic rules put the model into a group as well.
	_ = s.svc.SetGroupRules(bg, cheaper.ID, []GroupRule{{Type: RuleProtocol, Value: ProtoOpenAIChat}})
	if rm(1, mid) != 10 {
		t.Fatalf("rule-matched group: %d", rm(1, mid))
	}
	// Default groups apply to everyone; subscription groups when resolved.
	_ = s.svc.UpdatePermissionGroup(bg, withID(PermissionGroup{Name: "pricey", IsDefault: true, RateMultiplierPercent: 200}, pricey.ID))
	if rm(77, mid) != 200 {
		t.Fatalf("default group: %d", rm(77, mid))
	}
	s.svc.opts.Subscriptions = subFn(func(uid uint) (uint, bool) { return cheap.ID, uid == 77 })
	if rm(77, mid) != 50 {
		t.Fatalf("subscription group: %d", rm(77, mid))
	}
}

func TestPermissionGroupMaintenance(t *testing.T) {
	s := newSuite(t)
	_, a := s.mkChat("pgm1", "pgm-a")
	_, b := s.mkChat("pgm2", "pgm-b")
	g, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "g", Description: "d"})
	other, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "other"})

	// Model sets are replaced wholesale; duplicates and zero ids are dropped.
	if err := s.svc.SetGroupModels(bg, g.ID, []uint{a.PlatformModelID, a.PlatformModelID, 0, b.PlatformModelID}); err != nil {
		t.Fatal(err)
	}
	var n int64
	s.env.DB.Model(&PermissionGroupModelAccess{}).Where("group_id = ?", g.ID).Count(&n)
	if n != 2 {
		t.Fatalf("model links: %d", n)
	}
	_ = s.svc.SetGroupModels(bg, g.ID, []uint{b.PlatformModelID})
	s.env.DB.Model(&PermissionGroupModelAccess{}).Where("group_id = ?", g.ID).Count(&n)
	if n != 1 {
		t.Fatalf("replaced: %d", n)
	}
	// Rules are keyed by type+value.
	_ = s.svc.SetGroupRules(bg, g.ID, []GroupRule{{RuleVendor, "openai"}, {RuleVendor, "openai"}, {RuleAll, ""}, {"", "x"}, {RuleProtocol, ""}})
	s.env.DB.Model(&PermissionGroupModelRule{}).Where("group_id = ?", g.ID).Count(&n)
	if n != 2 {
		t.Fatalf("rules: %d", n)
	}
	// Setting a model's groups does not touch dynamic rules.
	_ = s.svc.SetModelGroups(bg, a.PlatformModelID, []uint{g.ID, other.ID, g.ID, 0})
	s.env.DB.Model(&PermissionGroupModelRule{}).Where("group_id = ?", g.ID).Count(&n)
	if n != 2 {
		t.Fatalf("rules untouched: %d", n)
	}
	got, _ := s.svc.ManualModelGroups(bg, a.PlatformModelID)
	if fmt.Sprint(got) != fmt.Sprint([]uint{g.ID, other.ID}) {
		t.Fatalf("manual groups: %v", got)
	}
	_ = s.svc.SetGroupUsers(bg, g.ID, []uint{5, 5, 6, 0})
	s.env.DB.Model(&PermissionGroupUserAccess{}).Where("group_id = ?", g.ID).Count(&n)
	if n != 2 {
		t.Fatalf("user links: %d", n)
	}

	// Summaries.
	s.svc.opts.Users = userCount(42)
	_ = s.svc.UpdatePermissionGroup(bg, withID(PermissionGroup{Name: "other", IsDefault: true}, other.ID))
	list, err := s.svc.ListPermissionGroups(bg)
	if err != nil || len(list) != 2 {
		t.Fatalf("summaries: %v %v", list, err)
	}
	gs, os := list[0], list[1]
	if gs.Name != "g" || gs.ManualModelCount != 2 || gs.RuleModelCount != 2 || gs.ModelCount != 2 || gs.UserCount != 2 || gs.ManualUserCount != 2 {
		t.Fatalf("g: %+v", gs)
	}
	if os.UserCount != 42 || os.ManualModelCount != 1 || !os.IsDefault {
		t.Fatalf("default group counts all users: %+v", os)
	}

	// Deleting a group removes its links, rules and user links.
	if err := s.svc.DeletePermissionGroup(bg, g.ID); err != nil {
		t.Fatal(err)
	}
	for _, m := range []any{&PermissionGroupModelAccess{}, &PermissionGroupModelRule{}, &PermissionGroupUserAccess{}} {
		var c int64
		s.env.DB.Model(m).Where("group_id = ?", g.ID).Count(&c)
		if c != 0 {
			t.Fatalf("%T left %d rows", m, c)
		}
	}
	// The other group's link survives.
	got, _ = s.svc.ManualModelGroups(bg, a.PlatformModelID)
	if fmt.Sprint(got) != fmt.Sprint([]uint{other.ID}) {
		t.Fatalf("remaining: %v", got)
	}
}

func TestModelDeleteRemovesManualLinksOnly(t *testing.T) {
	s := newSuite(t)
	_, a := s.mkChat("ml1", "ml-a")
	g, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "g"})
	_ = s.svc.SetGroupRules(bg, g.ID, []GroupRule{{RuleAll, ""}})
	_ = s.svc.SetModelGroups(bg, a.PlatformModelID, []uint{g.ID})
	s.ok("DELETE", fmt.Sprintf("%s/%d", mdPath, a.PlatformModelID), nil)
	var n int64
	s.env.DB.Model(&PermissionGroupModelAccess{}).Count(&n)
	if n != 0 {
		t.Fatal("manual link must go")
	}
	s.env.DB.Model(&PermissionGroupModelRule{}).Count(&n)
	if n != 1 {
		t.Fatal("rules are not model links and stay")
	}
}

// ---- external (personal) provider helpers ---------------------------------------------------------------

func TestSuggestExternalProtocols(t *testing.T) {
	cases := []struct {
		provider, model string
		want            string
	}{
		{ProtoOpenAIChat, "gpt-4o", "openai_chat_completions"},
		{ProtoAnthropicMessages, "claude-3", "anthropic_messages"},
		{ProtoOpenAIChat, "gpt-image-1", "openai_image_generations,openai_image_edits"},
		{ProtoOpenAIResponses, "openai/gpt-image-1", "openai_image_generations,openai_image_edits"},
		{ProtoOpenAIChat, "dall-e-3", "openai_image_generations"},
		{ProtoOpenAIChat, "grok-imagine-image-1", "xai_image,xai_image_edits"},
		{ProtoXAIResponses, "grok-imagine-video-1", "xai_video,xai_video_extensions"},
		{ProtoOpenAIChat, "grok-imagine-video-1", "xai_video,xai_video_extensions"},
		{ProtoGoogleGenerate, "gemini-3-pro-image", "google_image_generation"},
		{ProtoGoogleGenerate, "gemini-omni-flash-x", "gemini_interactions"},
		{ProtoOpenRouterChat, "x/gemini-3-pro-image", "openrouter_images"},
		// Video on a style whose protocol has no transport falls back to the provider protocol.
		{ProtoOpenAIChat, "sora", "openai_chat_completions"},
		// Anthropic cannot serve images: inference fails -> provider protocol.
		{ProtoAnthropicMessages, "dall-e-3", "anthropic_messages"},
	}
	for _, c := range cases {
		if got := strings.Join(SuggestExternalProtocols(c.provider, c.model), ","); got != c.want {
			t.Errorf("%s/%s: %s want %s", c.provider, c.model, got, c.want)
		}
	}
}

func TestNormalizeExternalProtocols(t *testing.T) {
	if got, err := NormalizeExternalProtocols([]string{" OpenAI_Image_Edits ", "openai_image_generations", "openai_image_edits"}); err != nil || strings.Join(got, ",") != "openai_image_edits,openai_image_generations" {
		t.Errorf("pair: %v %v", got, err)
	}
	for in, want := range map[string]error{
		"bogus":                    ErrInvalidAdapter,
		"openai_video_generations": ErrInvalidAdapter, // known but not implemented
		"":                         ErrProtocolRequired,
	} {
		if _, err := NormalizeExternalProtocols([]string{in}); !errors.Is(err, want) {
			t.Errorf("%q: %v want %v", in, err, want)
		}
	}
	if _, err := NormalizeExternalProtocols(nil); !errors.Is(err, ErrProtocolRequired) {
		t.Errorf("empty: %v", err)
	}
	if _, err := NormalizeExternalProtocols([]string{"openai_chat_completions", "anthropic_messages"}); !errors.Is(err, ErrInvalidCombination) {
		t.Errorf("combination: %v", err)
	}
}

func TestExternalKindsAndTaskProtocol(t *testing.T) {
	cases := []struct {
		model     string
		protocols []string
		want      string
	}{
		{"gpt-4o", []string{ProtoOpenAIChat}, "chat"},
		{"vendor/gpt-image-1", []string{ProtoOpenAIImageGen, ProtoOpenAIImageEdit}, "image_gen,image_edit"},
		{"gpt-image-1", []string{ProtoOpenAIImageGen}, "image_gen"},
		// Inferred kinds that no protocol supports: everything the protocols support except audio.
		{"mystery-model", []string{ProtoOpenAIChat}, "chat"},
		{"gpt-4o-audio-preview", []string{ProtoOpenAIChat}, "audio"},
		{"gpt-image-1", []string{ProtoOpenAIChat}, "chat"},
		{"claude-3", []string{ProtoGeminiInteractions}, "chat"},
		{"gpt-4o-audio-preview", []string{ProtoGeminiInteractions}, "chat,image_gen,image_edit,video_gen"},
	}
	for _, c := range cases {
		if got := strings.Join(ExternalKinds(c.model, c.protocols), ","); got != c.want {
			t.Errorf("%s %v: %s want %s", c.model, c.protocols, got, c.want)
		}
	}
	kinds := []string{"image_gen", "image_edit"}
	protocols := []string{ProtoOpenAIImageGen, ProtoOpenAIImageEdit}
	if p, err := ExternalProtocolForTask(protocols, kinds, "image_edit"); err != nil || p != ProtoOpenAIImageEdit {
		t.Errorf("edit: %q %v", p, err)
	}
	if p, err := ExternalProtocolForTask(protocols, kinds, "image_generation"); err != nil || p != ProtoOpenAIImageGen {
		t.Errorf("gen: %q %v", p, err)
	}
	if _, err := ExternalProtocolForTask(protocols, kinds, "chat"); !errors.Is(err, ErrRouteNotFound) {
		t.Errorf("no protocol for task: %v", err)
	}
	// The first listed protocol that can serve wins.
	if p, _ := ExternalProtocolForTask([]string{ProtoOpenRouterImages, ProtoOpenAIImageEdit}, []string{"image_gen", "image_edit"}, "image_edit"); p != ProtoOpenRouterImages {
		t.Errorf("order: %q", p)
	}
}

func TestBuildExternalRoute(t *testing.T) {
	s := newCatalogSuite(t)
	prov := ExternalProvider{Name: "My DeepSeek", Icon: "ds", Host: "api.mine.test", Protocol: ProtoOpenAIChat, BaseURL: " https://api.mine.test/v1 ", APIKey: "user-secret"}
	r, err := s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: prov, Model: "deepseek-r1", Reference: "personal:prov_1/deepseek-r1"})
	if err != nil {
		t.Fatal(err)
	}
	if !r.External || !r.UntrustedEndpoint || r.UpstreamID != 0 || r.RouteID != 0 || r.Protocol != ProtoOpenAIChat || r.BaseURL != "https://api.mine.test/v1" ||
		r.APIKey != "user-secret" || r.UpstreamModelName != "deepseek-r1" || r.PlatformModelName != "personal:prov_1/deepseek-r1" {
		t.Fatalf("route: %+v", r)
	}
	if r.ConnectTimeout != 10*time.Second || r.ReadTimeout != 120*time.Second || r.StreamIdleTimeout != 60*time.Second {
		t.Fatalf("fixed timeouts: %+v", r)
	}
	if r.Vendor != "deepseek" || r.Icon != "deepseek" || !r.ReasoningPassback {
		t.Fatalf("vendor/passback: %+v", r)
	}
	// Failures are never counted against external routes.
	s.svc.MarkFailure(bg, r, failure(500))
	s.svc.MarkSuccess(bg, r)
	// Catalog facts are attached: the OpenRouter-style id match for modalities.
	r, err = s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: ExternalProvider{Name: "OR", Protocol: ProtoOpenRouterChat, BaseURL: "https://openrouter.ai/api/v1"}, Model: "acme/secret-model"})
	if err != nil || strings.Join(r.InputModalities, ",") != "text,image,audio" || !r.ReasoningPassback || r.Protocol != ProtoOpenRouterChat {
		t.Fatalf("openrouter route: %+v %v", r, err)
	}
	// Reasoning from the catalog for the route's protocol.
	r, err = s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: ExternalProvider{Name: "OA", Protocol: ProtoOpenAIResponses, BaseURL: "https://a.test"}, Model: "gpt-5"})
	if err != nil || r.Reasoning == nil || r.Reasoning.Source != "catalog" || strings.Join(r.InputModalities, ",") != "text,image,pdf" {
		t.Fatalf("gpt-5: %+v %v", r, err)
	}
	// Alibaba models carry the passback option.
	r, _ = s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: ExternalProvider{Name: "Q", Protocol: ProtoOpenAIChat, BaseURL: "https://a.test"}, Model: "qwen3-max"})
	if !r.ReasoningPassback || r.PassbackOptions["preserve_thinking"] != true {
		t.Fatalf("qwen passback: %+v", r)
	}
	// Task selection and errors.
	img := ExternalRouteSpec{Provider: ExternalProvider{Name: "I", Protocol: ProtoOpenAIChat, BaseURL: "https://a.test"}, Model: "gpt-image-1", Task: "image_edit"}
	if r, err = s.svc.BuildExternalRoute(bg, img); err != nil || r.Protocol != ProtoOpenAIImageEdit {
		t.Fatalf("image edit: %+v %v", r, err)
	}
	img.Task = "chat"
	if _, err = s.svc.BuildExternalRoute(bg, img); !errors.Is(err, ErrRouteNotFound) {
		t.Fatalf("chat on an image model: %v", err)
	}
	if _, err = s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: prov, Model: "  "}); !errors.Is(err, ErrModelNotFound) {
		t.Fatalf("blank model: %v", err)
	}
	if _, err = s.svc.BuildExternalRoute(bg, ExternalRouteSpec{Provider: prov, Model: "x", Protocols: []string{"bogus"}}); !errors.Is(err, ErrInvalidAdapter) {
		t.Fatalf("bad protocol: %v", err)
	}
}

func TestBuildExternalModelView(t *testing.T) {
	s := newCatalogSuite(t)
	prov := ExternalProvider{Name: "Mine", Icon: "mine-icon", Host: "api.mine.test", Protocol: ProtoOpenAIResponses}
	m := s.svc.BuildExternalModel(ExternalModelSpec{Provider: prov, Model: "gpt-5", Reference: "personal:prov_1/gpt-5"})
	if m.PlatformModelName != "personal:prov_1/gpt-5" || m.Source != "personal" || m.Description != "gpt-5" || m.Pricing != nil || m.CapabilitiesJSON != "{}" ||
		m.ProviderName != "Mine" || m.ProviderIcon != "mine-icon" || m.ProviderHost != "api.mine.test" || m.KindsJSON != `["chat"]` {
		t.Fatalf("model: %+v", m)
	}
	if m.Vendor != "openai" || m.VendorName != "openai" || m.ProtocolsJSON != `["openai_responses"]` {
		t.Fatalf("vendor: %+v", m)
	}
	if m.Reasoning == nil || m.Reasoning.Source != "catalog" || m.ContextWindow == nil || *m.ContextWindow != 400000 {
		t.Fatalf("catalog facts: %+v", m)
	}
	if m.Controls == nil || m.InputModalities == nil || m.OutputModalities == nil {
		t.Fatalf("slices must not be nil: %+v", m)
	}
	// Image models take the media protocols.
	img := s.svc.BuildExternalModel(ExternalModelSpec{Provider: ExternalProvider{Name: "I", Protocol: ProtoOpenAIChat}, Model: "gpt-image-1"})
	if img.KindsJSON != `["image_gen","image_edit"]` || img.ProtocolsJSON != `["openai_image_generations","openai_image_edits"]` {
		t.Fatalf("image model: %+v", img)
	}
	// An OpenRouter-style id resolves modalities regardless of protocol.
	or := s.svc.BuildExternalModel(ExternalModelSpec{Provider: ExternalProvider{Name: "Z", Protocol: ProtoOpenAIChat}, Model: "acme/secret-model"})
	if strings.Join(or.InputModalities, ",") != "text,image,audio" || or.InputModalitiesSource == nil || *or.InputModalitiesSource != "catalog" {
		t.Fatalf("external match: %+v", or)
	}
}

func TestCapabilityResolverBatch(t *testing.T) {
	s := newCatalogSuite(t)
	r := s.svc.CapabilityResolver()
	got := r.Resolve(CapabilityInput{Name: "gpt-5", Vendor: "OpenAI", Protocols: []string{ProtoOpenAIResponses}})
	if got.Reasoning == nil || got.Reasoning.Source != "catalog" || got.ContextWindow == nil || *got.ContextWindow != 400000 || got.InputModalitiesSource == nil {
		t.Fatalf("catalog model: %+v", got)
	}
	got = r.Resolve(CapabilityInput{Name: "gpt-5", Protocols: []string{ProtoOpenAIResponses}, CapabilitiesJSON: `{"contextWindow":50000,"inputModalities":["audio"],"reasoning":{"format":"openai","levels":["low"],"default":"low"}}`})
	if got.Reasoning.Source != "explicit" || *got.ContextWindow != 50000 || *got.InputModalitiesSource != "explicit" || strings.Join(got.InputModalities, ",") != "text,audio" {
		t.Fatalf("explicit overrides: %+v", got)
	}
	got = r.Resolve(CapabilityInput{Name: "acme/secret-model", Protocols: []string{ProtoOpenAIChat}, External: true})
	if len(got.InputModalities) == 0 {
		t.Fatalf("external variant: %+v", got)
	}
	got = r.Resolve(CapabilityInput{Name: "acme/secret-model", Protocols: []string{ProtoOpenAIChat}})
	if len(got.InputModalities) != 0 || got.ContextWindow != nil {
		t.Fatalf("platform variant: %+v", got)
	}
	// The resolver stays bound to its snapshot while the catalog is replaced.
	s.fetcher.set(apiJSON(map[string]map[string]apiModel{"openai": {"other": mdModel("other", false, nil, []string{"text"}, 8000)}}), nil)
	if _, err := s.svc.RefreshCatalog(bg); err != nil {
		t.Fatal(err)
	}
	if again := r.Resolve(CapabilityInput{Name: "gpt-5", Vendor: "openai", Protocols: []string{ProtoOpenAIResponses}}); again.Reasoning == nil {
		t.Fatal("a resolver sees one consistent snapshot")
	}
	if fresh := s.svc.CapabilityResolver().Resolve(CapabilityInput{Name: "gpt-5", Vendor: "openai", Protocols: []string{ProtoOpenAIResponses}}); fresh.Reasoning != nil {
		t.Fatal("a new resolver sees the refreshed catalog")
	}
	if w := s.svc.EffectiveContextWindow(bg, "gpt-4.1-mini", ""); w != 1047576 {
		t.Errorf("effective window: %d", w)
	}
	if w := s.svc.EffectiveContextWindow(bg, "mystery", `{"contextWindow":70000}`); w != 70000 {
		t.Errorf("effective window override: %d", w)
	}
}

func withID(g PermissionGroup, id uint) PermissionGroup {
	g.ID = id
	return g
}
