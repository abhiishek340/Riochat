// Package announcement implements site-wide notices: administrators publish
// Markdown announcements, and signed-in users see the ones currently in effect
// and may hide them for the day or close them. A user's choice only applies to
// the announcement version (its last-modified instant) that was on screen.
package announcement

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Allowed values of Announcement.Status.
const (
	StatusActive   = "active"
	StatusInactive = "inactive"
)

// Allowed values of Announcement.Type.
const (
	TypeCritical = "critical"
	TypeWarning  = "warning"
	TypeInfo     = "info"
	TypeNormal   = "normal"
	TypeGeneral  = "general"
)

// Size limits, counted in characters (runes).
const (
	maxTitleChars   = 120
	maxContentChars = 20000
)

// versionPrecision is the resolution at which version instants are stored and
// compared, so that a value survives a round trip through every supported
// database (PostgreSQL keeps microseconds).
const versionPrecision = time.Microsecond

// Announcement is one published notice. UpdatedAt doubles as its version.
type Announcement struct {
	database.Model
	Title           string     `gorm:"size:120;not null;default:''"`
	ContentMarkdown string     `gorm:"type:text;not null;default:''"`
	Status          string     `gorm:"size:32;not null;default:active;index"`
	Type            string     `gorm:"size:32;not null;default:general;index"`
	Pinned          bool       `gorm:"not null;default:false;index"`
	Priority        int        `gorm:"not null;default:0;index"`
	StartsAt        *time.Time `gorm:"index"`
	ExpiresAt       *time.Time `gorm:"index"`
	CreatedByUserID uint       `gorm:"not null;default:0;index"`
}

// TableName implements database.Tabler.
func (Announcement) TableName() string { return "system_announcements" }

// inEffect reports whether the announcement is visible to ordinary users at t:
// active, started (inclusive) and not yet expired (exclusive).
func (a *Announcement) inEffect(t time.Time) bool {
	if a.Status != StatusActive {
		return false
	}
	if a.StartsAt != nil && a.StartsAt.After(t) {
		return false
	}
	if a.ExpiresAt != nil && !a.ExpiresAt.After(t) {
		return false
	}
	return true
}

// UserState records what one user chose for one version of one announcement.
type UserState struct {
	database.Model
	AnnouncementID        uint       `gorm:"not null;index;uniqueIndex:idx_announcement_user_states_version,priority:1"`
	UserID                uint       `gorm:"not null;index;uniqueIndex:idx_announcement_user_states_version,priority:2"`
	AnnouncementUpdatedAt time.Time  `gorm:"not null;uniqueIndex:idx_announcement_user_states_version,priority:3"`
	DismissedUntil        *time.Time `gorm:"index"`
	ClosedAt              *time.Time `gorm:"index"`
}

// TableName implements database.Tabler.
func (UserState) TableName() string { return "announcement_user_states" }
