// Package mcp lets administrators register external tool servers that speak the
// Model Context Protocol over HTTP, curates the tools they offer, and gives the
// chat runtime what it needs to offer those tools to a model, call them, and
// meter the calls.
package mcp

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Allowed values of Server.Status and Tool.Status.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// Allowed values of Tool.AttachmentInputMode.
const (
	AttachNone  = "none"
	AttachImage = "image"
)

// Allowed values of Tool.AttachmentEncoding.
const (
	EncodingNone    = ""
	EncodingBase64  = "base64"
	EncodingDataURL = "data_url"
)

// Server is a registered remote tool server.
type Server struct {
	database.HardModel
	Name         string     `gorm:"column:name;size:128;not null;default:''"`
	BaseURL      string     `gorm:"column:base_url;size:2048;not null;default:''"`
	AuthTokenEnc string     `gorm:"column:auth_token_enc;type:text;not null;default:''"`
	HeadersJSON  string     `gorm:"column:headers_json;type:text;not null;default:'{}'"`
	Status       string     `gorm:"column:status;size:32;not null;default:active;index:idx_mcp_servers_status"`
	SortOrder    int        `gorm:"column:sort_order;not null;default:0;index:idx_mcp_servers_sort_order"`
	ToolCount    int        `gorm:"column:tool_count;not null;default:0"`
	LastSyncedAt *time.Time `gorm:"column:last_synced_at"`
	LastError    string     `gorm:"column:last_error;type:text;not null;default:''"`
}

// TableName implements database.Tabler.
func (Server) TableName() string { return "mcp_servers" }

// Tool is one tool discovered on a server and curated by an administrator.
type Tool struct {
	database.HardModel
	ServerID                 uint   `gorm:"column:server_id;not null;default:0;index:idx_mcp_tools_server_id;uniqueIndex:uidx_mcp_tools_server_name,priority:1"`
	Name                     string `gorm:"column:name;size:160;not null;default:'';uniqueIndex:uidx_mcp_tools_server_name,priority:2"`
	DisplayName              string `gorm:"column:display_name;size:160;not null;default:''"`
	Description              string `gorm:"column:description;type:text;not null;default:''"`
	MetadataCustomized       *bool  `gorm:"column:metadata_customized"`
	InputSchemaJSON          string `gorm:"column:input_schema_json;type:text;not null;default:'{}'"`
	AttachmentInputMode      string `gorm:"column:attachment_input_mode;size:32;not null;default:none"`
	AttachmentArgument       string `gorm:"column:attachment_argument;size:128;not null;default:''"`
	AttachmentEncoding       string `gorm:"column:attachment_encoding;size:32;not null;default:''"`
	AttachmentPromptArgument string `gorm:"column:attachment_prompt_argument;size:128;not null;default:''"`
	PriceNanoUSD             int64  `gorm:"column:price_nanousd;not null;default:0"`
	Status                   string `gorm:"column:status;size:32;not null;default:inactive;index:idx_mcp_tools_status"`
	SortOrder                int    `gorm:"column:sort_order;not null;default:0;index:idx_mcp_tools_sort_order"`
}

// TableName implements database.Tabler.
func (Tool) TableName() string { return "mcp_tools" }

// attachment is the image-routing part of a tool's configuration.
type attachment struct {
	Mode     string
	Argument string
	Encoding string
	Prompt   string
}

func (t *Tool) attachment() attachment {
	return attachment{Mode: t.AttachmentInputMode, Argument: t.AttachmentArgument, Encoding: t.AttachmentEncoding, Prompt: t.AttachmentPromptArgument}
}

// IsImageProcessor reports whether the tool analyzes uploaded images.
func (t *Tool) IsImageProcessor() bool { return t.AttachmentInputMode == AttachImage }
