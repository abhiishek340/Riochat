// Package errorlog keeps a durable, administrator-only record of server-side
// request failures so that an error ID shown to a user can later be traced to
// the real underlying reason.
package errorlog

import "time"

// Entry is one persisted failed request. The table is append-only: there is no
// soft delete and no update timestamp. RequestID is deliberately not unique.
type Entry struct {
	ID         uint      `gorm:"primaryKey"`
	RequestID  string    `gorm:"column:request_id;size:64;not null;default:'';index"`
	TraceID    string    `gorm:"column:trace_id;size:64;not null;default:''"`
	UserID     uint      `gorm:"column:user_id;not null;default:0;index"`
	Method     string    `gorm:"column:method;size:16;not null;default:''"`
	Route      string    `gorm:"column:route;size:255;not null;default:''"`
	Path       string    `gorm:"column:path;size:512;not null;default:''"`
	StatusCode int       `gorm:"column:status_code;not null;default:0;index"`
	ErrorCode  string    `gorm:"column:error_code;size:128;not null;default:'';index"`
	Cause      string    `gorm:"column:cause;type:text;not null;default:''"`
	Detail     string    `gorm:"column:detail;type:text;not null;default:''"`
	LatencyMS  int64     `gorm:"column:latency_ms;not null;default:0"`
	ClientIP   string    `gorm:"column:client_ip;size:64;not null;default:''"`
	UserAgent  string    `gorm:"column:user_agent;size:255;not null;default:''"`
	CreatedAt  time.Time `gorm:"column:created_at;not null;index"`
}

// TableName declares the table.
func (Entry) TableName() string { return "system_error_logs" }

// entryView is the JSON shape returned to administrators.
type entryView struct {
	ID              uint      `json:"id"`
	RequestID       string    `json:"requestID"`
	TraceID         string    `json:"traceID"`
	UserID          uint      `json:"userID"`
	UserUsername    string    `json:"userUsername"`
	UserDisplayName string    `json:"userDisplayName"`
	UserLabel       string    `json:"userLabel"`
	Method          string    `json:"method"`
	Route           string    `json:"route"`
	Path            string    `json:"path"`
	StatusCode      int       `json:"statusCode"`
	ErrorCode       string    `json:"errorCode"`
	Cause           string    `json:"cause"`
	Detail          string    `json:"detail"`
	LatencyMS       int64     `json:"latencyMS"`
	ClientIP        string    `json:"clientIP"`
	UserAgent       string    `json:"userAgent"`
	CreatedAt       time.Time `json:"createdAt"`
}
