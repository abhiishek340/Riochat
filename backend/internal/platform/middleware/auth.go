package middleware

import (
	"context"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

// TokenParser verifies a bearer token. *token.Manager implements it.
type TokenParser interface {
	Parse(raw string) (*token.Claims, error)
}

// SessionCheck is what the session validator receives for each authenticated
// request.
type SessionCheck struct {
	UserID    uint
	SessionID uint
	// IssuedAt is the token's issued-at time (lets the validator reject tokens
	// older than a password change or global sign-out).
	IssuedAt time.Time
	Audit    AuditContext
}

// SessionValidator decides whether the session behind a token is still valid
// (not revoked, not expired, user not disabled). The auth feature provides it.
// Any returned error answers 401 auth.session_invalid.
type SessionValidator interface {
	ValidateSession(ctx context.Context, check SessionCheck) error
}

// SessionValidatorFunc adapts a function to SessionValidator.
type SessionValidatorFunc func(ctx context.Context, check SessionCheck) error

// ValidateSession implements SessionValidator.
func (f SessionValidatorFunc) ValidateSession(ctx context.Context, c SessionCheck) error {
	return f(ctx, c)
}

// SessionHook lets a feature register the session validator after the router
// has been built. Until one is registered, tokens are accepted on signature
// and expiry alone; the app warns at startup if none was set.
type SessionHook struct {
	v atomic.Pointer[SessionValidator]
}

// Set registers the validator (last writer wins).
func (h *SessionHook) Set(v SessionValidator) { h.v.Store(&v) }

// IsSet reports whether a validator is registered.
func (h *SessionHook) IsSet() bool { return h != nil && h.v.Load() != nil }

func (h *SessionHook) get() SessionValidator {
	if h == nil {
		return nil
	}
	if p := h.v.Load(); p != nil {
		return *p
	}
	return nil
}

func invalidToken(c *gin.Context, msg string) {
	httpx.Fail(c, http.StatusUnauthorized, apperr.ErrInvalidToken.WithMessage(msg))
}

// Authenticate requires "Authorization: Bearer <JWT>". All failures are 401:
//
//	missing header          auth.invalid_token  "authorization header is required"
//	other scheme / empty    auth.invalid_token  "invalid authorization header"
//	unparsable / expired    auth.invalid_token  "invalid token"
//	token type not access   auth.invalid_token  "invalid token type"
//	session no longer valid auth.session_invalid "session invalid"
//
// On success the request state carries the principal.
func Authenticate(parser TokenParser, sessions *SessionHook) gin.HandlerFunc {
	return func(c *gin.Context) {
		header := strings.TrimSpace(c.GetHeader("Authorization"))
		if header == "" {
			invalidToken(c, "authorization header is required")
			return
		}
		scheme, raw, ok := strings.Cut(header, " ")
		raw = strings.TrimSpace(raw)
		if !ok || scheme != "Bearer" || raw == "" {
			invalidToken(c, "invalid authorization header")
			return
		}
		claims, err := parser.Parse(raw)
		if err != nil {
			invalidToken(c, "invalid token")
			return
		}
		if claims.TokenType != "" && claims.TokenType != token.TypeAccess {
			invalidToken(c, "invalid token type")
			return
		}
		if v := sessions.get(); v != nil {
			err := v.ValidateSession(c.Request.Context(), SessionCheck{
				UserID:    claims.UserID,
				SessionID: claims.SessionID,
				IssuedAt:  claims.IssuedAtTime(),
				Audit:     AuditFromRequest(c.Request),
			})
			if err != nil {
				httpx.Fail(c, http.StatusUnauthorized, apperr.ErrSessionInvalid.WithCause(err))
				return
			}
		}
		reqctx.FromRequest(c.Request).SetPrincipal(reqctx.Principal{
			UserID: claims.UserID, Username: claims.Username, Role: claims.Role, SessionID: claims.SessionID,
		})
		c.Next()
	}
}

// AdminOnly allows administrators (admin or superadmin) only. A missing role is
// 403 auth.forbidden; any other role is 403 auth.admin_required.
func AdminOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		p, ok := reqctx.FromRequest(c.Request).Principal()
		if !ok || p.Role == "" {
			httpx.Fail(c, http.StatusForbidden, apperr.ErrForbidden)
			return
		}
		if !p.IsAdmin() {
			httpx.Fail(c, http.StatusForbidden, apperr.ErrAdminRequired)
			return
		}
		c.Next()
	}
}

// Principal returns the authenticated caller of the request.
func Principal(c *gin.Context) (reqctx.Principal, bool) {
	return reqctx.FromRequest(c.Request).Principal()
}

// MustPrincipal returns the authenticated caller and panics when called on a
// route without Authenticate (a programmer error; Recovery turns it into 500).
func MustPrincipal(c *gin.Context) reqctx.Principal {
	p, ok := Principal(c)
	if !ok {
		panic("middleware: MustPrincipal on an unauthenticated route")
	}
	return p
}
