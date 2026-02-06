package middleware

import (
	"compress/gzip"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
)

// MinCompressBytes is the smallest body worth compressing.
const MinCompressBytes = 1024

// Gzip compresses qualifying responses. The decision is made when the first
// body bytes are written, after handlers have set their headers:
//
//   - the client accepts gzip (an explicit q=0 means no);
//   - the method is not HEAD, there is no Range header, no WebSocket upgrade;
//   - the status is not 1xx/204/206/304, there is no Content-Encoding, and
//     Cache-Control does not say no-transform;
//   - the size (declared Content-Length, else the first chunk) is at least
//     1024 bytes;
//   - the content type is textual; event streams and NDJSON are never
//     compressed.
func Gzip() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !acceptsGzip(c.GetHeader("Accept-Encoding")) ||
			c.Request.Method == http.MethodHead ||
			c.GetHeader("Range") != "" ||
			isUpgrade(c.Request) {
			c.Next()
			return
		}
		gw := &gzipWriter{ResponseWriter: c.Writer}
		c.Writer = gw
		defer gw.finish()
		c.Next()
	}
}

func isUpgrade(r *http.Request) bool {
	return strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
		strings.Contains(strings.ToLower(r.Header.Get("Connection")), "upgrade")
}

// acceptsGzip parses Accept-Encoding and honors q=0. An explicit gzip entry
// decides; otherwise a "*" entry does.
func acceptsGzip(header string) bool {
	gzipQ, starQ := -1.0, -1.0
	for _, part := range strings.Split(header, ",") {
		name, params, _ := strings.Cut(strings.TrimSpace(part), ";")
		name = strings.ToLower(strings.TrimSpace(name))
		if name != "gzip" && name != "*" {
			continue
		}
		q := 1.0
		for _, p := range strings.Split(params, ";") {
			k, v, ok := strings.Cut(strings.TrimSpace(p), "=")
			if ok && strings.EqualFold(strings.TrimSpace(k), "q") {
				if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
					q = f
				}
			}
		}
		if name == "gzip" {
			gzipQ = q
		} else {
			starQ = q
		}
	}
	if gzipQ >= 0 {
		return gzipQ > 0
	}
	return starQ > 0
}

func compressibleType(ct string) bool {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.IndexByte(ct, ';'); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	switch ct {
	case "":
		return false
	case "text/event-stream", "application/x-ndjson", "application/ndjson", "application/jsonl", "application/x-jsonlines":
		return false
	case "application/json", "application/javascript", "application/xml", "application/wasm", "image/svg+xml":
		return true
	}
	return strings.HasPrefix(ct, "text/") || strings.HasSuffix(ct, "+json") || strings.HasSuffix(ct, "+xml")
}

type gzipWriter struct {
	gin.ResponseWriter
	decided bool
	gz      *gzip.Writer
}

// decide inspects headers and the first chunk (nil when unknown) and switches
// compression on when every condition holds.
func (w *gzipWriter) decide(first []byte) {
	if w.decided {
		return
	}
	w.decided = true
	h := w.Header()
	status := w.ResponseWriter.Status()
	if status < 200 || status == http.StatusNoContent || status == http.StatusPartialContent || status == http.StatusNotModified {
		return
	}
	if h.Get("Content-Encoding") != "" || strings.Contains(strings.ToLower(h.Get("Cache-Control")), "no-transform") {
		return
	}
	if !compressibleType(h.Get("Content-Type")) {
		return
	}
	size := len(first)
	if cl := h.Get("Content-Length"); cl != "" {
		if n, err := strconv.Atoi(cl); err == nil {
			size = n
		}
	}
	if size < MinCompressBytes {
		return
	}
	h.Del("Content-Length")
	h.Del("Accept-Ranges")
	h.Set("Content-Encoding", "gzip")
	h.Add("Vary", "Accept-Encoding")
	if etag := h.Get("ETag"); etag != "" && !strings.HasPrefix(etag, "W/") {
		h.Set("ETag", "W/"+etag)
	}
	w.gz = gzip.NewWriter(w.ResponseWriter)
}

func (w *gzipWriter) WriteHeaderNow() {
	w.decide(nil)
	w.ResponseWriter.WriteHeaderNow()
}

func (w *gzipWriter) Write(p []byte) (int, error) {
	w.decide(p)
	if w.gz != nil {
		return w.gz.Write(p)
	}
	return w.ResponseWriter.Write(p)
}

func (w *gzipWriter) WriteString(s string) (int, error) { return w.Write([]byte(s)) }

// Flush flushes the compressor first so streamed output stays timely.
func (w *gzipWriter) Flush() {
	w.decide(nil)
	if w.gz != nil {
		_ = w.gz.Flush()
	}
	w.ResponseWriter.Flush()
}

func (w *gzipWriter) finish() {
	if w.gz != nil {
		_ = w.gz.Close()
	}
}
