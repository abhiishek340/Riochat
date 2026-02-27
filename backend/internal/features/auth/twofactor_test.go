package auth

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// enableTOTP walks the setup flow for a signed-in user and returns the secret
// and recovery codes.
func (e *testEnv) enableTOTP(token string) (secret string, recovery []string) {
	e.t.Helper()
	w := e.request("POST", "/me/2fa/setup/start", token, nil)
	testkit.Expect(e.t, w, 200, "")
	secret = dataMap(e.t, w)["secret"].(string)
	code, err := totpAt(secret, e.Clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	w = e.request("POST", "/me/2fa/setup/confirm", token, map[string]string{"code": code})
	testkit.Expect(e.t, w, 200, "")
	var out struct {
		RecoveryCodes []string
	}
	testkit.Decode(e.t, w, &out)
	return secret, out.RecoveryCodes
}

func (e *testEnv) totp(secret string) string {
	c, err := totpAt(secret, e.Clock.Now())
	if err != nil {
		e.t.Fatal(err)
	}
	return c
}

var recoveryShape = regexp.MustCompile(`^[A-Z0-9+/]{4}-[A-Z0-9+/]{4}-[A-Z0-9+/]{4}$`)

func TestTwoFactorSetupFlow(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Email: "alice@example.com", Onboarded: true})

	w := e.request("GET", "/me/2fa", tok, nil)
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["available"] != true || m["totpEnabled"] != false || m["recoveryCount"] != float64(0) || m["required"] != false || m["enabledAt"] != nil {
		t.Fatalf("status: %v", m)
	}

	w1 := e.request("POST", "/me/2fa/setup/start", tok, nil)
	testkit.Expect(t, w1, 200, "")
	m1 := dataMap(t, w1)
	secret := m1["secret"].(string)
	if len(secret) != 32 || strings.Contains(secret, "=") {
		t.Fatalf("secret should be 20 bytes of unpadded base32: %q", secret)
	}
	want := "otpauth://totp/Riochat:alice@example.com?algorithm=SHA1&digits=6&issuer=Riochat&period=30&secret=" + secret
	if m1["otpauthURL"] != want {
		t.Fatalf("otpauth URL %v\nwant %s", m1["otpauthURL"], want)
	}
	// repeated start inside 30 minutes keeps the secret
	e.Clock.Advance(10 * time.Minute)
	w2 := e.request("POST", "/me/2fa/setup/start", tok, nil)
	m2 := dataMap(t, w2)
	if m2["secret"] != secret || m2["expiresAt"] != m1["expiresAt"] {
		t.Fatalf("pending setup must be reused: %v vs %v", m2, m1)
	}
	var row MFASettings
	e.DB.Where("user_id = ?", u.ID).First(&row)
	if strings.Contains(row.TOTPSecretEncrypted, secret) || !strings.HasPrefix(row.TOTPSecretEncrypted, "v1:") {
		t.Fatalf("secret must be stored encrypted: %q", row.TOTPSecretEncrypted)
	}

	// wrong code
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/confirm", tok, map[string]string{"code": "000000"}), 401, "auth.invalid_two_factor_code")
	// correct code
	w = e.request("POST", "/me/2fa/setup/confirm", tok, map[string]string{"code": e.totp(secret)})
	testkit.Expect(t, w, 200, "")
	var out struct {
		RecoveryCodes []string
		Status        map[string]any
	}
	testkit.Decode(t, w, &out)
	if len(out.RecoveryCodes) != 10 || out.Status["totpEnabled"] != true || out.Status["recoveryCount"] != float64(10) || out.Status["enabledAt"] == nil {
		t.Fatalf("confirm: %s", w.Body)
	}
	seen := map[string]bool{}
	for _, c := range out.RecoveryCodes {
		if !recoveryShape.MatchString(c) || seen[c] {
			t.Fatalf("bad or repeated recovery code %q", c)
		}
		seen[c] = true
	}
	var stored MFASettings
	e.DB.Where("user_id = ?", u.ID).First(&stored)
	if strings.Contains(stored.RecoveryCodesHash, out.RecoveryCodes[0]) || stored.TOTPSetupExpiresAt != nil {
		t.Fatalf("recovery codes must be hashed and the setup expiry cleared")
	}

	// starting again is refused, confirming again is idempotent without codes
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/start", tok, nil), 400, "auth.two_factor_already_enabled")
	w = e.request("POST", "/me/2fa/setup/confirm", tok, map[string]string{"code": e.totp(secret)})
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"recoveryCodes":null`) {
		t.Fatalf("already enabled confirm returns null recovery codes: %s", w.Body)
	}
}

func TestTwoFactorSetupEdges(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/confirm", tok, map[string]string{"code": "123456"}), 400, "auth.two_factor_not_started")

	w := e.request("POST", "/me/2fa/setup/start", tok, nil)
	secret := dataMap(t, w)["secret"].(string)
	// cancel clears the pending setup, twice is fine
	w = e.request("DELETE", "/me/2fa/setup", tok, nil)
	testkit.Expect(t, w, 200, "")
	testkit.Expect(t, e.request("DELETE", "/me/2fa/setup", tok, nil), 200, "")
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/confirm", tok, map[string]string{"code": e.totp(secret)}), 400, "auth.two_factor_not_started")

	// an expired setup is removed and reported
	e.request("POST", "/me/2fa/setup/start", tok, nil)
	e.Clock.Advance(31 * time.Minute)
	tok2 := func() string { d, _ := e.signIn("alice", "correct-horse-9"); return d.AccessToken }()
	w = e.request("POST", "/me/2fa/setup/confirm", tok2, map[string]string{"code": "123456"})
	testkit.Expect(t, w, 400, "auth.two_factor_expired")
	if testkit.Envelope(t, w).ErrorMsg != "two factor setup expired" {
		t.Fatalf("message: %s", w.Body)
	}
	var n int64
	e.DB.Model(&MFASettings{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 0 {
		t.Fatalf("expired setup must be deleted")
	}
	// a fresh start after expiry yields a new secret
	w = e.request("POST", "/me/2fa/setup/start", tok2, nil)
	if dataMap(t, w)["secret"] == secret {
		t.Fatalf("new secret expected")
	}
	// codes must be 6-32 long
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/confirm", tok2, map[string]string{"code": "123"}), 400, "request.invalid_body")
}

func TestTwoFactorLoginChallenge(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com", Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	secret, _ := e.enableTOTP(d0.AccessToken)

	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 200, "")
	ch := decodeLogin(t, w)
	if !ch.TwoFactorRequired || ch.TwoFactorChallengeToken == "" || ch.AccessToken != "" || ch.SessionID != "" ||
		len(ch.VerificationMethods) != 1 || ch.VerificationMethods[0] != "two_factor" || ch.RefreshToken != "" {
		t.Fatalf("challenge body: %+v", ch)
	}
	if refreshCookie(w) != nil {
		t.Fatalf("no cookie for a challenge")
	}
	if ch.User["username"] != "alice" {
		t.Fatalf("challenge carries the user")
	}
	var sessions int64
	e.DB.Model(&Session{}).Count(&sessions)
	if sessions != 1 {
		t.Fatalf("a challenge must not create a session")
	}
	if e.countEvents("login", "challenge") != 1 {
		t.Fatalf("challenge event missing")
	}
	// the challenge is not usable as a bearer token
	w2 := e.request("GET", "/me", ch.TwoFactorChallengeToken, nil)
	testkit.Expect(t, w2, 401, "auth.invalid_token")
	if testkit.Envelope(t, w2).ErrorMsg != "invalid token type" {
		t.Fatalf("message: %s", w2.Body)
	}
	// ...nor as a refresh token
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", refreshCookieName+"="+ch.TwoFactorChallengeToken), 401, "auth.invalid_refresh_token")

	// wrong code
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": "000000"}), 401, "auth.invalid_two_factor_code")
	// right code
	w = e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": e.totp(secret)})
	testkit.Expect(t, w, 200, "")
	done := decodeLogin(t, w)
	if done.AccessToken == "" || done.SessionID == "" || refreshCookie(w) == nil {
		t.Fatalf("verify should sign in: %+v", done)
	}
	testkit.Expect(t, e.request("GET", "/me", done.AccessToken, nil), 200, "")
}

func TestTwoFactorChallengeExpiry(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	secret, _ := e.enableTOTP(d0.AccessToken)
	ch := decodeLogin(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}))
	e.Clock.Advance(6 * time.Minute)
	w := e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": e.totp(secret)})
	testkit.Expect(t, w, 401, "auth.two_factor_expired")
	testkit.Expect(t, e.request("POST", "/auth/2fa/email/start", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken}), 401, "auth.two_factor_expired")
}

func TestTwoFactorInvalidChallenges(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	// an access token is not a challenge
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": tok, "code": "123456"}), 401, "auth.invalid_two_factor_code")
	testkit.Expect(t, e.request("POST", "/auth/2fa/email/start", "", map[string]string{"challengeToken": tok}), 401, "request.invalid_two_factor_challenge")
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": strings.Repeat("x", 30), "code": "123456"}), 401, "auth.invalid_two_factor_code")
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": "short", "code": "123456"}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": tok, "code": "123456", "verificationMethod": "sms"}), 400, "request.invalid_body")
}

func TestTwoFactorFailuresCountTowardLockout(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	e.enableTOTP(d0.AccessToken)
	ch := decodeLogin(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}))
	body := map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": "000000"}
	for i := 0; i < 4; i++ {
		testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", body), 401, "auth.invalid_two_factor_code")
	}
	w := e.request("POST", "/auth/2fa/verify", "", body)
	testkit.Expect(t, w, 423, "auth.account_locked")
	if w.Header().Get("Retry-After") != "900" {
		t.Fatalf("Retry-After %q", w.Header().Get("Retry-After"))
	}
	if e.reloadUser(u.ID).Status != StatusLocked {
		t.Fatalf("account should be locked")
	}
	// the challenge is useless once the account is not active
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", body), 401, "auth.invalid_two_factor_code")
}

func TestTwoFactorMethodNotAvailableCountsAsFailure(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	e.enableTOTP(d0.AccessToken)
	ch := decodeLogin(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}))
	w := e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": "123456", "verificationMethod": "email"})
	testkit.Expect(t, w, 401, "auth.invalid_two_factor_code")
	if e.credential(u.ID).FailedLoginCount != 1 {
		t.Fatalf("unavailable method counts as a failed attempt")
	}
	testkit.Expect(t, e.request("POST", "/auth/2fa/email/start", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken}), 400, "auth.verification_method_unavailable")
}

func TestTwoFactorEmailMethod(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(func(s *Settings) { s.EmailVerificationEnabled = true })
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com", EmailVerified: true, Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	e.enableTOTP(d0.AccessToken)
	ch := decodeLogin(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}))
	if len(ch.VerificationMethods) != 2 || ch.VerificationMethods[0] != "two_factor" || ch.VerificationMethods[1] != "email" {
		t.Fatalf("methods: %v", ch.VerificationMethods)
	}
	w := e.request("POST", "/auth/2fa/email/start", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken})
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["sent"] != true || m["verificationMethod"] != "email" {
		t.Fatalf("start: %v", m)
	}
	testkit.Expect(t, e.request("POST", "/auth/2fa/email/start", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken}), 400, "auth.verification_code_recent")
	code := e.Mail.lastCode(t, "alice@example.com")
	w = e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "verificationMethod": "email", "code": code})
	testkit.Expect(t, w, 200, "")
	if decodeLogin(t, w).AccessToken == "" {
		t.Fatalf("email code should sign in")
	}
	// the code is single use
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "verificationMethod": "email", "code": code}), 401, "auth.invalid_two_factor_code")
}

func TestRecoveryCodes(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d0, _ := e.signIn("alice", "correct-horse-9")
	secret, codes := e.enableTOTP(d0.AccessToken)

	login := func() loginData {
		return decodeLogin(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}))
	}
	ch := login()
	w := e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": strings.ToLower(codes[0])})
	testkit.Expect(t, w, 200, "") // case and dashes are not significant
	// reuse fails
	ch = login()
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": codes[0]}), 401, "auth.invalid_two_factor_code")
	w = e.request("GET", "/me/2fa", d0.AccessToken, nil)
	if dataMap(t, w)["recoveryCount"] != float64(9) {
		t.Fatalf("recovery count should drop to 9: %s", w.Body)
	}
	// dashes may be omitted
	ch = login()
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": strings.ReplaceAll(codes[1], "-", "")}), 200, "")

	// regenerate: needs a valid code, replaces everything
	testkit.Expect(t, e.request("POST", "/me/2fa/recovery/regenerate", d0.AccessToken, map[string]string{"code": "000000"}), 401, "auth.invalid_two_factor_code")
	w = e.request("POST", "/me/2fa/recovery/regenerate", d0.AccessToken, map[string]string{"code": e.totp(secret)})
	testkit.Expect(t, w, 200, "")
	var out struct{ RecoveryCodes []string }
	testkit.Decode(t, w, &out)
	if len(out.RecoveryCodes) != 10 {
		t.Fatalf("regenerate: %s", w.Body)
	}
	ch = login()
	testkit.Expect(t, e.request("POST", "/auth/2fa/verify", "", map[string]string{"challengeToken": ch.TwoFactorChallengeToken, "code": codes[2]}), 401, "auth.invalid_two_factor_code")

	// recovery codes work for disable too
	w = e.request("POST", "/me/2fa/disable", d0.AccessToken, map[string]string{"code": out.RecoveryCodes[0]})
	testkit.Expect(t, w, 200, "")
	if dataMap(t, w)["disabled"] != true {
		t.Fatalf("disable body")
	}
	w = e.request("GET", "/me/2fa", d0.AccessToken, nil)
	if dataMap(t, w)["totpEnabled"] != false {
		t.Fatalf("2fa should be off: %s", w.Body)
	}
	e.signIn("alice", "correct-horse-9") // no challenge any more
}

func TestDisableTwoFactorValidation(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	// nothing to disable: not a success
	testkit.Expect(t, e.request("POST", "/me/2fa/disable", tok, map[string]string{"code": "123456"}), 400, "auth.two_factor_not_started")
	testkit.Expect(t, e.request("POST", "/me/2fa/recovery/regenerate", tok, map[string]string{"code": "123456"}), 400, "auth.two_factor_not_started")
	// a pending setup is not an enabled factor: any code is rejected, nothing is removed
	testkit.Expect(t, e.request("POST", "/me/2fa/setup/start", tok, nil), 200, "")
	testkit.Expect(t, e.request("POST", "/me/2fa/disable", tok, map[string]string{"code": "123456"}), 401, "auth.invalid_two_factor_code")
	secret, _ := e.enableTOTP(tok)
	testkit.Expect(t, e.request("POST", "/me/2fa/disable", tok, map[string]string{"code": "111111"}), 401, "auth.invalid_two_factor_code")
	testkit.Expect(t, e.request("POST", "/me/2fa/disable", tok, map[string]string{"code": e.totp(secret)}), 200, "")
}

func TestRecoveryCodeConsumeIsSingleUseUnderRace(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	_, codes := e.enableTOTP(tok)
	m := e.credentialMFA(u.ID)
	ok1, err1 := e.Svc.verifySecondFactor(e.Svc.ctxForTest(), m, codes[0])
	// the second attempt works from the stale snapshot, as a concurrent request would
	ok2, err2 := e.Svc.verifySecondFactor(e.Svc.ctxForTest(), m, codes[0])
	if err1 != nil || err2 != nil || !ok1 || ok2 {
		t.Fatalf("compare-and-swap should let exactly one use win: %v %v %v %v", ok1, err1, ok2, err2)
	}
}

func (e *testEnv) credentialMFA(userID uint) *MFASettings {
	var m MFASettings
	if err := e.DB.Where("user_id = ?", userID).First(&m).Error; err != nil {
		e.t.Fatal(err)
	}
	return &m
}
