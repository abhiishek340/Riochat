package channel

import (
	"bytes"
	"compress/gzip"
	"encoding/json"
	"errors"
	"io"
	"math"
	"sort"
	"strings"
	"time"
)

const (
	snapshotVersion    = 2
	maxSnapshotBytes   = 32 << 20
	maxCatalogAPIBytes = 64 << 20
)

// Option types of a catalog entry's reasoning support.
const (
	optEffort = "effort"
	optBudget = "budget_tokens"
	optToggle = "toggle"
)

// catalogOption is one reasoning option of a model.
type catalogOption struct {
	Type   string   `json:"type"`
	Values []string `json:"values,omitempty"`
	Min    *int     `json:"min,omitempty"`
	Max    *int     `json:"max,omitempty"`
}

// catalogEntry is one model of the external catalog.
type catalogEntry struct {
	Provider    string          `json:"provider"`
	ID          string          `json:"id"`
	CanonicalID string          `json:"canonicalId,omitempty"`
	Options     []catalogOption `json:"options,omitempty"`
	Inputs      []string        `json:"inputs,omitempty"`
	Outputs     []string        `json:"outputs,omitempty"`
	Context     int             `json:"context,omitempty"`
}

func (e *catalogEntry) option(t string) *catalogOption {
	for i := range e.Options {
		if e.Options[i].Type == t {
			return &e.Options[i]
		}
	}
	return nil
}

func (e *catalogEntry) hasReasoning() bool { return len(e.Options) > 0 }

// catalogSnapshot is the persisted catalog (file format version 2).
type catalogSnapshot struct {
	Version   int            `json:"version"`
	Source    string         `json:"source"`
	Origin    string         `json:"origin,omitempty"`
	FetchedAt time.Time      `json:"fetchedAt"`
	Models    []catalogEntry `json:"models"`
}

var (
	errCatalogEmpty     = errors.New("catalog is empty")
	errCatalogMalformed = errors.New("catalog is malformed")
	errSnapshotOutdated = errors.New("snapshot is outdated")
)

// aggregatorProviders re-sell other vendors' models and are never first-party.
var aggregatorProviders = map[string]bool{"openrouter": true, "github-copilot": true, "amazon-bedrock": true}

// vendorProviders maps a platform vendor key to catalog provider ids; the
// first one also supplies the canonical prefix.
var vendorProviders = map[string][]string{
	"openai": {"openai"}, "anthropic": {"anthropic"}, "google": {"google"}, "meta": {"meta", "llama"},
	"microsoft": {"microsoft"}, "amazon": {"amazon", "amazon-bedrock"}, "nvidia": {"nvidia"}, "deepseek": {"deepseek"},
	"moonshot": {"moonshotai", "moonshotai-cn"}, "zhipu": {"zhipuai", "zai"}, "minimax": {"minimax", "minimax-cn"},
	"bytedance": {"bytedance-seed", "volcengine"}, "tencent": {"tencent", "tencent-tokenhub"},
	"longcat": {"meituan", "longcat"}, "mistral": {"mistral"}, "alibaba": {"alibaba", "alibaba-cn"},
	"qwen": {"alibaba", "alibaba-cn"}, "xai": {"xai"}, "xiaomi": {"xiaomi"}, "stepfun": {"stepfun", "stepfun-ai"},
	"openrouter": {"openrouter"}, "copilot": {"github-copilot"},
}

func providersFor(vendor string) []string {
	if p, ok := vendorProviders[vendor]; ok {
		return p
	}
	return []string{vendor}
}

// knownMaker reports whether a name is a model maker the catalog maps to
// first-party providers (a vendor key or a provider id).
func knownMaker(p string) bool {
	p = strings.ToLower(strings.TrimSpace(p))
	if _, ok := vendorProviders[p]; ok {
		return true
	}
	return isFirstPartyProvider(p)
}

func isFirstPartyProvider(p string) bool {
	if aggregatorProviders[p] {
		return false
	}
	for _, list := range vendorProviders {
		if containsString(list, p) {
			return true
		}
	}
	return false
}

// firstParty reports whether an entry comes straight from the model's maker.
func (e *catalogEntry) firstParty() bool {
	if isFirstPartyProvider(e.Provider) {
		return true
	}
	if i := strings.Index(e.CanonicalID, "/"); i > 0 && !aggregatorProviders[e.Provider] && strings.ToLower(e.CanonicalID[:i]) == strings.ToLower(e.Provider) {
		return true
	}
	return false
}

// parseModelsDev parses the models.dev api.json document.
func parseModelsDev(raw []byte) ([]catalogEntry, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(raw, &top); err != nil || top == nil {
		return nil, errCatalogMalformed
	}
	var out []catalogEntry
	for providerID, provRaw := range top {
		var prov struct {
			ID     string                     `json:"id"`
			Models map[string]json.RawMessage `json:"models"`
		}
		if err := json.Unmarshal(provRaw, &prov); err != nil {
			continue
		}
		pid := strings.TrimSpace(providerID)
		if pid == "" {
			pid = strings.TrimSpace(prov.ID)
		}
		for key, mRaw := range prov.Models {
			var m struct {
				ID         string `json:"id"`
				Reasoning  bool   `json:"reasoning"`
				Modalities struct {
					Input  []string `json:"input"`
					Output []string `json:"output"`
				} `json:"modalities"`
				Limit struct {
					Context json.Number `json:"context"`
				} `json:"limit"`
				ReasoningOptions []struct {
					Type   string          `json:"type"`
					Values []string        `json:"values"`
					Min    json.RawMessage `json:"min"`
					Max    json.RawMessage `json:"max"`
				} `json:"reasoning_options"`
			}
			if err := json.Unmarshal(mRaw, &m); err != nil {
				continue
			}
			id := strings.TrimSpace(m.ID)
			if id == "" {
				id = strings.TrimSpace(key)
			}
			if id == "" {
				continue
			}
			e := catalogEntry{Provider: pid, ID: id}
			if m.Reasoning {
				seen := map[string]bool{}
				for _, o := range m.ReasoningOptions {
					t := strings.ToLower(strings.TrimSpace(o.Type))
					if seen[t] {
						continue
					}
					switch t {
					case optEffort:
						var vals []string
						for _, v := range o.Values {
							if v = lower(v); v != "" {
								vals = append(vals, v)
							}
						}
						if len(vals) == 0 {
							continue
						}
						seen[t] = true
						e.Options = append(e.Options, catalogOption{Type: t, Values: vals})
					case optBudget:
						seen[t] = true
						e.Options = append(e.Options, catalogOption{Type: t, Min: boundedInt(o.Min), Max: boundedInt(o.Max)})
					case optToggle:
						seen[t] = true
						e.Options = append(e.Options, catalogOption{Type: t})
					}
				}
			}
			e.Inputs = normalizeModalities(m.Modalities.Input, true)
			e.Outputs = normalizeModalities(m.Modalities.Output, false)
			if n, err := m.Limit.Context.Int64(); err == nil && n >= minContextWindow && n <= maxContextWindow {
				e.Context = int(n)
			}
			if aggregatorProviders[pid] {
				// An aggregator id "<maker>/<model>" names the maker's own model
				// only when the maker is one we know; other prefixes are just
				// the aggregator's own namespace.
				if i := strings.Index(id, "/"); i > 0 && knownMaker(id[:i]) {
					e.CanonicalID = id
				}
			} else {
				e.CanonicalID = pid + "/" + id
			}
			if len(e.Options) == 0 && len(e.Inputs) == 0 {
				continue
			}
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, errCatalogEmpty
	}
	sortEntries(out)
	return out, nil
}

func boundedInt(raw json.RawMessage) *int {
	if len(raw) == 0 {
		return nil
	}
	var n json.Number
	if err := json.Unmarshal(raw, &n); err != nil {
		return nil
	}
	v, err := n.Int64()
	if err != nil || v < 0 || v > math.MaxInt32 {
		return nil
	}
	i := int(v)
	return &i
}

func sortEntries(list []catalogEntry) {
	sort.SliceStable(list, func(i, j int) bool {
		if list[i].Provider != list[j].Provider {
			return list[i].Provider < list[j].Provider
		}
		return list[i].ID < list[j].ID
	})
}

// encodeSnapshot renders a snapshot; gz selects the (deterministic) gzip form
// used for the built-in copy, otherwise indented plain JSON.
func encodeSnapshot(s catalogSnapshot, gz bool) ([]byte, error) {
	s.Version = snapshotVersion
	if gz {
		raw, err := json.Marshal(s)
		if err != nil {
			return nil, err
		}
		var buf bytes.Buffer
		w, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		if _, err := w.Write(raw); err != nil {
			return nil, err
		}
		if err := w.Close(); err != nil {
			return nil, err
		}
		return buf.Bytes(), nil
	}
	return json.MarshalIndent(s, "", "  ")
}

// decodeSnapshot reads a snapshot file, detecting gzip by its magic bytes.
// Version 1 files are reported as errSnapshotOutdated.
func decodeSnapshot(raw []byte) (*catalogSnapshot, error) {
	if len(raw) >= 2 && raw[0] == 0x1f && raw[1] == 0x8b {
		r, err := gzip.NewReader(bytes.NewReader(raw))
		if err != nil {
			return nil, err
		}
		defer r.Close()
		raw, err = io.ReadAll(io.LimitReader(r, maxSnapshotBytes+1))
		if err != nil {
			return nil, err
		}
		if len(raw) > maxSnapshotBytes {
			return nil, errors.New("snapshot too large")
		}
	}
	var probe struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(raw, &probe); err != nil {
		return nil, errCatalogMalformed
	}
	if probe.Version == 1 {
		return nil, errSnapshotOutdated
	}
	if probe.Version != snapshotVersion {
		return nil, errCatalogMalformed
	}
	var s catalogSnapshot
	if err := json.Unmarshal(raw, &s); err != nil {
		return nil, errCatalogMalformed
	}
	var usable []catalogEntry
	for _, e := range s.Models {
		if strings.TrimSpace(e.ID) == "" || strings.TrimSpace(e.Provider) == "" {
			continue
		}
		if len(e.Options) == 0 && len(e.Inputs) == 0 {
			continue
		}
		usable = append(usable, e)
	}
	if len(usable) == 0 {
		return nil, errCatalogEmpty
	}
	s.Models = usable
	return &s, nil
}

// catalogIndex answers the lookups of the matching rules for one set of entries.
type catalogIndex struct {
	byProvider map[string]map[string][]*catalogEntry // provider -> lower id -> entries
	byBare     map[string][]*catalogEntry            // lower id -> entries
	byCanon    map[string][]*catalogEntry            // lower canonical id
	bySuffix   map[string][]*catalogEntry            // lower text after the last "/" of the canonical id
}

func newCatalogIndex(entries []*catalogEntry) *catalogIndex {
	ix := &catalogIndex{
		byProvider: map[string]map[string][]*catalogEntry{},
		byBare:     map[string][]*catalogEntry{},
		byCanon:    map[string][]*catalogEntry{},
		bySuffix:   map[string][]*catalogEntry{},
	}
	sorted := append([]*catalogEntry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.firstParty() != b.firstParty() {
			return a.firstParty()
		}
		if a.Provider != b.Provider {
			return a.Provider < b.Provider
		}
		return a.ID < b.ID
	})
	seen := map[string]bool{}
	for _, e := range sorted {
		key := e.Provider + "\x00" + strings.ToLower(e.ID)
		if seen[key] {
			continue
		}
		seen[key] = true
		id := strings.ToLower(e.ID)
		if ix.byProvider[e.Provider] == nil {
			ix.byProvider[e.Provider] = map[string][]*catalogEntry{}
		}
		ix.byProvider[e.Provider][id] = append(ix.byProvider[e.Provider][id], e)
		ix.byBare[id] = append(ix.byBare[id], e)
		if e.CanonicalID != "" {
			c := strings.ToLower(e.CanonicalID)
			ix.byCanon[c] = append(ix.byCanon[c], e)
			if i := strings.LastIndex(c, "/"); i >= 0 {
				ix.bySuffix[c[i+1:]] = append(ix.bySuffix[c[i+1:]], e)
			}
		}
	}
	return ix
}

// match applies the lookup order to a platform model. protocols are the
// model's protocol keys.
func (ix *catalogIndex) match(name, vendor string, protocols []string) *catalogEntry {
	if ix == nil {
		return nil
	}
	full := strings.ToLower(strings.TrimSpace(name))
	bare := full
	if i := strings.LastIndex(full, "/"); i >= 0 {
		bare = full[i+1:]
	}
	if bare == "" {
		return nil
	}
	// Vendor keys to derive providers from: the platform vendor first, then a
	// name prefix before the first "/".
	var vendorKeys []string
	if v := lower(vendor); v != "" && v != "unknown" {
		vendorKeys = append(vendorKeys, v)
	}
	if i := strings.Index(full, "/"); i > 0 {
		p := full[:i]
		if p != "unknown" && !containsString(vendorKeys, p) {
			vendorKeys = append(vendorKeys, p)
		}
	}
	var providers []string
	prefix := ""
	for _, vk := range vendorKeys {
		ps := providersFor(vk)
		if prefix == "" && len(ps) > 0 {
			prefix = ps[0]
		}
		for _, p := range ps {
			if !containsString(providers, p) {
				providers = append(providers, p)
			}
		}
	}
	// (1) exact id in the derived providers.
	for _, p := range providers {
		for _, id := range []string{bare, full} {
			if es := ix.byProvider[p][id]; len(es) > 0 {
				return es[0]
			}
		}
	}
	// (2) any first-party provider entry with id == bare.
	for _, e := range ix.byBare[bare] {
		if e.firstParty() {
			return e
		}
	}
	// (3) canonical id == prefix/bare.
	if prefix != "" {
		if es := ix.byCanon[prefix+"/"+bare]; len(es) > 0 {
			return es[0]
		}
	}
	// (4) any entry whose canonical id ends in /bare.
	if es := ix.bySuffix[bare]; len(es) > 0 {
		return es[0]
	}
	// (5) OpenRouter lookups, only for OpenRouter routes.
	for _, pk := range protocols {
		if strings.HasPrefix(pk, "openrouter_") {
			return ix.openRouter(full, prefix, bare)
		}
	}
	return nil
}

func (ix *catalogIndex) openRouter(full, prefix, bare string) *catalogEntry {
	if es := ix.byProvider["openrouter"][full]; len(es) > 0 {
		return es[0]
	}
	if prefix != "" {
		if es := ix.byProvider["openrouter"][prefix+"/"+bare]; len(es) > 0 {
			return es[0]
		}
	}
	return nil
}

// matchExternal is match followed by an OpenRouter lookup regardless of protocol.
func (ix *catalogIndex) matchExternal(name, vendor string, protocols []string) *catalogEntry {
	if e := ix.match(name, vendor, protocols); e != nil {
		return e
	}
	if ix == nil {
		return nil
	}
	full := strings.ToLower(strings.TrimSpace(name))
	bare := full
	if i := strings.LastIndex(full, "/"); i >= 0 {
		bare = full[i+1:]
	}
	if bare == "" {
		return nil
	}
	prefix := ""
	if v := lower(vendor); v != "" && v != "unknown" {
		prefix = providersFor(v)[0]
	} else if i := strings.Index(full, "/"); i > 0 {
		prefix = providersFor(full[:i])[0]
	}
	return ix.openRouter(full, prefix, bare)
}

// catalogView is an immutable loaded catalog with its two indexes.
type catalogView struct {
	snap      *catalogSnapshot
	reasoning *catalogIndex
	modality  *catalogIndex
}

func buildCatalogView(s *catalogSnapshot) *catalogView {
	v := &catalogView{snap: s}
	var r, m []*catalogEntry
	for i := range s.Models {
		e := &s.Models[i]
		if e.hasReasoning() {
			r = append(r, e)
		}
		if len(e.Inputs) > 0 {
			m = append(m, e)
		}
	}
	v.reasoning = newCatalogIndex(r)
	v.modality = newCatalogIndex(m)
	return v
}

func (v *catalogView) count() int {
	if v == nil || v.snap == nil {
		return 0
	}
	return len(v.snap.Models)
}

func (v *catalogView) matchReasoning(name, vendor string, protocols []string) *catalogEntry {
	if v == nil {
		return nil
	}
	return v.reasoning.match(name, vendor, protocols)
}

func (v *catalogView) matchModality(name, vendor string, protocols []string) *catalogEntry {
	if v == nil {
		return nil
	}
	return v.modality.match(name, vendor, protocols)
}

func (v *catalogView) matchModalityExternal(name, vendor string, protocols []string) *catalogEntry {
	if v == nil {
		return nil
	}
	return v.modality.matchExternal(name, vendor, protocols)
}
