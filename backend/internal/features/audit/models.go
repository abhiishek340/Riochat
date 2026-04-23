// Package audit keeps the append-only record of security-relevant and
// administrative actions. It offers three things to the rest of the program:
//
//   - Recorder: a nil-safe, fire-and-forget writer other areas call after an
//     audited operation succeeded.
//   - The admin read endpoint GET /admin/audit-logs.
//   - Cleanup helpers (validation, purge, audit entry) for the shared
//     POST /admin/logs/cleanup endpoint, which the integrator mounts once for
//     all log types.
package audit

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Column limits. Values longer than these are truncated before insert so a
// long caller-supplied value can never make the write fail.
const (
	maxRequestID  = 64
	maxAction     = 128
	maxResource   = 128
	maxResourceID = 128
	maxIP         = 64
	maxUserAgent  = 255
)

// Log is one audit record.
type Log struct {
	database.Model
	RequestID   string `gorm:"column:request_id;size:64;not null;default:'';index"`
	ActorUserID uint   `gorm:"column:actor_user_id;not null;index"`
	Action      string `gorm:"column:action;size:128;not null;default:'';index"`
	Resource    string `gorm:"column:resource;size:128;not null;default:'';index"`
	ResourceID  string `gorm:"column:resource_id;size:128;not null;default:'';index"`
	IP          string `gorm:"column:ip;size:64;not null;default:''"`
	UserAgent   string `gorm:"column:user_agent;size:255;not null;default:''"`
	DetailJSON  string `gorm:"column:detail_json;type:text;not null;default:''"`
}

// TableName is the audit table.
func (Log) TableName() string { return "audit_logs" }

// Entry is the input of Recorder.Record.
type Entry struct {
	RequestID   string
	ActorUserID uint
	Action      string
	Resource    string
	ResourceID  string
	IP          string
	UserAgent   string
	// Detail is serialized to compact JSON; nil (or anything that cannot be
	// serialized) is stored as {}.
	Detail any
}

// LogView is one element of the list response.
type LogView struct {
	ID               uint      `json:"id"`
	RequestID        string    `json:"requestID"`
	ActorUserID      uint      `json:"actorUserID"`
	ActorUsername    string    `json:"actorUsername"`
	ActorDisplayName string    `json:"actorDisplayName"`
	ActorLabel       string    `json:"actorLabel"`
	Action           string    `json:"action"`
	Resource         string    `json:"resource"`
	ResourceID       string    `json:"resourceID"`
	IP               string    `json:"ip"`
	UserAgent        string    `json:"userAgent"`
	DetailJSON       string    `json:"detailJSON"`
	CreatedAt        time.Time `json:"createdAt"`
	UpdatedAt        time.Time `json:"updatedAt"`
}

func modelAt(t time.Time) database.Model {
	return database.Model{CreatedAt: t, UpdatedAt: t}
}
