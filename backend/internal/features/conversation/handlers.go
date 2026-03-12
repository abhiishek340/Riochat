package conversation

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"reflect"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

type api struct{ s *service }

func (a *api) fail(c *gin.Context, err error) { httpx.FailErr(c, err) }

func uid(c *gin.Context) uint { return middleware.MustPrincipal(c).UserID }

// auditEntry records an audit entry synchronously; failures are only logged.
func (s *service) auditEntry(c *gin.Context, userID uint, action, resource, resourceID string, detail map[string]any) {
	e := s.buildAudit(c, userID, action, resource, resourceID, detail)
	// A client that has already left must not turn the entry into a failure.
	if err := s.ports.Auditor.Record(context.WithoutCancel(c.Request.Context()), e); err != nil {
		s.log.Warn("conversation_audit_failed", logErr(err))
	}
}

// auditAsync records an audit entry without delaying a stream.
func (s *service) auditAsync(c *gin.Context, userID uint, action, resource, resourceID string, detail map[string]any) {
	e := s.buildAudit(c, userID, action, resource, resourceID, detail)
	s.lc.Detach(c.Request.Context(), "conversation-audit", 5*time.Second, func(ctx context.Context) {
		if err := s.ports.Auditor.Record(ctx, e); err != nil {
			s.log.Warn("conversation_audit_failed", logErr(err))
		}
	})
}

func (s *service) buildAudit(c *gin.Context, userID uint, action, resource, resourceID string, detail map[string]any) AuditEntry {
	ac := middleware.AuditFromRequest(c.Request)
	return AuditEntry{
		UserID: userID, RequestID: reqctx.FromRequest(c.Request).RequestID(), ClientIP: ac.ClientIP, UserAgent: ac.UserAgent,
		Action: action, Resource: resource, ResourceID: resourceID, Detail: detail,
	}
}

// bindOptional decodes a JSON body that may be empty.
func bindOptional(c *gin.Context, dst any) bool {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
	if err != nil {
		httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithCause(err))
		return false
	}
	if strings.TrimSpace(string(raw)) == "" {
		return true
	}
	return httpxBindBytes(c, raw, dst)
}

func queryFlag(c *gin.Context, key string) bool { return c.Query(key) == "true" }

// ----- conversations -----

type createConversationReq struct {
	Title     string `json:"title" binding:"max=255"`
	Model     string `json:"model" binding:"max=128"`
	ProjectID string `json:"projectID" binding:"max=32"`
}

func (a *api) createConversation(c *gin.Context) {
	var req createConversationReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.createConversation(c.Request.Context(), uid(c), req.Title, req.Model, req.ProjectID)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "create_conversation", "conversation", d.PublicID, map[string]any{"project_id": d.ProjectID, "model": d.Model})
	httpx.OK(c, d)
}

func (a *api) listConversations(c *gin.Context) {
	q := c.Request.URL.Query()
	items, total, err := a.s.listConversations(c.Request.Context(), uid(c), listFilter{
		Status: q.Get("status"), Starred: q.Get("starred"), Share: q.Get("share"), Project: q.Get("project"), Query: q.Get("q"),
	}, pagination.FromRequest(c.Request))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.Paged(c, total, items)
}

func (a *api) searchConversations(c *gin.Context) {
	items, more, err := a.s.searchConversations(c.Request.Context(), uid(c), c.Query("q"), pagination.FromRequest(c.Request))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"hasMore": more, "results": items})
}

func (a *api) getConversation(c *gin.Context) {
	d, err := a.s.getConversationDTO(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, d)
}

func (a *api) defaultModelCandidate(c *gin.Context) {
	httpx.OK(c, a.s.defaultModelCandidate(c.Request.Context()))
}

type titleReq struct {
	Title string `json:"title" binding:"required,max=255"`
}

func (a *api) rename(c *gin.Context) {
	var req titleReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.renameConversation(c.Request.Context(), uid(c), c.Param("id"), req.Title)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "rename_conversation", "conversation", d.PublicID, nil)
	httpx.OK(c, d)
}

func (a *api) regenerateTitle(c *gin.Context) {
	d, err := a.s.regenerateTitle(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "regenerate_conversation_title", "conversation", d.PublicID, nil)
	httpx.OK(c, d)
}

type labelsReq struct {
	Labels []string `json:"labels" binding:"max=6,dive,max=24"`
}

func (a *api) updateLabels(c *gin.Context) {
	var req labelsReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if req.Labels == nil {
		a.fail(c, ErrLabelsRequired)
		return
	}
	d, err := a.s.updateLabels(c.Request.Context(), uid(c), c.Param("id"), req.Labels)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "update_conversation_labels", "conversation", d.PublicID, map[string]any{"label_count": len(req.Labels)})
	httpx.OK(c, d)
}

type starReq struct {
	Starred *bool `json:"starred" binding:"required"`
}

func (a *api) star(c *gin.Context) {
	var req starReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.setStar(c.Request.Context(), uid(c), c.Param("id"), *req.Starred)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "set_conversation_star", "conversation", d.PublicID, map[string]any{"starred": d.IsStarred})
	httpx.OK(c, d)
}

type archiveReq struct {
	Archived *bool `json:"archived" binding:"required"`
}

func (a *api) archive(c *gin.Context) {
	var req archiveReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.setArchive(c.Request.Context(), uid(c), c.Param("id"), *req.Archived)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "set_conversation_archive", "conversation", d.PublicID, map[string]any{"archived": *req.Archived})
	httpx.OK(c, d)
}

func (a *api) deleteConversation(c *gin.Context) {
	deleteFiles := queryFlag(c, "delete_files")
	res, err := a.s.deleteConversation(c.Request.Context(), uid(c), c.Param("id"), deleteFiles)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "delete_conversation", "conversation", strings.TrimSpace(c.Param("id")),
		map[string]any{"delete_files": deleteFiles, "deleted_file_count": res.DeletedFileCount})
	httpx.OK(c, res)
}

type setProjectReq struct {
	ProjectID string `json:"projectID" binding:"max=32"`
}

func (a *api) setProject(c *gin.Context) {
	var req setProjectReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.setConversationProject(c.Request.Context(), uid(c), c.Param("id"), req.ProjectID)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "set_conversation_project", "conversation", d.PublicID, map[string]any{"project_id": d.ProjectID})
	httpx.OK(c, d)
}

type batchProjectReq struct {
	ConversationPublicIDs []string `json:"conversationPublicIDs" binding:"required,max=1000"`
	ProjectID             string   `json:"projectID" binding:"max=32"`
}

func (a *api) batchSetProject(c *gin.Context) {
	var req batchProjectReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	n, err := a.s.batchSetProject(c.Request.Context(), uid(c), req.ConversationPublicIDs, req.ProjectID)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "batch_set_conversation_project", "conversation", "", map[string]any{"project_id": req.ProjectID, "count": n})
	httpx.OK(c, gin.H{"updated": n})
}

// ----- export -----

func (a *api) exportConversation(c *gin.Context) {
	doc, err := a.s.exportConversation(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "export_conversation", "conversation", doc.Conversation.PublicID, map[string]any{"message_count": doc.TotalMessages})
	httpx.OK(c, doc)
}

func (a *api) exportAll(c *gin.Context) {
	h := c.Writer.Header()
	h.Set("Content-Type", "application/x-ndjson")
	h.Set("Content-Disposition", `attachment; filename="my-conversations-`+a.s.now().UTC().Format("20060102-150405")+`.jsonl"`)
	h.Set("Cache-Control", "no-store")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	w := &lineWriter{c: c}
	a.s.streamExportAll(c.Request.Context(), uid(c), w.write)
	a.s.auditEntry(c, uid(c), "export_all_conversations", "conversation", "", nil)
}

// ----- projects -----

func (a *api) listProjects(c *gin.Context) {
	items, err := a.s.listProjects(c.Request.Context(), uid(c), c.Query("status"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, items)
}
