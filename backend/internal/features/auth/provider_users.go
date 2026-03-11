package auth

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/cache"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

// conflictError builds the email-conflict error with its sign-in-then-bind hint.
func conflictError(slug, email string, withDetails bool) error {
	e := ErrProviderEmailConflict
	if withDetails {
		return e.WithDetails(map[string]any{"providerSlug": slug, "email": email, "action": "sign_in_then_bind"})
	}
	// Bind flows answer 400 and carry no details.
	return e.WithStatus(400)
}

func (s *Service) userByEmail(ctx context.Context, email string) (*User, error) {
	var u User
	if err := s.conn(ctx).Where("email = ?", email).Order("id ASC").First(&u).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &u, nil
}

func (s *Service) linkFor(ctx context.Context, tx *gorm.DB, providerID uint, subject string) (*UserLink, error) {
	var l UserLink
	err := tx.WithContext(ctx).Where("provider_id = ? AND provider_subject = ?", providerID, subject).First(&l).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &l, nil
}

func linkFromClaims(p *Provider, c providerClaims, now time.Time) UserLink {
	return UserLink{
		ProviderID: p.ID, ProviderSubject: c.Subject, ProviderType: p.Type,
		ProviderDisplayName: clip(c.DisplayName, 128), Email: c.Email, EmailVerified: c.EmailVerified,
		ProfileJSON: c.Raw, LinkedAt: now, LastLoginAt: &now,
	}
}

func (s *Service) refreshLink(ctx context.Context, tx *gorm.DB, l *UserLink, c providerClaims, now time.Time) error {
	return tx.WithContext(ctx).Model(&UserLink{}).Where("id = ?", l.ID).Updates(map[string]any{
		"provider_display_name": clip(c.DisplayName, 128), "email": c.Email,
		"email_verified": c.EmailVerified, "profile_json": c.Raw, "last_login_at": now,
	}).Error
}

// resolveProviderUser finds or creates the account for a provider identity. A
// returned user id may belong to an account that is not active; the exchange
// step turns that into the right error.
func (s *Service) resolveProviderUser(ctx context.Context, p *Provider, c providerClaims) (uint, error) {
	st := s.settings(ctx)
	now := s.now()

	if link, err := s.linkFor(ctx, s.db, p.ID, c.Subject); err != nil {
		return 0, err
	} else if link != nil {
		if !p.LoginEnabled {
			return 0, ErrProviderLoginDisabled
		}
		u, err := s.userByID(ctx, link.UserID)
		if err != nil {
			return 0, err
		}
		if u.Status == StatusActive {
			if err := s.refreshLink(ctx, s.db, link, c, now); err != nil {
				return 0, err
			}
		}
		return u.ID, nil
	}

	if c.Email != "" {
		existing, err := s.userByEmail(ctx, c.Email)
		if err != nil {
			return 0, err
		}
		if existing != nil {
			if !(st.AutoLinkVerifiedEmail && c.EmailVerified) {
				return 0, conflictError(p.Slug, c.Email, true)
			}
			if !p.LoginEnabled {
				return 0, ErrProviderLoginDisabled
			}
			if existing.Status != StatusActive {
				return existing.ID, nil
			}
			l := linkFromClaims(p, c, now)
			l.UserID = existing.ID
			if err := s.conn(ctx).Create(&l).Error; err != nil {
				if database.IsDuplicate(err) {
					return 0, ErrProviderIdentityConflict
				}
				return 0, err
			}
			return existing.ID, nil
		}
	}

	if !p.RegistrationEnabled {
		return 0, ErrProviderAccountNotReg
	}
	return s.registerProviderUser(ctx, p, c, now)
}

func (s *Service) registerProviderUser(ctx context.Context, p *Provider, c providerClaims, now time.Time) (uint, error) {
	hash, err := hashPassword(randomURLToken(24))
	if err != nil {
		return 0, err
	}
	name := c.Name
	if strings.TrimSpace(name) == "" {
		name = p.Name + " user"
	}
	role := p.DefaultRole
	if role == "" {
		role = RoleUser
	}
	u := User{
		DisplayName: normalizeDisplayName(name), AvatarURL: clip(c.Avatar, 2048), Email: c.Email,
		Role: role, Status: StatusActive, Timezone: "Etc/UTC", Locale: "en-US",
	}
	if c.Email != "" {
		if c.EmailVerified {
			u.EmailSource, u.EmailVerifiedAt = EmailSourceProviderVerified, &now
		} else {
			u.EmailSource = EmailSourceProviderUnverified
		}
	}
	cred := Credential{
		PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: false, PasswordOrigin: PasswordOriginSSOPlaceholder,
	}
	l := linkFromClaims(p, c, now)
	slugSeed := p.Slug
	if slugify(slugSeed) == "" {
		slugSeed = "oauth"
	}
	base := generatedUsername(slugSeed, "oauth", p.Slug+":"+c.Subject)
	created, err := s.createAccount(ctx, u, cred, &l, func(n int) string {
		if n == 0 {
			return base
		}
		return suffixedUsername(base, n+1)
	})
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

// ---- exchange ----------------------------------------------------------------------------

type exchangeInput struct {
	ClientID     string
	Grant        string
	CodeVerifier string
}

// consumeGrant redeems a grant with its verifier and checks it belongs to the
// slug and client.
func (s *Service) consumeGrant(ctx context.Context, slug string, in exchangeInput) (*bridgeGrant, error) {
	if !pkcePattern.MatchString(in.CodeVerifier) || !pkcePattern.MatchString(in.Grant) {
		return nil, ErrInvalidPKCE
	}
	if s.d.Cache == nil {
		return nil, ErrOAuthStateExpired
	}
	g, found, err := cache.ConsumeJSON[bridgeGrant](ctx, s.d.Cache, grantKey(in.Grant, pkceChallenge(in.CodeVerifier)))
	if err != nil {
		return nil, ErrOAuthStateExpired.WithCause(err)
	}
	if !found {
		return nil, ErrOAuthStateExpired
	}
	if g.Slug != slug || g.ClientID != in.ClientID || !g.ExpiresAt.After(s.now()) {
		return nil, ErrOAuthStateInvalid
	}
	return &g, nil
}

// grantError maps an error stored in a grant to the response error.
func grantError(g *bridgeGrant, withDetails bool) error {
	switch g.ErrCode {
	case "":
		return nil
	case grantErrEmailConflict:
		return conflictError(g.Slug, g.ErrEmail, withDetails)
	case grantErrLoginDisabled:
		return ErrProviderLoginDisabled
	case grantErrNotRegistered:
		return ErrProviderAccountNotReg
	case grantErrNoSubject:
		return ErrProviderSubjectMissing
	case grantErrInvalidEmail:
		return ErrInvalidEmail
	}
	return ErrProviderInvalid
}
