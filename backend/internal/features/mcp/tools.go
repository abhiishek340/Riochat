package mcp

import (
	"context"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ToolRow is a tool with its server's name (filled only by some listings).
type ToolRow struct {
	Tool
	ServerName string
}

func orderTools(db *gorm.DB) *gorm.DB { return db.Order("sort_order ASC, name ASC, id ASC") }

// ListServerTools returns every tool of a server, inactive ones included.
func (s *Service) ListServerTools(ctx context.Context, serverID uint) ([]Tool, error) {
	var tools []Tool
	if err := orderTools(s.db.WithContext(ctx).Where("server_id = ?", serverID)).Find(&tools).Error; err != nil {
		return nil, database.Translate(err)
	}
	return tools, nil
}

// ListEnabledTools returns what end users may select in chat. With the master
// switch off the answer is empty. In billing mode "self" every price is zero.
func (s *Service) ListEnabledTools(ctx context.Context) ([]ToolRow, error) {
	if !s.runtime(ctx).Enabled {
		return []ToolRow{}, nil
	}
	free := false
	if s.billing != nil {
		mode, err := s.billing.BillingMode(ctx)
		if err != nil {
			return nil, err
		}
		free = mode == "self"
	}
	db := s.db.WithContext(ctx)
	var servers []Server
	if err := db.Where("status = ?", StatusActive).Order("sort_order ASC, id ASC").Find(&servers).Error; err != nil {
		return nil, database.Translate(err)
	}
	out := []ToolRow{}
	if len(servers) == 0 {
		return out, nil
	}
	ids := make([]uint, len(servers))
	for i := range servers {
		ids[i] = servers[i].ID
	}
	var tools []Tool
	if err := orderTools(db.Where("server_id IN ? AND status = ?", ids, StatusActive)).Find(&tools).Error; err != nil {
		return nil, database.Translate(err)
	}
	byServer := make(map[uint][]Tool, len(servers))
	for _, t := range tools {
		byServer[t.ServerID] = append(byServer[t.ServerID], t)
	}
	for _, srv := range servers {
		for _, t := range byServer[srv.ID] {
			if free {
				t.PriceNanoUSD = 0
			}
			out = append(out, ToolRow{Tool: t, ServerName: srv.Name})
		}
	}
	return out, nil
}

// ToolPatch is a partial tool update; a nil field means "unchanged".
type ToolPatch struct {
	DisplayName              *string
	Description              *string
	AttachmentInputMode      *string
	AttachmentArgument       *string
	AttachmentEncoding       *string
	AttachmentPromptArgument *string
	PriceNanoUSD             *int64
	Status                   *string
}

func trimmedPtr(p *string, f func(string) string) *string {
	if p == nil {
		return nil
	}
	v := f(strings.TrimSpace(*p))
	return &v
}

// UpdateTool applies a patch under a row lock and returns the resulting tool.
// Only fields that differ are written.
func (s *Service) UpdateTool(ctx context.Context, id uint, patch ToolPatch) (*Tool, error) {
	same := func(v string) string { return v }
	displayName := trimmedPtr(patch.DisplayName, same)
	description := trimmedPtr(patch.Description, same)
	status := trimmedPtr(patch.Status, same)
	mode := trimmedPtr(patch.AttachmentInputMode, strings.ToLower)
	argument := trimmedPtr(patch.AttachmentArgument, same)
	encoding := trimmedPtr(patch.AttachmentEncoding, strings.ToLower)
	prompt := trimmedPtr(patch.AttachmentPromptArgument, same)

	if displayName != nil && utf8.RuneCountInString(*displayName) > maxToolNameChars {
		return nil, ErrInvalidToolName
	}
	if description != nil && utf8.RuneCountInString(*description) > maxToolDescChars {
		return nil, ErrInvalidToolDesc
	}
	if patch.PriceNanoUSD != nil && *patch.PriceNanoUSD < 0 {
		return nil, ErrInvalidToolPrice
	}
	if status != nil && *status != StatusActive && *status != StatusInactive {
		return nil, ErrInvalidToolStatus
	}
	if mode != nil && *mode != AttachNone && *mode != AttachImage {
		return nil, errAttachmentConfig()
	}
	if argument != nil && utf8.RuneCountInString(*argument) > maxArgumentName {
		return nil, errAttachmentConfig()
	}
	if prompt != nil && utf8.RuneCountInString(*prompt) > maxArgumentName {
		return nil, errAttachmentConfig()
	}
	if encoding != nil && *encoding != EncodingNone && *encoding != EncodingBase64 && *encoding != EncodingDataURL {
		return nil, errAttachmentConfig()
	}
	touchesAttachment := mode != nil || argument != nil || encoding != nil || prompt != nil

	var result Tool
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var cur Tool
		if err := lockRows(tx).First(&cur, id).Error; err != nil {
			return database.Translate(err)
		}
		changes := map[string]any{}
		if displayName != nil && *displayName != cur.DisplayName {
			changes["display_name"] = *displayName
			changes["metadata_customized"] = true
		}
		if description != nil && *description != cur.Description {
			changes["description"] = *description
			changes["metadata_customized"] = true
		}
		if patch.PriceNanoUSD != nil && *patch.PriceNanoUSD != cur.PriceNanoUSD {
			changes["price_nanousd"] = *patch.PriceNanoUSD
		}
		if status != nil && *status != cur.Status {
			changes["status"] = *status
		}
		if touchesAttachment {
			eff := cur.attachment()
			if mode != nil {
				eff.Mode = *mode
			}
			if argument != nil {
				eff.Argument = *argument
			}
			if encoding != nil {
				eff.Encoding = *encoding
			}
			if prompt != nil {
				eff.Prompt = *prompt
			}
			if mode != nil && *mode == AttachNone {
				eff.Argument, eff.Encoding, eff.Prompt = "", "", ""
			}
			if !attachmentValid(eff, cur.InputSchemaJSON) {
				return errAttachmentConfig()
			}
			if eff.Mode != cur.AttachmentInputMode {
				changes["attachment_input_mode"] = eff.Mode
			}
			if eff.Argument != cur.AttachmentArgument {
				changes["attachment_argument"] = eff.Argument
			}
			if eff.Encoding != cur.AttachmentEncoding {
				changes["attachment_encoding"] = eff.Encoding
			}
			if eff.Prompt != cur.AttachmentPromptArgument {
				changes["attachment_prompt_argument"] = eff.Prompt
			}
		}
		if len(changes) > 0 {
			if err := tx.Model(&Tool{}).Where("id = ?", id).Updates(changes).Error; err != nil {
				return database.Translate(err)
			}
		}
		return database.Translate(tx.First(&result, id).Error)
	})
	if err != nil {
		return nil, err
	}
	return &result, nil
}

// SetToolsStatus changes the status of the listed tools of one server (ids of
// other servers are ignored) and returns all of the server's tools.
func (s *Service) SetToolsStatus(ctx context.Context, serverID uint, toolIDs []uint, status string) ([]Tool, error) {
	status = strings.TrimSpace(status)
	if status != StatusActive && status != StatusInactive {
		return nil, ErrInvalidToolStatus
	}
	if len(toolIDs) == 0 {
		return nil, ErrInvalidSelection
	}
	if err := s.db.WithContext(ctx).Model(&Tool{}).
		Where("server_id = ? AND id IN ?", serverID, toolIDs).Update("status", status).Error; err != nil {
		return nil, database.Translate(err)
	}
	return s.ListServerTools(ctx, serverID)
}

// OrderEntry is one server in a reorder request.
type OrderEntry struct {
	ServerID uint   `json:"serverID"`
	ToolIDs  []uint `json:"toolIDs"`
}

// OrderedServer is one server of the reorder response.
type OrderedServer struct {
	Server Server
	Tools  []ToolRow
}

// Reorder applies a complete ordering of servers and of each server's tools.
func (s *Service) Reorder(ctx context.Context, entries []OrderEntry) ([]OrderedServer, error) {
	if len(entries) == 0 {
		return nil, ErrInvalidSelection
	}
	var result []OrderedServer
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var servers []Server
		if err := lockRows(tx).Order("sort_order ASC, id ASC").Find(&servers).Error; err != nil {
			return database.Translate(err)
		}
		if len(entries) != len(servers) {
			return ErrInvalidSelection
		}
		byID := make(map[uint]*Server, len(servers))
		for i := range servers {
			byID[servers[i].ID] = &servers[i]
		}
		seen := make(map[uint]bool, len(entries))
		for _, e := range entries {
			if e.ServerID == 0 || byID[e.ServerID] == nil || seen[e.ServerID] {
				return ErrInvalidSelection
			}
			seen[e.ServerID] = true
		}
		var tools []Tool
		if err := lockRows(tx).Find(&tools).Error; err != nil {
			return database.Translate(err)
		}
		toolByID := make(map[uint]*Tool, len(tools))
		perServer := make(map[uint]int, len(servers))
		for i := range tools {
			toolByID[tools[i].ID] = &tools[i]
			perServer[tools[i].ServerID]++
		}
		for _, e := range entries {
			if len(e.ToolIDs) != perServer[e.ServerID] {
				return ErrInvalidSelection
			}
			dup := make(map[uint]bool, len(e.ToolIDs))
			for _, tid := range e.ToolIDs {
				t := toolByID[tid]
				if tid == 0 || t == nil || t.ServerID != e.ServerID || dup[tid] {
					return ErrInvalidSelection
				}
				dup[tid] = true
			}
		}
		result = make([]OrderedServer, 0, len(entries))
		for i, e := range entries {
			srv := byID[e.ServerID]
			srv.SortOrder = (i + 1) * sortStep
			if err := tx.Model(&Server{}).Where("id = ?", srv.ID).Update("sort_order", srv.SortOrder).Error; err != nil {
				return database.Translate(err)
			}
			rows := make([]ToolRow, 0, len(e.ToolIDs))
			for j, tid := range e.ToolIDs {
				t := toolByID[tid]
				t.SortOrder = (j + 1) * sortStep
				if err := tx.Model(&Tool{}).Where("id = ?", tid).Update("sort_order", t.SortOrder).Error; err != nil {
					return database.Translate(err)
				}
				rows = append(rows, ToolRow{Tool: *t, ServerName: srv.Name})
			}
			result = append(result, OrderedServer{Server: *srv, Tools: rows})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return result, nil
}
