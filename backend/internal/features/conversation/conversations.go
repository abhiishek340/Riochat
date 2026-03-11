package conversation

import (
	"context"
	"encoding/json"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

const (
	placeholderTitle  = "Untitled chat"
	maxTitleLen       = 255
	maxLabels         = 6
	maxLabelLen       = 24
	fallbackTitleLen  = 15
	generatedTitleMax = 80
	maxSearchQuery    = 200
)

func (s *service) contextPolicyJSON(ctx context.Context) string {
	rs := s.settings(ctx)
	return marshalNoEscape(map[string]any{
		"max_turns":                      rs.ContextMaxTurns,
		"compact_enabled":                rs.ContextCompactionEnabled,
		"context_window_fallback_tokens": rs.ContextWindowFallbackTokens,
		"compact_trigger_percent":        rs.ContextCompactTriggerPct,
		"compact_preserve_recent_turns":  rs.ContextPreserveRecentTurns,
	})
}

// conversationDTOs converts rows, resolving project and share summaries in two
// batched queries.
func (s *service) conversationDTOs(ctx context.Context, rows []Conversation) ([]ConversationDTO, error) {
	projectIDs := map[uint]bool{}
	convIDs := make([]uint, 0, len(rows))
	for _, c := range rows {
		convIDs = append(convIDs, c.ID)
		if c.ProjectID != nil {
			projectIDs[*c.ProjectID] = true
		}
	}
	projects := map[uint]Project{}
	if len(projectIDs) > 0 {
		ids := make([]uint, 0, len(projectIDs))
		for id := range projectIDs {
			ids = append(ids, id)
		}
		var ps []Project
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&ps).Error; err != nil {
			return nil, err
		}
		for _, p := range ps {
			projects[p.ID] = p
		}
	}
	shares := map[uint]Share{}
	if len(convIDs) > 0 {
		var ss []Share
		if err := s.db.WithContext(ctx).Where("conversation_id IN ?", convIDs).Order("id DESC").Find(&ss).Error; err != nil {
			return nil, err
		}
		for _, sh := range ss {
			if _, ok := shares[sh.ConversationID]; !ok {
				shares[sh.ConversationID] = sh
			}
		}
	}
	out := make([]ConversationDTO, 0, len(rows))
	for _, c := range rows {
		var p *Project
		if c.ProjectID != nil {
			if pr, ok := projects[*c.ProjectID]; ok {
				p = &pr
			}
		}
		var sh *Share
		if v, ok := shares[c.ID]; ok {
			sh = &v
		}
		out = append(out, toConversationDTO(c, p, sh))
	}
	return out, nil
}

func toConversationDTO(c Conversation, p *Project, sh *Share) ConversationDTO {
	d := ConversationDTO{
		PublicID: c.PublicID, UserID: c.UserID, Title: c.Title, LabelsJSON: labelsOrEmpty(c.LabelsJSON),
		Model: c.ModelName, Provider: c.Provider, SessionKey: c.SessionKey, IsStarred: c.IsStarred,
		StarredAt: c.StarredAt, MessageCount: c.MessageCount, Status: c.Status, ContextPolicyJSON: c.ContextPolicy,
		LastCompactedAt: c.LastCompactedAt, LastResponseID: c.LastResponseID, ShareStatus: shareNone,
		CreatedAt: c.CreatedAt, UpdatedAt: c.UpdatedAt,
	}
	if p != nil {
		d.ProjectID, d.ProjectName = p.PublicID, p.Name
	}
	if sh != nil {
		d.ShareStatus = sh.Status
		if d.ShareStatus == "" {
			d.ShareStatus = shareNone
		}
		if sh.Status == shareActive {
			d.ShareID = sh.ShareID
			t := sh.CreatedAt
			if sh.RegeneratedAt != nil {
				t = *sh.RegeneratedAt
			}
			d.SharedAt = &t
			d.LastShareAccessedAt = sh.LastAccessedAt
		}
	}
	return d
}

func (s *service) conversationDTO(ctx context.Context, c *Conversation) (*ConversationDTO, error) {
	dtos, err := s.conversationDTOs(ctx, []Conversation{*c})
	if err != nil {
		return nil, err
	}
	return &dtos[0], nil
}

// ----- create -----

func (s *service) findProject(ctx context.Context, userID uint, publicID string) (*Project, error) {
	var p Project
	err := s.db.WithContext(ctx).Where("public_id = ? AND user_id = ?", publicID, userID).First(&p).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	return &p, nil
}

func (s *service) createConversation(ctx context.Context, userID uint, title, model, projectPublicID string) (*ConversationDTO, error) {
	title = strings.TrimSpace(title)
	if title == "" {
		title = placeholderTitle
	}
	model = strings.TrimSpace(model)
	var project *Project
	if pid := strings.TrimSpace(projectPublicID); pid != "" {
		p, err := s.findProject(ctx, userID, pid)
		if err != nil {
			return nil, err
		}
		project = p
		if model == "" && p.DefaultModel != "" && s.ports.Catalog.ChatModelAvailable(ctx, userID, p.DefaultModel) {
			model = p.DefaultModel
		}
	}
	c := &Conversation{
		UserID: userID, PublicID: newPublicID(), Title: title, LabelsJSON: "[]", ModelName: model,
		Provider: inferProvider(model), SessionKey: "sess_" + newPublicID(), Status: statusActive,
		ContextPolicy: s.contextPolicyJSON(ctx),
	}
	if project != nil {
		c.ProjectID = &project.ID
	}
	if err := s.db.WithContext(ctx).Create(c).Error; err != nil {
		return nil, database.Translate(err)
	}
	return s.conversationDTO(ctx, c)
}

// ----- list / search -----

type listFilter struct {
	Status  string
	Starred string
	Share   string
	Project string
	Query   string
}

func normalizeListFilter(f listFilter) listFilter {
	f.Status = strings.ToLower(strings.TrimSpace(f.Status))
	if f.Status != statusArchived && f.Status != "all" {
		f.Status = statusActive
	}
	f.Starred = strings.ToLower(strings.TrimSpace(f.Starred))
	if f.Starred != "starred" && f.Starred != "unstarred" {
		f.Starred = "all"
	}
	f.Share = strings.ToLower(strings.TrimSpace(f.Share))
	if f.Share != "shared" && f.Share != "unshared" {
		f.Share = "all"
	}
	f.Project = strings.TrimSpace(f.Project)
	f.Query = strings.TrimSpace(f.Query)
	return f
}

// keywordScope restricts a conversations query to rows matching q.
func keywordScope(userID uint, q string) func(*gorm.DB) *gorm.DB {
	return func(db *gorm.DB) *gorm.DB {
		if q == "" {
			return db
		}
		pat := "%" + escapeLike(strings.ToLower(q)) + "%"
		return db.Where(
			"(LOWER(chat_conversations.title) LIKE @p ESCAPE '!' OR LOWER(chat_conversations.public_id) LIKE @p ESCAPE '!'"+
				" OR LOWER(chat_conversations.labels_json) LIKE @p ESCAPE '!' OR LOWER(chat_conversations.model) LIKE @p ESCAPE '!'"+
				" OR LOWER(chat_conversations.provider) LIKE @p ESCAPE '!'"+
				" OR chat_conversations.project_id IN (SELECT pr.id FROM chat_conversation_projects pr WHERE pr.user_id = @uid AND pr.deleted_at IS NULL"+
				"   AND (LOWER(pr.name) LIKE @p ESCAPE '!' OR LOWER(pr.public_id) LIKE @p ESCAPE '!' OR LOWER(pr.description) LIKE @p ESCAPE '!'))"+
				" OR chat_conversations.id IN (SELECT m.conversation_id FROM chat_messages m WHERE m.user_id = @uid AND m.deleted_at IS NULL"+
				"   AND m.role IN ('user','assistant') AND LOWER(m.content) LIKE @p ESCAPE '!'))",
			map[string]any{"p": pat, "uid": userID})
	}
}
