package auth

import (
	"context"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const localGrantTTL = 2 * time.Minute

// BootstrapAdmin makes sure a super administrator exists (server mode). With
// none present it creates one with a random password that is logged once and
// must be replaced at first login.
func (s *Service) BootstrapAdmin(ctx context.Context) error {
	cfg := s.cfg()
	username := strings.TrimSpace(cfg.Admin.Username)
	if username == "" {
		username = "admin"
	}
	var count int64
	if err := s.conn(ctx).Model(&User{}).Where("role = ?", RoleSuperAdmin).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		// An untouched bootstrap account keeps being forced through onboarding.
		var u User
		err := s.conn(ctx).Where("role = ? AND LOWER(username) = ? AND username_changed_at IS NULL",
			RoleSuperAdmin, strings.ToLower(username)).First(&u).Error
		if err != nil {
			if database.IsNotFound(err) {
				return nil
			}
			return err
		}
		return s.conn(ctx).Model(&Credential{}).
			Where("user_id = ? AND password_origin = ? AND must_reset_password = ?", u.ID, PasswordOriginAdminCreated, false).
			Update("must_reset_password", true).Error
	}

	password := randomURLToken(24)
	hash, err := hashPassword(password)
	if err != nil {
		return err
	}
	now := s.now()
	display := strings.TrimSpace(cfg.Admin.DisplayName)
	if display == "" {
		display = username
	}
	u := User{
		DisplayName: truncateRunes(display, 128), Role: RoleSuperAdmin, Status: StatusActive,
		Timezone: "Etc/UTC", Locale: "en-US",
	}
	cred := Credential{
		PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: true, PasswordUpdatedAt: &now,
		PasswordSetAt: &now, PasswordOrigin: PasswordOriginAdminCreated, MustResetPassword: true,
	}
	created, err := s.createAccount(ctx, u, cred, nil, func(n int) string {
		if n == 0 {
			return username
		}
		return suffixedUsername(username, n+1)
	})
	if err != nil {
		return err
	}
	s.log.Warn("bootstrap_admin_created", logx.F("username", created.Username), logx.F("password", password))
	return nil
}

// EnsureLocalOwner creates the single desktop user when it does not exist.
func (s *Service) EnsureLocalOwner(ctx context.Context) (*User, error) {
	username := strings.TrimSpace(s.cfg().Admin.Username)
	if username == "" {
		username = "owner"
	}
	var u User
	err := s.conn(ctx).Where("role = ? AND username = ?", RoleSuperAdmin, username).First(&u).Error
	if err == nil {
		return &u, nil
	}
	if !database.IsNotFound(err) {
		return nil, err
	}
	if err := s.conn(ctx).Where("role = ?", RoleSuperAdmin).Order("id ASC").First(&u).Error; err == nil {
		return &u, nil
	} else if !database.IsNotFound(err) {
		return nil, err
	}
	hash, err := hashPassword(randomURLToken(24))
	if err != nil {
		return nil, err
	}
	now := s.now()
	row := User{
		DisplayName: "Me", Role: RoleSuperAdmin, Status: StatusActive, Timezone: "Etc/UTC", Locale: "en-US",
		OnboardingCompletedAt: &now, UsernameChangedAt: &now,
	}
	cred := Credential{
		PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: false,
		PasswordOrigin: PasswordOriginSSOPlaceholder,
	}
	return s.createAccount(ctx, row, cred, nil, func(n int) string {
		if n == 0 {
			return username
		}
		return suffixedUsername(username, n+1)
	})
}

// IssueLocalGrant creates the one-time desktop login grant, replacing any
// unused one.
func (s *Service) IssueLocalGrant(ctx context.Context) (string, error) {
	if _, err := s.EnsureLocalOwner(ctx); err != nil {
		return "", err
	}
	g := randomURLToken(32)
	s.grantMu.Lock()
	s.grantVal, s.grantExp = g, s.now().Add(localGrantTTL)
	s.grantMu.Unlock()
	return g, nil
}

// takeLocalGrant consumes the grant when presented is the live one.
func (s *Service) takeLocalGrant(presented string) bool {
	presented = strings.TrimSpace(presented)
	s.grantMu.Lock()
	defer s.grantMu.Unlock()
	if s.grantVal == "" || !s.now().Before(s.grantExp) || !constEq(s.grantVal, presented) {
		return false
	}
	s.grantVal = ""
	return true
}

// LocalExchange redeems the desktop grant for an ordinary session.
func (s *Service) LocalExchange(ctx context.Context, grant string) (signIn, error) {
	if !s.takeLocalGrant(grant) {
		s.event(ctx, eventInput{Type: "local_exchange", Result: resultFailure, Reason: "invalid grant"})
		return signIn{}, ErrInvalidLocalGrant
	}
	owner, err := s.EnsureLocalOwner(ctx)
	if err != nil {
		return signIn{}, err
	}
	out, sess, err := s.newSession(ctx, owner)
	if err != nil {
		return signIn{}, err
	}
	s.event(ctx, eventInput{Type: "local_exchange", Result: resultSuccess, UserID: owner.ID, Detail: sessionDetail(sess)})
	return out, nil
}

// loginOptions is the public description of the sign-in methods on offer.
type loginOptions struct {
	UsernameEnabled              bool           `json:"usernameEnabled"`
	EmailEnabled                 bool           `json:"emailEnabled"`
	EmailRegistrationEnabled     bool           `json:"emailRegistrationEnabled"`
	EmailVerificationEnabled     bool           `json:"emailVerificationEnabled"`
	PasswordResetEnabled         bool           `json:"passwordResetEnabled"`
	TurnstileRegistrationEnabled bool           `json:"turnstileRegistrationEnabled"`
	TurnstileSiteKey             string         `json:"turnstileSiteKey"`
	ProviderAuthBridge           bridgeInfo     `json:"providerAuthBridge"`
	Providers                    []providerView `json:"providers"`
}

type bridgeInfo struct {
	Enabled         bool   `json:"enabled"`
	ProtocolVersion int    `json:"protocolVersion"`
	CallbackBaseURL string `json:"callbackBaseURL"`
}

// LoginOptions builds the public login-options payload.
func (s *Service) LoginOptions(ctx context.Context) (loginOptions, error) {
	st := s.settings(ctx)
	opts := loginOptions{
		UsernameEnabled: st.UsernameLoginEnabled, EmailEnabled: st.EmailLoginEnabled,
		EmailRegistrationEnabled:     st.EmailRegistrationEnabled,
		EmailVerificationEnabled:     st.EmailVerificationEnabled,
		PasswordResetEnabled:         passwordResetAvailable(st),
		TurnstileRegistrationEnabled: st.TurnstileRegistrationEnabled,
		TurnstileSiteKey:             st.TurnstileSiteKey,
		ProviderAuthBridge:           bridgeInfo{ProtocolVersion: 1},
		Providers:                    []providerView{},
	}
	if cb, ok := s.bridgeEnabled(); ok {
		opts.ProviderAuthBridge.Enabled, opts.ProviderAuthBridge.CallbackBaseURL = true, cb
	}
	if st.ThirdPartyLoginEnabled {
		var rows []Provider
		err := s.conn(ctx).Where("login_enabled = ? OR registration_enabled = ?", true, true).
			Order("sort_order ASC, id ASC").Find(&rows).Error
		if err != nil {
			return opts, err
		}
		for i := range rows {
			opts.Providers = append(opts.Providers, toProviderView(&rows[i], false))
		}
	}
	return opts, nil
}
