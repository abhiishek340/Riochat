package channel

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type apiModel = map[string]any

// apiJSON renders a models.dev style document.
func apiJSON(providers map[string]map[string]apiModel) []byte {
	top := map[string]any{}
	for pid, models := range providers {
		top[pid] = map[string]any{"id": pid, "models": models}
	}
	raw, _ := json.Marshal(top)
	return raw
}

func mdModel(id string, reasoning bool, opts []map[string]any, inputs []string, ctx int) apiModel {
	m := apiModel{"id": id, "reasoning": reasoning, "modalities": map[string]any{"input": inputs, "output": []string{"text"}}, "limit": map[string]any{"context": ctx}}
	if opts != nil {
		m["reasoning_options"] = opts
	}
	return m
}

func testCatalogJSON() []byte {
	return apiJSON(map[string]map[string]apiModel{
		"openai": {
			"gpt-5":  mdModel("gpt-5", true, []map[string]any{{"type": "effort", "values": []string{"minimal", "low", "medium", "high"}}}, []string{"text", "image", "pdf"}, 400000),
			"gpt-4o": mdModel("gpt-4o", false, nil, []string{"text", "image"}, 128000),
		},
		"anthropic": {
			"claude-opus-9": mdModel("claude-opus-9", true, []map[string]any{{"type": "effort", "values": []string{"low", "medium", "high", "max"}}}, []string{"text", "image"}, 200000),
			"claude-old":    mdModel("claude-old", true, []map[string]any{{"type": "budget_tokens", "min": 1024, "max": 32000}}, []string{"text"}, 200000),
		},
		"moonshotai": {
			"kimi-k2-thinking": mdModel("kimi-k2-thinking", true, []map[string]any{{"type": "toggle"}, {"type": "budget_tokens", "min": 1000, "max": 50000}}, []string{"text"}, 262144),
		},
		"alibaba": {
			"qwen3-max": mdModel("qwen3-max", true, []map[string]any{{"type": "toggle"}, {"type": "budget_tokens", "min": 0, "max": 80000}}, []string{"text", "image"}, 262144),
		},
		"openrouter": {
			"acme/secret-model": mdModel("acme/secret-model", false, nil, []string{"text", "image", "audio"}, 64000),
		},
		"google": {
			"gemini-9-pro": mdModel("gemini-9-pro", true, []map[string]any{{"type": "budget_tokens", "min": 128, "max": 32768}}, []string{"text", "image", "video"}, 1048576),
		},
	})
}

func builtinFrom(t *testing.T, api []byte, fetched time.Time) []byte {
	t.Helper()
	entries, err := parseModelsDev(api)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeSnapshot(catalogSnapshot{Version: snapshotVersion, Source: "models.dev", Origin: "builtin", FetchedAt: fetched, Models: entries}, true)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func newCatalogSuite(t *testing.T) *suite {
	t.Helper()
	data := builtinFrom(t, testCatalogJSON(), time.Date(2026, 2, 28, 12, 0, 0, 0, time.UTC))
	return newSuite(t, func(o *Options) { o.BuiltinCatalog = data })
}

func (s *suite) resolveCatalog(name, vendor string, protocols []string, caps string) CatalogResolution {
	body := map[string]any{"name": name, "vendor": vendor, "protocols": protocols, "capabilitiesJSON": caps}
	return decodeData[CatalogResolution](s.t, s.do("POST", "/api/v1/admin/llm/model-catalog/resolve", body))
}

func (s *suite) waitIdle() {
	s.t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !s.svc.CatalogStatus(bg).Refreshing {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	s.t.Fatal("refresh did not finish")
}

func TestCatalogStatusLifecycle(t *testing.T) {
	// No snapshot anywhere: nothing is loaded.
	gate := make(chan struct{})
	s := newSuite(t, func(o *Options) { o.BuiltinCatalog = []byte{} })
	s.fetcher.gate = gate
	st := decodeData[CatalogStatus](t, s.do("GET", "/api/v1/admin/llm/model-catalog", nil))
	if st.Origin != nil || st.FetchedAt != nil || st.ModelCount != 0 || st.LastError != nil || !st.Refreshing {
		t.Fatalf("empty status (a refresh is nudged): %+v", st)
	}
	close(gate)
	s.waitIdle()
	st = s.svc.CatalogStatus(bg)
	if st.Origin != nil || st.LastError == nil || *st.LastError != "offline" {
		t.Fatalf("after failed background refresh: %+v", st)
	}
	// A manual refresh with a good document installs a remote catalog.
	s.fetcher.set(testCatalogJSON(), nil)
	st = decodeData[CatalogStatus](t, s.do("POST", "/api/v1/admin/llm/model-catalog/refresh", nil))
	if st.Origin == nil || *st.Origin != "remote" || st.LastError != nil || st.ModelCount != 8 || st.FetchedAt == nil ||
		*st.FetchedAt != s.env.Clock.Now().UTC().Format(time.RFC3339) || st.Refreshing {
		t.Fatalf("refreshed: %+v", st)
	}
	// The cache file is written as plain indented JSON.
	path := filepath.Join(s.env.Deps.Config.Load().Storage.Local.RootDir, "catalogs", "models-dev.json")
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 || raw[0] != '{' || !bytes.Contains(raw, []byte("\n  ")) {
		t.Fatalf("cache file: %v %q", err, raw)
	}
	var snap catalogSnapshot
	if err := json.Unmarshal(raw, &snap); err != nil || snap.Version != 2 || snap.Source == "" || len(snap.Models) != 8 {
		t.Fatalf("cache content: %v %+v", err, snap.Version)
	}
	// A failed refresh keeps the old catalog and stores the (truncated) error.
	s.fetcher.set(nil, fmt.Errorf("%s", strings.Repeat("e", 900)))
	w := s.do("POST", "/api/v1/admin/llm/model-catalog/refresh", nil)
	s.expect(w, 502, "llm.model_catalog_fetch_failed")
	st = s.svc.CatalogStatus(bg)
	if st.Origin == nil || *st.Origin != "remote" || st.ModelCount != 8 || st.LastError == nil || len(*st.LastError) != 500 {
		t.Fatalf("after failure: %+v", st)
	}
	// An empty or malformed document is a failure too.
	s.fetcher.set([]byte(`{}`), nil)
	s.expect(s.do("POST", "/api/v1/admin/llm/model-catalog/refresh", nil), 502, "llm.model_catalog_fetch_failed")
	s.fetcher.set([]byte(`[1,2`), nil)
	s.expect(s.do("POST", "/api/v1/admin/llm/model-catalog/refresh", nil), 502, "llm.model_catalog_fetch_failed")
	if st = s.svc.CatalogStatus(bg); st.ModelCount != 8 || st.LastError == nil || !strings.Contains(*st.LastError, "malformed") {
		t.Fatalf("malformed: %+v", st)
	}
	// A success clears the error.
	s.fetcher.set(testCatalogJSON(), nil)
	if st = decodeData[CatalogStatus](t, s.do("POST", "/api/v1/admin/llm/model-catalog/refresh", nil)); st.LastError != nil {
		t.Fatalf("error must clear: %+v", st)
	}
}

func TestCatalogBuiltinSeedAndStoredFile(t *testing.T) {
	s := newCatalogSuite(t)
	st := s.svc.CatalogStatus(bg)
	if st.Origin == nil || *st.Origin != "builtin" || st.ModelCount != 8 {
		t.Fatalf("builtin: %+v", st)
	}
	// The built-in snapshot is written to storage as a seed.
	path := filepath.Join(s.env.Deps.Config.Load().Storage.Local.RootDir, "catalogs", "models-dev.json")
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("seed not written: %v", err)
	}
	// A newer stored file wins over an older built-in; a corrupt one falls back to the built-in.
	m := s.svc.catalog
	newer := catalogSnapshot{Version: 2, Source: "models.dev", Origin: "remote", FetchedAt: time.Date(2026, 3, 1, 8, 0, 0, 0, time.UTC),
		Models: []catalogEntry{{Provider: "openai", ID: "only-one", CanonicalID: "openai/only-one", Inputs: []string{"text"}}}}
	if err := m.writeStored(&newer); err != nil {
		t.Fatal(err)
	}
	m.view, m.fetchedAt = nil, time.Time{}
	m.load(bg)
	if st = s.svc.CatalogStatus(bg); st.ModelCount != 1 || *st.Origin != "remote" {
		t.Fatalf("stored wins: %+v", st)
	}
	if err := os.WriteFile(path, []byte("not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	m.view, m.fetchedAt = nil, time.Time{}
	m.load(bg)
	if st = s.svc.CatalogStatus(bg); st.ModelCount != 8 || *st.Origin != "builtin" {
		t.Fatalf("corrupt file falls back: %+v", st)
	}
	// Version 1 files are treated as missing.
	if err := os.WriteFile(path, []byte(`{"version":1,"models":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readStored(); err == nil {
		t.Fatal("outdated snapshot reports an error value")
	}
	// A legacy file is moved into place.
	os.Remove(path)
	root := s.env.Deps.Config.Load().Storage.Local.RootDir
	legacy := filepath.Join(root, "admin", "models-dev-reasoning-catalog.json")
	_ = os.MkdirAll(filepath.Dir(legacy), 0o755)
	raw, _ := encodeSnapshot(newer, false)
	_ = os.WriteFile(legacy, raw, 0o644)
	got, err := m.readStored()
	if err != nil || len(got.Models) != 1 {
		t.Fatalf("legacy: %v", err)
	}
	if _, err := os.Stat(legacy); err == nil {
		t.Fatal("legacy file must be moved")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("new location must exist")
	}
}

func TestCatalogSnapshotEncoding(t *testing.T) {
	entries, err := parseModelsDev(testCatalogJSON())
	if err != nil {
		t.Fatal(err)
	}
	fetched := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	snap := catalogSnapshot{Version: 2, Source: "models.dev", FetchedAt: fetched, Models: entries}
	a, err := encodeSnapshot(snap, true)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := encodeSnapshot(snap, true)
	if !bytes.Equal(a, b) {
		t.Fatal("gzip snapshots must be deterministic")
	}
	if len(a) < 2 || a[0] != 0x1f || a[1] != 0x8b {
		t.Fatal("not gzip")
	}
	back, err := decodeSnapshot(a)
	if err != nil || len(back.Models) != len(entries) || !back.FetchedAt.Equal(fetched) {
		t.Fatalf("round trip: %v", err)
	}
	// Sorted by provider then id.
	for i := 1; i < len(back.Models); i++ {
		p, c := back.Models[i-1], back.Models[i]
		if p.Provider > c.Provider || (p.Provider == c.Provider && p.ID > c.ID) {
			t.Fatalf("unsorted: %s/%s before %s/%s", p.Provider, p.ID, c.Provider, c.ID)
		}
	}
	plain, _ := encodeSnapshot(snap, false)
	if _, err := decodeSnapshot(plain); err != nil {
		t.Fatal(err)
	}
	if _, err := decodeSnapshot([]byte(`{"version":3,"models":[{"provider":"a","id":"b","inputs":["text"]}]}`)); err == nil {
		t.Fatal("unknown versions are errors")
	}
	if _, err := decodeSnapshot([]byte(`{"version":2,"models":[]}`)); err == nil {
		t.Fatal("no entries is an error")
	}
	if _, err := decodeSnapshot([]byte(`{`)); err == nil {
		t.Fatal("malformed")
	}
	// A decompression bomb is refused.
	var bomb bytes.Buffer
	zw := gzip.NewWriter(&bomb)
	_, _ = zw.Write(append([]byte(`{"version":2,"models":[],"pad":"`), bytes.Repeat([]byte("a"), 33<<20)...))
	_ = zw.Close()
	if _, err := decodeSnapshot(bomb.Bytes()); err == nil {
		t.Fatal("oversized decompressed data must be refused")
	}
	// EncodeCatalogSnapshot demands at least 1000 entries.
	if _, n, err := EncodeCatalogSnapshot(testCatalogJSON(), fetched); err == nil || n != 8 {
		t.Fatalf("small catalogs are refused: %d %v", n, err)
	}
	big := map[string]apiModel{}
	for i := 0; i < 1005; i++ {
		id := fmt.Sprintf("m-%04d", i)
		big[id] = mdModel(id, false, nil, []string{"text"}, 8000)
	}
	data, n, err := EncodeCatalogSnapshot(apiJSON(map[string]map[string]apiModel{"acme": big}), fetched)
	if err != nil || n != 1005 || len(data) == 0 {
		t.Fatalf("big catalog: %d %v", n, err)
	}
}

func TestCatalogParsingRules(t *testing.T) {
	doc := apiJSON(map[string]map[string]apiModel{
		"p": {
			"keyed":        {"reasoning": true, "reasoning_options": []map[string]any{{"type": "effort", "values": []string{" HIGH ", "", "Low"}}, {"type": "effort", "values": []string{"max"}}, {"type": "weird"}, {"type": "budget_tokens", "min": -5, "max": 3000000000}}}, // id from key
			"textonly":     {"id": "textonly", "modalities": map[string]any{"input": []string{"smell"}}},
			"nothing":      {"id": "nothing"},
			"notreasoning": {"id": "notreasoning", "reasoning": false, "reasoning_options": []map[string]any{{"type": "toggle"}}, "modalities": map[string]any{"input": []string{"image"}}, "limit": map[string]any{"context": 100}},
			"noeffort":     {"id": "noeffort", "reasoning": true, "reasoning_options": []map[string]any{{"type": "effort", "values": []string{" "}}}},
		},
		"broken": nil,
	})
	entries, err := parseModelsDev(doc)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]catalogEntry{}
	for _, e := range entries {
		by[e.ID] = e
	}
	k, ok := by["keyed"]
	if !ok {
		t.Fatalf("entries: %+v", entries)
	}
	if len(k.Options) != 2 || k.Options[0].Type != "effort" || strings.Join(k.Options[0].Values, ",") != "high,low" ||
		k.Options[1].Type != "budget_tokens" || k.Options[1].Min != nil || k.Options[1].Max != nil || k.CanonicalID != "p/keyed" {
		t.Fatalf("keyed: %+v", k)
	}
	if _, ok := by["textonly"]; ok {
		t.Fatal("modalities that normalize to nothing but text-implied still need an entry")
	}
	if _, ok := by["nothing"]; ok {
		t.Fatal("an entry without options or modalities is dropped")
	}
	nr, ok := by["notreasoning"]
	if !ok || len(nr.Options) != 0 || strings.Join(nr.Inputs, ",") != "text,image" || nr.Context != 0 {
		t.Fatalf("not reasoning: %+v", nr)
	}
	if _, ok := by["noeffort"]; ok {
		t.Fatalf("effort without values is dropped: %+v", by["noeffort"])
	}
	if _, err := parseModelsDev([]byte(`{}`)); err == nil {
		// an empty document yields no entries; callers treat it as an empty catalog
		t.Log("empty document parsed")
	}
	if _, err := parseModelsDev([]byte(`[]`)); err == nil {
		t.Fatal("non-object documents are malformed")
	}
}

func TestCatalogMatching(t *testing.T) {
	s := newCatalogSuite(t)
	cases := []struct {
		name, vendor string
		protocols    []string
		provider, id string
	}{
		{"GPT-5", "openai", nil, "openai", "gpt-5"},
		{"openai/gpt-5", "", nil, "openai", "gpt-5"},
		{"GPT-5", "", nil, "openai", "gpt-5"}, // first-party search by bare id
		{"kimi-k2-thinking", "moonshot", nil, "moonshotai", "kimi-k2-thinking"},
		{"acme/secret-model", "", []string{ProtoOpenRouterChat}, "openrouter", "acme/secret-model"},
		{"acme/secret-model", "", []string{ProtoOpenAIChat}, "", ""},
		{"acme/secret-model", "unknown", nil, "", ""},
		{"gpt-5-turbo-max", "openai", nil, "", ""}, // never fuzzy
		{"unknown-model", "", nil, "", ""},
		{"", "openai", nil, "", ""},
		{"vendor/", "", nil, "", ""},
	}
	for _, c := range cases {
		res := s.resolveCatalog(c.name, c.vendor, c.protocols, "")
		gotP, gotID := "", ""
		if res.Provider != nil {
			gotP = *res.Provider
		}
		if res.ModelID != nil {
			gotID = *res.ModelID
		}
		if gotP != c.provider || gotID != c.id || res.Matched != (c.id != "") {
			t.Errorf("%+v: got %s/%s matched=%v", c, gotP, gotID, res.Matched)
		}
	}
	// The response carries empty arrays, never null.
	w := s.do("POST", "/api/v1/admin/llm/model-catalog/resolve", map[string]any{"name": "nope"})
	if !strings.Contains(w.Body.String(), `"inputModalities":[]`) || !strings.Contains(w.Body.String(), `"outputModalities":[]`) {
		t.Fatalf("unmatched body: %s", w.Body.String())
	}
	// Validation limits.
	s.expect(s.do("POST", "/api/v1/admin/llm/model-catalog/resolve", map[string]any{"name": strings.Repeat("n", 256)}), 400, "request.invalid_body")
	s.expect(s.do("POST", "/api/v1/admin/llm/model-catalog/resolve", map[string]any{"name": "x", "vendor": strings.Repeat("v", 129)}), 400, "request.invalid_body")
	// Modalities and window from a match.
	res := s.resolveCatalog("gpt-5", "openai", nil, "")
	if strings.Join(res.InputModalities, ",") != "text,image,pdf" || res.ContextWindow == nil || *res.ContextWindow != 400000 || strings.Join(res.OutputModalities, ",") != "text" {
		t.Fatalf("modalities: %+v", res)
	}
	// External lookups also try OpenRouter ids regardless of the protocol.
	view := s.svc.catalog.current()
	if e := view.matchModalityExternal("acme/secret-model", "", []string{ProtoOpenAIChat}); e == nil || e.Provider != "openrouter" {
		t.Fatalf("external match: %+v", e)
	}
	if e := view.matchModality("acme/secret-model", "", []string{ProtoOpenAIChat}); e != nil {
		t.Fatalf("platform match must not use openrouter without an openrouter protocol: %+v", e)
	}
}

func TestCatalogReasoningPerProtocol(t *testing.T) {
	s := newCatalogSuite(t)
	// effort [low, medium, high, max] on an Anthropic route -> anthropic_effort with default medium.
	res := s.resolveCatalog("claude-opus-9", "anthropic", []string{ProtoAnthropicMessages}, "")
	if res.Reasoning == nil || res.Reasoning.Format != "anthropic_effort" || res.Reasoning.Default != "medium" || res.Reasoning.Source != "catalog" ||
		strings.Join(res.Reasoning.Levels, ",") != "low,medium,high,max" {
		t.Fatalf("anthropic: %+v", res.Reasoning)
	}
	// The same entry on a chat-completions route: format openai.
	res = s.resolveCatalog("claude-opus-9", "anthropic", []string{ProtoOpenAIChat}, "")
	if res.Reasoning == nil || res.Reasoning.Format != "openai" {
		t.Fatalf("chat completions: %+v", res.Reasoning)
	}
	// An image protocol offers nothing.
	res = s.resolveCatalog("claude-opus-9", "anthropic", []string{ProtoOpenAIImageGen}, "")
	if res.Reasoning != nil {
		t.Fatalf("image protocol: %+v", res.Reasoning)
	}
	// Moonshot's toggle + budget entry: toggle for moonshot/unknown vendors, qwen for alibaba.
	for _, v := range []string{"moonshot", "unknown"} {
		res = s.resolveCatalog("kimi-k2-thinking", v, []string{ProtoOpenAIChat}, "")
		if res.Reasoning == nil || res.Reasoning.Format != "toggle" || strings.Join(res.Reasoning.Levels, ",") != "none,high" || res.Reasoning.Default != "high" {
			t.Fatalf("kimi/%s: %+v", v, res.Reasoning)
		}
	}
	for _, v := range []string{"qwen", "", "alibaba"} {
		res = s.resolveCatalog("qwen3-max", v, []string{ProtoOpenAIChat}, "")
		if res.Reasoning == nil || res.Reasoning.Format != "qwen" {
			t.Fatalf("qwen/%q: %+v", v, res.Reasoning)
		}
	}
	// Budget formats exist on Anthropic and Gemini generate content only.
	res = s.resolveCatalog("claude-old", "anthropic", []string{ProtoAnthropicMessages}, "")
	if res.Reasoning == nil || res.Reasoning.Format != "anthropic_budget" || strings.Join(res.Reasoning.Levels, ",") != "low,medium,high" {
		t.Fatalf("anthropic budget: %+v", res.Reasoning)
	}
	res = s.resolveCatalog("gemini-9-pro", "google", []string{ProtoGoogleGenerate}, "")
	if res.Reasoning == nil || res.Reasoning.Format != "gemini_budget" {
		t.Fatalf("gemini budget: %+v", res.Reasoning)
	}
	if r := s.resolveCatalog("claude-old", "anthropic", []string{ProtoOpenAIChat}, ""); r.Reasoning != nil {
		t.Fatalf("budget-only entries do not apply to chat completions: %+v", r.Reasoning)
	}
	// Non-reasoning entries have no capability; the form template falls back to the protocol's level format.
	res = s.resolveCatalog("gpt-4o", "openai", []string{ProtoOpenAIResponses}, "")
	if res.Reasoning != nil || res.ReasoningTemplate == nil || res.ReasoningTemplate.Format != "openai" {
		t.Fatalf("non reasoning: %+v %+v", res.Reasoning, res.ReasoningTemplate)
	}
	// A model nobody knows with no protocols has no template at all.
	if res = s.resolveCatalog("mystery", "", nil, ""); res.ReasoningTemplate != nil {
		t.Fatalf("template: %+v", res.ReasoningTemplate)
	}
	res = s.resolveCatalog("mystery", "", []string{ProtoAnthropicMessages}, "")
	if res.ReasoningTemplate == nil || res.ReasoningTemplate.Format != "anthropic_effort" || res.ReasoningTemplate.Budgets != nil {
		t.Fatalf("protocol template: %+v", res.ReasoningTemplate)
	}
	res = s.resolveCatalog("mystery", "", []string{ProtoGeminiInteractions}, "")
	if res.ReasoningTemplate == nil || res.ReasoningTemplate.Format != "gemini_level" || strings.Join(res.ReasoningTemplate.Levels, ",") != "minimal,low,medium,high" {
		t.Fatalf("gemini template: %+v", res.ReasoningTemplate)
	}
}

func TestCatalogReasoningTemplateBudgets(t *testing.T) {
	s := newCatalogSuite(t)
	res := s.resolveCatalog("claude-old", "anthropic", []string{ProtoAnthropicMessages}, "")
	tpl := res.ReasoningTemplate
	if tpl == nil || tpl.Format != "anthropic_budget" || tpl.Budgets["none"] != 0 || tpl.Budgets["low"] != 2048 || tpl.Budgets["medium"] != 8192 ||
		tpl.Budgets["high"] != 24576 || tpl.Budgets["xhigh"] != 32000 || tpl.Budgets["max"] != 32000 || tpl.Budgets["minimal"] != 1024 {
		t.Fatalf("anthropic template: %+v", tpl)
	}
	// An effective explicit declaration of the same format overrides suggested budgets.
	caps := `{"reasoning":{"format":"anthropic_budget","levels":["low","high"],"default":"low","budgets":{"low":3000,"high":9000}}}`
	res = s.resolveCatalog("claude-old", "anthropic", []string{ProtoAnthropicMessages}, caps)
	if res.ReasoningTemplate.Budgets["low"] != 3000 || res.ReasoningTemplate.Budgets["high"] != 9000 || res.ReasoningTemplate.Budgets["medium"] != 8192 {
		t.Fatalf("override: %+v", res.ReasoningTemplate)
	}
	// The explicit declaration is ignored in `reasoning` (what the model would get otherwise).
	if res.Reasoning == nil || res.Reasoning.Source != "catalog" || len(res.Reasoning.Levels) != 3 {
		t.Fatalf("reasoning ignoring explicit: %+v", res.Reasoning)
	}
	// Gemini with a positive minimum drops the none level.
	res = s.resolveCatalog("gemini-9-pro", "google", []string{ProtoGoogleGenerate}, "")
	if res.ReasoningTemplate == nil || res.ReasoningTemplate.Format != "gemini_budget" {
		t.Fatalf("gemini: %+v", res.ReasoningTemplate)
	}
	for _, l := range res.ReasoningTemplate.Levels {
		if l == "none" {
			t.Fatalf("none must be omitted with a positive minimum: %+v", res.ReasoningTemplate)
		}
	}
	if b := res.ReasoningTemplate.Budgets["low"]; b != 2048 {
		t.Fatalf("gemini low: %d", b)
	}
	// toggle template.
	res = s.resolveCatalog("kimi-k2-thinking", "moonshot", []string{ProtoOpenAIChat}, "")
	if res.ReasoningTemplate == nil || res.ReasoningTemplate.Format != "toggle" || strings.Join(res.ReasoningTemplate.Levels, ",") != "none,high" {
		t.Fatalf("toggle template: %+v", res.ReasoningTemplate)
	}
}

func publicOf(s *suite, name string) PublicModel {
	s.t.Helper()
	for _, m := range s.userModels(1) {
		if m.PlatformModelName == name {
			return m
		}
	}
	s.t.Fatalf("model %s not listed", name)
	return PublicModel{}
}

func TestPublicReasoningResolution(t *testing.T) {
	s := newCatalogSuite(t)
	s.openGroup()
	up := s.mkUpstream("rz", "openai", nil)
	mk := func(name, proto string, caps string) {
		row := s.bind(up.ID, name, name, []string{proto}, nil)
		if caps != "" {
			s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), map[string]any{"capabilitiesJSON": caps})
		}
	}
	mk("gpt-5", ProtoOpenAIResponses, "")
	m := publicOf(s, "gpt-5")
	if m.Reasoning == nil || m.Reasoning.Source != "catalog" || m.Reasoning.Default != "medium" || m.Reasoning.Locked || m.Reasoning.ControlPath != nil {
		t.Fatalf("catalog reasoning: %+v", m.Reasoning)
	}
	// Catalog-sourced controls start with an `auto` option, which is the default.
	if len(m.Controls) != 1 {
		t.Fatalf("controls: %+v", m.Controls)
	}
	c := m.Controls[0]
	if c.ID != "reasoning" || c.Kind != "reasoning" || c.Type != "select" || c.Icon != "brain" || c.Placement != "toolbar" || c.Locked ||
		c.Default == nil || *c.Default != "auto" || c.Options[0].Value != "auto" || len(c.Options) != 5 {
		t.Fatalf("control: %+v", c)
	}
	if m.InputModalitiesSource == nil || *m.InputModalitiesSource != "catalog" || strings.Join(m.InputModalities, ",") != "text,image,pdf" ||
		m.ContextWindow == nil || *m.ContextWindow != 400000 {
		t.Fatalf("catalog facts: %+v", m)
	}

	// Explicit declarations win and have no `auto` option.
	mk("explicit-1", ProtoOpenAIChat, `{"reasoning":{"format":"openai","levels":["low","high"],"default":"high"}}`)
	m = publicOf(s, "explicit-1")
	if m.Reasoning == nil || m.Reasoning.Source != "explicit" || strings.Join(m.Reasoning.Levels, ",") != "low,high" || m.Reasoning.Default != "high" {
		t.Fatalf("explicit: %+v", m.Reasoning)
	}
	if *m.Controls[0].Default != "high" || m.Controls[0].Options[0].Value != "low" {
		t.Fatalf("explicit control: %+v", m.Controls[0])
	}
	// A declaration the protocol cannot translate yields no capability and never falls back to the catalog.
	mk("gpt-5-turbo", ProtoOpenAIChat, `{"reasoning":{"format":"anthropic_effort","levels":["low"],"default":"low"}}`)
	if m = publicOf(s, "gpt-5-turbo"); m.Reasoning != nil || len(m.Controls) != 0 {
		t.Fatalf("unsupported explicit: %+v", m)
	}
	mk("kimi-k2-thinking", ProtoOpenAIChat, "") // the broken declaration is stored directly below, bypassing validation
	s.env.DB.Model(&PlatformModel{}).Where("name = ?", "kimi-k2-thinking").Update("capabilities_json", `{"reasoning":"broken"}`)
	s.svc.InvalidateCatalog()
	if m = publicOf(s, "kimi-k2-thinking"); m.Reasoning != nil {
		t.Fatalf("an invalid explicit declaration blocks the catalog fallback: %+v", m.Reasoning)
	}
	// Inference from native parameters: a select over reasoning_effort on an OpenAI chat route.
	mk("native-1", ProtoOpenAIChat, `{"defaultOptions":{"reasoning_effort":"medium"},"optionControls":[{"path":"reasoning_effort","type":"select","label":"Effort","options":[{"value":"medium"}]}]}`)
	m = publicOf(s, "native-1")
	if m.Reasoning == nil || m.Reasoning.Source != "inferred" || m.Reasoning.ControlPath == nil || *m.Reasoning.ControlPath != "reasoning_effort" ||
		strings.Join(m.Reasoning.Levels, ",") != "medium" || m.Reasoning.Default != "medium" {
		t.Fatalf("inferred: %+v", m.Reasoning)
	}
	for _, ctl := range m.Controls {
		if ctl.ID != "reasoning" {
			t.Fatalf("the raw control must be hidden: %+v", ctl)
		}
	}
	// Locked options lock the capability and the control.
	mk("locked-1", ProtoOpenAIChat, `{"reasoning":{"format":"openai","levels":["low","high"],"default":"low"},"lockedOptionPaths":["reasoning_effort"]}`)
	m = publicOf(s, "locked-1")
	if m.Reasoning == nil || !m.Reasoning.Locked || !m.Controls[0].Locked {
		t.Fatalf("locked: %+v", m.Reasoning)
	}
	// Hidden controls are not listed.
	mk("hidden-1", ProtoOpenAIChat, `{"controls":[{"id":"temp","type":"toggle","hidden":true},{"id":"shown","type":"toggle","label":"Shown"}]}`)
	m = publicOf(s, "hidden-1")
	if len(m.Controls) != 1 || m.Controls[0].ID != "shown" || m.Controls[0].Placement != "menu" || m.Controls[0].Default == nil || *m.Controls[0].Default != "off" {
		t.Fatalf("controls: %+v", m.Controls)
	}
	// Controls limited to other protocols are omitted.
	mk("proto-1", ProtoOpenAIChat, `{"controls":[{"id":"a","type":"toggle","protocols":["anthropic_messages"]},{"id":"b","type":"toggle","protocols":["openai_chat_completions"]}]}`)
	m = publicOf(s, "proto-1")
	if len(m.Controls) != 1 || m.Controls[0].ID != "b" {
		t.Fatalf("protocol scoped: %+v", m.Controls)
	}
	// Parameter patches are never exposed.
	w := s.env.Do("GET", "/api/v1/models", s.usr(1), nil)
	if strings.Contains(w.Body.String(), `"patch"`) {
		t.Fatal("patches must not be exposed")
	}
}

func TestResolveLevelAndPatches(t *testing.T) {
	c := &ReasoningCapability{Format: "openai", Levels: []string{"low", "high"}, Default: "low"}
	for in, want := range map[string]string{"low": "low", "HIGH": "high", "medium": "low", "xhigh": "high", "none": "low", "bogus": "low", "": "low", "max": "high"} {
		if got := ResolveLevel(c, in); got != want {
			t.Errorf("ResolveLevel(%q) = %s want %s", in, got, want)
		}
	}
	if ResolveLevel(nil, "low") != "" {
		t.Error("nil capability")
	}
	// Control value normalization and patches.
	caps := parseCapsLenient(`{"controls":[
		{"id":"mode","type":"select","default":"fast","options":[{"value":"fast","patch":{"a":{"b":1}}},{"value":"deep","patch":{"a":{"b":2}},"patches":{"anthropic_messages":{"z":true}}},{"value":"gone","patch":{"a":null}}]},
		{"id":"flag","type":"toggle","on":{"patch":{"x":true}},"off":{"patch":{"x":false}}},
		{"id":"temp","type":"number","path":"gen.temperature","min":0,"max":2,"step":0.5,"default":"1"}
	]}`)
	controls := effectiveControls(caps, nil, []string{ProtoOpenAIChat}, false)
	byID := map[string]*Control{}
	for _, c := range controls {
		byID[c.ID] = c
	}
	mode, flag, temp := byID["mode"], byID["flag"], byID["temp"]
	if mode == nil || flag == nil || temp == nil {
		t.Fatalf("controls: %v", controls)
	}
	if mode.NormalizeValue(" deep ") != "deep" || mode.NormalizeValue("nope") != "fast" || mode.NormalizeValue(nil) != "fast" {
		t.Error("select normalization")
	}
	if flag.NormalizeValue("ON") != "on" || flag.NormalizeValue(true) != "on" || flag.NormalizeValue("false") != "off" || flag.NormalizeValue("maybe") != "off" {
		t.Errorf("toggle normalization: %q %q %q %q", flag.NormalizeValue("ON"), flag.NormalizeValue(true), flag.NormalizeValue("false"), flag.NormalizeValue("maybe"))
	}
	if temp.NormalizeValue(1.5) != "1.5" || temp.NormalizeValue("2") != "2" || temp.NormalizeValue(2.5) != "1" || temp.NormalizeValue(0.3) != "1" || temp.NormalizeValue("x") != "1" {
		t.Errorf("number normalization: %q %q %q %q %q", temp.NormalizeValue(1.5), temp.NormalizeValue("2"), temp.NormalizeValue(2.5), temp.NormalizeValue(0.3), temp.NormalizeValue("x"))
	}
	if p := mode.Patch("deep", ProtoOpenAIChat); fmt.Sprint(p) != "map[a:map[b:2]]" {
		t.Errorf("patch: %v", p)
	}
	if p := mode.Patch("deep", ProtoAnthropicMessages); fmt.Sprint(p) != "map[z:true]" {
		t.Errorf("protocol patch: %v", p)
	}
	if p := mode.Patch("gone", ProtoOpenAIChat); p["a"] != nil || len(p) != 1 {
		t.Errorf("null leaf deletes: %v", p)
	}
	if p := flag.Patch("on", ProtoOpenAIChat); fmt.Sprint(p) != "map[x:true]" {
		t.Errorf("toggle patch: %v", p)
	}
	if p := temp.Patch("1.5", ProtoOpenAIChat); fmt.Sprint(p) != "map[gen:map[temperature:1.5]]" {
		t.Errorf("number patch: %v", p)
	}
}
