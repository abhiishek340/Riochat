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

// storedMap returns the effective stored value of every (namespace, key).
func (s *Service) storedMap(ctx context.Context) (map[string]string, error) {
	rows, err := s.repo.all(ctx)
	if err != nil {
		return nil, err
	}
	eff, _ := collapseRows(rows)
	m := make(map[string]string, len(eff))
	for _, row := range eff {
		m[nk(row.Namespace, row.Key)] = row.Value
	}
	return m, nil
}

// Patch validates and applies a batch of changes atomically and returns the
// fresh listing. Any validation failure rejects the whole batch.
func (s *Service) Patch(ctx context.Context, actor Actor, items []PatchItem) (map[string][]Item, error) {
	return s.patch(ctx, actor, items, true)
}

// patch is Patch with the audit entry optional: callers that keep their own
// audit trail (the billing configuration endpoint) pass audit=false.
func (s *Service) patch(ctx context.Context, actor Actor, items []PatchItem, audit bool) (map[string][]Item, error) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()

	ops, err := validateItems(items)
	if err != nil {
		return nil, err
	}
	if len(ops) > 0 {
		stored, err := s.storedMap(ctx)
		if err != nil {
			return nil, err
		}
		st := &patchState{stored: stored, ops: ops}

		if err := s.checkLocks(st); err != nil {
			return nil, err
		}
		if st.touchesNamespace("auth") {
			if err := s.authRules(ctx, st); err != nil {
				return nil, err
			}
		}
		if err := s.embeddingRules(ctx, st); err != nil {
			return nil, err
		}
		if st.touchesNamespace("extract") || st.touchesNamespace("file") {
			if err := extractRules(st); err != nil {
				return nil, err
			}
		}
		if st.touchesNamespace("billing") {
			if err := billingRules(st); err != nil {
				return nil, err
			}
		}
		reconcile := false
		if st.touchesNamespace("file") {
			reconcile = s.deriveSignature(st)
		}
		if err := s.persist(ctx, st.ops); err != nil {
			return nil, err
		}
		for _, o := range st.ops {
			s.cacheDelete(ctx, o.ns(), o.key())
		}
		if err := s.reloadLocked(ctx); err != nil {
			return nil, err
		}
		if audit {
			s.auditPatch(ctx, actor, items)
		}
		if reconcile {
			if idx := s.port().Embedding; idx != nil {
				if err := idx.Reconcile(ctx); err != nil {
					return nil, fmt.Errorf("settings: embedding reconcile: %w", err)
				}
			}
		}
	} else if audit {
		s.auditPatch(ctx, actor, items)
	}
	return s.List(ctx)
}

// persist writes the changes in one transaction.
func (s *Service) persist(ctx context.Context, ops []op) error {
	now := s.clock.Now()
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, o := range ops {
			value := o.value
			if o.def.sensitive {
				if o.clear {
					value = ""
				} else {
					enc, err := s.encrypt(value)
					if err != nil {
						return fmt.Errorf("settings: encrypt %s/%s: %w", o.ns(), o.key(), err)
					}
					value = enc
				}
			}
			if err := upsertRow(tx, o.ns(), o.key(), value, string(o.def.typ), o.def.desc, now, false); err != nil {
				return err
			}
		}
		return nil
	})
}

// ---- feature locks ---------------------------------------------------------------

// lockedKeys maps keys to the capability whose absence locks them.
var lockedKeys = []struct {
	ns, key, feature string
}{
	{"billing", "mode", capability.BillingGating},
	{"billing", "payment_providers", capability.BillingGating},
	{"desktop", "download_enabled", capability.DesktopDownload},
	{"desktop", "download_url", capability.DesktopDownload},
}

// checkLocks rejects a batch that changes a key whose capability is off. A
// locked key may be resubmitted with its current stored value.
func (s *Service) checkLocks(st *patchState) error {
	if s.caps == nil {
		return nil
	}
	byFeature := map[string][]string{}
	for _, lk := range lockedKeys {
		if s.caps.Enabled(lk.feature) {
			continue
		}
		for _, o := range st.ops {
			if o.ns() != lk.ns || o.key() != lk.key {
				continue
			}
			current := strings.TrimSpace(st.stored[nk(lk.ns, lk.key)])
			if _, ok := st.stored[nk(lk.ns, lk.key)]; !ok {
				current = strings.TrimSpace(o.def.def)
			}
			if o.clear || strings.TrimSpace(o.value) != current {
				byFeature[lk.feature] = append(byFeature[lk.feature], lk.ns+"."+lk.key)
				break
			}
		}
	}
	for _, feature := range []string{capability.BillingGating, capability.DesktopDownload} {
		if keys := byFeature[feature]; len(keys) > 0 {
			return featureLocked(feature, keys)
		}
	}
	return nil
}
