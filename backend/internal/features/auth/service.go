package auth

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// bcryptCost is the work factor of stored password hashes. Tests lower it.
var bcryptCost = 12

// Service is the area's business logic. All methods are safe for concurrent
// use.
type Service struct {
	d   deps.Deps
	db  *gorm.DB
	h   Hooks
	log *logx.Logger

	grantMu  sync.Mutex
	grantVal string
	grantExp time.Time
}

func newService(d deps.Deps, h Hooks) *Service {
	return &Service{d: d, db: d.DB, h: h, log: d.Logger}
}

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC() }

func (s *Service) cfg() *config.Config { return s.d.Config.Load() }

func (s *Service) settings(ctx context.Context) Settings {
	if s.h.Settings != nil {
		return s.h.Settings.AuthSettings(ctx)
	}
	return DefaultSettings()
}

func (s *Service) conn(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx) }

// tokenTTLs returns the access and refresh lifetimes with the documented
// fallbacks for non-positive values.
func (s *Service) tokenTTLs() (access, refresh time.Duration) {
	a := s.cfg().Dynamic.Auth
	access, refresh = a.AccessTokenTTL, a.RefreshTokenTTL
	if access <= 0 {
		access = 24 * time.Hour
	}
	if refresh <= 0 {
		refresh = 30 * 24 * time.Hour
	}
	return access, refresh
}

// ---- auth events and audit --------------------------------------------------

// Event results.
const (
	resultSuccess   = "success"
	resultFailure   = "failure"
	resultChallenge = "challenge"
)

type eventInput struct {
	Type   string
	Result string
	Reason string
	UserID uint
	Detail map[string]any
}

// event records an auth event. It never fails the caller.
func (s *Service) event(ctx context.Context, in eventInput) {
	ci := clientFrom(ctx)
	detail := ""
	if len(in.Detail) > 0 {
		if b, err := json.Marshal(in.Detail); err == nil {
			detail = string(b)
		}
	}
	row := AuthEvent{
		RequestID:  clip(ci.RequestID, 64),
		UserID:     in.UserID,
		EventType:  clip(in.Type, 64),
		Result:     in.Result,
		Reason:     clip(in.Reason, 255),
		ClientIP:   clip(ci.ClientIP, 64),
		UserAgent:  clip(ci.UserAgent, 512),
		DetailJSON: detail,
		OccurredAt: s.now(),
	}
	if err := s.db.WithContext(context.WithoutCancel(ctx)).Create(&row).Error; err != nil {
		s.log.Warn("auth_event_write_failed", logx.F("event", in.Type), logx.Err(err))
	}
}

func (s *Service) audit(ctx context.Context, userID uint, action, resourceType, resourceID string, detail map[string]any) {
	if s.h.Audit == nil {
		return
	}
	ci := clientFrom(ctx)
	s.h.Audit.WriteAudit(ctx, AuditEntry{
		UserID: userID, Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Detail: detail, ClientIP: ci.ClientIP, UserAgent: ci.UserAgent, RequestID: ci.RequestID,
	})
}

// sessionDetail is the flat event detail describing a session and its device.
func sessionDetail(sess *Session) map[string]any {
	m := map[string]any{
		"session_id":     sess.SessionID,
		"device_name":    sess.DeviceName,
		"browser_name":   sess.BrowserName,
		"os_name":        sess.OSName,
		"device_type":    sess.DeviceType,
		"geo_source":     sess.GeoSource,
		"geo_accuracy":   sess.GeoAccuracy,
		"client_ip":      sess.ClientIP,
		"country_code":   sess.CountryCode,
		"region_name":    sess.RegionName,
		"city_name":      sess.CityName,
		"timezone_name":  sess.TimezoneName,
		"location_label": locationLabel(sess.CityName, sess.RegionName, sess.CountryCode),
	}
	if sess.IPLatitude != nil {
		m["ip_latitude"] = *sess.IPLatitude
	}
	if sess.IPLongitude != nil {
		m["ip_longitude"] = *sess.IPLongitude
	}
	return m
}

// ---- lookups ----------------------------------------------------------------

func (s *Service) userByID(ctx context.Context, id uint) (*User, error) {
	var u User
	if err := s.conn(ctx).First(&u, id).Error; err != nil {
		return nil, database.Translate(err)
	}
	return &u, nil
}

func (s *Service) credentialOf(ctx context.Context, userID uint) (*Credential, error) {
	var c Credential
	err := s.conn(ctx).Where("user_id = ?", userID).First(&c).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &c, nil
}

func (s *Service) mfaOf(ctx context.Context, userID uint) (*MFASettings, error) {
	var m MFASettings
	err := s.conn(ctx).Where("user_id = ?", userID).First(&m).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &m, nil
}

func totpActive(m *MFASettings) bool {
	return m != nil && m.TOTPEnabled && strings.TrimSpace(m.TOTPSecretEncrypted) != ""
}

// ---- user view --------------------------------------------------------------

type userView struct {
	ID                    uint       `json:"id"`
	PublicID              string     `json:"publicID"`
	Username              string     `json:"username"`
	DisplayName           string     `json:"displayName"`
	AvatarURL             string     `json:"avatarURL"`
	Email                 string     `json:"email"`
	Phone                 string     `json:"phone"`
	Role                  string     `json:"role"`
	Status                string     `json:"status"`
	Timezone              string     `json:"timezone"`
	Locale                string     `json:"locale"`
	ProfilePreferences    string     `json:"profilePreferences"`
	AppearancePreferences string     `json:"appearancePreferences"`
	OnboardingCompletedAt *time.Time `json:"onboardingCompletedAt"`
	EmailVerifiedAt       *time.Time `json:"emailVerifiedAt"`
	EmailSource           string     `json:"emailSource"`
	EmailBootstrapUsedAt  *time.Time `json:"emailBootstrapUsedAt"`
	PhoneVerifiedAt       *time.Time `json:"phoneVerifiedAt"`
	UsernameChangedAt     *time.Time `json:"usernameChangedAt"`
	PasswordEnabled       bool       `json:"passwordEnabled"`
	PasswordSetAt         *time.Time `json:"passwordSetAt"`
	PasswordOrigin        string     `json:"passwordOrigin"`
	MustResetPassword     bool       `json:"mustResetPassword"`
	InitialUsernameReq    bool       `json:"initialUsernameRequired"`
	InitialSecurityReq    bool       `json:"initialSecurityRequired"`
	TwoFactorAvailable    bool       `json:"twoFactorAvailable"`
	TwoFactorEnabled      bool       `json:"twoFactorEnabled"`
	TwoFactorRequired     bool       `json:"twoFactorRequired"`
	TwoFactorRecoveryCnt  int        `json:"twoFactorRecoveryCount"`
	LastLoginAt           *time.Time `json:"lastLoginAt"`
	LastActiveAt          *time.Time `json:"lastActiveAt"`
	CreatedAt             time.Time  `json:"createdAt"`
	UpdatedAt             time.Time  `json:"updatedAt"`
	SubscriptionTier      string     `json:"subscriptionTier"`
	SubscriptionPlanID    *uint      `json:"subscriptionPlanID"`
	SubscriptionPlanName  string     `json:"subscriptionPlanName"`
	SubscriptionStatus    string     `json:"subscriptionStatus"`
	SubscriptionExpiresAt *time.Time `json:"subscriptionExpiresAt"`
	IdentityProviders     []any      `json:"identityProviders"`
}

// bootstrapPasswordActive reports whether the account still uses the generated
// administrator password.
func bootstrapPasswordActive(u *User, c *Credential) bool {
	return u.Role == RoleSuperAdmin && c != nil && c.PasswordEnabled && c.PasswordOrigin == PasswordOriginAdminCreated
}

func mustReset(u *User, c *Credential) bool {
	return c != nil && (c.MustResetPassword || bootstrapPasswordActive(u, c))
}

func (s *Service) initialUsernameRequired(u *User) bool {
	return u.Role == RoleSuperAdmin && u.UsernameChangedAt == nil &&
		strings.EqualFold(u.Username, s.cfg().Admin.Username)
}

func recoveryCount(m *MFASettings) int {
	if m == nil {
		return 0
	}
	return len(decodeRecoveryHashes(m.RecoveryCodesHash))
}

func (s *Service) viewUser(ctx context.Context, u *User) (userView, error) {
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return userView{}, err
	}
	mfa, err := s.mfaOf(ctx, u.ID)
	if err != nil {
		return userView{}, err
	}
	reset := mustReset(u, cred)
	v := userView{
		ID: u.ID, PublicID: u.PublicID, Username: u.Username, DisplayName: u.DisplayName,
		AvatarURL: u.AvatarURL, Email: u.Email, Phone: u.Phone, Role: u.Role, Status: u.Status,
		Timezone: u.Timezone, Locale: u.Locale, ProfilePreferences: u.ProfilePreferences,
		AppearancePreferences: u.AppearancePreferences, OnboardingCompletedAt: u.OnboardingCompletedAt,
		EmailVerifiedAt: u.EmailVerifiedAt, EmailSource: u.EmailSource,
		EmailBootstrapUsedAt: u.EmailBootstrapUsedAt, PhoneVerifiedAt: u.PhoneVerifiedAt,
		UsernameChangedAt:  u.UsernameChangedAt,
		MustResetPassword:  reset,
		InitialUsernameReq: s.initialUsernameRequired(u),
		InitialSecurityReq: reset || u.OnboardingCompletedAt == nil,
		TwoFactorAvailable: true, TwoFactorRequired: false,
		TwoFactorEnabled:     totpActive(mfa),
		TwoFactorRecoveryCnt: recoveryCount(mfa),
		LastLoginAt:          u.LastLoginAt, LastActiveAt: u.LastLoginAt,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		SubscriptionTier: "free", SubscriptionPlanName: "free", SubscriptionStatus: "free",
		IdentityProviders: []any{},
	}
	if cred != nil {
		v.PasswordEnabled = cred.PasswordEnabled
		v.PasswordSetAt = cred.PasswordSetAt
		v.PasswordOrigin = cred.PasswordOrigin
	}
	if s.h.Subscriptions != nil {
		if sub, ok := s.h.Subscriptions.SubscriptionFor(ctx, u.ID); ok {
			if sub.Tier != "" {
				v.SubscriptionTier = sub.Tier
			}
			v.SubscriptionPlanID = sub.PlanID
			if sub.PlanName != "" {
				v.SubscriptionPlanName = sub.PlanName
			}
			if sub.Status != "" {
				v.SubscriptionStatus = sub.Status
			}
			v.SubscriptionExpiresAt = sub.ExpiresAt
		}
	}
	return v, nil
}
