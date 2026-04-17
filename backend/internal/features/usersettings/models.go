// Package usersettings stores per-user preference values as string key/value
// pairs and serves them through GET and PATCH /api/v1/user/settings.
package usersettings

import "time"

// UserSetting is one stored preference. The key already carries its namespace
// prefix (for example "chat.file_mode"). There is no created-at column.
type UserSetting struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"not null;uniqueIndex:uidx_user_settings_user_key,priority:1;index:idx_user_settings_user_id"`
	Key       string    `gorm:"column:key;size:64;not null;uniqueIndex:uidx_user_settings_user_key,priority:2"`
	Value     string    `gorm:"type:text;not null;default:''"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName declares the table.
func (UserSetting) TableName() string { return "user_settings" }
