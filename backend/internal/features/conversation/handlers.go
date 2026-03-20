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

func (a *api) createProject(c *gin.Context) {
	var req createProjectRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.createProject(c.Request.Context(), uid(c), req)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "create_conversation_project", "conversation_project", d.PublicID, nil)
	httpx.OK(c, d)
}

func (a *api) updateProject(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		a.fail(c, ErrProjectInvalidID)
		return
	}
	var req updateProjectRequest
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.updateProject(c.Request.Context(), uid(c), id, req)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "update_conversation_project", "conversation_project", d.PublicID, nil)
	httpx.OK(c, d)
}

func (a *api) deleteProject(c *gin.Context) {
	id := strings.TrimSpace(c.Param("id"))
	if id == "" {
		a.fail(c, ErrProjectInvalidID)
		return
	}
	delConvs := queryFlag(c, "delete_conversations")
	delFiles := delConvs && queryFlag(c, "delete_files")
	res, err := a.s.deleteProject(c.Request.Context(), uid(c), id, delConvs, delFiles)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "delete_conversation_project", "conversation_project", id,
		map[string]any{"delete_conversations": delConvs, "delete_files": delFiles, "deleted_file_count": res.DeletedFileCount})
	httpx.OK(c, res)
}

type reorderReq struct {
	ProjectIDs []string `json:"projectIDs" binding:"required,max=200"`
}

func (a *api) reorderProjects(c *gin.Context) {
	var req reorderReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	items, err := a.s.reorderProjects(c.Request.Context(), uid(c), req.ProjectIDs)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "reorder_conversation_projects", "conversation_project", "", map[string]any{"count": len(req.ProjectIDs)})
	httpx.OK(c, items)
}

// ----- messages -----

func (a *api) listMessages(c *gin.Context) {
	q := messageListQuery{Page: pagination.FromRequest(c.Request), Tail: c.Query("tail") == "true", Summary: c.Query("trace") == "summary"}
	if raw, ok := c.GetQuery("before_id"); ok && strings.TrimSpace(raw) != "" {
		n, err := strconv.ParseUint(strings.TrimSpace(raw), 10, 64)
		if err != nil || n == 0 {
			a.fail(c, ErrBeforeMessageID)
			return
		}
		id := uint(n)
		q.BeforeID = &id
	}
	total, items, err := a.s.listMessages(c.Request.Context(), uid(c), c.Param("id"), q)
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.Paged(c, total, items)
}

func (a *api) previewMessages(c *gin.Context) {
	items, err := a.s.previewMessages(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, items)
}

func (a *api) listRuns(c *gin.Context) {
	items, total, err := a.s.listRuns(c.Request.Context(), uid(c), c.Param("id"), pagination.FromRequest(c.Request))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.Paged(c, total, items)
}

type statusesReq struct {
	RunIDs []string `json:"runIDs" binding:"required,min=1,max=100,dive,required,max=64"`
}

func (a *api) runStatuses(c *gin.Context) {
	raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, httpx.DefaultBodyLimit))
	var req statusesReq
	if err != nil || json.Unmarshal(raw, &req) != nil || len(httpx.ValidateStruct(&req)) > 0 {
		a.fail(c, ErrInvalidRunIDs)
		return
	}
	items, err := a.s.runStatuses(c.Request.Context(), uid(c), req.RunIDs)
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, items)
}

type editReq struct {
	Content string `json:"content" binding:"required"`
}

func (a *api) editMessage(c *gin.Context) {
	var req editReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	d, err := a.s.editMessage(c.Request.Context(), uid(c), c.Param("id"), req.Content)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "update_message", "message", d.PublicID, nil)
	httpx.OK(c, d)
}

func (a *api) messageTrace(c *gin.Context) {
	t, err := a.s.messageTrace(c.Request.Context(), uid(c), c.Param("id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, gin.H{"processTrace": t})
}

type feedbackReq struct {
	Feedback string `json:"feedback"`
}

// validFeedbackValue accepts up, down or empty in any letter case.
func validFeedbackValue(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "", "up", "down":
		return true
	}
	return false
}

func (a *api) feedback(c *gin.Context) {
	var req feedbackReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	if !validFeedbackValue(req.Feedback) {
		httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithMessage("invalid request body: feedback must be one of: up down").
			WithDetails(map[string]any{"reason": "validation", "fieldErrors": []httpx.FieldError{
				{Field: "feedback", Rule: "oneof", Param: "up down", Message: "feedback must be one of: up down"}}}))
		return
	}
	res, err := a.s.setFeedback(c.Request.Context(), uid(c), c.Param("id"), req.Feedback)
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "set_message_feedback", "message", res.MessagePublicID, map[string]any{"feedback": res.MyFeedback})
	httpx.OK(c, res)
}

func (a *api) deleteMessage(c *gin.Context) {
	res, err := a.s.deleteMessage(c.Request.Context(), uid(c), c.Param("id"), c.Param("message_id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "delete_message", "message", strings.TrimSpace(c.Param("message_id")),
		map[string]any{"conversation_id": c.Param("id"), "reparented": res.ReparentedMessageCount})
	httpx.OK(c, res)
}

func (a *api) fork(c *gin.Context) {
	res, err := a.s.forkConversation(c.Request.Context(), uid(c), c.Param("id"), c.Param("message_id"))
	if err != nil {
		a.fail(c, err)
		return
	}
	a.s.auditEntry(c, uid(c), "fork_conversation", "conversation", res.Conversation.PublicID,
		map[string]any{"source_conversation_id": res.SourceID, "source_message_id": res.MessageID, "message_count": res.CopiedCount})
	httpx.OK(c, res.Conversation)
}

// dupBlockKey marks a request that named one block twice. It is never a valid
// block id, so the service rejects the request as an invalid UI state.
const dupBlockKey = "\x00duplicate"

// uiBlocks is the blocks object of a UI-state request. Decoding keeps the raw
// value of every block and records repeated names, which a plain map would
// silently collapse.
type uiBlocks map[string]json.RawMessage

func (b *uiBlocks) UnmarshalJSON(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if tok == nil {
		*b = nil
		return nil
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return &json.UnmarshalTypeError{Value: "non-object", Type: reflect.TypeOf(map[string]json.RawMessage{})}
	}
	out := uiBlocks{}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := kt.(string)
		var val json.RawMessage
		if err := dec.Decode(&val); err != nil {
			return err
		}
		if _, seen := out[key]; seen {
			out[dupBlockKey] = json.RawMessage("{}")
			continue
		}
		out[key] = val
	}
	*b = out
	return nil
}

type uiStateReq struct {
	Blocks uiBlocks `json:"blocks" binding:"required,min=1,max=32"`
}

func (a *api) putUIState(c *gin.Context) {
	var req uiStateReq
	if !httpx.BindJSON(c, &req) {
		return
	}
	res, err := a.s.putUIState(c.Request.Context(), uid(c), c.Param("id"), c.Param("message_id"), req.Blocks)
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, res)
}

func (a *api) artifact(c *gin.Context) {
	n, err := strconv.ParseUint(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || n == 0 {
		a.fail(c, ErrArtifactInvalidID)
		return
	}
	d, err := a.s.getArtifact(c.Request.Context(), uid(c), uint(n))
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, d)
}

func (a *api) toolCall(c *gin.Context) {
	runID, ok := validRunParam(c.Param("run_id"))
	if !ok {
		a.fail(c, ErrRunInvalidID)
		return
	}
	callID := strings.TrimSpace(c.Param("tool_call_id"))
	if callID == "" || len(callID) > 255 {
		a.fail(c, ErrToolCallInvalidID)
		return
	}
	d, err := a.s.toolCallDetail(c.Request.Context(), uid(c), runID, callID)
	if err != nil {
		a.fail(c, err)
		return
	}
	httpx.OK(c, d)
}
