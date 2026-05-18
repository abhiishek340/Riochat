package uicomponent

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent steps: the table, then the
// synchronisation of built-in rows with the product's built-in list.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables", &UIComponent{}),
		migrate.Seed("builtin_catalog", func(ctx context.Context, db *gorm.DB) error {
			return SyncBuiltins(ctx, db)
		}),
	}
}

// SyncBuiltins reconciles the built-in rows with the current built-in list in a
// single transaction. Rows whose name left the list are removed. Missing rows
// are created enabled. Existing rows have description, props summary, version,
// sort order and renderer kind restored from the list while their enabled flag,
// an administrator decision, is kept. Platform and user rows are never touched.
func SyncBuiltins(ctx context.Context, db *gorm.DB) error {
	return syncCatalog(ctx, db, builtinCatalog())
}

func syncCatalog(ctx context.Context, db *gorm.DB, catalog []builtinSpec) error {
	err := db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []UIComponent
		if err := tx.Where("scope = ? AND owner_user_id = ?", ScopeBuiltin, 0).Find(&rows).Error; err != nil {
			return err
		}
		byName := make(map[string]*UIComponent, len(rows))
		for i := range rows {
			byName[rows[i].Name] = &rows[i]
		}
		wanted := make(map[string]bool, len(catalog))
		for _, spec := range catalog {
			wanted[spec.Name] = true
		}
		for _, row := range rows {
			if !wanted[row.Name] {
				if err := tx.Delete(&UIComponent{}, row.ID).Error; err != nil {
					return err
				}
			}
		}
		for i, spec := range catalog {
			order := i + 1
			cur := byName[spec.Name]
			if cur == nil {
				row := UIComponent{
					Scope: ScopeBuiltin, Name: spec.Name, Version: 1,
					Description: spec.Description, PropsSummary: spec.PropsSummary,
					RendererKind: RendererBuiltin, Enabled: true, SortOrder: order,
				}
				if err := tx.Create(&row).Error; err != nil {
					return err
				}
				continue
			}
			if cur.Description == spec.Description && cur.PropsSummary == spec.PropsSummary &&
				cur.Version == 1 && cur.SortOrder == order && cur.RendererKind == RendererBuiltin {
				continue
			}
			err := tx.Model(&UIComponent{}).Where("id = ?", cur.ID).Updates(map[string]any{
				"description":   spec.Description,
				"props_summary": spec.PropsSummary,
				"version":       1,
				"sort_order":    order,
				"renderer_kind": RendererBuiltin,
			}).Error
			if err != nil {
				return err
			}
		}
		return nil
	})
	return database.Translate(err)
}
