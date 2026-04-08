package settings

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/capability"
)

// PatchItem is one requested change. For a sensitive key an empty value without
// Clear leaves the stored secret alone; Clear is the only way to blank it.
type PatchItem struct {
	Namespace string `json:"namespace"`
	Key       string `json:"key"`
	Value     string `json:"value"`
	Clear     bool   `json:"clear"`
}

// Actor identifies who made a change, for the audit trail.
type Actor struct {
	UserID    uint
	RequestID string
	ClientIP  string
	UserAgent string
}

// op is one validated change. value is stored exactly as submitted for plain
// settings and encrypted for sensitive ones.
type op struct {
	def   *definition
	value string
	clear bool
}

func (o op) ns() string  { return o.def.ns }
func (o op) key() string { return o.def.key }

// patchState holds what the cross-field rules look at: the stored rows plus the
// batch laid over them.
type patchState struct {
	stored map[string]string
	ops    []op
}

func nk(ns, key string) string { return ns + "\x00" + key }

// effective returns the trimmed value a setting would have after the batch. The
// last occurrence of a key in the batch wins. For sensitive keys only the
// distinction blank / non-blank is meaningful.
func (st *patchState) effective(ns, key string) string {
	for i := len(st.ops) - 1; i >= 0; i-- {
		if st.ops[i].ns() == ns && st.ops[i].key() == key {
			return strings.TrimSpace(st.ops[i].value)
		}
	}
	if v, ok := st.stored[nk(ns, key)]; ok {
		return strings.TrimSpace(v)
	}
	if d := reg.lookup(ns, key); d != nil {
		return strings.TrimSpace(d.def)
	}
	return ""
}

func (st *patchState) effBool(ns, key string) bool {
	b, err := strconv.ParseBool(st.effective(ns, key))
	if err != nil {
		if d := reg.lookup(ns, key); d != nil {
			b, _ = strconv.ParseBool(d.def)
		}
	}
	return b
}

func (st *patchState) present(ns, key string) bool { return st.effective(ns, key) != "" }

func (st *patchState) inBatch(ns, key string) bool {
	for _, o := range st.ops {
		if o.ns() == ns && o.key() == key {
			return true
		}
	}
	return false
}

func (st *patchState) touchesNamespace(ns string) bool {
	for _, o := range st.ops {
		if o.ns() == ns {
			return true
		}
	}
	return false
}

// explicitTrue reports whether the first occurrence of a key in the batch sets
// it to true.
func (st *patchState) explicitTrue(ns, key string) bool {
	for _, o := range st.ops {
		if o.ns() == ns && o.key() == key {
			b, err := strconv.ParseBool(strings.TrimSpace(o.value))
			return err == nil && b
		}
	}
	return false
}

// set replaces the item for a key (dropping duplicates) or appends one.
func (st *patchState) set(ns, key, value string) {
	d := reg.lookup(ns, key)
	if d == nil {
		return
	}
	var out []op
	placed := false
	for _, o := range st.ops {
		if o.ns() == ns && o.key() == key {
			if !placed {
				out = append(out, op{def: d, value: value})
				placed = true
			}
			continue
		}
		out = append(out, o)
	}
	if !placed {
		out = append(out, op{def: d, value: value})
	}
	st.ops = out
}

// validateItems checks every item and returns the changes to apply.
func validateItems(items []PatchItem) ([]op, error) {
	var ops []op
	for _, it := range items {
		ns, key := strings.TrimSpace(it.Namespace), strings.TrimSpace(it.Key)
		field := fieldID(ns, key)
		if !reg.ns[ns] {
			return nil, invalid(ErrInvalidNamespace, field, violation{rule: "invalid_namespace"})
		}
		d := reg.lookup(ns, key)
		if d == nil {
			return nil, invalid(ErrInvalidKey, field, violation{rule: "invalid_key"})
		}
		if it.Clear {
			if !d.sensitive {
				return nil, invalid(d.group.err(), field, violation{rule: "clear_not_allowed"})
			}
			ops = append(ops, op{def: d, clear: true})
			continue
		}
		trimmed := strings.TrimSpace(it.Value)
		if d.sensitive && trimmed == "" {
			continue
		}
		if d.check != nil {
			if v := d.check(trimmed); v != nil {
				return nil, invalid(d.group.err(), field, *v)
			}
		}
		ops = append(ops, op{def: d, value: it.Value})
	}
	return ops, nil
}
