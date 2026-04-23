package audit

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const persistTimeout = 5 * time.Second

// Recorder writes audit entries. A nil *Recorder is valid and records nothing,
// which is how callers behave when auditing is not configured.
type Recorder struct {
	db  *gorm.DB
	log *logx.Logger
	clk clock.Clock
}

// NewRecorder builds a recorder. log receives the "audit" stream and the
// persistence failures; clk stamps the rows.
func NewRecorder(db *gorm.DB, log *logx.Logger, clk clock.Clock) *Recorder {
	if log == nil {
		log = logx.Nop()
	}
	if clk == nil {
		clk = clock.System{}
	}
	return &Recorder{db: db, log: log, clk: clk}
}

// Record stores one entry and emits the matching log line. It never returns an
// error and never panics: a failed insert is logged and swallowed so the
// audited business operation is not affected. Call it only after the operation
// succeeded.
func (r *Recorder) Record(ctx context.Context, e Entry) {
	if r == nil {
		return
	}
	// Auditing must never take the business operation down with it.
	defer func() { _ = recover() }()
	state := reqctx.From(ctx)
	row := normalize(e, r.clk.Now())

	r.log.Info("audit",
		logx.F("trace_id", state.TraceID()),
		logx.F("request_id", row.RequestID),
		logx.F("user_id", row.ActorUserID),
		logx.F("action", row.Action),
		logx.F("resource", row.Resource),
		logx.F("resource_id", row.ResourceID),
		logx.F("ip", row.IP),
		logx.F("user_agent", row.UserAgent),
		logx.F("detail", row.DetailJSON),
	)

	if r.db == nil {
		return
	}
	// The insert must survive the end of the request that triggered it.
	wctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), persistTimeout)
	defer cancel()
	if err := r.db.WithContext(wctx).Create(&row).Error; err != nil {
		r.log.Error("audit_persist_failed",
			logx.F("trace_id", state.TraceID()),
			logx.F("request_id", row.RequestID),
			logx.F("action", row.Action),
			logx.Err(err),
		)
	}
}

// RecordRequest records an entry for the authenticated caller of c, taking the
// request id, client address and user agent from the request.
func (r *Recorder) RecordRequest(c *gin.Context, action, resource, resourceID string, detail any) {
	if r == nil {
		return
	}
	state := reqctx.FromRequest(c.Request)
	e := Entry{
		RequestID:  state.RequestID(),
		IP:         state.ClientIP(),
		UserAgent:  c.Request.UserAgent(),
		Action:     action,
		Resource:   resource,
		ResourceID: resourceID,
		Detail:     detail,
	}
	if p, ok := middleware.Principal(c); ok {
		e.ActorUserID = p.UserID
	}
	r.Record(c.Request.Context(), e)
}

// normalize trims and truncates the entry into a row.
func normalize(e Entry, now time.Time) Log {
	now = now.UTC()
	return Log{
		Model:       modelAt(now),
		RequestID:   fit(e.RequestID, maxRequestID),
		ActorUserID: e.ActorUserID,
		Action:      fit(e.Action, maxAction),
		Resource:    fit(e.Resource, maxResource),
		ResourceID:  fit(e.ResourceID, maxResourceID),
		IP:          fit(e.IP, maxIP),
		UserAgent:   fit(e.UserAgent, maxUserAgent),
		DetailJSON:  encodeDetail(e.Detail),
	}
}

// fit trims whitespace and cuts the value to at most max characters (runes),
// matching how varchar(n) columns count length. A value that is not valid
// UTF-8 is cut at a rune boundary of its valid prefix.
func fit(s string, max int) string {
	s = strings.TrimSpace(s)
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	n := 0
	for i := range s {
		if n == max {
			return strings.TrimRightFunc(s[:i], unicode.IsSpace)
		}
		n++
	}
	return s
}

// encodeDetail renders detail as compact JSON text; absent or unserializable
// values become "{}".
func encodeDetail(detail any) (out string) {
	const empty = "{}"
	if detail == nil {
		return empty
	}
	defer func() {
		if recover() != nil {
			out = empty
		}
	}()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(detail); err != nil {
		return empty
	}
	s := strings.TrimSpace(buf.String())
	if s == "" || s == "null" {
		return empty
	}
	return s
}

// Redacted replaces a sensitive value in audit details.
const Redacted = "[REDACTED]"

// RedactValue returns the value to put into an audit detail: sensitive
// non-empty values become "[REDACTED]", sensitive empty values stay empty (so a
// cleared secret is distinguishable from a set one), others pass through.
func RedactValue(value string, sensitive bool) string {
	if sensitive && value != "" {
		return Redacted
	}
	return value
}
