package token

import (
	"errors"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/riochat/riochat/backend/internal/platform/clock"
)

func newMgr(secret string, clk clock.Clock) *Manager {
	return NewManager(func() string { return secret }, clk)
}

func TestIssueAndParse(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC))
	m := newMgr("secret-0123456789", clk)
	raw, err := m.Issue(Claims{UserID: 7, Username: "ann", Role: "admin", SessionID: 9}, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	c, err := m.Parse(raw)
	if err != nil {
		t.Fatal(err)
	}
	if c.UserID != 7 || c.Username != "ann" || c.Role != "admin" || c.SessionID != 9 || c.TokenType != TypeAccess || c.ID == "" {
		t.Fatalf("claims: %+v", c)
	}
	if !c.IssuedAtTime().Equal(clk.Now().Truncate(time.Second)) {
		t.Fatalf("iat: %v", c.IssuedAtTime())
	}
}

func TestRejects(t *testing.T) {
	clk := clock.NewFake(time.Now())
	m := newMgr("secret-0123456789", clk)
	raw, _ := m.Issue(Claims{UserID: 1}, time.Minute)

	clk.Advance(2 * time.Minute)
	if _, err := m.Parse(raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("expired: %v", err)
	}
	clk.Advance(-2 * time.Minute)
	if _, err := newMgr("another-secret-xxxx", clk).Parse(raw); !errors.Is(err, ErrInvalid) {
		t.Fatalf("wrong secret: %v", err)
	}
	if _, err := m.Parse("garbage"); !errors.Is(err, ErrInvalid) {
		t.Fatalf("garbage: %v", err)
	}
	// none algorithm and non-HMAC methods must be refused
	none, _ := jwt.NewWithClaims(jwt.SigningMethodNone, Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if _, err := m.Parse(none); !errors.Is(err, ErrInvalid) {
		t.Fatalf("alg none: %v", err)
	}
	hs512, _ := jwt.NewWithClaims(jwt.SigningMethodHS512, Claims{UserID: 1, RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour))}}).SignedString([]byte("secret-0123456789"))
	if _, err := m.Parse(hs512); !errors.Is(err, ErrInvalid) {
		t.Fatalf("hs512: %v", err)
	}
	// missing exp is refused
	noExp, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, Claims{UserID: 1}).SignedString([]byte("secret-0123456789"))
	if _, err := m.Parse(noExp); !errors.Is(err, ErrInvalid) {
		t.Fatalf("no exp: %v", err)
	}
}

func TestRefreshTypeRoundTrip(t *testing.T) {
	m := newMgr("secret-0123456789", nil)
	raw, _ := m.Issue(Claims{UserID: 2, TokenType: TypeRefresh}, time.Hour)
	c, err := m.Parse(raw)
	if err != nil || c.TokenType != TypeRefresh {
		t.Fatalf("refresh: %+v %v", c, err)
	}
}
