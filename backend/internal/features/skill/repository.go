package skill

import (
	"context"
	"errors"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// summaryColumns lists the columns read for discovery; markdown is left out so
// bodies never leave the database for listings.
var summaryColumns = []string{"id", "created_at", "updated_at", "scope", "owner_user_id", "title", "trigger", "description", "enabled", "sort_order"}

type repository struct{ db *gorm.DB }

// owned restricts a query to one scope and owner.
func owned(scope string, owner uint) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		return q.Where("scope = ? AND owner_user_id = ?", scope, owner)
	}
}

// visibleTo restricts a query to the skills usable by user: enabled and either
// built-in or owned by the user.
func visibleTo(user uint) func(*gorm.DB) *gorm.DB {
	return func(q *gorm.DB) *gorm.DB {
		return q.Where("enabled = ?", true).
			Where("(scope = ? OR (scope = ? AND owner_user_id = ?))", ScopeBuiltin, ScopeUser, user)
	}
}

// search matches a trimmed keyword against the given columns, case-insensitive,
// as a single parenthesised disjunction so it can never widen other filters.
func search(keyword string, columns ...string) func(*gorm.DB) *gorm.DB {
	keyword = strings.TrimSpace(keyword)
	return func(q *gorm.DB) *gorm.DB {
		if keyword == "" {
			return q
		}
		pattern := "%" + strings.ToLower(keyword) + "%"
		parts := make([]string, len(columns))
		args := make([]any, len(columns))
		for i, col := range columns {
			parts[i] = "LOWER(" + col + ") LIKE ?"
			args[i] = pattern
		}
		return q.Where("("+strings.Join(parts, " OR ")+")", args...)
	}
}

const (
	orderDiscovery = `CASE WHEN scope = 'user' THEN 0 ELSE 1 END ASC, sort_order ASC, updated_at DESC, id DESC`
	orderManage    = `CASE WHEN enabled THEN 0 ELSE 1 END ASC, sort_order ASC, updated_at DESC, id DESC`
)

func (r *repository) discover(ctx context.Context, user uint, ids []uint, keyword string, p pagination.Page) ([]Skill, int64, error) {
	q := r.db.WithContext(ctx).Model(&Skill{}).
		Scopes(visibleTo(user), search(keyword, "title", `"trigger"`, "description"))
	if len(ids) > 0 {
		q = q.Where("id IN ?", ids)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	var rows []Skill
	err := q.Select(summaryColumns).Order(orderDiscovery).Scopes(pagination.Scope(p)).Find(&rows).Error
	return rows, total, database.Translate(err)
}

func (r *repository) manage(ctx context.Context, scope string, owner uint, keyword string, enabled *bool, p pagination.Page) ([]Skill, int64, error) {
	q := r.db.WithContext(ctx).Model(&Skill{}).
		Scopes(owned(scope, owner), search(keyword, "title", `"trigger"`, "description", "markdown"))
	if enabled != nil {
		q = q.Where("enabled = ?", *enabled)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	var rows []Skill
	err := q.Order(orderManage).Scopes(pagination.Scope(p)).Find(&rows).Error
	return rows, total, database.Translate(err)
}

func (r *repository) usable(ctx context.Context, user uint, ids []uint) ([]Skill, error) {
	var rows []Skill
	err := r.db.WithContext(ctx).Scopes(visibleTo(user)).Where("id IN ?", ids).Find(&rows).Error
	return rows, database.Translate(err)
}

func (r *repository) countUsable(ctx context.Context, user uint, ids []uint) (int64, error) {
	var n int64
	err := r.db.WithContext(ctx).Model(&Skill{}).Scopes(visibleTo(user)).Where("id IN ?", ids).Count(&n).Error
	return n, database.Translate(err)
}

// insert stores a new skill; a non-positive sort order becomes max+1 within the
// same scope and owner, computed in the insert's transaction.
func (r *repository) insert(ctx context.Context, row *Skill) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if row.SortOrder <= 0 {
			var max int
			if err := tx.Model(&Skill{}).Scopes(owned(row.Scope, row.OwnerUserID)).
				Select("COALESCE(MAX(sort_order), 0)").Scan(&max).Error; err != nil {
				return err
			}
			row.SortOrder = max + 1
		}
		return tx.Create(row).Error
	})
	if err = database.Translate(err); database.IsDuplicate(err) {
		return ErrTriggerExists.WithCause(err)
	}
	return err
}

// patch applies changes to one owned skill and returns the stored row. The
// UPDATE comes first so the row (or the database writer) is locked before the
// follow-up read, which serializes concurrent patches of the same skill.
func (r *repository) patch(ctx context.Context, scope string, owner, id uint, changes map[string]any) (Skill, error) {
	var out Skill
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&Skill{}).Scopes(owned(scope, owner)).Where("id = ?", id).Updates(changes)
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Scopes(owned(scope, owner)).Where("id = ?", id).Take(&out).Error
	})
	if err == nil {
		return out, nil
	}
	if errors.Is(err, ErrNotFound) {
		return Skill{}, err
	}
	err = database.Translate(err)
	if database.IsDuplicate(err) {
		return Skill{}, ErrTriggerExists.WithCause(err)
	}
	if database.IsNotFound(err) {
		return Skill{}, ErrNotFound
	}
	return Skill{}, err
}

// remove deletes one owned skill together with its project links.
func (r *repository) remove(ctx context.Context, scope string, owner, id uint) error {
	err := r.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Scopes(owned(scope, owner)).Where("id = ?", id).Delete(&Skill{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return tx.Where("skill_id = ?", id).Delete(&ProjectLink{}).Error
	})
	if errors.Is(err, ErrNotFound) {
		return err
	}
	return database.Translate(err)
}
