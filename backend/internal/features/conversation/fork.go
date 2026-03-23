package conversation

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const maxForkDepth = 2000

// loadConversationMessages returns every non-deleted message of a conversation.
func (s *service) loadConversationMessages(ctx context.Context, convID uint) ([]Message, error) {
	var rows []Message
	err := s.db.WithContext(ctx).Where("conversation_id = ?", convID).Order("id ASC").Find(&rows).Error
	return rows, err
}

// rootToLeaf returns the chain from the root to target using the preloaded map.
func rootToLeaf(byID map[uint]Message, target Message) ([]Message, bool) {
	chain := []Message{target}
	seen := map[uint]bool{target.ID: true}
	cur := target
	for cur.ParentMessageID != nil {
		p, ok := byID[*cur.ParentMessageID]
		if !ok || seen[p.ID] || len(chain) >= maxForkDepth {
			return nil, false
		}
		seen[p.ID] = true
		chain = append(chain, p)
		cur = p
	}
	reverseMessages(chain)
	return chain, true
}

// ForkResult carries the new conversation and what it was forked from.
type ForkResult struct {
	Conversation *ConversationDTO
	SourceID     string
	MessageID    string
	CopiedCount  int
}

func (s *service) forkConversation(ctx context.Context, userID uint, convPublicID, msgPublicID string) (*ForkResult, error) {
	src, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	msgPublicID = strings.TrimSpace(msgPublicID)
	if msgPublicID == "" {
		return nil, ErrMessageInvalidID
	}
	var target Message
	if err := s.db.WithContext(ctx).Where("public_id = ? AND conversation_id = ? AND user_id = ?", msgPublicID, src.ID, userID).First(&target).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrMessageNotFound
		}
		return nil, err
	}
	if target.Role != roleAssistant {
		return nil, ErrForkTarget
	}
	if target.Status == msgPending {
		return nil, ErrForkState
	}
	all, err := s.loadConversationMessages(ctx, src.ID)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]Message, len(all))
	for _, m := range all {
		byID[m.ID] = m
	}
	chain, ok := rootToLeaf(byID, target)
	if !ok {
		return nil, ErrForkHistory
	}

	var created Conversation
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Hold the source row so concurrent edits cannot change the chain.
		var lock Conversation
		if err := lockRows(tx).First(&lock, src.ID).Error; err != nil {
			return ErrConversationNotFound
		}
		created = Conversation{
			UserID: userID, ProjectID: src.ProjectID, PublicID: newPublicID(), Title: src.Title, LabelsJSON: labelsOrEmpty(src.LabelsJSON),
			LabelsManuallyManaged: src.LabelsManuallyManaged, ModelName: src.ModelName, Provider: src.Provider,
			SessionKey: "sess_" + newPublicID(), Status: statusActive, MessageCount: len(chain),
			ContextPolicy: s.contextPolicyJSON(ctx),
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		idMap := map[uint]uint{}
		pubMap := map[uint]string{}
		for _, old := range chain {
			status := old.Status
			switch status {
			case msgPending:
				status = msgInterrupted
			case "":
				status = msgSuccess
			}
			nm := Message{
				ConversationID: created.ID, UserID: userID, PublicID: newPublicID(), Role: old.Role, ContentType: old.ContentType,
				Content: old.Content, ReasoningContent: old.ReasoningContent, BranchReason: branchDefault,
				TokenUsage: old.TokenUsage, InputTokens: old.InputTokens, OutputTokens: old.OutputTokens,
				CacheReadTokens: old.CacheReadTokens, CacheWriteTokens: old.CacheWriteTokens, ReasoningTokens: old.ReasoningTokens,
				LatencyMS: old.LatencyMS, ReasoningEffort: old.ReasoningEffort, BilledCurrency: "USD", BilledNanoUSD: 0,
				PricingSnapshot: "", Status: status, ErrorCode: old.ErrorCode, ErrorMessage: old.ErrorMessage,
				ModerationEventID: old.ModerationEventID, ModerationCategoriesJSON: old.ModerationCategoriesJSON,
				KnowledgeSourcesJSON: old.KnowledgeSourcesJSON, EditedAt: old.EditedAt,
			}
			if old.ParentMessageID != nil {
				p := idMap[*old.ParentMessageID]
				nm.ParentMessageID = &p
			}
			if err := tx.Create(&nm).Error; err != nil {
				return err
			}
			idMap[old.ID] = nm.ID
			pubMap[old.ID] = nm.PublicID

			var atts []Attachment
			if err := tx.Where("message_id = ? AND status <> ?", old.ID, fileStatusDeleted).Order("id ASC").Find(&atts).Error; err != nil {
				return err
			}
			for _, a := range atts {
				na := a
				na.ID, na.MessageID, na.ConversationID = 0, nm.ID, created.ID
				if err := tx.Create(&na).Error; err != nil {
					return err
				}
			}
			var states []UIState
			if err := tx.Where("message_id = ?", old.ID).Find(&states).Error; err != nil {
				return err
			}
			for _, st := range states {
				if err := tx.Create(&UIState{UserID: userID, ConversationID: created.ID, MessageID: nm.ID, BlockID: st.BlockID, StateJSON: st.StateJSON}).Error; err != nil {
					return err
				}
			}
			if err := cloneDisplayTrace(tx, traceKey(old), "msg_"+nm.PublicID, scrubOwner); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	dto, err := s.conversationDTO(ctx, &created)
	if err != nil {
		return nil, err
	}
	return &ForkResult{Conversation: dto, SourceID: src.PublicID, MessageID: target.PublicID, CopiedCount: len(chain)}, nil
}

// cloneDisplayTrace copies the display trace rows (blocks and events) of one
// trace key to another, removing the fields the mode calls internal from every
// payload.
func cloneDisplayTrace(tx *gorm.DB, fromKey, toKey string, mode scrubMode) error {
	var rows []RunEvent
	if err := tx.Where("run_id = ? AND event_scope IN ?", fromKey, []string{scopeTraceBlock, scopeTraceEvent}).Find(&rows).Error; err != nil {
		return err
	}
	for _, r := range rows {
		n := r
		n.ID, n.RunID = 0, toKey
		n.PayloadJSON = sanitizePayloadFor(r.PayloadJSON, mode)
		n.ToolCallID, n.MCPServerSnapshot, n.InputJSON, n.OutputJSON, n.ErrorJSON = "", "", "", "", ""
		if r.EventID == blockPrompt {
			n.PayloadJSON = r.PayloadJSON
		}
		if err := tx.Create(&n).Error; err != nil {
			return err
		}
	}
	return nil
}
