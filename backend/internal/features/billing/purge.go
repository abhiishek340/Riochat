package billing

import (
	"context"
	"time"

	"gorm.io/gorm"
)

// PurgeUsageBefore permanently deletes every usage ledger row (the call log)
// created strictly before the cutoff and returns how many rows were removed.
// The log-cleanup endpoint calls it for the "usage" type.
func PurgeUsageBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Unscoped().Where("created_at < ?", before.UTC()).Delete(&UsageLedger{})
	return res.RowsAffected, res.Error
}

// PurgeOrdersBefore permanently deletes every payment order created strictly
// before the cutoff and returns how many rows were removed. The log-cleanup
// endpoint calls it for the "orders" type.
func PurgeOrdersBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Unscoped().Where("created_at < ?", before.UTC()).Delete(&PaymentOrder{})
	return res.RowsAffected, res.Error
}
