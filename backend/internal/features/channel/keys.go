package channel

import (
	"encoding/json"
	"fmt"
	"math/rand/v2"
	"strings"
)

// Key selection strategies.
const (
	StrategyFailover   = "failover"
	StrategyRoundRobin = "round_robin"
	StrategyRandom     = "random"
)

// keyEntry is one API key of an upstream.
type keyEntry struct {
	Key    string `json:"key"`
	Status string `json:"status"`
	Note   string `json:"note"`
}

func (k keyEntry) active() bool {
	s := strings.TrimSpace(k.Status)
	return strings.TrimSpace(k.Key) != "" && (s == "" || s == "active")
}

// keyDoc is the decrypted API key configuration of an upstream. Unknown
// top-level fields are carried through edits untouched.
type keyDoc struct {
	Strategy string
	Keys     []keyEntry
	extra    map[string]json.RawMessage
}

// parseKeyDoc parses the JSON document without validating its content.
func parseKeyDoc(raw string) (*keyDoc, error) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal([]byte(raw), &top); err != nil || top == nil {
		return nil, ErrInvalidAPIKeys
	}
	doc := &keyDoc{extra: map[string]json.RawMessage{}}
	for k, v := range top {
		switch k {
		case "strategy":
			var s string
			if err := json.Unmarshal(v, &s); err != nil {
				return nil, ErrInvalidAPIKeys
			}
			doc.Strategy = s
		case "keys":
			if err := json.Unmarshal(v, &doc.Keys); err != nil {
				return nil, ErrInvalidAPIKeys
			}
		default:
			doc.extra[k] = v
		}
	}
	return doc, nil
}

// validate requires at least one key and at least one active key.
func (d *keyDoc) validate() error {
	if len(d.Keys) == 0 {
		return ErrInvalidAPIKeys
	}
	for _, k := range d.Keys {
		if k.active() {
			return nil
		}
	}
	return ErrInvalidAPIKeys
}

func (d *keyDoc) marshal() (string, error) {
	top := map[string]any{}
	for k, v := range d.extra {
		top[k] = v
	}
	top["strategy"] = d.Strategy
	keys := d.Keys
	if keys == nil {
		keys = []keyEntry{}
	}
	top["keys"] = keys
	raw, err := json.Marshal(top)
	return string(raw), err
}

func (d *keyDoc) activeKeys() []keyEntry {
	var out []keyEntry
	for _, k := range d.Keys {
		if k.active() {
			out = append(out, k)
		}
	}
	return out
}

func (d *keyDoc) normalizedStrategy() string {
	switch strings.TrimSpace(d.Strategy) {
	case StrategyFailover:
		return StrategyFailover
	case StrategyRoundRobin:
		return StrategyRoundRobin
	}
	return StrategyRandom
}

// maskKey shows at most the first and last four characters of a key.
func maskKey(key string) string {
	r := []rune(strings.TrimSpace(key))
	if len(r) <= 8 {
		return "****"
	}
	return string(r[:4]) + "****" + string(r[len(r)-4:])
}

// masked returns the document with every key masked. A blank status or note is
// left out, as in the stored document a client first sent.
func (d *keyDoc) masked() string {
	top := map[string]any{}
	for k, v := range d.extra {
		top[k] = v
	}
	top["strategy"] = d.Strategy
	keys := make([]map[string]string, 0, len(d.Keys))
	for _, k := range d.Keys {
		e := map[string]string{"key": maskKey(k.Key)}
		if k.Status != "" {
			e["status"] = k.Status
		}
		if k.Note != "" {
			e["note"] = k.Note
		}
		keys = append(keys, e)
	}
	top["keys"] = keys
	raw, _ := json.Marshal(top)
	return string(raw)
}

// APIKeyItem is the public view of one key.
type APIKeyItem struct {
	ID        string `json:"id"`
	Index     int    `json:"index"`
	KeyMasked string `json:"keyMasked"`
	Status    string `json:"status"`
	Note      string `json:"note"`
}

// keyID derives the opaque identifier of the key at an index. hash is the
// keyed digest function of the secret ring.
func keyID(hash func(string) string, index int, key string) string {
	return hash(fmt.Sprintf("llm-upstream-key|%d|%s", index, key))
}

func (d *keyDoc) items(hash func(string) string) []APIKeyItem {
	out := make([]APIKeyItem, 0, len(d.Keys))
	for i, k := range d.Keys {
		out = append(out, APIKeyItem{ID: keyID(hash, i, k.Key), Index: i, KeyMasked: maskKey(k.Key), Status: k.Status, Note: k.Note})
	}
	return out
}

// applyEdit removes the keys with the given ids and appends the added ones.
// Every id must match exactly one existing key; a repeated id counts once.
func (d *keyDoc) applyEdit(hash func(string) string, deleteIDs []string, added []keyEntry) error {
	if len(deleteIDs) > 0 {
		byID := map[string]int{}
		for i, k := range d.Keys {
			byID[keyID(hash, i, k.Key)] = i
		}
		drop := map[int]bool{}
		for _, id := range deleteIDs {
			idx, ok := byID[id]
			if !ok {
				return ErrInvalidAPIKeys
			}
			drop[idx] = true
		}
		kept := make([]keyEntry, 0, len(d.Keys))
		for i, k := range d.Keys {
			if !drop[i] {
				kept = append(kept, k)
			}
		}
		d.Keys = kept
	}
	d.Keys = append(d.Keys, added...)
	return d.validate()
}

// pickKey selects an active key. counter is used for round robin and returns
// the next counter value.
func (d *keyDoc) pickKey(counter func() int64) (string, bool) {
	active := d.activeKeys()
	if len(active) == 0 {
		return "", false
	}
	switch d.normalizedStrategy() {
	case StrategyFailover:
		return active[0].Key, true
	case StrategyRoundRobin:
		n := counter()
		if n < 0 {
			n = -n
		}
		return active[int(n%int64(len(active)))].Key, true
	}
	return active[rand.IntN(len(active))].Key, true
}
