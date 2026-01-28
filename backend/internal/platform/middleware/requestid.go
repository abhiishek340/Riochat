package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// Response headers carrying the identifiers.
const (
	HeaderRequestID = "X-Request-ID"
	HeaderTraceID   = "X-Trace-ID"
)

var (
	requestIDRe = regexp.MustCompile(`^[A-Za-z0-9._:-]{1,64}$`)
	traceIDRe   = regexp.MustCompile(`^[0-9a-f]{32}$`)
	zeroTraceID = strings.Repeat("0", 32)
)

// RequestID assigns the request and trace identifiers.
//
// The request ID is the user-facing "error ID" that administrators search in
// logs, so it is server-controlled: an inbound X-Request-ID is honored only
// when the immediate peer is a trusted proxy and the value matches
// [A-Za-z0-9._:-]{1,64}. The trace ID comes from a valid inbound X-Trace-ID
// (32 hex, non-zero, lower-cased) or is generated.
func RequestID() gin.HandlerFunc {
	return func(c *gin.Context) {
		state := reqctx.FromRequest(c.Request)
		reqID := ""
		if state.FromTrustedProxy() {
			if in := c.GetHeader(HeaderRequestID); requestIDRe.MatchString(in) {
				reqID = in
			}
		}
		if reqID == "" {
			reqID = uuid.NewString()
		}
		traceID := strings.ToLower(strings.TrimSpace(c.GetHeader(HeaderTraceID)))
		if !traceIDRe.MatchString(traceID) || traceID == zeroTraceID {
			traceID = newTraceID()
		}
		state.SetIdentifiers(reqID, traceID)
		c.Header(HeaderRequestID, reqID)
		c.Header(HeaderTraceID, traceID)
		c.Next()
	}
}

func newTraceID() string {
	for {
		b := make([]byte, 16)
		if _, err := rand.Read(b); err != nil {
			panic("middleware: crypto/rand failed: " + err.Error())
		}
		if id := hex.EncodeToString(b); id != zeroTraceID {
			return id
		}
	}
}
