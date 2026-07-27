package mcp

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// ReencryptTokens moves every non-empty stored server token to the key ring's
// active key and returns how many rows changed. Empty tokens are left alone.
// The secret-rotation job calls it.
func ReencryptTokens(ctx context.Context, db *gorm.DB, ring *secretbox.KeyRing) (int, error) {
	changed := 0
	var after uint
	for {
		var batch []Server
		err := db.WithContext(ctx).Where("id > ? AND auth_token_enc <> ''", after).Order("id ASC").Limit(100).Find(&batch).Error
		if err != nil {
			return changed, database.Translate(err)
		}
		if len(batch) == 0 {
			return changed, nil
		}
		for _, srv := range batch {
			after = srv.ID
			next, did, err := ring.Reencrypt(srv.AuthTokenEnc)
			if err != nil {
				return changed, err
			}
			if !did {
				continue
			}
			if err := db.WithContext(ctx).Model(&Server{}).Where("id = ?", srv.ID).
				Update("auth_token_enc", next).Error; err != nil {
				return changed, database.Translate(err)
			}
			changed++
		}
	}
}
