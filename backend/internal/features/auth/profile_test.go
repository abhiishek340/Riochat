package auth

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func patchMe(e *testEnv, tok string, body map[string]any) (int, map[string]any) {
	w := e.request("PATCH", "/me", tok, body)
	if w.Code != 200 {
		return w.Code, nil
	}
	return 200, dataMap(e.t, w)["user"].(map[string]any)
}

func TestProfileReadAndEmptyPatch(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	w := e.request("GET", "/me", tok, nil)
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["publicID"] == "" || user["subscriptionTier"] != "free" || user["subscriptionPlanName"] != "free" ||
		user["subscriptionStatus"] != "free" || user["twoFactorAvailable"] != true || user["twoFactorRequired"] != false {
		t.Fatalf("view: %v", user)
	}
	if ps, ok := user["identityProviders"].([]any); !ok || len(ps) != 0 {
		t.Fatalf("identityProviders: %v", user["identityProviders"])
	}
	for _, k := range []string{"emailVerifiedAt", "phoneVerifiedAt", "usernameChangedAt", "emailBootstrapUsedAt", "subscriptionPlanID", "subscriptionExpiresAt"} {
		if v, present := user[k]; !present || v != nil {
			t.Errorf("%s should be present and null: %v", k, v)
		}
	}
	if user["lastActiveAt"] != user["lastLoginAt"] {
		t.Fatalf("lastActiveAt mirrors lastLoginAt")
	}
	// no recognised fields: unchanged, still 200
	code, after := patchMe(e, tok, map[string]any{})
	if code != 200 || after["displayName"] != user["displayName"] || after["id"] != float64(u.ID) {
		t.Fatalf("empty patch: %d %v", code, after)
	}
}

func TestProfilePatchRules(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})

	_, u := patchMe(e, tok, map[string]any{"locale": "en"})
	if u["locale"] != "en" {
		t.Fatalf("locale %v", u["locale"])
	}
	_, u = patchMe(e, tok, map[string]any{"locale": "en_US"})
	if u["locale"] != "en-US" {
		t.Fatalf("locale %v", u["locale"])
	}
	_, u = patchMe(e, tok, map[string]any{"locale": ""})
	if u["locale"] != "en-US" {
		t.Fatalf("empty locale resets: %v", u["locale"])
	}
	for _, bad := range []string{"fr", "en-GB", "de-DE", "pt-BR"} {
		w := e.request("PATCH", "/me", tok, map[string]any{"locale": bad})
		testkit.Expect(t, w, 400, "user.invalid_locale")
	}

	_, u = patchMe(e, tok, map[string]any{"timezone": "Asia/Shanghai"})
	if u["timezone"] != "Asia/Shanghai" {
		t.Fatalf("tz %v", u["timezone"])
	}
	_, u = patchMe(e, tok, map[string]any{"timezone": " "})
	if u["timezone"] != "Etc/UTC" {
		t.Fatalf("blank tz resets to UTC: %v", u["timezone"])
	}
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"timezone": "Mars/Base"}), 400, "user.invalid_time_zone")

	_, u = patchMe(e, tok, map[string]any{"displayName": "  New Name  "})
	if u["displayName"] != "New Name" {
		t.Fatalf("display name %v", u["displayName"])
	}
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"displayName": "ab"}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"displayName": "this-name-is-way-too-long"}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"displayName": "  a  "}), 400, "user.invalid_display_name")

	_, u = patchMe(e, tok, map[string]any{"profilePreferences": "  likes tea  "})
	if u["profilePreferences"] != "likes tea" {
		t.Fatalf("prefs %q", u["profilePreferences"])
	}
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"locale": 12}), 400, "request.invalid_body")
}

func TestProfileAppearancePreferences(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	_, u := patchMe(e, tok, map[string]any{"appearancePreferences": `{"fontSize":"huge"}`})
	if u["appearancePreferences"] != `{"fontSize":"standard"}` {
		t.Fatalf("unknown fontSize is normalised: %v", u["appearancePreferences"])
	}
	_, u = patchMe(e, tok, map[string]any{"appearancePreferences": ` { "preset":"azure", "chatFont":"mono", "chatFontWeight":"bold", "fontSize":"large" } `})
	if u["appearancePreferences"] != `{"chatFont":"mono","chatFontWeight":"bold","fontSize":"large","preset":"azure"}` {
		t.Fatalf("normalised JSON: %v", u["appearancePreferences"])
	}
	for _, bad := range []string{`{"x":"y"}`, `{"preset":"neon"}`, `{"preset":1}`, `not json`, `["a"]`, `{"chatFont":"comic"}`} {
		w := e.request("PATCH", "/me", tok, map[string]any{"appearancePreferences": bad})
		testkit.Expect(t, w, 400, "request.invalid_appearance_preferences")
	}
	_, u = patchMe(e, tok, map[string]any{"appearancePreferences": ""})
	if u["appearancePreferences"] != "" {
		t.Fatalf("empty clears: %v", u["appearancePreferences"])
	}
}

type fakeFiles struct{ owned map[string]uint }

func (f fakeFiles) IsOwnedImage(_ context.Context, userID uint, id string) bool {
	return f.owned[id] == userID
}

func TestProfileAvatarRules(t *testing.T) {
	e := newEnv(t, withHooks(func(h *Hooks) { h.AvatarFiles = fakeFiles{owned: map[string]uint{"file_abc-1_x": 2}} }))
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	ok := []string{"", "/static/a.png", "generated:github:octocat", "https://cdn.example.com/a.png", "http://x.example/a", "file:file_abc-1_x"}
	for _, v := range ok {
		code, u := patchMe(e, tok, map[string]any{"avatarURL": v})
		if code != 200 || u["avatarURL"] != v {
			t.Errorf("avatar %q: %d %v", v, code, u["avatarURL"])
		}
	}
	bad := []string{"javascript:alert(1)", "ftp://x/a", "https://", "data:image/png;base64,AAAA", "file:abc", "file:file_x y",
		"file:file_notmine", "file:file_" + string(make([]byte, 0)) + "!", "relative/path"}
	for _, v := range bad {
		testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]any{"avatarURL": v}), 400, "user.invalid_avatar_url")
	}

	// without a validator every file reference is invalid
	e2 := newEnv(t)
	_, tok2 := e2.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e2.request("PATCH", "/me", tok2, map[string]any{"avatarURL": "file:file_abc"}), 400, "user.invalid_avatar_url")
}

func TestProfilePatchWritesAudit(t *testing.T) {
	var got []AuditEntry
	e := newEnv(t, withHooks(func(h *Hooks) { h.Audit = auditFunc(func(a AuditEntry) { got = append(got, a) }) }))
	alice := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d, _ := e.signIn("alice", "correct-horse-9")
	if len(got) != 1 || got[0].Action != "login" || got[0].ResourceType != "user" || got[0].ResourceID != "alice" {
		t.Fatalf("login audit: %+v", got)
	}
	testkit.Expect(t, e.request("PATCH", "/me", d.AccessToken, map[string]any{"displayName": "Alice Z", "locale": "en"}), 200, "")
	last := got[len(got)-1]
	if last.Action != "update_profile" || last.ResourceID != strconv.FormatUint(uint64(alice.ID), 10) {
		t.Fatalf("audit (resource id is the numeric user id): %+v", last)
	}
	fields := last.Detail["fields"]
	if list, ok := fields.([]string); !ok || len(list) != 2 {
		t.Fatalf("fields: %#v", fields)
	}
}

func TestUsernameChangeAuditUsesNumericResourceID(t *testing.T) {
	var got []AuditEntry
	e := newEnv(t, withHooks(func(h *Hooks) { h.Audit = auditFunc(func(a AuditEntry) { got = append(got, a) }) }))
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "alice-two"}), 200, "")
	last := got[len(got)-1]
	if last.Action != "update_username" || last.ResourceID != strconv.FormatUint(uint64(u.ID), 10) {
		t.Fatalf("audit: %+v", last)
	}
}

type auditFunc func(AuditEntry)

func (f auditFunc) WriteAudit(_ context.Context, a AuditEntry) { f(a) }

func TestUsernameChange(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	e.seedUser(userSpec{Username: "taken-one", Onboarded: true})

	for _, bad := range []string{"Admin", "root", "ab", "-lead", "trail_", "has space", "with@at", "dot.dot", "support", "RioChat", "rio-chat"} {
		w := e.request("PATCH", "/me/username", tok, map[string]string{"username": bad})
		if w.Code != 400 {
			t.Errorf("%q: status %d", bad, w.Code)
		}
	}
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "Admin"}), 400, "user.invalid_username")
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "a-name-that-is-too-long"}), 400, "request.invalid_body")

	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "TAKEN-one"}), 409, "user.username_already_exists")
	if e.reloadUser(u.ID).UsernameChangedAt != nil {
		t.Fatalf("a failed change must not use up the allowance")
	}
	w := e.request("PATCH", "/me/username", tok, map[string]string{"username": "Neo_One"})
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["username"] != "neo_one" || user["usernameChangedAt"] == nil {
		t.Fatalf("user: %v", user)
	}
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "neo_two"}), 409, "user.username_change_used")
}

func TestUsernameChangeToSameNameStampsAllowance(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "alice"}), 200, "")
	if e.reloadUser(u.ID).UsernameChangedAt == nil {
		t.Fatalf("allowance should be stamped")
	}
}

// ---- email management ------------------------------------------------------------------------------

func TestEmailBootstrapWithVerification(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	other := e.seedUser(userSpec{Username: "bob", Email: "bob@example.com", EmailVerified: true})
	_ = other

	testkit.Expect(t, e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "bob@example.com"}), 400, "auth.email_already_exists")
	testkit.Expect(t, e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "bad"}), 400, "request.invalid_body")
	w := e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "Alice@Example.com"})
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != true || m["verificationMethod"] != "email" {
		t.Fatalf("start: %v", m)
	}
	code := e.Mail.lastCode(t, "alice@example.com")
	if code != "000000" {
		testkit.Expect(t, e.request("POST", "/me/email/bootstrap/complete", tok, map[string]string{"email": "alice@example.com", "code": "000000"}), 400, "auth.verification_code_invalid")
	}
	w = e.request("POST", "/me/email/bootstrap/complete", tok, map[string]string{"email": "alice@example.com", "code": code})
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["email"] != "alice@example.com" || user["emailVerifiedAt"] == nil || user["emailSource"] != "user_set" || user["emailBootstrapUsedAt"] == nil {
		t.Fatalf("user: %v", user)
	}
	_ = u
	// verified address: bootstrap is closed for good
	testkit.Expect(t, e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "z@example.com"}), 400, "auth.email_bootstrap_not_allowed")
}

func TestEmailBootstrapWithoutVerification(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	w := e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "alice@example.com"})
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != false || m["verificationMethod"] != "none" {
		t.Fatalf("start: %v", m)
	}
	w = e.request("POST", "/me/email/bootstrap/complete", tok, map[string]string{"email": "alice@example.com"})
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["email"] != "alice@example.com" || user["emailVerifiedAt"] != nil || user["emailBootstrapUsedAt"] == nil {
		t.Fatalf("user: %v", user)
	}
}

func TestEmailBootstrapAllowedForUnverifiedProviderEmail(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Email: "old@example.com", Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "n@example.com"}), 400, "auth.email_bootstrap_not_allowed")
	e.DB.Model(&User{}).Where("id = ?", u.ID).Update("email_source", EmailSourceProviderUnverified)
	testkit.Expect(t, e.request("POST", "/me/email/bootstrap/start", tok, map[string]string{"email": "n@example.com"}), 200, "")
}

func TestEmailVerifyCurrent(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", Onboarded: true})
	// verification disabled globally
	w := e.request("POST", "/me/email/verify-current/start", tok, nil)
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != false || m["verificationMethod"] != "none" {
		t.Fatalf("start: %v", m)
	}
	testkit.Expect(t, e.request("POST", "/me/email/verify-current/complete", tok, map[string]string{"code": "123456"}), 400, "auth.email_verification_disabled")

	e.Settings.set(verificationOn)
	testkit.Expect(t, e.request("POST", "/me/email/verify-current/start", tok, nil), 200, "")
	code := e.Mail.lastCode(t, "alice@example.com")
	w = e.request("POST", "/me/email/verify-current/complete", tok, map[string]string{"code": code})
	testkit.Expect(t, w, 200, "")
	if dataMap(t, w)["user"].(map[string]any)["emailVerifiedAt"] == nil {
		t.Fatalf("not verified")
	}
	// nothing left to verify
	testkit.Expect(t, e.request("POST", "/me/email/verify-current/start", tok, nil), 400, "auth.email_not_verified")

	_, tok2 := e.session(userSpec{Username: "noemail", Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/email/verify-current/start", tok2, nil), 400, "auth.email_not_verified")
}

func TestEmailChangeFlow(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	u, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	e.seedUser(userSpec{Username: "bob", Email: "bob@example.com", EmailVerified: true})

	w := e.request("POST", "/me/email/change/start-current", tok, nil)
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != true || m["verificationMethod"] != "email" {
		t.Fatalf("start-current: %v", m)
	}
	cur := e.Mail.lastCode(t, "alice@example.com")

	testkit.Expect(t, e.request("POST", "/me/email/change/start-new", tok, map[string]string{"email": "bob@example.com"}), 400, "auth.email_already_exists")
	testkit.Expect(t, e.request("POST", "/me/email/change/start-new", tok, map[string]string{"email": "new@example.com"}), 200, "")
	newCode := e.Mail.lastCode(t, "new@example.com")

	complete := func(body map[string]string) int {
		return e.request("POST", "/me/email/change/complete", tok, body).Code
	}
	testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "alice@example.com", "currentCode": cur, "newCode": newCode}), 400, "auth.email_unchanged")
	testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "bob@example.com", "currentCode": cur, "newCode": newCode}), 400, "auth.email_already_exists")
	// current-identity proof wrong
	if cur != "000000" {
		testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "new@example.com", "currentCode": "000000", "newCode": newCode}), 400, "auth.verification_code_invalid")
	}
	if complete(map[string]string{"email": "new@example.com", "currentCode": cur}) == 200 {
		t.Fatalf("missing new-address code must fail while verification is on")
	}
	// the failed attempt used up the current-identity code; ask again
	e.Clock.Advance(2 * time.Minute)
	testkit.Expect(t, e.request("POST", "/me/email/change/start-current", tok, nil), 200, "")
	testkit.Expect(t, e.request("POST", "/me/email/change/start-new", tok, map[string]string{"email": "new@example.com"}), 200, "")
	cur, newCode = e.Mail.lastCode(t, "alice@example.com"), e.Mail.lastCode(t, "new@example.com")
	w = e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "new@example.com", "currentVerificationMethod": "email", "currentCode": cur, "newCode": newCode})
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["email"] != "new@example.com" || user["emailVerifiedAt"] == nil || user["emailSource"] != "user_set" {
		t.Fatalf("user: %v", user)
	}
	if e.reloadUser(u.ID).Email != "new@example.com" {
		t.Fatalf("not stored")
	}
}

func TestEmailChangeWithoutVerificationStoresUnverified(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", Onboarded: true})
	w := e.request("POST", "/me/email/change/start-new", tok, map[string]string{"email": "new@example.com"})
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != false || m["verificationMethod"] != "none" {
		t.Fatalf("start-new: %v", m)
	}
	w = e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "new@example.com"})
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["email"] != "new@example.com" || user["emailVerifiedAt"] != nil {
		t.Fatalf("user: %v", user)
	}
}

func TestEmailChangeWithTwoFactorProof(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", Onboarded: true})
	secret, _ := e.enableTOTP(tok)
	w := e.request("POST", "/me/email/change/start-current", tok, nil)
	if m := dataMap(t, w); m["verificationMethod"] != "two_factor" || m["sent"] != false {
		t.Fatalf("start-current: %v", m)
	}
	testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "n@example.com", "currentCode": "000000"}), 400, "auth.verification_code_invalid")
	testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "n@example.com", "currentVerificationMethod": "email", "currentCode": "123456"}), 400, "auth.verification_method_unavailable")
	testkit.Expect(t, e.request("POST", "/me/email/change/complete", tok, map[string]string{"email": "n@example.com", "currentCode": e.totp(secret)}), 200, "")
}

// ---- account deletion -----------------------------------------------------------------------------------

type recordingPurger struct {
	veto     error
	purged   []uint
	failures []string
	total    int
	after    int
}

func (p *recordingPurger) BeforeDelete(context.Context, uint) error { return p.veto }
func (p *recordingPurger) Purge(_ context.Context, _ *gorm.DB, id uint) error {
	p.purged = append(p.purged, id)
	return nil
}
func (p *recordingPurger) AfterCommit(context.Context, uint) (int, []string) {
	p.after++
	return p.total, p.failures
}

func TestAccountDeletionNeedsVerification(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/delete/start", tok, nil), 400, "user.account_delete_verification_required")
	testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "none", "code": "123456"}), 400, "user.account_delete_verification_required")
	// A named method the user does not have is "unavailable", not "required".
	for _, m := range []string{"two_factor", "email"} {
		testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": m, "code": "123456"}), 400, "auth.verification_method_unavailable")
	}

	e.Settings.set(verificationOn)
	_, tok2 := e.session(userSpec{Username: "bob", Email: "bob@example.com", EmailVerified: true, Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/delete/start", tok2, map[string]string{"verificationMethod": "two_factor"}), 400, "auth.verification_method_unavailable")
	testkit.Expect(t, e.request("DELETE", "/me", tok2, map[string]string{"verificationMethod": "two_factor", "code": "123456"}), 400, "auth.verification_method_unavailable")
	testkit.Expect(t, e.request("DELETE", "/me", tok2, map[string]string{"code": "123456"}), 400, "user.account_delete_verification_required")
}

func TestSuperadminCannotDeleteAccount(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "root-ish", Role: RoleSuperAdmin, Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/delete/start", tok, nil), 403, "user.superadmin_delete_protected")
	testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "two_factor", "code": "123456"}), 403, "user.superadmin_delete_protected")
}

func TestAccountDeletionWithTwoFactor(t *testing.T) {
	purger := &recordingPurger{total: 3, failures: []string{"a/b.png"}}
	e := newEnv(t, withHooks(func(h *Hooks) { h.Purgers = []AccountPurger{purger} }))
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	secret, _ := e.enableTOTP(tok)
	e.seedLink(u.ID)

	w := e.request("POST", "/me/delete/start", tok, nil)
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != false || m["verificationMethod"] != "two_factor" {
		t.Fatalf("start: %v", m)
	}
	testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "two_factor", "code": "000000"}), 400, "auth.verification_code_invalid")
	var still int64
	e.DB.Model(&User{}).Where("id = ?", u.ID).Count(&still)
	if still != 1 {
		t.Fatalf("a wrong code must delete nothing")
	}

	w = e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "two_factor", "code": e.totp(secret)})
	testkit.Expect(t, w, 200, "")
	if dataMap(t, w)["deleted"] != true {
		t.Fatalf("body %s", w.Body)
	}
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie must be cleared")
	}
	for _, m := range []any{&User{}, &Credential{}, &Session{}, &MFASettings{}, &UserLink{}, &ContactVerification{}} {
		var n int64
		q := e.DB.Unscoped().Model(m)
		if _, isUser := m.(*User); isUser {
			q = q.Where("id = ?", u.ID)
		} else {
			q = q.Where("user_id = ?", u.ID)
		}
		q.Count(&n)
		if n != 0 {
			t.Errorf("%T rows left: %d", m, n)
		}
	}
	if len(purger.purged) != 1 || purger.purged[0] != u.ID || purger.after != 1 {
		t.Fatalf("purger calls: %+v", purger)
	}
	if e.countEvents("account_delete", "success") != 1 || e.countEvents("account_delete_storage_cleanup", "failure") != 1 {
		t.Fatalf("events missing")
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 401, "auth.session_invalid")
}

func TestAccountDeletionWithEmailCode(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	u, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	w := e.request("POST", "/me/delete/start", tok, nil)
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != true || m["verificationMethod"] != "email" {
		t.Fatalf("start: %v", m)
	}
	if got := e.Mail.sent[len(e.Mail.sent)-1]; !contains(got.Text, "Confirm account deletion") {
		t.Fatalf("deletion mail uses its own template: %q", got.Text)
	}
	code := e.Mail.lastCode(t, "alice@example.com")
	testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "email", "code": code}), 200, "")
	var n int64
	e.DB.Unscoped().Model(&User{}).Where("id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("user row must be hard deleted")
	}
}

func TestAccountDeletionVetoedByPurger(t *testing.T) {
	purger := &recordingPurger{veto: ErrOwnerFileReference}
	e := newEnv(t, withHooks(func(h *Hooks) { h.Purgers = []AccountPurger{purger} }))
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	secret, _ := e.enableTOTP(tok)
	testkit.Expect(t, e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "two_factor", "code": e.totp(secret)}), 409, "knowledge_base.owner_file_reference")
	var n int64
	e.DB.Model(&User{}).Where("id = ?", u.ID).Count(&n)
	if n != 1 || len(purger.purged) != 0 {
		t.Fatalf("nothing may be deleted on a veto")
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 200, "")
}

func TestAccountDeletionRollsBackWhenPurgeFails(t *testing.T) {
	e := newEnv(t, withHooks(func(h *Hooks) { h.Purgers = []AccountPurger{failingPurger{}} }))
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	secret, _ := e.enableTOTP(tok)
	w := e.request("DELETE", "/me", tok, map[string]string{"verificationMethod": "two_factor", "code": e.totp(secret)})
	testkit.Expect(t, w, 500, "internal.error")
	var n int64
	e.DB.Model(&User{}).Where("id = ?", u.ID).Count(&n)
	var cred int64
	e.DB.Model(&Credential{}).Where("user_id = ?", u.ID).Count(&cred)
	if n != 1 || cred != 1 {
		t.Fatalf("deletion must be atomic: user=%d cred=%d", n, cred)
	}
}

type failingPurger struct{}

func (failingPurger) BeforeDelete(context.Context, uint) error { return nil }
func (failingPurger) Purge(context.Context, *gorm.DB, uint) error {
	return errors.New("table is on fire")
}
func (failingPurger) AfterCommit(context.Context, uint) (int, []string) { return 0, nil }

func (e *testEnv) seedLink(userID uint) {
	e.t.Helper()
	p := Provider{PublicID: newPublicID(), Type: "oauth2", Name: "X", Slug: "x-" + randomHex(3), LoginEnabled: true, RegistrationEnabled: true}
	if err := e.DB.Create(&p).Error; err != nil {
		e.t.Fatal(err)
	}
	l := UserLink{UserID: userID, ProviderID: p.ID, ProviderSubject: "sub-" + randomHex(3), ProviderType: "oauth2", LinkedAt: e.Clock.Now()}
	if err := e.DB.Create(&l).Error; err != nil {
		e.t.Fatal(err)
	}
}
