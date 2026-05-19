// Package uicomponent keeps the registry of interactive UI components the model
// may place in its replies, exposes the user and administrator management API,
// and produces the catalog text and carried-back UI state that the chat area
// adds to a turn's prompt. Components are an output format: nothing here
// executes renderer source.
package uicomponent

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Scope values stored in the scope column.
const (
	ScopeBuiltin  = "builtin"
	ScopePlatform = "platform"
	ScopeUser     = "user"
)

// Renderer kinds stored in renderer_kind.
const (
	RendererBuiltin = "builtin"
	RendererSandbox = "sandbox"
)

// Field limits. Text limits are in Unicode code points, the two source limits
// are in bytes.
const (
	maxName            = 64
	maxDescription     = 256
	maxPropsSummary    = 1024
	maxPropsSchemaSize = 16 * 1024
	maxRendererSize    = 256 * 1024
)

// UIComponent is one row of ui_components. The table keeps no soft-delete
// column; deleting a component removes the row.
type UIComponent struct {
	database.HardModel
	Scope           string `gorm:"column:scope;size:32;not null;default:'user';index;uniqueIndex:ux_ui_components_scope_owner_name,priority:1"`
	OwnerUserID     uint   `gorm:"column:owner_user_id;not null;default:0;index;uniqueIndex:ux_ui_components_scope_owner_name,priority:2"`
	Name            string `gorm:"column:name;size:64;not null;default:'';uniqueIndex:ux_ui_components_scope_owner_name,priority:3"`
	Version         int    `gorm:"column:version;not null"`
	Description     string `gorm:"column:description;size:256;not null;default:''"`
	PropsSummary    string `gorm:"column:props_summary;size:1024;not null;default:''"`
	PropsSchema     string `gorm:"column:props_schema;type:text;not null;default:''"`
	RendererKind    string `gorm:"column:renderer_kind;size:16;not null;default:'sandbox'"`
	RendererSource  string `gorm:"column:renderer_source;type:text;not null;default:''"`
	Enabled         bool   `gorm:"column:enabled;not null;index"`
	SortOrder       int    `gorm:"column:sort_order;not null;default:0;index"`
	CreatedByUserID uint   `gorm:"column:created_by_user_id;not null;default:0"`
	UpdatedByUserID uint   `gorm:"column:updated_by_user_id;not null;default:0"`
}

// TableName is the registry table.
func (UIComponent) TableName() string { return "ui_components" }

// view is the wire shape. The owner is intentionally absent.
type view struct {
	ID              uint      `json:"id"`
	Scope           string    `json:"scope"`
	Name            string    `json:"name"`
	Version         int       `json:"version"`
	Description     string    `json:"description"`
	PropsSummary    string    `json:"propsSummary"`
	PropsSchema     string    `json:"propsSchema"`
	RendererKind    string    `json:"rendererKind"`
	RendererSource  string    `json:"rendererSource"`
	Enabled         bool      `json:"enabled"`
	SortOrder       int       `json:"sortOrder"`
	CreatedByUserID uint      `json:"createdByUserID"`
	UpdatedByUserID uint      `json:"updatedByUserID"`
	CreatedAt       time.Time `json:"createdAt"`
	UpdatedAt       time.Time `json:"updatedAt"`
}

func toView(c *UIComponent) view {
	return view{
		ID: c.ID, Scope: c.Scope, Name: c.Name, Version: c.Version,
		Description: c.Description, PropsSummary: c.PropsSummary, PropsSchema: c.PropsSchema,
		RendererKind: c.RendererKind, RendererSource: c.RendererSource,
		Enabled: c.Enabled, SortOrder: c.SortOrder,
		CreatedByUserID: c.CreatedByUserID, UpdatedByUserID: c.UpdatedByUserID,
		CreatedAt: c.CreatedAt.Local(), UpdatedAt: c.UpdatedAt.Local(),
	}
}

func toViews(rows []UIComponent) []view {
	out := make([]view, len(rows))
	for i := range rows {
		out[i] = toView(&rows[i])
	}
	return out
}
