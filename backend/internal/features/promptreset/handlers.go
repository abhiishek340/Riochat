package promptreset

import (
	"errors"
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
	auditResource      = "prompt_presets"
	actionCreateGlobal = "prompt_preset.create_builtin"
	actionUpdateGlobal = "prompt_preset.update_builtin"
	actionDeleteGlobal = "prompt_preset.delete_builtin"
)

type handlers struct {
	svc   *service
	audit *audit.Recorder
}

// createRequest limits are checked on the values as received.
type createRequest struct {
	Title       string `json:"title" binding:"required,max=64"`
	Trigger     string `json:"trigger" binding:"required,max=64"`
	Description string `json:"description" binding:"max=256"`
	Content     string `json:"content" binding:"required,max=10000"`
	// Enabled is accepted (and type-checked) but ignored: a preset is always
	// created enabled, matching the legacy API. Use PATCH to disable it.
	Enabled   *bool `json:"enabled"`
	SortOrder int32 `json:"sortOrder"`
}

func (r createRequest) raw() rawInput {
	return rawInput{Title: r.Title, Trigger: r.Trigger, Description: r.Description,
		Content: r.Content, Enabled: true, SortOrder: int(r.SortOrder)}
}

type patchRequest struct {
	Title       *string `json:"title" binding:"omitempty,max=64"`
	Trigger     *string `json:"trigger" binding:"omitempty,max=64"`
	Description *string `json:"description" binding:"omitempty,max=256"`
	Content     *string `json:"content" binding:"omitempty,max=10000"`
	Enabled     *bool   `json:"enabled"`
	SortOrder   *int32  `json:"sortOrder"`
}

func (r patchRequest) raw() rawPatch {
	var so *int
	if r.SortOrder != nil {
		v := int(*r.SortOrder)
		so = &v
	}
	return rawPatch{Title: r.Title, Trigger: r.Trigger, Description: r.Description,
		Content: r.Content, Enabled: r.Enabled, SortOrder: so}
}

// fail maps a service error to a response; anything not recognised is a 500
// whose cause is only logged.
func fail(c *gin.Context, err error) {
	status := http.StatusInternalServerError
	switch {
	case errors.Is(err, ErrNotFound):
		status = http.StatusNotFound
	case errors.Is(err, ErrInvalid):
		status = http.StatusBadRequest
	case errors.Is(err, ErrTriggerExists):
		status = http.StatusConflict
	case errors.Is(err, ErrInvalidID):
		status = http.StatusBadRequest
	}
	if status == http.StatusInternalServerError {
		if _, typed := apperr.Find(err); !typed {
			err = apperr.ErrInternal.WithCause(err)
		}
	}
	httpx.Fail(c, status, err)
}

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

// lenientBool reads an optional boolean filter; empty or invalid means none.
func lenientBool(raw string) *bool {
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return nil
	}
	return &v
}

func filterFromQuery(c *gin.Context) listFilter {
	return listFilter{
		Keyword: strings.TrimSpace(c.Query("q")),
		Enabled: lenientBool(c.Query("enabled")),
	}
}

func one(c *gin.Context, p *PromptPreset) { httpx.OK(c, gin.H{"promptPreset": toView(p)}) }

func (h *handlers) record(c *gin.Context, action string, id uint, detail map[string]any) {
	var d any
	if detail != nil {
		d = detail
	}
	h.audit.RecordRequest(c, action, auditResource, strconv.FormatUint(uint64(id), 10), d)
}

// --- user routes ---

func (h *handlers) listVisible(c *gin.Context) {
	rows, total, err := h.svc.listVisible(c.Request.Context(), caller(c), c.Query("q"), pagination.FromRequest(c.Request))
	if err != nil {
		fail(c, err)
		return
	}
	httpx.Paged(c, total, toViews(rows))
}

func (h *handlers) listMine(c *gin.Context) {
	rows, total, err := h.svc.listOwned(c.Request.Context(), userOwner(caller(c)), filterFromQuery(c), pagination.FromRequest(c.Request))
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
	row, err := h.svc.create(c.Request.Context(), userOwner(uid), uid, req.raw())
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
	if !httpx.BindJSON(c, &req) {
		return
	}
	uid := caller(c)
	row, err := h.svc.update(c.Request.Context(), userOwner(uid), uid, id, req.raw())
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
	if err := h.svc.remove(c.Request.Context(), userOwner(caller(c)), id); err != nil {
		fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"deleted": true})
}

// --- administrator routes (builtin presets only) ---

func (h *handlers) adminList(c *gin.Context) {
	rows, total, err := h.svc.listOwned(c.Request.Context(), builtinOwner, filterFromQuery(c), pagination.FromRequest(c.Request))
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
	row, err := h.svc.create(c.Request.Context(), builtinOwner, caller(c), req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionCreateGlobal, row.ID, map[string]any{"trigger": row.Trigger})
	one(c, row)
}

func (h *handlers) adminUpdate(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	var req patchRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	row, err := h.svc.update(c.Request.Context(), builtinOwner, caller(c), id, req.raw())
	if err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionUpdateGlobal, row.ID, map[string]any{"trigger": row.Trigger})
	one(c, row)
}

func (h *handlers) adminDelete(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	if err := h.svc.remove(c.Request.Context(), builtinOwner, id); err != nil {
		fail(c, err)
		return
	}
	h.record(c, actionDeleteGlobal, id, nil)
	httpx.OK(c, gin.H{"deleted": true})
}
