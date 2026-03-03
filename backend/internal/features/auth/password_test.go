package auth

import (
	"regexp"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func smtpReady(s *Settings) {
	s.EmailVerificationEnabled = true
	s.PasswordResetEnabled = true
	s.SMTPHost, s.SMTPPort, s.SMTPUsername, s.SMTPPassword = "smtp.example.com", 587, "bot", "pw"
}

func (e *testEnv) resetStart(email string) map[string]any {
	e.t.Helper()
	w := e.request("POST", "/auth/password/reset/start", "", map[string]string{"email": email})
	testkit.Expect(e.t, w, 200, "")
	return dataMap(e.t, w)
}

func TestPasswordResetFlow(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(smtpReady)
	u := e.seedUser(userSpec{Username: "alice", Password: "old-Passw0rd", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	_, access := e.session(userSpec{Username: "bob", Email: "bob@example.com", EmailVerified: true})
	_ = access
	d, _ := e.signIn("alice", "old-Passw0rd")

	// unknown address: indistinguishable answer, no mail
	if m := e.resetStart("nobody@example.com"); m["sent"] != true || m["expiresAt"] == "" {
		t.Fatalf("unknown: %v", m)
	}
	if e.Mail.count() != 0 {
		t.Fatalf("no mail for unknown address")
	}
	if e.countEvents("password_reset_code", "failure") != 1 {
		t.Fatalf("target-unavailable failure event expected")
	}

	m := e.resetStart("alice@example.com")
	if m["sent"] != true || e.Mail.count() != 1 {
		t.Fatalf("known address: %v mails=%d", m, e.Mail.count())
	}
	if got := e.Mail.sent[0]; got.To != "alice@example.com" || !contains(got.Text, "Reset your password") {
		t.Fatalf("mail: %+v", got)
	}
	code := e.Mail.lastCode(t, "alice@example.com")

	// repeated start inside the cooldown answers 200 without a second mail
	m2 := e.resetStart("alice@example.com")
	if e.Mail.count() != 1 || m2["expiresAt"] != m["expiresAt"] {
		t.Fatalf("cooldown: mails=%d %v vs %v", e.Mail.count(), m2, m)
	}

	// wrong code and short password
	w := e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "alice@example.com", "code": "000000", "newPassword": "New-Passw0rd"})
	if code == "000000" {
		t.Skip("improbable code collision")
	}
	testkit.Expect(t, w, 400, "auth.password_reset_failed")
	testkit.Expect(t, e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "alice@example.com", "code": code, "newPassword": "12345678"}), 400, "auth.invalid_password")

	w = e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "alice@example.com", "code": code, "newPassword": "New-Passw0rd"})
	testkit.Expect(t, w, 200, "")
	if dataMap(t, w)["changed"] != true {
		t.Fatalf("body: %s", w.Body)
	}
	// every session is gone, the old password no longer works
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 401, "auth.session_invalid")
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "old-Passw0rd"}), 401, "auth.invalid_credentials")
	e.signIn("alice", "New-Passw0rd")
	var sess Session
	e.DB.Where("user_id = ? AND revoked_at IS NOT NULL", u.ID).First(&sess)
	if sess.RevokeReason != "password reset" {
		t.Fatalf("revoke reason %q", sess.RevokeReason)
	}
	// the code is single use
	testkit.Expect(t, e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "alice@example.com", "code": code, "newPassword": "Third-Passw0rd"}), 400, "auth.password_reset_failed")
}

func TestPasswordResetEligibility(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "old-Passw0rd", Email: "alice@example.com", EmailVerified: true})
	e.seedUser(userSpec{Username: "unver", Password: "old-Passw0rd", Email: "unver@example.com"})
	e.seedUser(userSpec{Username: "sso", Email: "sso@example.com", EmailVerified: true})
	e.seedUser(userSpec{Username: "susp", Password: "old-Passw0rd", Email: "susp@example.com", EmailVerified: true, Status: StatusSuspended})
	e.seedUser(userSpec{Username: "lock", Password: "old-Passw0rd", Email: "lock@example.com", EmailVerified: true, Status: StatusLocked})

	// feature off: nothing is sent even for a perfect target
	e.resetStart("alice@example.com")
	if e.Mail.count() != 0 {
		t.Fatalf("reset disabled must not send")
	}
	e.Settings.set(smtpReady)
	for _, addr := range []string{"unver@example.com", "sso@example.com", "susp@example.com"} {
		e.resetStart(addr)
	}
	if e.Mail.count() != 0 {
		t.Fatalf("ineligible accounts must not receive mail")
	}
	e.resetStart("lock@example.com")
	if e.Mail.count() != 1 {
		t.Fatalf("locked accounts may reset")
	}
	// an administrator-locked user is released by completing the reset
	code := e.Mail.lastCode(t, "lock@example.com")
	testkit.Expect(t, e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "lock@example.com", "code": code, "newPassword": "New-Passw0rd"}), 200, "")
	var u User
	e.DB.Where("username = ?", "lock").First(&u)
	if u.Status != StatusActive {
		t.Fatalf("status %s", u.Status)
	}
	e.signIn("lock", "New-Passw0rd")
}

func TestPasswordResetSendFailure(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(smtpReady)
	e.seedUser(userSpec{Username: "alice", Password: "old-Passw0rd", Email: "alice@example.com", EmailVerified: true})
	e.Mail.fail = ErrSMTPInvalid
	testkit.Expect(t, e.request("POST", "/auth/password/reset/start", "", map[string]string{"email": "alice@example.com"}), 500, "auth.password_reset_failed")
	e.Mail.fail = nil
	// the failed record was canceled: retry works at once
	e.resetStart("alice@example.com")
	if e.Mail.count() != 1 {
		t.Fatalf("retry must send")
	}
}

func TestPasswordResetClearsLockout(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(smtpReady)
	e.seedUser(userSpec{Username: "alice", Password: "old-Passw0rd", Email: "alice@example.com", EmailVerified: true})
	for i := 0; i < 5; i++ {
		e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "wrong-pass"})
	}
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "old-Passw0rd"}), 423, "auth.account_locked")
	e.resetStart("alice@example.com")
	code := e.Mail.lastCode(t, "alice@example.com")
	testkit.Expect(t, e.request("POST", "/auth/password/reset/complete", "", map[string]string{"email": "alice@example.com", "code": code, "newPassword": "New-Passw0rd"}), 200, "")
	e.signIn("alice", "New-Passw0rd")
}

// ---- change password ---------------------------------------------------------------

func TestPasswordChangeWithoutExtraVerification(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Password: "old-Passw0rd", Onboarded: true})
	d2, w2 := e.signIn("alice", "old-Passw0rd")
	_ = d2
	_ = w2

	w := e.request("POST", "/auth/password/change/start", tok, nil)
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["sent"] != false || m["verificationMethod"] != "none" {
		t.Fatalf("start: %v", m)
	}

	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, map[string]string{"currentPassword": "nope-nope", "newPassword": "newPassw0rd"}), 401, "auth.invalid_current_password")
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, map[string]string{"currentPassword": "old-Passw0rd", "newPassword": "12345678"}), 400, "auth.invalid_password")
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, map[string]string{"currentPassword": "old-Passw0rd", "newPassword": "short"}), 400, "request.invalid_body")

	w = e.request("POST", "/auth/password/change/complete", tok, map[string]string{"currentPassword": "old-Passw0rd", "newPassword": "newPassw0rd"})
	testkit.Expect(t, w, 200, "")
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie must be cleared: %v", c)
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 401, "auth.session_invalid")
	var live int64
	e.DB.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", u.ID).Count(&live)
	if live != 0 {
		t.Fatalf("every session must be revoked, %d live", live)
	}
	e.signIn("alice", "newPassw0rd")
	if c := e.credential(u.ID); c.PasswordOrigin != PasswordOriginUserSet || c.MustResetPassword {
		t.Fatalf("credential %+v", c)
	}
}

func TestPasswordChangeWithEmailVerification(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	_, tok := e.session(userSpec{Username: "alice", Password: "old-Passw0rd", Email: "alice@example.com", EmailVerified: true, Onboarded: true})

	w := e.request("POST", "/auth/password/change/start", tok, nil)
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["sent"] != true || m["verificationMethod"] != "email" {
		t.Fatalf("start: %v", m)
	}
	testkit.Expect(t, e.request("POST", "/auth/password/change/start", tok, nil), 400, "auth.verification_code_recent")
	testkit.Expect(t, e.request("POST", "/auth/password/change/start", tok, map[string]string{"verificationMethod": "two_factor"}), 400, "auth.verification_method_unavailable")
	code := e.Mail.lastCode(t, "alice@example.com")

	body := map[string]string{"currentPassword": "old-Passw0rd", "newPassword": "newPassw0rd", "verificationMethod": "email", "code": "000000"}
	if code != "000000" {
		testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, body), 400, "auth.verification_code_invalid")
	}
	body["code"] = code
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, body), 200, "")
	e.signIn("alice", "newPassw0rd")
}

func TestPasswordChangeWithTwoFactor(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Password: "old-Passw0rd", Onboarded: true})
	secret, _ := e.enableTOTP(tok)

	w := e.request("POST", "/auth/password/change/start", tok, nil)
	m := dataMap(t, w)
	if m["sent"] != false || m["verificationMethod"] != "two_factor" {
		t.Fatalf("start: %v", m)
	}
	body := map[string]string{"currentPassword": "old-Passw0rd", "newPassword": "newPassw0rd", "code": "000000"}
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, body), 400, "auth.verification_code_invalid")
	body["code"] = e.totp(secret)
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, body), 200, "")
}

func TestPasswordChangeForPasswordlessAccountCreatesPassword(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	e.DB.Model(&Credential{}).Where("user_id = ?", u.ID).Update("password_enabled", false)
	testkit.Expect(t, e.request("POST", "/auth/password/change/complete", tok, map[string]string{"newPassword": "brandNewPw1"}), 200, "")
	e.signIn("alice", "brandNewPw1")
	if !e.credential(u.ID).PasswordEnabled {
		t.Fatalf("password must be enabled afterwards")
	}
}

// ---- bootstrap administrator and onboarding ------------------------------------------------

var bootstrapLog = regexp.MustCompile(`bootstrap_admin_created.*?password[=:"\s]+([A-Za-z0-9_-]{20,})`)

// bootstrapPassword digs the one-time generated password out of the log.
func (e *testEnv) bootstrapPassword(t *testing.T) string {
	t.Helper()
	m := bootstrapLog.FindStringSubmatch(e.Logs.String())
	if m == nil {
		t.Fatalf("bootstrap password not logged:\n%s", e.Logs)
	}
	return m[1]
}

func TestBootstrapAdministratorOnboarding(t *testing.T) {
	e := newEnv(t)
	var admin User
	if err := e.DB.Where("role = ?", RoleSuperAdmin).First(&admin).Error; err != nil {
		t.Fatalf("bootstrap admin missing: %v", err)
	}
	if admin.Username != "admin" || admin.DisplayName != "System Admin" || admin.Email != "" {
		t.Fatalf("admin: %+v", admin)
	}
	cred := e.credential(admin.ID)
	if cred.PasswordOrigin != PasswordOriginAdminCreated || !cred.MustResetPassword {
		t.Fatalf("credential: %+v", cred)
	}
	pw := e.bootstrapPassword(t)
	d, _ := e.signIn("admin", pw)
	if d.User["mustResetPassword"] != true || d.User["initialUsernameRequired"] != true || d.User["initialSecurityRequired"] != true {
		t.Fatalf("view: %v", d.User)
	}
	tok := d.AccessToken

	// no username change yet
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, nil), 400, "auth.username_change_required")
	// the same name does not count as a change
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "admin"}), 400, "user.invalid_username")
	testkit.Expect(t, e.request("PATCH", "/me/username", tok, map[string]string{"username": "boss-man"}), 200, "")

	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, nil), 400, "auth.invalid_password")
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, map[string]string{"newPassword": pw}), 400, "auth.password_reuse_not_allowed")
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, map[string]string{"newPassword": "12345678"}), 400, "auth.invalid_password")

	w := e.request("POST", "/me/onboarding/complete", tok, map[string]string{"newPassword": "My-new-secret-1"})
	testkit.Expect(t, w, 200, "")
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie must be cleared")
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 401, "auth.session_invalid")

	d, _ = e.signIn("boss-man", "My-new-secret-1")
	if d.User["mustResetPassword"] != false || d.User["initialSecurityRequired"] != false || d.User["onboardingCompletedAt"] == nil {
		t.Fatalf("view after onboarding: %v", d.User)
	}
}

func TestOnboardingForOrdinaryUserNeedsNoPassword(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice"})
	w := e.request("POST", "/me/onboarding/complete", tok, nil)
	testkit.Expect(t, w, 200, "")
	user := dataMap(t, w)["user"].(map[string]any)
	if user["onboardingCompletedAt"] == nil || user["initialSecurityRequired"] != false {
		t.Fatalf("user: %v", user)
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 200, "") // sessions survive
	if e.reloadUser(u.ID).OnboardingCompletedAt == nil {
		t.Fatalf("stamp missing")
	}
	// an explicit short password is a validation error even when unused
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, map[string]string{"newPassword": "short"}), 400, "request.invalid_body")
}

func TestOnboardingAfterAdminForcedReset(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Password: "temp-Passw0rd"})
	e.DB.Model(&Credential{}).Where("user_id = ?", u.ID).Updates(map[string]any{"must_reset_password": true, "password_origin": PasswordOriginAdminReset})
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, nil), 400, "auth.invalid_password")
	// for a non-bootstrap account reusing the old password is allowed
	testkit.Expect(t, e.request("POST", "/me/onboarding/complete", tok, map[string]string{"newPassword": "fresh-Passw0rd"}), 200, "")
	// change flow for an account in the initial-reset state needs no current password
}

func TestBootstrapIsIdempotent(t *testing.T) {
	e := newEnv(t)
	var before, after int64
	e.DB.Model(&User{}).Where("role = ?", RoleSuperAdmin).Count(&before)
	if err := e.Svc.BootstrapAdmin(e.Svc.ctxForTest()); err != nil {
		t.Fatal(err)
	}
	e.DB.Model(&User{}).Where("role = ?", RoleSuperAdmin).Count(&after)
	if before != 1 || after != 1 {
		t.Fatalf("superadmins: %d -> %d", before, after)
	}
	// an untouched bootstrap account is pushed back into forced reset
	e.DB.Model(&Credential{}).Where("password_origin = ?", PasswordOriginAdminCreated).Update("must_reset_password", false)
	if err := e.Svc.BootstrapAdmin(e.Svc.ctxForTest()); err != nil {
		t.Fatal(err)
	}
	var c Credential
	e.DB.Where("password_origin = ?", PasswordOriginAdminCreated).First(&c)
	if !c.MustResetPassword {
		t.Fatalf("must-reset should be restored")
	}
}

func TestSessionTimeIsDrivenByClock(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	e.Clock.Advance(25 * time.Hour)
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 401, "auth.invalid_token")
}
