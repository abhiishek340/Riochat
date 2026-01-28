package middleware

import (
	"fmt"
	"net/http"
	"runtime/debug"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/redact"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// AccessLogOptions configures AccessLog.
type AccessLogOptions struct {
	Logger *logx.Logger
	Clock  clock.Clock
	// Recorder persists server-side failures for administrators (optional).
	Recorder ErrorRecorder
}

// AccessLog writes one multi-line log message per /api/ request after it
// completes and hands server-side failures to the ErrorRecorder.
//
// Level: 5xx is error; 4xx, or any request that produced an error code or a
// recorded cause (a stream failing after its 200 header), is warn; the rest is
// info. Query values and error text are redacted before logging or persisting.
func AccessLog(opts AccessLogOptions) gin.HandlerFunc {
	log := opts.Logger
	if log == nil {
		log = logx.Nop()
	}
	clk := opts.Clock
	if clk == nil {
		clk = clock.System{}
	}
	return func(c *gin.Context) {
		path := c.Request.URL.Path
		if !strings.HasPrefix(path, "/api/") {
			c.Next()
			return
		}
		start := clk.Now()
		completed := false
		// A panic skips the code after Next; the deferred call still logs the
		// request (as a 500) and leaves persistence to the Recovery middleware.
		defer func() {
			if completed {
				return
			}
			emit(c, log, opts.Recorder, clk, start, true)
		}()
		c.Next()
		completed = true
		emit(c, log, opts.Recorder, clk, start, false)
	}
}

func emit(c *gin.Context, log *logx.Logger, rec ErrorRecorder, clk clock.Clock, start time.Time, panicked bool) {
	end := clk.Now()
	state := reqctx.FromRequest(c.Request)
	status := c.Writer.Status()
	code, cause, detail := state.Failure()
	if panicked && !c.Writer.Written() {
		status, code = http.StatusInternalServerError, apperr.ErrInternal.Code
	}
	size := c.Writer.Size()
	if size < 0 {
		size = 0
	}
	route := c.FullPath()
	if route == "" {
		route = "-"
	}
	reqPath := redact.PathWithQuery(c.Request.URL.Path, c.Request.URL.RawQuery)
	userID := "-"
	var uid uint
	if p, ok := state.Principal(); ok {
		uid = p.UserID
		userID = fmt.Sprint(p.UserID)
	}

	var causeText string
	switch {
	case panicked:
		causeText = "panic (see http_panic_recovered)"
	case cause != nil:
		causeText = redact.Cause(cause.Error())
	case status >= 500:
		causeText = "<no cause recorded>"
	}
	level := logx.LevelInfo
	switch {
	case status >= 500:
		level = logx.LevelError
	case status >= 400, code != "", cause != nil:
		level = logx.LevelWarn
	}

	var b strings.Builder
	fmt.Fprintf(&b, "[AccessLog] %s %s\n", c.Request.Method, reqPath)
	fmt.Fprintf(&b, "Route: %s\n", route)
	fmt.Fprintf(&b, "StartTime: %s\n", start.Format(time.RFC3339))
	fmt.Fprintf(&b, "EndTime: %s\n", end.Format(time.RFC3339))
	fmt.Fprintf(&b, "Latency: %dms\n", end.Sub(start).Milliseconds())
	fmt.Fprintf(&b, "ClientIP: %s\n", state.ClientIP())
	fmt.Fprintf(&b, "Response: %d %d\n", status, size)
	fmt.Fprintf(&b, "RequestID: %s\n", state.RequestID())
	fmt.Fprintf(&b, "TraceID: %s\n", state.TraceID())
	fmt.Fprintf(&b, "UserID: %s\n", userID)
	fmt.Fprintf(&b, "UserAgent: %s", c.Request.UserAgent())
	if code != "" {
		fmt.Fprintf(&b, "\nErrorCode: %s", code)
	}
	if causeText != "" {
		fmt.Fprintf(&b, "\nError: %s", causeText)
	}
	log.Log(level, b.String())

	if rec == nil || panicked {
		return
	}
	if status >= 500 || (status < 400 && (code != "" || cause != nil)) {
		if code == "" {
			code, _, _ = apperr.Resolve(status, nil)
		}
		rec.RecordError(c.Request.Context(), ErrorRecord{
			RequestID: state.RequestID(),
			TraceID:   state.TraceID(),
			UserID:    uid,
			Method:    c.Request.Method,
			Route:     route,
			Path:      reqPath,
			Status:    status,
			ErrorCode: code,
			Cause:     causeText,
			Detail:    redact.Cause(detail),
			LatencyMS: end.Sub(start).Milliseconds(),
			ClientIP:  state.ClientIP(),
			UserAgent: c.Request.UserAgent(),
			At:        end,
		})
	}
}

// Recovery turns a handler panic into a logged error, a persisted error
// record and the standard 500 envelope. It must be the outermost middleware.
func Recovery(log *logx.Logger, rec ErrorRecorder, clk clock.Clock) gin.HandlerFunc {
	if log == nil {
		log = logx.Nop()
	}
	if clk == nil {
		clk = clock.System{}
	}
	return func(c *gin.Context) {
		start := clk.Now()
		defer func() {
			r := recover()
			if r == nil {
				return
			}
			if r == http.ErrAbortHandler { //nolint:errorlint // sentinel panic value by contract
				panic(r)
			}
			stack := string(debug.Stack())
			state := reqctx.FromRequest(c.Request)
			route := c.FullPath()
			if route == "" {
				route = "-"
			}
			log.Error("http_panic_recovered",
				logx.F("request_id", state.RequestID()), logx.F("trace_id", state.TraceID()),
				logx.F("method", c.Request.Method), logx.F("route", route),
				logx.F("panic", redact.Cause(fmt.Sprint(r))), logx.F("stack", stack))
			cause := fmt.Errorf("panic: %v", r)
			if c.Writer.Written() {
				c.Abort()
			} else {
				httpx.Fail(c, http.StatusInternalServerError, apperr.ErrInternal.WithCause(cause))
			}
			if rec != nil {
				end := clk.Now()
				uid := uint(0)
				if p, ok := state.Principal(); ok {
					uid = p.UserID
				}
				rec.RecordError(c.Request.Context(), ErrorRecord{
					RequestID: state.RequestID(), TraceID: state.TraceID(), UserID: uid,
					Method: c.Request.Method, Route: route,
					Path:      redact.PathWithQuery(c.Request.URL.Path, c.Request.URL.RawQuery),
					Status:    http.StatusInternalServerError,
					ErrorCode: apperr.ErrInternal.Code,
					Cause:     redact.Cause(cause.Error()),
					LatencyMS: end.Sub(start).Milliseconds(),
					ClientIP:  state.ClientIP(), UserAgent: c.Request.UserAgent(), At: end,
				})
			}
		}()
		c.Next()
	}
}
