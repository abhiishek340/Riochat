package mcp

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// SignedContextPlaceholder, as the trimmed value of a configured header, asks
// the platform to substitute a signed token that identifies the calling user.
const SignedContextPlaceholder = "${RIOCHAT_SIGNED_USER_CONTEXT}"

const (
	userContextVersion = "v1"
	userContextTTL     = 5 * time.Minute
)

// Signing and verification failures.
var (
	ErrSigningUnavailable = errors.New("mcp user context signing is not configured")
	ErrUserContextInvalid = errors.New("mcp user context token is invalid")
	ErrUserContextExpired = errors.New("mcp user context token has expired")
)

// UserContextClaims is the payload of a signed user context.
type UserContextClaims struct {
	UserID         uint   `json:"user_id"`
	ConversationID uint   `json:"conversation_id,omitempty"`
	RequestID      string `json:"request_id,omitempty"`
	Audience       string `json:"aud"`
	JTI            string `json:"jti"`
	ExpiresAt      int64  `json:"exp"`
}

var b64 = base64.RawURLEncoding

func userContextMAC(secret, payloadPart string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payloadPart))
	return b64.EncodeToString(m.Sum(nil))
}

// SignUserContext produces "v1.<payload>.<mac>" with unpadded base64url parts
// and HMAC-SHA256 over the encoded payload.
func SignUserContext(secret string, c UserContextClaims) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", ErrSigningUnavailable
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return "", err
	}
	payload := b64.EncodeToString(raw)
	return userContextVersion + "." + payload + "." + userContextMAC(secret, payload), nil
}

// VerifyUserContext checks the signature and expiry of a token. Remote servers
// can use the same algorithm to authenticate the header.
func VerifyUserContext(secret, token string, now time.Time) (UserContextClaims, error) {
	var c UserContextClaims
	if strings.TrimSpace(secret) == "" {
		return c, ErrSigningUnavailable
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[0] != userContextVersion {
		return c, ErrUserContextInvalid
	}
	want := userContextMAC(secret, parts[1])
	if !hmac.Equal([]byte(want), []byte(parts[2])) {
		return c, ErrUserContextInvalid
	}
	raw, err := b64.DecodeString(parts[1])
	if err != nil || json.Unmarshal(raw, &c) != nil {
		return UserContextClaims{}, ErrUserContextInvalid
	}
	if now.Unix() >= c.ExpiresAt {
		return c, ErrUserContextExpired
	}
	return c, nil
}

// newJTI returns a random version-4 UUID string.
func newJTI() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	h := hex.EncodeToString(b[:])
	return h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// isPlaceholder reports whether a header value asks for the signed context.
func isPlaceholder(v string) bool { return strings.TrimSpace(v) == SignedContextPlaceholder }

// parseHeadersLenient decodes stored headers; bad JSON yields none. Names and
// values are trimmed and blank names dropped.
func parseHeadersLenient(text string) map[string]string {
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &raw); err != nil {
		return nil
	}
	out := make(map[string]string, len(raw))
	for k, v := range raw {
		s, ok := v.(string)
		if !ok {
			continue
		}
		if k = strings.TrimSpace(k); k != "" {
			out[k] = strings.TrimSpace(s)
		}
	}
	return out
}

// withoutPlaceholders drops headers that ask for the signed context (used for
// admin sync requests, where there is no user).
func withoutPlaceholders(h map[string]string) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		if !isPlaceholder(v) {
			out[k] = v
		}
	}
	return out
}
