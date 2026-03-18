package integration

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/channel"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// PermissionMigrations seeds the default model-access group. The tables belong
// to the channel area; the platform contract asks for exactly one default
// group, and nothing else in this build would ever create it (without a group
// no user sees any model).
func PermissionMigrations() []migrate.Step {
	return []migrate.Step{migrate.Seed("default_group", seedDefaultPermissionGroup)}
}

// MountPermissions has no routes: the group administration routes are served by
// the channel area, which owns the group tables (see GAPS.md).
func (h *Hub) MountPermissions(deps.Deps) error { return nil }

// seedDefaultPermissionGroup keeps exactly one default permission group (the lowest id
// flagged default, else a new "Default" group), removes explicit membership
// rows that point at it (membership is implicit), and, when no model access or
// rule exists anywhere, grants it every model with a rule of type "all". It
// never touches a group an administrator has edited beyond that.
func seedDefaultPermissionGroup(ctx context.Context, db *gorm.DB) error {
	db = db.WithContext(ctx)
	return db.Transaction(func(tx *gorm.DB) error {
		var defaults []channel.PermissionGroup
		if err := tx.Where("is_default = ?", true).Order("id ASC").Find(&defaults).Error; err != nil {
			return err
		}
		var def channel.PermissionGroup
		if len(defaults) == 0 {
			def = channel.PermissionGroup{Name: "Default", Description: "All users implicitly belong to this group", IsDefault: true, RateMultiplierPercent: 100}
			if err := tx.Create(&def).Error; err != nil {
				return err
			}
		} else {
			def = defaults[0]
			for _, extra := range defaults[1:] {
				if err := tx.Model(&channel.PermissionGroup{}).Where("id = ?", extra.ID).Update("is_default", false).Error; err != nil {
					return err
				}
			}
		}
		if err := tx.Where("group_id = ?", def.ID).Delete(&channel.PermissionGroupUserAccess{}).Error; err != nil {
			return err
		}
		var access, rules int64
		if err := tx.Model(&channel.PermissionGroupModelAccess{}).Count(&access).Error; err != nil {
			return err
		}
		if err := tx.Model(&channel.PermissionGroupModelRule{}).Count(&rules).Error; err != nil {
			return err
		}
		if access == 0 && rules == 0 {
			return tx.Create(&channel.PermissionGroupModelRule{GroupID: def.ID, RuleType: channel.RuleAll, Value: ""}).Error
		}
		return nil
	})
}
