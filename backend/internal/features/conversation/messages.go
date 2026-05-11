package conversation

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

type messageListQuery struct {
	Page     pagination.Page
	Tail     bool
	BeforeID *uint
	Summary  bool
}

func (s *service) countMessages(ctx context.Context, convID uint) (int64, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&Message{}).Where("conversation_id = ?", convID).Count(&n).Error
	return n, err
}

func (s *service) listMessages(ctx context.Context, userID uint, convPublicID string, q messageListQuery) (int64, []MessageDTO, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return 0, nil, err
	}
	total, err := s.countMessages(ctx, conv.ID)
	if err != nil {
		return 0, nil, err
	}
	base := s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID)
	var rows []Message
	switch {
	case q.BeforeID != nil:
		if err := base.Where("id < ?", *q.BeforeID).Order("id DESC").Limit(q.Page.Limit()).Find(&rows).Error; err != nil {
			return 0, nil, err
		}
		reverseMessages(rows)
	case q.Tail:
		if err := base.Order("id DESC").Limit(q.Page.Limit()).Find(&rows).Error; err != nil {
			return 0, nil, err
		}
		reverseMessages(rows)
	default:
		if err := base.Order("id ASC").Scopes(pagination.Scope(q.Page)).Find(&rows).Error; err != nil {
			return 0, nil, err
		}
	}
	dtos, err := s.enrich(ctx, rows, enrichOpts{userID: userID, conv: conv, summaryTrace: q.Summary})
	return total, dtos, err
}

func reverseMessages(m []Message) {
	for i, j := 0, len(m)-1; i < j; i, j = i+1, j-1 {
		m[i], m[j] = m[j], m[i]
	}
}

// PreviewItem is one entry of the conversation preview.
type PreviewItem struct {
	PublicID     string `json:"publicID"`
	Role         string `json:"role"`
	Content      string `json:"content"`
	ErrorMessage string `json:"errorMessage"`
}

func (s *service) previewMessages(ctx context.Context, userID uint, convPublicID string) ([]PreviewItem, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	var newest Message
	err = s.db.WithContext(ctx).Where("conversation_id = ?", conv.ID).Order("id DESC").First(&newest).Error
	if err != nil {
		if database.IsNotFound(err) {
			return []PreviewItem{}, nil
		}
		return nil, err
	}
	chain, err := s.ancestorChain(ctx, conv.ID, &newest, 100)
	if err != nil {
		return nil, err
	}
	var usable []Message
	for _, m := range chain {
		if m.Role == roleUser || m.Role == roleAssistant {
			usable = append(usable, m)
		}
	}
	if len(usable) > 10 {
		usable = usable[len(usable)-10:]
	}
	out := make([]PreviewItem, 0, len(usable))
	for _, m := range usable {
		content := m.Content
		if m.Status == msgBlocked && m.Role == roleAssistant {
			content = ""
		}
		out = append(out, PreviewItem{PublicID: m.PublicID, Role: m.Role, Content: content, ErrorMessage: m.ErrorMessage})
	}
	return out, nil
}

// ancestorChain returns the messages from the root down to start (inclusive),
// walking at most limit parents. Deleted parents end the walk.
func (s *service) ancestorChain(ctx context.Context, convID uint, start *Message, limit int) ([]Message, error) {
	chain := []Message{*start}
	seen := map[uint]bool{start.ID: true}
	cur := start
	for cur.ParentMessageID != nil && len(chain) < limit {
		var p Message
		err := s.db.WithContext(ctx).Where("id = ? AND conversation_id = ?", *cur.ParentMessageID, convID).First(&p).Error
		if err != nil {
			if database.IsNotFound(err) {
				break
			}
			return nil, err
		}
		if seen[p.ID] {
			break
		}
		seen[p.ID] = true
		chain = append(chain, p)
		cur = &chain[len(chain)-1]
	}
	reverseMessages(chain)
	return chain, nil
}

// ----- runs -----

func (s *service) listRuns(ctx context.Context, userID uint, convPublicID string, p pagination.Page) ([]RunDTO, int64, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, 0, err
	}
	q := s.db.WithContext(ctx).Model(&Run{}).Where("conversation_id = ? AND user_id = ?", conv.ID, userID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Run
	if err := q.Order("id DESC").Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	out := make([]RunDTO, 0, len(rows))
	for _, r := range rows {
		out = append(out, toRunDTO(r))
	}
	return out, total, nil
}

// RunStatus is one entry of the run status batch.
type RunStatus struct {
	RunID  string `json:"runID"`
	Status string `json:"status"`
}

func (s *service) runStatuses(ctx context.Context, userID uint, ids []string) ([]RunStatus, error) {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			clean = append(clean, t)
		}
	}
	clean = dedupeStrings(clean)
	out := []RunStatus{}
	if len(clean) == 0 {
		return out, nil
	}
	var rows []Run
	if err := s.db.WithContext(ctx).Select("run_id", "status").Where("user_id = ? AND run_id IN ?", userID, clean).Find(&rows).Error; err != nil {
		return nil, err
	}
	by := map[string]string{}
	for _, r := range rows {
		by[r.RunID] = r.Status
	}
	for _, id := range clean {
		if st, ok := by[id]; ok {
			out = append(out, RunStatus{RunID: id, Status: st})
		}
	}
	return out, nil
}

// ----- edit / trace / feedback -----

func (s *service) editMessage(ctx context.Context, userID uint, publicID, content string) (*MessageDTO, error) {
	m, err := s.getMessage(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	content = strings.TrimSpace(content)
	if content == "" {
		return nil, ErrInvalidContent
	}
	if m.Role != roleUser && m.Role != roleAssistant {
		return nil, ErrEditTargetInvalid
	}
	if m.Status == msgPending {
		return nil, ErrEditStateInvalid
	}
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&Message{}).Where("id = ?", m.ID).Updates(map[string]any{"content": content, "edited_at": now}).Error; err != nil {
		return nil, err
	}
	m.Content, m.EditedAt = content, &now
	m.UpdatedAt = now
	conv, err := s.conversationByID(ctx, m.ConversationID)
	if err != nil {
		return nil, err
	}
	return s.enrichOne(ctx, *m, enrichOpts{userID: userID, conv: conv})
}

func (s *service) conversationByID(ctx context.Context, id uint) (*Conversation, error) {
	var c Conversation
	if err := s.db.WithContext(ctx).First(&c, id).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrConversationNotFound
		}
		return nil, err
	}
	return &c, nil
}

func (s *service) messageTrace(ctx context.Context, userID uint, publicID string) (*ProcessTrace, error) {
	m, err := s.getMessage(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	conv, err := s.conversationByID(ctx, m.ConversationID)
	if err != nil {
		return nil, err
	}
	d, err := s.enrichOne(ctx, *m, enrichOpts{userID: userID, conv: conv})
	if err != nil {
		return nil, err
	}
	return d.ProcessTrace, nil
}

// FeedbackResult is the answer to a feedback update.
type FeedbackResult struct {
	MessageID       uint   `json:"messageID"`
	MessagePublicID string `json:"messagePublicID"`
	MyFeedback      string `json:"myFeedback"`
	ThumbsUpCount   int64  `json:"thumbsUpCount"`
	ThumbsDownCount int64  `json:"thumbsDownCount"`
}

func (s *service) setFeedback(ctx context.Context, userID uint, publicID, feedback string) (*FeedbackResult, error) {
	m, err := s.getMessage(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	fb := strings.ToLower(strings.TrimSpace(feedback))
	if fb != "" && fb != "up" && fb != "down" {
		return nil, ErrInvalidFeedback
	}
	if m.Role != roleAssistant {
		return nil, ErrFeedbackTarget
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if fb == "" {
			return tx.Unscoped().Where("user_id = ? AND message_id = ?", userID, m.ID).Delete(&Feedback{}).Error
		}
		var cur Feedback
		e := tx.Unscoped().Where("user_id = ? AND message_id = ?", userID, m.ID).First(&cur).Error
		if e == nil {
			return tx.Unscoped().Model(&Feedback{}).Where("id = ?", cur.ID).Updates(map[string]any{"feedback": fb, "deleted_at": nil}).Error
		}
		if !database.IsNotFound(e) {
			return e
		}
		return tx.Create(&Feedback{UserID: userID, ConversationID: m.ConversationID, MessageID: m.ID, Feedback: fb}).Error
	})
	if err != nil {
		return nil, err
	}
	res := &FeedbackResult{MessageID: m.ID, MessagePublicID: m.PublicID, MyFeedback: fb}
	for _, kind := range []string{"up", "down"} {
		var n int64
		if err := s.db.WithContext(ctx).Model(&Feedback{}).Where("message_id = ? AND feedback = ?", m.ID, kind).Count(&n).Error; err != nil {
			return nil, err
		}
		if kind == "up" {
			res.ThumbsUpCount = n
		} else {
			res.ThumbsDownCount = n
		}
	}
	return res, nil
}

// ----- delete -----

// DeleteMessageResult is the answer to a message delete.
type DeleteMessageResult struct {
	Deleted                bool  `json:"deleted"`
	ReparentedMessageCount int64 `json:"reparentedMessageCount"`
}

func (s *service) deleteMessage(ctx context.Context, userID uint, convPublicID, msgPublicID string) (*DeleteMessageResult, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	msgPublicID = strings.TrimSpace(msgPublicID)
	if msgPublicID == "" {
		return nil, ErrMessageInvalidID
	}
	var result DeleteMessageResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m Message
		e := lockRows(tx).Where("public_id = ? AND conversation_id = ? AND user_id = ?", msgPublicID, conv.ID, userID).First(&m).Error
		if e != nil {
			if database.IsNotFound(e) {
				return ErrMessageNotFound
			}
			return e
		}
		if m.Role != roleUser && m.Role != roleAssistant {
			return ErrDeleteTarget
		}
		if m.Status == msgPending {
			return ErrDeleteState
		}
		if m.ParentMessageID == nil {
			return ErrDeleteRoot
		}
		subtree, err := subtreeIDs(tx, conv.ID, m.ID)
		if err != nil {
			return err
		}
		res := tx.Model(&Message{}).Where("conversation_id = ? AND parent_message_id = ?", conv.ID, m.ID).
			UpdateColumn("parent_message_id", *m.ParentMessageID)
		if res.Error != nil {
			return res.Error
		}
		result.ReparentedMessageCount = res.RowsAffected
		if err := tx.Delete(&Message{}, m.ID).Error; err != nil {
			return err
		}
		if err := tx.Where("message_id = ?", m.ID).Delete(&UIState{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&Attachment{}).Where("message_id = ?", m.ID).UpdateColumn("status", fileStatusDeleted).Error; err != nil {
			return err
		}
		if err := tx.Model(&Conversation{}).Where("id = ? AND message_count > 0", conv.ID).
			UpdateColumn("message_count", gorm.Expr("message_count - 1")).Error; err != nil {
			return err
		}
		if err := tx.Where("conversation_id = ? AND record_type = ? AND covered_until_message_id IN ?", conv.ID, recordSnapshot, subtree).
			Delete(&ContextRecord{}).Error; err != nil {
			return err
		}
		result.Deleted = true
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// subtreeIDs returns root and all its non-deleted descendants.
func subtreeIDs(tx *gorm.DB, convID, root uint) ([]uint, error) {
	var rows []Message
	if err := tx.Select("id", "parent_message_id").Where("conversation_id = ?", convID).Find(&rows).Error; err != nil {
		return nil, err
	}
	children := map[uint][]uint{}
	for _, r := range rows {
		if r.ParentMessageID != nil {
			children[*r.ParentMessageID] = append(children[*r.ParentMessageID], r.ID)
		}
	}
	out := []uint{root}
	seen := map[uint]bool{root: true}
	for i := 0; i < len(out); i++ {
		for _, c := range children[out[i]] {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
	}
	return out, nil
}

// ----- UI state -----

const (
	maxUIBlocksPerRequest = 32
	maxUIBlocksPerMessage = 32
	maxUIStateBytes       = 4096
	maxUIStateDepth       = 4
	maxUIKeyBytes         = 64
)

var uiIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validUIKey(k string) bool {
	return len(k) >= 1 && len(k) <= maxUIKeyBytes && uiIDPattern.MatchString(k)
}

// jsonDepth returns the nesting depth where a scalar is level 1; ok is false if
// any object key is invalid.
func jsonDepth(v any) (int, bool) {
	switch x := v.(type) {
	case map[string]any:
		max := 0
		for k, val := range x {
			if !validUIKey(k) {
				return 0, false
			}
			d, ok := jsonDepth(val)
			if !ok {
				return 0, false
			}
			if d > max {
				max = d
			}
		}
		return max + 1, true
	case []any:
		max := 0
		for _, val := range x {
			d, ok := jsonDepth(val)
			if !ok {
				return 0, false
			}
			if d > max {
				max = d
			}
		}
		return max + 1, true
	default:
		return 1, true
	}
}

// normalizeUIState validates one block state. clear is true when the state
// removes the block.
func normalizeUIState(raw json.RawMessage) (compact string, clear bool, err error) {
	t := strings.TrimSpace(string(raw))
	if t == "" || t == "null" || t == "{}" {
		return "", true, nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return "", false, ErrInvalidUIState
	}
	obj, ok := v.(map[string]any)
	if !ok {
		return "", false, ErrInvalidUIState
	}
	if len(obj) == 0 {
		return "", true, nil
	}
	depth, ok := jsonDepth(v)
	if !ok || depth > maxUIStateDepth {
		return "", false, ErrInvalidUIState
	}
	c := marshalNoEscape(v)
	if len(c) > maxUIStateBytes {
		return "", false, ErrInvalidUIState
	}
	return c, false, nil
}

// UIStateResult is the answer to a UI state update.
type UIStateResult struct {
	MessageID       uint                       `json:"messageID"`
	MessagePublicID string                     `json:"messagePublicID"`
	UIState         map[string]json.RawMessage `json:"uiState"`
}

func (s *service) putUIState(ctx context.Context, userID uint, convPublicID, msgPublicID string, blocks map[string]json.RawMessage) (*UIStateResult, error) {
	if len(blocks) == 0 || len(blocks) > maxUIBlocksPerRequest {
		return nil, ErrInvalidUIState
	}
	normalized := map[string]string{}
	clears := map[string]bool{}
	for id, raw := range blocks {
		if !validUIKey(id) {
			return nil, ErrInvalidUIState
		}
		c, clr, err := normalizeUIState(raw)
		if err != nil {
			return nil, err
		}
		if clr {
			clears[id] = true
		} else {
			normalized[id] = c
		}
	}
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	msgPublicID = strings.TrimSpace(msgPublicID)
	if msgPublicID == "" {
		return nil, ErrMessageInvalidID
	}
	var result UIStateResult
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var m Message
		e := lockRows(tx).Where("public_id = ? AND conversation_id = ? AND user_id = ?", msgPublicID, conv.ID, userID).First(&m).Error
		if e != nil {
			if database.IsNotFound(e) {
				return ErrMessageNotFound
			}
			return e
		}
		if m.Role != roleAssistant || m.Status == msgPending {
			return ErrUIStateTarget
		}
		var existing []UIState
		if err := tx.Where("message_id = ?", m.ID).Find(&existing).Error; err != nil {
			return err
		}
		have := map[string]UIState{}
		for _, r := range existing {
			have[r.BlockID] = r
		}
		for id := range clears {
			if r, ok := have[id]; ok {
				if err := tx.Delete(&UIState{}, r.ID).Error; err != nil {
					return err
				}
				delete(have, id)
			}
		}
		for id, c := range normalized {
			if r, ok := have[id]; ok {
				if err := tx.Model(&UIState{}).Where("id = ?", r.ID).Update("state_json", c).Error; err != nil {
					return err
				}
				r.StateJSON = c
				have[id] = r
				continue
			}
			if len(have) >= maxUIBlocksPerMessage {
				return ErrInvalidUIState
			}
			row := UIState{UserID: userID, ConversationID: conv.ID, MessageID: m.ID, BlockID: id, StateJSON: c}
			if err := tx.Create(&row).Error; err != nil {
				return err
			}
			have[id] = row
		}
		result.MessageID, result.MessagePublicID = m.ID, m.PublicID
		result.UIState = map[string]json.RawMessage{}
		for id, r := range have {
			if c, ok := compactJSON([]byte(r.StateJSON)); ok && c != "{}" && c != "" {
				result.UIState[id] = json.RawMessage(c)
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// ----- artifacts and tool calls -----

// ArtifactDTO is a context artifact.
type ArtifactDTO struct {
	ID            uint    `json:"id"`
	MessageID     uint    `json:"messageID"`
	RunID         string  `json:"runID"`
	Kind          string  `json:"kind"`
	SourceType    string  `json:"sourceType"`
	SourceID      string  `json:"sourceID"`
	SourceTitle   string  `json:"sourceTitle"`
	Content       string  `json:"content"`
	TokenEstimate int     `json:"tokenEstimate"`
	Score         float64 `json:"score"`
	MetadataJSON  string  `json:"metadataJSON"`
	ExpiresAt     *string `json:"expiresAt,omitempty"`
	CreatedAt     string  `json:"createdAt"`
}

func (s *service) getArtifact(ctx context.Context, userID uint, id uint) (*ArtifactDTO, error) {
	var r ContextRecord
	err := s.db.WithContext(ctx).Where("id = ? AND user_id = ? AND record_type = ?", id, userID, recordArtifact).First(&r).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrArtifactNotFound
		}
		return nil, err
	}
	if r.ExpiresAt != nil && !r.ExpiresAt.After(s.now()) {
		return nil, ErrArtifactNotFound
	}
	return &ArtifactDTO{
		ID: r.ID, MessageID: r.MessageID, RunID: r.RunID, Kind: r.Kind, SourceType: r.SourceType, SourceID: r.SourceID,
		SourceTitle: r.SourceTitle, Content: r.Content, TokenEstimate: r.TokenEstimate, Score: r.Score,
		MetadataJSON: r.MetadataJSON, ExpiresAt: rfc(r.ExpiresAt), CreatedAt: *rfc(&r.CreatedAt),
	}, nil
}

// ToolCallDetail is the stored detail of one tool call.
type ToolCallDetail struct {
	RunID           string `json:"runID"`
	ToolCallID      string `json:"toolCallID"`
	ToolName        string `json:"toolName"`
	Status          string `json:"status"`
	OutputJSON      string `json:"outputJSON"`
	OutputSizeBytes int    `json:"outputSizeBytes"`
	OutputOmitted   bool   `json:"outputOmitted"`
	ErrorJSON       string `json:"errorJSON"`
	ErrorSizeBytes  int    `json:"errorSizeBytes"`
	ErrorOmitted    bool   `json:"errorOmitted"`
}

func (s *service) toolCallDetail(ctx context.Context, userID uint, runID, toolCallID string) (*ToolCallDetail, error) {
	var run Run
	err := s.db.WithContext(ctx).Where("run_id = ? AND user_id = ?", runID, userID).First(&run).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrToolCallNotFound
		}
		return nil, err
	}
	var ev RunEvent
	err = s.db.WithContext(ctx).Where("run_id = ? AND event_scope = ? AND tool_call_id = ?", runID, scopeToolCall, toolCallID).First(&ev).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrToolCallNotFound
		}
		return nil, err
	}
	d := &ToolCallDetail{RunID: runID, ToolCallID: toolCallID, ToolName: ev.ToolName, Status: ev.Status}
	d.OutputSizeBytes = len(ev.OutputJSON)
	if d.OutputSizeBytes > toolPayloadMaxBytes {
		d.OutputOmitted = true
	} else {
		d.OutputJSON = sanitizePayload(ev.OutputJSON, false)
	}
	d.ErrorSizeBytes = len(ev.ErrorJSON)
	if d.ErrorSizeBytes > toolPayloadMaxBytes {
		d.ErrorOmitted = true
	} else {
		d.ErrorJSON = sanitizePayload(ev.ErrorJSON, false)
	}
	return d, nil
}
