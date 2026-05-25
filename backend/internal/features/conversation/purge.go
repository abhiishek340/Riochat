package conversation

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// PurgeEventsBefore permanently deletes every run event (trace blocks, trace
// events and tool-call details) created strictly before the cutoff and returns
// how many rows were removed. The log-cleanup endpoint calls it for the
// "conversation" type.
func PurgeEventsBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Unscoped().Where("created_at < ?", before.UTC()).Delete(&RunEvent{})
	return res.RowsAffected, res.Error
}
