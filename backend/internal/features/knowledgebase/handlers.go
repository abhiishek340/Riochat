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

func (h *handlers) adminDelete(c *gin.Context) {
	deleteFiles := c.Query("delete_files") == "true"
	n, err := h.svc.deleteBase(c.Request.Context(), adminAccess(callerID(c)), c.Param("id"), deleteFiles)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.delete_builtin", c.Param("id"),
		map[string]any{"delete_files": deleteFiles, "deleted_file_count": n})
	httpx.OK(c, deleteResponse{Deleted: true, DeletedFileCount: n})
}

// ----- files of a base -----

func (h *handlers) serveFiles(c *gin.Context, a access) {
	views, total, err := h.svc.listFiles(c.Request.Context(), a, c.Param("id"), pagination.FromRequest(c.Request))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.Paged(c, total, views)
}

func (h *handlers) filesVisible(c *gin.Context) { h.serveFiles(c, visibleTo(callerID(c))) }
func (h *handlers) adminFiles(c *gin.Context)   { h.serveFiles(c, adminAccess(callerID(c))) }

func (h *handlers) serveAvailable(c *gin.Context, a access) {
	views, total, err := h.svc.availableFiles(c.Request.Context(), a, c.Param("id"), c.Query("q"), pagination.FromRequest(c.Request))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.Paged(c, total, views)
}

func (h *handlers) availableMine(c *gin.Context) { h.serveAvailable(c, personalOf(callerID(c))) }
func (h *handlers) adminAvailable(c *gin.Context) {
	h.serveAvailable(c, adminAccess(callerID(c)))
}

func (h *handlers) addMine(c *gin.Context) {
	var req fileIDsRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	uid := callerID(c)
	if err := h.svc.addFiles(c.Request.Context(), personalOf(uid), uid, c.Param("id"), req.FileIDs); err != nil {
		h.fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"updated": true})
}

func (h *handlers) adminAddFiles(c *gin.Context) {
	var req fileIDsRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	uid := callerID(c)
	if err := h.svc.addFiles(c.Request.Context(), adminAccess(uid), uid, c.Param("id"), req.FileIDs); err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.add_files_builtin", c.Param("id"), map[string]any{"count": len(req.FileIDs)})
	httpx.OK(c, gin.H{"updated": true})
}

func (h *handlers) removeMine(c *gin.Context) {
	if err := h.svc.removeFile(c.Request.Context(), personalOf(callerID(c)), c.Param("id"), c.Param("file_id")); err != nil {
		h.fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"updated": true})
}

func (h *handlers) adminRemoveFile(c *gin.Context) {
	if err := h.svc.removeFile(c.Request.Context(), adminAccess(callerID(c)), c.Param("id"), c.Param("file_id")); err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.remove_file_builtin", c.Param("id"), map[string]any{"file_id": c.Param("file_id")})
	httpx.OK(c, gin.H{"updated": true})
}

// ----- processing status -----

func (h *handlers) serveStatuses(c *gin.Context, a access) {
	var req fileIDsRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	sts, err := h.svc.statuses(c.Request.Context(), a, c.Param("id"), req.FileIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.OK(c, sts)
}

func (h *handlers) statusesVisible(c *gin.Context) { h.serveStatuses(c, visibleTo(callerID(c))) }
func (h *handlers) adminStatuses(c *gin.Context)   { h.serveStatuses(c, adminAccess(callerID(c))) }

func (h *handlers) serveSnapshot(c *gin.Context, a access) {
	var req optionalFileIDsRequest
	if !bindIDs(c, &req, true) {
		return
	}
	snap, err := h.svc.snapshot(c.Request.Context(), a, c.Param("id"), req.FileIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.OK(c, snap)
}

func (h *handlers) snapshotVisible(c *gin.Context) { h.serveSnapshot(c, visibleTo(callerID(c))) }
func (h *handlers) adminSnapshot(c *gin.Context)   { h.serveSnapshot(c, adminAccess(callerID(c))) }

// ----- content -----

// sendContent writes a file as a download. The body is closed here.
func (h *handlers) sendContent(c *gin.Context, fc *FileContent) {
	defer fc.Body.Close()
	name := filepath.Base(strings.ReplaceAll(fc.FileName, `\`, "/"))
	disposition := mime.FormatMediaType("attachment", map[string]string{"filename": name})
	if disposition == "" {
		disposition = "attachment"
	}
	ctype := fc.MimeType
	if ctype == "" {
		ctype = "application/octet-stream"
	}
	c.Header("Content-Disposition", disposition)
	c.Header("X-Content-Type-Options", "nosniff")
	size := fc.Size
	if size < 0 {
		size = -1
	}
	c.DataFromReader(http.StatusOK, size, ctype, fc.Body, nil)
}

func (h *handlers) serveContent(c *gin.Context, a access) {
	fc, err := h.svc.openContent(c.Request.Context(), a, c.Param("id"), c.Param("file_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendContent(c, fc)
}

func (h *handlers) contentVisible(c *gin.Context) { h.serveContent(c, visibleTo(callerID(c))) }
func (h *handlers) adminContent(c *gin.Context)   { h.serveContent(c, adminAccess(callerID(c))) }

// ----- platform file pool -----

func (h *handlers) poolList(c *gin.Context) {
	views, total, err := h.svc.platformFiles(c.Request.Context(), c.Query("q"), pagination.FromRequest(c.Request))
	if err != nil {
		h.fail(c, err)
		return
	}
	httpx.Paged(c, total, views)
}

func (h *handlers) poolContent(c *gin.Context) {
	fc, err := h.svc.openPoolContent(c.Request.Context(), c.Param("file_id"))
	if err != nil {
		h.fail(c, err)
		return
	}
	h.sendContent(c, fc)
}

func (h *handlers) poolUpload(c *gin.Context) {
	ctx := c.Request.Context()
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, h.svc.uploadLimit(ctx))
	fh, err := c.FormFile("file")
	if err != nil {
		var tooBig *http.MaxBytesError
		switch {
		case errors.As(err, &tooBig):
			h.fail(c, ErrFileRequired)
		case errors.Is(err, http.ErrMissingFile), errors.Is(err, http.ErrNotMultipart):
			h.fail(c, ErrFileRequired)
		default:
			h.fail(c, ErrInvalidStream.WithCause(err))
		}
		return
	}
	f, err := fh.Open()
	if err != nil {
		h.fail(c, ErrInvalidStream.WithCause(err))
		return
	}
	defer f.Close()

	v, err := h.svc.uploadPlatformFile(ctx, callerID(c), UploadRequest{
		FileName: filepath.Base(fh.Filename), ContentType: fh.Header.Get("Content-Type"), Size: fh.Size, Body: f,
	})
	if err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.upload_builtin_file", v.FileID,
		map[string]any{"file_name": v.FileName, "size_bytes": v.SizeBytes})
	httpx.OK(c, gin.H{"file": v})
}

func (h *handlers) poolDelete(c *gin.Context) {
	fid := strings.TrimSpace(c.Param("file_id"))
	if err := h.svc.deletePlatformFile(c.Request.Context(), fid); err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.delete_platform_file", fid, map[string]any{"deleted": true})
	httpx.OK(c, gin.H{"deleted": true})
}

func (h *handlers) poolEmbeddings(c *gin.Context) {
	var req fileIDsRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	res, requested, err := h.svc.submitEmbeddings(c.Request.Context(), req.FileIDs)
	if err != nil {
		h.fail(c, err)
		return
	}
	h.audit(c, "knowledge_base.submit_platform_file_embeddings", "", map[string]any{
		"requested_file_ids": requested, "submitted_file_ids": res.SubmittedFileIDs, "skipped_count": len(res.Skipped),
	})
	httpx.OK(c, res)
}
