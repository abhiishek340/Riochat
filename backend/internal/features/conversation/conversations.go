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

func (s *service) listConversations(ctx context.Context, userID uint, f listFilter, p pagination.Page) ([]ConversationDTO, int64, error) {
	f = normalizeListFilter(f)
	q := s.db.WithContext(ctx).Model(&Conversation{}).Where("chat_conversations.user_id = ?", userID)
	switch f.Status {
	case statusArchived:
		q = q.Where("chat_conversations.status = ?", statusArchived)
	case statusActive:
		q = q.Where("chat_conversations.status <> ?", statusArchived)
	}
	switch f.Starred {
	case "starred":
		q = q.Where("chat_conversations.is_starred = ?", true)
	case "unstarred":
		q = q.Where("chat_conversations.is_starred = ?", false)
	}
	switch f.Share {
	case "shared":
		q = q.Where("EXISTS (SELECT 1 FROM chat_conversation_shares sh WHERE sh.conversation_id = chat_conversations.id AND sh.status = ? AND sh.deleted_at IS NULL)", shareActive)
	case "unshared":
		q = q.Where("NOT EXISTS (SELECT 1 FROM chat_conversation_shares sh WHERE sh.conversation_id = chat_conversations.id AND sh.status = ? AND sh.deleted_at IS NULL)", shareActive)
	}
	switch strings.ToLower(f.Project) {
	case "", "all":
	case "unassigned":
		q = q.Where("chat_conversations.project_id IS NULL")
	default:
		proj, err := s.findProject(ctx, userID, f.Project)
		if err != nil {
			if err == ErrProjectNotFound {
				return []ConversationDTO{}, 0, nil
			}
			return nil, 0, err
		}
		q = q.Where("chat_conversations.project_id = ?", proj.ID)
	}
	q = q.Scopes(keywordScope(userID, f.Query))
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "chat_conversations.updated_at DESC, chat_conversations.id DESC"
	if f.Starred == "starred" {
		order = "chat_conversations.starred_at DESC, chat_conversations.id DESC"
	}
	var rows []Conversation
	if err := q.Order(order).Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	dtos, err := s.conversationDTOs(ctx, rows)
	return dtos, total, err
}

func (s *service) searchConversations(ctx context.Context, userID uint, query string, p pagination.Page) ([]ConversationSearchItem, bool, error) {
	query = strings.TrimSpace(query)
	if utf8.RuneCountInString(query) > maxSearchQuery {
		return nil, false, errSearchTooLong
	}
	q := s.db.WithContext(ctx).Model(&Conversation{}).Where("chat_conversations.user_id = ?", userID).Scopes(keywordScope(userID, query))
	var rows []Conversation
	if err := q.Order("chat_conversations.updated_at DESC, chat_conversations.id DESC").
		Offset(p.Offset()).Limit(p.Limit() + 1).Find(&rows).Error; err != nil {
		return nil, false, err
	}
	hasMore := len(rows) > p.Limit()
	if hasMore {
		rows = rows[:p.Limit()]
	}
	dtos, err := s.conversationDTOs(ctx, rows)
	if err != nil {
		return nil, false, err
	}
	items := make([]ConversationSearchItem, 0, len(dtos))
	for _, d := range dtos {
		items = append(items, ConversationSearchItem{
			PublicID: d.PublicID, ProjectID: d.ProjectID, ProjectName: d.ProjectName, Title: d.Title,
			LabelsJSON: d.LabelsJSON, IsStarred: d.IsStarred, MessageCount: d.MessageCount, Status: d.Status, UpdatedAt: d.UpdatedAt,
		})
	}
	return items, hasMore, nil
}

// ----- simple updates -----

func (s *service) getConversationDTO(ctx context.Context, userID uint, publicID string) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return s.conversationDTO(ctx, c)
}

func (s *service) renameConversation(ctx context.Context, userID uint, publicID, title string) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > maxTitleLen {
		return nil, ErrInvalidTitle
	}
	if err := s.db.WithContext(ctx).Model(c).Update("title", title).Error; err != nil {
		return nil, err
	}
	c.Title = title
	return s.conversationDTO(ctx, c)
}

// normalizeLabels applies the label rules; ok is false for an invalid list.
func normalizeLabels(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, raw := range in {
		l := collapseSpaces(raw)
		l = strings.TrimSpace(strings.TrimLeft(l, "#"))
		n := utf8.RuneCountInString(l)
		if n == 0 || n > maxLabelLen {
			return nil, false
		}
		key := strings.ToLower(l)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, l)
	}
	if len(out) > maxLabels {
		return nil, false
	}
	return out, true
}

func (s *service) updateLabels(ctx context.Context, userID uint, publicID string, labels []string) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	norm, ok := normalizeLabels(labels)
	if !ok {
		return nil, ErrInvalidLabels
	}
	raw := marshalNoEscape(norm)
	if err := s.db.WithContext(ctx).Model(c).UpdateColumns(map[string]any{
		"labels_json": raw, "labels_manually_managed": true, "updated_at": s.now(),
	}).Error; err != nil {
		return nil, err
	}
	c.LabelsJSON, c.LabelsManuallyManaged = raw, true
	return s.conversationDTO(ctx, c)
}

func (s *service) setStar(ctx context.Context, userID uint, publicID string, starred bool) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if c.IsStarred != starred {
		upd := map[string]any{"is_starred": starred, "starred_at": nil}
		if starred {
			now := s.now()
			upd["starred_at"] = now
			c.StarredAt = &now
		} else {
			c.StarredAt = nil
		}
		if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", c.ID).UpdateColumns(upd).Error; err != nil {
			return nil, err
		}
		c.IsStarred = starred
	}
	return s.conversationDTO(ctx, c)
}

func (s *service) setArchive(ctx context.Context, userID uint, publicID string, archived bool) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	status := statusActive
	if archived {
		status = statusArchived
	}
	// Archiving or restoring moves the conversation in recency order.
	now := s.now()
	if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", c.ID).UpdateColumns(map[string]any{"status": status, "updated_at": now}).Error; err != nil {
		return nil, err
	}
	c.Status, c.UpdatedAt = status, now
	return s.conversationDTO(ctx, c)
}

func (s *service) deleteConversation(ctx context.Context, userID uint, publicID string, deleteFiles bool) (*DeleteResult, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if err := s.db.WithContext(ctx).Delete(&Conversation{}, c.ID).Error; err != nil {
		return nil, err
	}
	res := &DeleteResult{Deleted: true}
	if deleteFiles {
		n, quota, err := s.cleanupUnreferencedFiles(ctx, userID, []uint{c.ID})
		if err != nil {
			s.log.Warn("conversation_file_cleanup_failed", logErr(err))
		}
		res.DeletedFileCount, res.Quota = n, quota
	}
	return res, nil
}

func (s *service) setConversationProject(ctx context.Context, userID uint, publicID, projectPublicID string) (*ConversationDTO, error) {
	c, err := s.getConversation(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	pid := strings.TrimSpace(projectPublicID)
	var target *uint
	if pid != "" && !strings.EqualFold(pid, "unassigned") {
		p, err := s.findProject(ctx, userID, pid)
		if err != nil {
			return nil, err
		}
		target = &p.ID
	}
	if err := s.db.WithContext(ctx).Model(&Conversation{}).Where("id = ?", c.ID).UpdateColumns(map[string]any{"project_id": target}).Error; err != nil {
		return nil, err
	}
	c.ProjectID = target
	return s.conversationDTO(ctx, c)
}
