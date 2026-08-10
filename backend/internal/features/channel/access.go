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

// CreatePermissionGroup creates a group.
func (s *Service) CreatePermissionGroup(ctx context.Context, g PermissionGroup) (*PermissionGroup, error) {
	g.ID = 0
	g.RateMultiplierPercent = normalizeMultiplier(g.RateMultiplierPercent)
	if err := s.db.WithContext(ctx).Create(&g).Error; err != nil {
		return nil, err
	}
	return &g, nil
}

// UpdatePermissionGroup updates name, description, default flag and multiplier.
func (s *Service) UpdatePermissionGroup(ctx context.Context, g PermissionGroup) error {
	return s.db.WithContext(ctx).Model(&PermissionGroup{}).Where("id = ?", g.ID).Updates(map[string]any{
		"name": g.Name, "description": g.Description, "is_default": g.IsDefault,
		"rate_multiplier_percent": normalizeMultiplier(g.RateMultiplierPercent),
	}).Error
}

// DeletePermissionGroup removes a group with its model links, rules and user links.
func (s *Service) DeletePermissionGroup(ctx context.Context, id uint) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		for _, m := range []any{&PermissionGroupModelAccess{}, &PermissionGroupModelRule{}, &PermissionGroupUserAccess{}} {
			if err := tx.Where("group_id = ?", id).Delete(m).Error; err != nil {
				return err
			}
		}
		return tx.Delete(&PermissionGroup{}, id).Error
	})
	if err == nil {
		s.invalidateCatalog()
	}
	return err
}

// SetGroupModels replaces the manually granted models of a group.
func (s *Service) SetGroupModels(ctx context.Context, groupID uint, modelIDs []uint) error {
	return s.tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&PermissionGroupModelAccess{}).Error; err != nil {
			return err
		}
		for _, id := range uniqueUints(modelIDs) {
			if id == 0 {
				continue
			}
			if err := tx.Create(&PermissionGroupModelAccess{GroupID: groupID, PlatformModelID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// GroupRule is one dynamic access rule.
type GroupRule struct {
	Type  string `json:"type"`
	Value string `json:"value"`
}

// SetGroupRules replaces the dynamic rules of a group (keyed by type+value).
func (s *Service) SetGroupRules(ctx context.Context, groupID uint, rules []GroupRule) error {
	return s.tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&PermissionGroupModelRule{}).Error; err != nil {
			return err
		}
		seen := map[GroupRule]bool{}
		for _, r := range rules {
			r.Type, r.Value = strings.TrimSpace(r.Type), strings.TrimSpace(r.Value)
			if r.Type == "" || (r.Type != RuleAll && r.Value == "") || seen[r] {
				continue
			}
			seen[r] = true
			if err := tx.Create(&PermissionGroupModelRule{GroupID: groupID, RuleType: r.Type, Value: r.Value}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SetGroupUsers replaces the manual members of a group.
func (s *Service) SetGroupUsers(ctx context.Context, groupID uint, userIDs []uint) error {
	return s.tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("group_id = ?", groupID).Delete(&PermissionGroupUserAccess{}).Error; err != nil {
			return err
		}
		for _, id := range uniqueUints(userIDs) {
			if id == 0 {
				continue
			}
			if err := tx.Create(&PermissionGroupUserAccess{GroupID: groupID, UserID: id}).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// SetModelGroups replaces the manual groups of one model without touching
// dynamic rules.
func (s *Service) SetModelGroups(ctx context.Context, modelID uint, groupIDs []uint) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		if err := tx.Where("platform_model_id = ?", modelID).Delete(&PermissionGroupModelAccess{}).Error; err != nil {
			return err
		}
		ids := uniqueUints(groupIDs)
		sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
		for _, id := range ids {
			if id == 0 {
				continue
			}
			if err := tx.Create(&PermissionGroupModelAccess{GroupID: id, PlatformModelID: modelID}).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err == nil {
		s.invalidateCatalog()
	}
	return err
}

// ManualModelGroups returns the groups manually granted to a model.
func (s *Service) ManualModelGroups(ctx context.Context, modelID uint) ([]uint, error) {
	var ids []uint
	err := s.db.WithContext(ctx).Model(&PermissionGroupModelAccess{}).Where("platform_model_id = ?", modelID).
		Order("group_id ASC").Pluck("group_id", &ids).Error
	return ids, err
}

// GroupSummary reports per-group counts.
type GroupSummary struct {
	PermissionGroup
	ModelCount            int64 `json:"modelCount"`
	ManualModelCount      int64 `json:"manualModelCount"`
	RuleModelCount        int64 `json:"ruleModelCount"`
	UserCount             int64 `json:"userCount"`
	ManualUserCount       int64 `json:"manualUserCount"`
	SubscriptionUserCount int64 `json:"subscriptionUserCount"`
}

// ListPermissionGroups lists groups with their counts. Rule-matched models
// are evaluated against the current catalog facts.
func (s *Service) ListPermissionGroups(ctx context.Context) ([]GroupSummary, error) {
	d, err := s.loadGroupData(ctx)
	if err != nil {
		return nil, err
	}
	var models []PlatformModel
	if err := s.db.WithContext(ctx).Find(&models).Error; err != nil {
		return nil, err
	}
	routes, err := s.loadRoutes(ctx, nil)
	if err != nil {
		return nil, err
	}
	by := groupRoutesByModel(routes)
	manualUsers := map[uint]map[uint]bool{}
	var ua []PermissionGroupUserAccess
	if err := s.db.WithContext(ctx).Find(&ua).Error; err != nil {
		return nil, err
	}
	for _, u := range ua {
		if manualUsers[u.GroupID] == nil {
			manualUsers[u.GroupID] = map[uint]bool{}
		}
		manualUsers[u.GroupID][u.UserID] = true
	}
	lister, _ := s.opts.Subscriptions.(SubscriptionUserLister)
	var totalUsers int64
	if s.opts.Users != nil {
		if totalUsers, err = s.opts.Users.CountAllUsers(ctx); err != nil {
			return nil, err
		}
	}
	ids := make([]uint, 0, len(d.groups))
	for id := range d.groups {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	out := []GroupSummary{}
	for _, id := range ids {
		sum := GroupSummary{PermissionGroup: d.groups[id], ManualUserCount: int64(len(manualUsers[id]))}
		members := map[uint]bool{}
		for u := range manualUsers[id] {
			members[u] = true
		}
		if lister != nil {
			subs, err := lister.SubscriptionGroupUserIDs(ctx, id, s.now())
			if err != nil {
				return nil, err
			}
			seenSub := map[uint]bool{}
			for _, u := range subs {
				if u != 0 && !seenSub[u] {
					seenSub[u] = true
					members[u] = true
				}
			}
			sum.SubscriptionUserCount = int64(len(seenSub))
		}
		sum.UserCount = int64(len(members))
		if sum.IsDefault {
			sum.UserCount = totalUsers
		}
		for _, m := range models {
			facts := publicFacts{vendor: m.Vendor}
			protos := map[string]bool{}
			seen := map[uint]bool{}
			for _, r := range by[m.ID] {
				if r.activeChain() {
					protos[r.Protocol] = true
					if !seen[r.UpstreamID] {
						seen[r.UpstreamID] = true
						facts.upstreams = append(facts.upstreams, r.UpstreamID)
					}
				}
			}
			facts.protocols = sortedKeys(protos)
			manual := d.manual[m.ID][id]
			ruleMatch := false
			if !manual || true {
				only := &groupData{groups: map[uint]PermissionGroup{id: d.groups[id]}, manual: map[uint]map[uint]bool{}, rules: map[uint][]PermissionGroupModelRule{id: d.rules[id]}}
				ruleMatch = only.modelGroups(m.ID, facts)[id]
			}
			if manual {
				sum.ManualModelCount++
			}
			if ruleMatch {
				sum.RuleModelCount++
			}
			if manual || ruleMatch {
				sum.ModelCount++
			}
		}
		out = append(out, sum)
	}
	return out, nil
}
