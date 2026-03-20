package conversation

import (
	"context"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	maxProjectsPerUser = 200
	mcpModeInherit     = "inherit"
	mcpModeCustom      = "custom"
	maxProjectKBs      = 8
)

// createProjectRequest is the body of POST /conversation-projects.
type createProjectRequest struct {
	Name                    string   `json:"name" binding:"required,max=80"`
	Description             string   `json:"description" binding:"max=255"`
	SystemPrompt            string   `json:"systemPrompt" binding:"max=12000"`
	DefaultModel            string   `json:"defaultModel" binding:"max=128"`
	MCPDefaultMode          string   `json:"mcpDefaultMode" binding:"omitempty,oneof=inherit custom"`
	DefaultMCPToolIDs       []uint   `json:"defaultMCPToolIDs" binding:"max=128"`
	DefaultSkillIDs         []uint   `json:"defaultSkillIDs" binding:"max=128"`
	DefaultKnowledgeBaseIDs []string `json:"defaultKnowledgeBaseIDs" binding:"max=8,dive,required,max=32"`
	Color                   string   `json:"color" binding:"max=32"`
	Icon                    string   `json:"icon" binding:"max=32"`
}

// updateProjectRequest is the body of PATCH /conversation-projects/:id.
type updateProjectRequest struct {
	Name                    *string  `json:"name" binding:"omitempty,max=80"`
	Description             *string  `json:"description" binding:"omitempty,max=255"`
	SystemPrompt            *string  `json:"systemPrompt" binding:"omitempty,max=12000"`
	DefaultModel            *string  `json:"defaultModel" binding:"omitempty,max=128"`
	MCPDefaultMode          *string  `json:"mcpDefaultMode" binding:"omitempty,oneof=inherit custom"`
	DefaultMCPToolIDs       []uint   `json:"defaultMCPToolIDs" binding:"max=128"`
	DefaultSkillIDs         []uint   `json:"defaultSkillIDs" binding:"max=128"`
	DefaultKnowledgeBaseIDs []string `json:"defaultKnowledgeBaseIDs" binding:"max=8,dive,required,max=32"`
	Color                   *string  `json:"color" binding:"omitempty,max=32"`
	Icon                    *string  `json:"icon" binding:"omitempty,max=32"`
	Status                  *string  `json:"status" binding:"omitempty,oneof=active archived"`
}

type projectLinks struct {
	tools  []uint
	skills []uint
	kbs    []string
}

func (s *service) loadLinks(ctx context.Context, projectIDs []uint) (map[uint]*projectLinks, error) {
	out := map[uint]*projectLinks{}
	for _, id := range projectIDs {
		out[id] = &projectLinks{tools: []uint{}, skills: []uint{}, kbs: []string{}}
	}
	if len(projectIDs) == 0 {
		return out, nil
	}
	var tools []ProjectMCPTool
	if err := s.db.WithContext(ctx).Where("project_id IN ?", projectIDs).Order("sort_order ASC").Find(&tools).Error; err != nil {
		return nil, err
	}
	for _, t := range tools {
		out[t.ProjectID].tools = append(out[t.ProjectID].tools, t.ToolID)
	}
	var skills []ProjectSkill
	if err := s.db.WithContext(ctx).Where("project_id IN ?", projectIDs).Order("sort_order ASC").Find(&skills).Error; err != nil {
		return nil, err
	}
	for _, t := range skills {
		out[t.ProjectID].skills = append(out[t.ProjectID].skills, t.SkillID)
	}
	var kbs []ProjectKnowledgeBase
	if err := s.db.WithContext(ctx).Where("project_id IN ?", projectIDs).Order("sort_order ASC").Find(&kbs).Error; err != nil {
		return nil, err
	}
	for _, t := range kbs {
		out[t.ProjectID].kbs = append(out[t.ProjectID].kbs, t.KnowledgeBaseID)
	}
	return out, nil
}

func (s *service) projectDTOs(ctx context.Context, rows []Project) ([]ProjectDTO, error) {
	ids := make([]uint, 0, len(rows))
	for _, p := range rows {
		ids = append(ids, p.ID)
	}
	links, err := s.loadLinks(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]ProjectDTO, 0, len(rows))
	for _, p := range rows {
		l := links[p.ID]
		mode := p.MCPDefaultMode
		if mode == "" {
			mode = mcpModeInherit
		}
		out = append(out, ProjectDTO{
			PublicID: p.PublicID, Name: p.Name, Description: p.Description, SystemPrompt: p.SystemPrompt,
			DefaultModel: p.DefaultModel, MCPDefaultMode: mode, DefaultMCPToolIDs: l.tools, DefaultSkillIDs: l.skills,
			DefaultKnowledgeBaseIDs: l.kbs, Color: p.Color, Icon: p.Icon, SortOrder: p.SortOrder, Status: p.Status,
			CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
		})
	}
	return out, nil
}

func (s *service) projectDTO(ctx context.Context, p *Project) (*ProjectDTO, error) {
	d, err := s.projectDTOs(ctx, []Project{*p})
	if err != nil {
		return nil, err
	}
	return &d[0], nil
}

func (s *service) listProjects(ctx context.Context, userID uint, status string) ([]ProjectDTO, error) {
	status = strings.ToLower(strings.TrimSpace(status))
	q := s.db.WithContext(ctx).Where("user_id = ?", userID)
	switch status {
	case "archived":
		q = q.Where("status = ?", "archived")
	case "all":
	default:
		q = q.Where("status = ?", statusActive)
	}
	var rows []Project
	if err := q.Order("sort_order ASC, id DESC").Find(&rows).Error; err != nil {
		return nil, err
	}
	return s.projectDTOs(ctx, rows)
}

// cleanKnowledgeBaseIDs applies the trim/blank/dedupe rule; ok is false when the
// input was not already clean or has too many entries.
func cleanKnowledgeBaseIDs(in []string) ([]string, bool) {
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, v := range in {
		t := strings.TrimSpace(v)
		if t == "" || seen[t] {
			return nil, false
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > maxProjectKBs {
		return nil, false
	}
	return out, true
}

func diffUints(next, prev []uint) []uint {
	had := map[uint]bool{}
	for _, v := range prev {
		had[v] = true
	}
	var out []uint
	for _, v := range next {
		if !had[v] {
			out = append(out, v)
		}
	}
	return out
}

func diffStrings(next, prev []string) []string {
	had := map[string]bool{}
	for _, v := range prev {
		had[v] = true
	}
	var out []string
	for _, v := range next {
		if !had[v] {
			out = append(out, v)
		}
	}
	return out
}

func trimmedRunes(s string, max int) (string, bool) {
	t := strings.TrimSpace(s)
	return t, utf8.RuneCountInString(t) <= max
}

// projectState is the resulting state validated before saving.
type projectState struct {
	name, description, systemPrompt, defaultModel, mode, color, icon string
	tools, skills                                                    []uint
	kbs                                                              []string
}

func (s *service) validateProjectState(ctx context.Context, userID uint, next projectState, prev *projectState) error {
	rs := s.settings(ctx)
	modelChanged := prev == nil || prev.defaultModel != next.defaultModel
	if modelChanged && next.defaultModel != "" && !s.ports.Catalog.ChatModelAvailable(ctx, userID, next.defaultModel) {
		return ErrInvalidProject
	}
	if len(next.tools) > rs.MaxSelectedTools || len(next.skills) > rs.MaxSelectedTools {
		return ErrInvalidProject
	}
	var prevTools []uint
	var prevSkills []uint
	var prevKBs []string
	if prev != nil {
		prevTools, prevSkills, prevKBs = prev.tools, prev.skills, prev.kbs
	}
	if next.mode == mcpModeCustom {
		added := diffUints(next.tools, prevTools)
		if prev != nil && prev.mode != mcpModeCustom {
			added = next.tools
		}
		if len(added) > 0 {
			if err := projectCapabilityError(s.ports.Capabilities.ValidateMCPTools(ctx, userID, added)); err != nil {
				return err
			}
			// The image-processor limit concerns the whole resulting set, so a
			// complete check also runs; only that rule is enforced here because
			// tools kept from earlier saves are not re-checked for existence.
			if len(added) < len(next.tools) {
				if err := s.ports.Capabilities.ValidateMCPTools(ctx, userID, next.tools); err != nil {
					if te, ok := apperr.Find(err); ok && te.Code == ErrMultipleImageProcs.Code {
						return ErrInvalidProject
					}
				}
			}
		}
	}
	if added := diffUints(next.skills, prevSkills); len(added) > 0 {
		if err := projectCapabilityError(s.ports.Capabilities.ValidateSkills(ctx, userID, added)); err != nil {
			return err
		}
	}
	if len(next.kbs) > 0 && !rs.KnowledgeBaseEnabled {
		return ErrInvalidProject
	}
	if added := diffStrings(next.kbs, prevKBs); len(added) > 0 {
		if err := projectCapabilityError(s.ports.Capabilities.ValidateKnowledgeBases(ctx, userID, added)); err != nil {
			return err
		}
	}
	return nil
}

// projectCapabilityError turns a rejection by another area (unknown tool,
// hidden skill, unready knowledge base ...) into the project validation error.
// Failures that are not client errors (database trouble) are returned as is.
func projectCapabilityError(err error) error {
	if err == nil {
		return nil
	}
	if te, ok := apperr.Find(err); ok && te.Status < 500 {
		return ErrInvalidProject
	}
	return err
}

func normalizeMode(raw string, defaultInherit bool) (string, bool) {
	m := strings.ToLower(strings.TrimSpace(raw))
	if m == "" && defaultInherit {
		return mcpModeInherit, true
	}
	if m == mcpModeInherit || m == mcpModeCustom {
		return m, true
	}
	return "", false
}

func (s *service) createProject(ctx context.Context, userID uint, req createProjectRequest) (*ProjectDTO, error) {
	st := projectState{}
	var ok bool
	if st.name, ok = trimmedRunes(req.Name, 80); !ok || st.name == "" {
		return nil, ErrInvalidProject
	}
	if st.description, ok = trimmedRunes(req.Description, 255); !ok {
		return nil, ErrInvalidProject
	}
	if st.systemPrompt, ok = trimmedRunes(req.SystemPrompt, 12000); !ok {
		return nil, ErrInvalidProject
	}
	if st.defaultModel, ok = trimmedRunes(req.DefaultModel, 128); !ok {
		return nil, ErrInvalidProject
	}
	if st.color, ok = trimmedRunes(req.Color, 32); !ok {
		return nil, ErrInvalidProject
	}
	if st.icon, ok = trimmedRunes(req.Icon, 32); !ok {
		return nil, ErrInvalidProject
	}
	if st.mode, ok = normalizeMode(req.MCPDefaultMode, true); !ok {
		return nil, ErrInvalidProject
	}
	st.tools = dedupeUints(req.DefaultMCPToolIDs, false)
	if st.mode == mcpModeInherit {
		st.tools = []uint{}
	}
	st.skills = dedupeUints(req.DefaultSkillIDs, true)
	if st.kbs, ok = cleanKnowledgeBaseIDs(req.DefaultKnowledgeBaseIDs); !ok {
		return nil, ErrInvalidProject
	}
	if err := s.validateProjectState(ctx, userID, st, nil); err != nil {
		return nil, err
	}
	var created Project
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&Project{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
			return err
		}
		if n >= maxProjectsPerUser {
			return ErrProjectLimit
		}
		created = Project{
			UserID: userID, PublicID: newPublicID(), Name: st.name, Description: st.description,
			SystemPrompt: st.systemPrompt, DefaultModel: st.defaultModel, MCPDefaultMode: st.mode,
			Color: st.color, Icon: st.icon, SortOrder: 0, Status: statusActive,
		}
		if err := tx.Create(&created).Error; err != nil {
			return err
		}
		return writeLinks(tx, created.ID, st.tools, st.skills, st.kbs, true, true, true)
	})
	if err != nil {
		return nil, err
	}
	return s.projectDTO(ctx, &created)
}

// writeLinks replaces the selected link sets of a project.
func writeLinks(tx *gorm.DB, projectID uint, tools, skills []uint, kbs []string, doTools, doSkills, doKBs bool) error {
	if doTools {
		if err := tx.Where("project_id = ?", projectID).Delete(&ProjectMCPTool{}).Error; err != nil {
			return err
		}
		for i, id := range tools {
			if err := tx.Create(&ProjectMCPTool{ProjectID: projectID, ToolID: id, SortOrder: i + 1}).Error; err != nil {
				return err
			}
		}
	}
	if doSkills {
		if err := tx.Where("project_id = ?", projectID).Delete(&ProjectSkill{}).Error; err != nil {
			return err
		}
		for i, id := range skills {
			if err := tx.Create(&ProjectSkill{ProjectID: projectID, SkillID: id, SortOrder: i + 1}).Error; err != nil {
				return err
			}
		}
	}
	if doKBs {
		if err := tx.Where("project_id = ?", projectID).Delete(&ProjectKnowledgeBase{}).Error; err != nil {
			return err
		}
		for i, id := range kbs {
			if err := tx.Create(&ProjectKnowledgeBase{ProjectID: projectID, KnowledgeBaseID: id, SortOrder: i + 1}).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *service) updateProject(ctx context.Context, userID uint, publicID string, req updateProjectRequest) (*ProjectDTO, error) {
	publicID = strings.TrimSpace(publicID)
	p, err := s.findProject(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if req.Name == nil && req.Description == nil && req.SystemPrompt == nil && req.DefaultModel == nil &&
		req.MCPDefaultMode == nil && req.DefaultMCPToolIDs == nil && req.DefaultSkillIDs == nil &&
		req.DefaultKnowledgeBaseIDs == nil && req.Color == nil && req.Icon == nil && req.Status == nil {
		return nil, ErrInvalidProject
	}
	links, err := s.loadLinks(ctx, []uint{p.ID})
	if err != nil {
		return nil, err
	}
	l := links[p.ID]
	mode := p.MCPDefaultMode
	if mode == "" {
		mode = mcpModeInherit
	}
	prev := projectState{
		name: p.Name, description: p.Description, systemPrompt: p.SystemPrompt, defaultModel: p.DefaultModel,
		mode: mode, color: p.Color, icon: p.Icon, tools: l.tools, skills: l.skills, kbs: l.kbs,
	}
	next := prev
	var ok bool
	if req.Name != nil {
		if next.name, ok = trimmedRunes(*req.Name, 80); !ok || next.name == "" {
			return nil, ErrInvalidProject
		}
	}
	if req.Description != nil {
		if next.description, ok = trimmedRunes(*req.Description, 255); !ok {
			return nil, ErrInvalidProject
		}
	}
	if req.SystemPrompt != nil {
		if next.systemPrompt, ok = trimmedRunes(*req.SystemPrompt, 12000); !ok {
			return nil, ErrInvalidProject
		}
	}
	if req.DefaultModel != nil {
		if next.defaultModel, ok = trimmedRunes(*req.DefaultModel, 128); !ok {
			return nil, ErrInvalidProject
		}
	}
	if req.Color != nil {
		if next.color, ok = trimmedRunes(*req.Color, 32); !ok {
			return nil, ErrInvalidProject
		}
	}
	if req.Icon != nil {
		if next.icon, ok = trimmedRunes(*req.Icon, 32); !ok {
			return nil, ErrInvalidProject
		}
	}
	if req.MCPDefaultMode != nil {
		if next.mode, ok = normalizeMode(*req.MCPDefaultMode, false); !ok {
			return nil, ErrInvalidProject
		}
	}
	rewriteTools := req.DefaultMCPToolIDs != nil
	if rewriteTools {
		next.tools = dedupeUints(req.DefaultMCPToolIDs, false)
	}
	if next.mode == mcpModeInherit {
		if len(next.tools) > 0 {
			rewriteTools = true
		}
		next.tools = []uint{}
	}
	rewriteSkills := req.DefaultSkillIDs != nil
	if rewriteSkills {
		next.skills = dedupeUints(req.DefaultSkillIDs, true)
	}
	rewriteKBs := req.DefaultKnowledgeBaseIDs != nil
	if rewriteKBs {
		if next.kbs, ok = cleanKnowledgeBaseIDs(req.DefaultKnowledgeBaseIDs); !ok {
			return nil, ErrInvalidProject
		}
	}
	if err := s.validateProjectState(ctx, userID, next, &prev); err != nil {
		return nil, err
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		upd := map[string]any{
			"name": next.name, "description": next.description, "system_prompt": next.systemPrompt,
			"default_model": next.defaultModel, "mcp_default_mode": next.mode, "color": next.color, "icon": next.icon,
		}
		if req.Status != nil {
			upd["status"] = *req.Status
		}
		if err := tx.Model(&Project{}).Where("id = ?", p.ID).Updates(upd).Error; err != nil {
			return err
		}
		return writeLinks(tx, p.ID, next.tools, next.skills, next.kbs, rewriteTools, rewriteSkills, rewriteKBs)
	})
	if err != nil {
		return nil, err
	}
	fresh, err := s.findProject(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	return s.projectDTO(ctx, fresh)
}

func (s *service) reorderProjects(ctx context.Context, userID uint, ids []string) ([]ProjectDTO, error) {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		clean = append(clean, strings.TrimSpace(id))
	}
	if len(clean) == 0 || len(dedupeStrings(clean)) != len(clean) {
		return nil, ErrInvalidProject
	}
	for _, id := range clean {
		if id == "" {
			return nil, ErrInvalidProject
		}
	}
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range clean {
			res := tx.Model(&Project{}).Where("public_id = ? AND user_id = ?", id, userID).Update("sort_order", i+1)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return ErrProjectNotFound
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.listProjects(ctx, userID, statusActive)
}

func (s *service) deleteProject(ctx context.Context, userID uint, publicID string, deleteConversations, deleteFiles bool) (*DeleteResult, error) {
	p, err := s.findProject(ctx, userID, strings.TrimSpace(publicID))
	if err != nil {
		return nil, err
	}
	var convIDs []uint
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Model(&Conversation{}).Where("user_id = ? AND project_id = ?", userID, p.ID).Pluck("id", &convIDs).Error; err != nil {
			return err
		}
		if len(convIDs) > 0 {
			if deleteConversations {
				if err := tx.Where("id IN ?", convIDs).Delete(&Conversation{}).Error; err != nil {
					return err
				}
			} else if err := tx.Model(&Conversation{}).Where("id IN ?", convIDs).UpdateColumns(map[string]any{"project_id": nil}).Error; err != nil {
				return err
			}
		}
		if err := writeLinks(tx, p.ID, nil, nil, nil, true, true, true); err != nil {
			return err
		}
		return tx.Delete(&Project{}, p.ID).Error
	})
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrProjectNotFound
		}
		return nil, err
	}
	res := &DeleteResult{Deleted: true}
	if deleteConversations && deleteFiles && len(convIDs) > 0 {
		n, quota, err := s.cleanupUnreferencedFiles(ctx, userID, convIDs)
		if err != nil {
			s.log.Warn("conversation_file_cleanup_failed", logErr(err))
		}
		res.DeletedFileCount, res.Quota = n, quota
	}
	return res, nil
}
