// Package reqctx carries per-request facts (identifiers, caller identity, the
// recorded failure) through context.Context so that middleware, handlers and
// background code can share them without depending on the HTTP framework.
package reqctx

import (
	"context"
	"net/http"
	"sync"
)

// Role names with administrative rights.
const (
	RoleAdmin      = "admin"
	RoleSuperAdmin = "superadmin"
)

// Principal is the authenticated caller.
type Principal struct {
	UserID    uint
	Username  string
	Role      string
	SessionID uint
}

// IsAdmin reports whether the principal holds an administrator role.
func (p Principal) IsAdmin() bool { return p.Role == RoleAdmin || p.Role == RoleSuperAdmin }

// State is the mutable per-request record. All methods are safe for
// concurrent use.
type State struct {
	mu          sync.Mutex
	requestID   string
	traceID     string
	clientIP    string
	trustedPeer bool
	principal   *Principal
	errorCode   string
	cause       error
	diagnostic  string
}

type ctxKey struct{}

// NewState builds a state with the given identifiers.
func NewState(requestID, traceID string) *State {
	return &State{requestID: requestID, traceID: traceID}
}

// Attach stores s in ctx.
func Attach(ctx context.Context, s *State) context.Context {
	return context.WithValue(ctx, ctxKey{}, s)
}

// From returns the state stored in ctx. When none is present a detached empty
// state is returned, so callers never need a nil check.
func From(ctx context.Context) *State {
	if ctx != nil {
		if s, ok := ctx.Value(ctxKey{}).(*State); ok && s != nil {
			return s
		}
	}
	return &State{}
}

// FromRequest is From applied to r's context.
func FromRequest(r *http.Request) *State {
	if r == nil {
		return &State{}
	}
	return From(r.Context())
}

// Detach returns a context that keeps ctx's values (including the request
// state) but is not canceled when ctx ends. Use it for follow-up work that
// must outlive the request.
func Detach(ctx context.Context) context.Context { return context.WithoutCancel(ctx) }

// RequestID returns the server-controlled request identifier.
func (s *State) RequestID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.requestID }

// TraceID returns the 32 hex character trace identifier.
func (s *State) TraceID() string { s.mu.Lock(); defer s.mu.Unlock(); return s.traceID }

// SetIdentifiers replaces the request and trace identifiers.
func (s *State) SetIdentifiers(requestID, traceID string) {
	s.mu.Lock()
	s.requestID, s.traceID = requestID, traceID
	s.mu.Unlock()
}

// ClientIP returns the resolved client address.
func (s *State) ClientIP() string { s.mu.Lock(); defer s.mu.Unlock(); return s.clientIP }

// FromTrustedProxy reports whether the TCP peer is a configured trusted proxy.
func (s *State) FromTrustedProxy() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.trustedPeer }

// SetClient records the resolved client address and trusted-peer flag.
func (s *State) SetClient(ip string, trustedPeer bool) {
	s.mu.Lock()
	s.clientIP, s.trustedPeer = ip, trustedPeer
	s.mu.Unlock()
}

// Principal returns the authenticated caller, if any.
func (s *State) Principal() (Principal, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.principal == nil {
		return Principal{}, false
	}
	return *s.principal, true
}

// SetPrincipal records the authenticated caller.
func (s *State) SetPrincipal(p Principal) {
	s.mu.Lock()
	s.principal = &p
	s.mu.Unlock()
}

// RecordFailure remembers the error response that was produced: the public
// code, the internal cause and an optional diagnostic detail. Raw cause text
// never reaches the client; the access log and error log read it from here.
func (s *State) RecordFailure(code string, cause error, diagnostic string) {
	s.mu.Lock()
	s.errorCode, s.cause, s.diagnostic = code, cause, diagnostic
	s.mu.Unlock()
}

// Failure returns what RecordFailure stored.
func (s *State) Failure() (code string, cause error, diagnostic string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.errorCode, s.cause, s.diagnostic
}
