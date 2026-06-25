// Package memory implements the long-term user memory area: a small set of
// named facts or instructions per user that the chat system injects into model
// requests. It offers three authenticated HTTP endpoints, a Service other areas
// call directly, optional vector indexing for semantic recall, and a handful of
// pure helpers that turn stored entries into prompt material.
package memory

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Scopes an entry may carry when written through the HTTP API.
const (
	ScopeProfile    = "profile"
	ScopePreference = "preference"
	ScopeCustom     = "custom"
)

// Limits.
const (
	// MaxKeyChars is the longest memory key, counted in characters.
	MaxKeyChars = 128
	// MaxValueChars is the longest value accepted by the HTTP API.
	MaxValueChars = 10000
	// MaxEntriesPerUser caps the live entries of one user.
	MaxEntriesPerUser = 500

	maxScopeChars     = 32
	maxUpdatedByChars = 32
	maxSignatureChars = 64

	// UpdatedByUser is the origin label recorded for HTTP writes.
	UpdatedByUser = "user"
)

// Memory is one stored entry (table user_memories).
type Memory struct {
	database.Model
	UserID             uint   `gorm:"column:user_id;not null;index"`
	MemoryKey          string `gorm:"column:memory_key;size:128;not null"`
	Value              string `gorm:"column:value;type:text;not null;default:''"`
	Scope              string `gorm:"column:scope;size:32;not null;default:'';index"`
	UpdatedBy          string `gorm:"column:updated_by;size:32;not null;default:''"`
	EmbeddingSignature string `gorm:"column:embedding_signature;size:64;not null;default:'';index"`
}

// TableName is the entries table.
func (Memory) TableName() string { return "user_memories" }

// vectorRow holds the embedding of one entry. The vector is a packed sequence
// of little-endian float32 values. A row exists only while the entry's own
// embedding_signature matches the row's.
type vectorRow struct {
	MemoryID  uint   `gorm:"column:memory_id;primaryKey;autoIncrement:false"`
	UserID    uint   `gorm:"column:user_id;not null;index"`
	Signature string `gorm:"column:embedding_signature;size:64;not null;default:'';index"`
	Embedding []byte `gorm:"column:embedding;not null"`
}

// TableName is the companion vector table.
func (vectorRow) TableName() string { return "user_memory_embeddings" }

// Entry is the public shape of a memory: what the list endpoint returns and
// what Service methods hand to other areas. The embedding state is never part
// of it.
type Entry struct {
	ID        uint      `json:"id"`
	UserID    uint      `json:"userID"`
	MemoryKey string    `json:"memoryKey"`
	Value     string    `json:"value"`
	Scope     string    `json:"scope"`
	UpdatedBy string    `json:"updatedBy"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

func toEntry(m Memory) Entry {
	return Entry{
		ID: m.ID, UserID: m.UserID, MemoryKey: m.MemoryKey, Value: m.Value,
		Scope: m.Scope, UpdatedBy: m.UpdatedBy, CreatedAt: m.CreatedAt, UpdatedAt: m.UpdatedAt,
	}
}

func validScope(s string) bool {
	return s == ScopeProfile || s == ScopePreference || s == ScopeCustom
}
