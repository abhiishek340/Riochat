package personalprovider

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// maxOwnerMatches bounds the id list produced by an owner search.
const maxOwnerMatches = 1000

// userRow is the read-only projection of the identity user table used by the
// default OwnerDirectory. The auth area owns the table.
type userRow struct {
	ID          uint   `gorm:"column:id"`
	PublicID    string `gorm:"column:public_id"`
	Username    string `gorm:"column:username"`
	DisplayName string `gorm:"column:display_name"`
	Email       string `gorm:"column:email"`
	AvatarURL   string `gorm:"column:avatar_url"`
}

func (userRow) TableName() string { return "identity_users" }

// dbOwners reads owner details straight from the identity user table (soft
// deleted rows are skipped).
type dbOwners struct{ db *gorm.DB }

func (o dbOwners) Lookup(ctx context.Context, ids []uint) (map[uint]Owner, error) {
	out := map[uint]Owner{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []userRow
	err := o.db.WithContext(ctx).Table("identity_users").
		Select("id, public_id, username, display_name, email, avatar_url").
		Where("id IN ? AND deleted_at IS NULL", ids).Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	for _, r := range rows {
		out[r.ID] = Owner{PublicID: r.PublicID, Username: r.Username, DisplayName: r.DisplayName, Email: r.Email, AvatarURL: r.AvatarURL}
	}
	return out, nil
}

func (o dbOwners) SearchIDs(ctx context.Context, q string) ([]uint, error) {
	pat := likePattern(q)
	var ids []uint
	err := o.db.WithContext(ctx).Table("identity_users").
		Where("deleted_at IS NULL").
		Where("LOWER(username) LIKE ? ESCAPE '\\' OR LOWER(display_name) LIKE ? ESCAPE '\\' OR LOWER(email) LIKE ? ESCAPE '\\'", pat, pat, pat).
		Limit(maxOwnerMatches).Pluck("id", &ids).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	return ids, nil
}

// likePattern builds a case-insensitive "contains" pattern with LIKE
// wildcards escaped by a backslash.
func likePattern(q string) string {
	q = strings.ToLower(q)
	q = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(q)
	return "%" + q + "%"
}
