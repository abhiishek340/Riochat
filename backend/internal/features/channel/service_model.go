package channel

import (
	"context"
	"encoding/json"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ---- lookups --------------------------------------------------------------------

func (s *Service) vendorMap(ctx context.Context) (map[string]ModelVendor, error) {
	var rows []ModelVendor
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[string]ModelVendor, len(rows))
	for _, v := range rows {
		out[v.Key] = v
	}
	return out, nil
}

func (s *Service) groupMap(ctx context.Context) (map[uint]ModelDisplayGroup, error) {
	var rows []ModelDisplayGroup
	if err := s.db.WithContext(ctx).Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[uint]ModelDisplayGroup, len(rows))
	for _, g := range rows {
		out[g.ID] = g
	}
	return out, nil
}

// ---- aggregates ---------------------------------------------------------------------

// modelAgg are the route aggregates of one platform model.
type modelAgg struct {
	routes      []routeRecord
	sourceCount int64
	chainCount  int64 // active-chain routes, circuits ignored
	liveCount   int64 // active-chain routes with closed circuits (breaker aware)
	protocols   []string
	upstreams   []string
}

func (s *Service) aggregate(ctx context.Context, m *PlatformModel, routes []routeRecord, breaker bool) modelAgg {
	a := modelAgg{routes: routes, sourceCount: int64(len(routes))}
	protos, ups := map[string]bool{}, map[string]bool{}
	for _, r := range routes {
		if !r.activeChain() {
			continue
		}
		a.chainCount++
		protos[r.Protocol] = true
		ups[r.UpName] = true
		live := true
		if breaker {
			if open, _, err := s.circuits.peekOpen(ctx, upstreamPrefix(r.UpstreamID)); err == nil && open {
				live = false
			} else if open, _, err := s.circuits.peekOpen(ctx, bindingPrefix(r.UpstreamID, r.BindingCode)); err == nil && open {
				live = false
			}
		}
		if live {
			a.liveCount++
		}
	}
	a.protocols, a.upstreams = sortedKeys(protos), sortedKeys(ups)
	if m.Status != StatusActive {
		a.liveCount = 0
	}
	return a
}

func jsonStringArray(list []string) string {
	if list == nil {
		list = []string{}
	}
	raw, _ := json.Marshal(list)
	return string(raw)
}

func (s *Service) modelView(m *PlatformModel, a modelAgg, vendors map[string]ModelVendor, groups map[uint]ModelDisplayGroup, fallback int) ModelView {
	v := ModelView{
		ID: m.ID, PlatformModelName: m.Name, Vendor: m.Vendor, VendorName: m.Vendor,
		DisplayGroupID: m.DisplayGroupID, KindsJSON: m.KindsJSON, Icon: m.Icon, CapabilitiesJSON: m.CapabilitiesJSON,
		ContextWindow: effectiveContextWindow(m.Name, parseCapsLenient(m.CapabilitiesJSON), fallback),
		SystemPrompt:  m.SystemPrompt, AccessScope: m.AccessScope, Status: m.Status, Description: m.Description,
		CBPolicyMode: m.CBPolicyMode, CBFailureThreshold: m.CBFailureThreshold, CBDurationMin: m.CBDurationMin,
		CBWindowMin: m.CBWindowMin, SortOrder: m.SortOrder, SourceCount: a.sourceCount, ActiveSourceCount: a.liveCount,
		ProtocolsJSON: jsonStringArray(a.protocols), UpstreamNamesJSON: jsonStringArray(a.upstreams),
		CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
	if vd, ok := vendors[m.Vendor]; ok {
		if vd.Name != "" {
			v.VendorName = vd.Name
		}
		v.VendorIcon = vd.Icon
	}
	if m.DisplayGroupID != nil {
		if g, ok := groups[*m.DisplayGroupID]; ok {
			v.DisplayGroupName, v.DisplayGroupIcon = g.Name, g.Icon
		}
	}
	return v
}

// ModelQuery are the list filters of GET /admin/llm/models.
type ModelQuery struct {
	Q             string
	Status        string
	OnlyActive    bool
	OnlyAvailable bool
	Vendor        string
	Protocol      string
	Upstream      string
	Sort          string
	Page          int
	PageSize      int
}

type modelRow struct {
	model PlatformModel
	agg   modelAgg
}

// defaultOrder sorts rows by availability rank, presentation group and sort order.
func defaultOrder(rows []modelRow) {
	rank := func(r modelRow) int {
		switch {
		case r.model.Status == StatusActive && r.agg.chainCount > 0:
			return 0
		case r.agg.sourceCount > 0:
			return 1
		}
		return 2
	}
	groupKey := func(m PlatformModel) string {
		if m.DisplayGroupID != nil {
			return "group:" + strconv.FormatUint(uint64(*m.DisplayGroupID), 10)
		}
		v := strings.ToLower(m.Vendor)
		if v == "" {
			v = strings.ToLower(m.Name)
		}
		return "vendor:" + v
	}
	type k struct {
		rank int
		key  string
	}
	minOrder := map[k]int{}
	for _, r := range rows {
		kk := k{rank(r), groupKey(r.model)}
		if cur, ok := minOrder[kk]; !ok || r.model.SortOrder < cur {
			minOrder[kk] = r.model.SortOrder
		}
	}
	sort.SliceStable(rows, func(i, j int) bool {
		a, b := rows[i], rows[j]
		ra, rb := rank(a), rank(b)
		if ra != rb {
			return ra < rb
		}
		ka, kb := groupKey(a.model), groupKey(b.model)
		ma, mb := minOrder[k{ra, ka}], minOrder[k{rb, kb}]
		if ma != mb {
			return ma < mb
		}
		if ka != kb {
			return ka < kb
		}
		if a.model.SortOrder != b.model.SortOrder {
			return a.model.SortOrder < b.model.SortOrder
		}
		return a.model.ID < b.model.ID
	})
}

func (s *Service) loadModelRows(ctx context.Context, where func(*gorm.DB) *gorm.DB) ([]modelRow, error) {
	var models []PlatformModel
	db := s.db.WithContext(ctx)
	if where != nil {
		db = where(db)
	}
	if err := db.Find(&models).Error; err != nil {
		return nil, err
	}
	ids := make([]uint, 0, len(models))
	for _, m := range models {
		ids = append(ids, m.ID)
	}
	routes, err := s.loadRoutes(ctx, ids)
	if err != nil {
		return nil, err
	}
	by := groupRoutesByModel(routes)
	breaker := s.BreakerEnabled(ctx)
	rows := make([]modelRow, 0, len(models))
	for i := range models {
		rows = append(rows, modelRow{model: models[i], agg: s.aggregate(ctx, &models[i], by[models[i].ID], breaker)})
	}
	return rows, nil
}

// ListModels lists platform models for administrators.
func (s *Service) ListModels(ctx context.Context, q ModelQuery) ([]ModelView, int64, error) {
	rows, err := s.loadModelRows(ctx, nil)
	if err != nil {
		return nil, 0, err
	}
	vendors, err := s.vendorMap(ctx)
	if err != nil {
		return nil, 0, err
	}
	groups, err := s.groupMap(ctx)
	if err != nil {
		return nil, 0, err
	}
	needle := strings.ToLower(strings.TrimSpace(q.Q))
	upstreamFilter := uint64(0)
	if q.Upstream != "" {
		if n, err := strconv.ParseUint(q.Upstream, 10, 64); err == nil {
			upstreamFilter = n
		}
	}
	filtered := rows[:0:0]
	for _, r := range rows {
		m := r.model
		if needle != "" {
			hay := []string{m.Name, m.Vendor, m.Description, vendors[m.Vendor].Name}
			if m.DisplayGroupID != nil {
				hay = append(hay, groups[*m.DisplayGroupID].Name)
			}
			found := false
			for _, h := range hay {
				if strings.Contains(strings.ToLower(h), needle) {
					found = true
					break
				}
			}
			if !found {
				continue
			}
		}
		switch {
		case q.OnlyAvailable:
			if m.Status != StatusActive || m.AccessScope != ScopePublic || r.agg.chainCount == 0 {
				continue
			}
		case q.OnlyActive:
			if m.Status != StatusActive {
				continue
			}
		case q.Status == StatusActive || q.Status == StatusInactive:
			if m.Status != q.Status {
				continue
			}
		}
		if q.Vendor != "" && m.Vendor != q.Vendor {
			continue
		}
		if q.Protocol != "" || upstreamFilter > 0 {
			ok := false
			for _, rt := range r.agg.routes {
				if !rt.activeChain() {
					continue
				}
				if (q.Protocol == "" || rt.Protocol == q.Protocol) && (upstreamFilter == 0 || uint64(rt.UpstreamID) == upstreamFilter) {
					ok = true
					break
				}
			}
			if !ok {
				continue
			}
		}
		filtered = append(filtered, r)
	}
	switch q.Sort {
	case "id_desc":
		sort.SliceStable(filtered, func(i, j int) bool { return filtered[i].model.ID > filtered[j].model.ID })
	case "platformModelName_asc":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].model.Name != filtered[j].model.Name {
				return filtered[i].model.Name < filtered[j].model.Name
			}
			return filtered[i].model.ID > filtered[j].model.ID
		})
	case "sourceCount_desc":
		sort.SliceStable(filtered, func(i, j int) bool {
			if filtered[i].agg.sourceCount != filtered[j].agg.sourceCount {
				return filtered[i].agg.sourceCount > filtered[j].agg.sourceCount
			}
			return filtered[i].model.ID > filtered[j].model.ID
		})
	case "updated_desc":
		sort.SliceStable(filtered, func(i, j int) bool {
			if !filtered[i].model.UpdatedAt.Equal(filtered[j].model.UpdatedAt) {
				return filtered[i].model.UpdatedAt.After(filtered[j].model.UpdatedAt)
			}
			return filtered[i].model.ID > filtered[j].model.ID
		})
	default:
		defaultOrder(filtered)
	}
	total := int64(len(filtered))
	from, to := pageWindow(q.Page, q.PageSize, len(filtered))
	fallback := s.contextFallbackTokens(ctx)
	out := make([]ModelView, 0, to-from)
	for _, r := range filtered[from:to] {
		out = append(out, s.modelView(&r.model, r.agg, vendors, groups, fallback))
	}
	return out, total, nil
}

// GetModel returns the admin view of one model.
func (s *Service) GetModel(ctx context.Context, id uint) (*ModelView, error) {
	rows, err := s.loadModelRows(ctx, func(db *gorm.DB) *gorm.DB { return db.Where("id = ?", id) })
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrModelNotFound
	}
	vendors, err := s.vendorMap(ctx)
	if err != nil {
		return nil, err
	}
	groups, err := s.groupMap(ctx)
	if err != nil {
		return nil, err
	}
	v := s.modelView(&rows[0].model, rows[0].agg, vendors, groups, s.contextFallbackTokens(ctx))
	return &v, nil
}

func (s *Service) loadModel(ctx context.Context, db *gorm.DB, id uint) (*PlatformModel, error) {
	var m PlatformModel
	if err := db.WithContext(ctx).First(&m, id).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrModelNotFound
		}
		return nil, err
	}
	return &m, nil
}

// ---- create --------------------------------------------------------------------------

func (s *Service) requireVendor(ctx context.Context, tx *gorm.DB, key string) error {
	var n int64
	if err := tx.WithContext(ctx).Model(&ModelVendor{}).Where("\"key\" = ?", key).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return badRequest(ErrVendorNotFound)
	}
	return nil
}

func (s *Service) requireGroup(ctx context.Context, tx *gorm.DB, id uint) error {
	var n int64
	if err := tx.WithContext(ctx).Model(&ModelDisplayGroup{}).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return badRequest(ErrGroupNotFound)
	}
	return nil
}

func (s *Service) nextSortOrder(tx *gorm.DB) (int, error) {
	var max *int
	if err := tx.Model(&PlatformModel{}).Select("MAX(sort_order)").Scan(&max).Error; err != nil {
		return 0, err
	}
	if max == nil {
		return 100, nil
	}
	return *max + 100, nil
}

// CreateModel creates a platform model.
func (s *Service) CreateModel(ctx context.Context, req createModelReq) (*ModelView, error) {
	name, err := normalizePlatformName(req.PlatformModelName)
	if err != nil {
		return nil, err
	}
	var kinds []string
	if strings.TrimSpace(req.KindsJSON) == "" {
		kinds = inferKinds(name)
	} else if kinds, err = normalizeKinds(req.KindsJSON); err != nil {
		return nil, err
	}
	var vendor string
	if strings.TrimSpace(req.Vendor) != "" {
		if vendor, err = normalizeVendorKey(req.Vendor); err != nil {
			return nil, err
		}
	} else {
		vendor = deriveVendor("", name)
	}
	scope, err := normalizeAccessScope(req.AccessScope)
	if err != nil {
		return nil, err
	}
	prompt, err := validateSystemPrompt(req.SystemPrompt)
	if err != nil {
		return nil, err
	}
	caps := strings.TrimSpace(req.CapabilitiesJSON)
	if err := validateCapabilitiesText(caps); err != nil {
		return nil, err
	}
	ref, err := parseIconRef(req.Icon)
	if err != nil {
		return nil, err
	}
	status := normalizeStatus(req.Status)
	if !validWritableStatus(status) {
		return nil, badRequest(apperr.ErrInvalidBody)
	}
	var created PlatformModel
	err = s.tx(ctx, func(tx *gorm.DB) error {
		if err := s.requireVendor(ctx, tx, vendor); err != nil {
			return err
		}
		var groupID *uint
		if req.DisplayGroupID > 0 {
			if err := s.requireGroup(ctx, tx, req.DisplayGroupID); err != nil {
				return err
			}
			groupID = ptr(req.DisplayGroupID)
		}
		icon := ref.Value
		if ref.AssetID != "" {
			if err := s.reserveIcon(ctx, tx, ref.AssetID); err != nil {
				return asBadRequest(err)
			}
		}
		if icon == "" {
			icon = autoIcon("", name, vendor)
		}
		var exists int64
		if err := tx.Model(&PlatformModel{}).Where("name = ?", name).Count(&exists).Error; err != nil {
			return err
		}
		if exists > 0 {
			return ErrPlatformNameExists
		}
		order, err := s.nextSortOrder(tx)
		if err != nil {
			return err
		}
		created = PlatformModel{
			Name: name, Vendor: vendor, DisplayGroupID: groupID, KindsJSON: kindsJSON(kinds), CapabilitiesJSON: caps,
			SystemPrompt: prompt, AccessScope: scope, Icon: icon, Description: req.Description,
			CBPolicyMode: normalizeCBPolicyMode(req.CBPolicyMode), CBFailureThreshold: req.CBFailureThreshold,
			CBDurationMin: req.CBDurationMin, CBWindowMin: req.CBWindowMin, Status: status, SortOrder: order,
		}
		if created.CapabilitiesJSON == "" {
			created.CapabilitiesJSON = "{}"
		}
		if err := tx.Create(&created).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrPlatformNameExists
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	return s.GetModel(ctx, created.ID)
}

// ---- update ---------------------------------------------------------------------------
