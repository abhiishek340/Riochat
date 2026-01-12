package database

import (
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Model is the standard base for soft-deleted tables: id, created_at,
// updated_at and an indexed deleted_at. Embed it in feature models.
type Model struct {
	ID        uint           `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time      `json:"createdAt"`
	UpdatedAt time.Time      `json:"updatedAt"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
}

// HardModel is the base for hard-delete tables (no deleted_at).
type HardModel struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// TablePrefixes lists the permitted domain prefixes. Every table name is
// "<prefix>_<noun>" (or "<prefix>s" for the few single-noun tables) and every
// model declares its table name explicitly through TableName().
var TablePrefixes = []string{
	"announcement", "audit", "billing", "chat", "content_moderation", "file",
	"identity", "knowledge_base", "llm", "mcp", "permission_group", "prompt",
	"skill", "system", "ui", "user",
}

// CheckTableName validates a table name against TablePrefixes.
func CheckTableName(name string) error {
	for _, p := range TablePrefixes {
		if name == p || name == p+"s" || strings.HasPrefix(name, p+"_") {
			return nil
		}
	}
	return fmt.Errorf("table %q does not start with a permitted domain prefix (%s)", name, strings.Join(TablePrefixes, ", "))
}

// Tabler is implemented by models that declare their table name.
type Tabler interface{ TableName() string }
