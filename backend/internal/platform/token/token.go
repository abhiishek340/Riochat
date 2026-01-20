// Package token issues and parses the HS256 access and refresh tokens.
package token

import (
	"errors"
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/riochat/riochat/backend/internal/platform/clock"
)

// Token types carried in the token_type claim.
const (
	TypeAccess  = "access"
	TypeRefresh = "refresh"
)

// ErrInvalid is returned for any unparsable, expired or wrongly signed token.
var ErrInvalid = errors.New("token: invalid token")

// Claims is the application payload of a token.
type Claims struct {
	UserID    uint   `json:"user_id"`
	Username  string `json:"username"`
	Role      string `json:"role"`
	SessionID uint   `json:"session_id"`
	TokenType string `json:"token_type"`
	jwt.RegisteredClaims
}

// IssuedAtTime returns the iat claim as a time (zero when absent).
func (c *Claims) IssuedAtTime() time.Time {
	if c.RegisteredClaims.IssuedAt == nil {
		return time.Time{}
	}
	return c.RegisteredClaims.IssuedAt.Time
}

// Manager signs and verifies tokens with a shared secret. The secret is read
// through a function so a rotated configuration applies without rebuilding.
type Manager struct {
	secret func() string
	clock  clock.Clock
}

// NewManager builds a Manager. clk may be nil (system clock).
func NewManager(secret func() string, clk clock.Clock) *Manager {
	if clk == nil {
		clk = clock.System{}
	}
	return &Manager{secret: secret, clock: clk}
}

// Issue signs a token for c, valid for ttl from now. TokenType defaults to
// access. The returned token carries a fresh jti.
func (m *Manager) Issue(c Claims, ttl time.Duration) (string, error) {
	if c.TokenType == "" {
		c.TokenType = TypeAccess
	}
	now := m.clock.Now()
	c.RegisteredClaims = jwt.RegisteredClaims{
		ID:        uuid.NewString(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
	}
	signed, err := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString([]byte(m.secret()))
	if err != nil {
		return "", fmt.Errorf("token: sign: %w", err)
	}
	return signed, nil
}

// Parse verifies signature (HMAC only) and expiry and returns the claims.
func (m *Manager) Parse(raw string) (*Claims, error) {
	var c Claims
	parsed, err := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
		}
		return []byte(m.secret()), nil
	},
		jwt.WithTimeFunc(m.clock.Now),
		jwt.WithValidMethods([]string{"HS256"}),
		jwt.WithExpirationRequired(),
	)
	if err != nil || !parsed.Valid {
		return nil, ErrInvalid
	}
	return &c, nil
}
