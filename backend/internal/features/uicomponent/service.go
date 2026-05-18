package uicomponent

import (
	"context"
	"fmt"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// bucket identifies one uniqueness bucket: a scope together with its owner.
type bucket struct {
	scope string
	owner uint
}

var platformBucket = bucket{scope: ScopePlatform}

func userBucket(id uint) bucket { return bucket{scope: ScopeUser, owner: id} }

// Service is the registry's application layer. The chat area uses its exported
// methods (Enabled, Resolve, Catalog) when it builds a turn.
type Service struct {
	db       *gorm.DB
	clk      clock.Clock
	settings Settings
}

// NewService builds a Service. A nil clock selects the system clock and nil
// settings leave the global switch on.
func NewService(db *gorm.DB, clk clock.Clock, settings Settings) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	if settings == nil {
		settings = AlwaysEnabled()
	}
	return &Service{db: db, clk: clk, settings: settings}
}

// Enabled reports the global switch chat.ui_components_enabled.
func (s *Service) Enabled(ctx context.Context) bool {
	return s.settings.UIComponentsEnabled(ctx)
}

// listFilter narrows a management listing.
type listFilter struct {
	Keyword string
	Enabled *bool
	Scope   string // admin only: "", builtin or platform
}

// likePattern turns a keyword into a case-folded substring pattern whose
// wildcard characters match literally.
func likePattern(keyword string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(strings.ToLower(keyword)) + "%"
}

func applyKeyword(q *gorm.DB, keyword string) *gorm.DB {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return q
	}
	p := likePattern(keyword)
	return q.Where(`(LOWER(name) LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\')`, p, p)
}

const (
	visibleOrder    = "CASE scope WHEN 'builtin' THEN 0 WHEN 'platform' THEN 1 ELSE 2 END ASC, sort_order ASC, id ASC"
	managementOrder = "enabled DESC, sort_order ASC, updated_at DESC, id DESC"
)

func (s *Service) page(q *gorm.DB, order string, p pagination.Page) ([]UIComponent, int64, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	rows := make([]UIComponent, 0)
	if err := q.Session(&gorm.Session{}).Order(order).Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	return rows, total, nil
}

// visibleScope restricts q to enabled rows the user may see.
func visibleScope(q *gorm.DB, userID uint) *gorm.DB {
	return q.Where("enabled = ?", true).
		Where("(scope IN ? OR (scope = ? AND owner_user_id = ?))", []string{ScopeBuiltin, ScopePlatform}, ScopeUser, userID)
}

// listVisible is the catalog the picker shows. A switched-off feature yields an
// empty page rather than an error.
func (s *Service) listVisible(ctx context.Context, userID uint, keyword string, p pagination.Page) ([]UIComponent, int64, error) {
	if userID == 0 {
		return nil, 0, ErrInvalid
	}
	if !s.Enabled(ctx) {
		return []UIComponent{}, 0, nil
	}
	q := visibleScope(s.db.WithContext(ctx).Model(&UIComponent{}), userID)
	return s.page(applyKeyword(q, keyword), visibleOrder, p)
}

// listMine lists the caller's own components regardless of the global switch.
func (s *Service) listMine(ctx context.Context, userID uint, f listFilter, p pagination.Page) ([]UIComponent, int64, error) {
	if userID == 0 {
		return nil, 0, ErrInvalid
	}
	q := s.db.WithContext(ctx).Model(&UIComponent{}).Where("scope = ? AND owner_user_id = ?", ScopeUser, userID)
	if f.Enabled != nil {
		q = q.Where("enabled = ?", *f.Enabled)
	}
	return s.page(applyKeyword(q, f.Keyword), managementOrder, p)
}

// listAdmin lists built-in and platform rows (never user rows), ignoring the
// global switch and including disabled rows.
func (s *Service) listAdmin(ctx context.Context, f listFilter, p pagination.Page) ([]UIComponent, int64, error) {
	scopes := []string{ScopeBuiltin, ScopePlatform}
	if f.Scope != "" {
		scopes = []string{f.Scope}
	}
	q := s.db.WithContext(ctx).Model(&UIComponent{}).Where("owner_user_id = ? AND scope IN ?", 0, scopes)
	if f.Enabled != nil {
		q = q.Where("enabled = ?", *f.Enabled)
	}
	return s.page(applyKeyword(q, f.Keyword), managementOrder, p)
}

// create validates and inserts a sandbox component in bucket b on behalf of
// actor.
func (s *Service) create(ctx context.Context, b bucket, actor uint, raw rawInput) (*UIComponent, error) {
	if actor == 0 || (b.scope == ScopeUser && b.owner == 0) {
		return nil, ErrInvalid
	}
	in, err := normalizeInput(raw)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now().UTC()
	row := &UIComponent{
		Scope: b.scope, OwnerUserID: b.owner, Name: in.Name, Version: in.Version,
		Description: in.Description, PropsSummary: in.PropsSummary, PropsSchema: in.PropsSchema,
		RendererKind: RendererSandbox, RendererSource: in.RendererSource,
		Enabled: in.Enabled, SortOrder: in.SortOrder,
		CreatedByUserID: actor, UpdatedByUserID: actor,
	}
	row.CreatedAt, row.UpdatedAt = now, now

	insert := func(tx *gorm.DB) error {
		if row.SortOrder <= 0 {
			var maxOrder int
			if err := tx.Model(&UIComponent{}).
				Where("scope = ? AND owner_user_id = ?", b.scope, b.owner).
				Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
				return err
			}
			row.SortOrder = maxOrder + 1
		}
		return tx.Create(row).Error
	}
	lockName := fmt.Sprintf("ui_components:sort:%s:%d", b.scope, b.owner)
	err = database.WithAdvisoryLock(ctx, s.db, lockName, func(c *gorm.DB) error {
		return c.Transaction(insert)
	})
	if err != nil {
		err = database.Translate(err)
		if database.IsDuplicate(err) {
			return nil, ErrAlreadyExists.WithCause(err)
		}
		return nil, err
	}
	return row, nil
}

// patchTarget says which rows a patch may reach.
type patchTarget struct {
	scopes []string // allowed scopes
	owner  uint     // required owner (0 for admin targets)
}

var (
	userTarget  = func(id uint) patchTarget { return patchTarget{scopes: []string{ScopeUser}, owner: id} }
	adminTarget = patchTarget{scopes: []string{ScopeBuiltin, ScopePlatform}}
)

// update applies a partial change. The row is located first, so a component
// that is missing or out of reach is always not-found whatever the body holds.
// Read, merge and write run in one transaction with the row locked.
func (s *Service) update(ctx context.Context, t patchTarget, actor, id uint, raw rawPatch) (*UIComponent, error) {
	if actor == 0 || id == 0 {
		return nil, ErrInvalid
	}
	var out UIComponent
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur UIComponent
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND owner_user_id = ? AND scope IN ?", id, t.owner, t.scopes).
			Take(&cur).Error
		if err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		if cur.Scope == ScopeBuiltin && raw.touchesContent() {
			return ErrBuiltinProtected
		}
		if cur.RendererKind != RendererSandbox && raw.RendererSource != nil {
			return ErrBuiltinProtected
		}
		p, err := normalizePatch(raw)
		if err != nil {
			return err
		}
		set := map[string]any{
			"updated_by_user_id": actor,
			"updated_at":         s.clk.Now().UTC(),
		}
		if p.Name != nil {
			set["name"] = *p.Name
		}
		if p.Version != nil {
			set["version"] = *p.Version
		}
		if p.Description != nil {
			set["description"] = *p.Description
		}
		if p.PropsSummary != nil {
			set["props_summary"] = *p.PropsSummary
		}
		if p.PropsSchema != nil {
			set["props_schema"] = *p.PropsSchema
		}
		if p.RendererSource != nil {
			set["renderer_source"] = *p.RendererSource
		}
		if p.Enabled != nil {
			set["enabled"] = *p.Enabled
		}
		if p.SortOrder != nil {
			set["sort_order"] = *p.SortOrder
		}
		if err := tx.Model(&UIComponent{}).Where("id = ?", cur.ID).Updates(set).Error; err != nil {
			return err
		}
		return tx.Where("id = ?", cur.ID).Take(&out).Error
	})
	if err != nil {
		err = database.Translate(err)
		if database.IsDuplicate(err) {
			return nil, ErrAlreadyExists.WithCause(err)
		}
		return nil, err
	}
	return &out, nil
}

// removeOwn deletes one of the caller's own components.
func (s *Service) removeOwn(ctx context.Context, userID, id uint) error {
	if userID == 0 || id == 0 {
		return ErrInvalid
	}
	res := s.db.WithContext(ctx).
		Where("id = ? AND scope = ? AND owner_user_id = ?", id, ScopeUser, userID).
		Delete(&UIComponent{})
	if res.Error != nil {
		return database.Translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}

// removePlatform deletes a platform component. Built-in
// rows are protected and user rows are out of reach.
func (s *Service) removePlatform(ctx context.Context, id uint) error {
	if id == 0 {
		return ErrInvalid
	}
	return database.Translate(s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur UIComponent
		if err := tx.Where("id = ? AND owner_user_id = ? AND scope IN ?", id, 0, adminTarget.scopes).Take(&cur).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		if cur.Scope == ScopeBuiltin {
			return ErrBuiltinProtected
		}
		res := tx.Where("id = ?", cur.ID).Delete(&UIComponent{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return nil
	}))
}
