// Package auth implements authentication, sessions, account security and
// third-party identity providers.
//
// The package is mounted through Mount (or MountWith when the integrator wants
// to connect collaborators that live in other areas, see Hooks).
package auth

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Role names.
const (
	RoleUser       = "user"
	RoleAdmin      = "admin"
	RoleSuperAdmin = "superadmin"
)

// User statuses.
const (
	StatusPendingActivation = "pending_activation"
	StatusActive            = "active"
	StatusLocked            = "locked"
	StatusSuspended         = "suspended"
	StatusDeactivated       = "deactivated"
)

// Email sources.
const (
	EmailSourceLocalRegister      = "local_register"
	EmailSourceUserSet            = "user_set"
	EmailSourceProviderVerified   = "provider_verified"
	EmailSourceProviderUnverified = "provider_unverified"
	EmailSourceAdminSet           = "admin_set"
)

// Password origins.
const (
	PasswordOriginLocalRegister  = "local_register"
	PasswordOriginAdminCreated   = "admin_created"
	PasswordOriginAdminReset     = "admin_reset"
	PasswordOriginUserSet        = "user_set"
	PasswordOriginSSOPlaceholder = "sso_placeholder"
)

// User is a row of identity_users.
type User struct {
	database.Model
	PublicID              string     `gorm:"column:public_id;size:32;not null;uniqueIndex"`
	Username              string     `gorm:"column:username;size:64;not null;uniqueIndex:idx_identity_users_username"`
	DisplayName           string     `gorm:"column:display_name;size:128;not null;default:''"`
	AvatarURL             string     `gorm:"column:avatar_url;size:2048;not null;default:''"`
	Email                 string     `gorm:"column:email;size:128;not null;default:'';index"`
	Phone                 string     `gorm:"column:phone;size:32;not null;default:'';index"`
	Role                  string     `gorm:"column:role;size:32;not null;default:user;index"`
	Status                string     `gorm:"column:status;size:32;not null;default:active;index"`
	Timezone              string     `gorm:"column:timezone;size:64;default:Etc/UTC"`
	Locale                string     `gorm:"column:locale;size:16;default:en-US"`
	ProfilePreferences    string     `gorm:"column:profile_preferences;type:text;default:''"`
	AppearancePreferences string     `gorm:"column:appearance_preferences;type:text;default:''"`
	OnboardingCompletedAt *time.Time `gorm:"column:onboarding_completed_at;index"`
	EmailVerifiedAt       *time.Time `gorm:"column:email_verified_at"`
	EmailSource           string     `gorm:"column:email_source;size:32;default:''"`
	EmailBootstrapUsedAt  *time.Time `gorm:"column:email_bootstrap_used_at"`
	PhoneVerifiedAt       *time.Time `gorm:"column:phone_verified_at"`
	UsernameChangedAt     *time.Time `gorm:"column:username_changed_at"`
	LastLoginAt           *time.Time `gorm:"column:last_login_at"`
}

// TableName implements gorm's Tabler.
func (User) TableName() string { return "identity_users" }

// Credential is a row of identity_credentials (one per user).
type Credential struct {
	database.Model
	UserID            uint       `gorm:"column:user_id;not null;uniqueIndex"`
	PasswordHash      string     `gorm:"column:password_hash;size:255"`
	PasswordAlgo      string     `gorm:"column:password_algo;size:32;default:bcrypt"`
	PasswordEnabled   bool       `gorm:"column:password_enabled;not null"`
	PasswordUpdatedAt *time.Time `gorm:"column:password_updated_at"`
	PasswordSetAt     *time.Time `gorm:"column:password_set_at"`
	PasswordOrigin    string     `gorm:"column:password_origin;size:32;default:local_register"`
	MustResetPassword bool       `gorm:"column:must_reset_password;not null"`
	FailedLoginCount  int        `gorm:"column:failed_login_count;not null;default:0"`
	LockedUntil       *time.Time `gorm:"column:locked_until;index"`
}

// TableName implements gorm's Tabler.
func (Credential) TableName() string { return "identity_credentials" }

// Session is a row of identity_sessions.
type Session struct {
	database.Model
	SessionID                string     `gorm:"column:session_id;size:64;not null;uniqueIndex"`
	UserID                   uint       `gorm:"column:user_id;not null;index"`
	RefreshTokenHash         string     `gorm:"column:refresh_token_hash;size:255"`
	PreviousRefreshTokenHash string     `gorm:"column:previous_refresh_token_hash;size:255"`
	RefreshRotatedAt         *time.Time `gorm:"column:refresh_rotated_at;index"`
	AccessJTI                string     `gorm:"column:access_jti;size:64;index"`
	ClientIP                 string     `gorm:"column:client_ip;size:64"`
	UserAgent                string     `gorm:"column:user_agent;size:512"`
	DeviceName               string     `gorm:"column:device_name;size:128"`
	BrowserName              string     `gorm:"column:browser_name;size:64"`
	OSName                   string     `gorm:"column:os_name;size:64"`
	DeviceType               string     `gorm:"column:device_type;size:32"`
	GeoSource                string     `gorm:"column:geo_source;size:32"`
	GeoAccuracy              string     `gorm:"column:geo_accuracy;size:32"`
	CountryCode              string     `gorm:"column:country_code;size:32"`
	RegionName               string     `gorm:"column:region_name;size:64"`
	CityName                 string     `gorm:"column:city_name;size:64"`
	TimezoneName             string     `gorm:"column:timezone_name;size:64"`
	IPLatitude               *float64   `gorm:"column:ip_latitude"`
	IPLongitude              *float64   `gorm:"column:ip_longitude"`
	PreciseLatitude          *float64   `gorm:"column:precise_latitude"`
	PreciseLongitude         *float64   `gorm:"column:precise_longitude"`
	PreciseAccuracyM         *float64   `gorm:"column:precise_accuracy_m"`
	PreciseLocatedAt         *time.Time `gorm:"column:precise_located_at"`
	IssuedAt                 time.Time  `gorm:"column:issued_at;not null"`
	LastSeenAt               *time.Time `gorm:"column:last_seen_at"`
	ExpiresAt                time.Time  `gorm:"column:expires_at;not null;index"`
	RevokedAt                *time.Time `gorm:"column:revoked_at;index"`
	RevokeReason             string     `gorm:"column:revoke_reason;size:128"`
}

// TableName implements gorm's Tabler.
func (Session) TableName() string { return "identity_sessions" }

// AuthEvent is a row of identity_auth_events.
type AuthEvent struct {
	database.Model
	RequestID  string    `gorm:"column:request_id;size:64;index"`
	UserID     uint      `gorm:"column:user_id;index"`
	EventType  string    `gorm:"column:event_type;size:64;index"`
	Result     string    `gorm:"column:result;size:32;index"`
	Reason     string    `gorm:"column:reason;size:255"`
	ClientIP   string    `gorm:"column:client_ip;size:64"`
	UserAgent  string    `gorm:"column:user_agent;size:512"`
	DetailJSON string    `gorm:"column:detail_json;type:text"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null;index"`
}

// TableName implements gorm's Tabler.
func (AuthEvent) TableName() string { return "identity_auth_events" }

// ContactVerification is a row of identity_contact_verifications.
type ContactVerification struct {
	database.Model
	UserID       uint       `gorm:"column:user_id;index"`
	Channel      string     `gorm:"column:channel;size:16"`
	Purpose      string     `gorm:"column:purpose;size:32;index"`
	Target       string     `gorm:"column:target;size:128"`
	Token        string     `gorm:"column:token;size:96;uniqueIndex"`
	CodeHash     string     `gorm:"column:code_hash;size:255"`
	Status       string     `gorm:"column:status;size:16;default:pending"`
	SentAt       *time.Time `gorm:"column:sent_at"`
	ExpiresAt    *time.Time `gorm:"column:expires_at;index"`
	VerifiedAt   *time.Time `gorm:"column:verified_at"`
	ConsumedAt   *time.Time `gorm:"column:consumed_at"`
	AttemptCount int        `gorm:"column:attempt_count;not null;default:0"`
}

// TableName implements gorm's Tabler.
func (ContactVerification) TableName() string { return "identity_contact_verifications" }

// Verification purposes and statuses.
const (
	PurposeRegister           = "register"
	PurposePasswordChange     = "password_change"
	PurposePasswordReset      = "password_reset"
	PurposeLogin              = "login"
	PurposeAccountDelete      = "account_delete"
	PurposeEmailChangeCurrent = "email_change_current"
	PurposeEmailChangeNew     = "email_change_new"
	PurposeEmailBootstrapNew  = "email_bootstrap_new"
	PurposeEmailVerifyCurrent = "email_verify_current"

	verifPending  = "pending"
	verifVerified = "verified"
	verifCanceled = "canceled"
)

// Provider is a row of identity_providers.
type Provider struct {
	database.Model
	PublicID              string `gorm:"column:public_id;size:32;not null;uniqueIndex"`
	Type                  string `gorm:"column:type;size:16;not null;index"`
	Name                  string `gorm:"column:name;size:80;not null"`
	Slug                  string `gorm:"column:slug;size:64;not null;uniqueIndex"`
	LogoURL               string `gorm:"column:logo_url;size:512"`
	LoginEnabled          bool   `gorm:"column:login_enabled;not null;index"`
	RegistrationEnabled   bool   `gorm:"column:registration_enabled;not null;index"`
	ClientID              string `gorm:"column:client_id;size:255"`
	ClientSecretEncrypted string `gorm:"column:client_secret_encrypted;type:text"`
	IssuerURL             string `gorm:"column:issuer_url;size:512"`
	DiscoveryURL          string `gorm:"column:discovery_url;size:512"`
	AuthURL               string `gorm:"column:auth_url;size:512"`
	TokenURL              string `gorm:"column:token_url;size:512"`
	UserInfoURL           string `gorm:"column:user_info_url;size:512"`
	JWKSURL               string `gorm:"column:jwks_url;size:512"`
	Scopes                string `gorm:"column:scopes;size:255"`
	PKCEEnabled           bool   `gorm:"column:pkce_enabled;not null"`
	DefaultRole           string `gorm:"column:default_role;size:32;default:user"`
	SubjectField          string `gorm:"column:subject_field;size:64;default:sub"`
	EmailField            string `gorm:"column:email_field;size:64;default:email"`
	EmailVerifiedField    string `gorm:"column:email_verified_field;size:64;default:email_verified"`
	NameField             string `gorm:"column:name_field;size:64;default:name"`
	AvatarField           string `gorm:"column:avatar_field;size:64;default:picture"`
	SortOrder             int    `gorm:"column:sort_order;default:100;index"`
}

// TableName implements gorm's Tabler.
func (Provider) TableName() string { return "identity_providers" }

// UserLink is a row of identity_user_links.
type UserLink struct {
	database.Model
	UserID              uint       `gorm:"column:user_id;index"`
	ProviderID          uint       `gorm:"column:provider_id;uniqueIndex:idx_identity_user_links_provider_subject"`
	ProviderSubject     string     `gorm:"column:provider_subject;size:255;uniqueIndex:idx_identity_user_links_provider_subject"`
	ProviderType        string     `gorm:"column:provider_type;size:16"`
	ProviderDisplayName string     `gorm:"column:provider_display_name;size:128"`
	Email               string     `gorm:"column:email;size:128;index"`
	EmailVerified       bool       `gorm:"column:email_verified"`
	ProfileJSON         string     `gorm:"column:profile_json;type:text"`
	LinkedAt            time.Time  `gorm:"column:linked_at;not null"`
	LastLoginAt         *time.Time `gorm:"column:last_login_at"`
}

// TableName implements gorm's Tabler.
func (UserLink) TableName() string { return "identity_user_links" }

// MFASettings is a row of identity_mfa_settings (one per user).
type MFASettings struct {
	database.Model
	UserID                 uint       `gorm:"column:user_id;not null;uniqueIndex"`
	TOTPEnabled            bool       `gorm:"column:totp_enabled;not null"`
	TOTPSecretEncrypted    string     `gorm:"column:totp_secret_encrypted;type:text"`
	TOTPSetupExpiresAt     *time.Time `gorm:"column:totp_setup_expires_at;index"`
	RecoveryCodesHash      string     `gorm:"column:recovery_codes_hash;type:text"`
	Enforced               bool       `gorm:"column:enforced;not null"`
	EnabledAt              *time.Time `gorm:"column:enabled_at"`
	LastVerifiedAt         *time.Time `gorm:"column:last_verified_at"`
	TrustedDeviceExpiresAt *time.Time `gorm:"column:trusted_device_expires_at"`
}

// TableName implements gorm's Tabler.
func (MFASettings) TableName() string { return "identity_mfa_settings" }

// TrustedDevice is a row of identity_trusted_devices. The area never fills it;
// it exists for schema compatibility and is cleared on account deletion.
type TrustedDevice struct {
	database.Model
	UserID    uint      `gorm:"column:user_id;index"`
	DeviceID  string    `gorm:"column:device_id;size:64;uniqueIndex"`
	TokenHash string    `gorm:"column:token_hash;size:255"`
	UserAgent string    `gorm:"column:user_agent;size:512"`
	ClientIP  string    `gorm:"column:client_ip;size:64"`
	ExpiresAt time.Time `gorm:"column:expires_at"`
}

// TableName implements gorm's Tabler.
func (TrustedDevice) TableName() string { return "identity_trusted_devices" }
