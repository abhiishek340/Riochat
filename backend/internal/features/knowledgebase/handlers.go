package knowledgebase

import (
	"bytes"
	"errors"
	"io"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

type handlers struct{ svc *Service }

// ----- shared plumbing -----

// fail answers a service error. Typed errors keep their code and message;
// anything else becomes the generic area error and its text stays in the logs.
func (h *handlers) fail(c *gin.Context, err error) {
	err = mapStorage(err)
	st := statusFor(err)
	if st == http.StatusInternalServerError && !errors.Is(err, ErrInternal) {
		err = ErrInternal.WithCause(err)
	}
	httpx.Fail(c, st, err)
}

func (h *handlers) requireEnabled(c *gin.Context) {
	if !h.svc.Enabled(c.Request.Context()) {
		httpx.Fail(c, http.StatusForbidden, ErrDisabled)
		return
	}
	c.Next()
}

func callerID(c *gin.Context) uint { return middleware.MustPrincipal(c).UserID }

func (h *handlers) audit(c *gin.Context, action, resourceID string, detail map[string]any) {
	st := reqctx.FromRequest(c.Request)
	h.svc.ports.Auditor.Record(c.Request.Context(), AuditEntry{
		ActorUserID: callerID(c), RequestID: st.RequestID(), ClientIP: st.ClientIP(),
		UserAgent: c.Request.UserAgent(), Action: action, Resource: "knowledge_bases",
		ResourceID: resourceID, Detail: detail,
	})
}

func one(c *gin.Context, v *KnowledgeBaseView) { httpx.OK(c, gin.H{"knowledgeBase": v}) }

// parseLenientBool reads a boolean filter; anything unparsable means "no filter".
func parseLenientBool(raw string) *bool {
	v, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	return &v
}

// bindIDs decodes a body of the shape {fileIDs: [...]}. When allowEmpty is
// set a missing or empty body is accepted and yields no ids.
func bindIDs[T any](c *gin.Context, dst *T, allowEmpty bool) bool {
	if allowEmpty {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
		if err != nil {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrInvalid.WithCause(err))
			return false
		}
		if len(bytes.TrimSpace(raw)) == 0 {
			return true
		}
		c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	}
	return httpx.BindJSON(c, dst)
}

type fileIDsRequest struct {
	FileIDs []string `json:"fileIDs" binding:"required,min=1,max=100,dive,min=1,max=64"`
}

type optionalFileIDsRequest struct {
	FileIDs []string `json:"fileIDs" binding:"omitempty,max=100,dive,min=1,max=64"`
}

// ----- lists -----

func (h *handlers) listFrom(c *gin.Context, mode listMode, uid uint) (listQuery, bool) {
	q := c.Request.URL.Query()
	lq := listQuery{
		mode: mode, userID: uid, keyword: q.Get("q"), sort: q.Get("sort"),
		page: pagination.FromRequest(c.Request),
	}
	if mode != listVisible {
		lq.enabled = parseLenientBool(q.Get("enabled"))
	}
	ids, ok := normalizeIDFilter(q["id"])
	lq.ids = ids
	return lq, ok
}

func (h *handlers) serveList(c *gin.Context, mode listMode, uid uint) {
	lq, ok := h.listFrom(c, mode, uid)
	if !ok {
		httpx.Paged[KnowledgeBaseView](c, 0, nil)
		return
	}
	views, total, err := h.svc.list(c.Request.Context(), lq)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.Paged(c, total, views)
}

func (h *handlers) listVisible(c *gin.Context) { h.serveList(c, listVisible, callerID(c)) }
func (h *handlers) listMine(c *gin.Context)    { h.serveList(c, listMine, callerID(c)) }
func (h *handlers) adminList(c *gin.Context)   { h.serveList(c, listBuiltin, 0) }

// ----- single base -----

func (h *handlers) serveGet(c *gin.Context, a access) {
	v, err := h.svc.get(c.Request.Context(), a, c.Param("id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	one(c, v)
}

func (h *handlers) getVisible(c *gin.Context) { h.serveGet(c, visibleTo(callerID(c))) }
func (h *handlers) adminGet(c *gin.Context)   { h.serveGet(c, adminAccess(callerID(c))) }

type createMineRequest struct {
	Name        string `json:"name" binding:"required,max=80"`
	Description string `json:"description" binding:"omitempty,max=255"`
}

func (h *handlers) createMine(c *gin.Context) {
	var req createMineRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.createPersonal(c.Request.Context(), callerID(c), req.Name, req.Description)
	if err != nil {
		h.fail(c, err)
		return
	}
	one(c, v)
}

type patchMineRequest struct {
	Name        *string `json:"name" binding:"omitempty,max=80"`
	Description *string `json:"description" binding:"omitempty,max=255"`
	// Enabled and SortOrder are decoded only so that a body carrying them can
	// be refused; personal bases cannot be disabled or reordered.
	Enabled   *bool `json:"enabled"`
	SortOrder *int  `json:"sortOrder"`
}

func (h *handlers) updateMine(c *gin.Context) {
	var req patchMineRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.updatePersonal(c.Request.Context(), callerID(c), c.Param("id"), patchInput{
		Name: req.Name, Description: req.Description, Enabled: req.Enabled, SortOrder: req.SortOrder,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	one(c, v)
}

type deleteResponse struct {
	Deleted          bool `json:"deleted"`
	DeletedFileCount int  `json:"deletedFileCount,omitempty"`
}

func (h *handlers) deleteMine(c *gin.Context) {
	deleteFiles := c.Query("delete_files") == "true"
	n, err := h.svc.deleteBase(c.Request.Context(), personalOf(callerID(c)), c.Param("id"), deleteFiles)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.OK(c, deleteResponse{Deleted: true, DeletedFileCount: n})
}

type adminCreateRequest struct {
	Name        string `json:"name" binding:"required,max=80"`
	Description string `json:"description" binding:"omitempty,max=255"`
	Enabled     *bool  `json:"enabled"`
	SortOrder   int    `json:"sortOrder"`
}

func (h *handlers) adminCreate(c *gin.Context) {
	var req adminCreateRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	v, err := h.svc.createBuiltin(c.Request.Context(), callerID(c), createInput{
		Name: req.Name, Description: req.Description, Enabled: enabled, SortOrder: req.SortOrder,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.create_builtin", v.PublicID, map[string]any{"name": v.Name})
	one(c, v)
}

type adminPatchRequest struct {
	Name        *string `json:"name" binding:"omitempty,max=80"`
	Description *string `json:"description" binding:"omitempty,max=255"`
	Enabled     *bool   `json:"enabled"`
	SortOrder   *int    `json:"sortOrder"`
}

func (h *handlers) adminUpdate(c *gin.Context) {
	var req adminPatchRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	v, err := h.svc.updateBuiltin(c.Request.Context(), callerID(c), c.Param("id"), patchInput{
		Name: req.Name, Description: req.Description, Enabled: req.Enabled, SortOrder: req.SortOrder,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.update_builtin", v.PublicID, nil)
	one(c, v)
}
