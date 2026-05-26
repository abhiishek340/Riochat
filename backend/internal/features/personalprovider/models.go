package personalprovider

import (
	"encoding/json"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Provider statuses and sources.
const (
	StatusActive    = "active"
	StatusDisabled  = "disabled"
	StatusSuspended = "suspended"

	SourceManual = "manual"
	SourceLink   = "link"
)

// Provider is one user-owned model provider (table llm_user_providers). Rows
// are hard-deleted.
type Provider struct {
	database.HardModel
	PublicID        string     `gorm:"column:public_id;size:32;not null;uniqueIndex:uidx_llm_user_providers_public_id"`
	UserID          uint       `gorm:"column:user_id;not null;index:idx_llm_user_providers_user"`
	Name            string     `gorm:"column:name;size:64;not null"`
	Icon            string     `gorm:"column:icon;size:64;not null;default:''"`
	Protocol        string     `gorm:"column:protocol;size:64;not null"`
	BaseURL         string     `gorm:"column:base_url;size:512;not null"`
	Host            string     `gorm:"column:host;size:255;not null;index:idx_llm_user_providers_host"`
	APIKeyEncrypted string     `gorm:"column:api_key_encrypted;type:text;not null"`
	KeyHint         string     `gorm:"column:key_hint;size:32;not null"`
	EnabledModels   string     `gorm:"column:enabled_models;type:text;not null;default:'[]'"`
	Status          string     `gorm:"column:status;size:16;not null;default:'active';index:idx_llm_user_providers_status"`
	Source          string     `gorm:"column:source;size:16;not null;default:'manual'"`
	LastError       string     `gorm:"column:last_error;size:512;not null;default:''"`
	LastCheckedAt   *time.Time `gorm:"column:last_checked_at"`
}

// TableName implements database.Tabler.
func (Provider) TableName() string { return "llm_user_providers" }

// settingRow mirrors the shared system_settings table. The settings area owns
// that table; this package only reads its own namespace and declares the model
// so that the table exists when this area runs alone.
type settingRow struct {
	ID          uint      `gorm:"primaryKey"`
	Namespace   string    `gorm:"column:namespace;size:32;not null"`
	Key         string    `gorm:"column:key;size:128;not null"`
	Value       string    `gorm:"column:value;type:text;not null"`
	ValueType   string    `gorm:"column:value_type;size:16;not null"`
	Description string    `gorm:"column:description;size:255;not null;default:''"`
	CreatedAt   time.Time `gorm:"column:created_at"`
	UpdatedAt   time.Time `gorm:"column:updated_at"`
}

// TableName implements database.Tabler.
func (settingRow) TableName() string { return "system_settings" }

// EnabledModel is one model the owner enabled, with the protocols it is served
// through.
type EnabledModel struct {
	Name      string   `json:"name"`
	Protocols []string `json:"protocols"`
}

// decodeModels reads the stored enabled-models column. Plain strings (legacy
// rows) and objects without protocols receive the provider protocol; entries
// with an empty name or of unknown shape are skipped; an unreadable column
// yields an empty list. The result is never nil.
func decodeModels(raw, providerProtocol string) []EnabledModel {
	out := []EnabledModel{}
	var items []json.RawMessage
	if err := json.Unmarshal([]byte(strings.TrimSpace(raw)), &items); err != nil {
		return out
	}
	for _, item := range items {
		var name string
		if err := json.Unmarshal(item, &name); err == nil {
			if name = strings.TrimSpace(name); name != "" {
				out = append(out, EnabledModel{Name: name, Protocols: []string{providerProtocol}})
			}
			continue
		}
		var obj struct {
			Name      string   `json:"name"`
			Protocols []string `json:"protocols"`
		}
		if err := json.Unmarshal(item, &obj); err != nil {
			continue
		}
		obj.Name = strings.TrimSpace(obj.Name)
		if obj.Name == "" {
			continue
		}
		protos := make([]string, 0, len(obj.Protocols))
		for _, p := range obj.Protocols {
			if p = strings.TrimSpace(p); p != "" {
				protos = append(protos, p)
			}
		}
		if len(protos) == 0 {
			protos = []string{providerProtocol}
		}
		out = append(out, EnabledModel{Name: obj.Name, Protocols: protos})
	}
	return out
}

func encodeModels(models []EnabledModel) string {
	if len(models) == 0 {
		return "[]"
	}
	raw, err := json.Marshal(models)
	if err != nil {
		return "[]"
	}
	return string(raw)
}

// Models returns the decoded enabled models of the row.
func (p Provider) Models() []EnabledModel { return decodeModels(p.EnabledModels, p.Protocol) }
