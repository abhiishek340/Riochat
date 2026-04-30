package errorlog

import (
	"context"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/redact"
)

// Byte limits applied to every stored text field.
const (
	limitRequestID = 64
	limitTraceID   = 64
	limitMethod    = 16
	limitRoute     = 255
	limitPath      = 512
	limitErrorCode = 128
	limitCause     = 4096
	limitDetail    = 16 * 1024
	limitClientIP  = 64
	limitUserAgent = 255

	// maxInFlight caps concurrent database writes; further records are dropped.
	maxInFlight = 32
	// writeTimeout bounds one write.
	writeTimeout = 5 * time.Second
)

// spawnFunc runs fn detached from the request's cancellation while keeping the
// context values, bounded by timeout.
type spawnFunc func(ctx context.Context, timeout time.Duration, fn func(context.Context))

// recorder implements middleware.ErrorRecorder. It never blocks the request:
// the write happens on a detached task and is shed when too many are running.
type recorder struct {
	log     *logx.Logger
	clk     clock.Clock
	spawn   spawnFunc
	insert  func(ctx context.Context, e *Entry) error
	slots   chan struct{}
	dropped atomic.Int64
}

func newRecorder(db *gorm.DB, log *logx.Logger, clk clock.Clock, spawn spawnFunc) *recorder {
	return &recorder{
		log:   log,
		clk:   clk,
		spawn: spawn,
		insert: func(ctx context.Context, e *Entry) error {
			return db.WithContext(ctx).Create(e).Error
		},
		slots: make(chan struct{}, maxInFlight),
	}
}

// Dropped reports how many records were discarded because the writer was
// saturated.
func (r *recorder) Dropped() int64 { return r.dropped.Load() }

// RecordError implements middleware.ErrorRecorder.
func (r *recorder) RecordError(ctx context.Context, rec middleware.ErrorRecord) {
	entry, ok := r.normalize(rec)
	if !ok {
		return
	}
	select {
	case r.slots <- struct{}{}:
	default:
		n := r.dropped.Add(1)
		if n == 1 || n%100 == 0 {
			r.log.Warn("error_log_dropped",
				logx.F("request_id", entry.RequestID), logx.F("dropped_total", n))
		}
		return
	}
	r.spawn(ctx, writeTimeout, func(wctx context.Context) {
		defer func() { <-r.slots }()
		if err := r.insert(wctx, &entry); err != nil {
			r.log.Warn("error_log_write_failed",
				logx.F("request_id", entry.RequestID), logx.Err(err))
		}
	})
}

// normalize converts a record into a storable row. It reports false when the
// record has no usable request ID and must be skipped.
func (r *recorder) normalize(rec middleware.ErrorRecord) (Entry, bool) {
	reqID := clip(rec.RequestID, limitRequestID)
	if reqID == "" {
		return Entry{}, false
	}
	at := rec.At
	if at.IsZero() {
		at = r.clk.Now()
	}
	return Entry{
		RequestID:  reqID,
		TraceID:    clip(rec.TraceID, limitTraceID),
		UserID:     rec.UserID,
		Method:     clip(rec.Method, limitMethod),
		Route:      clip(rec.Route, limitRoute),
		Path:       clip(maskPathQuery(rec.Path), limitPath),
		StatusCode: rec.Status,
		ErrorCode:  clip(rec.ErrorCode, limitErrorCode),
		Cause:      clip(redact.Text(rec.Cause), limitCause),
		Detail:     clip(redact.Text(rec.Detail), limitDetail),
		LatencyMS:  rec.LatencyMS,
		ClientIP:   clip(rec.ClientIP, limitClientIP),
		UserAgent:  clip(rec.UserAgent, limitUserAgent),
		CreatedAt:  at.UTC(),
	}, true
}

// maskPathQuery re-applies sensitive-query masking to a stored path so the row
// stays safe even when a caller hands over an unmasked query string.
func maskPathQuery(p string) string {
	p = strings.TrimSpace(p)
	i := strings.IndexByte(p, '?')
	if i < 0 {
		return p
	}
	return p[:i+1] + redact.Query(p[i+1:])
}

// clip trims whitespace and cuts s to at most max bytes on a character
// boundary, so the result is always valid UTF-8 text when the input was.
func clip(s string, max int) string {
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	cut := max
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// PurgeBefore permanently deletes every entry created strictly before the
// given instant and returns how many rows were removed. The log-cleanup area
// calls it for the "errors" type.
func PurgeBefore(ctx context.Context, db *gorm.DB, before time.Time) (int64, error) {
	res := db.WithContext(ctx).Where("created_at < ?", before.UTC()).Delete(&Entry{})
	return res.RowsAffected, res.Error
}
