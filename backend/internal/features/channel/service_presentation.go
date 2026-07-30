package channel

import (
	"context"
	"encoding/json"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ---- vendors -----------------------------------------------------------------------------

func vendorView(v *ModelVendor) VendorView {
	return VendorView{ID: v.ID, Key: v.Key, Name: v.Name, Icon: v.Icon, BuiltIn: v.BuiltIn, SortOrder: v.SortOrder,
		CreatedAt: v.CreatedAt, UpdatedAt: v.UpdatedAt}
}

func groupView(g *ModelDisplayGroup) GroupView {
	return GroupView{ID: g.ID, Name: g.Name, Icon: g.Icon, SortOrder: g.SortOrder, CreatedAt: g.CreatedAt, UpdatedAt: g.UpdatedAt}
}

// ListVendors lists the vendor catalog ordered by sort order then id.
func (s *Service) ListVendors(ctx context.Context, q string, page, size int) ([]VendorView, int64, error) {
	build := func() *gorm.DB {
		db := s.db.WithContext(ctx).Model(&ModelVendor{})
		if t := strings.TrimSpace(q); t != "" {
			like := "%" + escapeLike(t) + "%"
			db = db.Where(`LOWER("key") LIKE ? ESCAPE '\' OR LOWER(name) LIKE ? ESCAPE '\'`, like, like)
		}
		return db
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ModelVendor
	if err := build().Order("sort_order ASC, id ASC").Offset(pageOffset(page, size)).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]VendorView, 0, len(rows))
	for i := range rows {
		out = append(out, vendorView(&rows[i]))
	}
	return out, total, nil
}

// resolveIconInput validates an icon reference and, for managed assets, renews
// the asset's lease. Asset problems answer 400 here (the reference comes from a
// request body, not from a URL).
func (s *Service) resolveIconInput(ctx context.Context, tx *gorm.DB, raw string) (string, error) {
	ref, err := parseIconRef(raw)
	if err != nil {
		return "", err
	}
	if ref.AssetID != "" {
		if err := s.reserveIcon(ctx, tx, ref.AssetID); err != nil {
			return "", asBadRequest(err)
		}
	}
	return ref.Value, nil
}

// CreateVendor adds a custom vendor.
func (s *Service) CreateVendor(ctx context.Context, req createVendorReq) (*VendorView, error) {
	key, err := normalizeVendorKey(req.Key)
	if err != nil {
		return nil, err
	}
	name, ok := normalizeDisplayName(req.Name)
	if !ok {
		return nil, ErrInvalidVendor
	}
	var created ModelVendor
	err = s.tx(ctx, func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&ModelVendor{}).Where("\"key\" = ?", key).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrVendorExists
		}
		icon, err := s.resolveIconInput(ctx, tx, req.Icon)
		if err != nil {
			return err
		}
		var max *int
		if err := tx.Model(&ModelVendor{}).Select("MAX(sort_order)").Scan(&max).Error; err != nil {
			return err
		}
		order := 100
		if max != nil {
			order = *max + 100
		}
		created = ModelVendor{Key: key, Name: name, Icon: icon, SortOrder: order}
		if err := tx.Create(&created).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrVendorExists
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Vendor creation does not change the public catalog.
	v := vendorView(&created)
	return &v, nil
}

func (s *Service) loadVendor(ctx context.Context, tx *gorm.DB, rawKey string) (*ModelVendor, error) {
	key := lower(rawKey)
	if canon := canonicalVendor(key); canon != "" {
		key = canon
	}
	var v ModelVendor
	if err := tx.WithContext(ctx).Where("\"key\" = ?", key).First(&v).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrVendorNotFound
		}
		return nil, err
	}
	return &v, nil
}

// UpdateVendor changes a vendor's name and icon. The key never changes.
func (s *Service) UpdateVendor(ctx context.Context, rawKey string, req updateVendorReq) (*VendorView, error) {
	var out ModelVendor
	err := s.tx(ctx, func(tx *gorm.DB) error {
		v, err := s.loadVendor(ctx, tx, rawKey)
		if err != nil {
			return err
		}
		if req.Name != nil {
			name, ok := normalizeDisplayName(*req.Name)
			if !ok {
				return ErrInvalidVendor
			}
			v.Name = name
		}
		if req.Icon != nil {
			if v.Icon, err = s.resolveIconInput(ctx, tx, *req.Icon); err != nil {
				return err
			}
		}
		if err := tx.Save(v).Error; err != nil {
			return err
		}
		out = *v
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	view := vendorView(&out)
	return &view, nil
}

// VendorUsage is the detail attached to a refused vendor deletion.
type VendorUsage struct {
	Reason         string             `json:"reason"`
	ReferenceCount int64              `json:"referenceCount"`
	Models         []VendorUsageModel `json:"models"`
}

// VendorUsageModel is one referencing model in a VendorUsage preview.
type VendorUsageModel struct {
	ID                uint   `json:"id"`
	PlatformModelName string `json:"platformModelName"`
}

// DeleteVendor removes a custom vendor that no model references.
func (s *Service) DeleteVendor(ctx context.Context, rawKey string) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		v, err := s.loadVendor(ctx, tx, rawKey)
		if err != nil {
			return err
		}
		var count int64
		if err := tx.Model(&PlatformModel{}).Where("vendor = ?", v.Key).Count(&count).Error; err != nil {
			return err
		}
		usage := VendorUsage{ReferenceCount: count, Models: []VendorUsageModel{}}
		if count > 0 {
			var rows []PlatformModel
			if err := tx.Where("vendor = ?", v.Key).Order("id ASC").Limit(20).Find(&rows).Error; err != nil {
				return err
			}
			for _, m := range rows {
				usage.Models = append(usage.Models, VendorUsageModel{ID: m.ID, PlatformModelName: m.Name})
			}
		}
		if v.BuiltIn {
			usage.Reason = "built_in"
			return ErrVendorBuiltin.WithDetails(usage)
		}
		if count > 0 {
			usage.Reason = "referenced_models"
			return ErrVendorInUse.WithDetails(usage)
		}
		return tx.Delete(&ModelVendor{}, v.ID).Error
	})
	if err != nil {
		return err
	}
	s.invalidateCatalog()
	return nil
}

// ---- display groups -------------------------------------------------------------------------

// ListGroups lists display groups ordered by sort order then id.
func (s *Service) ListGroups(ctx context.Context, q string, page, size int) ([]GroupView, int64, error) {
	build := func() *gorm.DB {
		db := s.db.WithContext(ctx).Model(&ModelDisplayGroup{})
		if t := strings.TrimSpace(q); t != "" {
			db = db.Where(`LOWER(name) LIKE ? ESCAPE '\'`, "%"+escapeLike(t)+"%")
		}
		return db
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []ModelDisplayGroup
	if err := build().Order("sort_order ASC, id ASC").Offset(pageOffset(page, size)).Limit(size).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]GroupView, 0, len(rows))
	for i := range rows {
		out = append(out, groupView(&rows[i]))
	}
	return out, total, nil
}

func checkMemberIDs(tx *gorm.DB, ids []uint) ([]uint, error) {
	ids = uniqueUints(ids)
	for _, id := range ids {
		if id == 0 {
			return nil, ErrInvalidDisplayGroup
		}
	}
	if len(ids) > 0 {
		var n int64
		if err := tx.Model(&PlatformModel{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
			return nil, err
		}
		if int(n) != len(ids) {
			return nil, ErrInvalidDisplayGroup
		}
	}
	return ids, nil
}

// CreateGroup adds a display group, optionally moving models into it.
func (s *Service) CreateGroup(ctx context.Context, req createGroupReq) (*GroupView, error) {
	name, ok := normalizeDisplayName(req.Name)
	if !ok {
		return nil, ErrInvalidDisplayGroup
	}
	var created ModelDisplayGroup
	err := s.tx(ctx, func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&ModelDisplayGroup{}).Where("name = ?", name).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return ErrGroupExists
		}
		ids, err := checkMemberIDs(tx, req.ModelIDs)
		if err != nil {
			return err
		}
		icon, err := s.resolveIconInput(ctx, tx, req.Icon)
		if err != nil {
			return err
		}
		var max *int
		if err := tx.Model(&ModelDisplayGroup{}).Select("MAX(sort_order)").Scan(&max).Error; err != nil {
			return err
		}
		order := 100
		if max != nil {
			order = *max + 100
		}
		created = ModelDisplayGroup{Name: name, Icon: icon, SortOrder: order}
		if err := tx.Create(&created).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrGroupExists
			}
			return err
		}
		if len(ids) > 0 {
			return tx.Model(&PlatformModel{}).Where("id IN ?", ids).Update("display_group_id", created.ID).Error
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	v := groupView(&created)
	return &v, nil
}

// UpdateGroup edits a group. A present modelIDs list replaces the member set.
func (s *Service) UpdateGroup(ctx context.Context, id uint, req updateGroupReq) (*GroupView, error) {
	var out ModelDisplayGroup
	err := s.tx(ctx, func(tx *gorm.DB) error {
		var g ModelDisplayGroup
		if err := tx.First(&g, id).Error; err != nil {
			if isNotFound(err) {
				return ErrGroupNotFound
			}
			return err
		}
		if req.Name != nil {
			name, ok := normalizeDisplayName(*req.Name)
			if !ok {
				return ErrInvalidDisplayGroup
			}
			if name != g.Name {
				var n int64
				if err := tx.Model(&ModelDisplayGroup{}).Where("name = ? AND id <> ?", name, g.ID).Count(&n).Error; err != nil {
					return err
				}
				if n > 0 {
					return ErrGroupExists
				}
				g.Name = name
			}
		}
		if req.Icon != nil {
			icon, err := s.resolveIconInput(ctx, tx, *req.Icon)
			if err != nil {
				return err
			}
			g.Icon = icon
		}
		var ids []uint
		if req.ModelIDs != nil {
			var err error
			if ids, err = checkMemberIDs(tx, *req.ModelIDs); err != nil {
				return err
			}
		}
		if err := tx.Save(&g).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrGroupExists
			}
			return err
		}
		if req.ModelIDs != nil {
			// Un-group former members that are no longer listed, then pull the
			// listed models in from wherever they were.
			q := tx.Model(&PlatformModel{}).Where("display_group_id = ?", g.ID)
			if len(ids) > 0 {
				q = q.Where("id NOT IN ?", ids)
			}
			if err := q.Update("display_group_id", nil).Error; err != nil {
				return err
			}
			if len(ids) > 0 {
				if err := tx.Model(&PlatformModel{}).Where("id IN ?", ids).Update("display_group_id", g.ID).Error; err != nil {
					return err
				}
			}
		}
		out = g
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	v := groupView(&out)
	return &v, nil
}

// DeleteGroup removes a group; its members return to vendor-based display.
func (s *Service) DeleteGroup(ctx context.Context, id uint) error {
	err := s.tx(ctx, func(tx *gorm.DB) error {
		var g ModelDisplayGroup
		if err := tx.First(&g, id).Error; err != nil {
			if isNotFound(err) {
				return ErrGroupNotFound
			}
			return err
		}
		if err := tx.Model(&PlatformModel{}).Where("display_group_id = ?", id).Update("display_group_id", nil).Error; err != nil {
			return err
		}
		return tx.Delete(&ModelDisplayGroup{}, id).Error
	})
	if err != nil {
		return err
	}
	s.invalidateCatalog()
	return nil
}

// ---- global settings --------------------------------------------------------------------------

func settingView(r *settingRow) SettingView {
	return SettingView{ID: r.ID, Key: r.Key, Value: r.Value, Description: r.Description, CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt}
}

// ListSettings returns the rows of the llm namespace ordered by id.
func (s *Service) ListSettings(ctx context.Context) ([]SettingView, error) {
	db := s.db.WithContext(ctx)
	out := []SettingView{}
	if !db.Migrator().HasTable(&settingRow{}) {
		return out, nil
	}
	var rows []settingRow
	if err := db.Where("namespace = ?", settingsNamespace).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		out = append(out, settingView(&rows[i]))
	}
	return out, nil
}

// UpdateSetting changes the value of an existing llm setting.
func (s *Service) UpdateSetting(ctx context.Context, key, value string) (*SettingView, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		return nil, ErrInvalidSettingKey
	}
	db := s.db.WithContext(ctx)
	if !db.Migrator().HasTable(&settingRow{}) {
		return nil, ErrSettingNotFound
	}
	var row settingRow
	if err := db.Where("namespace = ? AND \"key\" = ?", settingsNamespace, key).First(&row).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrSettingNotFound
		}
		return nil, err
	}
	value = strings.TrimSpace(value)
	if !json.Valid([]byte(value)) {
		return nil, ErrInvalidJSON
	}
	var parsed breakerDefaults
	isBreaker := key == "circuit_breaker.defaults"
	wasEnabled := false
	if isBreaker {
		var err error
		if parsed, err = parseBreakerDefaults(value); err != nil {
			return nil, err
		}
		wasEnabled = breakerEnabledIn(row.Value)
		if parsed.Enabled && !wasEnabled {
			// Stale state from an earlier enabled period must not leak in.
			if err := s.circuits.resetAll(ctx); err != nil {
				return nil, err
			}
		}
	}
	row.Value = value
	if err := db.Model(&settingRow{}).Where("id = ?", row.ID).Updates(map[string]any{
		"value": value, "updated_at": s.now(),
	}).Error; err != nil {
		return nil, err
	}
	row.UpdatedAt = s.now()
	if isBreaker {
		s.setBreakerCache(parsed)
		if wasEnabled && !parsed.Enabled {
			if err := s.circuits.resetAll(ctx); err != nil {
				s.log.Warn("llm_circuit_cleanup_failed")
			}
		}
	}
	v := settingView(&row)
	return &v, nil
}
