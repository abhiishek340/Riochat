package example

import (
	"context"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

type createRequest struct {
	Title string `json:"title" binding:"required,min=1,max=128"`
}

// Mount registers the area's routes and lifecycle hooks.
func Mount(d deps.Deps) error {
	svc := &service{db: d.DB}

	// Public: no authentication, per-IP rate limit already attached.
	d.Routes.Public.GET("/examples/public", func(c *gin.Context) {
		httpx.OK(c, gin.H{"hello": "world"})
	})

	// Authenticated: the principal is on the request.
	d.Routes.Auth.POST("/examples", func(c *gin.Context) {
		var req createRequest
		if !httpx.BindJSON(c, &req) {
			return
		}
		row, err := svc.create(c.Request.Context(), middleware.MustPrincipal(c).UserID, req.Title)
		if err != nil {
			status := http.StatusInternalServerError
			if err == ErrTooMany {
				status = http.StatusConflict
			}
			httpx.Fail(c, status, err)
			return
		}
		httpx.Created(c, row)
	})
	d.Routes.Auth.GET("/examples", func(c *gin.Context) {
		rows, total, err := svc.list(c.Request.Context(), middleware.MustPrincipal(c).UserID, pagination.FromRequest(c.Request))
		if err != nil {
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		httpx.Paged(c, total, rows)
	})
	d.Routes.Auth.GET("/examples/:id", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			httpx.InvalidID(c)
			return
		}
		row, err := svc.get(c.Request.Context(), middleware.MustPrincipal(c).UserID, uint(id))
		if err != nil {
			status := http.StatusInternalServerError
			if err == ErrNotFound {
				status = http.StatusNotFound
			}
			httpx.Fail(c, status, err)
			return
		}
		httpx.OK(c, row)
	})

	// A group hidden behind a capability flag.
	d.Gated(capability.Sharing).Auth.GET("/examples-shared", func(c *gin.Context) {
		httpx.OK(c, gin.H{"shared": true})
	})

	// Administrators only.
	d.Routes.Admin.DELETE("/examples/:id", func(c *gin.Context) {
		id, err := strconv.ParseUint(c.Param("id"), 10, 64)
		if err != nil {
			httpx.InvalidID(c)
			return
		}
		if err := svc.deleteAny(c.Request.Context(), uint(id)); err != nil {
			status := http.StatusInternalServerError
			if err == ErrNotFound {
				status = http.StatusNotFound
			}
			httpx.Fail(c, status, err)
			return
		}
		httpx.OK(c, gin.H{"deleted": id})
	})

	// Startup hook: runs after every area is mounted, before traffic.
	d.Lifecycle.OnStart("example-count", func(ctx context.Context) error {
		var n int64
		if err := d.DB.WithContext(ctx).Model(&Example{}).Count(&n).Error; err != nil {
			return err
		}
		d.Logger.Info("example_ready", logx.F("rows", n))
		return nil
	})

	// Worker: stops when the worker context is canceled during shutdown.
	d.Lifecycle.Go("example-worker", func(ctx context.Context) { <-ctx.Done() })
	return nil
}
