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

// ProviderExchange redeems a login or register grant for a session.
func (s *Service) ProviderExchange(ctx context.Context, slug string, in exchangeInput) (signIn, error) {
	g, err := s.consumeGrant(ctx, slug, in)
	if err != nil {
		return signIn{}, err
	}
	if gerr := grantError(g, true); gerr != nil {
		s.event(ctx, eventInput{Type: "provider_login", Result: resultFailure, Reason: g.ErrCode,
			Detail: map[string]any{"provider": slug}})
		return signIn{}, gerr
	}
	if g.Intent == intentBind {
		return signIn{}, ErrOAuthStateInvalid
	}
	u, err := s.userByID(ctx, g.UserID)
	if err != nil {
		if database.IsNotFound(err) {
			return signIn{}, ErrInvalidCredentials.WithStatus(400)
		}
		return signIn{}, err
	}
	detail := map[string]any{"provider": slug, "subject": g.Subject}
	switch {
	case u.Status == StatusLocked:
		cred, _ := s.credentialOf(ctx, u.ID)
		var until *time.Time
		if cred != nil {
			until = cred.LockedUntil
		}
		s.event(ctx, eventInput{Type: "provider_login", Result: resultFailure, Reason: reasonAccountLocked, UserID: u.ID, Detail: detail})
		return signIn{}, &lockedError{retryAfter: retryAfterSeconds(until, s.now())}
	case u.Status != StatusActive:
		s.event(ctx, eventInput{Type: "provider_login", Result: resultFailure, Reason: "account not active", UserID: u.ID, Detail: detail})
		return signIn{}, ErrInvalidCredentials.WithStatus(400)
	}
	mfa, err := s.mfaOf(ctx, u.ID)
	if err != nil {
		return signIn{}, err
	}
	if totpActive(mfa) {
		out, err := s.buildChallenge(ctx, u)
		if err != nil {
			return signIn{}, err
		}
		s.event(ctx, eventInput{Type: "provider_login", Result: resultChallenge, UserID: u.ID, Detail: detail})
		return out, nil
	}
	out, sess, err := s.newSession(ctx, u)
	if err != nil {
		return signIn{}, err
	}
	for k, v := range sessionDetail(sess) {
		detail[k] = v
	}
	s.event(ctx, eventInput{Type: "provider_login", Result: resultSuccess, UserID: u.ID, Detail: detail})
	return out, nil
}

// identityView is one bound external identity.
type identityView struct {
	ID                  uint       `json:"id"`
	ProviderID          string     `json:"providerID"`
	ProviderType        string     `json:"providerType"`
	ProviderName        string     `json:"providerName"`
	ProviderSlug        string     `json:"providerSlug"`
	ProviderLogoURL     string     `json:"providerLogoURL"`
	ProviderDisplayName string     `json:"providerDisplayName"`
	Email               string     `json:"email"`
	EmailVerified       bool       `json:"emailVerified"`
	LinkedAt            time.Time  `json:"linkedAt"`
	LastLoginAt         *time.Time `json:"lastLoginAt"`
}

func (s *Service) identityViews(ctx context.Context, links []UserLink) ([]identityView, error) {
	out := make([]identityView, 0, len(links))
	provs := map[uint]*Provider{}
	for i := range links {
		l := &links[i]
		p, ok := provs[l.ProviderID]
		if !ok {
			var row Provider
			if err := s.conn(ctx).Unscoped().First(&row, l.ProviderID).Error; err == nil {
				p = &row
			}
			provs[l.ProviderID] = p
		}
		v := identityView{
			ID: l.ID, ProviderType: l.ProviderType, ProviderDisplayName: l.ProviderDisplayName,
			Email: l.Email, EmailVerified: l.EmailVerified, LinkedAt: l.LinkedAt, LastLoginAt: l.LastLoginAt,
		}
		if p != nil {
			v.ProviderID, v.ProviderName, v.ProviderSlug, v.ProviderLogoURL = p.PublicID, p.Name, p.Slug, p.LogoURL
		}
		out = append(out, v)
	}
	return out, nil
}

// ListIdentities returns the caller's bound identities.
func (s *Service) ListIdentities(ctx context.Context, userID uint) ([]identityView, error) {
	var links []UserLink
	if err := s.conn(ctx).Where("user_id = ?", userID).Order("id ASC").Find(&links).Error; err != nil {
		return nil, err
	}
	return s.identityViews(ctx, links)
}

// BindExchange redeems a bind grant for the signed-in user.
func (s *Service) BindExchange(ctx context.Context, userID uint, slug string, in exchangeInput) (identityView, error) {
	g, err := s.consumeGrant(ctx, slug, in)
	if err != nil {
		return identityView{}, err
	}
	if gerr := grantError(g, false); gerr != nil {
		return identityView{}, gerr
	}
	if g.Intent != intentBind || g.UserID != userID || g.Profile == nil {
		return identityView{}, ErrOAuthStateInvalid
	}
	p, err := s.providerBySlug(ctx, slug)
	if err != nil {
		return identityView{}, err
	}
	now := s.now()
	claims := providerClaims{Subject: g.Subject, Email: g.Profile.Email, EmailVerified: g.Profile.EmailVerified,
		DisplayName: g.Profile.DisplayName, Raw: g.Profile.Raw}
	var link UserLink
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		// A soft-deleted link for the same identity would trip the unique index.
		if err := tx.Unscoped().Where("provider_id = ? AND provider_subject = ? AND deleted_at IS NOT NULL",
			p.ID, claims.Subject).Delete(&UserLink{}).Error; err != nil {
			return err
		}
		existing, err := s.linkFor(ctx, tx, p.ID, claims.Subject)
		if err != nil {
			return err
		}
		if existing != nil {
			if existing.UserID != userID {
				return ErrProviderIdentityConflict
			}
			if err := s.refreshLink(ctx, tx, existing, claims, now); err != nil {
				return err
			}
			return tx.First(&link, existing.ID).Error
		}
		if claims.Email != "" {
			var n int64
			if err := tx.Model(&User{}).Where("email = ? AND id <> ?", claims.Email, userID).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return ErrProviderEmailConflict.WithStatus(400)
			}
		}
		var mine int64
		if err := tx.Model(&UserLink{}).Where("user_id = ? AND provider_id = ?", userID, p.ID).Count(&mine).Error; err != nil {
			return err
		}
		if mine > 0 {
			return ErrProviderAlreadyBound
		}
		link = linkFromClaims(p, claims, now)
		link.UserID = userID
		if err := tx.Create(&link).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrProviderIdentityConflict
			}
			return err
		}
		return nil
	})
	if err != nil {
		s.event(ctx, eventInput{Type: "provider_bind", Result: resultFailure, Reason: reasonOf(err), UserID: userID,
			Detail: map[string]any{"provider": slug}})
		return identityView{}, err
	}
	s.event(ctx, eventInput{Type: "provider_bind", Result: resultSuccess, UserID: userID,
		Detail: map[string]any{"provider": slug, "subject": claims.Subject}})
	views, err := s.identityViews(ctx, []UserLink{link})
	if err != nil {
		return identityView{}, err
	}
	return views[0], nil
}

// UnbindIdentity removes one of the caller's identities unless it is their
// only way to sign in.
func (s *Service) UnbindIdentity(ctx context.Context, userID, identityID uint) error {
	return s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var user User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).First(&user, userID).Error; err != nil {
			return database.Translate(err)
		}
		var link UserLink
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND user_id = ?", identityID, userID).First(&link).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrIdentityNotFound
			}
			return err
		}
		var cred Credential
		passwordOn := false
		if err := tx.Where("user_id = ?", userID).First(&cred).Error; err == nil {
			passwordOn = cred.PasswordEnabled
		}
		if !passwordOn {
			var n int64
			if err := tx.Model(&UserLink{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
				return err
			}
			if n <= 1 {
				return ErrLastLoginMethod
			}
		}
		return tx.Unscoped().Delete(&UserLink{}, link.ID).Error
	})
}
