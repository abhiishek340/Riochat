package audit

import (
	"context"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// CleanupTypeAudit is the log-cleanup type that purges audit records.
const CleanupTypeAudit = "audit"

// CleanupAction, CleanupResource describe the entry written after a purge.
const (
	CleanupAction   = "admin_cleanup_logs"
	CleanupResource = "logs"
)

var cleanupTypes = map[string]bool{
	"audit": true, "auth": true, "usage": true, "orders": true,
	"conversation": true, "moderation": true, "errors": true,
}

// ValidateCleanup checks the raw type and before values of a log-cleanup
// request and returns the normalized (trimmed, lower-case) type and the cutoff.
// The checks run in the order: before format, type, zero time, future time.
// It knows every cleanup type of the shared endpoint so the area that mounts
// that endpoint can use it for all of them.
func ValidateCleanup(rawType, rawBefore string, now time.Time) (string, time.Time, error) {
	before, err := time.Parse(time.RFC3339, strings.TrimSpace(rawBefore))
	if err != nil {
		return "", time.Time{}, ErrInvalidCleanupBefore
	}
	typ := strings.ToLower(strings.TrimSpace(rawType))
	if !cleanupTypes[typ] {
		return "", time.Time{}, ErrInvalidCleanupType
	}
	if before.IsZero() {
		return "", time.Time{}, ErrInvalidCleanupBefore
	}
	if before.After(now) {
		return "", time.Time{}, ErrCleanupBeforeFuture
	}
	return typ, before, nil
}

// PurgeBefore physically deletes (bypassing soft delete) every audit record
// created strictly before the cutoff and returns the number removed.
func PurgeBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Unscoped().Where("created_at < ?", before.UTC()).Delete(&Log{})
	return res.RowsAffected, res.Error
}

// RecordCleanup writes the entry describing a successful purge. Call it after
// the deletion; it is a no-op on a nil recorder.
func (r *Recorder) RecordCleanup(c *gin.Context, cleanupType string, before time.Time, deleted int64) {
	r.RecordRequest(c, CleanupAction, CleanupResource, cleanupType, map[string]any{
		"type":          cleanupType,
		"before":        before.Format(time.RFC3339),
		"deleted_count": deleted,
	})
}
