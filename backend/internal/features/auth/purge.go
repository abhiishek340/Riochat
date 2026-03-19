package auth

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// PurgeEventsBefore permanently deletes every auth event created strictly
// before the cutoff and returns how many rows were removed. The log-cleanup
// endpoint calls it for the "auth" type.
func PurgeEventsBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Unscoped().Where("created_at < ?", before.UTC()).Delete(&AuthEvent{})
	return res.RowsAffected, res.Error
}
