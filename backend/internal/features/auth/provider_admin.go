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

// ListProviders returns every provider, disabled ones included.
func (s *Service) ListProviders(ctx context.Context) ([]providerView, error) {
	var rows []Provider
	if err := s.conn(ctx).Order("sort_order ASC, id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]providerView, 0, len(rows))
	for i := range rows {
		out = append(out, toProviderView(&rows[i], true))
	}
	return out, nil
}

func (s *Service) providerByPublicID(ctx context.Context, tx *gorm.DB, id string) (*Provider, error) {
	var p Provider
	if err := tx.WithContext(ctx).Where("public_id = ?", strings.TrimSpace(id)).First(&p).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, ErrBadRequest
		}
		return nil, err
	}
	return &p, nil
}

// CreateProvider validates and stores a new provider.
func (s *Service) CreateProvider(ctx context.Context, actorRole string, in providerInput) (providerView, error) {
	p, secret, err := s.buildProvider(in, actorRole, nil)
	if err != nil {
		return providerView{}, err
	}
	if secret == "" {
		return providerView{}, ErrProviderClientSecretReq
	}
	if p.ClientSecretEncrypted, err = s.d.Secrets.Encrypt(secret); err != nil {
		return providerView{}, err
	}
	p.PublicID = newPublicID()
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// A soft-deleted provider holding the slug would trip the unique index.
		if err := tx.Unscoped().Where("slug = ? AND deleted_at IS NOT NULL", p.Slug).Delete(&Provider{}).Error; err != nil {
			return err
		}
		return tx.Create(p).Error
	})
	if err != nil {
		if database.IsDuplicate(err) {
			return providerView{}, ErrBadRequest.WithCause(err)
		}
		return providerView{}, err
	}
	return toProviderView(p, true), nil
}

// UpdateProvider re-specifies an existing provider.
func (s *Service) UpdateProvider(ctx context.Context, actorRole, publicID string, in providerInput) (providerView, error) {
	cur, err := s.providerByPublicID(ctx, s.db, publicID)
	if err != nil {
		return providerView{}, err
	}
	p, secret, err := s.buildProvider(in, actorRole, cur)
	if err != nil {
		return providerView{}, err
	}
	enc := cur.ClientSecretEncrypted
	if secret != "" {
		if enc, err = s.d.Secrets.Encrypt(secret); err != nil {
			return providerView{}, err
		}
	}
	err = s.conn(ctx).Model(&Provider{}).Where("id = ?", cur.ID).Updates(map[string]any{
		"type": p.Type, "name": p.Name, "slug": p.Slug, "logo_url": p.LogoURL,
		"login_enabled": p.LoginEnabled, "registration_enabled": p.RegistrationEnabled,
		"client_id": p.ClientID, "client_secret_encrypted": enc, "issuer_url": p.IssuerURL,
		"discovery_url": p.DiscoveryURL, "auth_url": p.AuthURL, "token_url": p.TokenURL,
		"user_info_url": p.UserInfoURL, "jwks_url": p.JWKSURL, "scopes": p.Scopes,
		"pkce_enabled": true, "default_role": p.DefaultRole, "subject_field": p.SubjectField,
		"email_field": p.EmailField, "email_verified_field": p.EmailVerifiedField,
		"name_field": p.NameField, "avatar_field": p.AvatarField,
	}).Error
	if err != nil {
		if database.IsDuplicate(err) {
			return providerView{}, ErrBadRequest.WithCause(err)
		}
		return providerView{}, err
	}
	fresh, err := s.providerByPublicID(ctx, s.db, publicID)
	if err != nil {
		return providerView{}, err
	}
	return toProviderView(fresh, true), nil
}

// ReorderProviders assigns sort orders 100, 200, ... in the given order.
func (s *Service) ReorderProviders(ctx context.Context, ids []string) error {
	clean := make([]string, 0, len(ids))
	seen := map[string]bool{}
	for _, id := range ids {
		id = strings.TrimSpace(id)
		if id == "" {
			return ErrProviderIDRequired
		}
		if seen[id] {
			return ErrProviderOrderInvalid
		}
		seen[id] = true
		clean = append(clean, id)
	}
	return s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var rows []Provider
		if err := tx.Where("public_id IN ?", clean).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) != len(clean) {
			return ErrBadRequest
		}
		for i, id := range clean {
			if err := tx.Model(&Provider{}).Where("public_id = ?", id).Update("sort_order", 100*(i+1)).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// dependentUsers counts users whose only login method is this provider.
func (s *Service) dependentUsers(ctx context.Context, tx *gorm.DB, providerID uint) (int, error) {
	var userIDs []uint
	if err := tx.WithContext(ctx).Model(&UserLink{}).Where("provider_id = ?", providerID).Pluck("user_id", &userIDs).Error; err != nil {
		return 0, err
	}
	if len(userIDs) == 0 {
		return 0, nil
	}
	var noPassword []uint
	if err := tx.WithContext(ctx).Model(&Credential{}).
		Where("user_id IN ? AND password_enabled = ?", userIDs, false).Pluck("user_id", &noPassword).Error; err != nil {
		return 0, err
	}
	// Users with no credential row at all cannot use a password either.
	var withCred []uint
	if err := tx.WithContext(ctx).Model(&Credential{}).Where("user_id IN ?", userIDs).Pluck("user_id", &withCred).Error; err != nil {
		return 0, err
	}
	have := map[uint]bool{}
	for _, id := range withCred {
		have[id] = true
	}
	candidates := map[uint]bool{}
	for _, id := range noPassword {
		candidates[id] = true
	}
	for _, id := range userIDs {
		if !have[id] {
			candidates[id] = true
		}
	}
	count := 0
	for id := range candidates {
		var others int64
		if err := tx.WithContext(ctx).Model(&UserLink{}).
			Where("user_id = ? AND provider_id <> ?", id, providerID).Count(&others).Error; err != nil {
			return 0, err
		}
		if others == 0 {
			count++
		}
	}
	return count, nil
}

// DeleteProvider removes a provider. Without force it refuses when some user
// would lose their only login method.
func (s *Service) DeleteProvider(ctx context.Context, publicID string, force bool) error {
	return s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		p, err := s.providerByPublicID(ctx, tx, publicID)
		if err != nil {
			return err
		}
		if !force {
			n, err := s.dependentUsers(ctx, tx, p.ID)
			if err != nil {
				return err
			}
			if n > 0 {
				return ErrProviderDeleteConflict.WithDetails(map[string]any{"dependentUsers": n})
			}
		}
		if err := tx.Unscoped().Where("provider_id = ?", p.ID).Delete(&UserLink{}).Error; err != nil {
			return err
		}
		return tx.Unscoped().Delete(&Provider{}, p.ID).Error
	})
}
