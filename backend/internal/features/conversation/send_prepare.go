package conversation

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// sendUIState is one advisory interactive-block state in a send request.
type sendUIState struct {
	MessagePublicID string         `json:"messagePublicID" binding:"max=64"`
	BlockID         string         `json:"blockID" binding:"max=128"`
	Title           string         `json:"title" binding:"max=200"`
	State           map[string]any `json:"state"`
}

// sendRequest is the body of the send and stream endpoints.
type sendRequest struct {
	ContentType           string         `json:"contentType" binding:"required,oneof=text markdown image file mixed"`
	Content               string         `json:"content" binding:"required"`
	Model                 string         `json:"model" binding:"max=128"`
	Options               map[string]any `json:"options"`
	Controls              map[string]any `json:"controls"`
	ClientRunID           string         `json:"clientRunID" binding:"max=64"`
	FileIDs               []string       `json:"fileIDs" binding:"max=20,dive,max=128"`
	SelectedToolIDs       []uint         `json:"selectedToolIDs" binding:"max=128"`
	SkillIDs              []uint         `json:"skillIDs" binding:"max=128"`
	KnowledgeBaseIDs      []string       `json:"knowledgeBaseIDs" binding:"max=8,dive,required,max=32"`
	HTMLVisualPrompt      bool           `json:"htmlVisualPrompt"`
	ParentMessagePublicID string         `json:"parentMessagePublicID" binding:"max=32"`
	SourceMessagePublicID string         `json:"sourceMessagePublicID" binding:"max=32"`
	BranchReason          string         `json:"branchReason" binding:"omitempty,oneof=default retry edit"`
	UIComponentIDs        []uint         `json:"uiComponentIDs" binding:"max=32"`
	UIBlockVersion        int            `json:"uiBlockVersion" binding:"omitempty,oneof=1 2"`
	UIStates              []sendUIState  `json:"uiStates" binding:"max=32,dive"`
}

// mediaRequest is the body of the media generation endpoints.
type mediaRequest struct {
	Prompt                string         `json:"prompt" binding:"required"`
	Model                 string         `json:"model" binding:"max=128"`
	Options               map[string]any `json:"options"`
	ClientRunID           string         `json:"clientRunID" binding:"max=64"`
	FileIDs               []string       `json:"fileIDs" binding:"max=20,dive,max=128"`
	MaskFileID            string         `json:"maskFileID" binding:"max=128"`
	SourceVideoFileID     string         `json:"-"`
	ParentMessagePublicID string         `json:"parentMessagePublicID" binding:"max=32"`
	SourceMessagePublicID string         `json:"sourceMessagePublicID" binding:"max=32"`
	BranchReason          string         `json:"branchReason" binding:"omitempty,oneof=default retry edit"`
}

// mediaExtensionRequest is the body of the video extension endpoint, which adds
// a mandatory source video to the common media fields.
type mediaExtensionRequest struct {
	Prompt                string         `json:"prompt" binding:"required"`
	Model                 string         `json:"model" binding:"max=128"`
	Options               map[string]any `json:"options"`
	ClientRunID           string         `json:"clientRunID" binding:"max=64"`
	FileIDs               []string       `json:"fileIDs" binding:"max=20,dive,max=128"`
	SourceVideoFileID     string         `json:"sourceVideoFileID" binding:"required,max=128"`
	ParentMessagePublicID string         `json:"parentMessagePublicID" binding:"max=32"`
	SourceMessagePublicID string         `json:"sourceMessagePublicID" binding:"max=32"`
	BranchReason          string         `json:"branchReason" binding:"omitempty,oneof=default retry edit"`
}

func (r mediaExtensionRequest) common() mediaRequest {
	return mediaRequest{
		Prompt: r.Prompt, Model: r.Model, Options: r.Options, ClientRunID: r.ClientRunID, FileIDs: r.FileIDs,
		SourceVideoFileID: r.SourceVideoFileID, ParentMessagePublicID: r.ParentMessagePublicID,
		SourceMessagePublicID: r.SourceMessagePublicID, BranchReason: r.BranchReason,
	}
}

var reservedOptionKeys = map[string]bool{
	"contents": true, "instructions": true, "input": true, "messages": true, "model": true,
	"prompt": true, "stream": true, "system": true, "systemInstruction": true,
}

var validEfforts = map[string]bool{
	"none": true, "minimal": true, "low": true, "medium": true, "high": true, "xhigh": true, "max": true, "ultra": true,
}

const (
	maxControls      = 64
	maxControlKeyLen = 64
	maxControlValLen = 128
)

// sanitizeOptions strips reserved keys. Ordinary users keep only the native
// tools selection and the reasoning-effort option.
func sanitizeOptions(in map[string]any, isAdmin bool) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if reservedOptionKeys[k] {
			continue
		}
		if !isAdmin {
			switch k {
			case "nativeTools", "native_tools", "reasoningEffort", "reasoning_effort":
			default:
				continue
			}
		}
		out[k] = v
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// stripReservedOptions removes only the reserved keys; media generation
// options (size, quality, duration ...) are honoured for every user.
func stripReservedOptions(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		if !reservedOptionKeys[k] {
			out[k] = v
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// sanitizeControls keeps scalar entries with acceptable keys.
func sanitizeControls(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		key := strings.TrimSpace(k)
		if key == "" || utf8.RuneCountInString(key) > maxControlKeyLen {
			continue
		}
		switch x := v.(type) {
		case string:
			if utf8.RuneCountInString(x) > maxControlValLen {
				continue
			}
			out[key] = x
		case bool:
			out[key] = x
		case float64:
			out[key] = x
		default:
			continue
		}
		if len(out) >= maxControls {
			break
		}
	}
	return out
}

// reasoningEffortOf reads the reasoning effort from controls or options.
func reasoningEffortOf(controls, options map[string]any) string {
	for _, src := range []map[string]any{controls, options} {
		for _, k := range []string{"reasoning", "reasoningEffort", "reasoning_effort"} {
			if v, ok := src[k].(string); ok && validEfforts[strings.ToLower(strings.TrimSpace(v))] {
				return strings.ToLower(strings.TrimSpace(v))
			}
		}
	}
	return ""
}

// preparedRequest is a validated, normalised send request.
type preparedRequest struct {
	ContentType     string
	Content         string
	Model           string
	Options         map[string]any
	Controls        map[string]any
	RunID           string
	FileIDs         []string
	ToolIDs         []uint
	SkillIDs        []uint
	KBIDs           []string
	HTMLVisual      bool
	ParentPub       string
	SourcePub       string
	Reason          string
	UIComponentIDs  []uint
	UIBlockVersion  int
	UIStates        []sendUIState
	ReasoningEffort string
	Media           *MediaRequest
	// InStream makes some rejections persist an error pair, so the stream can
	// answer them as an in-stream error event.
	InStream bool
}

func (s *service) normalizeSend(ctx context.Context, req sendRequest, isAdmin bool) (preparedRequest, error) {
	rs := s.settings(ctx)
	p := preparedRequest{
		ContentType: req.ContentType, Content: req.Content, Model: strings.TrimSpace(req.Model),
		RunID: normalizeRunID(req.ClientRunID), HTMLVisual: req.HTMLVisualPrompt,
		ParentPub: strings.TrimSpace(req.ParentMessagePublicID), SourcePub: strings.TrimSpace(req.SourceMessagePublicID),
		Reason: strings.TrimSpace(req.BranchReason), UIComponentIDs: req.UIComponentIDs, UIBlockVersion: req.UIBlockVersion,
		UIStates: req.UIStates,
	}
	if p.UIBlockVersion != 2 {
		p.UIBlockVersion = 1
	}
	p.Options = sanitizeOptions(req.Options, isAdmin)
	p.Controls = sanitizeControls(req.Controls)
	p.ReasoningEffort = reasoningEffortOf(p.Controls, p.Options)
	if p.ReasoningEffort != "" {
		if _, ok := p.Controls["reasoning"]; !ok {
			p.Controls["reasoning"] = p.ReasoningEffort
		}
	}
	if len(req.SelectedToolIDs) > rs.MaxSelectedTools {
		return p, ErrTooManyTools
	}
	if len(req.SkillIDs) > rs.MaxSelectedTools {
		return p, ErrTooManySkills
	}
	p.ToolIDs = dedupeUints(req.SelectedToolIDs, false)
	p.SkillIDs = dedupeUints(req.SkillIDs, true)
	for _, id := range req.KnowledgeBaseIDs {
		t := strings.TrimSpace(id)
		if t == "" {
			return p, ErrInvalidKBReference
		}
		p.KBIDs = append(p.KBIDs, t)
	}
	p.KBIDs = dedupeStrings(p.KBIDs)
	if len(p.KBIDs) > 0 && !rs.KnowledgeBaseEnabled {
		return p, ErrInvalidKBReference
	}
	for _, f := range req.FileIDs {
		p.FileIDs = append(p.FileIDs, strings.TrimSpace(f))
	}
	return p, nil
}

// validateRefs checks tool, skill and knowledge base references with their owners.
// Tools are validated separately (validateTools), after the budget check.
func (s *service) validateRefs(ctx context.Context, userID uint, p preparedRequest) error {
	if len(p.SkillIDs) > 0 {
		if err := s.ports.Capabilities.ValidateSkills(ctx, userID, p.SkillIDs); err != nil {
			return referenceError(err)
		}
	}
	if len(p.KBIDs) > 0 {
		// Without a working retrieval backend no base can be used, whether
		// or not the ids exist.
		if !s.settings(ctx).RAGAvailable {
			return ErrKBUnavailable
		}
		if err := s.ports.Capabilities.ValidateKnowledgeBases(ctx, userID, p.KBIDs); err != nil {
			return referenceError(err)
		}
	}
	return nil
}

// validateTools checks the selected tool references. It runs once the budget
// is reserved, so an account without funds hears about that first.
func (s *service) validateTools(ctx context.Context, userID uint, p preparedRequest) error {
	if len(p.ToolIDs) == 0 {
		return nil
	}
	if err := s.ports.Capabilities.ValidateMCPTools(ctx, userID, p.ToolIDs); err != nil {
		return referenceError(err)
	}
	return nil
}

// referenceError gives a typed rejection of a client supplied reference that
// carries no status of its own the usual 400.
func referenceError(err error) error {
	if te, ok := apperr.Find(err); ok && te.Status == 0 {
		return te.WithStatus(http.StatusBadRequest)
	}
	return err
}

// resolveFiles loads and checks the files referenced by a message.
func (s *service) resolveFiles(ctx context.Context, userID uint, ids []string) ([]FileObject, error) {
	rs := s.settings(ctx)
	if len(ids) == 0 {
		return nil, nil
	}
	for _, id := range ids {
		if id == "" {
			return nil, ErrFileInvalidRef
		}
	}
	// The same file named twice attaches once.
	ids = dedupeStrings(ids)
	if len(ids) > rs.MaxMessageFiles {
		return nil, ErrTooManyFiles
	}
	out := make([]FileObject, 0, len(ids))
	for _, id := range ids {
		f, err := s.getFile(ctx, userID, id)
		if err != nil {
			if errors.Is(err, ErrFileNotFound) || errors.Is(err, ErrFileInvalidID) {
				return nil, ErrFileInvalidRef
			}
			return nil, err
		}
		if !f.ProcessingReady {
			return nil, ErrFileNotReady
		}
		if f.ExtractStatus == "ready" && estimateTokens(f.ExtractText) > rs.FullContextMaxTokens && !rs.RAGAvailable {
			return nil, ErrFileTooLargeCtx
		}
		out = append(out, *f)
	}
	return out, nil
}

// branchPlan says where the new messages attach.
type branchPlan struct {
	Reason     string
	UserParent *Message
	Source     *Message
	ReuseUser  *Message
}

func (s *service) branchMessage(ctx context.Context, convID, userID uint, publicID string) (*Message, error) {
	var m Message
	err := s.db.WithContext(ctx).Where("public_id = ? AND conversation_id = ? AND user_id = ?", publicID, convID, userID).First(&m).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrInvalidBranch
		}
		return nil, err
	}
	return &m, nil
}

func (s *service) resolveBranch(ctx context.Context, conv *Conversation, userID uint, p preparedRequest) (*branchPlan, error) {
	reason := p.Reason
	if reason == "" {
		reason = branchDefault
	}
	var parent, source *Message
	var err error
	if p.ParentPub != "" {
		if parent, err = s.branchMessage(ctx, conv.ID, userID, p.ParentPub); err != nil {
			return nil, err
		}
	}
	if p.SourcePub != "" {
		if source, err = s.branchMessage(ctx, conv.ID, userID, p.SourcePub); err != nil {
			return nil, err
		}
	}
	plan := &branchPlan{Reason: reason}
	if source != nil {
		if reason != branchRetry && reason != branchEdit {
			return nil, ErrInvalidBranch
		}
		if parent != nil && (source.ParentMessageID == nil || *source.ParentMessageID != parent.ID) {
			return nil, ErrInvalidBranch
		}
		if source.ParentMessageID == nil && parent != nil {
			return nil, ErrInvalidBranch
		}
		plan.Source = source
		switch source.Role {
		case roleUser:
			if source.ParentMessageID != nil {
				pm, err := s.messageByID(ctx, conv.ID, *source.ParentMessageID)
				if err != nil {
					return nil, ErrInvalidBranch
				}
				plan.UserParent = pm
			}
		case roleAssistant:
			if reason != branchRetry || source.ParentMessageID == nil {
				return nil, ErrInvalidBranch
			}
			um, err := s.messageByID(ctx, conv.ID, *source.ParentMessageID)
			if err != nil || um.Role != roleUser {
				return nil, ErrInvalidBranch
			}
			plan.ReuseUser = um
		default:
			return nil, ErrInvalidBranch
		}
		return plan, nil
	}
	if reason != branchDefault {
		return nil, ErrInvalidBranch
	}
	if parent != nil {
		plan.UserParent = parent
		return plan, nil
	}
	plan.UserParent, err = s.defaultParent(ctx, conv.ID)
	return plan, err
}

func (s *service) messageByID(ctx context.Context, convID, id uint) (*Message, error) {
	var m Message
	if err := s.db.WithContext(ctx).Where("id = ? AND conversation_id = ?", id, convID).First(&m).Error; err != nil {
		return nil, err
	}
	return &m, nil
}

// defaultParent picks the newest context-usable message: the latest assistant
// message that finished (or was interrupted), else the latest successful user or
// system message, else none (a new root).
func (s *service) defaultParent(ctx context.Context, convID uint) (*Message, error) {
	var m Message
	err := s.db.WithContext(ctx).Where("conversation_id = ? AND role = ? AND status IN ?", convID, roleAssistant, []string{msgSuccess, msgInterrupted}).
		Order("id DESC").First(&m).Error
	if err == nil {
		return &m, nil
	}
	if !database.IsNotFound(err) {
		return nil, err
	}
	err = s.db.WithContext(ctx).Where("conversation_id = ? AND role IN ? AND status = ?", convID, []string{roleUser, roleSystem}, msgSuccess).
		Order("id DESC").First(&m).Error
	if err == nil {
		return &m, nil
	}
	if database.IsNotFound(err) {
		return nil, nil
	}
	return nil, err
}

// pendingSend is everything a generation needs once its rows exist.
type pendingSend struct {
	conv        Conversation
	userID      uint
	isAdmin     bool
	requestID   string
	req         preparedRequest
	plan        *branchPlan
	files       []FileObject
	model       string
	task        string
	reservation Reservation
	userMsg     Message
	asstMsg     Message
	created     int
	hint        string
}

// prepareSend validates a send request, reserves budget and creates the
// message pair and run row. Errors are ordinary request errors.
func (s *service) prepareSend(ctx context.Context, userID uint, isAdmin bool, convPublicID, requestID string, req preparedRequest) (*pendingSend, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	if err := s.validateRefs(ctx, userID, req); err != nil {
		if req.InStream && errors.Is(err, ErrKBUnavailable) {
			return s.rejectedPair(ctx, conv, userID, isAdmin, requestID, req, err), err
		}
		return nil, err
	}
	files, err := s.resolveFiles(ctx, userID, req.FileIDs)
	if err != nil {
		return nil, err
	}
	plan, err := s.resolveBranch(ctx, conv, userID, req)
	if err != nil {
		return nil, err
	}
	model := req.Model
	if model == "" {
		model = conv.ModelName
	}
	task := taskChat
	if req.Media != nil {
		task = req.Media.Kind
	}
	if task == taskChat && model != "" && !s.ports.Catalog.ChatModelAvailable(ctx, userID, model) {
		if pc, ok := s.ports.Catalog.(PricingCatalog); ok && pc.ModelUnpriced(ctx, userID, model) {
			return nil, ErrPricingRequired
		}
		if rc, ok := s.ports.Catalog.(RouteCatalog); ok && req.InStream && !rc.ModelRoutable(ctx, model) {
			return s.rejectedPair(ctx, conv, userID, isAdmin, requestID, req, ErrModelRouteMissing), ErrModelRouteMissing
		}
		return nil, ErrModelAccessDenied
	}
	ps := &pendingSend{conv: *conv, userID: userID, isAdmin: isAdmin, requestID: requestID, req: req, plan: plan, files: files, model: model, task: task}

	res, rerr := s.ports.Billing.Reserve(ctx, ReserveRequest{UserID: userID, ConversationID: conv.ID, Model: model, Task: task, RunID: req.RunID})
	if rerr != nil {
		// Persist the rejection as an error pair so the conversation shows it.
		f := classifyFailure(rerr)
		if _, perr := s.createPair(ctx, ps, true, &f); perr != nil {
			if apperr.Registered(ErrRunAlreadyExists.Code) && perr == ErrRunAlreadyExists {
				return nil, perr
			}
			s.log.Warn("conversation_rejection_persist_failed", logErr(perr))
		}
		return nil, rerr
	}
	if err := s.validateTools(ctx, userID, req); err != nil {
		s.ports.Billing.Release(ctx, res)
		return nil, err
	}
	ps.reservation = res
	if _, err := s.createPair(ctx, ps, false, nil); err != nil {
		s.ports.Billing.Release(ctx, res)
		return nil, err
	}
	return ps, nil
}

// rejectedPair persists the error pair of a send that was refused before any
// generation started. It returns nil when the pair could not be stored.
func (s *service) rejectedPair(ctx context.Context, conv *Conversation, userID uint, isAdmin bool, requestID string, req preparedRequest, cause error) *pendingSend {
	plan, err := s.resolveBranch(ctx, conv, userID, req)
	if err != nil {
		return nil
	}
	model := req.Model
	if model == "" {
		model = conv.ModelName
	}
	ps := &pendingSend{conv: *conv, userID: userID, isAdmin: isAdmin, requestID: requestID, req: req, plan: plan, model: model, task: taskChat}
	f := classifyFailure(cause)
	if _, err := s.createPair(ctx, ps, true, &f); err != nil {
		return nil
	}
	return ps
}

// createPair inserts the run, the messages and the attachment rows.
func (s *service) createPair(ctx context.Context, ps *pendingSend, rejected bool, fail *failure) (*pendingSend, error) {
	now := s.now()
	status := msgPending
	runStatus := runRunning
	if rejected {
		status, runStatus = msgError, runError
	}
	reuse := ps.plan.ReuseUser != nil
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var parentID *uint
		if ps.plan.UserParent != nil {
			var n int64
			if err := tx.Model(&Message{}).Where("id = ? AND conversation_id = ?", ps.plan.UserParent.ID, ps.conv.ID).Count(&n).Error; err != nil {
				return err
			}
			if n == 0 {
				return ErrParentDeleted
			}
			id := ps.plan.UserParent.ID
			parentID = &id
		}
		var effort *string
		if ps.req.ReasoningEffort != "" {
			e := ps.req.ReasoningEffort
			effort = &e
		}
		run := Run{
			RunID: ps.req.RunID, RequestID: ps.requestID, UserID: ps.userID, ConversationID: ps.conv.ID, TaskType: ps.task,
			Provider: "internal", RequestedModelName: ps.model, ReasoningEffort: effort,
			Status: runStatus, ModerationState: "not_required", StartedAt: now,
		}
		if rejected {
			run.EndedAt = &now
			run.ErrorCode, run.ErrorMessage = fail.Code, truncateRunes(fail.Message, 255)
		}
		if err := tx.Create(&run).Error; err != nil {
			if database.IsDuplicate(database.Translate(err)) {
				return ErrRunAlreadyExists
			}
			return err
		}
		if reuse {
			ps.userMsg = *ps.plan.ReuseUser
		} else {
			um := Message{
				ConversationID: ps.conv.ID, UserID: ps.userID, PublicID: newPublicID(), ParentMessageID: parentID,
				RunID: ps.req.RunID, Role: roleUser, ContentType: ps.req.ContentType, Content: ps.req.Content,
				BranchReason: ps.plan.Reason, BilledCurrency: "USD", Status: status, ReasoningEffort: effort,
			}
			if ps.plan.Source != nil {
				um.SourceMessageID = &ps.plan.Source.ID
			}
			if rejected {
				um.ErrorCode, um.ErrorMessage = fail.Code, truncateRunes(fail.Message, 255)
			}
			if err := tx.Create(&um).Error; err != nil {
				return err
			}
			ps.userMsg = um
			for _, f := range ps.files {
				if err := tx.Create(&Attachment{
					ConversationID: ps.conv.ID, MessageID: um.ID, UserID: ps.userID, FileID: f.FileID, Kind: attachmentKind(f.FileCategory),
					FileName: f.FileName, MimeType: f.DetectedMIME, FileSize: f.SizeBytes, SHA256: f.SHA256,
					StoragePath: f.StoragePath, Status: "active", UploadedAt: &f.CreatedAt,
				}).Error; err != nil {
					return err
				}
			}
		}
		umID := ps.userMsg.ID
		am := Message{
			ConversationID: ps.conv.ID, UserID: ps.userID, PublicID: newPublicID(), ParentMessageID: &umID,
			RunID: ps.req.RunID, Role: roleAssistant, ContentType: "text", BranchReason: branchDefault,
			BilledCurrency: "USD", Status: status, ReasoningEffort: effort,
		}
		if ps.plan.Source != nil {
			// A branch makes both new messages carry its reason.
			am.BranchReason = ps.plan.Reason
		}
		if reuse {
			am.SourceMessageID = &ps.plan.Source.ID
		}
		if rejected {
			am.ErrorCode, am.ErrorMessage = fail.Code, truncateRunes(fail.Message, 255)
		}
		if err := tx.Create(&am).Error; err != nil {
			return err
		}
		ps.asstMsg = am
		ps.created = 2
		if reuse {
			ps.created = 1
		}
		upd := map[string]any{"message_count": gorm.Expr("message_count + ?", ps.created), "updated_at": now}
		return tx.Model(&Conversation{}).Where("id = ?", ps.conv.ID).Updates(upd).Error
	})
	if err != nil {
		return nil, err
	}
	ps.conv.MessageCount += ps.created
	if !rejected {
		s.applyFallbackTitle(ctx, ps)
	}
	return ps, nil
}

// applyFallbackTitle stores the immediate title for placeholder titles and
// decides the metadata refresh hint.
func (s *service) applyFallbackTitle(ctx context.Context, ps *pendingSend) {
	first := s.firstUserContent(ctx, ps)
	titleCand := isPlaceholderTitle(ps.conv.Title)
	if titleCand {
		if t := fallbackTitle(first); t != "" {
			if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", ps.conv.ID).UpdateColumn("title", t).Error; err == nil {
				ps.conv.Title = t
			}
		}
	}
	switch {
	case strings.TrimSpace(first) == "":
		ps.hint = "skipped_no_titleable_content"
	default:
		wantTitle := s.userFlag(ctx, ps.userID, "chat.auto_generate_title") && s.isReplaceableTitle(ps.conv.Title, first)
		wantLabels := s.userFlag(ctx, ps.userID, "chat.auto_generate_labels") && !ps.conv.LabelsManuallyManaged && len(parseStringList(ps.conv.LabelsJSON)) == 0
		if wantTitle || wantLabels {
			ps.hint = "pending"
		} else {
			ps.hint = "not_needed"
		}
	}
}

func (s *service) isReplaceableTitle(title, firstContent string) bool {
	return isPlaceholderTitle(title) || (strings.TrimSpace(title) != "" && title == fallbackTitle(firstContent))
}

// firstUserContent returns the content of the conversation's first user turn.
func (s *service) firstUserContent(ctx context.Context, ps *pendingSend) string {
	var m Message
	err := s.db.WithContext(ctx).Where("conversation_id = ? AND role = ? AND TRIM(content) <> ''", ps.conv.ID, roleUser).Order("id ASC").First(&m).Error
	if err != nil {
		return ps.req.Content
	}
	return m.Content
}

var _ = json.Marshal
