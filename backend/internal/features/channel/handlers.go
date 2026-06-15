package channel

import (
	"bytes"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// handlers adapts the service to HTTP.
type handlers struct{ svc *Service }

// pathID parses an unsigned integer path parameter; a bad value answers 400
// with the code that belongs to the parameter.
func pathID(c *gin.Context, param string, bad *apperr.Error) (uint, bool) {
	n, err := strconv.ParseUint(c.Param(param), 10, 64)
	if err != nil || uint64(uint(n)) != n {
		httpx.Fail(c, http.StatusBadRequest, bad)
		return 0, false
	}
	return uint(n), true
}

func fail(c *gin.Context, err error) { httpx.FailErr(c, err) }

func queryTrue(c *gin.Context, name string) bool {
	return strings.EqualFold(strings.TrimSpace(c.Query(name)), "true")
}

// optionalBody binds a JSON body when one is present; an empty body leaves
// dst untouched.
func optionalBody(c *gin.Context, dst any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, 1<<20))
	if err != nil {
		httpx.Fail(c, http.StatusRequestEntityTooLarge, apperr.ErrInvalidRequest.WithCause(err))
		return false
	}
	if len(bytes.TrimSpace(raw)) == 0 {
		return true
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	return httpx.BindJSON(c, dst)
}

// ---- user side ------------------------------------------------------------------------------

func (h *handlers) listModels(c *gin.Context) {
	list, err := h.svc.UserModels(c.Request.Context(), middleware.MustPrincipal(c).UserID)
	if err != nil {
		fail(c, err)
		return
	}
	if list == nil {
		list = []PublicModel{}
	}
	httpx.OK(c, list)
}

func (h *handlers) readIcon(c *gin.Context) {
	obj, err := h.svc.OpenIcon(c.Request.Context(), c.Param("public_id"))
	if err != nil {
		fail(c, err)
		return
	}
	etag := `"` + obj.SHA256 + `"`
	const cacheControl = "public, max-age=31536000, immutable"
	c.Header("Cache-Control", cacheControl)
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		c.Writer.WriteHeaderNow()
		return
	}
	c.Header("Content-Disposition", "inline")
	c.Header("Content-Length", strconv.Itoa(len(obj.Data)))
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Cross-Origin-Resource-Policy", "cross-origin")
	c.Data(http.StatusOK, obj.ContentType, obj.Data)
}

// ---- upstreams --------------------------------------------------------------------------------

func (h *handlers) listUpstreams(c *gin.Context) {
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListUpstreams(c.Request.Context(), UpstreamQuery{
		Q: c.Query("q"), Status: c.Query("status"), Compatible: c.Query("compatible"), Sort: c.Query("sort"),
		Page: p.Page, PageSize: p.PageSize,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) createUpstream(c *gin.Context) {
	var req createUpstreamReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.CreateUpstream(c.Request.Context(), req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"upstream": v})
}

func (h *handlers) updateUpstream(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	var req updateUpstreamReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateUpstream(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"upstream": v})
}

func (h *handlers) deleteUpstream(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	if err := h.svc.DeleteUpstream(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) batchDeleteUpstreams(c *gin.Context) {
	var req batchDeleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	httpx.OK(c, h.svc.BatchDeleteUpstreams(c.Request.Context(), req.IDs))
}

func (h *handlers) openUpstreamCircuit(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	if err := h.svc.OpenUpstreamCircuit(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) resetUpstreamCircuit(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	if err := h.svc.ResetUpstreamCircuit(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"reset": true})
}

// ---- upstream models ---------------------------------------------------------------------------

func (h *handlers) listUpstreamModels(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	p := pagination.FromRequest(c.Request)
	rows, total, err := h.svc.ListUpstreamModels(c.Request.Context(), id, UpstreamModelQuery{
		Q: c.Query("q"), RouteStatus: c.Query("route_status"), UpstreamStatus: c.Query("upstream_status"),
		Protocol: c.Query("protocol"), Sort: c.Query("sort"), Page: p.Page, PageSize: p.PageSize,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, rows)
}

func (h *handlers) upsertBinding(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	var req upsertBindingReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	row, err := h.svc.UpsertBinding(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"binding": row})
}

func (h *handlers) batchDeleteRoutes(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	var req batchDeleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	httpx.OK(c, h.svc.BatchDeleteRoutes(c.Request.Context(), id, req.IDs))
}

// routeAction wraps the handlers that act on one route of an upstream.
func (h *handlers) routeAction(do func(c *gin.Context, upstreamID, routeID uint) (any, error)) gin.HandlerFunc {
	return func(c *gin.Context) {
		up, ok := pathID(c, "id", ErrInvalidUpstreamID)
		if !ok {
			return
		}
		rt, ok := pathID(c, "route_id", ErrInvalidRouteID)
		if !ok {
			return
		}
		out, err := do(c, up, rt)
		if err != nil {
			fail(c, err)
			return
		}
		if c.IsAborted() {
			return
		}
		httpx.OK(c, out)
	}
}
