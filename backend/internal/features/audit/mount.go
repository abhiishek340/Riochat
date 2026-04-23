package audit

import (
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Mount registers the admin listing. It is deliberately not behind a
// capability gate, so it stays available in local (single-user) mode.
func Mount(d deps.Deps) error {
	svc := &service{db: d.DB}
	d.Routes.Admin.GET("/admin/audit-logs", svc.handleList)
	return nil
}

func (s *service) handleList(c *gin.Context) {
	q := c.Request.URL.Query()
	f := Filter{
		Query:    strings.TrimSpace(q.Get("query")),
		Resource: strings.TrimSpace(q.Get("resource")),
		Action:   strings.TrimSpace(q.Get("action")),
		Sort:     strings.TrimSpace(q.Get("sort")),
	}
	if raw := strings.TrimSpace(q.Get("actor_user_id")); raw != "" {
		id, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || id <= 0 {
			httpx.InvalidQuery(c, "actor_user_id")
			return
		}
		f.ActorUserID = uint(id)
	}
	var ok bool
	if f.CreatedFrom, ok = timeParam(c, "created_from"); !ok {
		return
	}
	if f.CreatedTo, ok = timeParam(c, "created_to"); !ok {
		return
	}

	rows, total, err := s.list(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		httpx.Fail(c, http.StatusInternalServerError, err)
		return
	}
	httpx.Paged(c, total, views(c.Request.Context(), rows))
}

// timeParam reads an optional RFC 3339 query value. ok is false once the
// failure response has been written.
func timeParam(c *gin.Context, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Request.URL.Query().Get(name))
	if raw == "" {
		return nil, true
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		httpx.InvalidQuery(c, name)
		return nil, false
	}
	return &t, true
}
