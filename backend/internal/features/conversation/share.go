package conversation

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

func shareMessageCount(sh Share) int { return len(parseStringList(sh.MessageIDsJSON)) }

func toShareDTO(sh *Share) *ShareDTO {
	if sh == nil {
		return &ShareDTO{Status: shareNone}
	}
	return &ShareDTO{
		ShareID: sh.ShareID, Status: sh.Status, TitleSnapshot: sh.TitleSnapshot, ModelSnapshot: sh.ModelSnapshot,
		MessageCount: shareMessageCount(*sh), CreatedAt: sh.CreatedAt, UpdatedAt: sh.UpdatedAt,
		RevokedAt: sh.RevokedAt, LastAccessedAt: sh.LastAccessedAt,
	}
}

func (s *service) latestShare(ctx context.Context, convID uint) (*Share, error) {
	var sh Share
	err := s.db.WithContext(ctx).Where("conversation_id = ?", convID).Order("id DESC").First(&sh).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &sh, nil
}

func (s *service) getShare(ctx context.Context, userID uint, convPublicID string) (*ShareDTO, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	sh, err := s.latestShare(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	return toShareDTO(sh), nil
}

func (s *service) createShare(ctx context.Context, userID uint, convPublicID string, defaultIDs []string, regenerate bool) (*ShareDTO, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	msgs, err := s.loadConversationMessages(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	if len(msgs) == 0 {
		return nil, ErrShareInvalid
	}
	all := make([]string, 0, len(msgs))
	member := map[string]bool{}
	for _, m := range msgs {
		all = append(all, m.PublicID)
		member[m.PublicID] = true
	}
	var def []string
	for _, id := range defaultIDs {
		if t := strings.TrimSpace(id); t != "" {
			def = append(def, t)
		}
	}
	def = dedupeStrings(def)
	for _, id := range def {
		if !member[id] {
			return nil, ErrShareInvalid
		}
	}
	if len(def) == 0 {
		def = latestVisibleBranch(msgs)
	}
	now := s.now()
	sh := Share{
		ShareID: newPublicID(), ConversationID: conv.ID, UserID: userID, Status: shareActive,
		TitleSnapshot: conv.Title, ModelSnapshot: conv.ModelName,
		MessageIDsJSON: marshalNoEscape(all), DefaultMessageIDsJSON: marshalNoEscape(def),
	}
	if regenerate {
		sh.RegeneratedAt = &now
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Share{}).Where("conversation_id = ? AND status = ?", conv.ID, shareActive).
			Updates(map[string]any{"status": shareRevoked, "revoked_at": now}).Error; err != nil {
			return err
		}
		return tx.Create(&sh).Error
	})
	if err != nil {
		return nil, err
	}
	return toShareDTO(&sh), nil
}

func (s *service) revokeShare(ctx context.Context, userID uint, convPublicID string) (*ShareDTO, error) {
	conv, err := s.getConversation(ctx, userID, convPublicID)
	if err != nil {
		return nil, err
	}
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&Share{}).Where("conversation_id = ? AND status = ?", conv.ID, shareActive).
		Updates(map[string]any{"status": shareRevoked, "revoked_at": now}).Error; err != nil {
		return nil, err
	}
	sh, err := s.latestShare(ctx, conv.ID)
	if err != nil {
		return nil, err
	}
	return toShareDTO(sh), nil
}

func (s *service) revokeShares(ctx context.Context, userID uint, ids []string) error {
	var clean []string
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			clean = append(clean, t)
		}
	}
	clean = dedupeStrings(clean)
	if len(clean) == 0 {
		return nil
	}
	var convs []Conversation
	if err := s.db.WithContext(ctx).Select("id", "public_id").Where("user_id = ? AND public_id IN ?", userID, clean).Find(&convs).Error; err != nil {
		return err
	}
	if len(convs) != len(clean) {
		return ErrConversationNotFound
	}
	convIDs := make([]uint, 0, len(convs))
	for _, c := range convs {
		convIDs = append(convIDs, c.ID)
	}
	return s.db.WithContext(ctx).Model(&Share{}).Where("conversation_id IN ? AND status = ?", convIDs, shareActive).
		Updates(map[string]any{"status": shareRevoked, "revoked_at": s.now()}).Error
}

// ----- public reads -----

// SharedMessage is the reduced message shape of public share pages.
type SharedMessage struct {
	PublicID          string          `json:"publicID"`
	ParentPublicID    string          `json:"parentPublicID"`
	SourcePublicID    string          `json:"sourcePublicID"`
	RunID             string          `json:"runID"`
	Role              string          `json:"role"`
	ContentType       string          `json:"contentType"`
	Content           string          `json:"content"`
	BranchReason      string          `json:"branchReason"`
	TokenUsage        int64           `json:"tokenUsage"`
	InputTokens       int64           `json:"inputTokens"`
	OutputTokens      int64           `json:"outputTokens"`
	CacheReadTokens   int64           `json:"cacheReadTokens"`
	CacheWriteTokens  int64           `json:"cacheWriteTokens"`
	ReasoningTokens   int64           `json:"reasoningTokens"`
	ReasoningEffort   *string         `json:"reasoningEffort"`
	LatencyMS         int64           `json:"latencyMS"`
	Status            string          `json:"status"`
	ErrorCode         string          `json:"errorCode"`
	ErrorMessage      string          `json:"errorMessage"`
	Attachments       string          `json:"attachments"`
	PlatformModelName string          `json:"platformModelName"`
	UpstreamModelName string          `json:"upstreamModelName"`
	ModelVendor       string          `json:"modelVendor"`
	ModelIcon         string          `json:"modelIcon"`
	ProcessTrace      *ProcessTrace   `json:"processTrace,omitempty"`
	Moderation        *ModerationInfo `json:"moderation,omitempty"`
	EditedAt          *time.Time      `json:"editedAt"`
	CreatedAt         time.Time       `json:"createdAt"`
	UpdatedAt         time.Time       `json:"updatedAt"`
}

// SharedConversation is the public share page payload.
type SharedConversation struct {
	ShareID                 string          `json:"shareID"`
	Title                   string          `json:"title"`
	Model                   string          `json:"model"`
	CreatedAt               time.Time       `json:"createdAt"`
	LastAccessedAt          *time.Time      `json:"lastAccessedAt"`
	DefaultMessagePublicIDs []string        `json:"defaultMessagePublicIDs"`
	Messages                []SharedMessage `json:"messages"`
	UIComponentsEnabled     bool            `json:"uiComponentsEnabled"`
}

func (s *service) activeShare(ctx context.Context, shareID string) (*Share, *Conversation, error) {
	shareID = strings.TrimSpace(shareID)
	if shareID == "" {
		return nil, nil, ErrShareInvalidID
	}
	var sh Share
	err := s.db.WithContext(ctx).Where("share_id = ? AND status = ?", shareID, shareActive).First(&sh).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil, ErrShareNotFound
		}
		return nil, nil, err
	}
	var conv Conversation
	if err := s.db.WithContext(ctx).First(&conv, sh.ConversationID).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, nil, ErrShareNotFound
		}
		return nil, nil, err
	}
	return &sh, &conv, nil
}

func (s *service) snapshotMessages(ctx context.Context, sh *Share) ([]Message, error) {
	ids := parseStringList(sh.MessageIDsJSON)
	if len(ids) == 0 {
		return nil, nil
	}
	var out []Message
	for _, part := range chunk(ids, 500) {
		var rows []Message
		if err := s.db.WithContext(ctx).Where("conversation_id = ? AND public_id IN ?", sh.ConversationID, part).Find(&rows).Error; err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (s *service) readSharedConversation(ctx context.Context, shareID string) (*SharedConversation, error) {
	sh, conv, err := s.activeShare(ctx, shareID)
	if err != nil {
		return nil, err
	}
	msgs, err := s.snapshotMessages(ctx, sh)
	if err != nil {
		return nil, err
	}
	dtos, err := s.enrich(ctx, msgs, enrichOpts{userID: sh.UserID, conv: conv, public: true})
	if err != nil {
		return nil, err
	}
	out := &SharedConversation{
		ShareID: sh.ShareID, Title: sh.TitleSnapshot, Model: sh.ModelSnapshot, CreatedAt: sh.CreatedAt,
		Messages: make([]SharedMessage, 0, len(dtos)), UIComponentsEnabled: s.settings(ctx).UIComponentsEnabled,
	}
	if out.Title == "" {
		out.Title = conv.Title
	}
	if out.Model == "" {
		out.Model = conv.ModelName
	}
	present := map[string]bool{}
	for _, d := range dtos {
		present[d.PublicID] = true
		content := d.Content
		if d.Status == msgBlocked {
			content = ""
		}
		out.Messages = append(out.Messages, SharedMessage{
			PublicID: d.PublicID, ParentPublicID: d.ParentPublicID, SourcePublicID: d.SourcePublicID, RunID: d.RunID,
			Role: d.Role, ContentType: d.ContentType, Content: content, BranchReason: d.BranchReason,
			TokenUsage: d.TokenUsage, InputTokens: d.InputTokens, OutputTokens: d.OutputTokens,
			CacheReadTokens: d.CacheReadTokens, CacheWriteTokens: d.CacheWriteTokens, ReasoningTokens: d.ReasoningTokens,
			ReasoningEffort: d.ReasoningEffort, LatencyMS: d.LatencyMS, Status: d.Status, ErrorCode: d.ErrorCode,
			ErrorMessage: d.ErrorMessage, Attachments: d.Attachments, PlatformModelName: d.PlatformModelName,
			UpstreamModelName: d.UpstreamModelName, ModelVendor: d.ModelVendor, ModelIcon: d.ModelIcon,
			ProcessTrace: d.ProcessTrace, Moderation: d.Moderation, EditedAt: d.EditedAt, CreatedAt: d.CreatedAt, UpdatedAt: d.UpdatedAt,
		})
	}
	var def []string
	for _, id := range parseStringList(sh.DefaultMessageIDsJSON) {
		if present[id] {
			def = append(def, id)
		}
	}
	if len(def) == 0 {
		def = latestVisibleBranch(msgs)
	}
	out.DefaultMessagePublicIDs = def
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&Share{}).Where("id = ?", sh.ID).UpdateColumn("last_accessed_at", now).Error; err != nil {
		s.log.Warn("conversation_share_access_update_failed", logErr(err))
	}
	out.LastAccessedAt = &now
	return out, nil
}
