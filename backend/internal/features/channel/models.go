package channel

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Upstream is a remote LLM API endpoint (table llm_upstreams).
type Upstream struct {
	database.HardModel
	Name                 string `gorm:"column:name;size:128;not null"`
	BaseURL              string `gorm:"column:base_url;size:512;not null"`
	Compatible           string `gorm:"column:compatible;size:32;not null;default:openai;index"`
	ProtocolDefaultsJSON string `gorm:"column:protocol_defaults_json;type:text;not null;default:'{}'"`
	Status               string `gorm:"column:status;size:32;not null;default:active;index"`
	ConnectTimeoutMS     int    `gorm:"column:connect_timeout_ms;default:10000"`
	ReadTimeoutMS        int    `gorm:"column:read_timeout_ms;default:120000"`
	StreamIdleTimeoutMS  int    `gorm:"column:stream_idle_timeout_ms;default:60000"`
	APIKeysEnc           string `gorm:"column:api_keys_enc;type:text"`
	CBFailureThreshold   int    `gorm:"column:cb_failure_threshold;default:0"`
	CBModelThreshold     int    `gorm:"column:cb_model_threshold;default:0"`
	CBThresholdLogic     string `gorm:"column:cb_threshold_logic;size:8;default:or"`
	CBDurationMin        int    `gorm:"column:cb_duration_min;default:0"`
	CBWindowMin          int    `gorm:"column:cb_window_min;default:0"`
	HeadersJSON          string `gorm:"column:headers_json;type:text"`
}

func (Upstream) TableName() string { return "llm_upstreams" }

// PlatformModel is a user-facing catalog entry (table llm_platform_models).
type PlatformModel struct {
	database.HardModel
	Name               string `gorm:"column:name;size:128;not null;uniqueIndex"`
	Vendor             string `gorm:"column:vendor;size:64;index"`
	DisplayGroupID     *uint  `gorm:"column:display_group_id;index"`
	KindsJSON          string `gorm:"column:kinds_json;type:text;default:'[\"chat\"]'"`
	CapabilitiesJSON   string `gorm:"column:capabilities_json;type:text;default:'{}'"`
	SystemPrompt       string `gorm:"column:system_prompt;type:text"`
	AccessScope        string `gorm:"column:access_scope;size:32;default:public;index"`
	Icon               string `gorm:"column:icon;size:2048"`
	Description        string `gorm:"column:description;type:text"`
	CBPolicyMode       string `gorm:"column:cb_policy_mode;size:16;default:default"`
	CBFailureThreshold int    `gorm:"column:cb_failure_threshold;default:0"`
	CBDurationMin      int    `gorm:"column:cb_duration_min;default:0"`
	CBWindowMin        int    `gorm:"column:cb_window_min;default:0"`
	Status             string `gorm:"column:status;size:32;default:active;index"`
	SortOrder          int    `gorm:"column:sort_order;default:0;index"`
}

func (PlatformModel) TableName() string { return "llm_platform_models" }

// ModelVendor is a technical vendor of the catalog (table llm_model_vendors).
type ModelVendor struct {
	database.HardModel
	Key       string `gorm:"column:key;size:64;not null;uniqueIndex"`
	Name      string `gorm:"column:name;size:64;index"`
	Icon      string `gorm:"column:icon;size:2048;default:''"`
	BuiltIn   bool   `gorm:"column:built_in;default:false"`
	SortOrder int    `gorm:"column:sort_order;default:0;index"`
}

func (ModelVendor) TableName() string { return "llm_model_vendors" }

// ModelDisplayGroup is an optional presentation group (table llm_model_display_groups).
type ModelDisplayGroup struct {
	database.HardModel
	Name      string `gorm:"column:name;size:64;not null;uniqueIndex"`
	Icon      string `gorm:"column:icon;size:2048;default:''"`
	SortOrder int    `gorm:"column:sort_order;default:0;index"`
}

func (ModelDisplayGroup) TableName() string { return "llm_model_display_groups" }

// IconAsset is the metadata of an uploaded icon (table llm_model_icon_assets).
type IconAsset struct {
	database.HardModel
	PublicID          string     `gorm:"column:public_id;size:64;uniqueIndex"`
	SHA256            string     `gorm:"column:sha256;size:64;uniqueIndex"`
	StoragePath       string     `gorm:"column:storage_path;size:512"`
	ContentType       string     `gorm:"column:content_type;size:64"`
	SizeBytes         int64      `gorm:"column:size_bytes"`
	Width             int        `gorm:"column:width"`
	Height            int        `gorm:"column:height"`
	CreatedByUserID   uint       `gorm:"column:created_by_user_id;default:0;index"`
	ReadyAt           *time.Time `gorm:"column:ready_at;index"`
	LeaseExpiresAt    time.Time  `gorm:"column:lease_expires_at;not null;index"`
	UnreferencedAt    *time.Time `gorm:"column:unreferenced_at;index"`
	DeleteRequestedAt *time.Time `gorm:"column:delete_requested_at;index"`
	DeletingAt        *time.Time `gorm:"column:deleting_at;index"`
}

func (IconAsset) TableName() string { return "llm_model_icon_assets" }

// UpstreamModel is a real model name an upstream exposes (table llm_upstream_models).
type UpstreamModel struct {
	database.HardModel
	UpstreamID        uint       `gorm:"column:upstream_id;uniqueIndex:ux_llm_upstream_models_name"`
	BindingCode       string     `gorm:"column:binding_code;size:64;uniqueIndex"`
	UpstreamModelName string     `gorm:"column:upstream_model_name;size:256;uniqueIndex:ux_llm_upstream_models_name"`
	Vendor            string     `gorm:"column:vendor;size:64;index"`
	Icon              string     `gorm:"column:icon;size:64"`
	SuggestedProtocol string     `gorm:"column:suggested_protocol;size:64;default:'';index"`
	KindsJSON         string     `gorm:"column:kinds_json;type:text;default:'[\"chat\"]'"`
	Status            string     `gorm:"column:status;size:32;default:active;index"`
	Source            string     `gorm:"column:source;size:16;default:sync;index"`
	LastSyncedAt      *time.Time `gorm:"column:last_synced_at"`
	RawJSON           string     `gorm:"column:raw_json;type:text;default:'{}'"`
}

func (UpstreamModel) TableName() string { return "llm_upstream_models" }

// ModelRoute binds a platform model to an upstream model under one protocol
// (table llm_model_routes).
type ModelRoute struct {
	database.HardModel
	PlatformModelID    uint   `gorm:"column:platform_model_id;uniqueIndex:ux_llm_model_routes_binding;index"`
	UpstreamModelID    uint   `gorm:"column:upstream_model_id;uniqueIndex:ux_llm_model_routes_binding;index"`
	Protocol           string `gorm:"column:protocol;size:64;uniqueIndex:ux_llm_model_routes_binding;index"`
	Status             string `gorm:"column:status;size:32;default:active;index"`
	Priority           int    `gorm:"column:priority;default:1;index"`
	Weight             int    `gorm:"column:weight;default:1"`
	Source             string `gorm:"column:source;size:16;default:manual;index"`
	CBFailureThreshold int    `gorm:"column:cb_failure_threshold;default:0"`
	CBDurationMin      int    `gorm:"column:cb_duration_min;default:0"`
	CBWindowMin        int    `gorm:"column:cb_window_min;default:0"`
	HeadersJSON        string `gorm:"column:headers_json;type:text"`
}

func (ModelRoute) TableName() string { return "llm_model_routes" }

// PermissionGroup is a model access group (table permission_groups).
type PermissionGroup struct {
	database.HardModel
	Name                  string `gorm:"column:name;size:128;not null"`
	Description           string `gorm:"column:description;size:512"`
	IsDefault             bool   `gorm:"column:is_default;default:false"`
	RateMultiplierPercent int    `gorm:"column:rate_multiplier_percent;not null;default:100"`
}

func (PermissionGroup) TableName() string { return "permission_groups" }

// PermissionGroupModelAccess manually grants a group access to a model.
type PermissionGroupModelAccess struct {
	GroupID         uint `gorm:"column:group_id;primaryKey;autoIncrement:false"`
	PlatformModelID uint `gorm:"column:platform_model_id;primaryKey;autoIncrement:false;index"`
}

func (PermissionGroupModelAccess) TableName() string { return "permission_group_model_access" }

// PermissionGroupModelRule grants a group access to models matching a rule.
type PermissionGroupModelRule struct {
	GroupID  uint   `gorm:"column:group_id;primaryKey;autoIncrement:false"`
	RuleType string `gorm:"column:rule_type;size:32;primaryKey"`
	Value    string `gorm:"column:value;size:128;primaryKey"`
}

func (PermissionGroupModelRule) TableName() string { return "permission_group_model_rules" }

// PermissionGroupUserAccess puts a user in a group.
type PermissionGroupUserAccess struct {
	GroupID uint `gorm:"column:group_id;primaryKey;autoIncrement:false"`
	UserID  uint `gorm:"column:user_id;primaryKey;autoIncrement:false;index"`
}

func (PermissionGroupUserAccess) TableName() string { return "permission_group_user_access" }

// settingRow maps the shared system_settings table, which the settings area
// owns and migrates. The channel area reads and edits only namespace "llm".
type settingRow struct {
	ID          uint      `gorm:"column:id;primaryKey"`
	Namespace   string    `gorm:"column:namespace;size:32;not null;uniqueIndex:ux_system_settings_ns_key"`
	Key         string    `gorm:"column:key;size:128;not null;uniqueIndex:ux_system_settings_ns_key"`
	Value       string    `gorm:"column:value;type:text;not null"`
	ValueType   string    `gorm:"column:value_type;size:16;not null"`
	Description string    `gorm:"column:description;size:255;not null;default:''"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

func (settingRow) TableName() string { return "system_settings" }

// Rule types of permission_group_model_rules.
const (
	RuleAll      = "all"
	RuleVendor   = "vendor"
	RuleProtocol = "protocol"
	RuleUpstream = "upstream"
)

// Upstream-model and route sources.
const (
	SourceManual = "manual"
	SourceSync   = "sync"
	SourceImport = "import"
)
