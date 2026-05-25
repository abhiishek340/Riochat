package uicomponent

import (
	"bytes"
	"errors"
	"io"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Audit vocabulary for administrator changes.
const (
	auditResource     = "ui_components"
	actionCreatePlatf = "ui_component.create_platform"
	actionUpdate      = "ui_component.update"
	actionDeletePlatf = "ui_component.delete_platform"
)

type handlers struct {
	svc   *Service
	audit *audit.Recorder
}

// createRequest is the body of the two create endpoints. Size limits are
// enforced again, on the trimmed values and in bytes, by the service.
type createRequest struct {
	Name           string `json:"name" binding:"required,max=64"`
	Version        int    `json:"version" binding:"omitempty,min=1,max=2147483647"`
	Description    string `json:"description" binding:"required,max=256"`
	PropsSummary   string `json:"propsSummary" binding:"required,max=1024"`
	PropsSchema    string `json:"propsSchema" binding:"max=16384"`
	RendererSource string `json:"rendererSource" binding:"required,max=262144"`
	Enabled        bool   `json:"enabled"`
	SortOrder      int    `json:"sortOrder" binding:"min=-2147483648,max=2147483647"`
}

func (r createRequest) raw() rawInput {
	return rawInput{
		Name: r.Name, Version: r.Version, Description: r.Description,
		PropsSummary: r.PropsSummary, PropsSchema: r.PropsSchema,
		RendererSource: r.RendererSource, Enabled: r.Enabled, SortOrder: r.SortOrder,
	}
}

// patchRequest distinguishes absent fields (nil) from zero values.
type patchRequest struct {
	Name           *string `json:"name" binding:"omitempty,max=64"`
	Version        *int    `json:"version" binding:"omitempty,max=2147483647"`
	Description    *string `json:"description" binding:"omitempty,max=256"`
	PropsSummary   *string `json:"propsSummary" binding:"omitempty,max=1024"`
	PropsSchema    *string `json:"propsSchema" binding:"omitempty,max=16384"`
	RendererSource *string `json:"rendererSource" binding:"omitempty,max=262144"`
	Enabled        *bool   `json:"enabled"`
	SortOrder      *int    `json:"sortOrder" binding:"omitempty,min=-2147483648,max=2147483647"`
}

func (r patchRequest) raw() rawPatch {
	return rawPatch{
		Name: r.Name, Version: r.Version, Description: r.Description,
		PropsSummary: r.PropsSummary, PropsSchema: r.PropsSchema,
		RendererSource: r.RendererSource, Enabled: r.Enabled, SortOrder: r.SortOrder,
	}
}

// fail maps a service error to a response. Anything not recognised is a 500
// whose cause is only logged.
func fail(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalid), errors.Is(err, ErrBuiltinProtected), errors.Is(err, ErrInvalidID):
		status = http.StatusBadRequest
	case errors.Is(err, ErrAlreadyExists):
		status = http.StatusConflict
	}
	if status == http.StatusInternalServerError {
		if _, typed := apperr.Find(err); !typed {
			err = apperr.ErrInternal.WithCause(err)
		}
	}
	httpx.Fail(c, status, err)
}

// bindPatch reads a patch body. A zero-length body is the same as `{}`: every
// field absent, which only records the acting user.
func bindPatch(c *gin.Context, dst *patchRequest) bool {
	if c.Request.Body != nil {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
		if err == nil && len(bytes.TrimSpace(raw)) == 0 {
			raw = []byte("{}")
		}
		if err == nil {
			c.Request.Body = io.NopCloser(bytes.NewReader(raw))
		} else {
			c.Request.Body = io.NopCloser(&failingReader{err: err})
		}
	}
	return httpx.BindJSON(c, dst)
}

// failingReader replays a read error so BindJSON reports it the usual way.
type failingReader struct{ err error }

func (f *failingReader) Read([]byte) (int, error) { return 0, f.err }

func caller(c *gin.Context) uint { return middleware.MustPrincipal(c).UserID }

// pathID parses a positive identifier; on failure the response is written.
func pathID(c *gin.Context) (uint, bool) {
	n, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil || n == 0 || n > math.MaxInt64 {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidID)
		return 0, false
	}
	return uint(n), true
}

// lenientBool reads an optional boolean filter; empty or unparsable means none.
func lenientBool(raw string) *bool {
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &v
}

func one(c *gin.Context, row *UIComponent) { httpx.OK(c, gin.H{"component": toView(row)}) }

func (h *handlers) record(c *gin.Context, action string, id uint, detail map[string]any) {
	var d any
	if detail != nil {
		d = detail
	}
	h.audit.RecordRequest(c, action, auditResource, strconv.FormatUint(uint64(id), 10), d)
}

// --- user routes ---

func (h *handlers) listVisible(c *gin.Context) {
	rows, total, err := h.svc.listVisible(c.Request.Context(), caller(c), strings.TrimSpace(c.Query("q")), pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, toViews(rows))
}

func (h *handlers) listMine(c *gin.Context) {
	f := listFilter{Keyword: strings.TrimSpace(c.Query("q")), Enabled: lenientBool(c.Query("enabled"))}
	rows, total, err := h.svc.listMine(c.Request.Context(), caller(c), f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, toViews(rows))
}

func (h *handlers) createMine(c *gin.Context) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	uid := caller(c)
	row, err := h.svc.create(c.Request.Context(), userBucket(uid), uid, req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	one(c, row)
}

func (h *handlers) updateMine(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req patchRequest
	if !bindPatch(c, &req) {
		return
	}
	uid := caller(c)
	row, err := h.svc.update(c.Request.Context(), userTarget(uid), uid, id, req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	one(c, row)
}

func (h *handlers) deleteMine(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.removeOwn(c.Request.Context(), caller(c), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

// --- administrator routes ---

func (h *handlers) adminList(c *gin.Context) {
	scope := c.Query("scope")
	if scope != "" && scope != ScopeBuiltin && scope != ScopePlatform {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalid)
		return
	}
	f := listFilter{Keyword: strings.TrimSpace(c.Query("q")), Enabled: lenientBool(c.Query("enabled")), Scope: scope}
	rows, total, err := h.svc.listAdmin(c.Request.Context(), f, pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, toViews(rows))
}

func (h *handlers) adminCreate(c *gin.Context) {
	var req createRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	row, err := h.svc.create(c.Request.Context(), platformBucket, caller(c), req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionCreatePlatf, row.ID, map[string]any{"name": row.Name})
	one(c, row)
}

func (h *handlers) adminUpdate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req patchRequest
	if !bindPatch(c, &req) {
		return
	}
	row, err := h.svc.update(c.Request.Context(), adminTarget, caller(c), id, req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionUpdate, row.ID, map[string]any{"name": row.Name, "scope": row.Scope})
	one(c, row)
}

func (h *handlers) adminDelete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.removePlatform(c.Request.Context(), id); err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionDeletePlatf, id, nil)
	httpx.OK(c, gin.H{"deleted": true})
}
