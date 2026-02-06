package user

import (
	"strings"
	"time"
)

// IdentityProviderSummary is the provider part of a linked identity in a view.
type IdentityProviderSummary struct {
	ID      any    `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Slug    string `json:"slug"`
	LogoURL string `json:"logoURL"`
}

// SubscriptionInfo is the optional subscription input of a view.
type SubscriptionInfo struct {
	Tier      string
	PlanID    *uint
	PlanName  string
	Status    string
	ExpiresAt *time.Time
}

// BillingAccountInfo is the optional billing account input of a view.
type BillingAccountInfo struct {
	Currency string
	Balance  int64
	Status   string
}

// ViewInput gathers everything BuildView folds into a user view.
type ViewInput struct {
	Account    Account
	Credential *Credential
	// Subscription overrides the free defaults when present.
	Subscription *SubscriptionInfo
	Billing      *BillingAccountInfo
	Providers    []IdentityProviderSummary
	// LastActiveAt is the latest session activity; nil keeps last login.
	LastActiveAt *time.Time

	InitialUsernameRequired bool
	InitialSecurityRequired bool
	TwoFactorAvailable      bool
	TwoFactorEnabled        bool
	TwoFactorRequired       bool
	TwoFactorRecoveryCount  int
}

// UserView is the JSON shape of a user in auth and admin responses. Nullable
// timestamps are emitted as null, never omitted.
type UserView struct {
	ID                      uint                      `json:"id"`
	PublicID                string                    `json:"publicID"`
	Username                string                    `json:"username"`
	DisplayName             string                    `json:"displayName"`
	AvatarURL               string                    `json:"avatarURL"`
	Email                   string                    `json:"email"`
	Phone                   string                    `json:"phone"`
	Role                    string                    `json:"role"`
	Status                  string                    `json:"status"`
	Timezone                string                    `json:"timezone"`
	Locale                  string                    `json:"locale"`
	ProfilePreferences      string                    `json:"profilePreferences"`
	AppearancePreferences   string                    `json:"appearancePreferences"`
	OnboardingCompletedAt   *time.Time                `json:"onboardingCompletedAt"`
	EmailVerifiedAt         *time.Time                `json:"emailVerifiedAt"`
	EmailSource             string                    `json:"emailSource"`
	EmailBootstrapUsedAt    *time.Time                `json:"emailBootstrapUsedAt"`
	PhoneVerifiedAt         *time.Time                `json:"phoneVerifiedAt"`
	UsernameChangedAt       *time.Time                `json:"usernameChangedAt"`
	PasswordEnabled         bool                      `json:"passwordEnabled"`
	PasswordSetAt           *time.Time                `json:"passwordSetAt"`
	PasswordOrigin          string                    `json:"passwordOrigin"`
	MustResetPassword       bool                      `json:"mustResetPassword"`
	InitialUsernameRequired bool                      `json:"initialUsernameRequired"`
	InitialSecurityRequired bool                      `json:"initialSecurityRequired"`
	TwoFactorAvailable      bool                      `json:"twoFactorAvailable"`
	TwoFactorEnabled        bool                      `json:"twoFactorEnabled"`
	TwoFactorRequired       bool                      `json:"twoFactorRequired"`
	TwoFactorRecoveryCount  int                       `json:"twoFactorRecoveryCount"`
	LastLoginAt             *time.Time                `json:"lastLoginAt"`
	LastActiveAt            *time.Time                `json:"lastActiveAt"`
	CreatedAt               time.Time                 `json:"createdAt"`
	UpdatedAt               time.Time                 `json:"updatedAt"`
	SubscriptionTier        string                    `json:"subscriptionTier"`
	SubscriptionPlanID      *uint                     `json:"subscriptionPlanID"`
	SubscriptionPlanName    string                    `json:"subscriptionPlanName"`
	SubscriptionStatus      string                    `json:"subscriptionStatus"`
	SubscriptionExpiresAt   *time.Time                `json:"subscriptionExpiresAt"`
	BillingCurrency         string                    `json:"-"`
	BillingBalanceNanousd   int64                     `json:"-"`
	BillingStatus           string                    `json:"-"`
	IdentityProviders       []IdentityProviderSummary `json:"identityProviders"`
}

// BuildView derives the user view. Without subscription info the tier, plan
// name and status are "free"; with it, non-blank values replace those defaults
// while the plan id and expiry are copied as given. Billing defaults are USD,
// balance 0, status "active".
func BuildView(in ViewInput) UserView {
	u := in.Account
	v := UserView{
		ID: u.ID, PublicID: u.PublicID, Username: u.Username, DisplayName: u.DisplayName,
		AvatarURL: u.AvatarURL, Email: u.Email, Phone: u.Phone, Role: u.Role, Status: u.Status,
		Timezone: u.Timezone, Locale: u.Locale, ProfilePreferences: u.ProfilePreferences,
		AppearancePreferences: u.AppearancePreferences, OnboardingCompletedAt: u.OnboardingCompletedAt,
		EmailVerifiedAt: u.EmailVerifiedAt, EmailSource: u.EmailSource,
		EmailBootstrapUsedAt: u.EmailBootstrapUsedAt, PhoneVerifiedAt: u.PhoneVerifiedAt,
		UsernameChangedAt:       u.UsernameChangedAt,
		InitialUsernameRequired: in.InitialUsernameRequired, InitialSecurityRequired: in.InitialSecurityRequired,
		TwoFactorAvailable: in.TwoFactorAvailable, TwoFactorEnabled: in.TwoFactorEnabled,
		TwoFactorRequired: in.TwoFactorRequired, TwoFactorRecoveryCount: in.TwoFactorRecoveryCount,
		LastLoginAt: u.LastLoginAt, LastActiveAt: u.LastLoginAt,
		CreatedAt: u.CreatedAt, UpdatedAt: u.UpdatedAt,
		SubscriptionTier: "free", SubscriptionPlanName: "free", SubscriptionStatus: "free",
		BillingCurrency: "USD", BillingStatus: "active",
		IdentityProviders: []IdentityProviderSummary{},
	}
	if c := in.Credential; c != nil {
		v.PasswordEnabled = c.PasswordEnabled
		v.PasswordSetAt = c.PasswordSetAt
		v.PasswordOrigin = c.PasswordOrigin
		v.MustResetPassword = c.MustResetPassword
	}
	if in.LastActiveAt != nil {
		v.LastActiveAt = in.LastActiveAt
	}
	if s := in.Subscription; s != nil {
		if t := strings.TrimSpace(s.Tier); t != "" {
			v.SubscriptionTier = t
		}
		if n := strings.TrimSpace(s.PlanName); n != "" {
			v.SubscriptionPlanName = n
		}
		if st := strings.TrimSpace(s.Status); st != "" {
			v.SubscriptionStatus = st
		}
		v.SubscriptionPlanID = s.PlanID
		v.SubscriptionExpiresAt = s.ExpiresAt
	}
	if b := in.Billing; b != nil {
		if c := strings.TrimSpace(b.Currency); c != "" {
			v.BillingCurrency = c
		}
		v.BillingBalanceNanousd = b.Balance
		if st := strings.TrimSpace(b.Status); st != "" {
			v.BillingStatus = st
		}
	}
	if len(in.Providers) > 0 {
		v.IdentityProviders = append(v.IdentityProviders, in.Providers...)
	}
	return v
}
