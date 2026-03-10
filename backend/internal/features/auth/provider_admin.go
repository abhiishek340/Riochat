package auth

import (
	"context"
	"net/url"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// providerInput is the body of provider create and update. An update is a full
// re-specification: omitted optional fields revert to their defaults.
type providerInput struct {
	Type                string
	Name                string
	Slug                string
	LogoURL             string
	LoginEnabled        *bool
	RegistrationEnabled *bool
	ClientID            string
	ClientSecret        string
	IssuerURL           string
	DiscoveryURL        string
	AuthURL             string
	TokenURL            string
	UserInfoURL         string
	JWKSURL             string
	Scopes              string
	DefaultRole         string
	SubjectField        string
	EmailField          string
	EmailVerifiedField  string
	NameField           string
	AvatarField         string
}

// providerView is the admin and login-options shape of a provider.
type providerView struct {
	PublicID            string    `json:"publicID"`
	Type                string    `json:"type"`
	Name                string    `json:"name"`
	Slug                string    `json:"slug"`
	LogoURL             string    `json:"logoURL"`
	LoginEnabled        bool      `json:"loginEnabled"`
	RegistrationEnabled bool      `json:"registrationEnabled"`
	ClientID            string    `json:"clientID,omitempty"`
	IssuerURL           string    `json:"issuerURL,omitempty"`
	DiscoveryURL        string    `json:"discoveryURL,omitempty"`
	AuthURL             string    `json:"authURL,omitempty"`
	TokenURL            string    `json:"tokenURL,omitempty"`
	UserInfoURL         string    `json:"userinfoURL,omitempty"`
	JWKSURL             string    `json:"jwksURL,omitempty"`
	Scopes              string    `json:"scopes"`
	DefaultRole         string    `json:"defaultRole"`
	SubjectField        string    `json:"subjectField"`
	EmailField          string    `json:"emailField"`
	EmailVerifiedField  string    `json:"emailVerifiedField"`
	NameField           string    `json:"nameField"`
	AvatarField         string    `json:"avatarField"`
	CreatedAt           time.Time `json:"createdAt"`
	UpdatedAt           time.Time `json:"updatedAt"`
}

func toProviderView(p *Provider, admin bool) providerView {
	v := providerView{
		PublicID: p.PublicID, Type: p.Type, Name: p.Name, Slug: p.Slug, LogoURL: p.LogoURL,
		LoginEnabled: p.LoginEnabled, RegistrationEnabled: p.RegistrationEnabled,
		IssuerURL: p.IssuerURL, DiscoveryURL: p.DiscoveryURL, AuthURL: p.AuthURL, TokenURL: p.TokenURL,
		UserInfoURL: p.UserInfoURL, JWKSURL: p.JWKSURL, Scopes: p.Scopes, DefaultRole: p.DefaultRole,
		SubjectField: p.SubjectField, EmailField: p.EmailField, EmailVerifiedField: p.EmailVerifiedField,
		NameField: p.NameField, AvatarField: p.AvatarField, CreatedAt: p.CreatedAt, UpdatedAt: p.UpdatedAt,
	}
	if admin {
		v.ClientID = p.ClientID
	}
	return v
}

func orDefault(v, def string) string {
	if v = strings.TrimSpace(v); v != "" {
		return v
	}
	return def
}

func validLogoURL(raw string) bool {
	if raw == "" {
		return true
	}
	if strings.HasPrefix(raw, "/") {
		return !strings.HasPrefix(raw, "//") && !strings.Contains(raw, "\\")
	}
	u, err := url.Parse(raw)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != "" && u.User == nil
}

// buildProvider validates in and returns the row to store plus the plaintext
// secret ("" keeps the stored one on update).
func (s *Service) buildProvider(in providerInput, actorRole string, existing *Provider) (*Provider, string, error) {
	typ := strings.ToLower(strings.TrimSpace(in.Type))
	if typ != "oidc" && typ != "oauth2" {
		return nil, "", ErrProviderTypeInvalid
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		return nil, "", ErrProviderNameRequired
	}
	slug := slugify(in.Slug)
	if slug == "" {
		slug = slugify(name)
	}
	slug = strings.Trim(truncateRunes(slug, 64), "-_")
	if slug == "" {
		return nil, "", ErrProviderSlugRequired
	}
	scopes := strings.TrimSpace(in.Scopes)
	if scopes == "" {
		if typ == "oidc" {
			scopes = "openid profile email"
		} else {
			scopes = "profile email"
		}
	}
	role := strings.TrimSpace(in.DefaultRole)
	if role == "" {
		role = RoleUser
	}
	switch role {
	case RoleUser, RoleAdmin:
	case RoleSuperAdmin:
		if actorRole != RoleSuperAdmin {
			return nil, "", ErrProviderSuperadminRole
		}
	default:
		return nil, "", ErrProviderDefaultRoleInvalid
	}
	logo := strings.TrimSpace(in.LogoURL)
	if !validLogoURL(logo) {
		return nil, "", ErrProviderLogoURLInvalid
	}
	login, reg := true, true
	if in.LoginEnabled != nil {
		login = *in.LoginEnabled
	}
	if in.RegistrationEnabled != nil {
		reg = *in.RegistrationEnabled
	}
	if reg && !login {
		return nil, "", ErrProviderRegistrationNeedsLo
	}
	clientID := strings.TrimSpace(in.ClientID)
	if clientID == "" {
		return nil, "", ErrProviderClientIDRequired
	}
	secret := strings.TrimSpace(in.ClientSecret)
	if secret == "" && (existing == nil || existing.ClientSecretEncrypted == "") {
		return nil, "", ErrProviderClientSecretReq
	}
	p := &Provider{
		Type: typ, Name: name, Slug: slug, LogoURL: logo, LoginEnabled: login, RegistrationEnabled: reg,
		ClientID: clientID, IssuerURL: strings.TrimSpace(in.IssuerURL), DiscoveryURL: strings.TrimSpace(in.DiscoveryURL),
		AuthURL: strings.TrimSpace(in.AuthURL), TokenURL: strings.TrimSpace(in.TokenURL),
		UserInfoURL: strings.TrimSpace(in.UserInfoURL), JWKSURL: strings.TrimSpace(in.JWKSURL),
		Scopes: scopes, PKCEEnabled: true, DefaultRole: role,
		SubjectField: orDefault(in.SubjectField, "sub"), EmailField: orDefault(in.EmailField, "email"),
		EmailVerifiedField: orDefault(in.EmailVerifiedField, "email_verified"),
		NameField:          orDefault(in.NameField, "name"), AvatarField: orDefault(in.AvatarField, "picture"),
		SortOrder: 100,
	}
	if typ == "oidc" {
		if p.IssuerURL == "" && p.DiscoveryURL == "" {
			return nil, "", ErrProviderOIDCIssuerRequired
		}
	} else if p.AuthURL == "" || p.TokenURL == "" || p.UserInfoURL == "" {
		return nil, "", ErrProviderOAuthURLsRequired
	}
	for _, u := range []string{p.IssuerURL, p.DiscoveryURL, p.AuthURL, p.TokenURL, p.UserInfoURL, p.JWKSURL} {
		if u == "" {
			continue
		}
		if err := validateEndpointURL(u); err != nil {
			return nil, "", err
		}
	}
	return p, secret, nil
}
