package contentmoderation

import (
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	routePrefix   = "/admin/content-moderation"
	maxQueryChars = 200
	probePerMin   = 20
)

// Mount registers the routes and background work with default collaborators.
// Without the optional ports the feature records events and statistics, but
// cannot reach conversations, files or object storage; see MountWith.
func Mount(d deps.Deps) error {
	_, err := MountWith(d, Ports{})
	return err
}

// MountWith is Mount with explicit collaborators. The returned service is also
// available through Shared.
func MountWith(d deps.Deps, ports Ports) (*Service, error) {
	s := newService(d, ports)
	s.registerRoutes(d)
	s.startBackground(d)
	shared.Store(s)
	return s, nil
}

func actorOf(c *gin.Context) Actor {
	p := middleware.MustPrincipal(c)
	return Actor{UserID: p.UserID, Role: p.Role}
}

func auditInfoOf(c *gin.Context) AuditInfo {
	a := middleware.AuditFromRequest(c.Request)
	return AuditInfo{
		RequestID: reqctx.FromRequest(c.Request).RequestID(),
		ClientIP:  a.ClientIP, UserAgent: a.UserAgent,
	}
}

func (s *Service) registerRoutes(d deps.Deps) {
	g := d.Gated(capability.ContentModeration).Admin.Group(routePrefix)

	g.GET("/config", func(c *gin.Context) {
		res, err := s.GetConfig(c.Request.Context(), actorOf(c))
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, res)
	})

	g.PUT("/config", func(c *gin.Context) {
		actor := actorOf(c)
		if err := actor.requireSuper(); err != nil {
			httpx.FailErr(c, err)
			return
		}
		var req UpdateRequest
		if !httpx.BindJSON(c, &req) {
			return
		}
		view, err := s.UpdateConfig(c.Request.Context(), actor, req)
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, gin.H{"config": view})
	})

	g.POST("/probe", d.ProbeLimit(probePerMin), func(c *gin.Context) {
		res, err := s.Probe(c.Request.Context(), actorOf(c))
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, res)
	})

	g.GET("/stats", func(c *gin.Context) {
		actor := actorOf(c)
		if !actor.isAdmin() {
			httpx.FailErr(c, ErrAdminRequired)
			return
		}
		from, ok := parseRFC3339(c, "from")
		if !ok {
			return
		}
		to, ok := parseRFC3339(c, "to")
		if !ok {
			return
		}
		items, err := s.Stats(c.Request.Context(), actor, from, to)
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, gin.H{"items": items})
	})

	g.GET("/events", func(c *gin.Context) {
		actor := actorOf(c)
		if err := actor.requireSuper(); err != nil {
			httpx.FailErr(c, err)
			return
		}
		filter, ok := parseEventFilter(c)
		if !ok {
			return
		}
		p := pagination.FromRequest(c.Request)
		page, err := s.ListEvents(c.Request.Context(), actor, filter, p.Page, p.PageSize)
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, page)
	})

	g.GET("/events/:eventID", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		detail, err := s.GetEvent(c.Request.Context(), actorOf(c), auditInfoOf(c), c.Param("eventID"))
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		httpx.OK(c, detail)
	})

	g.GET("/events/:eventID/images/:index", func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		actor := actorOf(c)
		if err := actor.requireSuper(); err != nil {
			httpx.FailErr(c, err)
			return
		}
		n, err := strconv.ParseUint(strings.TrimSpace(c.Param("index")), 10, 64)
		if err != nil {
			httpx.FailErr(c, ErrInvalidImageIndex)
			return
		}
		// A well-formed index too large to exist simply finds no image.
		if n > math.MaxInt32 {
			n = math.MaxInt32
		}
		data, mime, err := s.EventImage(c.Request.Context(), actor, auditInfoOf(c), c.Param("eventID"), int(n))
		if err != nil {
			httpx.FailErr(c, err)
			return
		}
		c.Data(http.StatusOK, mime, data)
	})
}

// parseRFC3339 reads an optional timestamp query parameter; on a bad value it
// answers 400 itself.
func parseRFC3339(c *gin.Context, name string) (*time.Time, bool) {
	raw := strings.TrimSpace(c.Query(name))
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

func parseEventFilter(c *gin.Context) (EventFilter, bool) {
	var f EventFilter
	bad := func() (EventFilter, bool) {
		httpx.FailErr(c, ErrInvalidQuery)
		return EventFilter{}, false
	}
	if raw := strings.TrimSpace(c.Query("user_id")); raw != "" {
		n, err := strconv.ParseUint(raw, 10, 64)
		if err != nil || n == 0 {
			httpx.FailErr(c, ErrInvalidUserID)
			return EventFilter{}, false
		}
		f.UserID = uint(n)
	}
	f.Query = strings.TrimSpace(c.Query("query"))
	if utf8.RuneCountInString(f.Query) > maxQueryChars {
		return bad()
	}
	f.Direction = strings.TrimSpace(c.Query("direction"))
	if f.Direction != "" && f.Direction != DirectionInput && f.Direction != DirectionOutput {
		return bad()
	}
	f.Modality = strings.TrimSpace(c.Query("modality"))
	if f.Modality != "" && f.Modality != ModalityText && f.Modality != ModalityImage {
		return bad()
	}
	f.Result = strings.TrimSpace(c.Query("result"))
	if f.Result != "" && f.Result != ResultHit && f.Result != ResultFailedOpen && f.Result != ResultPassed {
		return bad()
	}
	f.Category = strings.TrimSpace(c.Query("category"))
	if f.Category != "" && !IsKnownCategory(f.Category) {
		return bad()
	}
	f.RunID = strings.TrimSpace(c.Query("run_id"))
	var ok bool
	if f.From, ok = parseRFC3339(c, "from"); !ok {
		return EventFilter{}, false
	}
	if f.To, ok = parseRFC3339(c, "to"); !ok {
		return EventFilter{}, false
	}
	if f.From != nil && f.To != nil && f.To.Before(*f.From) {
		return bad()
	}
	return f, true
}
