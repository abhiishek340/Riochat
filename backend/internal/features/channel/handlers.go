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

func (h *handlers) deleteRoute() gin.HandlerFunc {
	return h.routeAction(func(c *gin.Context, up, rt uint) (any, error) {
		return nil, h.svc.DeleteRoute(c.Request.Context(), up, rt)
	})
}

func (h *handlers) setRouteStatus(status string) gin.HandlerFunc {
	return h.routeAction(func(c *gin.Context, up, rt uint) (any, error) {
		return nil, h.svc.SetRouteStatus(c.Request.Context(), up, rt, status)
	})
}

func (h *handlers) openBindingCircuit() gin.HandlerFunc {
	return h.routeAction(func(c *gin.Context, up, rt uint) (any, error) {
		return nil, h.svc.OpenBindingCircuit(c.Request.Context(), up, rt)
	})
}

func (h *handlers) resetBindingCircuit() gin.HandlerFunc {
	return h.routeAction(func(c *gin.Context, up, rt uint) (any, error) {
		if err := h.svc.ResetBindingCircuit(c.Request.Context(), up, rt); err != nil {
			return nil, err
		}
		return gin.H{"reset": true}, nil
	})
}

func (h *handlers) probeRoute() gin.HandlerFunc {
	return h.routeAction(func(c *gin.Context, up, rt uint) (any, error) {
		var req probeReq
		if !optionalBody(c, &req) {
			return nil, nil
		}
		return h.svc.ProbeRoute(c.Request.Context(), up, rt, req.TaskType)
	})
}

// ---- remote discovery ---------------------------------------------------------------------------

func (h *handlers) previewRemote(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	out, err := h.svc.PreviewRemote(c.Request.Context(), id)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) syncRemote(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	out, err := h.svc.SyncRemote(c.Request.Context(), id, queryTrue(c, "allow_empty"), strings.TrimSpace(c.Query("expected_snapshot")))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) importRemote(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidUpstreamID)
	if !ok {
		return
	}
	var req importReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	out, err := h.svc.ImportModels(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

// ---- platform models -------------------------------------------------------------------------------

func (h *handlers) listAdminModels(c *gin.Context) {
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListModels(c.Request.Context(), ModelQuery{
		Q: c.Query("q"), Status: c.Query("status"), OnlyActive: queryTrue(c, "only_active"),
		OnlyAvailable: queryTrue(c, "only_available"), Vendor: c.Query("vendor"), Protocol: c.Query("protocol"),
		Upstream: c.Query("upstream"), Sort: c.Query("sort"), Page: p.Page, PageSize: p.PageSize,
	})
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) createModel(c *gin.Context) {
	var req createModelReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.CreateModel(c.Request.Context(), req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"model": v})
}

func (h *handlers) updateModel(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	var req updateModelReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateModel(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"model": v})
}

func (h *handlers) setModelProtocols(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	var req setProtocolsReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.SetModelProtocols(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"model": v})
}

func (h *handlers) deleteModel(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	if err := h.svc.DeleteModel(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) batchDeleteModels(c *gin.Context) {
	var req batchDeleteReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	httpx.OK(c, h.svc.BatchDeleteModels(c.Request.Context(), req.IDs))
}

func (h *handlers) reorderModels(c *gin.Context) {
	var req reorderReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := h.svc.ReorderModels(c.Request.Context(), req.ModelIDs); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) assignDisplayGroup(c *gin.Context) {
	var req assignGroupReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if err := h.svc.AssignDisplayGroup(c.Request.Context(), req.ModelIDs, *req.DisplayGroupID); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) probeModel(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	var req probeReq
	if !optionalBody(c, &req) {
		return
	}
	out, err := h.svc.ProbeModel(c.Request.Context(), id, req.TaskType)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) probeModelAll(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	var req probeReq
	if !optionalBody(c, &req) {
		return
	}
	out, err := h.svc.ProbeModelAll(c.Request.Context(), id, req.TaskType)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) listSources(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListSources(c.Request.Context(), id, p.Page, p.PageSize)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) bindSource(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	var req bindSourceReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.BindSource(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"source": v})
}

func (h *handlers) updateSource(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidModelID)
	if !ok {
		return
	}
	rt, ok := pathID(c, "route_id", ErrInvalidRouteID)
	if !ok {
		return
	}
	var req updateSourceReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateSource(c.Request.Context(), id, rt, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"source": v})
}

// ---- vendors and display groups -------------------------------------------------------------------------

func (h *handlers) listVendors(c *gin.Context) {
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListVendors(c.Request.Context(), c.Query("q"), p.Page, p.PageSize)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) createVendor(c *gin.Context) {
	var req createVendorReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.CreateVendor(c.Request.Context(), req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"vendor": v})
}

func (h *handlers) updateVendor(c *gin.Context) {
	var req updateVendorReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateVendor(c.Request.Context(), c.Param("key"), req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"vendor": v})
}

func (h *handlers) deleteVendor(c *gin.Context) {
	if err := h.svc.DeleteVendor(c.Request.Context(), c.Param("key")); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

func (h *handlers) listGroups(c *gin.Context) {
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListGroups(c.Request.Context(), c.Query("q"), p.Page, p.PageSize)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) createGroup(c *gin.Context) {
	var req createGroupReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.CreateGroup(c.Request.Context(), req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"group": v})
}

func (h *handlers) updateGroup(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidGroupID)
	if !ok {
		return
	}
	var req updateGroupReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateGroup(c.Request.Context(), id, req)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"group": v})
}

func (h *handlers) deleteGroup(c *gin.Context) {
	id, ok := pathID(c, "id", ErrInvalidGroupID)
	if !ok {
		return
	}
	if err := h.svc.DeleteGroup(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

// ---- icon assets ----------------------------------------------------------------------------------------------

func (h *handlers) uploadIcon(c *gin.Context) {
	if c.Request.ContentLength > IconMaxBodyBytes {
		httpx.FailErr(c, ErrIconFileTooLarge)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, IconMaxBodyBytes)
	if err := c.Request.ParseMultipartForm(2 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.FailErr(c, ErrIconFileTooLarge)
			return
		}
		httpx.FailErr(c, ErrIconFileInvalid)
		return
	}
	defer c.Request.MultipartForm.RemoveAll()
	file, _, err := c.Request.FormFile("file")
	if err != nil {
		httpx.FailErr(c, ErrIconFileInvalid)
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, maxIconBytes+1))
	if err != nil {
		httpx.FailErr(c, ErrIconFileInvalid)
		return
	}
	out, err := h.svc.UploadIcon(c.Request.Context(), middleware.MustPrincipal(c).UserID, data)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, out)
}

func (h *handlers) listIcons(c *gin.Context) {
	p := pagination.FromRequest(c.Request)
	list, total, err := h.svc.ListIcons(c.Request.Context(), p.Page, p.PageSize)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, list)
}

func (h *handlers) deleteIcon(c *gin.Context) {
	if err := h.svc.RemoveIcon(c.Request.Context(), c.Param("public_id")); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, nil)
}

// ---- catalog and settings -----------------------------------------------------------------------------------------

func (h *handlers) catalogStatus(c *gin.Context) {
	httpx.OK(c, h.svc.CatalogStatus(c.Request.Context()))
}

func (h *handlers) catalogResolve(c *gin.Context) {
	var req resolveCatalogReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	httpx.OK(c, h.svc.ResolveCatalog(c.Request.Context(), req))
}

func (h *handlers) catalogRefresh(c *gin.Context) {
	st, err := h.svc.RefreshCatalog(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, st)
}

func (h *handlers) listSettings(c *gin.Context) {
	list, err := h.svc.ListSettings(c.Request.Context())
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, list)
}

func (h *handlers) patchSetting(c *gin.Context) {
	var req patchSettingReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.UpdateSetting(c.Request.Context(), c.Param("key"), req.Value)
	if err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, v)
}
