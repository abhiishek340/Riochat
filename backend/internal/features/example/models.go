// Package example is the reference feature area. It is NOT registered in
// internal/registry/registry.go; it exists to show the conventions end to end and to exercise the
// platform in tests. Copy it as the starting point for a real area.
package example

import "github.com/riochat/riochat/backend/internal/platform/database"

// Example is a row owned by one user.
type Example struct {
	database.Model
	OwnerID uint   `gorm:"not null;index" json:"ownerId"`
	Title   string `gorm:"size:128;not null" json:"title"`
}

// TableName declares the table; the prefix must be a permitted domain prefix.
func (Example) TableName() string { return "user_examples" }
