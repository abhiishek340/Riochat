package promptreset

import (
	"context"
	"fmt"
	"math"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// owner identifies the slice of the table an operation may touch: the builtin
// set (owner 0) or one user's presets.
type owner struct {
	scope string
	id    uint
}

var builtinOwner = owner{scope: ScopeBuiltin}

func userOwner(id uint) owner { return owner{scope: ScopeUser, id: id} }

type service struct {
	db  *gorm.DB
	clk clock.Clock
}

func newService(db *gorm.DB, clk clock.Clock) *service {
	if clk == nil {
		clk = clock.System{}
	}
	return &service{db: db, clk: clk}
}

// listFilter narrows a listing.
type listFilter struct {
	Keyword string
	Enabled *bool
}

// likePattern turns a keyword into a case-folded substring pattern whose
// wildcard characters are matched literally.
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
	return q.Where(`(LOWER(title) LIKE ? ESCAPE '\' OR LOWER("trigger") LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\' OR LOWER(content) LIKE ? ESCAPE '\')`,
		p, p, p, p)
}

const tieBreak = "sort_order ASC, updated_at DESC, id DESC"

func (s *service) page(ctx context.Context, q *gorm.DB, order string, p pagination.Page) ([]PromptPreset, int64, error) {
	var total int64
	if err := q.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	rows := make([]PromptPreset, 0)
	if err := q.Session(&gorm.Session{}).Order(order).Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	return rows, total, nil
}

// listVisible is the slash-picker list: enabled builtin presets plus the
// caller's own enabled presets, the caller's first.
func (s *service) listVisible(ctx context.Context, userID uint, keyword string, p pagination.Page) ([]PromptPreset, int64, error) {
	if userID == 0 {
		return nil, 0, ErrInvalid
	}
	q := s.db.WithContext(ctx).Model(&PromptPreset{}).
		Where("enabled = ?", true).
		Where("(scope = ? OR (scope = ? AND owner_user_id = ?))", ScopeBuiltin, ScopeUser, userID)
	q = applyKeyword(q, keyword)
	return s.page(ctx, q, "CASE WHEN scope = 'user' THEN 0 ELSE 1 END ASC, "+tieBreak, p)
}

// listOwned lists one owner's presets (the caller's own, or the builtin set).
func (s *service) listOwned(ctx context.Context, o owner, f listFilter, p pagination.Page) ([]PromptPreset, int64, error) {
	if o.scope == ScopeUser && o.id == 0 {
		return nil, 0, ErrInvalid
	}
	q := s.db.WithContext(ctx).Model(&PromptPreset{}).Where("scope = ? AND owner_user_id = ?", o.scope, o.id)
	if f.Enabled != nil {
		q = q.Where("enabled = ?", *f.Enabled)
	}
	q = applyKeyword(q, f.Keyword)
	return s.page(ctx, q, "enabled DESC, "+tieBreak, p)
}

// create validates and inserts a preset owned by o on behalf of actor.
func (s *service) create(ctx context.Context, o owner, actor uint, raw rawInput) (*PromptPreset, error) {
	if actor == 0 || (o.scope == ScopeUser && o.id == 0) {
		return nil, ErrInvalid
	}
	in, err := normalizeInput(raw)
	if err != nil {
		return nil, err
	}
	now := s.clk.Now().UTC()
	row := &PromptPreset{
		Scope: o.scope, OwnerUserID: o.id, Title: in.Title, Trigger: in.Trigger,
		Description: in.Description, Content: in.Content, Enabled: in.Enabled, SortOrder: in.SortOrder,
		CreatedByUserID: actor, UpdatedByUserID: actor,
	}
	row.CreatedAt, row.UpdatedAt = now, now

	insert := func(tx *gorm.DB) error {
		if row.SortOrder <= 0 {
			var maxOrder int
			if err := tx.Model(&PromptPreset{}).
				Where("scope = ? AND owner_user_id = ?", o.scope, o.id).
				Select("COALESCE(MAX(sort_order), 0)").Scan(&maxOrder).Error; err != nil {
				return err
			}
			// The column is a 32-bit integer; stay inside it.
			if maxOrder >= math.MaxInt32 {
				row.SortOrder = math.MaxInt32
			} else {
				row.SortOrder = maxOrder + 1
			}
		}
		if err := tx.Create(row).Error; err != nil {
			return err
		}
		if !in.Enabled {
			// The column default is true, which GORM substitutes for a zero
			// value on insert; write the explicit value.
			if err := tx.Model(&PromptPreset{}).Where("id = ?", row.ID).UpdateColumn("enabled", false).Error; err != nil {
				return err
			}
			row.Enabled = false
		}
		return nil
	}

	lockName := fmt.Sprintf("prompt_presets:sort:%s:%d", o.scope, o.id)
	err = database.WithAdvisoryLock(ctx, s.db, lockName, func(c *gorm.DB) error {
		return c.Transaction(insert)
	})
	if err != nil {
		err = database.Translate(err)
		if database.IsDuplicate(err) {
			return nil, ErrTriggerExists.WithCause(err)
		}
		return nil, err
	}
	return row, nil
}

// update applies a partial change to a preset owned by o. Ownership is decided
// before the supplied values are validated.
func (s *service) update(ctx context.Context, o owner, actor, id uint, raw rawPatch) (*PromptPreset, error) {
	if actor == 0 || id == 0 || (o.scope == ScopeUser && o.id == 0) {
		return nil, ErrInvalid
	}
	var out PromptPreset
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		scoped := func() *gorm.DB {
			return tx.Model(&PromptPreset{}).Where("id = ? AND scope = ? AND owner_user_id = ?", id, o.scope, o.id)
		}
		var current PromptPreset
		if err := scoped().Take(&current).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		p, err := normalizePatch(raw)
		if err != nil {
			return err
		}
		set := map[string]any{
			"updated_by_user_id": actor,
			"updated_at":         s.clk.Now().UTC(),
		}
		if p.Title != nil {
			set["title"] = *p.Title
		}
		if p.Trigger != nil {
			set["trigger"] = *p.Trigger
		}
		if p.Description != nil {
			set["description"] = *p.Description
		}
		if p.Content != nil {
			set["content"] = *p.Content
		}
		if p.Enabled != nil {
			set["enabled"] = *p.Enabled
		}
		if p.SortOrder != nil {
			set["sort_order"] = *p.SortOrder
		}
		res := scoped().Updates(set)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		if err := scoped().Take(&out).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrNotFound
			}
			return err
		}
		return nil
	})
	if err != nil {
		err = database.Translate(err)
		if database.IsDuplicate(err) {
			return nil, ErrTriggerExists.WithCause(err)
		}
		return nil, err
	}
	return &out, nil
}

// remove hard-deletes a preset owned by o.
func (s *service) remove(ctx context.Context, o owner, id uint) error {
	if id == 0 || (o.scope == ScopeUser && o.id == 0) {
		return ErrInvalid
	}
	res := s.db.WithContext(ctx).
		Where("id = ? AND scope = ? AND owner_user_id = ?", id, o.scope, o.id).
		Delete(&PromptPreset{})
	if res.Error != nil {
		return database.Translate(res.Error)
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
