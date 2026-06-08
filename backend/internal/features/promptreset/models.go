// Package promptreset stores reusable slash-command prompt blocks. Builtin
// presets are maintained by administrators and visible to everyone; user
// presets belong to one account. Expansion of a preset inside a message is a
// client concern and never happens here.
package promptreset

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Scope values stored in the scope column.
const (
	ScopeBuiltin = "builtin"
	ScopeUser    = "user"
)

// Field limits, counted in Unicode code points.
const (
	maxTitle       = 64
	maxTrigger     = 64
	maxDescription = 256
	maxContent     = 10000
)

// PromptPreset is one row of prompt_presets. The table has no soft-delete
// column: deleting a preset removes the row.
type PromptPreset struct {
	database.HardModel
	Scope           string `gorm:"column:scope;size:32;not null;default:'user';index;uniqueIndex:ux_prompt_presets_scope_owner_trigger,priority:1"`
	OwnerUserID     uint   `gorm:"column:owner_user_id;not null;default:0;index;uniqueIndex:ux_prompt_presets_scope_owner_trigger,priority:2"`
	Title           string `gorm:"column:title;size:64;not null;default:''"`
	Trigger         string `gorm:"column:trigger;size:64;not null;default:'';index;uniqueIndex:ux_prompt_presets_scope_owner_trigger,priority:3"`
	Description     string `gorm:"column:description;size:256;not null;default:''"`
	Content         string `gorm:"column:content;type:text;not null;default:''"`
	Enabled         bool   `gorm:"column:enabled;not null;default:true;index"`
	SortOrder       int    `gorm:"column:sort_order;type:integer;not null;default:0;index"`
	CreatedByUserID uint   `gorm:"column:created_by_user_id;not null;default:0"`
	UpdatedByUserID uint   `gorm:"column:updated_by_user_id;not null;default:0"`
}

// TableName is the preset table.
func (PromptPreset) TableName() string { return "prompt_presets" }

// view is the wire shape; the owner is intentionally absent.
type view struct {
	ID              uint      `json:"id"`
	Scope           string    `json:"scope"`
	Title           string    `json:"title"`
	Trigger         string    `json:"trigger"`
	Description     string    `json:"description"`
	Content         string    `json:"content"`
	Enabled         bool      `json:"enabled"`
	SortOrder       int       `json:"sortOrder"`
	CreatedByUserID uint      `json:"createdByUserID"`
	UpdatedByUserID uint      `json:"updatedByUserID"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func toView(p *PromptPreset) view {
	return view{
		ID: p.ID, Scope: p.Scope, Title: p.Title, Trigger: p.Trigger, Description: p.Description,
		Content: p.Content, Enabled: p.Enabled, SortOrder: p.SortOrder,
		CreatedByUserID: p.CreatedByUserID, UpdatedByUserID: p.UpdatedByUserID,
		CreatedAt: p.CreatedAt.Local(), UpdatedAt: p.UpdatedAt.Local(),
	}
}

func toViews(rows []PromptPreset) []view {
	out := make([]view, len(rows))
	for i := range rows {
		out[i] = toView(&rows[i])
	}
	return out
}
