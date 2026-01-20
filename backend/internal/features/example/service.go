package example

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Domain errors are declared with apperr.Define so their codes join the HTTP
// boundary allowlist. The namespace is the area name.
var (
	ErrNotFound = apperr.Define("example.not_found", "example not found")
	ErrTooMany  = apperr.Define("example.too_many", "too many examples")
)

const maxPerUser = 50

type service struct{ db *gorm.DB }

func (s *service) create(ctx context.Context, owner uint, title string) (*Example, error) {
	var n int64
	if err := s.db.WithContext(ctx).Model(&Example{}).Where("owner_id = ?", owner).Count(&n).Error; err != nil {
		return nil, err
	}
	if n >= maxPerUser {
		return nil, ErrTooMany
	}
	row := &Example{OwnerID: owner, Title: title}
	if err := s.db.WithContext(ctx).Create(row).Error; err != nil {
		return nil, database.Translate(err)
	}
	return row, nil
}

func (s *service) list(ctx context.Context, owner uint, p pagination.Page) ([]Example, int64, error) {
	q := s.db.WithContext(ctx).Model(&Example{}).Where("owner_id = ?", owner)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Example
	if err := q.Order("id DESC").Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// get returns the row only if it belongs to owner; a foreign row looks exactly
// like a missing one.
func (s *service) get(ctx context.Context, owner, id uint) (*Example, error) {
	var row Example
	err := s.db.WithContext(ctx).Where("id = ? AND owner_id = ?", id, owner).First(&row).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &row, nil
}

func (s *service) deleteAny(ctx context.Context, id uint) error {
	res := s.db.WithContext(ctx).Delete(&Example{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return ErrNotFound
	}
	return nil
}
