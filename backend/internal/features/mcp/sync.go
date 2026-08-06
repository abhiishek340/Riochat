package mcp

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const syncTimeout = 10 * time.Second

// discovered is one tool of a listing after cleanup.
type discovered struct {
	name, display, description, schema string
}

func cleanListing(remote []RemoteTool) (list []discovered, count int) {
	index := make(map[string]int, len(remote))
	for _, r := range remote {
		name := strings.TrimSpace(r.Name)
		// A name that cannot be stored in the name column is skipped too.
		if name == "" || utf8.RuneCountInString(name) > maxToolNameChars {
			continue
		}
		count++
		d := discovered{
			name:        name,
			display:     strings.TrimSpace(r.Title),
			description: strings.TrimSpace(r.Description),
			schema:      strings.TrimSpace(string(r.InputSchema)),
		}
		if d.display == "" {
			d.display = name
		}
		if utf8.RuneCountInString(d.display) > maxToolNameChars {
			d.display = string([]rune(d.display)[:maxToolNameChars])
		}
		if d.schema == "" || d.schema == "null" {
			d.schema = "{}"
		}
		if i, dup := index[name]; dup {
			// A repeated name collapses onto the first position; the later
			// content wins, as an upsert would.
			list[i] = d
			continue
		}
		index[name] = len(list)
		list = append(list, d)
	}
	return list, count
}

func (s *Service) decryptToken(enc string) (string, error) {
	if strings.TrimSpace(enc) == "" {
		return "", nil
	}
	plain, _, err := s.secrets.Decrypt(enc)
	if err != nil {
		return "", fmt.Errorf("decrypt mcp token: %w", err)
	}
	return plain, nil
}

// SyncServer discovers the server's tools and merges them into the stored
// catalog. With overwrite the administrator's name and description edits are
// replaced by the remote ones.
func (s *Service) SyncServer(ctx context.Context, id uint, overwrite bool) ([]Tool, error) {
	var srv Server
	if err := s.db.WithContext(ctx).First(&srv, id).Error; err != nil {
		return nil, database.Translate(err)
	}
	if _, err := checkEndpoint(srv.BaseURL); err != nil {
		return nil, ErrInvalidServerURL.WithCause(err)
	}
	if s.client == nil {
		return nil, ErrClientUnavailable
	}
	token, err := s.decryptToken(srv.AuthTokenEnc)
	if err != nil {
		return nil, err
	}
	ep := Endpoint{BaseURL: srv.BaseURL, Token: token, Headers: withoutPlaceholders(parseHeadersLenient(srv.HeadersJSON))}
	remote, err := s.client.ListTools(ctx, ep, syncTimeout)
	if err != nil {
		s.recordSyncFailure(ctx, srv.ID, err)
		return nil, errors.New("mcp tool sync failed")
	}
	list, count := cleanListing(remote)
	now := s.clock.Now().UTC()
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var locked Server
		if err := lockRows(tx).First(&locked, id).Error; err != nil {
			return database.Translate(err)
		}
		var existing []Tool
		if err := tx.Where("server_id = ?", id).Find(&existing).Error; err != nil {
			return database.Translate(err)
		}
		byName := make(map[string]*Tool, len(existing))
		maxSort := 0
		for i := range existing {
			byName[existing[i].Name] = &existing[i]
			maxSort = max(maxSort, existing[i].SortOrder)
		}
		keep := make(map[string]bool, len(list))
		for pos, d := range list {
			keep[d.name] = true
			cur := byName[d.name]
			if cur == nil {
				no := false
				row := Tool{
					ServerID: id, Name: d.name, DisplayName: d.display, Description: d.description,
					MetadataCustomized: &no, InputSchemaJSON: d.schema, AttachmentInputMode: AttachNone,
					Status: StatusActive, SortOrder: maxSort + sortStep*(pos+1),
				}
				if err := tx.Create(&row).Error; err != nil {
					return database.Translate(err)
				}
				continue
			}
			if err := tx.Model(&Tool{}).Where("id = ?", cur.ID).Updates(mergeTool(cur, d, overwrite)).Error; err != nil {
				return database.Translate(err)
			}
		}
		var gone []uint
		for name, t := range byName {
			if !keep[name] {
				gone = append(gone, t.ID)
			}
		}
		if len(gone) > 0 {
			if err := s.links.RemoveToolLinks(ctx, tx, gone); err != nil {
				return err
			}
			if err := tx.Where("id IN ?", gone).Delete(&Tool{}).Error; err != nil {
				return database.Translate(err)
			}
		}
		return database.Translate(tx.Model(&Server{}).Where("id = ?", id).Updates(map[string]any{
			"tool_count": count, "last_synced_at": now, "last_error": "",
		}).Error)
	})
	if err != nil {
		return nil, err
	}
	return s.ListServerTools(ctx, id)
}

// mergeTool computes the column changes for an existing tool.
func mergeTool(cur *Tool, d discovered, overwrite bool) map[string]any {
	display, desc := cur.DisplayName, cur.Description
	var customized bool
	switch {
	case overwrite:
		display, desc, customized = d.display, d.description, false
	case cur.MetadataCustomized == nil:
		customized = cur.DisplayName != d.display || cur.Description != d.description
	case *cur.MetadataCustomized:
		customized = true
	default:
		display, desc, customized = d.display, d.description, false
	}
	att := cur.attachment()
	if !attachmentValid(att, d.schema) {
		att = attachment{Mode: AttachNone}
	}
	return map[string]any{
		"display_name":               display,
		"description":                desc,
		"metadata_customized":        customized,
		"input_schema_json":          d.schema,
		"attachment_input_mode":      att.Mode,
		"attachment_argument":        att.Argument,
		"attachment_encoding":        att.Encoding,
		"attachment_prompt_argument": att.Prompt,
	}
}

// recordSyncFailure stores only the fixed public summary: remote details such
// as URLs, tokens or bodies must never be persisted.
func (s *Service) recordSyncFailure(ctx context.Context, id uint, cause error) {
	detail := "transport"
	var se *StatusError
	var re *RPCError
	switch {
	case errors.As(cause, &se):
		detail = fmt.Sprintf("http_status_%d", se.Code)
	case errors.As(cause, &re):
		detail = "jsonrpc_error"
	case errors.Is(cause, context.DeadlineExceeded):
		detail = "timeout"
	}
	if s.log != nil {
		s.log.Warn("mcp_sync_failed", logx.F("server_id", id), logx.F("reason", detail))
	}
	err := s.db.WithContext(ctx).Model(&Server{}).Where("id = ?", id).Update("last_error", syncFailureSummary).Error
	if err != nil && s.log != nil {
		s.log.Error("mcp_sync_failure_record_failed", logx.F("server_id", id))
	}
}
