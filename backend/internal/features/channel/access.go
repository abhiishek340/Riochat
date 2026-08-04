package channel

import (
	"context"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"
)

// groupData is a snapshot of the group tables used for access evaluation.
type groupData struct {
	groups     map[uint]PermissionGroup
	manual     map[uint]map[uint]bool // platform model id -> group ids
	rules      map[uint][]PermissionGroupModelRule
	defaultIDs []uint
}

func (s *Service) loadGroupData(ctx context.Context) (*groupData, error) {
	d := &groupData{groups: map[uint]PermissionGroup{}, manual: map[uint]map[uint]bool{}, rules: map[uint][]PermissionGroupModelRule{}}
	db := s.db.WithContext(ctx)
	var gs []PermissionGroup
	if err := db.Find(&gs).Error; err != nil {
		return nil, err
	}
	for _, g := range gs {
		d.groups[g.ID] = g
		if g.IsDefault {
			d.defaultIDs = append(d.defaultIDs, g.ID)
		}
	}
	var links []PermissionGroupModelAccess
	if err := db.Find(&links).Error; err != nil {
		return nil, err
	}
	for _, l := range links {
		if d.manual[l.PlatformModelID] == nil {
			d.manual[l.PlatformModelID] = map[uint]bool{}
		}
		d.manual[l.PlatformModelID][l.GroupID] = true
	}
	var rules []PermissionGroupModelRule
	if err := db.Find(&rules).Error; err != nil {
		return nil, err
	}
	for _, r := range rules {
		d.rules[r.GroupID] = append(d.rules[r.GroupID], r)
	}
	return d, nil
}

// modelGroups are the groups a model belongs to: manual grants plus dynamic rules.
func (d *groupData) modelGroups(modelID uint, f publicFacts) map[uint]bool {
	out := map[uint]bool{}
	for g := range d.manual[modelID] {
		if _, ok := d.groups[g]; ok {
			out[g] = true
		}
	}
	for gid, rules := range d.rules {
		if _, ok := d.groups[gid]; !ok {
			continue
		}
		for _, r := range rules {
			switch r.RuleType {
			case RuleAll:
				out[gid] = true
			case RuleVendor:
				if r.Value == f.vendor {
					out[gid] = true
				}
			case RuleProtocol:
				if containsString(f.protocols, r.Value) {
					out[gid] = true
				}
			case RuleUpstream:
				for _, u := range f.upstreams {
					if r.Value == strconv.FormatUint(uint64(u), 10) {
						out[gid] = true
					}
				}
			}
		}
	}
	return out
}

// userGroups are the effective groups of a user: manual membership, default
// groups and the group of the active subscription plan.
func (s *Service) userGroups(ctx context.Context, d *groupData, userID uint) (map[uint]bool, error) {
	out := map[uint]bool{}
	for _, id := range d.defaultIDs {
		out[id] = true
	}
	var rows []PermissionGroupUserAccess
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		if _, ok := d.groups[r.GroupID]; ok {
			out[r.GroupID] = true
		}
	}
	if s.opts.Subscriptions != nil {
		gid, ok, err := s.opts.Subscriptions.SubscriptionGroupID(ctx, userID, s.now())
		if err != nil {
			return nil, err
		}
		if _, exists := d.groups[gid]; ok && exists {
			out[gid] = true
		}
	}
	return out, nil
}

func intersects(a, b map[uint]bool) bool {
	for k := range a {
		if b[k] {
			return true
		}
	}
	return false
}

// newAccessFilter builds the per-user filter over the cached catalog.
func (s *Service) newAccessFilter(ctx context.Context, userID uint, e *publicEntry) (func(PublicModel) bool, error) {
	d, err := s.loadGroupData(ctx)
	if err != nil {
		return nil, err
	}
	mine, err := s.userGroups(ctx, d, userID)
	if err != nil {
		return nil, err
	}
	return func(m PublicModel) bool {
		return intersects(d.modelGroups(m.modelID, e.facts[m.modelID]), mine)
	}, nil
}

// modelFacts loads the access facts of one model from the database.
func (s *Service) modelFacts(ctx context.Context, m *PlatformModel) (publicFacts, error) {
	routes, err := s.loadRoutes(ctx, []uint{m.ID})
	if err != nil {
		return publicFacts{}, err
	}
	f := publicFacts{vendor: m.Vendor}
	protos := map[string]bool{}
	seen := map[uint]bool{}
	for _, r := range routes {
		if !r.activeChain() {
			continue
		}
		protos[r.Protocol] = true
		if !seen[r.UpstreamID] {
			seen[r.UpstreamID] = true
			f.upstreams = append(f.upstreams, r.UpstreamID)
		}
	}
	f.protocols = sortedKeys(protos)
	return f, nil
}

// CheckModelAccess is the route-time access check for one model. User id 0
// bypasses it. A model without groups denies.
func (s *Service) CheckModelAccess(ctx context.Context, userID uint, m *PlatformModel) error {
	if userID == 0 {
		return nil
	}
	d, err := s.loadGroupData(ctx)
	if err != nil {
		return err
	}
	facts, err := s.modelFacts(ctx, m)
	if err != nil {
		return err
	}
	mine, err := s.userGroups(ctx, d, userID)
	if err != nil {
		return err
	}
	if !intersects(d.modelGroups(m.ID, facts), mine) {
		return ErrModelAccessDenied
	}
	return nil
}

// RateMultiplier is the user's rate multiplier percent for a model: the
// minimum among groups shared by the user and the model, otherwise 100.
func (s *Service) RateMultiplier(ctx context.Context, userID, modelID uint) (int, error) {
	if userID == 0 || modelID == 0 {
		return 100, nil
	}
	var m PlatformModel
	if err := s.db.WithContext(ctx).First(&m, modelID).Error; err != nil {
		if isNotFound(err) {
			return 100, nil
		}
		return 100, err
	}
	d, err := s.loadGroupData(ctx)
	if err != nil {
		return 100, err
	}
	facts, err := s.modelFacts(ctx, &m)
	if err != nil {
		return 100, err
	}
	mine, err := s.userGroups(ctx, d, userID)
	if err != nil {
		return 100, err
	}
	best := 0
	for g := range d.modelGroups(modelID, facts) {
		if !mine[g] {
			continue
		}
		p := d.groups[g].RateMultiplierPercent
		if p <= 0 {
			p = 100
		}
		if best == 0 || p < best {
			best = p
		}
	}
	if best == 0 {
		return 100, nil
	}
	return best, nil
}

// ---- group maintenance (persistence used by the admin and billing areas) ----------

func normalizeMultiplier(p int) int {
	if p <= 0 {
		return 100
	}
	return p
}
