// Package apperr defines typed application errors. A typed error carries a
// stable machine code ("<namespace>.<snake_case>") and a public message. The
// HTTP boundary exposes only those two values; any other error text stays in
// the server logs.
package apperr

import (
	"errors"
	"net/http"
	"sort"
	"sync"
)

// Error is a typed application error.
type Error struct {
	// Code is the stable machine-readable code.
	Code string
	// Message is the public, client-visible message.
	Message string
	// Status is an optional HTTP status hint (0 means "caller decides").
	Status int
	// Details is optional structured data returned in the envelope.
	Details any

	cause      error
	diagnostic string
}

// Error returns the internal text: the cause when one is wrapped, otherwise
// the public message.
func (e *Error) Error() string {
	if e.cause != nil {
		return e.cause.Error()
	}
	return e.Message
}

// Unwrap exposes the wrapped cause.
func (e *Error) Unwrap() error { return e.cause }

// Is makes errors.Is match two typed errors that share a code, so a wrapped
// or re-annotated copy still matches its sentinel.
func (e *Error) Is(target error) bool {
	var t *Error
	if errors.As(target, &t) {
		return t.Code == e.Code
	}
	return false
}

// DiagnosticDetail returns extra operator-only text (for example an upstream
// response body). It is logged and persisted after redaction, never returned
// to clients.
func (e *Error) DiagnosticDetail() string { return e.diagnostic }

func (e *Error) clone() *Error { c := *e; return &c }

// WithCause returns a copy whose internal text is cause. The public message is
// unchanged ("masked" flavor).
func (e *Error) WithCause(cause error) *Error {
	c := e.clone()
	c.cause = cause
	return c
}

// WithMessage returns a copy with a different public message.
func (e *Error) WithMessage(msg string) *Error {
	c := e.clone()
	c.Message = msg
	return c
}

// WithStatus returns a copy carrying an HTTP status hint.
func (e *Error) WithStatus(status int) *Error {
	c := e.clone()
	c.Status = status
	return c
}

// WithDetails returns a copy carrying structured details for the envelope.
func (e *Error) WithDetails(details any) *Error {
	c := e.clone()
	c.Details = details
	return c
}

// WithDiagnostic returns a copy carrying operator-only diagnostic text.
func (e *Error) WithDiagnostic(detail string) *Error {
	c := e.clone()
	c.diagnostic = detail
	return c
}

// New builds an unregistered typed error. Prefer Define for package-level
// sentinels so the code joins the boundary allowlist.
func New(code, message string) *Error { return &Error{Code: code, Message: message} }

var (
	regMu    sync.RWMutex
	registry = map[string]string{}
)

// Define creates a typed error and registers its code (and canonical message)
// with the boundary allowlist. Feature packages call it from package-level
// variable declarations:
//
//	var ErrNotFound = apperr.Define("announcement.not_found", "announcement not found")
func Define(code, message string) *Error {
	Register(code, message)
	return &Error{Code: code, Message: message}
}

// Register adds a code to the boundary allowlist. Re-registering a code with
// the same message is a no-op; re-registering with a different message keeps
// the first message.
func Register(code, message string) {
	regMu.Lock()
	defer regMu.Unlock()
	if _, ok := registry[code]; !ok {
		registry[code] = message
	}
}

// Registered reports whether code is on the allowlist.
func Registered(code string) bool {
	regMu.RLock()
	defer regMu.RUnlock()
	_, ok := registry[code]
	return ok
}

// CanonicalMessage returns the registered message for code.
func CanonicalMessage(code string) (string, bool) {
	regMu.RLock()
	defer regMu.RUnlock()
	m, ok := registry[code]
	return m, ok
}

// Codes lists every registered code, sorted.
func Codes() []string {
	regMu.RLock()
	defer regMu.RUnlock()
	out := make([]string, 0, len(registry))
	for c := range registry {
		out = append(out, c)
	}
	sort.Strings(out)
	return out
}

// Find returns the first typed error in err's chain.
func Find(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) && e != nil {
		return e, true
	}
	return nil, false
}

// Diagnostic returns the first non-empty diagnostic detail found in err's
// chain, looking for any error exposing DiagnosticDetail.
func Diagnostic(err error) string {
	for err != nil {
		if d, ok := err.(interface{ DiagnosticDetail() string }); ok {
			if s := d.DiagnosticDetail(); s != "" {
				return s
			}
		}
		err = errors.Unwrap(err)
	}
	return ""
}

// Generic returns the generic code and message for an HTTP status when no
// typed error is present.
func Generic(status int) (code, message string) {
	switch status {
	case http.StatusUnauthorized:
		return "auth.unauthorized", "unauthorized"
	case http.StatusForbidden:
		return "auth.forbidden", "forbidden"
	case http.StatusNotFound:
		return "resource.not_found", "resource not found"
	case http.StatusConflict:
		return "resource.conflict", "resource conflict"
	case http.StatusPaymentRequired:
		return "billing.payment_required", "payment required"
	case http.StatusTooManyRequests:
		return "rate_limit.exceeded", "rate limit exceeded"
	case http.StatusBadGateway:
		return "upstream.unavailable", "upstream service unavailable"
	case http.StatusServiceUnavailable:
		return "service.unavailable", "service unavailable"
	}
	if status >= 500 {
		return "internal.error", "internal server error"
	}
	return "request.invalid", "invalid request"
}

// Resolve decides the public code and message for a response with the given
// status. A registered typed error wins; anything else degrades to the
// status's generic contract so raw error text never leaks.
func Resolve(status int, err error) (code, message string, details any) {
	if te, ok := Find(err); ok && Registered(te.Code) {
		return te.Code, te.Message, te.Details
	}
	code, message = Generic(status)
	return code, message, nil
}
