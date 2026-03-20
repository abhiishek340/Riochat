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
