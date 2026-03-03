package auth

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func verificationOn(s *Settings) { s.EmailVerificationEnabled = true }

func (e *testEnv) registerStart(email string) *httptest.ResponseRecorder {
	return e.request("POST", "/auth/register/email/start", "", map[string]string{"email": email})
}

func (e *testEnv) registerComplete(email, password, code string) *httptest.ResponseRecorder {
	body := map[string]string{"email": email, "password": password}
	if code != "" {
		body["code"] = code
	}
	return e.request("POST", "/auth/register/email/complete", "", body)
}

func TestRegistrationWithEmailVerification(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)

	w := e.registerStart("new@example.com")
	testkit.Expect(t, w, 200, "")
	if m := dataMap(t, w); m["sent"] != true || m["expiresAt"] == "" {
		t.Fatalf("start body: %v", m)
	}
	if e.Mail.count() != 1 {
		t.Fatalf("one mail expected, got %d", e.Mail.count())
	}
	code := e.Mail.lastCode(t, "new@example.com")
	if len(code) != 6 {
		t.Fatalf("code %q", code)
	}
	msg := e.Mail.sent[0]
	if msg.Subject != "Your Riochat verification code" || !strings.Contains(msg.Text, "Create your account") || !strings.Contains(msg.HTML, code) {
		t.Fatalf("mail: %+v", msg)
	}

	// the stored record never holds the plaintext code
	var rec ContactVerification
	if err := e.DB.Where("purpose = ?", PurposeRegister).First(&rec).Error; err != nil {
		t.Fatal(err)
	}
	if rec.UserID != 0 || rec.Target != "new@example.com" || rec.Status != "pending" || strings.Contains(rec.CodeHash, code) || rec.CodeHash == "" {
		t.Fatalf("record: %+v", rec)
	}

	// cooldown
	testkit.Expect(t, e.registerStart("new@example.com"), 400, "auth.verification_code_recent")

	w = e.registerComplete("new@example.com", "Passw0rdA", code)
	testkit.Expect(t, w, 200, "")
	d := decodeLogin(t, w)
	if d.AccessToken == "" || refreshCookie(w) == nil {
		t.Fatalf("registration must sign the user in: %+v", d)
	}
	if d.User["emailVerifiedAt"] == nil || d.User["onboardingCompletedAt"] != nil || d.User["initialSecurityRequired"] != true {
		t.Fatalf("user view: %v", d.User)
	}
	if d.User["email"] != "new@example.com" || d.User["role"] != "user" || d.User["emailSource"] != "local_register" {
		t.Fatalf("user view: %v", d.User)
	}
	if name := d.User["username"].(string); len(name) > 16 || !strings.HasPrefix(name, "new-") {
		t.Fatalf("generated username %q", name)
	}
	var u User
	e.DB.Where("email = ?", "new@example.com").First(&u)
	cred := e.credential(u.ID)
	if cred.PasswordOrigin != PasswordOriginLocalRegister || !cred.PasswordEnabled || !strings.HasPrefix(cred.PasswordHash, "$2") {
		t.Fatalf("credential: %+v", cred)
	}
	e.DB.First(&rec, rec.ID)
	if rec.Status != "verified" || rec.VerifiedAt == nil || rec.ConsumedAt == nil {
		t.Fatalf("record after use: %+v", rec)
	}

	// the same address cannot register again
	testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", code), 400, "auth.email_already_exists")
	testkit.Expect(t, e.registerStart("new@example.com"), 400, "auth.email_already_exists")
}

func TestRegistrationCodeAttemptsAreLimited(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")
	real := e.Mail.lastCode(t, "new@example.com")
	wrong := "000000"
	if real == wrong {
		wrong = "111111"
	}
	for i := 0; i < 5; i++ {
		testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", wrong), 400, "auth.verification_code_invalid")
	}
	// even the right code is refused once five guesses were wasted
	testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", real), 400, "auth.verification_code_attempts_exceeded")
}

func TestRegistrationNeedsAPendingCode(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	testkit.Expect(t, e.registerComplete("ghost@example.com", "Passw0rdA", "123456"), 400, "auth.verification_code_invalid")

	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")
	code := e.Mail.lastCode(t, "new@example.com")
	e.Clock.Advance(11 * time.Minute)
	testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", code), 400, "auth.verification_code_invalid")

	// a code for one address never works for another
	testkit.Expect(t, e.registerStart("a@example.com"), 200, "")
	codeA := e.Mail.lastCode(t, "a@example.com")
	testkit.Expect(t, e.registerComplete("b@example.com", "Passw0rdA", codeA), 400, "auth.verification_code_invalid")
}

func TestRegistrationResendCancelsEarlierCode(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")
	first := e.Mail.lastCode(t, "new@example.com")
	e.Clock.Advance(61 * time.Second)
	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")
	var canceled, pending int64
	e.DB.Model(&ContactVerification{}).Where("status = ?", "canceled").Count(&canceled)
	e.DB.Model(&ContactVerification{}).Where("status = ?", "pending").Count(&pending)
	if canceled != 1 || pending != 1 {
		t.Fatalf("canceled=%d pending=%d", canceled, pending)
	}
	second := e.Mail.lastCode(t, "new@example.com")
	if first != second {
		testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", first), 400, "auth.verification_code_invalid")
	}
	testkit.Expect(t, e.registerComplete("new@example.com", "Passw0rdA", second), 200, "")
}

func TestRegistrationMailFailureAllowsImmediateRetry(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	e.Mail.fail = ErrSMTPInvalid
	testkit.Expect(t, e.registerStart("new@example.com"), 400, "settings.smtp_invalid")
	var pending int64
	e.DB.Model(&ContactVerification{}).Where("status = ?", "pending").Count(&pending)
	if pending != 0 {
		t.Fatalf("a failed send must not leave a pending record")
	}
	e.Mail.fail = nil
	testkit.Expect(t, e.registerStart("new@example.com"), 200, "")

	e.Clock.Advance(2 * time.Minute)
	e.Mail.fail = errors.New("boom: connection refused 10.0.0.1")
	w := e.registerStart("other@example.com")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "10.0.0.1") {
		t.Fatalf("internal detail must not leak: %d %s", w.Code, w.Body)
	}
}

func TestRegistrationWithoutVerification(t *testing.T) {
	e := newEnv(t)
	// start is meaningless without verification
	testkit.Expect(t, e.registerStart("new@example.com"), 400, "auth.email_verification_disabled")
	w := e.registerComplete("new@example.com", "Passw0rdA", "")
	testkit.Expect(t, w, 200, "")
	d := decodeLogin(t, w)
	if d.User["emailVerifiedAt"] != nil {
		t.Fatalf("without verification the email stays unverified: %v", d.User)
	}
	if e.Mail.count() != 0 {
		t.Fatalf("no mail expected")
	}
}

func TestRegistrationSwitchesAndPolicy(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(func(s *Settings) { s.EmailVerificationEnabled = true; s.EmailRegistrationEnabled = false })
	testkit.Expect(t, e.registerStart("a@example.com"), 400, "auth.email_registration_disabled")
	testkit.Expect(t, e.registerComplete("a@example.com", "Passw0rdA", "123456"), 400, "auth.email_registration_disabled")

	e.Settings.set(func(s *Settings) { s.EmailRegistrationEnabled = true; s.EmailLoginEnabled = false })
	testkit.Expect(t, e.registerStart("a@example.com"), 400, "auth.email_registration_disabled")

	e.Settings.set(func(s *Settings) {
		s.EmailLoginEnabled = true
		s.EmailAllowedDomains = "@example.com, corp.test\nmail.test"
		s.EmailBlockPlusAlias = true
	})
	testkit.Expect(t, e.registerStart("a@other.org"), 400, "auth.email_domain_not_allowed")
	testkit.Expect(t, e.registerStart("a+x@example.com"), 400, "auth.email_alias_not_allowed")
	testkit.Expect(t, e.registerStart("a@EXAMPLE.com"), 200, "")
	testkit.Expect(t, e.registerStart("b@corp.test"), 200, "")
	testkit.Expect(t, e.registerStart("c@sub.example.com"), 400, "auth.email_domain_not_allowed")

	e.Settings.set(func(s *Settings) { s.EmailBlockPlusAlias = false })
	testkit.Expect(t, e.registerStart("a+x@example.com"), 200, "")
}

func TestRegistrationValidationAndPasswordPolicy(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(verificationOn)
	testkit.Expect(t, e.request("POST", "/auth/register/email/start", "", map[string]string{"email": "not-an-email"}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("POST", "/auth/register/email/start", "", map[string]string{}), 400, "request.invalid_body")
	testkit.Expect(t, e.registerComplete("a@example.com", "abc", "123456"), 400, "request.invalid_body")
	testkit.Expect(t, e.registerComplete("a@example.com", "Passw0rdA", "12345"), 400, "request.invalid_body")
	testkit.Expect(t, e.registerComplete("a@example.com", "12345678", "123456"), 400, "auth.invalid_password")
	// the policy check precedes any database lookup of the code
	if n := e.attempts(); n != 0 {
		t.Fatalf("no attempt may be recorded: %d", n)
	}
}

func (e *testEnv) attempts() int64 {
	var n int64
	e.DB.Model(&ContactVerification{}).Where("attempt_count > 0").Count(&n)
	return n
}

func TestRegistrationUsernameCollisionsGetSuffixes(t *testing.T) {
	e := newEnv(t)
	for _, addr := range []string{"same@a.example", "same@b.example"} {
		w := e.registerComplete(addr, "Passw0rdA", "")
		testkit.Expect(t, w, 200, "")
	}
	var names []string
	e.DB.Model(&User{}).Where("email LIKE ?", "same@%").Order("id").Pluck("username", &names)
	if len(names) != 2 || names[0] == names[1] || len(names[0]) > 16 || len(names[1]) > 16 {
		t.Fatalf("usernames %v", names)
	}
}

func TestTurnstileOnRegistration(t *testing.T) {
	verdict := make(chan bool, 4)
	var seen []string
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		seen = append(seen, r.PostForm.Get("secret")+"|"+r.PostForm.Get("response")+"|"+r.PostForm.Get("remoteip"))
		ok := <-verdict
		w.Header().Set("Content-Type", "application/json")
		if ok {
			_, _ = w.Write([]byte(`{"success":true}`))
		} else {
			_, _ = w.Write([]byte(`{"success":false}`))
		}
	}))
	defer ts.Close()

	e := newEnv(t, withCfg(func(c *configT) { c.Security.TurnstileSiteverifyURL = ts.URL }),
		withHooks(func(h *Hooks) { h.HTTPClient = ts.Client() }))
	e.Settings.set(func(s *Settings) {
		s.EmailVerificationEnabled = true
		s.TurnstileRegistrationEnabled = true
		s.TurnstileSiteKey = "site"
		s.TurnstileSecretKey = "shh"
	})
	start := func(token string) *httptest.ResponseRecorder {
		body := map[string]string{"email": "t@example.com"}
		if token != "" {
			body["turnstileToken"] = token
		}
		return e.request("POST", "/auth/register/email/start", "", body)
	}
	testkit.Expect(t, start(""), 400, "auth.turnstile_required")
	testkit.Expect(t, start(strings.Repeat("x", 2049)), 400, "request.invalid_body")

	verdict <- false
	testkit.Expect(t, start("bad-token"), 400, "auth.turnstile_invalid")
	verdict <- true
	testkit.Expect(t, start("good-token"), 200, "")
	if len(seen) != 2 || !strings.HasPrefix(seen[1], "shh|good-token|") {
		t.Fatalf("siteverify form: %v", seen)
	}

	// without a secret the check cannot run
	e.Settings.set(func(s *Settings) { s.TurnstileSecretKey = "" })
	testkit.Expect(t, start("whatever"), 400, "auth.turnstile_not_configured")
	// no site key: skipped altogether
	e.Settings.set(func(s *Settings) { s.TurnstileSiteKey = "" })
	e.Clock.Advance(2 * time.Minute)
	testkit.Expect(t, start(""), 200, "")
}

func TestTurnstileCheckedAtCompleteWhenVerificationIsOff(t *testing.T) {
	e := newEnv(t)
	e.Settings.set(func(s *Settings) {
		s.TurnstileRegistrationEnabled = true
		s.TurnstileSiteKey = "site"
		s.TurnstileSecretKey = "shh"
	})
	testkit.Expect(t, e.registerComplete("t@example.com", "Passw0rdA", ""), 400, "auth.turnstile_required")
}

func TestLoginOptionsReflectSettings(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) { c.Server.PublicAPIBaseURL = "https://api.example.com/" }))
	w := e.request("GET", "/auth/login-options", "", nil)
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	for _, k := range []string{"usernameEnabled", "emailEnabled", "emailRegistrationEnabled"} {
		if m[k] != true {
			t.Errorf("%s should be true: %v", k, m)
		}
	}
	for _, k := range []string{"emailVerificationEnabled", "passwordResetEnabled", "turnstileRegistrationEnabled"} {
		if m[k] != false {
			t.Errorf("%s should be false: %v", k, m)
		}
	}
	bridge := m["providerAuthBridge"].(map[string]any)
	if bridge["enabled"] != true || bridge["protocolVersion"] != float64(1) || bridge["callbackBaseURL"] != "https://api.example.com/api/v1/auth/providers" {
		t.Fatalf("bridge: %v", bridge)
	}
	if ps, ok := m["providers"].([]any); !ok || len(ps) != 0 {
		t.Fatalf("providers must be an empty array: %v", m["providers"])
	}

	// reset needs the whole SMTP set
	e.Settings.set(func(s *Settings) {
		s.PasswordResetEnabled, s.EmailVerificationEnabled = true, true
		s.SMTPHost, s.SMTPUsername = "smtp.example.com", "bot"
	})
	if dataMap(t, e.request("GET", "/auth/login-options", "", nil))["passwordResetEnabled"] != false {
		t.Fatalf("incomplete SMTP must hide password reset")
	}
	e.Settings.set(func(s *Settings) { s.SMTPPassword = "pw" })
	if dataMap(t, e.request("GET", "/auth/login-options", "", nil))["passwordResetEnabled"] != true {
		t.Fatalf("complete SMTP enables password reset")
	}
	e.Settings.set(func(s *Settings) { s.UsernameLoginEnabled, s.EmailLoginEnabled = false, false })
	if dataMap(t, e.request("GET", "/auth/login-options", "", nil))["passwordResetEnabled"] != false {
		t.Fatalf("no password login at all hides password reset")
	}
}

func TestLoginOptionsBridgeNeedsCleanBaseURL(t *testing.T) {
	for _, bad := range []string{"", "ftp://x.example", "https://u:p@x.example", "https://x.example/?q=1", "https://x.example/#f"} {
		e := newEnv(t, withCfg(func(c *configT) { c.Server.PublicAPIBaseURL = bad }))
		b := dataMap(t, e.request("GET", "/auth/login-options", "", nil))["providerAuthBridge"].(map[string]any)
		if b["enabled"] != false || b["callbackBaseURL"] != "" {
			t.Errorf("base %q: bridge %v", bad, b)
		}
	}
}
