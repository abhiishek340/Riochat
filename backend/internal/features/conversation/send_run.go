package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// failure is the classified outcome of a failed generation.
type failure struct {
	Status   int
	Code     string
	Message  string
	Canceled bool
	// Detail is the text stored on the failed messages when it differs from
	// the public message; Broken marks a response stream that died midway.
	Detail string
	Broken bool
}

// failureWording is implemented by causes that explain an upstream failure.
type failureWording interface {
	FailureDetail() string
	StreamBroken() bool
}

// stored is the error text kept on the messages and the run.
func (f failure) stored() string {
	if f.Detail != "" {
		return f.Detail
	}
	return f.Message
}

var codeStatus = map[string]int{
	"upstream.unavailable":                http.StatusBadGateway,
	"llm.empty_response":                  http.StatusBadGateway,
	"upstream.rate_limited":               http.StatusTooManyRequests,
	"media.artifact_unavailable":          http.StatusBadGateway,
	"media.image_stream_unsupported":      http.StatusBadGateway,
	"tool_run.final_answer_missing":       http.StatusBadGateway,
	"mcp.image_processing_failed":         http.StatusBadGateway,
	"llm.model_route_not_configured":      http.StatusServiceUnavailable,
	"service.unavailable":                 http.StatusServiceUnavailable,
	"conversation_run.stream_interrupted": http.StatusServiceUnavailable,
}

// classifyFailure maps an error to a status, a stable code and a public message.
func classifyFailure(err error) failure {
	if errors.Is(err, context.Canceled) {
		return failure{Status: http.StatusBadRequest, Code: ErrRunCanceled.Code, Message: ErrRunCanceled.Message, Canceled: true}
	}
	if te, ok := apperr.Find(err); ok && apperr.Registered(te.Code) {
		status := te.Status
		if status == 0 {
			status = codeStatus[te.Code]
		}
		if status == 0 {
			status = http.StatusInternalServerError
		}
		if te.Code == ErrRunCanceled.Code {
			return failure{Status: status, Code: te.Code, Message: te.Message, Canceled: true}
		}
		out := failure{Status: status, Code: te.Code, Message: te.Message}
		var fw failureWording
		if errors.As(err, &fw) {
			out.Detail, out.Broken = fw.FailureDetail(), fw.StreamBroken()
		}
		return out
	}
	return failure{Status: http.StatusInternalServerError, Code: apperr.ErrInternal.Code, Message: apperr.ErrInternal.Message}
}

func (f failure) asError() error {
	e := apperr.New(f.Code, f.Message).WithStatus(f.Status)
	return e
}

// execOutcome is what a finished job reports.
type execOutcome struct {
	Result   *SendResult
	Fail     *failure
	Billable bool
	Blocked  bool
}

// newLiveRun builds the in-memory state of a pending send.
func newLiveRun(ps *pendingSend) *liveRun {
	return &liveRun{
		runID: ps.req.RunID, userID: ps.userID, convPublicID: ps.conv.PublicID, requestID: ps.requestID,
		userMsgPublicID: ps.userMsg.PublicID, asstMsgPublicID: ps.asstMsg.PublicID,
	}
}

// startRun registers the run and executes it detached from the request.
func (s *service) startRun(reqCtx context.Context, ps *pendingSend) (*liveRun, <-chan execOutcome) {
	lr := newLiveRun(ps)
	rctx, cancel := context.WithCancel(reqctx.Detach(reqCtx))
	lr.cancel = cancel
	s.hub.register(lr)
	out := make(chan execOutcome, 1)
	s.lc.Detach(reqCtx, "conversation-lease", 0, func(context.Context) { s.hub.leaseLoop(rctx, lr) })
	s.lc.Detach(reqCtx, "conversation-run", 0, func(context.Context) {
		defer cancel()
		var outcome execOutcome
		func() {
			defer func() {
				if r := recover(); r != nil {
					s.log.Error("conversation_run_panic")
					f := failure{Status: http.StatusInternalServerError, Code: apperr.ErrInternal.Code, Message: apperr.ErrInternal.Message}
					outcome = s.finishFailure(context.WithoutCancel(rctx), ps, lr, f, GenerateResult{}, false, time.Time{})
				}
			}()
			outcome = s.runJob(rctx, ps, lr)
		}()
		s.hub.finish(lr)
		out <- outcome
	})
	return lr, out
}

// historyMessages builds the model context for the new user message.
func (s *service) buildHistory(ctx context.Context, ps *pendingSend) ([]PromptMessage, map[string]bool, error) {
	rs := s.settings(ctx)
	chain, err := s.ancestorChain(ctx, ps.conv.ID, &ps.userMsg, 10000)
	if err != nil {
		return nil, nil, err
	}
	total := 0
	onBranch := map[string]bool{}
	for _, m := range chain {
		total += len(m.Content) + len(m.ReasoningContent)
		onBranch[m.PublicID] = true
	}
	if total > 64<<20 {
		return nil, nil, ErrContextTooLarge
	}
	passback := false
	if v, ok, err := s.ports.Settings.UserSetting(ctx, ps.userID, "chat.reasoning_content_passback"); err == nil && ok && strings.EqualFold(strings.TrimSpace(v), "true") {
		passback = true
	}
	var out []PromptMessage
	for i, m := range chain {
		if m.ID == ps.userMsg.ID {
			continue
		}
		if strings.TrimSpace(m.Content) == "" {
			continue
		}
		usable := false
		switch m.Role {
		case roleUser, roleSystem:
			usable = m.Status == msgSuccess
			// A failed user message counts when its retry succeeded.
			if !usable && m.Role == roleUser && i+1 < len(chain) && chain[i+1].Role == roleAssistant && chain[i+1].Status == msgSuccess {
				usable = true
			}
		case roleAssistant:
			usable = m.Status == msgSuccess || m.Status == msgInterrupted
		}
		if !usable {
			continue
		}
		pm := PromptMessage{Role: m.Role, Content: m.Content}
		if passback && m.Role == roleAssistant {
			pm.Reasoning = m.ReasoningContent
		}
		out = append(out, pm)
	}
	if max := rs.ContextMaxTurns * 2; max > 0 && len(out) > max {
		out = out[len(out)-max:]
	}
	cur := PromptMessage{Role: roleUser, Content: ps.userMsg.Content}
	for _, f := range ps.files {
		cur.Files = append(cur.Files, PromptFile{FileID: f.FileID, FileName: f.FileName, MimeType: f.DetectedMIME, Kind: f.FileCategory, Size: f.SizeBytes, Text: f.ExtractText})
	}
	out = append(out, cur)
	return out, onBranch, nil
}

const uiHintBudget = 16 * 1024

func uiHints(states []sendUIState, onBranch map[string]bool) []UIStateHint {
	var out []UIStateHint
	used := 0
	for _, st := range states {
		if !onBranch[st.MessagePublicID] || st.BlockID == "" || len(st.State) == 0 {
			continue
		}
		raw := marshalNoEscape(st.State)
		if len(raw) > maxUIStateBytes || used+len(raw) > uiHintBudget {
			continue
		}
		used += len(raw)
		out = append(out, UIStateHint{MessagePublicID: st.MessagePublicID, BlockID: st.BlockID, Title: st.Title, State: st.State})
	}
	return out
}

func (s *service) projectSystemPrompt(ctx context.Context, conv *Conversation) string {
	if conv.ProjectID == nil {
		return ""
	}
	var p Project
	if err := s.db.WithContext(ctx).First(&p, *conv.ProjectID).Error; err != nil {
		return ""
	}
	return p.SystemPrompt
}

func (s *service) genRequest(ctx context.Context, ps *pendingSend, history []PromptMessage, onBranch map[string]bool) GenerateRequest {
	rs := s.settings(ctx)
	g := GenerateRequest{
		Task: ps.task, UserID: ps.userID, ConversationPublicID: ps.conv.PublicID, RunID: ps.req.RunID, RequestID: ps.requestID,
		Model: ps.model, System: s.projectSystemPrompt(ctx, &ps.conv), Messages: history, Options: ps.req.Options,
		Controls: ps.req.Controls, ReasoningEffort: ps.req.ReasoningEffort, ToolIDs: ps.req.ToolIDs, SkillIDs: ps.req.SkillIDs,
		KnowledgeBaseIDs: ps.req.KBIDs, HTMLVisualPrompt: ps.req.HTMLVisual, UIComponentIDs: ps.req.UIComponentIDs,
		UIBlockVersion: ps.req.UIBlockVersion, UIStates: uiHints(ps.req.UIStates, onBranch), Media: ps.req.Media,
		MaxLLMCalls: rs.MaxLLMCalls,
	}
	if !rs.UIComponentsEnabled {
		g.UIComponentIDs, g.UIStates = nil, nil
	}
	return g
}

// runJob executes a prepared send: moderation, generation, settlement and
// persistence. It publishes progress and exactly one terminal event.
func (s *service) runJob(ctx context.Context, ps *pendingSend, lr *liveRun) execOutcome {
	dbctx := context.WithoutCancel(ctx)
	started := s.now()
	emit := func(typ string, fields map[string]any) { s.hub.publish(lr, typ, fields) }

	if len(ps.files) > 0 {
		emit("file_proc", map[string]any{"message": "processing attachments"})
	}

	// genCtx lets an input hit stop the upstream generation.
	genCtx, cancelGen := context.WithCancel(ctx)
	defer cancelGen()

	var mod *turnModeration
	sm, tracked := s.ports.Moderation.(SessionModeration)
	if tracked {
		mod = s.beginTurnModeration(dbctx, sm, ps, lr, cancelGen)
		defer mod.close(lr)
	} else if ps.plan.ReuseUser == nil {
		// Without run tracking only the text of new user content is checked.
		v, err := s.ports.Moderation.Check(ctx, ModerationRequest{UserID: ps.userID, ConversationID: ps.conv.ID, Direction: dirInput, Text: ps.req.Content})
		if err != nil {
			s.log.Warn("conversation_moderation_failed", logErr(err))
		} else if v.Blocked {
			return s.finishBlocked(dbctx, ps, lr, v, dirInput, GenerateResult{}, started)
		}
	}
	history, onBranch, err := s.buildHistory(dbctx, ps)
	if err != nil {
		if v, blocked := mod.finish(dbctx, GenerateResult{}); blocked {
			return s.finishBlocked(dbctx, ps, lr, v, v.Direction, GenerateResult{}, started)
		}
		return s.finishFailure(dbctx, ps, lr, classifyFailure(err), GenerateResult{}, false, started)
	}
	kr, kerr := s.knowledgeFor(genCtx, ps, emit)
	if kerr != nil {
		if v, blocked := mod.finish(dbctx, GenerateResult{}); blocked {
			return s.finishBlocked(dbctx, ps, lr, v, v.Direction, GenerateResult{}, started)
		}
		return s.finishFailure(dbctx, ps, lr, classifyFailure(kerr), GenerateResult{}, false, started)
	}
	history = applyKnowledge(history, kr)
	greq := s.genRequest(dbctx, ps, history, onBranch)
	greq.DynamicContext = joinContext(kr.Context, s.historicalEvidence(dbctx, ps, kr.Artifacts))
	res, gerr := s.ports.Generator.Generate(genCtx, greq, func(ev GenerateEvent) {
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
	res = mergeKnowledge(res, kr, s.now())
	if res.Content == "" || gerr != nil {
		// Fall back to what was streamed when the generator returned no text.
		lr.mu.Lock()
		if res.Content == "" {
			res.Content = lr.content.String()
		}
		if res.Reasoning == "" {
			res.Reasoning = lr.think.String()
		}
		lr.mu.Unlock()
	}
	if gerr == nil && ctx.Err() != nil {
		gerr = ctx.Err()
	}
	if gerr == nil && strings.TrimSpace(res.Content) == "" && len(res.Files) == 0 {
		gerr = ErrEmptyResponse
	}
	if mod != nil {
		// Every exit after Begin settles the moderation state. Whatever was
		// produced, even a partial reply, is still checked on the output side.
		if v, blocked := mod.finish(dbctx, res); blocked {
			return s.finishBlocked(dbctx, ps, lr, v, v.Direction, res, started)
		}
	}
	if gerr != nil {
		f := classifyFailure(gerr)
		if f.Canceled {
			lr.mu.Lock()
			byUser := lr.canceledByUser
			lr.mu.Unlock()
			if !byUser {
				f = failure{Status: http.StatusServiceUnavailable, Code: ErrStreamInterrupted.Code, Message: ErrStreamInterrupted.Message}
			}
		}
		return s.finishFailure(dbctx, ps, lr, f, res, res.Billable || res.Usage.Total() > 0, started)
	}
	if !tracked {
		if v, merr := s.ports.Moderation.Check(ctx, ModerationRequest{UserID: ps.userID, ConversationID: ps.conv.ID, Direction: dirOutput, Text: res.Content}); merr != nil {
			s.log.Warn("conversation_moderation_failed", logErr(merr))
		} else if v.Blocked {
			return s.finishBlocked(dbctx, ps, lr, v, dirOutput, res, started)
		}
	}
	return s.finishSuccess(dbctx, ps, lr, res, started)
}

// turnModeration is the moderation tracking of one persisted turn.
type turnModeration struct {
	sess ModerationSession
	done bool
}

// beginTurnModeration starts tracking the run and queues the input checks: the
// user's text and every attached file (images only are really checked; the
// source video of an extension is left out because it cannot be evaluated).
func (s *service) beginTurnModeration(ctx context.Context, sm SessionModeration, ps *pendingSend, lr *liveRun, cancel func()) *turnModeration {
	paid := ps.reservation.Mode != "" && !strings.EqualFold(ps.reservation.Mode, "self")
	sess := sm.Begin(ctx, ModerationBegin{
		UserID: ps.userID, ConversationID: ps.conv.ID, RunID: ps.req.RunID, MessageID: ps.asstMsg.ID,
		MessagePublicID: ps.asstMsg.PublicID, PaidReservation: paid, CancelGeneration: cancel,
	})
	if sess == nil {
		return nil
	}
	lr.mu.Lock()
	lr.holdBlocked = true
	lr.mu.Unlock()
	if ps.plan.ReuseUser == nil {
		sess.CheckInputText(ps.req.Content)
		var files []ModerationFile
		for _, f := range ps.files {
			if ps.req.Media != nil && f.FileID == ps.req.Media.SourceVideoFileID {
				continue
			}
			files = append(files, ModerationFile{ObjectID: f.ID, SHA256: f.SHA256})
		}
		if len(files) > 0 {
			sess.CheckInputImages(files)
		}
	}
	return &turnModeration{sess: sess}
}

// finish settles the moderation of the turn once and reports a block. The
// verdict's billing label follows the live-notification rule: an input-side
// block is labelled only by the settlement snapshot, not on the stream.
func (m *turnModeration) finish(ctx context.Context, res GenerateResult) (ModerationVerdict, bool) {
	if m == nil || m.done {
		return ModerationVerdict{}, false
	}
	m.done = true
	var images []ModerationImage
	for _, f := range res.Files {
		if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(f.MimeType)), "image/") && f.Kind != "image" {
			continue
		}
		if len(f.Data) == 0 {
			m.sess.ReportOutputImageFailure(ctx)
			continue
		}
		images = append(images, ModerationImage{Name: f.FileName, Bytes: f.Data})
	}
	v := m.sess.Finish(ctx, ModerationOutput{Text: res.Content, Reasoning: res.Reasoning, Images: images})
	return v, v.Blocked
}

// close releases the tracking of an abandoned turn and lets the run publish
// its terminal blocked event itself again.
func (m *turnModeration) close(lr *liveRun) {
	if m == nil {
		return
	}
	m.sess.Close()
	lr.mu.Lock()
	lr.holdBlocked = false
	lr.mu.Unlock()
}

func latencyMS(started time.Time, now time.Time, reported int64) int64 {
	if reported > 0 {
		return reported
	}
	return now.Sub(started).Milliseconds()
}

// settle bills the usage; a settlement failure is returned to the caller.
func (s *service) settle(ctx context.Context, ps *pendingSend, res GenerateResult, failed bool) (Settlement, error) {
	return s.ports.Billing.Settle(ctx, ps.reservation, SettleRequest{
		UserID: ps.userID, RunID: ps.req.RunID, Task: ps.task, Usage: res.Usage, Route: res.Route, Failed: failed, LatencyMS: res.TotalLatencyMS,
	})
}

func settlementSnapshot(st Settlement) string {
	if strings.TrimSpace(st.PricingSnapshotJSON) != "" {
		return st.PricingSnapshotJSON
	}
	mode := st.Mode
	if mode == "" {
		mode = "self"
	}
	return marshalNoEscape(map[string]any{"billingMode": mode})
}

func runRouteColumns(res GenerateResult, ps *pendingSend) map[string]any {
	r := res.Route
	platform := r.PlatformModelName
	if platform == "" {
		platform = ps.model
	}
	cols := map[string]any{
		"provider": r.Provider, "provider_protocol": r.ProviderProtocol, "endpoint": r.Endpoint,
		"upstream_id": r.UpstreamID, "upstream_model_id": r.UpstreamModelID, "upstream_name": r.UpstreamName,
		"platform_model_name": platform, "routed_binding_code": r.RoutedBindingCode, "model_vendor": r.ModelVendor,
		"model_icon": r.ModelIcon, "upstream_model_name": r.UpstreamModelName,
		"input_tokens": res.Usage.Input, "output_tokens": res.Usage.Output, "cache_read_tokens": res.Usage.CacheRead,
		"cache_write_tokens": res.Usage.CacheWrite, "reasoning_tokens": res.Usage.Reasoning,
		"tool_calls_count": res.ToolCalls, "first_token_latency_ms": res.FirstTokenLatencyMS,
	}
	if r.RequestedModelName != "" {
		cols["requested_model_name"] = r.RequestedModelName
	}
	// Runs record the gateway as provider, not the model's vendor.
	cols["provider"] = "internal"
	return cols
}

// persistTrace replaces the stored trace rows of a run.
func (s *service) persistTrace(tx *gorm.DB, runID string, td *TraceData) error {
	if td == nil {
		return nil
	}
	if err := tx.Unscoped().Where("run_id = ?", runID).Delete(&RunEvent{}).Error; err != nil {
		return err
	}
	for _, row := range traceRows(runID, td, s.now()) {
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	}
	return nil
}

func (s *service) finishSuccess(ctx context.Context, ps *pendingSend, lr *liveRun, res GenerateResult, started time.Time) execOutcome {
	now := s.now()
	st, serr := s.settle(ctx, ps, res, false)
	if serr != nil {
		// Settlement failed: the caller gets the mapped error, never a plain
		// success carrying the persisted pair.
		out := s.finishFailure(ctx, ps, lr, classifyFailure(serr), res, true, started)
		out.Billable = false
		return out
	}
	sources := "[]"
	if len(res.KnowledgeSources) > 0 {
		sources = marshalNoEscape(res.KnowledgeSources)
	}
	applied := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{
			"content": res.Content, "reasoning_content": res.Reasoning, "status": msgSuccess, "error_code": "", "error_message": "",
			"token_usage": res.Usage.Output, "input_tokens": 0, "output_tokens": res.Usage.Output,
			"cache_read_tokens": res.Usage.CacheRead, "cache_write_tokens": res.Usage.CacheWrite, "reasoning_tokens": res.Usage.Reasoning,
			"latency_ms": latencyMS(started, now, res.TotalLatencyMS), "billed_currency": currencyOr(st.Currency),
			"billed_nanousd": st.Nanousd, "pricing_snapshot": settlementSnapshot(st), "knowledge_sources_json": sources,
		}
		r := tx.Model(&Message{}).Where("id = ? AND status = ?", ps.asstMsg.ID, msgPending).Updates(upd)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return nil
		}
		applied = true
		uu := map[string]any{"status": msgSuccess, "error_code": "", "error_message": ""}
		s.userPromptTokens(uu, ps, res)
		if err := tx.Model(&Message{}).Where("id = ? AND status <> ?", ps.userMsg.ID, msgBlocked).Updates(uu).Error; err != nil {
			return err
		}
		cols := runRouteColumns(res, ps)
		cols["status"], cols["ended_at"] = runSuccess, now
		cols["total_latency_ms"] = latencyMS(started, now, res.TotalLatencyMS)
		if err := tx.Model(&Run{}).Where("run_id = ?", ps.req.RunID).Updates(cols).Error; err != nil {
			return err
		}
		if err := s.persistTrace(tx, ps.req.RunID, res.Trace); err != nil {
			return err
		}
		for _, gf := range res.Files {
			f, err := s.storeGenerated(ctx, tx, ps.userID, gf)
			if err != nil {
				return err
			}
			meta := ""
			if gf.DurationSeconds > 0 {
				meta = marshalNoEscape(map[string]any{"duration_seconds": gf.DurationSeconds})
			}
			if err := tx.Create(&Attachment{
				ConversationID: ps.conv.ID, MessageID: ps.asstMsg.ID, UserID: ps.userID, FileID: f.FileID, Kind: attachmentKind(f.FileCategory),
				FileName: f.FileName, MimeType: f.DetectedMIME, FileSize: f.SizeBytes, SHA256: f.SHA256, StoragePath: f.StoragePath,
				Status: "active", MetaJSON: meta, UploadedAt: &now,
			}).Error; err != nil {
				return err
			}
		}
		return s.updateConversationModel(tx, ps, res, now)
	})
	if err != nil {
		return s.finishFailure(ctx, ps, lr, classifyFailure(err), res, true, started)
	}
	if !applied {
		// The pair was already closed (for example by a lease-loss interruption).
		return s.reportAlreadyClosed(ctx, ps)
	}
	s.persistArtifacts(ctx, ps, res.Trace)
	result, rerr := s.sendResult(ctx, ps)
	if rerr != nil {
		s.log.Warn("conversation_result_load_failed", logErr(rerr))
		f := classifyFailure(rerr)
		s.hub.publish(lr, evError, errorEvent(f, ps.requestID, nil))
		return execOutcome{Fail: &f}
	}
	s.hub.publish(lr, evCompleted, map[string]any{"data": result})
	s.scheduleMetadata(ctx, ps)
	return execOutcome{Result: result}
}

// userPromptTokens charges the prompt to the user message: the upstream's
// reported input count, or an estimate of the text when none arrived. A
// message that is only re-answered keeps its first count.
func (s *service) userPromptTokens(upd map[string]any, ps *pendingSend, res GenerateResult) {
	if ps.plan.ReuseUser != nil {
		return
	}
	n := res.Usage.Input
	if n <= 0 {
		n = int64(estimateTokens(ps.req.Content))
	}
	upd["input_tokens"], upd["token_usage"] = n, n
}

func currencyOr(c string) string {
	if c == "" {
		return "USD"
	}
	return c
}

func (s *service) updateConversationModel(tx *gorm.DB, ps *pendingSend, res GenerateResult, now time.Time) error {
	model := res.Route.PlatformModelName
	if model == "" {
		model = ps.model
	}
	upd := map[string]any{"updated_at": now}
	if model != "" && model != ps.conv.ModelName {
		upd["model"] = model
	}
	if res.ResponseID != "" {
		upd["last_response_id"] = truncateRunes(res.ResponseID, 128)
	}
	return tx.Model(&Conversation{}).Where("id = ?", ps.conv.ID).Updates(upd).Error
}

func errorEvent(f failure, requestID string, data *SendResult) map[string]any {
	ev := map[string]any{"status": f.Status, "message": f.Message, "errorCode": f.Code}
	if requestID != "" {
		ev["requestId"] = requestID
	}
	if data != nil {
		ev["data"] = data
	}
	return ev
}

// reportAlreadyClosed handles a job whose messages were closed elsewhere.
func (s *service) reportAlreadyClosed(ctx context.Context, ps *pendingSend) execOutcome {
	f := failure{Status: http.StatusServiceUnavailable, Code: ErrStreamInterrupted.Code, Message: ErrStreamInterrupted.Message}
	result, _ := s.sendResult(ctx, ps)
	return execOutcome{Result: result, Fail: &f}
}

func (s *service) finishFailure(ctx context.Context, ps *pendingSend, lr *liveRun, f failure, res GenerateResult, billable bool, started time.Time) execOutcome {
	now := s.now()
	asstStatus, runStatus, userStatus := msgError, runError, msgError
	switch {
	case f.Canceled:
		asstStatus, runStatus, userStatus = msgCanceled, runCanceled, msgSuccess
	case f.Code == ErrStreamInterrupted.Code, f.Broken && strings.TrimSpace(res.Content) != "":
		asstStatus, runStatus = msgInterrupted, runInterrupted
	}
	var st Settlement
	if billable {
		var err error
		if st, err = s.settle(ctx, ps, res, true); err != nil {
			s.log.Warn("conversation_failure_settle_failed", logErr(err))
			billable = false
		}
	} else {
		s.ports.Billing.Release(ctx, ps.reservation)
	}
	msg := truncateRunes(f.stored(), 255)
	applied := false
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{"status": asstStatus, "error_code": f.Code, "error_message": msg}
		if strings.TrimSpace(res.Content) != "" {
			upd["content"] = res.Content
		}
		if strings.TrimSpace(res.Reasoning) != "" {
			upd["reasoning_content"] = res.Reasoning
		}
		if billable {
			upd["token_usage"], upd["input_tokens"], upd["output_tokens"] = res.Usage.Output, 0, res.Usage.Output
			upd["cache_read_tokens"], upd["cache_write_tokens"], upd["reasoning_tokens"] = res.Usage.CacheRead, res.Usage.CacheWrite, res.Usage.Reasoning
			upd["billed_currency"], upd["billed_nanousd"], upd["pricing_snapshot"] = currencyOr(st.Currency), st.Nanousd, settlementSnapshot(st)
		}
		upd["latency_ms"] = latencyMS(started, now, res.TotalLatencyMS)
		r := tx.Model(&Message{}).Where("id = ? AND status = ?", ps.asstMsg.ID, msgPending).Updates(upd)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return nil
		}
		applied = true
		uu := map[string]any{"status": userStatus}
		if userStatus == msgError {
			uu["error_code"], uu["error_message"] = f.Code, msg
		}
		s.userPromptTokens(uu, ps, res)
		if err := tx.Model(&Message{}).Where("id = ? AND status IN ?", ps.userMsg.ID, []string{msgPending, msgError}).Updates(uu).Error; err != nil {
			return err
		}
		cols := runRouteColumns(res, ps)
		cols["status"], cols["ended_at"], cols["error_code"], cols["error_message"] = runStatus, now, f.Code, msg
		cols["total_latency_ms"] = latencyMS(started, now, res.TotalLatencyMS)
		if err := tx.Model(&Run{}).Where("run_id = ?", ps.req.RunID).Updates(cols).Error; err != nil {
			return err
		}
		if err := s.persistTrace(tx, ps.req.RunID, res.Trace); err != nil {
			return err
		}
		cu := map[string]any{"updated_at": now}
		if ps.model != "" && ps.model != ps.conv.ModelName {
			cu["model"] = ps.model
		}
		return tx.Model(&Conversation{}).Where("id = ?", ps.conv.ID).Updates(cu).Error
	})
	if err != nil {
		s.log.Error("conversation_failure_persist_failed", logErr(err))
	} else if applied {
		s.persistArtifacts(ctx, ps, res.Trace)
	}
	result, _ := s.sendResult(ctx, ps)
	if result != nil {
		// A failed run starts no title or label generation.
		result.MetadataRefreshHint = ""
	}
	if !applied && err == nil {
		// Someone else already closed the pair; report whatever is stored.
		s.hub.publish(lr, evError, errorEvent(f, ps.requestID, result))
		return execOutcome{Result: result, Fail: &f, Billable: billable}
	}
	s.hub.publish(lr, evError, errorEvent(f, ps.requestID, result))
	return execOutcome{Result: result, Fail: &f, Billable: billable}
}

func (s *service) finishBlocked(ctx context.Context, ps *pendingSend, lr *liveRun, v ModerationVerdict, direction string, res GenerateResult, started time.Time) execOutcome {
	now := s.now()
	var st Settlement
	billed := res.Usage.Total() > 0 || res.Billable
	if billed {
		var err error
		if st, err = s.settle(ctx, ps, res, false); err != nil {
			s.log.Warn("conversation_blocked_settle_failed", logErr(err))
			billed = false
		}
	} else {
		s.ports.Billing.Release(ctx, ps.reservation)
	}
	cats := marshalNoEscape(orEmptyStrings(v.Categories))
	// An input-side block is labelled on the stream only by the ledger
	// snapshot after settlement (whether upstream usage happened depends on
	// timing); an output-side block carries the label immediately.
	liveReason := v.BilledReason
	if direction == dirInput {
		liveReason = ""
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{
			"status": msgBlocked, "content": "", "reasoning_content": "", "content_type": "text",
			"moderation_event_id": v.EventID, "moderation_categories_json": cats,
			"error_code": BlockedErrorCode, "error_message": BlockedErrorMessage,
		}
		if billed {
			upd["token_usage"], upd["input_tokens"], upd["output_tokens"] = res.Usage.Total(), res.Usage.Input, res.Usage.Output
			upd["billed_currency"], upd["billed_nanousd"] = currencyOr(st.Currency), st.Nanousd
			upd["pricing_snapshot"] = snapshotWithReason(settlementSnapshot(st), v.BilledReason)
		}
		// The moderation engine may already have withdrawn the pair; the
		// update is idempotent over pending and blocked rows.
		r := tx.Model(&Message{}).Where("id = ? AND status IN ?", ps.asstMsg.ID, []string{msgPending, msgBlocked}).Updates(upd)
		if r.Error != nil {
			return r.Error
		}
		if r.RowsAffected == 0 {
			return nil
		}
		uu := map[string]any{"status": msgSuccess}
		if direction == dirInput {
			uu = map[string]any{
				"status": msgBlocked, "moderation_event_id": v.EventID, "moderation_categories_json": cats,
				"error_code": BlockedErrorCode, "error_message": BlockedErrorMessage,
			}
		}
		if err := tx.Model(&Message{}).Where("id = ?", ps.userMsg.ID).Updates(uu).Error; err != nil {
			return err
		}
		cols := runRouteColumns(res, ps)
		cols["status"], cols["ended_at"] = runBlocked, now
		cols["moderation_state"], cols["moderation_event_id"], cols["moderation_categories_json"] = "blocked", v.EventID, cats
		cols["total_latency_ms"] = latencyMS(started, now, res.TotalLatencyMS)
		if err := tx.Model(&Run{}).Where("run_id = ?", ps.req.RunID).Updates(cols).Error; err != nil {
			return err
		}
		return tx.Model(&Conversation{}).Where("id = ?", ps.conv.ID).UpdateColumn("updated_at", now).Error
	})
	if err != nil {
		s.log.Error("conversation_blocked_persist_failed", logErr(err))
	}
	result, _ := s.sendResult(ctx, ps)
	ev := map[string]any{"eventID": v.EventID, "direction": direction, "categories": orEmptyStrings(v.Categories), "data": result}
	if liveReason != "" {
		ev["billedReason"] = liveReason
	}
	s.hub.publish(lr, evModerationBlocked, ev)
	return execOutcome{Result: result, Blocked: true, Billable: billed}
}

// snapshotWithReason adds the billed reason to a pricing snapshot object so a
// reload can rebuild the billing note of a blocked message.
func snapshotWithReason(snapshot, reason string) string {
	if reason == "" {
		return snapshot
	}
	var m map[string]any
	if json.Unmarshal([]byte(snapshot), &m) != nil || m == nil {
		m = map[string]any{}
	}
	m["billedReason"] = reason
	return marshalNoEscape(m)
}

func orEmptyStrings(in []string) []string {
	if in == nil {
		return []string{}
	}
	return in
}

// sendResult loads the pair from the database as response objects.
func (s *service) sendResult(ctx context.Context, ps *pendingSend) (*SendResult, error) {
	var msgs []Message
	ids := []uint{ps.userMsg.ID, ps.asstMsg.ID}
	if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&msgs).Error; err != nil {
		return nil, err
	}
	conv, err := s.conversationByID(ctx, ps.conv.ID)
	if err != nil {
		return nil, err
	}
	dtos, err := s.enrich(ctx, msgs, enrichOpts{userID: ps.userID, conv: conv, send: true})
	if err != nil {
		return nil, err
	}
	res := &SendResult{MetadataRefreshHint: ps.hint}
	for i := range dtos {
		d := dtos[i]
		// The vendor label is resolved on later reads, not on the send result.
		d.ModelVendor, d.ModelIcon = "", ""
		if d.ID == ps.userMsg.ID {
			// The send result shows the user message as it was created.
			d.UpdatedAt = d.CreatedAt
			res.UserMessage = &d
		} else {
			res.AssistantMessage = &d
		}
	}
	return res, nil
}
