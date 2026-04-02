package integration

import (
	"context"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/errorlog"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
)

// mountLogCleanup registers POST /admin/logs/cleanup, the one endpoint that
// physically removes old records of every log type. The audit area validates
// the request and records the purge; each owning area supplies the deletion
// for its own table.
func (h *Hub) mountLogCleanup(d deps.Deps) {
	rec := h.recorder(d)
	d.Routes.Admin.POST("/admin/logs/cleanup", func(c *gin.Context) {
		var req struct {
			Type   string `json:"type" binding:"required"`
			Before string `json:"before" binding:"required"`
		}
		if !httpx.BindJSON(c, &req) {
			return
		}
		typ, before, err := audit.ValidateCleanup(req.Type, req.Before, d.Clock.Now())
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		purge := h.cleanupFor(d, typ)
		n, err := purge(c.Request.Context(), before)
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		rec.RecordCleanup(c, typ, before, n)
		httpx.OK(c, gin.H{"type": typ, "before": req.Before, "deletedCount": n})
	})
}

type purgeFunc func(ctx context.Context, before time.Time) (int64, error)

// cleanupFor returns the deletion of one cleanup type. The type has already
// been validated against audit's list of all seven.
func (h *Hub) cleanupFor(d deps.Deps, typ string) purgeFunc {
	db := d.DB
	switch typ {
	case "auth":
		return func(ctx context.Context, b time.Time) (int64, error) { return auth.PurgeEventsBefore(ctx, db, b) }
	case "usage":
		return func(ctx context.Context, b time.Time) (int64, error) { return billing.PurgeUsageBefore(ctx, db, b) }
	case "orders":
		return func(ctx context.Context, b time.Time) (int64, error) { return billing.PurgeOrdersBefore(ctx, db, b) }
	case "conversation":
		return func(ctx context.Context, b time.Time) (int64, error) {
			return conversation.PurgeEventsBefore(ctx, db, b)
		}
	case "errors":
		return func(ctx context.Context, b time.Time) (int64, error) { return errorlog.PurgeBefore(ctx, db, b) }
	case "moderation":
		return func(ctx context.Context, b time.Time) (int64, error) {
			if h.mod == nil {
				return 0, nil
			}
			return h.mod.DeleteEventsBefore(ctx, b)
		}
	default: // audit
		return func(ctx context.Context, b time.Time) (int64, error) { return audit.PurgeBefore(ctx, db, b) }
	}
}
