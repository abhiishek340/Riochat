package channel

import (
	"context"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Errors of the permission-group administration surface.
var (
	ErrInvalidPermGroupID = apperr.Define("permission_group.invalid_id", "invalid permission group id").WithStatus(http.StatusBadRequest)
	ErrInvalidGroupName   = apperr.Define("admin.invalid_permission_group_name", "invalid permission group name").WithStatus(http.StatusBadRequest)
	ErrInvalidGroupRate   = apperr.Define("admin.invalid_permission_group_rate_multiplier", "invalid permission group rate multiplier").WithStatus(http.StatusBadRequest)
	ErrInvalidGroupUsers  = apperr.Define("admin.invalid_permission_group_users", "invalid permission group users").WithStatus(http.StatusBadRequest)

	ErrPermGroupNotFound   = apperr.Define("admin.permission_group_not_found", "permission group not found").WithStatus(http.StatusNotFound)
	ErrDefaultGroupDelete  = apperr.Define("admin.default_permission_group_delete_not_allowed", "default permission group cannot be deleted").WithStatus(http.StatusConflict)
	ErrDefaultGroupMembers = apperr.Define("admin.default_permission_group_users_implicit", "default permission group users are implicit").WithStatus(http.StatusConflict)
	ErrGroupUsedByPlan     = apperr.Define("admin.permission_group_referenced_by_plan", "permission group is referenced by a billing plan").WithStatus(http.StatusConflict)
)

// PlanReferences tells how many billing plans are bound to a permission group.
// The billing area implements it; without one a group is never considered in
// use.
type PlanReferences interface {
	PlansReferencingGroup(ctx context.Context, groupID uint) (int64, error)
}

// UserVerifier reports which of the given user ids belong to existing
// accounts. Without one the area reads the identity users table itself.
type UserVerifier interface {
	ExistingUserIDs(ctx context.Context, ids []uint) ([]uint, error)
}

// Bounds of the group fields.
const (
	maxGroupName        = 128
	maxGroupDescription = 512
	maxGroupRate        = 10000
	defaultGroupRate    = 100
)

// AdminGroupView is one permission group with its figures, as the admin console
// lists it.
type AdminGroupView struct {
	ID                    uint      `json:"id"`
	Name                  string    `json:"name"`
	Description           string    `json:"description"`
	IsDefault             bool      `json:"isDefault"`
	RateMultiplierPercent int       `json:"rateMultiplierPercent"`
	ModelCount            int64     `json:"modelCount"`
	ManualModelCount      int64     `json:"manualModelCount"`
	RuleModelCount        int64     `json:"ruleModelCount"`
	UserCount             int64     `json:"userCount"`
	ManualUserCount       int64     `json:"manualUserCount"`
	SubscriptionUserCount int64     `json:"subscriptionUserCount"`
	CreatedAt             time.Time `json:"createdAt"`
	UpdatedAt             time.Time `json:"updatedAt"`
}

// GroupInput is the editable part of a group.
type GroupInput struct {
	Name                  string
	Description           string
	RateMultiplierPercent int
}

// GroupModelSet is the stored model access of a group.
type GroupModelSet struct {
	ModelIDs []uint      `json:"modelIDs"`
	Rules    []GroupRule `json:"rules"`
}

// GroupDeletion describes what removing a group took with it.
type GroupDeletion struct {
	ManualModelCount int64 `json:"manualModelCount"`
	RuleCount        int64 `json:"ruleCount"`
	ManualUserCount  int64 `json:"manualUserCount"`
	PlanCount        int64 `json:"planCount"`
}

// ModelGroupSet is the group reach of one model.
type ModelGroupSet struct {
	ManualGroupIDs    []uint `json:"manualGroupIDs"`
	MatchedGroupIDs   []uint `json:"matchedGroupIDs"`
	EffectiveGroupIDs []uint `json:"effectiveGroupIDs"`
	Unassigned        bool   `json:"unassigned"`
}

func normalizeGroupInput(in GroupInput) (GroupInput, error) {
	in.Name = strings.TrimSpace(in.Name)
	in.Description = strings.TrimSpace(in.Description)
	if in.Name == "" || len([]rune(in.Name)) > maxGroupName || len([]rune(in.Description)) > maxGroupDescription {
		return in, ErrInvalidGroupName
	}
	if in.RateMultiplierPercent == 0 {
		in.RateMultiplierPercent = defaultGroupRate
	}
	if in.RateMultiplierPercent < 1 || in.RateMultiplierPercent > maxGroupRate {
		return in, ErrInvalidGroupRate
	}
	return in, nil
}

// ruleMatches reports whether one rule selects a model with the given facts.
// Rule type and value are compared literally; an unknown type matches nothing
// and so does an empty value on every type except "all".
func ruleMatches(r PermissionGroupModelRule, f publicFacts) bool {
	rt, val := strings.TrimSpace(r.RuleType), strings.TrimSpace(r.Value)
	switch rt {
	case RuleAll:
		return true
	case RuleVendor:
		return val != "" && val == f.vendor
	case RuleProtocol:
		return val != "" && containsString(f.protocols, val)
	case RuleUpstream:
		if val == "" {
			return false
		}
		for _, u := range f.upstreams {
			if strconv.FormatUint(uint64(u), 10) == val {
				return true
			}
		}
	}
	return false
}

// factsByModel derives the access facts (vendor, usable protocols, usable
// upstreams) of every platform model.
func (s *Service) factsByModel(ctx context.Context, models []PlatformModel) (map[uint]publicFacts, error) {
	routes, err := s.loadRoutes(ctx, nil)
	if err != nil {
		return nil, err
	}
	by := groupRoutesByModel(routes)
	out := make(map[uint]publicFacts, len(models))
	for _, m := range models {
		f := publicFacts{vendor: m.Vendor}
		protos := map[string]bool{}
		seen := map[uint]bool{}
		for _, r := range by[m.ID] {
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
		out[m.ID] = f
	}
	return out, nil
}

func adminGroupView(g PermissionGroup) AdminGroupView {
	return AdminGroupView{
		ID: g.ID, Name: g.Name, Description: g.Description, IsDefault: g.IsDefault,
		RateMultiplierPercent: g.RateMultiplierPercent, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt,
	}
}

// AdminListGroups lists every group, the default one first and the rest by id,
// with model and user figures.
func (s *Service) AdminListGroups(ctx context.Context) ([]AdminGroupView, error) {
	db := s.db.WithContext(ctx)
	var groups []PermissionGroup
	if err := db.Order("id ASC").Find(&groups).Error; err != nil {
		return nil, err
	}
	var models []PlatformModel
	if err := db.Find(&models).Error; err != nil {
		return nil, err
	}
	facts, err := s.factsByModel(ctx, models)
	if err != nil {
		return nil, err
	}
	var grants []PermissionGroupModelAccess
	if err := db.Find(&grants).Error; err != nil {
		return nil, err
	}
	var rules []PermissionGroupModelRule
	if err := db.Find(&rules).Error; err != nil {
		return nil, err
	}
	var members []PermissionGroupUserAccess
	if err := db.Find(&members).Error; err != nil {
		return nil, err
	}
	known := make(map[uint]bool, len(models))
	for _, m := range models {
		known[m.ID] = true
	}
	manualModels := map[uint]map[uint]bool{}
	for _, g := range grants {
		if !known[g.PlatformModelID] {
			continue
		}
		if manualModels[g.GroupID] == nil {
			manualModels[g.GroupID] = map[uint]bool{}
		}
		manualModels[g.GroupID][g.PlatformModelID] = true
	}
	rulesBy := map[uint][]PermissionGroupModelRule{}
	for _, r := range rules {
		rulesBy[r.GroupID] = append(rulesBy[r.GroupID], r)
	}
	manualUsers := map[uint]map[uint]bool{}
	for _, m := range members {
		if manualUsers[m.GroupID] == nil {
			manualUsers[m.GroupID] = map[uint]bool{}
		}
		manualUsers[m.GroupID][m.UserID] = true
	}
	lister, _ := s.opts.Subscriptions.(SubscriptionUserLister)
	var totalUsers int64
	if s.opts.Users != nil {
		if totalUsers, err = s.opts.Users.CountAllUsers(ctx); err != nil {
			return nil, err
		}
	}

	out := make([]AdminGroupView, 0, len(groups))
	for _, g := range groups {
		v := adminGroupView(g)
		matched := map[uint]bool{}
		for _, m := range models {
			for _, r := range rulesBy[g.ID] {
				if ruleMatches(r, facts[m.ID]) {
					matched[m.ID] = true
					break
				}
			}
		}
		union := map[uint]bool{}
		for id := range manualModels[g.ID] {
			union[id] = true
		}
		for id := range matched {
			union[id] = true
		}
		v.ManualModelCount, v.RuleModelCount, v.ModelCount = int64(len(manualModels[g.ID])), int64(len(matched)), int64(len(union))
		if g.IsDefault {
			v.UserCount = totalUsers
		} else {
			people := map[uint]bool{}
			for id := range manualUsers[g.ID] {
				people[id] = true
			}
			v.ManualUserCount = int64(len(manualUsers[g.ID]))
			if lister != nil {
				ids, err := lister.SubscriptionGroupUserIDs(ctx, g.ID, s.now())
				if err != nil {
					return nil, err
				}
				subs := map[uint]bool{}
				for _, id := range ids {
					if id != 0 {
						subs[id] = true
						people[id] = true
					}
				}
				v.SubscriptionUserCount = int64(len(subs))
			}
			v.UserCount = int64(len(people))
		}
		out = append(out, v)
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].IsDefault != out[j].IsDefault {
			return out[i].IsDefault
		}
		return out[i].ID < out[j].ID
	})
	return out, nil
}

// AdminGroup returns one group with its figures.
func (s *Service) AdminGroup(ctx context.Context, id uint) (*AdminGroupView, error) {
	list, err := s.AdminListGroups(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].ID == id {
			return &list[i], nil
		}
	}
	return nil, ErrPermGroupNotFound
}

func (s *Service) requirePermGroup(ctx context.Context, db *gorm.DB, id uint) (*PermissionGroup, error) {
	var g PermissionGroup
	if err := db.WithContext(ctx).Where("id = ?", id).Take(&g).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrPermGroupNotFound
		}
		return nil, err
	}
	return &g, nil
}

// AdminCreateGroup creates an ordinary (never default) group.
func (s *Service) AdminCreateGroup(ctx context.Context, in GroupInput) (*AdminGroupView, error) {
	in, err := normalizeGroupInput(in)
	if err != nil {
		return nil, err
	}
	g := PermissionGroup{Name: in.Name, Description: in.Description, RateMultiplierPercent: in.RateMultiplierPercent}
	if err := s.db.WithContext(ctx).Create(&g).Error; err != nil {
		return nil, err
	}
	return s.AdminGroup(ctx, g.ID)
}

// AdminUpdateGroup replaces name, description and rate of a group.
func (s *Service) AdminUpdateGroup(ctx context.Context, id uint, in GroupInput) (*AdminGroupView, error) {
	in, err := normalizeGroupInput(in)
	if err != nil {
		return nil, err
	}
	if _, err := s.requirePermGroup(ctx, s.db, id); err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Model(&PermissionGroup{}).Where("id = ?", id).Updates(map[string]any{
		"name": in.Name, "description": in.Description, "rate_multiplier_percent": in.RateMultiplierPercent,
		"updated_at": s.now().UTC(),
	}).Error
	if err != nil {
		return nil, err
	}
	view, err := s.AdminGroup(ctx, id)
	if err != nil {
		return nil, err
	}
	if view.IsDefault {
		// The update response of the default group has always reported zero
		// model and user counts; the list endpoint carries the real figures.
		view.ModelCount, view.ManualModelCount, view.UserCount = 0, 0, 0
	}
	return view, nil
}

// AdminDeleteGroup removes an ordinary group that no plan references, together
// with its grants, rules and memberships, and reports what went away.
func (s *Service) AdminDeleteGroup(ctx context.Context, id uint) (*GroupDeletion, error) {
	g, err := s.requirePermGroup(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	if g.IsDefault {
		return nil, ErrDefaultGroupDelete
	}
	if s.opts.Plans != nil {
		n, err := s.opts.Plans.PlansReferencingGroup(ctx, id)
		if err != nil {
			return nil, err
		}
		if n > 0 {
			return nil, ErrGroupUsedByPlan
		}
	}
	var sum GroupDeletion
	err = s.tx(ctx, func(tx *gorm.DB) error {
		for _, step := range []struct {
			model any
			into  *int64
		}{
			{&PermissionGroupModelAccess{}, &sum.ManualModelCount},
			{&PermissionGroupModelRule{}, &sum.RuleCount},
			{&PermissionGroupUserAccess{}, &sum.ManualUserCount},
		} {
			if err := tx.Model(step.model).Where("group_id = ?", id).Count(step.into).Error; err != nil {
				return err
			}
			if err := tx.Where("group_id = ?", id).Delete(step.model).Error; err != nil {
				return err
			}
		}
		return tx.Where("id = ?", id).Delete(&PermissionGroup{}).Error
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	return &sum, nil
}

// AdminGroupModels returns the manual grants and the rules of a group.
func (s *Service) AdminGroupModels(ctx context.Context, id uint) (*GroupModelSet, error) {
	if _, err := s.requirePermGroup(ctx, s.db, id); err != nil {
		return nil, err
	}
	db := s.db.WithContext(ctx)
	out := &GroupModelSet{ModelIDs: []uint{}, Rules: []GroupRule{}}
	if err := db.Model(&PermissionGroupModelAccess{}).Where("group_id = ?", id).
		Order("platform_model_id ASC").Pluck("platform_model_id", &out.ModelIDs).Error; err != nil {
		return nil, err
	}
	var rules []PermissionGroupModelRule
	if err := db.Where("group_id = ?", id).Order("rule_type ASC, value ASC").Find(&rules).Error; err != nil {
		return nil, err
	}
	for _, r := range rules {
		out.Rules = append(out.Rules, GroupRule{Type: r.RuleType, Value: r.Value})
	}
	return out, nil
}

// existingModelIDs returns how many of the ids are platform models.
func (s *Service) allModelsExist(ctx context.Context, ids []uint) (bool, error) {
	if len(ids) == 0 {
		return true, nil
	}
	var n int64
	if err := s.db.WithContext(ctx).Model(&PlatformModel{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
		return false, err
	}
	return int(n) == len(ids), nil
}
