package auth

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

func isErr(err error, target *apperr.Error) bool { return errors.Is(err, target) }

func newGet(ctx context.Context, rawURL string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	return req, nil
}

func errOrStatus(err error, status int) error {
	if err != nil {
		return err
	}
	return fmt.Errorf("unexpected status %d", status)
}

// providerClaims is the identity a provider vouches for.
type providerClaims struct {
	Subject       string
	Email         string
	EmailVerified bool
	DisplayName   string
	Name          string // the raw name field, empty when the provider sent none
	Avatar        string
	Raw           string
}

// dig reads a possibly dotted path out of a decoded JSON object.
func dig(m map[string]any, path string) (any, bool) {
	var cur any = m
	for _, part := range strings.Split(path, ".") {
		obj, ok := cur.(map[string]any)
		if !ok {
			return nil, false
		}
		if cur, ok = obj[part]; !ok {
			return nil, false
		}
	}
	return cur, true
}

func asString(v any) string {
	switch t := v.(type) {
	case string:
		return strings.TrimSpace(t)
	case json.Number:
		return t.String()
	case bool:
		return fmt.Sprint(t)
	}
	return ""
}

func asBool(v any) (bool, bool) {
	switch t := v.(type) {
	case bool:
		return t, true
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "1", "yes":
			return true, true
		case "false", "0", "no", "":
			return false, true
		}
	case json.Number:
		return t.String() == "1", true
	}
	return false, false
}

func isGitHub(p *Provider, userInfoURL string) bool {
	if strings.EqualFold(p.Slug, "github") || strings.EqualFold(p.Name, "github") {
		return true
	}
	u, err := url.Parse(userInfoURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return (h == "api.github.com" || strings.HasSuffix(h, ".github.com")) && strings.HasSuffix(u.Path, "/user")
}

func isDiscord(p *Provider, userInfoURL string) bool {
	if strings.EqualFold(p.Slug, "discord") || strings.EqualFold(p.Name, "discord") {
		return true
	}
	u, err := url.Parse(userInfoURL)
	if err != nil {
		return false
	}
	h := strings.ToLower(u.Hostname())
	return h == "discord.com" || strings.HasSuffix(h, ".discord.com") || h == "discordapp.com" || strings.HasSuffix(h, ".discordapp.com")
}

func decodeObject(body []byte) (map[string]any, error) {
	dec := json.NewDecoder(bytes.NewReader(body))
	dec.UseNumber()
	var m map[string]any
	if err := dec.Decode(&m); err != nil {
		return nil, err
	}
	return m, nil
}

// fetchProviderClaims redeems the authorization code and reads the profile.
func (s *Service) fetchProviderClaims(ctx context.Context, p *Provider, code, verifier string) (providerClaims, error) {
	ep, err := s.resolveEndpoints(ctx, p)
	if err != nil {
		return providerClaims{}, err
	}
	if ep.Token == "" || ep.UserInfo == "" {
		return providerClaims{}, ErrProviderInvalid
	}
	base, ok := apiBaseURL(s.cfg())
	if !ok {
		return providerClaims{}, ErrProviderCallbackMisconf
	}
	secret, _, err := s.d.Secrets.Decrypt(p.ClientSecretEncrypted)
	if err != nil {
		return providerClaims{}, err
	}
	client := s.httpClient(append(p.configuredURLs(), ep.Auth, ep.Token, ep.UserInfo)...)

	form := url.Values{
		"grant_type": {"authorization_code"}, "code": {code},
		"redirect_uri": {base + "/api/v1/auth/providers/" + p.Slug + "/callback"},
		"client_id":    {p.ClientID}, "client_secret": {secret}, "code_verifier": {verifier},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, ep.Token, strings.NewReader(form.Encode()))
	if err != nil {
		return providerClaims{}, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")
	status, body, err := doCapped(client, req)
	if err != nil || status < 200 || status > 299 {
		return providerClaims{}, ErrProviderUpstreamFailed.WithCause(errOrStatus(err, status))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
	}
	if json.Unmarshal(body, &tok) != nil || strings.TrimSpace(tok.AccessToken) == "" {
		return providerClaims{}, ErrProviderUpstreamFailed.WithCause(errors.New("token response without access_token"))
	}

	getJSON := func(rawURL string) ([]byte, error) {
		r, err := newGet(ctx, rawURL)
		if err != nil {
			return nil, err
		}
		r.Header.Set("Authorization", "Bearer "+tok.AccessToken)
		st, b, err := doCapped(client, r)
		if err != nil || st < 200 || st > 299 {
			return nil, ErrProviderUpstreamFailed.WithCause(errOrStatus(err, st))
		}
		return b, nil
	}
	infoBody, err := getJSON(ep.UserInfo)
	if err != nil {
		return providerClaims{}, err
	}
	profile, err := decodeObject(infoBody)
	if err != nil {
		return providerClaims{}, ErrProviderUpstreamFailed.WithCause(err)
	}
	c, err := claimsFromProfile(p, ep.UserInfo, profile)
	if err != nil {
		return providerClaims{}, err
	}
	c.Raw = string(infoBody)

	if isGitHub(p, ep.UserInfo) && !c.EmailVerified {
		if mailBody, merr := getJSON(strings.TrimRight(ep.UserInfo, "/") + "/emails"); merr == nil {
			if email, ok := pickGitHubEmail(mailBody, c.Email); ok {
				c.Email, c.EmailVerified = email, true
			}
		}
	}
	return c, nil
}

func claimsFromProfile(p *Provider, userInfoURL string, profile map[string]any) (providerClaims, error) {
	var c providerClaims
	if v, ok := dig(profile, p.SubjectField); ok {
		c.Subject = asString(v)
	}
	if c.Subject == "" {
		return c, ErrProviderSubjectMissing
	}
	if v, ok := dig(profile, p.EmailField); ok {
		raw := asString(v)
		if raw != "" {
			email, err := normalizeEmail(raw)
			if err != nil {
				return c, err
			}
			c.Email = email
		}
	}
	fields := []string{p.EmailVerifiedField, "email_verified", "verified_email"}
	if isDiscord(p, userInfoURL) {
		fields = append(fields, "verified")
	}
	for _, f := range fields {
		if f == "" {
			continue
		}
		if v, ok := dig(profile, f); ok {
			if b, ok := asBool(v); ok {
				c.EmailVerified = b
				break
			}
		}
	}
	if v, ok := dig(profile, p.NameField); ok {
		c.Name = asString(v)
	}
	c.DisplayName = c.Name
	if c.DisplayName == "" {
		c.DisplayName = c.Email
	}
	if c.DisplayName == "" {
		c.DisplayName = c.Subject
	}
	if v, ok := dig(profile, p.AvatarField); ok {
		c.Avatar = asString(v)
	}
	return c, nil
}

// pickGitHubEmail chooses a verified address from GitHub's email list: the one
// equal to the profile email, else the primary one, else the first verified.
func pickGitHubEmail(body []byte, profileEmail string) (string, bool) {
	var list []struct {
		Email    string `json:"email"`
		Primary  bool   `json:"primary"`
		Verified bool   `json:"verified"`
	}
	if json.Unmarshal(body, &list) != nil {
		return "", false
	}
	norm := func(e string) string {
		n, err := normalizeEmail(e)
		if err != nil {
			return ""
		}
		return n
	}
	if profileEmail != "" {
		for _, e := range list {
			if e.Verified && norm(e.Email) == profileEmail {
				return profileEmail, true
			}
		}
	}
	for _, e := range list {
		if e.Verified && e.Primary && norm(e.Email) != "" {
			return norm(e.Email), true
		}
	}
	for _, e := range list {
		if e.Verified && norm(e.Email) != "" {
			return norm(e.Email), true
		}
	}
	return "", false
}
