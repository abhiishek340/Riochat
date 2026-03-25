package settings

import "time"

// Index names. The unique index on system_settings is shared with the other
// areas that declare a partial mapping of the same table, so the name matters.
const (
	systemSettingsUniqueIndex = "ux_system_settings_ns_key"
	userSettingsUniqueIndex   = "uidx_user_settings_user_key"
)

// quotedKey is the key column as written in raw SQL fragments. Double quotes
// are a valid identifier quote on both SQLite and PostgreSQL.
const quotedKey = `"key"`

// SystemSetting is one administrator-managed configuration row. The pair
// (namespace, key) identifies the setting; sensitive entries hold ciphertext.
type SystemSetting struct {
	ID          uint      `gorm:"primaryKey" json:"id"`
	Namespace   string    `gorm:"column:namespace;size:32;not null;uniqueIndex:ux_system_settings_ns_key" json:"namespace"`
	Key         string    `gorm:"column:key;size:128;not null;uniqueIndex:ux_system_settings_ns_key" json:"key"`
	Value       string    `gorm:"column:value;type:text;not null" json:"value"`
	ValueType   string    `gorm:"column:value_type;size:16;not null" json:"valueType"`
	Description string    `gorm:"column:description;size:255;not null;default:''" json:"description"`
	CreatedAt   time.Time `gorm:"column:created_at" json:"createdAt"`
	UpdatedAt   time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName declares the table.
func (SystemSetting) TableName() string { return "system_settings" }

// UserSetting is one personal preference of one user. The key carries its own
// prefix (for example "chat.file_mode").
type UserSetting struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	UserID    uint      `gorm:"column:user_id;not null;uniqueIndex:uidx_user_settings_user_key,priority:1;index:idx_user_settings_user_id" json:"userId"`
	Key       string    `gorm:"column:key;size:64;not null;uniqueIndex:uidx_user_settings_user_key,priority:2" json:"key"`
	Value     string    `gorm:"column:value;type:text;not null;default:''" json:"value"`
	UpdatedAt time.Time `gorm:"column:updated_at" json:"updatedAt"`
}

// TableName declares the table.
func (UserSetting) TableName() string { return "user_settings" }
