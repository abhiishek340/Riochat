package conversation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	tempJSONLimit      = 8 << 20
	tempMaxAttachments = 20
	tempMaxTotalRunes  = 1_000_000
	tempAuditAction    = "temporary_chat.stream_message"
)

type tempMessage struct {
	Role    string `json:"role" binding:"required,oneof=user assistant"`
	Content string `json:"content" binding:"max=200000"`
}

type tempUIState struct {
	MessageIndex int            `json:"messageIndex" binding:"min=0,max=99"`
	BlockID      string         `json:"blockID" binding:"max=128"`
	Title        string         `json:"title" binding:"max=200"`
	State        map[string]any `json:"state"`
}

// tempRequest is the body of POST /temporary-chat/messages/stream.
type tempRequest struct {
	SessionID        string         `json:"sessionID" binding:"required,max=64"`
	ClientRunID      string         `json:"clientRunID" binding:"required,max=64"`
	Model            string         `json:"model" binding:"required,max=128"`
	Options          map[string]any `json:"options"`
	Controls         map[string]any `json:"controls"`
	SelectedToolIDs  []uint         `json:"selectedToolIDs" binding:"max=128"`
	SkillIDs         []uint         `json:"skillIDs" binding:"max=128"`
	KnowledgeBaseIDs []string       `json:"knowledgeBaseIDs" binding:"max=8,dive,max=32"`
	HTMLVisualPrompt bool           `json:"htmlVisualPrompt"`
	Messages         []tempMessage  `json:"messages" binding:"required,min=1,max=100,dive"`
	UIComponentIDs   []uint         `json:"uiComponentIDs" binding:"max=32"`
	UIBlockVersion   int            `json:"uiBlockVersion" binding:"omitempty,oneof=1 2"`
	UIStates         []tempUIState  `json:"uiStates" binding:"max=32,dive"`
}

// tempAttachment is an uploaded file kept only for the duration of a request.
type tempAttachment struct {
	name string
	mime string
	data []byte
}

// parseTemporary reads the JSON or multipart body. It writes the error response
// itself and returns false on failure.
func (s *service) parseTemporary(c *gin.Context) (*tempRequest, []tempAttachment, []int, bool) {
	rs := s.settings(c.Request.Context())
	ct := strings.ToLower(c.GetHeader("Content-Type"))
	if !strings.HasPrefix(ct, "multipart/form-data") {
		raw, err := io.ReadAll(http.MaxBytesReader(c.Writer, c.Request.Body, tempJSONLimit))
		if err != nil {
			var tooBig *http.MaxBytesError
			if errors.As(err, &tooBig) {
				httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrTempContextTooLarge)
			} else {
				httpx.Fail(c, http.StatusBadRequest, apperr.ErrInvalidBody.WithCause(err))
			}
			return nil, nil, nil, false
		}
		var req tempRequest
		if !httpxBindBytes(c, raw, &req) {
			return nil, nil, nil, false
		}
		return &req, nil, nil, true
	}
	limit := int64(tempJSONLimit) + rs.MaxUploadBytes*tempMaxAttachments
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
	if err := c.Request.ParseMultipartForm(16 << 20); err != nil {
		var tooBig *http.MaxBytesError
		if errors.As(err, &tooBig) {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrTempContextTooLarge)
			return nil, nil, nil, false
		}
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidFile.WithCause(err))
		return nil, nil, nil, false
	}
	defer func() {
		if c.Request.MultipartForm != nil {
			_ = c.Request.MultipartForm.RemoveAll()
		}
	}()
	payload := c.Request.FormValue("payload")
	if strings.TrimSpace(payload) == "" {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidTempMessages)
		return nil, nil, nil, false
	}
	var req tempRequest
	if !httpxBindBytes(c, []byte(payload), &req) {
		return nil, nil, nil, false
	}
	var idx []int
	if raw := strings.TrimSpace(c.Request.FormValue("attachmentMessageIndexes")); raw != "" {
		if err := json.Unmarshal([]byte(raw), &idx); err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidRef)
			return nil, nil, nil, false
		}
	}
	var headers []*multipart.FileHeader
	if c.Request.MultipartForm != nil {
		headers = c.Request.MultipartForm.File["attachments"]
	}
	if len(headers) == 0 || len(headers) > tempMaxAttachments || len(headers) != len(idx) {
		httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidRef)
		return nil, nil, nil, false
	}
	var atts []tempAttachment
	for _, h := range headers {
		if h.Size > rs.MaxUploadBytes {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrFileTooLarge)
			return nil, nil, nil, false
		}
		f, err := h.Open()
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidStream.WithCause(err))
			return nil, nil, nil, false
		}
		data, err := io.ReadAll(io.LimitReader(f, rs.MaxUploadBytes+1))
		_ = f.Close()
		if err != nil {
			httpx.Fail(c, http.StatusBadRequest, ErrFileInvalidStream.WithCause(err))
			return nil, nil, nil, false
		}
		if int64(len(data)) > rs.MaxUploadBytes {
			httpx.Fail(c, http.StatusRequestEntityTooLarge, ErrFileTooLarge)
			return nil, nil, nil, false
		}
		name := filepath.Base(h.Filename)
		head := data
		if len(head) > 512 {
			head = head[:512]
		}
		mt := detectMIME(name, head, h.Header.Get("Content-Type"))
		if blockedMIME[mt] || blockedExt[strings.ToLower(filepath.Ext(name))] {
			httpx.Fail(c, http.StatusBadRequest, ErrFileTypeBlocked)
			return nil, nil, nil, false
		}
		atts = append(atts, tempAttachment{name: name, mime: mt, data: data})
	}
	return &req, atts, idx, true
}

func httpxBindBytes(c *gin.Context, raw []byte, dst any) bool {
	saved := c.Request.Body
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	ok := httpx.BindJSONLimit(c, dst, tempJSONLimit)
	c.Request.Body = saved
	return ok
}

// validatedTemp is a temporary-chat request after semantic validation.
type validatedTemp struct {
	req      *tempRequest
	messages []PromptMessage
}

// validateTemporary applies the semantic rules of the temporary chat request.
func (s *service) validateTemporary(ctx context.Context, req *tempRequest, atts []tempAttachment, idx []int) (*validatedTemp, error) {
	rs := s.settings(ctx)
	msgs := make([]PromptMessage, len(req.Messages))
	prev := ""
	total := 0
	perMsg := map[int]int{}
	for _, i := range idx {
		if i < 0 || i >= len(req.Messages) || strings.ToLower(strings.TrimSpace(req.Messages[i].Role)) != roleUser {
			return nil, ErrInvalidTempMessages
		}
		perMsg[i]++
	}
	if len(atts) > tempMaxAttachments {
		return nil, ErrFileInvalidRef
	}
	for i, m := range req.Messages {
		role := strings.ToLower(strings.TrimSpace(m.Role))
		if role != roleUser && role != roleAssistant {
			return nil, ErrInvalidTempMessages
		}
		if role == prev {
			return nil, ErrInvalidTempMessages
		}
		prev = role
		content := strings.TrimSpace(m.Content)
		if content == "" && !(role == roleUser && perMsg[i] > 0) {
			return nil, ErrInvalidTempMessages
		}
		total += utf8.RuneCountInString(content)
		msgs[i] = PromptMessage{Role: role, Content: content}
	}
	if prev != roleUser || total > tempMaxTotalRunes {
		return nil, ErrInvalidTempMessages
	}
	for _, n := range perMsg {
		if n > rs.MaxMessageFiles {
			return nil, ErrTooManyFiles
		}
	}
	seen := map[string]bool{}
	for _, kb := range req.KnowledgeBaseIDs {
		t := strings.TrimSpace(kb)
		if t == "" || seen[t] {
			return nil, ErrInvalidTempMessages
		}
		seen[t] = true
	}
	if len(seen) > 0 && !rs.KnowledgeBaseEnabled {
		return nil, ErrInvalidKBReference
	}
	if len(req.SelectedToolIDs) > rs.MaxSelectedTools {
		return nil, ErrTooManyTools
	}
	if len(req.SkillIDs) > rs.MaxSelectedTools {
		return nil, ErrTooManySkills
	}
	for k, i := range idx {
		a := atts[k]
		pf := PromptFile{FileName: a.name, MimeType: a.mime, Kind: fileCategory(a.mime, a.name), Size: int64(len(a.data)), Data: a.data}
		if isTextual(a.mime, a.name) && utf8.Valid(a.data) {
			pf.Text = string(a.data)
		}
		msgs[i].Files = append(msgs[i].Files, pf)
	}
	return &validatedTemp{req: req, messages: msgs}, nil
}

// blockVersionOf maps an absent or unknown UI block version to the static one.
func blockVersionOf(v int) int {
	if v == 2 {
		return 2
	}
	return 1
}

func hashSession(id string) string {
	sum := sha256.Sum256([]byte(id))
	return hex.EncodeToString(sum[:8])
}

// runTemporary performs the stateless stream. Nothing is persisted and the
// generation is bound to the request context.
func (s *service) runTemporary(c *gin.Context, vt *validatedTemp) {
	ctx := c.Request.Context()
	p := middleware.MustPrincipal(c)
	req := vt.req
	audit := func(status string, extra map[string]any) {
		detail := map[string]any{"status": status, "message_count": len(req.Messages), "session_hash": hashSession(req.SessionID), "content_stored": false}
		for k, v := range extra {
			detail[k] = v
		}
		s.auditEntry(c, p.UserID, tempAuditAction, "temporary_chat", "", detail)
	}
	model := strings.TrimSpace(req.Model)
	if !s.ports.Catalog.ChatModelAvailable(ctx, p.UserID, model) {
		httpx.Fail(c, http.StatusForbidden, ErrModelAccessDenied)
		return
	}
	var kbs []string
	for _, kb := range req.KnowledgeBaseIDs {
		kbs = append(kbs, strings.TrimSpace(kb))
	}
	refs := preparedRequest{
		ToolIDs: dedupeUints(req.SelectedToolIDs, false), SkillIDs: dedupeUints(req.SkillIDs, true), KBIDs: kbs,
	}
	if err := s.validateTools(ctx, p.UserID, refs); err != nil {
		httpx.FailErr(c, err)
		return
	}
	if err := s.validateRefs(ctx, p.UserID, refs); err != nil {
		httpx.FailErr(c, err)
		return
	}
	runID := normalizeRunID(req.ClientRunID)
	res, rerr := s.ports.Billing.Reserve(ctx, ReserveRequest{UserID: p.UserID, Model: model, Task: taskChat, RunID: runID})
	if rerr != nil {
		audit("billing_failed", nil)
		httpx.FailErr(c, rerr)
		return
	}
	opts := sanitizeOptions(req.Options, p.IsAdmin())
	controls := sanitizeControls(req.Controls)
	effort := reasoningEffortOf(controls, opts)
	if effort != "" {
		if _, ok := controls["reasoning"]; !ok {
			controls["reasoning"] = effort
		}
	}
	rs := s.settings(ctx)
	hints := []UIStateHint{}
	if rs.UIComponentsEnabled {
		used := 0
		for _, st := range req.UIStates {
			if st.MessageIndex >= len(req.Messages) || st.BlockID == "" || len(st.State) == 0 {
				continue
			}
			size := len(marshalNoEscape(st.State))
			if size > maxUIStateBytes || used+size > uiHintBudget {
				continue
			}
			used += size
			hints = append(hints, UIStateHint{MessageIndex: st.MessageIndex, BlockID: st.BlockID, Title: st.Title, State: st.State})
		}
	}
	gen := GenerateRequest{
		Task: taskChat, UserID: p.UserID, RunID: runID, RequestID: reqctx.FromRequest(c.Request).RequestID(), Model: model,
		Messages: vt.messages, Options: opts, Controls: controls, ReasoningEffort: effort, ToolIDs: req.SelectedToolIDs,
		SkillIDs: dedupeUints(req.SkillIDs, true), KnowledgeBaseIDs: req.KnowledgeBaseIDs, HTMLVisualPrompt: req.HTMLVisualPrompt,
		UIComponentIDs: req.UIComponentIDs, UIBlockVersion: blockVersionOf(req.UIBlockVersion), UIStates: hints, Temporary: true,
		MaxLLMCalls: rs.MaxLLMCalls,
	}
	if !rs.UIComponentsEnabled {
		gen.UIComponentIDs = nil
	}

	h := c.Writer.Header()
	h.Set("Content-Type", "application/x-ndjson; charset=utf-8")
	h.Set("Cache-Control", "no-store, no-cache, no-transform")
	h.Set("Pragma", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	c.Status(http.StatusOK)
	c.Writer.WriteHeaderNow()
	c.Writer.Flush()
	w := &lineWriter{c: c}
	seq := 0
	emit := func(typ string, fields map[string]any) {
		seq++
		obj := map[string]any{"type": typ, "seq": seq}
		for k, v := range fields {
			obj[k] = v
		}
		w.write([]byte(marshalNoEscape(obj)))
	}
	requestID := reqctx.FromRequest(c.Request).RequestID()
	fail := func(f failure, status string, billable bool, partial GenerateResult) {
		if billable {
			if _, err := s.ports.Billing.Settle(context.WithoutCancel(ctx), res, SettleRequest{UserID: p.UserID, RunID: runID, Task: taskChat, Usage: partial.Usage, Route: partial.Route, Failed: true}); err != nil {
				s.log.Warn("conversation_temp_settle_failed", logErr(err))
			}
		} else {
			s.ports.Billing.Release(context.WithoutCancel(ctx), res)
		}
		if f.Canceled {
			// The client left; there is nobody to tell.
			audit("failed", map[string]any{"canceled": true})
			return
		}
		if rec := reqctx.FromRequest(c.Request); rec != nil {
			rec.RecordFailure(f.Code, f.asError(), "")
		}
		emit(evError, errorEvent(f, requestID, nil))
		audit(status, nil)
	}

	last := vt.messages[len(vt.messages)-1]
	// Images travel inside the request; they never touch the file library.
	var inputImages []ModerationImage
	for _, f := range last.Files {
		if f.Kind == "image" || strings.HasPrefix(strings.ToLower(f.MimeType), "image/") {
			inputImages = append(inputImages, ModerationImage{Name: f.FileName, Bytes: f.Data})
		}
	}
	if v, err := s.ports.Moderation.Check(ctx, ModerationRequest{UserID: p.UserID, Direction: dirInput, Text: last.Content, Ephemeral: true, Images: inputImages}); err != nil {
		s.log.Warn("conversation_moderation_failed", logErr(err))
	} else if v.Blocked {
		s.ports.Billing.Release(context.WithoutCancel(ctx), res)
		emit(evModerationBlocked, map[string]any{"eventID": v.EventID, "direction": dirInput, "categories": orEmptyStrings(v.Categories)})
		audit("blocked", map[string]any{"direction": dirInput})
		return
	}
	var kr KnowledgeResult
	if len(kbs) > 0 && s.ports.Knowledge != nil && rs.KnowledgeBaseEnabled {
		var kerr error
		kr, kerr = s.ports.Knowledge.Prepare(ctx, KnowledgeRequest{
			UserID: p.UserID, Message: last.Content, KnowledgeBaseIDs: kbs, Ephemeral: true,
			Searching: func() { emit(evRAGSearch, map[string]any{"status": "searching"}) },
		})
		if kerr != nil {
			fail(classifyFailure(kerr), "failed", false, GenerateResult{})
			return
		}
		gen.Messages = applyKnowledge(gen.Messages, kr)
		gen.DynamicContext = kr.Context
	}
	started := s.now()
	out, gerr := s.ports.Generator.Generate(ctx, gen, func(ev GenerateEvent) {
		switch ev.Type {
		case "delta", "upstream_think_delta":
			if ev.Delta != "" {
				emit(ev.Type, map[string]any{"delta": ev.Delta})
			}
		case "":
		default:
			emit(ev.Type, ev.Fields)
		}
	})
	out = mergeKnowledge(out, kr, s.now())
	if gerr == nil && ctx.Err() != nil {
		gerr = ctx.Err()
	}
	if gerr == nil && strings.TrimSpace(out.Content) == "" {
		gerr = ErrEmptyResponse
	}
	if gerr != nil {
		fail(classifyFailure(gerr), "failed", out.Billable || out.Usage.Total() > 0, out)
		return
	}
	if v, err := s.ports.Moderation.Check(ctx, ModerationRequest{UserID: p.UserID, Direction: dirOutput, Text: out.Content, Ephemeral: true}); err != nil {
		s.log.Warn("conversation_moderation_failed", logErr(err))
	} else if v.Blocked {
		if _, err := s.ports.Billing.Settle(context.WithoutCancel(ctx), res, SettleRequest{UserID: p.UserID, RunID: runID, Task: taskChat, Usage: out.Usage, Route: out.Route}); err != nil {
			s.log.Warn("conversation_temp_settle_failed", logErr(err))
		}
		emit(evModerationBlocked, map[string]any{"eventID": v.EventID, "direction": dirOutput, "categories": orEmptyStrings(v.Categories)})
		audit("blocked", map[string]any{"direction": dirOutput})
		return
	}
	if _, err := s.ports.Billing.Settle(context.WithoutCancel(ctx), res, SettleRequest{UserID: p.UserID, RunID: runID, Task: taskChat, Usage: out.Usage, Route: out.Route}); err != nil {
		f := classifyFailure(err)
		emit(evError, errorEvent(f, requestID, nil))
		audit("billing_failed", nil)
		return
	}
	now := s.now()
	platform := out.Route.PlatformModelName
	if platform == "" {
		platform = model
	}
	var effortPtr *string
	if effort != "" {
		effortPtr = &effort
	}
	clientRun := strings.TrimSpace(req.ClientRunID)
	// The pair carries the same identity fields a stored send would: public
	// ids, the caller's run id and user id, and the routed model names.
	asst := &MessageDTO{
		PublicID: newPublicID(), RunID: clientRun, UserID: p.UserID,
		Role: roleAssistant, ContentType: "markdown", Content: out.Content, BranchReason: "", Status: msgSuccess,
		TokenUsage: out.Usage.Total(), InputTokens: out.Usage.Input, OutputTokens: out.Usage.Output,
		CacheReadTokens: out.Usage.CacheRead, CacheWriteTokens: out.Usage.CacheWrite, ReasoningTokens: out.Usage.Reasoning,
		ReasoningEffort: effortPtr, LatencyMS: latencyMS(started, now, out.TotalLatencyMS), Attachments: "",
		PlatformModelName: platform, UpstreamModelName: out.Route.UpstreamModelName, ModelVendor: out.Route.ModelVendor,
		ModelIcon: out.Route.ModelIcon, CreatedAt: now, UpdatedAt: now,
	}
	if ks := out.KnowledgeSources; len(ks) > 0 {
		asst.KnowledgeSources = ks
	}
	user := &MessageDTO{
		PublicID: newPublicID(), RunID: clientRun, UserID: p.UserID,
		Role: roleUser, ContentType: "text", Content: last.Content, BranchReason: "", Status: msgSuccess, Attachments: "",
		PlatformModelName: platform, UpstreamModelName: out.Route.UpstreamModelName, CreatedAt: now, UpdatedAt: now,
	}
	// The completed event of a temporary chat carries no sequence number.
	w.write([]byte(marshalNoEscape(map[string]any{"type": evCompleted, "data": SendResult{UserMessage: user, AssistantMessage: asst}})))
	audit("completed", map[string]any{"output_tokens": out.Usage.Output})
}

var _ = time.Second
