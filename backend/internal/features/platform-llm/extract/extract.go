// Package extract converts uploaded files to text through pluggable engines:
// built-in parsers, Apache Tika, Docling, MinerU, OCR services, Mistral OCR and
// model-based OCR. Engines whose settings are missing are unavailable.
package extract

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"net/url"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// MaxBodyBytes bounds engine responses and uploads.
const MaxBodyBytes = 50 << 20

// Request is one file to convert.
type Request struct {
	Name string
	MIME string
	Data []byte
	// Pages restricts OCR to pages, 1-based, for example "1,3-5". Empty means all.
	Pages string
	// Model and Prompt are used by model-based engines.
	Model  string
	Prompt string
	// MaxPages limits native PDF parsing (0 = no limit).
	MaxPages int
}

// Result is the extracted text.
type Result struct {
	Text      string
	Pages     int      // rendered/processed page count when known
	PageTexts []string // per-page text when the engine reports it
	Engine    string   // engine that produced the text
}

// Engine is the extraction port.
type Engine interface {
	Name() string
	Extract(ctx context.Context, r Request) (*Result, error)
	// Probe checks reachability with a short timeout.
	Probe(ctx context.Context) (ok bool, detail string)
}

// Error is a coded extraction failure: Code is the stable identifier, Detail
// optional text. Error() renders "code" or "code: detail".
type Error struct {
	Code   string
	Detail string
	Cause  error
}

func (e *Error) Error() string {
	if e.Detail != "" {
		return e.Code + ": " + e.Detail
	}
	return e.Code
}

// Unwrap returns the cause.
func (e *Error) Unwrap() error { return e.Cause }

// IsCode reports whether err is an *Error with the given code.
func IsCode(err error, code string) bool {
	var e *Error
	return errors.As(err, &e) && e.Code == code
}

func coded(code, detail string) *Error { return &Error{Code: code, Detail: detail} }

// Doer executes requests through the outbound pool.
type Doer interface {
	Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error)
}

// NormalizeText trims every line and drops blank lines.
func NormalizeText(s string) string {
	lines := strings.Split(strings.ReplaceAll(s, "\r\n", "\n"), "\n")
	out := lines[:0]
	for _, l := range lines {
		if t := strings.TrimSpace(l); t != "" {
			out = append(out, t)
		}
	}
	return strings.Join(out, "\n")
}

// authHeaders sets the three token headers compatible proxies accept.
func authHeaders(h http.Header, token string) {
	if token = strings.TrimSpace(token); token != "" {
		h.Set("Authorization", "Bearer "+token)
		h.Set("X-API-Key", token)
		h.Set("token", token)
	}
}

// httpBase carries the shared HTTP plumbing of the network engines.
type httpBase struct {
	engine  string // error-code prefix
	baseURL string
	token   string
	timeout time.Duration
	http    Doer
	kind    outbound.Kind
}

func (b httpBase) endpoint() outbound.Endpoint {
	return outbound.Endpoint{URL: b.baseURL, Kind: b.kind, Integration: b.engine + " engine"}
}

func (b httpBase) ctx(ctx context.Context) (context.Context, context.CancelFunc) {
	t := b.timeout
	if t <= 0 {
		t = 60 * time.Second
	}
	return context.WithTimeout(ctx, t)
}

// do runs a request and returns status, body (bounded) or an unavailable
// error for transport failures.
func (b httpBase) do(ctx context.Context, req *http.Request) (int, []byte, http.Header, error) {
	resp, err := b.http.Do(ctx, b.endpoint(), req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		return 0, nil, nil, &Error{Code: b.engine + "_unavailable", Detail: err.Error(), Cause: err}
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxBodyBytes+1))
	if err != nil {
		return 0, nil, nil, &Error{Code: b.engine + "_unavailable", Detail: err.Error(), Cause: err}
	}
	if len(body) > MaxBodyBytes {
		return resp.StatusCode, nil, resp.Header, coded(b.engine+"_http_"+fmt.Sprint(resp.StatusCode), "response body is too large")
	}
	return resp.StatusCode, body, resp.Header, nil
}

// statusError maps a non-success status to the engine's error code.
func (b httpBase) statusError(status int, body []byte, withUnsupported bool) *Error {
	detail := strings.TrimSpace(string(body))
	if len(detail) > 300 {
		detail = detail[:300]
	}
	switch {
	case status == http.StatusUnauthorized:
		return coded(b.engine+"_unauthorized", "")
	case status == http.StatusForbidden:
		return coded(b.engine+"_forbidden", "")
	case status == http.StatusUnsupportedMediaType && withUnsupported:
		return coded(b.engine+"_unsupported_media_type", "")
	case status == http.StatusUnprocessableEntity:
		return coded(b.engine+"_unprocessable", detail)
	}
	return coded(fmt.Sprintf("%s_http_%d", b.engine, status), detail)
}

func (b httpBase) multipart(ctx context.Context, target string, fields map[string]string, fileField string, r Request) (*http.Request, error) {
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	h := textproto.MIMEHeader{}
	name := r.Name
	if name == "" {
		name = "file"
	}
	h.Set("Content-Disposition", fmt.Sprintf(`form-data; name=%q; filename=%q`, fileField, name))
	mime := r.MIME
	if mime == "" {
		mime = "application/octet-stream"
	}
	h.Set("Content-Type", mime)
	pw, err := w.CreatePart(h)
	if err != nil {
		return nil, err
	}
	if _, err := pw.Write(r.Data); err != nil {
		return nil, err
	}
	for k, v := range fields {
		if v != "" {
			_ = w.WriteField(k, v)
		}
	}
	if err := w.Close(); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, target, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", w.FormDataContentType())
	authHeaders(req.Header, b.token)
	return req, nil
}

func (b httpBase) probe(ctx context.Context, target string) (bool, string) {
	ctx, cancel := context.WithTimeout(ctx, 800*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return false, "invalid url"
	}
	authHeaders(req.Header, b.token)
	resp, err := b.http.Do(ctx, b.endpoint(), req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		return false, err.Error()
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode >= 500 || resp.StatusCode == http.StatusNotFound {
		return false, fmt.Sprintf("status %d", resp.StatusCode)
	}
	return true, fmt.Sprintf("status %d", resp.StatusCode)
}

// ---- flexible response parsing -------------------------------------------

var textKeys = []string{"text", "full_text", "content", "markdown"}

// jsonText finds text in flexible JSON documents: top-level text keys, nested
// under data/result (objects or arrays), and per-page arrays.
func jsonText(v any) (string, []string) {
	switch x := v.(type) {
	case string:
		return x, nil
	case []any:
		var pages []string
		for _, e := range x {
			if t, _ := jsonText(e); t != "" {
				pages = append(pages, t)
			}
		}
		return strings.Join(pages, "\n"), pages
	case map[string]any:
		var pages []string
		if pg, ok := x["pages"].([]any); ok {
			for _, e := range pg {
				if t, _ := jsonText(e); t != "" {
					pages = append(pages, t)
				}
			}
		}
		for _, k := range textKeys {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return s, pages
			}
		}
		if len(pages) > 0 {
			return strings.Join(pages, "\n"), pages
		}
		for _, k := range []string{"data", "result", "results"} {
			if nested, ok := x[k]; ok {
				if t, p := jsonText(nested); strings.TrimSpace(t) != "" {
					return t, p
				}
			}
		}
	}
	return "", nil
}

// parseFlexible reads JSON when the body is JSON, otherwise treats it as text.
func parseFlexible(body []byte) (string, []string) {
	t := bytes.TrimSpace(body)
	if len(t) > 0 && (t[0] == '{' || t[0] == '[') {
		var v any
		if json.Unmarshal(t, &v) == nil {
			return jsonText(v)
		}
	}
	return string(body), nil
}
