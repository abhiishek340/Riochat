package billing

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	openRouterURL        = "https://openrouter.ai/api/v1/models"
	officialCacheKey     = "billing:official_pricing:openrouter"
	officialCacheVersion = 5
	officialFreshFor     = 24 * time.Hour
	officialCacheTTL     = 30 * 24 * time.Hour
	officialBodyLimit    = 8 << 20
	officialSnapshotCap  = 32 << 20
)

// OfficialOverride is one input-size price tier of the official catalog.
type OfficialOverride struct {
	MinPromptTokens int64  `json:"minPromptTokens"`
	Prompt          string `json:"prompt,omitempty"`
	Completion      string `json:"completion,omitempty"`
	InputCacheRead  string `json:"inputCacheRead,omitempty"`
	InputCacheWrite string `json:"inputCacheWrite,omitempty"`
}

// OfficialPricing are the USD-per-token price strings of one model.
type OfficialPricing struct {
	Prompt               string             `json:"prompt"`
	Completion           string             `json:"completion"`
	InputCacheRead       string             `json:"inputCacheRead"`
	InputCacheWrite      string             `json:"inputCacheWrite"`
	CacheWritePriceBasis string             `json:"cacheWritePriceBasis"`
	Overrides            []OfficialOverride `json:"overrides,omitempty"`
	UnsupportedFields    []string           `json:"unsupportedFields,omitempty"`
}

// OfficialItem is one normalized catalog model.
type OfficialItem struct {
	ID                  string          `json:"id"`
	CanonicalSlug       string          `json:"canonicalSlug"`
	Name                string          `json:"name"`
	ContextLength       int64           `json:"contextLength"`
	MaxCompletionTokens int64           `json:"maxCompletionTokens"`
	Pricing             OfficialPricing `json:"pricing"`
}

type officialBlob struct {
	Version   int            `json:"version"`
	FetchedAt time.Time      `json:"fetchedAt"`
	Origin    string         `json:"origin"`
	Items     []OfficialItem `json:"items"`
}

// OfficialCatalog is the response of the official pricing endpoint.
type OfficialCatalog struct {
	FetchedAt time.Time      `json:"fetchedAt"`
	Cached    bool           `json:"cached"`
	Stale     bool           `json:"stale"`
	Origin    string         `json:"origin"`
	Items     []OfficialItem `json:"items"`
}

// ErrOfficialUnavailable marks a failure to obtain any catalog (HTTP 502).
var ErrOfficialUnavailable = ErrUpstreamUnavailable

func isAnthropicID(id string) bool {
	id = strings.ToLower(id)
	return strings.HasPrefix(id, "anthropic/") || strings.HasPrefix(id, "~anthropic/")
}

// priceString converts a source price value (number or numeric string) to a
// trimmed decimal string; ok is false for anything else or a negative or
// non-finite value.
func priceString(raw json.RawMessage) (string, bool) {
	var s string
	if json.Unmarshal(raw, &s) != nil {
		var n json.Number
		d := json.NewDecoder(bytes.NewReader(raw))
		d.UseNumber()
		if d.Decode(&n) != nil {
			return "", false
		}
		s = n.String()
	}
	s = strings.TrimSpace(s)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) || f < 0 {
		return "", false
	}
	return s, true
}

type sourceModel struct {
	ID            string `json:"id"`
	CanonicalSlug string `json:"canonical_slug"`
	Name          string `json:"name"`
	ContextLength int64  `json:"context_length"`
	TopProvider   struct {
		ContextLength       int64 `json:"context_length"`
		MaxCompletionTokens int64 `json:"max_completion_tokens"`
	} `json:"top_provider"`
	Pricing   map[string]json.RawMessage   `json:"pricing"`
	Overrides []map[string]json.RawMessage `json:"overrides"`
}

var knownPricingKeys = map[string]bool{
	"prompt": true, "completion": true, "input_cache_read": true, "input_cache_write": true,
	"web_search": true, "overrides": true,
}

// normalizeOfficial converts one source model; ok is false for blank ids.
func normalizeOfficial(m sourceModel) (OfficialItem, bool) {
	id := strings.TrimSpace(m.ID)
	if id == "" {
		return OfficialItem{}, false
	}
	it := OfficialItem{ID: id, CanonicalSlug: firstNonEmpty(strings.TrimSpace(m.CanonicalSlug), id), Name: firstNonEmpty(strings.TrimSpace(m.Name), id)}
	it.ContextLength = m.TopProvider.ContextLength
	if it.ContextLength <= 0 {
		it.ContextLength = m.ContextLength
	}
	it.ContextLength = maxI64(it.ContextLength, 0)
	it.MaxCompletionTokens = maxI64(m.TopProvider.MaxCompletionTokens, 0)
	unsupported := map[string]bool{}
	get := func(key string) string {
		raw, ok := m.Pricing[key]
		if !ok {
			return ""
		}
		v, ok := priceString(raw)
		if !ok {
			unsupported[key] = true
			return ""
		}
		return v
	}
	p := OfficialPricing{Prompt: get("prompt"), Completion: get("completion"), InputCacheRead: get("input_cache_read"),
		InputCacheWrite: get("input_cache_write"), CacheWritePriceBasis: "direct"}
	if isAnthropicID(id) {
		p.CacheWritePriceBasis = "anthropic_5m"
	}
	if _, ok := m.Pricing["prompt"]; !ok {
		unsupported["prompt"] = true
	}
	if _, ok := m.Pricing["completion"]; !ok {
		unsupported["completion"] = true
	}
	for k := range m.Pricing {
		if !knownPricingKeys[k] {
			unsupported[k] = true
		}
	}
	// Overrides live either at the model level or inside the pricing object.
	overrides := m.Overrides
	if raw, ok := m.Pricing["overrides"]; ok && len(overrides) == 0 {
		_ = json.Unmarshal(raw, &overrides)
	}
	type entry struct {
		min  int64
		vals map[string]string
	}
	var entries []entry
	for _, ov := range overrides {
		if _, a := ov["utc_days"]; a {
			unsupported["utc_days"] = true
		}
		if _, a := ov["utc_start"]; a {
			unsupported["utc_start"] = true
		}
		if _, a := ov["utc_end"]; a {
			unsupported["utc_end"] = true
		}
		if ov["utc_days"] != nil || ov["utc_start"] != nil || ov["utc_end"] != nil {
			continue
		}
		var min int64
		raw, ok := ov["min_prompt_tokens"]
		if !ok {
			continue
		}
		var f float64
		if json.Unmarshal(raw, &f) != nil {
			var s string
			if json.Unmarshal(raw, &s) != nil {
				continue
			}
			var err error
			if f, err = strconv.ParseFloat(strings.TrimSpace(s), 64); err != nil {
				continue
			}
		}
		if f <= 0 || f != math.Trunc(f) || f > math.MaxInt64/2 {
			continue
		}
		min = int64(f)
		vals := map[string]string{}
		for _, k := range []string{"prompt", "completion", "input_cache_read", "input_cache_write"} {
			if r, ok := ov[k]; ok {
				if v, ok := priceString(r); ok {
					vals[k] = v
				} else {
					unsupported["overrides."+k] = true
				}
			}
		}
		entries = append(entries, entry{min, vals})
	}
	thresholds := map[int64]bool{}
	for _, e := range entries {
		thresholds[e.min] = true
	}
	var ths []int64
	for t := range thresholds {
		ths = append(ths, t)
	}
	sort.Slice(ths, func(i, j int) bool { return ths[i] < ths[j] })
	for _, th := range ths {
		eff := map[string]string{"prompt": p.Prompt, "completion": p.Completion, "input_cache_read": p.InputCacheRead, "input_cache_write": p.InputCacheWrite}
		for _, e := range entries {
			if e.min <= th {
				for k, v := range e.vals {
					eff[k] = v
				}
			}
		}
		p.Overrides = append(p.Overrides, OfficialOverride{MinPromptTokens: th, Prompt: eff["prompt"], Completion: eff["completion"],
			InputCacheRead: eff["input_cache_read"], InputCacheWrite: eff["input_cache_write"]})
	}
	for k := range unsupported {
		p.UnsupportedFields = append(p.UnsupportedFields, k)
	}
	sort.Strings(p.UnsupportedFields)
	it.Pricing = p
	return it, true
}

func (s *Service) fetchOfficial(ctx context.Context) (*officialBlob, error) {
	url := s.ports.OpenRouterURL
	if url == "" {
		url = openRouterURL
	}
	ctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, fmt.Errorf("official pricing: unexpected status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, officialBodyLimit))
	if err != nil {
		return nil, err
	}
	var doc struct {
		Data []sourceModel `json:"data"`
	}
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, err
	}
	blob := &officialBlob{Version: officialCacheVersion, FetchedAt: s.now(), Origin: "remote"}
	for _, m := range doc.Data {
		if it, ok := normalizeOfficial(m); ok {
			blob.Items = append(blob.Items, it)
		}
	}
	if len(blob.Items) == 0 {
		return nil, errors.New("official pricing: empty catalog")
	}
	return blob, nil
}

func (s *Service) builtinOfficial() *officialBlob {
	if len(s.ports.BuiltinPricing) == 0 {
		return nil
	}
	zr, err := gzip.NewReader(bytes.NewReader(s.ports.BuiltinPricing))
	if err != nil {
		return nil
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, officialSnapshotCap+1))
	if err != nil || len(raw) > officialSnapshotCap {
		return nil
	}
	var b officialBlob
	if json.Unmarshal(raw, &b) != nil || len(b.Items) == 0 {
		return nil
	}
	b.Origin, b.Version = "builtin", officialCacheVersion
	return &b
}

func markLegacy(b *officialBlob) {
	for i := range b.Items {
		f := b.Items[i].Pricing.UnsupportedFields
		f = append(f, "legacy_cache", "overrides", "input_cache_write")
		sort.Strings(f)
		out := f[:0]
		for j, v := range f {
			if j == 0 || v != f[j-1] {
				out = append(out, v)
			}
		}
		b.Items[i].Pricing.UnsupportedFields = out
	}
}

// OfficialPricing returns the normalized OpenRouter price catalog.
func (s *Service) OfficialPricing(ctx context.Context, refresh bool) (*OfficialCatalog, error) {
	s.officialMu.Lock()
	defer s.officialMu.Unlock()
	now := s.now()
	var stored *officialBlob
	if raw, found, err := s.cache.Get(ctx, officialCacheKey); err != nil {
		return nil, err
	} else if found {
		var b officialBlob
		if json.Unmarshal(raw, &b) == nil && len(b.Items) > 0 {
			stored = &b
		}
	}
	eff := stored
	if snap := s.builtinOfficial(); snap != nil {
		if eff == nil || eff.Version < officialCacheVersion || eff.FetchedAt.Before(snap.FetchedAt) {
			eff = snap
			if raw, err := json.Marshal(snap); err == nil {
				_ = s.cache.Set(ctx, officialCacheKey, raw, officialCacheTTL) // seeding is best effort
			}
		}
	}
	current := eff != nil && eff.Version >= officialCacheVersion
	if current && !refresh && now.Sub(eff.FetchedAt) < officialFreshFor {
		return &OfficialCatalog{FetchedAt: eff.FetchedAt, Cached: true, Origin: eff.Origin, Items: eff.Items}, nil
	}
	fresh, err := s.fetchOfficial(ctx)
	if err != nil {
		if eff != nil {
			cp := *eff
			if eff.Version < officialCacheVersion {
				cp.Items = append([]OfficialItem(nil), eff.Items...)
				markLegacy(&cp)
			}
			return &OfficialCatalog{FetchedAt: cp.FetchedAt, Cached: true, Stale: true, Origin: cp.Origin, Items: cp.Items}, nil
		}
		return nil, ErrOfficialUnavailable.WithCause(err)
	}
	raw, err := json.Marshal(fresh)
	if err != nil {
		return nil, err
	}
	if err := s.cache.Set(ctx, officialCacheKey, raw, officialCacheTTL); err != nil {
		return nil, err
	}
	return &OfficialCatalog{FetchedAt: fresh.FetchedAt, Origin: "remote", Items: fresh.Items}, nil
}
