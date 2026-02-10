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

func TestAdminPatchBasics(t *testing.T) {
	ae := newAdminEnv(t, "self")
	verified := ae.env.Clock.Now()
	u := ae.seed("target1", func(a *Account) {
		a.Email = "t@example.com"
		a.EmailVerifiedAt = &verified
		a.PhoneVerifiedAt = &verified
		a.Phone = "123456"
	})
	path := fmt.Sprintf("/admin/users/%d", u.ID)

	got := ae.userFrom(ae.do("PATCH", path, map[string]any{"displayName": "Zed Zed"}))
	if got.DisplayName != "Zed Zed" {
		t.Fatalf("display name: %+v", got)
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"displayName": "Zed Zed"}), 400, "user.empty_patch")
	ae.expect(ae.do("PATCH", path, map[string]any{}), 400, "user.empty_patch")
	ae.expect(ae.do("PATCH", path, map[string]any{"reason": "x"}), 400, "user.empty_patch")
	ae.expect(ae.do("PATCH", path, map[string]any{"displayName": "ab"}), 400, "request.invalid_body")
	if w := ae.do("PATCH", path, map[string]any{"displayName": "ab"}); !strings.Contains(w.Body.String(), "displayName must be at least 3") ||
		!strings.Contains(w.Body.String(), `"fieldErrors"`) {
		t.Fatalf("display name field error: %s", w.Body.String())
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"displayName": "   "}), 400, "user.invalid_display_name")
	ae.expect(ae.do("PATCH", path, map[string]any{"displayName": strings.Repeat("d", 17)}), 400, "request.invalid_body")

	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"email": "new@example.com", "reason": "fix typo"}))
	if got.Email != "new@example.com" || got.EmailVerifiedAt != nil {
		t.Fatalf("email change resets verification: %+v", got)
	}
	if ae.reload(u.ID).EmailSource != EmailSourceAdminSet {
		t.Fatal("email source becomes admin_set")
	}
	logs := ae.audits("admin_patch_user")
	last := logs[len(logs)-1]
	if !strings.Contains(last.DetailJSON, `"email_verification_reset":"true"`) || !strings.Contains(last.DetailJSON, `"reason":"fix typo"`) ||
		!strings.Contains(last.DetailJSON, `"from_email":"t@example.com"`) {
		t.Fatalf("audit detail: %s", last.DetailJSON)
	}
	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"email": ""}))
	if got.Email != "" {
		t.Fatalf("email cleared: %+v", got)
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"email": "not-an-email"}), 400, "user.invalid_email")
	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"phone": "+44 7700 900123"}))
	if got.Phone != "+447700900123" || ae.reload(u.ID).PhoneVerifiedAt != nil {
		t.Fatalf("phone: %+v", got)
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"phone": "zz"}), 400, "user.invalid_phone")

	ae.expect(ae.do("PATCH", path, map[string]any{"timezone": "Nowhere/City"}), 400, "user.invalid_time_zone")
	ae.userFrom(ae.do("PATCH", path, map[string]any{"timezone": "Asia/Tokyo", "locale": "ja_jp"}))
	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"timezone": "", "locale": ""}))
	if got.Timezone != "Etc/UTC" || got.Locale != "en-US" {
		t.Fatalf("blank means default: %+v", got)
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"locale": "english"}), 400, "user.invalid_locale")
	ae.userFrom(ae.do("PATCH", path, map[string]any{"profilePreferences": "  anything goes  "}))
	if ae.reload(u.ID).ProfilePreferences != "anything goes" {
		t.Fatal("profile preferences stored verbatim after trimming")
	}

	ae.expect(ae.do("PATCH", "/admin/users/0", map[string]any{"displayName": "Zed Zed"}), 400, "user.invalid_id")
	ae.expect(ae.do("PATCH", "/admin/users/abc", nil), 400, "user.invalid_id")
	ae.expect(ae.do("PATCH", "/admin/users/99999", map[string]any{"displayName": "Zed Zed"}), 404, "user.not_found")
	ae.expect(ae.do("PATCH", path, nil), 400, "request.invalid_body")
}

func TestAdminPatchAvatar(t *testing.T) {
	images := newFakeImages()
	ae := newAdminEnv(t, "self", func(p *Ports) { p.Images = images })
	u := ae.seed("avatarman")
	other := ae.seed("other1")
	path := fmt.Sprintf("/admin/users/%d", u.ID)
	images.allow(u.ID, "file_abc-1")
	images.allow(other.ID, "file_theirs")
	if g := ae.userFrom(ae.do("PATCH", path, map[string]any{"avatarURL": "file:file_abc-1"})); ae.reload(u.ID).AvatarURL != "file:file_abc-1" {
		t.Fatalf("owned upload accepted: %+v", g)
	}
	ae.expect(ae.do("PATCH", path, map[string]any{"avatarURL": "file:file_theirs"}), 400, "user.invalid_avatar_url")
	ae.expect(ae.do("PATCH", path, map[string]any{"avatarURL": "ftp://nope"}), 400, "user.invalid_avatar_url")
	ae.userFrom(ae.do("PATCH", path, map[string]any{"avatarURL": " https://x.test/a.png "}))
	if ae.reload(u.ID).AvatarURL != "https://x.test/a.png" {
		t.Fatal("avatar trimmed")
	}
}

func TestAdminPatchRoleRules(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super1", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("plain1")
	sid := ae.session(u.ID)
	path := fmt.Sprintf("/admin/users/%d", u.ID)

	got := ae.userFrom(ae.do("PATCH", path, map[string]any{"role": "admin"}))
	if got.Role != "admin" {
		t.Fatalf("promoted: %+v", got)
	}
	if rev, reason := ae.revoked(sid); !rev || reason != "role changed to admin" {
		t.Fatalf("sessions revoked on role change: %v %q", rev, reason)
	}
	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", ae.admin.ID), map[string]any{"role": "user"}), 409, "user.self_role_change_not_allowed")
	ae.expect(ae.do("PATCH", path, map[string]any{"role": "root"}), 400, "user.invalid_role")
	ae.expect(ae.do("PATCH", path, map[string]any{"role": "superadmin"}), 403, "user.superadmin_management_protected")
	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", super.ID), map[string]any{"displayName": "Boss Man"}), 403, "user.superadmin_management_protected")

	// A superadmin may promote, but never demote the last superadmin.
	root := super
	ae.expect(ae.doAs(root, "PATCH", path, map[string]any{"role": "superadmin"}), 200, "")
	second := ae.reload(u.ID)
	if second.Role != RoleSuperAdmin {
		t.Fatalf("second superadmin: %s", second.Role)
	}
	ae.expect(ae.doAs(root, "PATCH", path, map[string]any{"role": "user"}), 200, "")
	ae.expect(ae.doAs(root, "PATCH", fmt.Sprintf("/admin/users/%d", root.ID), map[string]any{"role": "user"}), 409, "user.self_role_change_not_allowed")
}

func TestAdminPatchLastSuperadminWithTwo(t *testing.T) {
	ae := newAdminEnv(t, "self")
	a := ae.seed("super-a", func(x *Account) { x.Role = RoleSuperAdmin })
	b := ae.seed("super-b", func(x *Account) { x.Role = RoleSuperAdmin })
	ae.expect(ae.doAs(a, "PATCH", fmt.Sprintf("/admin/users/%d", b.ID), map[string]any{"role": "user"}), 200, "")
	if ae.reload(b.ID).Role != RoleUser {
		t.Fatal("with two superadmins one can be demoted")
	}
}

func TestAdminPatchStatusAndSubscription(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super2", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("plain2")
	sid := ae.session(u.ID)
	path := fmt.Sprintf("/admin/users/%d", u.ID)

	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", ae.admin.ID), map[string]any{"status": "suspended"}), 409, "user.self_status_change_not_allowed")
	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", super.ID), map[string]any{"status": "suspended"}), 403, "user.superadmin_management_protected")
	ae.expect(ae.doAs(super, "PATCH", fmt.Sprintf("/admin/users/%d", ae.admin.ID), map[string]any{"status": "locked"}), 200, "")
	ae.expect(ae.do("PATCH", path, map[string]any{"status": "pending_activation"}), 400, "user.invalid_status")
	ae.expect(ae.do("PATCH", path, map[string]any{"subscriptionTier": "pro"}), 409, "billing.payment_required")
	ae.expect(ae.do("PATCH", path, map[string]any{"subscriptionExpiresAt": "2031-01-01T00:00:00Z"}), 409, "billing.payment_required")

	got := ae.userFrom(ae.do("PATCH", path, map[string]any{"status": "suspended"}))
	if got.Status != "suspended" {
		t.Fatalf("status: %+v", got)
	}
	if rev, reason := ae.revoked(sid); !rev || reason != "status set to suspended" {
		t.Fatalf("revoked: %v %q", rev, reason)
	}
}

func TestAdminPatchSubscriptionInPeriodMode(t *testing.T) {
	ae := newAdminEnv(t, "period")
	seedPlan(t, ae.env.DB, "free", true, "month", true, true)
	seedPlan(t, ae.env.DB, "pro", true, "month", true, true)
	u := ae.seed("subject")
	path := fmt.Sprintf("/admin/users/%d", u.ID)
	future := ae.env.Clock.Now().Add(96 * time.Hour).UTC()

	ae.expect(ae.do("PATCH", path, map[string]any{"subscriptionTier": "pro"}), 400, "billing.subscription_expiry_required")
	ae.expect(ae.do("PATCH", path, map[string]any{"subscriptionTier": "gold"}), 400, "billing.invalid_subscription_tier")
	got := ae.userFrom(ae.do("PATCH", path, map[string]any{"subscriptionTier": "Pro", "subscriptionExpiresAt": future.Format(time.RFC3339)}))
	if got.SubscriptionTier != "pro" || got.SubscriptionExpiresAt == nil || !got.SubscriptionExpiresAt.Equal(future) {
		t.Fatalf("assigned: %+v", got)
	}
	// Nothing differs: no effect, so the patch is empty.
	ae.expect(ae.do("PATCH", path, map[string]any{"subscriptionTier": "pro"}), 400, "user.empty_patch")
	later := future.Add(24 * time.Hour)
	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"subscriptionExpiresAt": later.Format(time.RFC3339)}))
	if got.SubscriptionTier != "pro" || !got.SubscriptionExpiresAt.Equal(later) {
		t.Fatalf("expiry extended: %+v", got)
	}
	got = ae.userFrom(ae.do("PATCH", path, map[string]any{"subscriptionTier": "free"}))
	if got.SubscriptionTier != "free" || got.SubscriptionExpiresAt != nil {
		t.Fatalf("back to free: %+v", got)
	}
	last := ae.audits("admin_patch_user")
	if d := last[len(last)-1].DetailJSON; !strings.Contains(d, "subscription_tier") {
		t.Fatalf("audit lists the subscription change: %s", d)
	}
}

func TestAdminSetStatusRoute(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super3", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("locked-out", func(a *Account) { a.Status = StatusLocked })
	cred := seedCredential(t, ae.env.DB, u.ID)
	lockedUntil := ae.env.Clock.Now().Add(time.Hour)
	ae.env.DB.Model(&Credential{}).Where("id = ?", cred.ID).Update("locked_until", lockedUntil)
	sid := ae.session(u.ID)
	path := fmt.Sprintf("/admin/users/%d/status", u.ID)

	ae.expect(ae.do("PATCH", path, map[string]any{"status": "bogus"}), 400, "user.invalid_status")
	ae.expect(ae.do("PATCH", path, map[string]any{}), 400, "request.invalid_body")
	ae.expect(ae.do("PATCH", "/admin/users/77777/status", map[string]any{"status": "active"}), 404, "user.not_found")
	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d/status", super.ID), map[string]any{"status": "active"}), 403, "user.superadmin_management_protected")
	ae.expect(ae.doAs(super, "PATCH", fmt.Sprintf("/admin/users/%d/status", super.ID), map[string]any{"status": "active"}), 409, "user.superadmin_status_protected")

	got := ae.userFrom(ae.do("PATCH", path, map[string]any{"status": " active ", "reason": "appeal"}))
	if got.Status != "active" {
		t.Fatalf("status: %+v", got)
	}
	var c Credential
	ae.env.DB.Where("id = ?", cred.ID).Take(&c)
	if c.FailedLoginCount != 0 || c.LockedUntil != nil {
		t.Fatalf("activation clears the lock: %+v", c)
	}
	if rev, _ := ae.revoked(sid); rev {
		t.Fatal("activation keeps sessions")
	}
	// Suspending ends sessions; repeating the same status still runs the effects.
	ae.userFrom(ae.do("PATCH", path, map[string]any{"status": "suspended"}))
	if rev, reason := ae.revoked(sid); !rev || reason != "status set to suspended" {
		t.Fatalf("revoked: %v %q", rev, reason)
	}
	ae.userFrom(ae.do("PATCH", path, map[string]any{"status": "suspended"}))
	logs := ae.audits("admin_update_user_status")
	if len(logs) != 3 || !strings.Contains(logs[0].DetailJSON, `"from_status":"locked"`) || !strings.Contains(logs[0].DetailJSON, `"reason":"appeal"`) {
		t.Fatalf("audit: %+v", logs)
	}
	// An admin may suspend themself on this route.
	ae.expect(ae.do("PATCH", fmt.Sprintf("/admin/users/%d/status", ae.admin.ID), map[string]any{"status": "suspended"}), 200, "")
}

func TestAdminResetPassword(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super4", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("forgetful")
	seedCredential(t, ae.env.DB, u.ID)
	superCred := seedCredential(t, ae.env.DB, super.ID)
	_ = superCred
	sid := ae.session(u.ID)
	path := fmt.Sprintf("/admin/users/%d/reset-password", u.ID)

	ae.expect(ae.do("POST", path, map[string]any{"newPassword": "12345678"}), 400, "auth.invalid_password")
	ae.expect(ae.do("POST", path, map[string]any{"newPassword": "short"}), 400, "request.invalid_body")
	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/reset-password", super.ID), map[string]any{"newPassword": "NewPass123"}), 403, "user.superadmin_management_protected")
	ae.expect(ae.doAs(super, "POST", fmt.Sprintf("/admin/users/%d/reset-password", super.ID), map[string]any{"newPassword": "NewPass123"}), 409, "user.superadmin_password_reset_protected")
	ae.expect(ae.do("POST", "/admin/users/4040/reset-password", map[string]any{"newPassword": "NewPass123"}), 404, "user.not_found")

	w := ae.do("POST", path, map[string]any{"newPassword": "NewPass123"})
	ae.expect(w, 200, "")
	if !strings.Contains(w.Body.String(), `"reset":true`) {
		t.Fatalf("body: %s", w.Body.String())
	}
	var c Credential
	ae.env.DB.Where("user_id = ?", u.ID).Take(&c)
	if c.PasswordOrigin != PasswordOriginAdminReset || !c.MustResetPassword || c.FailedLoginCount != 0 || c.PasswordHash == "old-hash" || !c.PasswordEnabled {
		t.Fatalf("credential: %+v", c)
	}
	if rev, reason := ae.revoked(sid); !rev || reason != "admin reset password" {
		t.Fatalf("revoked: %v %q", rev, reason)
	}
	ae.expect(ae.do("POST", path, map[string]any{"newPassword": "AnotherOne1", "mustResetPassword": false}), 200, "")
	ae.env.DB.Where("user_id = ?", u.ID).Take(&c)
	if c.MustResetPassword {
		t.Fatal("mustResetPassword=false honored")
	}

	// The trace event for the target is visible in the event listing.
	w = ae.do("GET", fmt.Sprintf("/admin/user-auth-events?user_id=%d&event_type=password_reset", u.ID), nil)
	ae.expect(w, 200, "")
	var evs struct {
		Total   int64
		Results []AuthEventView
	}
	testkit.Decode(t, w, &evs)
	if evs.Total != 2 || evs.Results[0].Reason != "admin_reset_password" || evs.Results[0].Result != "success" ||
		!strings.Contains(evs.Results[0].DetailJSON, `"actor_user_id":"`+fmt.Sprint(ae.admin.ID)+`"`) ||
		!strings.Contains(evs.Results[0].DetailJSON, `"must_reset_password":"false"`) || evs.Results[0].UserLabel != "forgetful" {
		t.Fatalf("events: %+v", evs)
	}
	// A target without a credential row cannot be reset.
	bare := ae.seed("nocred")
	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/reset-password", bare.ID), map[string]any{"newPassword": "NewPass123"}), 404, "user.not_found")
	if logs := ae.audits("admin_reset_user_password"); len(logs) != 2 || !strings.Contains(logs[0].DetailJSON, `"must_reset_password":"true"`) {
		t.Fatalf("audit: %+v", logs)
	}
}

func TestAdminResetTwoFactorAndRevoke(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super5", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("tfa-user")
	ae.env.DB.Create(&mfaRow{UserID: u.ID, TOTPEnabled: true, RecoveryCodesHash: `[{"h":"1"},{"h":"2"}]`})
	sid := ae.session(u.ID)
	path := fmt.Sprintf("/admin/users/%d/reset-2fa", u.ID)

	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/reset-2fa", super.ID), nil), 403, "user.superadmin_management_protected")
	ae.expect(ae.doAs(super, "POST", fmt.Sprintf("/admin/users/%d/reset-2fa", super.ID), nil), 409, "user.superadmin_two_factor_reset_protected")
	before := ae.userFrom(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", u.ID), map[string]any{"timezone": "Asia/Tokyo"}))
	if !before.TwoFactorEnabled || before.TwoFactorRecoveryCnt != 2 {
		t.Fatalf("2fa before: %+v", before)
	}
	ae.expect(ae.do("POST", path, nil), 200, "")
	after := ae.userFrom(ae.do("PATCH", fmt.Sprintf("/admin/users/%d", u.ID), map[string]any{"timezone": "Asia/Seoul"}))
	if after.TwoFactorEnabled || after.TwoFactorRecoveryCnt != 0 {
		t.Fatalf("2fa after: %+v", after)
	}
	if rev, reason := ae.revoked(sid); !rev || reason != "admin reset 2fa" {
		t.Fatalf("revoked: %v %q", rev, reason)
	}
	ae.expect(ae.do("POST", path, nil), 200, "")
	if logs := ae.audits("admin_reset_user_2fa"); len(logs) != 2 || !strings.Contains(logs[0].DetailJSON, fmt.Sprintf(`"target_user_id":"%d"`, u.ID)) {
		t.Fatalf("audit: %+v", logs)
	}

	// Revoking sessions works on anybody who is not protected, including the caller.
	other := ae.session(u.ID)
	mine := ae.session(ae.admin.ID)
	w := ae.do("POST", fmt.Sprintf("/admin/users/%d/revoke-sessions", u.ID), nil)
	ae.expect(w, 200, "")
	if !strings.Contains(w.Body.String(), `"revoked":true`) {
		t.Fatalf("body: %s", w.Body.String())
	}
	if rev, reason := ae.revoked(other); !rev || reason != "admin revoke all sessions" {
		t.Fatalf("revoked: %v %q", rev, reason)
	}
	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/revoke-sessions", ae.admin.ID), nil), 200, "")
	if rev, _ := ae.revoked(mine); !rev {
		t.Fatal("own sessions revoked")
	}
	if logs := ae.audits("admin_revoke_user_sessions"); len(logs) == 0 || !strings.Contains(logs[0].DetailJSON, fmt.Sprintf(`"target_user_id":"%d"`, u.ID)) {
		t.Fatalf("revoke audit: %+v", logs)
	}
	ae.expect(ae.do("POST", "/admin/users/0/revoke-sessions", nil), 400, "user.invalid_id")
	ae.expect(ae.do("POST", "/admin/users/91919/revoke-sessions", nil), 404, "user.not_found")
	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/revoke-sessions", super.ID), nil), 403, "user.superadmin_management_protected")
}

func TestAdminResetTwoFactorWithoutSecurityTables(t *testing.T) {
	ae := newAdminEnv(t, "self")
	u := ae.seed("nosec")
	sid := ae.session(u.ID)
	if err := ae.env.DB.Migrator().DropTable(&mfaRow{}); err != nil {
		t.Fatal(err)
	}
	ae.expect(ae.do("POST", fmt.Sprintf("/admin/users/%d/reset-2fa", u.ID), nil), 200, "")
	if rev, _ := ae.revoked(sid); rev {
		t.Fatal("nothing is done when the component is absent")
	}
	if len(ae.audits("admin_reset_user_2fa")) != 0 {
		t.Fatal("and nothing is audited")
	}
}

func TestAdminDeleteUser(t *testing.T) {
	ae := newAdminEnv(t, "self")
	super := ae.seed("super6", func(a *Account) { a.Role = RoleSuperAdmin })
	u := ae.seed("doomed")
	seedCredential(t, ae.env.DB, u.ID)
	ae.session(u.ID)
	ae.env.DB.Create(&billingAccountRow{UserID: u.ID, Currency: "USD", Status: "active"})

	ae.expect(ae.do("DELETE", fmt.Sprintf("/admin/users/%d", ae.admin.ID), nil), 409, "user.self_delete_not_allowed")
	ae.expect(ae.do("DELETE", fmt.Sprintf("/admin/users/%d", super.ID), nil), 403, "user.superadmin_management_protected")
	ae.expect(ae.doAs(ae.seed("super7", func(a *Account) { a.Role = RoleSuperAdmin }), "DELETE", fmt.Sprintf("/admin/users/%d", super.ID), nil), 409, "user.superadmin_delete_protected")
	ae.expect(ae.do("DELETE", "/admin/users/31337", nil), 404, "user.not_found")
	ae.expect(ae.do("DELETE", "/admin/users/x", nil), 400, "user.invalid_id")

	w := ae.do("DELETE", fmt.Sprintf("/admin/users/%d", u.ID), nil)
	ae.expect(w, 200, "")
	if !strings.Contains(w.Body.String(), `"deleted":true`) {
		t.Fatalf("body: %s", w.Body.String())
	}
	var n int64
	for _, m := range []any{&Account{}, &Credential{}, &sessionRow{}, &billingAccountRow{}} {
		ae.env.DB.Unscoped().Model(m).Where(map[string]any{userKey(m): u.ID}).Count(&n)
		if n != 0 {
			t.Fatalf("%T rows remain", m)
		}
	}
	logs := ae.audits("admin_delete_user")
	if len(logs) != 1 || !strings.Contains(logs[0].DetailJSON, `"username":"doomed"`) || !strings.Contains(logs[0].DetailJSON, u.PublicID) ||
		!strings.Contains(logs[0].DetailJSON, fmt.Sprintf(`"target_user_id":"%d"`, u.ID)) {
		t.Fatalf("audit: %+v", logs)
	}
}

func userKey(m any) string {
	if _, ok := m.(*Account); ok {
		return "id"
	}
	return "user_id"
}

func TestAdminDeleteRefusedWhenBuiltinKnowledgeBaseUsesFile(t *testing.T) {
	ae := newAdminEnv(t, "self")
	db := ae.env.DB
	u := ae.seed("fileowner")
	for _, ddl := range []string{
		`CREATE TABLE knowledge_bases (id integer primary key, scope text, owner_user_id integer, public_id text)`,
		`CREATE TABLE knowledge_base_files (id integer primary key, knowledge_base_id integer, file_object_id integer)`,
		`CREATE TABLE file_objects (id integer primary key, user_id integer, status text, deleted_at datetime, storage_path text)`,
		`INSERT INTO knowledge_bases (id, scope) VALUES (1, 'builtin')`,
		fmt.Sprintf(`INSERT INTO file_objects (id, user_id, status) VALUES (1, %d, 'active')`, u.ID),
		`INSERT INTO knowledge_base_files (knowledge_base_id, file_object_id) VALUES (1, 1)`,
	} {
		if err := db.Exec(ddl).Error; err != nil {
			t.Fatal(err)
		}
	}
	ae.expect(ae.do("DELETE", fmt.Sprintf("/admin/users/%d", u.ID), nil), 409, "knowledge_base.owner_file_reference")
	if ae.reload(u.ID).ID == 0 {
		t.Fatal("nothing deleted")
	}
}

func TestAdminRoutesRequireAdminAndMultiUser(t *testing.T) {
	ae := newAdminEnv(t, "self")
	plain := ae.seed("just-a-user")
	for _, p := range []string{"/admin/users", "/admin/user-auth-events"} {
		ae.expect(ae.doAs(plain, "GET", p, nil), 403, "auth.admin_required")
	}
	// A demoted admin with a still-valid token can read but not mutate.
	demoted := ae.seed("demoted", func(a *Account) { a.Role = RoleAdmin })
	tok := ae.as(demoted)
	ae.env.DB.Model(&Account{}).Where("id = ?", demoted.ID).Update("role", RoleUser)
	ae.expect(ae.env.Do("GET", "/api/v1/admin/users", tok, nil), 200, "")
	ae.expect(ae.env.Do("PATCH", fmt.Sprintf("/api/v1/admin/users/%d", plain.ID), tok, map[string]any{"displayName": "Zed Zed"}), 403, "auth.admin_required")
	ae.expect(ae.env.Do("POST", "/api/v1/admin/users", tok, map[string]any{"username": "nope1", "password": "abcdefgh"}), 403, "auth.admin_required")
	// An acting user whose row is gone gets 404 on most routes.
	ghost := Account{}
	ghost.ID = 9999
	ghost.Role = RoleAdmin
	ae.expect(ae.doAs(ghost, "POST", fmt.Sprintf("/admin/users/%d/revoke-sessions", plain.ID), nil), 404, "user.not_found")
}

func TestAdminAuthEvents(t *testing.T) {
	ae := newAdminEnv(t, "self")
	u := ae.seed("eventful", func(a *Account) { a.DisplayName = "Eve Ntful" })
	nameless := ae.seed("nameless", func(a *Account) { a.DisplayName = "  " })
	db := ae.env.DB
	base := ae.env.Clock.Now()
	add := func(uid uint, typ, result string, at time.Time) {
		db.Create(&AuthEvent{UserID: uid, EventType: typ, Result: result, OccurredAt: at, RequestID: "r-" + typ})
	}
	add(u.ID, "login", "failure", base)
	add(u.ID, "login", "success", base.Add(time.Minute))
	add(nameless.ID, "logout", "success", base.Add(2*time.Minute))
	add(555, "login", "failure", base.Add(3*time.Minute))
	add(0, "token_refresh", "blocked", base.Add(4*time.Minute))

	type listing struct {
		Total   int64
		Results []AuthEventView
	}
	get := func(q string) listing {
		w := ae.do("GET", "/admin/user-auth-events"+q, nil)
		ae.expect(w, 200, "")
		var l listing
		testkit.Decode(t, w, &l)
		return l
	}
	all := get("")
	if all.Total != 5 || all.Results[0].EventType != "token_refresh" || all.Results[4].Result != "failure" {
		t.Fatalf("newest first: %+v", all)
	}
	if r := all.Results[1]; r.Username != "" || r.UserDisplayName != "" || r.UserLabel != "" {
		t.Fatalf("deleted user: labels are empty: %+v", r)
	}
	if r := all.Results[0]; r.UserLabel != "" || r.UserID != 0 {
		t.Fatalf("user id 0: %+v", r)
	}
	if r := all.Results[2]; r.UserLabel != "nameless" || r.UserDisplayName != "  " {
		t.Fatalf("label falls back to the username: %+v", r)
	}
	if r := all.Results[3]; r.UserLabel != "Eve Ntful" || r.RequestID != "r-login" {
		t.Fatalf("display name label: %+v", r)
	}
	if l := get(fmt.Sprintf("?user_id=%d&event_type=login&result=failure", u.ID)); l.Total != 1 {
		t.Fatalf("exact filters: %+v", l)
	}
	if l := get("?page_size=2&page=2"); len(l.Results) != 2 || l.Total != 5 {
		t.Fatalf("paging: %+v", l)
	}
	for _, bad := range []string{"abc", "0", "-3", "99999999999999999999"} {
		ae.expect(ae.do("GET", "/admin/user-auth-events?user_id="+bad, nil), 400, "user.invalid_id")
	}
}
