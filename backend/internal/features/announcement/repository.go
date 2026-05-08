package announcement

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// repository is the only code that touches the area's tables. Every method
// takes the *gorm.DB to use so callers can run several of them in one
// transaction.
type repository struct{}

// adminFilter narrows the administrator listing. Zero values mean "no filter".
type adminFilter struct {
	Status  string
	Type    string
	Pinned  *bool
	Keyword string
}

// lockForUpdate takes the write lock on one live announcement and reports
// whether it exists. The no-op write works the same on SQLite and PostgreSQL
// and serializes concurrent editors of the same row.
func (repository) lockForUpdate(ctx context.Context, tx *gorm.DB, id uint) (bool, error) {
	res := tx.WithContext(ctx).Model(&Announcement{}).Where("id = ?", id).UpdateColumn("id", gorm.Expr("id"))
	if res.Error != nil {
		return false, database.Translate(res.Error)
	}
	return res.RowsAffected > 0, nil
}

// find loads one live (not soft-deleted) announcement.
func (repository) find(ctx context.Context, tx *gorm.DB, id uint) (*Announcement, error) {
	var a Announcement
	if err := tx.WithContext(ctx).Where("id = ?", id).First(&a).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return &a, nil
}

// create inserts a new announcement.
func (repository) create(ctx context.Context, tx *gorm.DB, a *Announcement) error {
	return database.Translate(tx.WithContext(ctx).Create(a).Error)
}

// update writes the given columns of one announcement.
func (repository) update(ctx context.Context, tx *gorm.DB, id uint, cols map[string]any) error {
	return database.Translate(tx.WithContext(ctx).Model(&Announcement{}).Where("id = ?", id).Updates(cols).Error)
}

// softDelete removes one live announcement and reports whether a row matched.
func (repository) softDelete(ctx context.Context, tx *gorm.DB, id uint) (bool, error) {
	res := tx.WithContext(ctx).Delete(&Announcement{}, id)
	if res.Error != nil {
		return false, database.Translate(res.Error)
	}
	return res.RowsAffected > 0, nil
}

// listActive returns every live announcement whose status is active. The
// display window is applied by the caller in Go so that the comparison does not
// depend on how a driver stores timestamps.
func (repository) listActive(ctx context.Context, tx *gorm.DB) ([]Announcement, error) {
	var rows []Announcement
	err := tx.WithContext(ctx).Where("status = ?", StatusActive).Find(&rows).Error
	return rows, err
}

// statesFor returns the user's live state rows for the given announcements.
func (repository) statesFor(ctx context.Context, tx *gorm.DB, userID uint, ids []uint) ([]UserState, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var rows []UserState
	err := tx.WithContext(ctx).Where("user_id = ? AND announcement_id IN ?", userID, ids).Find(&rows).Error
	return rows, err
}

// statesIncludingDeleted returns every state row, soft-deleted ones too, of one
// user for one announcement (all versions).
func (repository) statesIncludingDeleted(ctx context.Context, tx *gorm.DB, userID, announcementID uint) ([]UserState, error) {
	var rows []UserState
	err := tx.WithContext(ctx).Unscoped().
		Where("user_id = ? AND announcement_id = ?", userID, announcementID).Find(&rows).Error
	return rows, err
}

func (repository) createState(ctx context.Context, tx *gorm.DB, st *UserState) error {
	return database.Translate(tx.WithContext(ctx).Create(st).Error)
}

// reviveState applies cols to an existing state row and clears its soft-delete
// marker.
func (repository) reviveState(ctx context.Context, tx *gorm.DB, id uint, cols map[string]any) error {
	cols["deleted_at"] = nil
	return database.Translate(tx.WithContext(ctx).Unscoped().Model(&UserState{}).Where("id = ?", id).Updates(cols).Error)
}

// adminList returns one page of announcements of any status or window plus the
// count of all matches.
func (repository) adminList(ctx context.Context, tx *gorm.DB, f adminFilter, p pagination.Page) ([]Announcement, int64, error) {
	q := tx.WithContext(ctx).Model(&Announcement{})
	if f.Status != "" {
		q = q.Where("status = ?", f.Status)
	}
	if f.Type != "" {
		q = q.Where("type = ?", f.Type)
	}
	if f.Pinned != nil {
		q = q.Where("pinned = ?", *f.Pinned)
	}
	if kw := strings.ToLower(strings.TrimSpace(f.Keyword)); kw != "" {
		pat := "%" + likeEscaper.Replace(kw) + "%"
		q = q.Where(`(LOWER(title) LIKE ? ESCAPE '\' OR LOWER(content_markdown) LIKE ? ESCAPE '\')`, pat, pat)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Announcement
	err := q.Order("pinned DESC").Order("priority DESC").Order("updated_at DESC").Order("id DESC").
		Scopes(pagination.Scope(p)).Find(&rows).Error
	return rows, total, err
}

var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

// timePtr returns a UTC copy of t truncated to the storage precision, or nil.
func timePtr(t *time.Time) *time.Time {
	if t == nil {
		return nil
	}
	v := t.UTC().Truncate(versionPrecision)
	return &v
}
