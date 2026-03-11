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

// resolveEndpoints returns the provider's endpoints, consulting the OIDC
// discovery document for any that are not configured.
func (s *Service) resolveEndpoints(ctx context.Context, p *Provider) (endpoints, error) {
	ep := endpoints{Auth: p.AuthURL, Token: p.TokenURL, UserInfo: p.UserInfoURL}
	if ep.Auth != "" && ep.Token != "" && ep.UserInfo != "" {
		return ep, nil
	}
	if p.Type != "oidc" {
		return ep, nil
	}
	disco := strings.TrimSpace(p.DiscoveryURL)
	if disco == "" {
		if strings.TrimSpace(p.IssuerURL) == "" {
			return ep, nil
		}
		disco = strings.TrimRight(strings.TrimSpace(p.IssuerURL), "/") + "/.well-known/openid-configuration"
	}
	if err := validateEndpointURL(disco); err != nil {
		return ep, err
	}
	req, err := newGet(ctx, disco)
	if err != nil {
		return ep, ErrProviderUpstreamFailed.WithCause(err)
	}
	status, body, err := doCapped(s.httpClient(append(p.configuredURLs(), disco)...), req)
	if err != nil || status < 200 || status > 299 {
		return ep, ErrProviderUpstreamFailed.WithCause(errOrStatus(err, status))
	}
	var doc struct {
		Auth     string `json:"authorization_endpoint"`
		Token    string `json:"token_endpoint"`
		UserInfo string `json:"userinfo_endpoint"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return ep, ErrProviderUpstreamFailed
	}
	if ep.Auth == "" {
		ep.Auth = doc.Auth
	}
	if ep.Token == "" {
		ep.Token = doc.Token
	}
	if ep.UserInfo == "" {
		ep.UserInfo = doc.UserInfo
	}
	for _, u := range []string{ep.Auth, ep.Token, ep.UserInfo} {
		if u != "" {
			if err := validateEndpointURL(u); err != nil {
				return ep, err
			}
		}
	}
	return ep, nil
}

// ---- authorize ------------------------------------------------------------------------------------

type authorizeInput struct {
	ClientID      string
	RedirectURI   string
	CodeChallenge string
	ClientState   string
	Intent        string
	Next          string
}

type authorizeResult struct {
	AuthorizationURL string    `json:"authorizationURL"`
	ExpiresAt        time.Time `json:"expiresAt"`
}

func safeNext(next string) string {
	next = strings.TrimSpace(next)
	if strings.HasPrefix(next, "/") && !strings.HasPrefix(next, "//") && !strings.Contains(next, "\\") {
		return next
	}
	return "/chat"
}

// ProviderAuthorize starts a provider sign-in (userID 0) or a bind (userID set).
func (s *Service) ProviderAuthorize(ctx context.Context, slug string, in authorizeInput, bindUser uint) (authorizeResult, error) {
	if s.d.Cache == nil {
		return authorizeResult{}, ErrProviderInvalid.WithMessage("provider auth bridge is not configured")
	}
	st := s.settings(ctx)
	if !st.ThirdPartyLoginEnabled {
		return authorizeResult{}, ErrProviderLoginDisabled
	}
	if err := s.validateClientRedirect(in.ClientID, in.RedirectURI, slug); err != nil {
		return authorizeResult{}, err
	}
	if !pkcePattern.MatchString(in.CodeChallenge) || !pkcePattern.MatchString(in.ClientState) {
		return authorizeResult{}, ErrInvalidPKCE
	}
	p, err := s.providerBySlug(ctx, slug)
	if err != nil {
		return authorizeResult{}, err
	}
	intent := in.Intent
	if bindUser != 0 {
		intent = intentBind
	}
	switch intent {
	case "", intentLogin:
		intent = intentLogin
		if !p.LoginEnabled {
			return authorizeResult{}, ErrProviderLoginDisabled
		}
	case intentRegister:
		if !p.LoginEnabled {
			return authorizeResult{}, ErrProviderLoginDisabled
		}
		if !p.RegistrationEnabled {
			return authorizeResult{}, ErrProviderRegistrationOff
		}
	case intentBind:
	default:
		return authorizeResult{}, ErrBadRequest
	}
	ep, err := s.resolveEndpoints(ctx, p)
	if err != nil {
		return authorizeResult{}, err
	}
	base, ok := apiBaseURL(s.cfg())
	if !ok {
		return authorizeResult{}, ErrProviderCallbackMisconf
	}
	if ep.Auth == "" {
		return authorizeResult{}, ErrProviderAuthURLMissing
	}
	callback := base + "/api/v1/auth/providers/" + slug + "/callback"

	now := s.now()
	exp := now.Add(bridgeTxTTL)
	verifier := randomURLToken(48)
	txID := randomURLToken(32)
	tx := bridgeTx{
		Slug: slug, ClientID: in.ClientID, Redirect: in.RedirectURI, ClientState: in.ClientState,
		ClientChallenge: in.CodeChallenge, Verifier: verifier, Intent: intent, UserID: bindUser,
		Next: safeNext(in.Next), ExpiresAt: exp,
	}
	if err := cache.SetJSON(ctx, s.d.Cache, txKey(txID), tx, bridgeTxTTL); err != nil {
		return authorizeResult{}, ErrProviderInvalid.WithMessage("provider auth bridge is not configured").WithCause(err)
	}

	authURL, err := url.Parse(ep.Auth)
	if err != nil {
		return authorizeResult{}, ErrProviderAuthURLMissing.WithCause(err)
	}
	q := authURL.Query()
	q.Set("client_id", p.ClientID)
	q.Set("redirect_uri", callback)
	q.Set("response_type", "code")
	q.Set("scope", p.Scopes)
	q.Set("state", s.signState(slug, txID, exp))
	q.Set("code_challenge", pkceChallenge(verifier))
	q.Set("code_challenge_method", "S256")
	authURL.RawQuery = q.Encode()
	return authorizeResult{AuthorizationURL: authURL.String(), ExpiresAt: exp}, nil
}

// ---- callback -------------------------------------------------------------------------------------------

// ProviderCallback finishes the provider leg and returns the URL to send the
// client to. An error means no safe redirect target exists.
func (s *Service) ProviderCallback(ctx context.Context, slug, code, state, providerErr string) (string, error) {
	payload, err := s.openState(state, slug)
	if err != nil {
		return "", err
	}
	if s.d.Cache == nil {
		return "", ErrOAuthStateExpired
	}
	tx, found, err := cache.ConsumeJSON[bridgeTx](ctx, s.d.Cache, txKey(payload.Tx))
	if err != nil {
		return "", ErrOAuthStateExpired.WithCause(err)
	}
	if !found {
		return "", ErrOAuthStateExpired
	}
	if tx.Slug != slug || !tx.ExpiresAt.After(s.now()) {
		return "", ErrOAuthStateInvalid
	}

	grant := bridgeGrant{Slug: slug, ClientID: tx.ClientID, Intent: tx.Intent, ExpiresAt: s.now().Add(bridgeGrantTTL)}
	switch {
	case strings.TrimSpace(providerErr) != "":
		grant.ErrCode = grantErrProvider
	case strings.TrimSpace(code) == "":
		grant.ErrCode = grantErrProvider
	default:
		s.completeProviderLeg(ctx, &tx, strings.TrimSpace(code), &grant)
	}

	id := randomURLToken(32)
	if err := cache.SetJSON(ctx, s.d.Cache, grantKey(id, tx.ClientChallenge), grant, bridgeGrantTTL); err != nil {
		return "", ErrOAuthStateExpired.WithCause(err)
	}
	target, err := url.Parse(tx.Redirect)
	if err != nil {
		return "", ErrInvalidRedirectURI
	}
	q := target.Query()
	q.Set("provider", slug)
	q.Set("grant", id)
	q.Set("state", tx.ClientState)
	q.Set("intent", tx.Intent)
	q.Set("next", tx.Next)
	target.RawQuery = q.Encode()
	return target.String(), nil
}

// completeProviderLeg exchanges the code, reads the profile and resolves the
// user. Failures are recorded in the grant instead of failing the callback.
func (s *Service) completeProviderLeg(ctx context.Context, tx *bridgeTx, code string, grant *bridgeGrant) {
	fail := func(err error, detail string) {
		grant.ErrCode = grantErrProvider
		switch {
		case isErr(err, ErrProviderSubjectMissing):
			grant.ErrCode = grantErrNoSubject
		case isErr(err, ErrInvalidEmail):
			grant.ErrCode = grantErrInvalidEmail
		case isErr(err, ErrProviderLoginDisabled):
			grant.ErrCode = grantErrLoginDisabled
		case isErr(err, ErrProviderAccountNotReg):
			grant.ErrCode = grantErrNotRegistered
		}
		if grant.ErrCode == grantErrProvider {
			s.log.Warn("provider_callback_failed", logx.F("provider", tx.Slug), logx.F("stage", detail), logx.Err(err))
		}
	}
	p, err := s.providerBySlug(ctx, tx.Slug)
	if err != nil {
		fail(err, "provider")
		return
	}
	claims, err := s.fetchProviderClaims(ctx, p, code, tx.Verifier)
	if err != nil {
		fail(err, "claims")
		return
	}
	grant.Subject = claims.Subject
	switch tx.Intent {
	case intentBind:
		grant.UserID = tx.UserID
		grant.Profile = &bridgeProfile{DisplayName: claims.DisplayName, Email: claims.Email,
			EmailVerified: claims.EmailVerified, Raw: claims.Raw}
	default:
		uid, err := s.resolveProviderUser(ctx, p, claims)
		if err != nil {
			if isErr(err, ErrProviderEmailConflict) {
				grant.ErrCode, grant.ErrEmail = grantErrEmailConflict, claims.Email
				return
			}
			fail(err, "resolve")
			return
		}
		grant.UserID = uid
	}
}
