package errorlog

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Mount wires the recorder into the request pipeline and registers the
// administrator query endpoint. User names are read from the identity users
// table; use MountWith to supply a different directory.
func Mount(d deps.Deps) error { return MountWith(nil)(d) }

// MountWith returns a Mount function that resolves user names through users.
// A nil directory selects the default database-backed one.
func MountWith(users UserDirectory) func(deps.Deps) error {
	return func(d deps.Deps) error {
		if users == nil {
			users = dbUsers{db: d.DB}
		}
		svc := &service{db: d.DB, users: users}

		// Persistence is off in desktop sidecar mode: nothing is recorded.
		if !d.Config.Load().Local.Enabled {
			rec := newRecorder(d.DB, d.Logger, d.Clock,
				func(ctx context.Context, timeout time.Duration, fn func(context.Context)) {
					d.Lifecycle.Detach(ctx, "errorlog-write", timeout, fn)
				})
			d.ErrorRecorder.Set(rec)
		}

		d.Routes.Admin.GET("/admin/error-logs", listHandler(svc))
		return nil
	}
}

// listHandler serves the admin list. A nil service answers an empty page.
func listHandler(svc *service) gin.HandlerFunc {
	return func(c *gin.Context) {
		if svc == nil {
			httpx.Paged[entryView](c, 0, nil)
			return
		}
		f, bad := parseFilter(c)
		if bad != "" {
			httpx.InvalidQuery(c, bad)
			return
		}
		rows, total, err := svc.list(c.Request.Context(), f, pagination.FromRequest(c.Request))
		if err != nil {
			httpx.Fail(c, http.StatusInternalServerError, err)
			return
		}
		httpx.Paged(c, total, rows)
	}
}

// parseFilter reads the query string; on a bad parameter it returns the
// parameter's name.
func parseFilter(c *gin.Context) (filter, string) {
	q := c.Request.URL.Query()
	f := filter{
		RequestID:   strings.TrimSpace(q.Get("request_id")),
		Keyword:     strings.TrimSpace(q.Get("query")),
		ErrorCode:   strings.TrimSpace(q.Get("error_code")),
		StatusClass: strings.TrimSpace(q.Get("status_class")),
		Ascending:   strings.TrimSpace(q.Get("sort")) == "created_asc",
	}
	if raw := strings.TrimSpace(q.Get("user_id")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 32)
		if err != nil || n == 0 {
			return f, "user_id"
		}
		f.UserID = uint(n)
	}
	for _, p := range []struct {
		name string
		dst  **time.Time
	}{{"created_from", &f.From}, {"created_to", &f.To}} {
		raw := strings.TrimSpace(q.Get(p.name))
		if raw == "" {
			continue
		}
		t, err := time.Parse(time.RFC3339, raw)
		if err != nil {
			return f, p.name
		}
		*p.dst = &t
	}
	return f, ""
}
