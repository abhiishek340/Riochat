package llm

import (
	"errors"
	"fmt"
	"net/http"
)

// Sentinel causes, matched with errors.Is.
var (
	ErrUnsupportedProtocol = errors.New("unsupported protocol")
	ErrUnsupportedStream   = errors.New("streaming is not supported")
	ErrInvalidBaseURL      = errors.New("invalid base url")
	ErrInvalidOption       = errors.New("invalid option")
	ErrInvalidRequest      = errors.New("invalid request")
	ErrBodyTooLarge        = errors.New("upstream response body too large")
	ErrStreamIdle          = errors.New("stream idle timeout")
	ErrFirstByteTimeout    = errors.New("timed out waiting for the first response byte")
)

// ErrKind classifies an Error.
type ErrKind int

// Error kinds.
const (
	// KindInvalid: the request could not be built (bad option, missing prompt).
	KindInvalid ErrKind = iota
	// KindUnsupported: protocol or streaming not supported.
	KindUnsupported
	// KindTransport: the exchange failed below HTTP.
	KindTransport
	// KindUpstream: a non-2xx answer or an in-stream error event.
	KindUpstream
	// KindParse: a 2xx body could not be understood.
	KindParse
)

// Error is the single error type of the package.
type Error struct {
	Kind    ErrKind
	Status  int    // HTTP status for upstream errors, else 0
	Code    string // provider failure code when known
	Message string
	// Body is the raw (bounded) upstream body for upstream errors.
	Body string
	// Accepted is true when the upstream may already have started work, so a
	// router must not silently retry on another route.
	Accepted bool
	Debug    *Debug
	Sentinel error
	Cause    error
}

func (e *Error) Error() string {
	switch {
	case e.Message != "":
		return e.Message
	case e.Cause != nil:
		return e.Cause.Error()
	case e.Sentinel != nil:
		return e.Sentinel.Error()
	}
	return "llm error"
}

// Unwrap exposes the sentinel and the cause to errors.Is / errors.As.
func (e *Error) Unwrap() []error {
	var out []error
	if e.Sentinel != nil {
		out = append(out, e.Sentinel)
	}
	if e.Cause != nil {
		out = append(out, e.Cause)
	}
	return out
}

func invalid(sentinel error, format string, args ...any) *Error {
	return &Error{Kind: KindInvalid, Sentinel: sentinel, Message: fmt.Sprintf(format, args...)}
}

// AsError extracts an *Error from err's chain.
func AsError(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// IsAccepted reports whether err means the upstream may have started work.
func IsAccepted(err error) bool {
	e, ok := AsError(err)
	return ok && e.Accepted
}

// Retryable reports whether trying another route is safe and sensible: the
// request was not accepted and the failure is a connection problem, a rate
// limit or a server-side fault.
func Retryable(err error) bool {
	e, ok := AsError(err)
	if !ok || e.Accepted {
		return false
	}
	switch e.Kind {
	case KindTransport:
		return true
	case KindUpstream:
		return e.Status == http.StatusTooManyRequests || e.Status == http.StatusRequestTimeout || e.Status >= 500
	}
	return false
}

func transportErr(err error, accepted bool) *Error {
	return &Error{Kind: KindTransport, Cause: err, Message: err.Error(), Accepted: accepted}
}
