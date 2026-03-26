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
