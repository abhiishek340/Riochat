package user

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

// Account statuses.
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

// Setting is one stored preference (table user_settings). The row carries no
// created-at column and is never soft deleted.
type Setting struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    uint      `gorm:"column:user_id;not null;index:idx_user_settings_user_id;uniqueIndex:uidx_user_settings_user_key,priority:1"`
	Key       string    `gorm:"column:key;size:64;not null;uniqueIndex:uidx_user_settings_user_key,priority:2"`
	Value     string    `gorm:"column:value;type:text;not null;default:''"`
	UpdatedAt time.Time `gorm:"column:updated_at"`
}

// TableName implements database.Tabler.
func (Setting) TableName() string { return "user_settings" }

// ---- rows of tables owned by other areas ----------------------------------
//
// The identity and billing areas own and migrate the tables below. The structs
// map just the columns this package reads or writes for the administration
// capabilities; they are never passed to a migration here.

// Account is a row of identity_users.
type Account struct {
	database.Model
	PublicID              string     `gorm:"column:public_id;size:32;not null"`
	Username              string     `gorm:"column:username;size:64;not null"`
	DisplayName           string     `gorm:"column:display_name;size:128;not null"`
	AvatarURL             string     `gorm:"column:avatar_url;size:2048;not null"`
	Email                 string     `gorm:"column:email;size:128;not null"`
	Phone                 string     `gorm:"column:phone;size:32;not null"`
	Role                  string     `gorm:"column:role;size:32;not null"`
	Status                string     `gorm:"column:status;size:32;not null"`
	Timezone              string     `gorm:"column:timezone;size:64"`
	Locale                string     `gorm:"column:locale;size:16"`
	ProfilePreferences    string     `gorm:"column:profile_preferences;type:text"`
	AppearancePreferences string     `gorm:"column:appearance_preferences;type:text"`
	OnboardingCompletedAt *time.Time `gorm:"column:onboarding_completed_at"`
	EmailVerifiedAt       *time.Time `gorm:"column:email_verified_at"`
	EmailSource           string     `gorm:"column:email_source;size:32"`
	EmailBootstrapUsedAt  *time.Time `gorm:"column:email_bootstrap_used_at"`
	PhoneVerifiedAt       *time.Time `gorm:"column:phone_verified_at"`
	UsernameChangedAt     *time.Time `gorm:"column:username_changed_at"`
	LastLoginAt           *time.Time `gorm:"column:last_login_at"`
}

// TableName implements database.Tabler.
func (Account) TableName() string { return "identity_users" }

// Credential is a row of identity_credentials.
type Credential struct {
	database.Model
	UserID            uint       `gorm:"column:user_id;not null"`
	PasswordHash      string     `gorm:"column:password_hash;size:255"`
	PasswordAlgo      string     `gorm:"column:password_algo;size:32"`
	PasswordEnabled   bool       `gorm:"column:password_enabled;not null"`
	PasswordUpdatedAt *time.Time `gorm:"column:password_updated_at"`
	PasswordSetAt     *time.Time `gorm:"column:password_set_at"`
	PasswordOrigin    string     `gorm:"column:password_origin;size:32"`
	MustResetPassword bool       `gorm:"column:must_reset_password;not null"`
	FailedLoginCount  int        `gorm:"column:failed_login_count;not null"`
	LockedUntil       *time.Time `gorm:"column:locked_until"`
}

// TableName implements database.Tabler.
func (Credential) TableName() string { return "identity_credentials" }

// AuthEvent is a row of identity_auth_events.
type AuthEvent struct {
	database.Model
	RequestID  string    `gorm:"column:request_id;size:64"`
	UserID     uint      `gorm:"column:user_id"`
	EventType  string    `gorm:"column:event_type;size:64"`
	Result     string    `gorm:"column:result;size:32"`
	Reason     string    `gorm:"column:reason;size:255"`
	ClientIP   string    `gorm:"column:client_ip;size:64"`
	UserAgent  string    `gorm:"column:user_agent;size:512"`
	DetailJSON string    `gorm:"column:detail_json;type:text"`
	OccurredAt time.Time `gorm:"column:occurred_at;not null"`
}

// TableName implements database.Tabler.
func (AuthEvent) TableName() string { return "identity_auth_events" }

// sessionRow maps the columns of identity_sessions this package touches.
type sessionRow struct {
	database.Model
	SessionID    string     `gorm:"column:session_id;size:64;not null"`
	UserID       uint       `gorm:"column:user_id;not null"`
	IssuedAt     time.Time  `gorm:"column:issued_at;not null"`
	LastSeenAt   *time.Time `gorm:"column:last_seen_at"`
	ExpiresAt    time.Time  `gorm:"column:expires_at;not null"`
	RevokedAt    *time.Time `gorm:"column:revoked_at"`
	RevokeReason string     `gorm:"column:revoke_reason;size:128"`
}

func (sessionRow) TableName() string { return "identity_sessions" }

// providerRow maps identity_providers.
type providerRow struct {
	database.Model
	PublicID  string `gorm:"column:public_id;size:32;not null"`
	Type      string `gorm:"column:type;size:16"`
	Name      string `gorm:"column:name;size:80"`
	Slug      string `gorm:"column:slug;size:64"`
	LogoURL   string `gorm:"column:logo_url;size:512"`
	SortOrder int    `gorm:"column:sort_order"`
}

func (providerRow) TableName() string { return "identity_providers" }

// linkRow maps identity_user_links.
type linkRow struct {
	database.Model
	UserID          uint      `gorm:"column:user_id"`
	ProviderID      uint      `gorm:"column:provider_id"`
	ProviderSubject string    `gorm:"column:provider_subject;size:255"`
	LinkedAt        time.Time `gorm:"column:linked_at;not null"`
}

func (linkRow) TableName() string { return "identity_user_links" }

// planRow maps billing_plans.
type planRow struct {
	database.Model
	Code     string `gorm:"column:code;size:32;not null"`
	Name     string `gorm:"column:name;size:64"`
	IsActive bool   `gorm:"column:is_active"`
}

func (planRow) TableName() string { return "billing_plans" }

// priceRow maps billing_prices.
type priceRow struct {
	database.Model
	PlanID          uint   `gorm:"column:plan_id"`
	BillingInterval string `gorm:"column:billing_interval;size:16"`
	IsActive        bool   `gorm:"column:is_active"`
	IsDefault       bool   `gorm:"column:is_default"`
}

func (priceRow) TableName() string { return "billing_prices" }

// subscriptionRow maps billing_subscriptions.
type subscriptionRow struct {
	database.Model
	UserID               uint       `gorm:"column:user_id"`
	PlanID               uint       `gorm:"column:plan_id"`
	PriceID              uint       `gorm:"column:price_id"`
	Status               string     `gorm:"column:status;size:32"`
	StartAt              time.Time  `gorm:"column:start_at;not null"`
	CurrentPeriodStartAt time.Time  `gorm:"column:current_period_start_at;not null"`
	CurrentPeriodEndAt   *time.Time `gorm:"column:current_period_end_at"`
	CancelAtPeriodEnd    bool       `gorm:"column:cancel_at_period_end"`
	CanceledAt           *time.Time `gorm:"column:canceled_at"`
	AutoRenew            bool       `gorm:"column:auto_renew"`
}

func (subscriptionRow) TableName() string { return "billing_subscriptions" }

// billingAccountRow maps billing_accounts.
type billingAccountRow struct {
	database.Model
	UserID         uint   `gorm:"column:user_id"`
	Currency       string `gorm:"column:currency;size:16"`
	BalanceNanousd int64  `gorm:"column:balance_nanousd"`
	Status         string `gorm:"column:status;size:32"`
}

func (billingAccountRow) TableName() string { return "billing_accounts" }

// balanceTxRow maps billing_balance_transactions.
type balanceTxRow struct {
	database.Model
	AccountID           uint   `gorm:"column:account_id"`
	UserID              uint   `gorm:"column:user_id"`
	Type                string `gorm:"column:type;size:32"`
	AmountNanousd       int64  `gorm:"column:amount_nanousd"`
	BalanceAfterNanousd int64  `gorm:"column:balance_after_nanousd"`
	RefType             string `gorm:"column:ref_type;size:64"`
	RefID               uint   `gorm:"column:ref_id"`
	RefNo               string `gorm:"column:ref_no;size:128"`
	Description         string `gorm:"column:description;size:255"`
}

func (balanceTxRow) TableName() string { return "billing_balance_transactions" }

// mfaRow maps the columns of identity_mfa_settings the administration views
// read: whether TOTP is on and the stored recovery-code list.
type mfaRow struct {
	database.Model
	UserID            uint   `gorm:"column:user_id;not null"`
	TOTPEnabled       bool   `gorm:"column:totp_enabled;not null"`
	RecoveryCodesHash string `gorm:"column:recovery_codes_hash;type:text"`
}

func (mfaRow) TableName() string { return "identity_mfa_settings" }
