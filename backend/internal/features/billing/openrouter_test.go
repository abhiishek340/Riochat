package billing

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const officialURL = "/api/v1/admin/billing/official-pricing/openrouter"

type orFake struct {
	srv    *httptest.Server
	calls  int
	status int
	body   string
}

func newORFake(t *testing.T, body string) *orFake {
	f := &orFake{status: 200, body: body}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls++
		if r.Header.Get("Accept") != "application/json" {
			w.WriteHeader(406)
			return
		}
		w.WriteHeader(f.status)
		_, _ = w.Write([]byte(f.body))
	}))
	t.Cleanup(f.srv.Close)
	return f
}

const orBody = `{"data":[
 {"id":"openai/gpt-x","canonical_slug":"openai/gpt-x-2026","name":"GPT X","context_length":1000,"top_provider":{"context_length":2000,"max_completion_tokens":300},
  "pricing":{"prompt":"0.000003","completion":0.000015,"input_cache_read":"0.0000003","web_search":"0.01","image":"0.1","weird":"x"}},
 {"id":"anthropic/claude-y","name":"","context_length":-5,"top_provider":{"max_completion_tokens":-1},
  "pricing":{"prompt":"0.000003","completion":"0.000015","input_cache_write":"0.00000375","input_cache_read":"bad"},
  "overrides":[
    {"min_prompt_tokens":200000,"prompt":"0.000004"},
    {"min_prompt_tokens":1000000,"prompt":"0.000006","completion":"0.0000225"},
    {"min_prompt_tokens":500,"utc_days":[1],"prompt":"0.1"},
    {"prompt":"0.2"},
    {"min_prompt_tokens":0,"prompt":"0.3"}
  ]},
 {"id":"  ","name":"blank"},
 {"id":"~Anthropic/old","pricing":{"prompt":"-1","completion":"0.1"}}
]}`

func orEnv(t *testing.T, f *orFake, opts ...func(*Ports)) *env {
	return newEnv(t, withPorts(func(p *Ports) {
		p.OpenRouterURL = f.srv.URL
		for _, o := range opts {
			o(p)
		}
	}))
}

func getOfficial(t *testing.T, e *env, q string) (int, OfficialCatalog, string) {
	w := e.do("GET", officialURL+q, e.admin(2), nil)
	var out OfficialCatalog
	if w.Code == 200 {
		testkit.Decode(t, w, &out)
	}
	return w.Code, out, w.Body.String()
}

func TestOfficialPricingNormalization(t *testing.T) {
	f := newORFake(t, orBody)
	e := orEnv(t, f)
	code, cat, body := getOfficial(t, e, "")
	if code != 200 || cat.Cached || cat.Stale || cat.Origin != "remote" || len(cat.Items) != 3 {
		t.Fatalf("%d %+v %s", code, cat, body)
	}
	gpt := cat.Items[0]
	if gpt.ID != "openai/gpt-x" || gpt.CanonicalSlug != "openai/gpt-x-2026" || gpt.Name != "GPT X" || gpt.ContextLength != 2000 || gpt.MaxCompletionTokens != 300 {
		t.Fatalf("gpt %+v", gpt)
	}
	p := gpt.Pricing
	if p.Prompt != "0.000003" || p.Completion != "0.000015" || p.InputCacheRead != "0.0000003" || p.InputCacheWrite != "" || p.CacheWritePriceBasis != "direct" {
		t.Fatalf("gpt pricing %+v", p)
	}
	// web_search is ignored; unknown keys are listed, sorted
	if strings.Join(p.UnsupportedFields, ",") != "image,weird" {
		t.Fatalf("unsupported %v", p.UnsupportedFields)
	}
	cl := cat.Items[1]
	if cl.CanonicalSlug != "anthropic/claude-y" || cl.Name != "anthropic/claude-y" || cl.ContextLength != 0 || cl.MaxCompletionTokens != 0 {
		t.Fatalf("claude %+v", cl)
	}
	cp := cl.Pricing
	if cp.CacheWritePriceBasis != "anthropic_5m" || cp.InputCacheWrite != "0.00000375" || cp.InputCacheRead != "" {
		t.Fatalf("claude pricing %+v", cp)
	}
	// thresholds ascend; each overlays all overrides at or below it on the base prices
	if len(cp.Overrides) != 2 || cp.Overrides[0].MinPromptTokens != 200000 || cp.Overrides[1].MinPromptTokens != 1000000 {
		t.Fatalf("overrides %+v", cp.Overrides)
	}
	if o := cp.Overrides[0]; o.Prompt != "0.000004" || o.Completion != "0.000015" || o.InputCacheWrite != "0.00000375" {
		t.Fatalf("tier 200k %+v", o)
	}
	if o := cp.Overrides[1]; o.Prompt != "0.000006" || o.Completion != "0.0000225" {
		t.Fatalf("tier 1M %+v", o)
	}
	// time-conditional override skipped and flagged; invalid price dropped and flagged
	got := strings.Join(cp.UnsupportedFields, ",")
	for _, want := range []string{"utc_days", "input_cache_read"} {
		if !strings.Contains(got, want) {
			t.Fatalf("unsupported %q lacks %s", got, want)
		}
	}
	old := cat.Items[2]
	if old.Pricing.CacheWritePriceBasis != "anthropic_5m" || old.Pricing.Prompt != "" || !strings.Contains(strings.Join(old.Pricing.UnsupportedFields, ","), "prompt") {
		t.Fatalf("~anthropic %+v", old)
	}
}

func TestOfficialPricingCacheFlow(t *testing.T) {
	f := newORFake(t, orBody)
	e := orEnv(t, f)
	_, first, _ := getOfficial(t, e, "")
	if f.calls != 1 {
		t.Fatalf("calls %d", f.calls)
	}
	// fresh cache
	e.Clock.Advance(time.Hour)
	code, cat, _ := getOfficial(t, e, "")
	if code != 200 || !cat.Cached || cat.Stale || f.calls != 1 || !cat.FetchedAt.Equal(first.FetchedAt) {
		t.Fatalf("fresh: %+v calls=%d", cat, f.calls)
	}
	// refresh=true forces a refetch
	_, cat, _ = getOfficial(t, e, "?refresh=true")
	if cat.Cached || f.calls != 2 {
		t.Fatalf("refresh: %+v calls=%d", cat, f.calls)
	}
	// expired cache refetches
	e.Clock.Advance(25 * time.Hour)
	_, cat, _ = getOfficial(t, e, "")
	if cat.Cached || f.calls != 3 {
		t.Fatalf("expired: %+v calls=%d", cat, f.calls)
	}
	// remote down with a cache: stale
	f.status, f.body = 500, "down"
	e.Clock.Advance(25 * time.Hour)
	code, cat, _ = getOfficial(t, e, "")
	if code != 200 || !cat.Stale || !cat.Cached || len(cat.Items) != 3 {
		t.Fatalf("stale: %d %+v", code, cat)
	}
	_, cat, _ = getOfficial(t, e, "?refresh=true")
	if !cat.Stale {
		t.Fatal("refresh failure with cache must stay stale")
	}
}

func TestOfficialPricingUnavailable(t *testing.T) {
	for name, body := range map[string]string{"empty": `{"data":[]}`, "garbage": `nope`, "all blank ids": `{"data":[{"id":""}]}`} {
		f := newORFake(t, body)
		e := orEnv(t, f)
		w := e.do("GET", officialURL, e.admin(2), nil)
		if w.Code != 502 {
			t.Errorf("%s: %d %s", name, w.Code, w.Body)
			continue
		}
		testkit.Expect(t, w, 502, "upstream.unavailable")
	}
	f := newORFake(t, orBody)
	f.status = 503
	e := orEnv(t, f)
	testkit.Expect(t, e.do("GET", officialURL, e.admin(2), nil), 502, "upstream.unavailable")
	if w := e.do("GET", officialURL, e.user(1), nil); w.Code == 200 {
		t.Fatal("non admin allowed")
	}
}

func builtinBlob(t *testing.T, fetched time.Time, version int) []byte {
	b, _ := json.Marshal(officialBlob{Version: version, FetchedAt: fetched, Items: []OfficialItem{{ID: "builtin/model", Pricing: OfficialPricing{Prompt: "0.1", Completion: "0.2", CacheWritePriceBasis: "direct"}}}})
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	_, _ = zw.Write(b)
	_ = zw.Close()
	return buf.Bytes()
}

func TestOfficialPricingBuiltinSnapshot(t *testing.T) {
	f := newORFake(t, orBody)
	f.status = 500
	snap := builtinBlob(t, time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC), 5)
	e := orEnv(t, f, func(p *Ports) { p.BuiltinPricing = snap })
	// no cache, remote down: the snapshot is served as fresh builtin data (seeded into the cache)
	code, cat, body := getOfficial(t, e, "")
	if code != 200 || cat.Origin != "builtin" || len(cat.Items) != 1 || cat.Items[0].ID != "builtin/model" {
		t.Fatalf("builtin: %d %+v %s", code, cat, body)
	}
	// a refresh while the remote is down keeps serving the builtin data as stale
	_, cat, _ = getOfficial(t, e, "?refresh=true")
	if !cat.Stale || cat.Origin != "builtin" {
		t.Fatalf("refresh: %+v", cat)
	}
	// a successful remote refresh replaces it; then the cache is newer than the snapshot
	f.status = 200
	_, cat, _ = getOfficial(t, e, "?refresh=true")
	if cat.Origin != "remote" {
		t.Fatalf("remote: %+v", cat)
	}
	_, cat, _ = getOfficial(t, e, "")
	if cat.Origin != "remote" || !cat.Cached {
		t.Fatalf("cache stays: %+v", cat)
	}
	// a corrupt snapshot is ignored
	e2 := orEnv(t, newORFake(t, orBody), func(p *Ports) { p.BuiltinPricing = []byte("not gzip") })
	if code, cat, _ := getOfficial(t, e2, ""); code != 200 || cat.Origin != "remote" {
		t.Fatalf("corrupt snapshot: %d %+v", code, cat)
	}
}

func TestOfficialPricingLegacyCache(t *testing.T) {
	f := newORFake(t, orBody)
	e := orEnv(t, f)
	legacy := officialBlob{Version: 3, FetchedAt: e.Clock.Now(), Origin: "remote", Items: []OfficialItem{{ID: "old/model", Pricing: OfficialPricing{Prompt: "1", Completion: "2"}}}}
	raw, _ := json.Marshal(legacy)
	if err := e.svc.cache.Set(context.Background(), officialCacheKey, raw, time.Hour); err != nil {
		t.Fatal(err)
	}
	// an older cache version is not served as fresh: the remote is consulted
	_, cat, _ := getOfficial(t, e, "")
	if cat.Cached || f.calls != 1 || cat.Items[0].ID != "openai/gpt-x" {
		t.Fatalf("legacy refetch: %+v calls=%d", cat, f.calls)
	}
	// legacy data served when the remote is down reports the lost fields
	if err := e.svc.cache.Set(context.Background(), officialCacheKey, raw, time.Hour); err != nil {
		t.Fatal(err)
	}
	f.status = 500
	_, cat, _ = getOfficial(t, e, "")
	if !cat.Stale || len(cat.Items) != 1 {
		t.Fatalf("legacy stale: %+v", cat)
	}
	fields := strings.Join(cat.Items[0].Pricing.UnsupportedFields, ",")
	for _, want := range []string{"legacy_cache", "overrides", "input_cache_write"} {
		if !strings.Contains(fields, want) {
			t.Fatalf("legacy fields %q lack %s", fields, want)
		}
	}
}

func TestNormalizeOfficialPriceValues(t *testing.T) {
	for in, want := range map[string]string{`"0.5"`: "0.5", `" 0.5 "`: "0.5", `0.25`: "0.25", `0`: "0"} {
		got, ok := priceString(json.RawMessage(in))
		if !ok || got != want {
			t.Errorf("%s: %q %v", in, got, ok)
		}
	}
	for _, in := range []string{`"abc"`, `"-1"`, `-3`, `null`, `true`, `[]`, `"NaN"`, `"Inf"`} {
		if _, ok := priceString(json.RawMessage(in)); ok {
			t.Errorf("%s accepted", in)
		}
	}
}
