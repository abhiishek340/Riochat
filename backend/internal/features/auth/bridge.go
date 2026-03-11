package auth

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// Client identifiers of the first-party apps allowed to start the bridge.
const (
	clientWeb     = "riochat-web"
	clientNative  = "com.riochat.native"
	clientDesktop = "com.riochat.desktop"

	nativeRedirect = "com.riochat:/oauth/callback"

	bridgeTxTTL    = 10 * time.Minute
	bridgeGrantTTL = 90 * time.Second

	intentLogin    = "login"
	intentRegister = "register"
	intentBind     = "bind"
)

var pkcePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{43,128}$`)

type bridgeTx struct {
	Slug            string    `json:"slug"`
	ClientID        string    `json:"clientID"`
	Redirect        string    `json:"redirect"`
	ClientState     string    `json:"clientState"`
	ClientChallenge string    `json:"clientChallenge"`
	Verifier        string    `json:"verifier"`
	Intent          string    `json:"intent"`
	UserID          uint      `json:"userID"`
	Next            string    `json:"next"`
	ExpiresAt       time.Time `json:"expiresAt"`
}

type bridgeProfile struct {
	DisplayName   string `json:"displayName"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"emailVerified"`
	Raw           string `json:"raw"`
}

// Grant error codes stored when the callback could not finish the sign-in.
const (
	grantErrProvider      = "provider_invalid"
	grantErrEmailConflict = "email_conflict"
	grantErrLoginDisabled = "login_disabled"
	grantErrNotRegistered = "not_registered"
	grantErrNoSubject     = "subject_missing"
	grantErrInvalidEmail  = "invalid_email"
)

type bridgeGrant struct {
	Slug      string         `json:"slug"`
	ClientID  string         `json:"clientID"`
	Intent    string         `json:"intent"`
	UserID    uint           `json:"userID"`
	Subject   string         `json:"subject"`
	Profile   *bridgeProfile `json:"profile,omitempty"`
	ErrCode   string         `json:"errCode,omitempty"`
	ErrEmail  string         `json:"errEmail,omitempty"`
	ExpiresAt time.Time      `json:"expiresAt"`
}

func txKey(id string) string { return "auth:bridge:tx:" + id }

func grantKey(grant, challenge string) string {
	return "auth:bridge:grant:" + sha256Hex(grant+":"+challenge)
}

// ---- configuration ----------------------------------------------------------------

// apiBaseURL returns PUBLIC_API_BASE_URL without a trailing slash when it is a
// clean http(s) URL (no credentials, query or fragment).
func apiBaseURL(c *config.Config) (string, bool) {
	raw := strings.TrimSpace(c.Server.PublicAPIBaseURL)
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.Contains(raw, "#") {
		return "", false
	}
	return strings.TrimRight(raw, "/"), true
}

func (s *Service) bridgeEnabled() (callbackBase string, ok bool) {
	if s.d.Cache == nil {
		return "", false
	}
	base, ok := apiBaseURL(s.cfg())
	if !ok {
		return "", false
	}
	return base + "/api/v1/auth/providers", true
}

// ---- signed state --------------------------------------------------------------------

type statePayload struct {
	Aud  string `json:"aud"`
	Slug string `json:"slug"`
	Tx   string `json:"tx"`
	Exp  int64  `json:"exp"`
}

func (s *Service) stateMAC(body string) []byte {
	m := hmac.New(sha256.New, []byte(s.cfg().Security.JWTSecret))
	m.Write([]byte("provider-state\x1f" + body))
	return m.Sum(nil)
}

func (s *Service) signState(slug, tx string, exp time.Time) string {
	raw, _ := json.Marshal(statePayload{Aud: "provider_auth", Slug: slug, Tx: tx, Exp: exp.Unix()})
	body := base64.RawURLEncoding.EncodeToString(raw)
	return body + "." + base64.RawURLEncoding.EncodeToString(s.stateMAC(body))
}

// openState verifies a state value and returns its payload.
func (s *Service) openState(state, slug string) (*statePayload, error) {
	body, sig, ok := strings.Cut(state, ".")
	if !ok || body == "" || sig == "" {
		return nil, ErrOAuthStateInvalid
	}
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(got, s.stateMAC(body)) {
		return nil, ErrOAuthStateInvalid
	}
	raw, err := base64.RawURLEncoding.DecodeString(body)
	if err != nil {
		return nil, ErrOAuthStateInvalid
	}
	var p statePayload
	if json.Unmarshal(raw, &p) != nil || p.Aud != "provider_auth" || p.Slug != slug || p.Tx == "" {
		return nil, ErrOAuthStateInvalid
	}
	if !time.Unix(p.Exp, 0).After(s.now()) {
		return nil, ErrOAuthStateExpired
	}
	return &p, nil
}

// ---- client redirect rules ---------------------------------------------------------------

func isLoopbackHost(h string) bool {
	h = strings.ToLower(h)
	if h == "localhost" {
		return true
	}
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback()
}

func (s *Service) validateClientRedirect(clientID, redirect, slug string) error {
	switch clientID {
	case clientWeb:
		u, err := url.Parse(redirect)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil ||
			u.Fragment != "" || u.Path != "/auth/callback" || u.Query().Get("provider") != slug {
			return ErrInvalidRedirectURI
		}
		cfg := s.cfg()
		host := strings.ToLower(u.Hostname())
		if !cfg.IsProd() && isLoopbackHost(host) {
			return nil
		}
		origin := strings.ToLower(u.Scheme + "://" + u.Host)
		for _, entry := range cfg.CORSOrigins() {
			entry = strings.ToLower(strings.TrimRight(strings.TrimSpace(entry), "/"))
			if entry != "" && !strings.Contains(entry, "*") && entry == origin {
				return nil
			}
		}
		return ErrInvalidRedirectURI.WithMessage("redirect uri origin is not allowed")
	case clientNative:
		if redirect != nativeRedirect {
			return ErrInvalidRedirectURI.WithMessage("invalid callback URL")
		}
		return nil
	case clientDesktop:
		bad := ErrInvalidRedirectURI.WithMessage("invalid callback URL")
		u, err := url.Parse(redirect)
		if err != nil || u.Scheme != "http" || u.User != nil || u.RawQuery != "" || u.Fragment != "" ||
			u.Path != "/oauth/callback" || !isLoopbackHost(u.Hostname()) {
			return bad
		}
		port, perr := parsePort(u.Port())
		if perr != nil {
			return bad
		}
		_ = port
		return nil
	}
	return ErrProviderInvalid
}

func parsePort(p string) (int, error) {
	if p == "" {
		return 0, ErrInvalidRedirectURI
	}
	n := 0
	for _, r := range p {
		if r < '0' || r > '9' || n > 65535 {
			return 0, ErrInvalidRedirectURI
		}
		n = n*10 + int(r-'0')
	}
	if n < 1 || n > 65535 {
		return 0, ErrInvalidRedirectURI
	}
	return n, nil
}

// ---- provider lookup and endpoints --------------------------------------------------------------

func (s *Service) providerBySlug(ctx context.Context, slug string) (*Provider, error) {
	var p Provider
	if err := s.conn(ctx).Where("slug = ?", slug).First(&p).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrBadRequest
		}
		return nil, err
	}
	return &p, nil
}

func (p *Provider) configuredURLs() []string {
	return []string{p.IssuerURL, p.DiscoveryURL, p.AuthURL, p.TokenURL, p.UserInfoURL, p.JWKSURL}
}

type endpoints struct{ Auth, Token, UserInfo string }
