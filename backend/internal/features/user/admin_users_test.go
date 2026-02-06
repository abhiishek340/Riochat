package user

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestAdminListPaginationAndSearch(t *testing.T) {
	ae := newAdminEnv(t, "self")
	ae.seed("alice")
	ae.seed("bob", func(a *Account) { a.Email = "100%real@example.com"; a.PublicID = "abcdef0123456789abcdef0123456789" })
	ae.seed("carol_x")

	total, list := ae.page(ae.do("GET", "/admin/users", nil))
	if total != 4 || len(list) != 4 || list[0].Username != "carol_x" || list[3].Username != "root-admin" {
		t.Fatalf("newest first: %d %s", total, names(list))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?page_size=2", nil)); len(l) != 2 {
		t.Fatalf("page size: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?page=abc&page_size=-4", nil)); len(l) != 4 {
		t.Fatalf("bad numbers fall back: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?page=2&page_size=3", nil)); len(l) != 1 {
		t.Fatalf("second page: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?q=ALI", nil)); names(l) != "alice" {
		t.Fatalf("case-insensitive: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?q=%25", nil)); names(l) != "bob" {
		t.Fatalf("percent is literal: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?q=_x", nil)); names(l) != "carol_x" {
		t.Fatalf("underscore is literal: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?q=CDEF0123", nil)); names(l) != "bob" {
		t.Fatalf("public id fragment: %s", names(l))
	}
	if tot, l := ae.page(ae.do("GET", "/admin/users?q=nobody", nil)); tot != 0 || l == nil {
		t.Fatalf("empty page is an array: %v", l)
	}
}

func TestAdminListFiltersBySubscriptionAndProvider(t *testing.T) {
	ae := newAdminEnv(t, "period")
	db := ae.env.DB
	free, _ := seedPlan(t, db, "free", true, "month", true, true)
	pro, _ := seedPlan(t, db, "pro", true, "month", true, true)
	payer := ae.seed("payer")
	freebie := ae.seed("freebie")
	gone := ae.seed("gone")
	now := ae.env.Clock.Now()
	end := now.Add(48 * time.Hour)
	db.Create(&subscriptionRow{UserID: payer.ID, PlanID: pro.ID, Status: "active", StartAt: now.Add(-time.Hour), CurrentPeriodStartAt: now.Add(-time.Hour), CurrentPeriodEndAt: &end})
	db.Create(&subscriptionRow{UserID: freebie.ID, PlanID: free.ID, Status: "active", StartAt: now.Add(-time.Hour), CurrentPeriodStartAt: now.Add(-time.Hour)})
	db.Create(&subscriptionRow{UserID: gone.ID, PlanID: pro.ID, Status: "canceled", StartAt: now.Add(-time.Hour), CurrentPeriodStartAt: now.Add(-time.Hour), CurrentPeriodEndAt: &end})

	if _, l := ae.page(ae.do("GET", "/admin/users?subscription_status=active", nil)); names(l) != "payer" {
		t.Fatalf("paid only: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?subscription_status=free", nil)); names(l) != "gone,freebie,root-admin" {
		t.Fatalf("everybody else: %s", names(l))
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?subscription_status=canceled", nil)); names(l) != "gone" {
		t.Fatalf("exact status: %s", names(l))
	}

	prov := providerRow{PublicID: "pp01", Type: "oidc", Name: "Corp SSO", Slug: "corp", LogoURL: "/l.png"}
	db.Create(&prov)
	db.Create(&linkRow{UserID: gone.ID, ProviderID: prov.ID, ProviderSubject: "s1", LinkedAt: now})
	total, l := ae.page(ae.do("GET", "/admin/users?identity_provider=corp", nil))
	if total != 1 || names(l) != "gone" || len(l[0].Providers) != 1 || l[0].Providers[0].Slug != "corp" || l[0].Providers[0].Name != "Corp SSO" {
		t.Fatalf("provider filter: %v", l)
	}
	if _, l := ae.page(ae.do("GET", "/admin/users?identity_provider=unknown", nil)); len(l) != 0 {
		t.Fatalf("unknown slug: %s", names(l))
	}
	// Users without providers still carry an array.
	if _, l := ae.page(ae.do("GET", "/admin/users?q=payer", nil)); l[0].Providers == nil {
		t.Fatal("identityProviders is never null")
	}
}

func TestAdminViewDerivations(t *testing.T) {
	ae := newAdminEnv(t, "period")
	db := ae.env.DB
	pro, _ := seedPlan(t, db, "pro", true, "month", true, true)
	login := ae.env.Clock.Now().Add(-48 * time.Hour)
	u := ae.seed("viewer", func(a *Account) { a.LastLoginAt = &login })
	idle := ae.seed("idle")
	seen := ae.env.Clock.Now().Add(-time.Hour)
	db.Create(&sessionRow{SessionID: "x", UserID: u.ID, IssuedAt: seen.Add(-time.Hour), LastSeenAt: &seen, ExpiresAt: seen.Add(24 * time.Hour)})
	end := ae.env.Clock.Now().Add(72 * time.Hour)
	db.Create(&subscriptionRow{UserID: u.ID, PlanID: pro.ID, Status: "active", StartAt: login, CurrentPeriodStartAt: login, CurrentPeriodEndAt: &end})
	db.Create(&billingAccountRow{UserID: u.ID, Currency: "EUR", BalanceNanousd: 2_500_000_000, Status: "frozen"})
	db.Create(&mfaRow{UserID: u.ID, TOTPEnabled: true, RecoveryCodesHash: `[{"h":"a"},{"h":"b"},{"h":"c"}]`})

	got := ae.userFrom(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", u.ID), map[string]any{"reason": "x", "timezone": "Asia/Tokyo"}))
	if !got.TwoFactorAvailable || !got.TwoFactorEnabled || got.TwoFactorRequired || got.TwoFactorRecoveryCnt != 3 {
		t.Fatalf("2fa: %+v", got)
	}
	if got.LastActiveAt == nil || !got.LastActiveAt.Equal(seen) {
		t.Fatalf("last active from sessions: %v", got.LastActiveAt)
	}
	if got.SubscriptionTier != "pro" || got.SubscriptionStatus != "active" || got.SubscriptionExpiresAt == nil || got.SubscriptionPlanID == nil {
		t.Fatalf("subscription: %+v", got)
	}
	if got.BillingCurrency != "EUR" || got.BillingNano != 2_500_000_000 || got.BillingUSD != 2.5 || got.BillingStatus != "frozen" {
		t.Fatalf("account: %+v", got)
	}
	_, list := ae.page(ae.do("GET", "/admin/users?q=idle", nil))
	d := list[0]
	if d.ID != idle.ID || d.LastActiveAt != nil || d.TwoFactorEnabled || d.TwoFactorRecoveryCnt != 0 || d.SubscriptionTier != "free" ||
		d.SubscriptionPlanName != "free" || d.SubscriptionStatus != "free" || d.SubscriptionPlanID != nil || d.BillingCurrency != "USD" ||
		d.BillingNano != 0 || d.BillingStatus != "active" {
		t.Fatalf("defaults: %+v", d)
	}

	// Billing mode decides which half is filled.
	ae.billing.mode = "self"
	g := ae.userFrom(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", u.ID), map[string]any{"timezone": "Asia/Seoul"}))
	if g.SubscriptionTier != "pro" || g.BillingCurrency != "USD" || g.BillingNano != 0 {
		t.Fatalf("self mode: subscription only: %+v", g)
	}
	ae.billing.mode = "usage"
	g = ae.userFrom(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", u.ID), map[string]any{"timezone": "Asia/Shanghai"}))
	if g.SubscriptionTier != "free" || g.BillingCurrency != "EUR" || g.BillingNano != 2_500_000_000 {
		t.Fatalf("usage mode: account only: %+v", g)
	}
}

func TestAdminCreateUser(t *testing.T) {
	ae := newAdminEnv(t, "self")
	w := ae.do("POST", "/admin/users", map[string]any{"username": "Bob_1", "password": "abcdefgh"})
	u := ae.userFrom(w)
	if u.Username != "bob_1" || u.DisplayName != "bob_1" || u.Role != "user" || u.Status != "active" || u.Locale != "en-US" ||
		u.Timezone != "Etc/UTC" || u.TwoFactorEnabled || u.Providers == nil || len(u.Providers) != 0 || len(u.PublicID) != 32 {
		t.Fatalf("created: %+v", u)
	}
	var n int64
	ae.env.DB.Model(&billingAccountRow{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatal("no billing account row is created")
	}
	if logs := ae.audits("admin_create_user"); len(logs) != 1 || logs[0].ResourceID != fmt.Sprint(u.ID) || logs[0].Resource != "user" ||
		!strings.Contains(logs[0].DetailJSON, `"username":"bob_1"`) || logs[0].ActorUserID != ae.admin.ID {
		t.Fatalf("audit: %+v", logs)
	}
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "BOB_1", "password": "abcdefgh"}), 409, "user.username_already_exists")

	for _, name := range []string{"admin", "-abc", "a@b.c", "ab c", "abc_"} {
		ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": name, "password": "abcdefgh"}), 400, "user.invalid_username")
	}
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "ab", "password": "abcdefgh"}), 400, "request.invalid_body")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": strings.Repeat("a", 17), "password": "abcdefgh"}), 400, "request.invalid_body")

	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "pw1", "password": "12345678"}), 400, "auth.invalid_password")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "pw2", "password": "  1234567 "}), 400, "auth.invalid_password")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "pw3", "password": strings.Repeat("a", 129)}), 400, "request.invalid_body")
	ae.expect(ae.do("POST", "/admin/users", nil), 400, "request.invalid_body")

	for _, avatar := range []string{"file:file_abc", "ftp://x"} {
		ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "av1", "password": "abcdefgh", "avatarURL": avatar}), 400, "user.invalid_avatar_url")
	}
	for i, avatar := range []string{"https://x.test/a.png", "/static/a.png", "generated:github:foo"} {
		ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": fmt.Sprintf("av%d", i+2), "password": "abcdefgh", "avatarURL": avatar}), 200, "")
	}

	p := ae.userFrom(ae.do("POST", "/admin/users", map[string]any{"username": "ph1", "password": "abcdefgh", "phone": "+86 (138) 0013-8000"}))
	if p.Phone != "+8613800138000" {
		t.Fatalf("phone: %q", p.Phone)
	}
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "ph2", "password": "abcdefgh", "phone": "abc"}), 400, "user.invalid_phone")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "ph3", "password": "abcdefgh", "phone": "12345"}), 400, "user.invalid_phone")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "em1", "password": "abcdefgh", "email": "Name <a@b.co>"}), 400, "request.invalid_body")
	// A malformed email is a body validation error carrying the field hint.
	w = ae.do("POST", "/admin/users", map[string]any{"username": "em2", "password": "abcdefgh", "email": "notanemail"})
	ae.expect(w, 400, "request.invalid_body")
	if b := w.Body.String(); !strings.Contains(b, `"field":"email"`) || !strings.Contains(b, `"rule":"email"`) ||
		!strings.Contains(b, "email must be a valid email address") {
		t.Fatalf("email field error: %s", b)
	}

	l := ae.userFrom(ae.do("POST", "/admin/users", map[string]any{"username": "lc1", "password": "abcdefgh", "locale": "fr_fr", "timezone": "Asia/Tokyo"}))
	if l.Locale != "fr-FR" || l.Timezone != "Asia/Tokyo" {
		t.Fatalf("locale/zone: %+v", l)
	}
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "lc2", "password": "abcdefgh", "locale": "english"}), 400, "user.invalid_locale")
	ae.expect(ae.do("POST", "/admin/users", map[string]any{"username": "lc3", "password": "abcdefgh", "timezone": "Mars/Base"}), 400, "user.invalid_time_zone")

	// In self mode the subscription fields are accepted and ignored.
	future := ae.env.Clock.Now().Add(24 * time.Hour).Format(time.RFC3339)
	s := ae.userFrom(ae.do("POST", "/admin/users", map[string]any{"username": "sub0", "password": "abcdefgh", "subscriptionTier": "pro", "subscriptionExpiresAt": future}))
	if s.SubscriptionTier != "free" {
		t.Fatalf("self mode ignores the tier: %+v", s)
	}
}

func TestAdminCreateInPeriodMode(t *testing.T) {
	ae := newAdminEnv(t, "period")
	seedPlan(t, ae.env.DB, "free", true, "month", true, true)
	seedPlan(t, ae.env.DB, "pro", true, "month", true, true)
	body := func(name string, extra map[string]any) map[string]any {
		m := map[string]any{"username": name, "password": "abcdefgh"}
		for k, v := range extra {
			m[k] = v
		}
		return m
	}
	ae.expect(ae.do("POST", "/admin/users", body("pp01", map[string]any{"subscriptionTier": "pro"})), 400, "billing.subscription_expiry_required")
	past := ae.env.Clock.Now().Add(-time.Hour).Format(time.RFC3339)
	ae.expect(ae.do("POST", "/admin/users", body("pp02", map[string]any{"subscriptionTier": "pro", "subscriptionExpiresAt": past})), 400, "billing.invalid_subscription_expiry")
	ae.expect(ae.do("POST", "/admin/users", body("pp03", map[string]any{"subscriptionTier": "gold"})), 400, "billing.invalid_subscription_tier")
	future := ae.env.Clock.Now().Add(72 * time.Hour).UTC().Format(time.RFC3339)
	u := ae.userFrom(ae.do("POST", "/admin/users", body("pp04", map[string]any{"subscriptionTier": "PRO", "subscriptionExpiresAt": future})))
	if u.SubscriptionTier != "pro" || u.SubscriptionExpiresAt == nil || u.SubscriptionStatus != "active" {
		t.Fatalf("paid user: %+v", u)
	}
	f := ae.userFrom(ae.do("POST", "/admin/users", body("pp05", nil)))
	if f.SubscriptionTier != "free" || f.SubscriptionStatus != "free" {
		t.Fatalf("default is the free plan: %+v", f)
	}
}
