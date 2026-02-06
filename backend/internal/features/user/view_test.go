package user

import (
	"encoding/json"
	"testing"
	"time"
)

func TestViewDefaultsWithoutSubscriptionOrCredential(t *testing.T) {
	login := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := BuildView(ViewInput{Account: Account{PublicID: "pid", Username: "alice", LastLoginAt: &login}})
	if v.SubscriptionTier != "free" || v.SubscriptionPlanName != "free" || v.SubscriptionStatus != "free" ||
		v.SubscriptionPlanID != nil || v.SubscriptionExpiresAt != nil {
		t.Fatalf("subscription defaults: %+v", v)
	}
	if v.BillingCurrency != "USD" || v.BillingBalanceNanousd != 0 || v.BillingStatus != "active" {
		t.Fatalf("billing defaults: %+v", v)
	}
	if v.IdentityProviders == nil || len(v.IdentityProviders) != 0 {
		t.Fatal("identity providers must be an empty array")
	}
	if v.LastActiveAt == nil || !v.LastActiveAt.Equal(login) {
		t.Fatalf("last active starts as last login: %v", v.LastActiveAt)
	}
	if v.PasswordEnabled || v.PasswordOrigin != "" || v.PasswordSetAt != nil {
		t.Fatalf("no credential: %+v", v)
	}
}

func TestViewEmitsNullableTimestampsAsNullAndUsesContractNames(t *testing.T) {
	v := BuildView(ViewInput{Account: Account{PublicID: "pid"}})
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(raw, &m)
	for _, k := range []string{
		"id", "publicID", "username", "displayName", "avatarURL", "email", "phone", "role", "status", "timezone", "locale",
		"profilePreferences", "appearancePreferences", "onboardingCompletedAt", "emailVerifiedAt", "emailSource",
		"emailBootstrapUsedAt", "phoneVerifiedAt", "usernameChangedAt", "passwordEnabled", "passwordSetAt",
		"passwordOrigin", "mustResetPassword", "initialUsernameRequired", "initialSecurityRequired",
		"twoFactorAvailable", "twoFactorEnabled", "twoFactorRequired", "twoFactorRecoveryCount", "lastLoginAt",
		"lastActiveAt", "createdAt", "updatedAt", "subscriptionTier", "subscriptionPlanID", "subscriptionPlanName",
		"subscriptionStatus", "subscriptionExpiresAt", "identityProviders",
	} {
		if _, ok := m[k]; !ok {
			t.Errorf("missing %q", k)
		}
	}
	for _, k := range []string{"onboardingCompletedAt", "emailVerifiedAt", "emailBootstrapUsedAt", "phoneVerifiedAt", "usernameChangedAt", "passwordSetAt", "lastLoginAt", "lastActiveAt", "subscriptionPlanID", "subscriptionExpiresAt"} {
		if m[k] != nil {
			t.Errorf("%s = %v, want null", k, m[k])
		}
	}
	if arr, ok := m["identityProviders"].([]any); !ok || len(arr) != 0 {
		t.Fatalf("identityProviders: %v", m["identityProviders"])
	}
	for _, secret := range []string{"passwordHash", "password_hash", "billingBalanceNanousd"} {
		if _, leaked := m[secret]; leaked {
			t.Errorf("%s must not be serialized", secret)
		}
	}
}

func TestViewSubscriptionOverrides(t *testing.T) {
	exp := time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC)
	planID := uint(7)
	v := BuildView(ViewInput{Subscription: &SubscriptionInfo{Tier: " pro ", PlanName: "  ", Status: "active", PlanID: &planID, ExpiresAt: &exp}})
	if v.SubscriptionTier != "pro" || v.SubscriptionPlanName != "free" || v.SubscriptionStatus != "active" ||
		v.SubscriptionPlanID == nil || *v.SubscriptionPlanID != 7 || v.SubscriptionExpiresAt == nil || !v.SubscriptionExpiresAt.Equal(exp) {
		t.Fatalf("%+v", v)
	}
	// Plan id and expiry are copied as given, even when null.
	v = BuildView(ViewInput{Subscription: &SubscriptionInfo{Tier: "", PlanID: nil, ExpiresAt: nil}})
	if v.SubscriptionTier != "free" || v.SubscriptionPlanID != nil || v.SubscriptionExpiresAt != nil {
		t.Fatalf("%+v", v)
	}
}

func TestViewLastActiveAndBillingAndProviders(t *testing.T) {
	login := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	active := login.Add(48 * time.Hour)
	v := BuildView(ViewInput{
		Account:      Account{LastLoginAt: &login},
		LastActiveAt: &active,
		Billing:      &BillingAccountInfo{Currency: " ", Balance: 42, Status: "suspended"},
		Providers:    []IdentityProviderSummary{{ID: "p1", Type: "oidc", Name: "Okta", Slug: "okta", LogoURL: "l"}},
	})
	if !v.LastActiveAt.Equal(active) || !v.LastLoginAt.Equal(login) {
		t.Fatalf("activity: %v %v", v.LastActiveAt, v.LastLoginAt)
	}
	if v.BillingCurrency != "USD" || v.BillingBalanceNanousd != 42 || v.BillingStatus != "suspended" {
		t.Fatalf("billing: %+v", v)
	}
	if len(v.IdentityProviders) != 1 || v.IdentityProviders[0].Slug != "okta" {
		t.Fatalf("providers: %+v", v.IdentityProviders)
	}
}

func TestViewCredentialFlags(t *testing.T) {
	at := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	v := BuildView(ViewInput{
		Account:            Account{Username: "bob"},
		Credential:         &Credential{PasswordEnabled: true, PasswordSetAt: &at, PasswordOrigin: PasswordOriginAdminReset, MustResetPassword: true, PasswordHash: "secret-hash"},
		TwoFactorAvailable: true, TwoFactorEnabled: true, TwoFactorRecoveryCount: 8,
		InitialUsernameRequired: true, InitialSecurityRequired: true,
	})
	if !v.PasswordEnabled || !v.MustResetPassword || v.PasswordOrigin != PasswordOriginAdminReset || !v.PasswordSetAt.Equal(at) ||
		!v.TwoFactorEnabled || v.TwoFactorRecoveryCount != 8 || !v.InitialUsernameRequired || !v.InitialSecurityRequired || v.TwoFactorRequired {
		t.Fatalf("%+v", v)
	}
	raw, _ := json.Marshal(v)
	if string(raw) == "" || contains(string(raw), "secret-hash") {
		t.Fatal("hash must never be serialized")
	}
}
