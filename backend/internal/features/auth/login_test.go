package auth

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestLoginSuccessSetsBrowserCookie(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d, w := e.signIn("alice", "correct-horse-9", "User-Agent", "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0 Safari/537.36")
	if d.AccessToken == "" || len(d.SessionID) != 36 || d.TwoFactorRequired || d.RefreshToken != "" {
		t.Fatalf("unexpected body: %+v", d)
	}
	c := refreshCookie(w)
	if c == nil || !c.HttpOnly || c.Path != "/api/v1/auth" || c.SameSite != http.SameSiteLaxMode || c.Value == "" {
		t.Fatalf("cookie: %+v", c)
	}
	if strings.Contains(w.Body.String(), `"refreshToken"`) {
		t.Fatalf("browser response must not carry refreshToken: %s", w.Body)
	}
	if d.User["username"] != "alice" || d.User["id"].(float64) != float64(u.ID) {
		t.Fatalf("user: %v", d.User)
	}
	var sess Session
	if err := e.DB.Where("user_id = ?", u.ID).First(&sess).Error; err != nil {
		t.Fatal(err)
	}
	if sess.SessionID != d.SessionID || sess.DeviceName != "Chrome on macOS" || sess.DeviceType != "desktop" || sess.ClientIP != "203.0.113.5" {
		t.Fatalf("session row: %+v", sess)
	}
	if sess.RefreshTokenHash != sha256Hex(c.Value) {
		t.Fatalf("refresh digest not stored")
	}
	if e.countEvents("login", "success") != 1 {
		t.Fatalf("success event missing")
	}
	if e.reloadUser(u.ID).LastLoginAt == nil {
		t.Fatalf("last_login_at not set")
	}
}

func TestLoginNativeAndForgedPlatformHeader(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) {
		c.Server.CORSAllowOrigin += ",https://evil.example"
	}))
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})

	d, w := e.signIn("alice", "correct-horse-9", "X-Client-Platform", "desktop")
	if d.RefreshToken == "" || refreshCookie(w) != nil {
		t.Fatalf("native client must get the token in the body only: %q %v", d.RefreshToken, refreshCookie(w))
	}
	d, w = e.signIn("alice", "correct-horse-9", "X-Client-Platform", " Mobile ", "Origin", "tauri://localhost")
	if d.RefreshToken == "" || refreshCookie(w) != nil {
		t.Fatalf("custom-scheme origin is not a web origin")
	}
	d, w = e.signIn("alice", "correct-horse-9", "X-Client-Platform", "desktop", "Origin", "https://evil.example")
	if d.RefreshToken != "" || refreshCookie(w) == nil {
		t.Fatalf("web origin must fall back to the cookie")
	}
	d, w = e.signIn("alice", "correct-horse-9", "X-Client-Platform", "desktop", "Origin", "http://tauri.localhost")
	if d.RefreshToken == "" {
		t.Fatalf("tauri.localhost is the desktop webview")
	}
}

func TestLoginRejectsBadCredentialsUniformly(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com"})
	for _, body := range []map[string]string{
		{"username": "alice", "password": "wrong-password"},
		{"username": "nobody", "password": "wrong-password"},
		{"username": "Alice", "password": "correct-horse-9"}, // identifiers are matched exactly
		{"username": "ALICE@example.com", "password": "correct-horse-9"},
	} {
		w := e.request("POST", "/auth/login", "", body)
		testkit.Expect(t, w, 401, "auth.invalid_credentials")
		if testkit.Envelope(t, w).ErrorMsg != "invalid username or password" {
			t.Fatalf("message: %s", w.Body)
		}
	}
	// email identifiers work when email login is on
	d, _ := e.signIn("alice@example.com", "correct-horse-9")
	if d.AccessToken == "" {
		t.Fatal("email login failed")
	}
	if e.countEvents("login", "failure") != 4 {
		t.Fatalf("failure events: %d", e.countEvents("login", "failure"))
	}
}

func TestLoginMethodSwitches(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Email: "alice@example.com"})
	e.Settings.set(func(s *Settings) { s.EmailLoginEnabled = false })
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice@example.com", "password": "correct-horse-9"}), 401, "auth.invalid_credentials")
	e.signIn("alice", "correct-horse-9")
	e.Settings.set(func(s *Settings) { s.EmailLoginEnabled = true; s.UsernameLoginEnabled = false })
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"}), 401, "auth.invalid_credentials")
	e.signIn("alice@example.com", "correct-horse-9")
}

func TestLoginValidation(t *testing.T) {
	e := newEnv(t)
	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "short"})
	testkit.Expect(t, w, 400, "request.invalid_body")
	details := testkit.Envelope(t, w).Details.(map[string]any)
	fe := details["fieldErrors"].([]any)[0].(map[string]any)
	if details["reason"] != "validation" || fe["field"] != "password" || fe["rule"] != "min" {
		t.Fatalf("details: %v", details)
	}
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "ab", "password": "correct-horse-9"}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("POST", "/auth/login", "", ""), 400, "request.invalid_body")
	testkit.Expect(t, e.request("POST", "/auth/login", "", "{bad"), 400, "request.invalid_body")
}

func TestLockoutAfterRepeatedFailures(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	wrong := map[string]string{"username": "alice", "password": "wrong-password"}
	for i := 1; i <= 5; i++ {
		testkit.Expect(t, e.request("POST", "/auth/login", "", wrong), 401, "auth.invalid_credentials")
	}
	if got := e.reloadUser(u.ID).Status; got != StatusLocked {
		t.Fatalf("status after 5 failures: %s", got)
	}
	cred := e.credential(u.ID)
	if cred.FailedLoginCount != 5 || cred.LockedUntil == nil {
		t.Fatalf("credential: %+v", cred)
	}

	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 423, "auth.account_locked")
	if ra := w.Header().Get("Retry-After"); ra != "900" {
		t.Fatalf("Retry-After %q", ra)
	}
	// a wrong password while locked does not reveal the lock and does not count
	testkit.Expect(t, e.request("POST", "/auth/login", "", wrong), 401, "auth.invalid_credentials")
	if e.credential(u.ID).FailedLoginCount != 5 {
		t.Fatalf("counter must not move while locked")
	}

	e.Clock.Advance(14*time.Minute + 30*time.Second)
	w = e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 423, "auth.account_locked")
	if ra := w.Header().Get("Retry-After"); ra != "30" {
		t.Fatalf("Retry-After near the end: %q", ra)
	}

	e.Clock.Advance(time.Minute)
	e.signIn("alice", "correct-horse-9")
	if got := e.reloadUser(u.ID).Status; got != StatusActive {
		t.Fatalf("status after unlock: %s", got)
	}
	cred = e.credential(u.ID)
	if cred.FailedLoginCount != 0 || cred.LockedUntil != nil {
		t.Fatalf("lock not cleared: %+v", cred)
	}
}

func TestLockoutDisabledNeverLocks(t *testing.T) {
	for name, set := range map[string]func(*Settings){
		"zero threshold": func(s *Settings) { s.LoginMaxFailures = 0 },
		"zero minutes":   func(s *Settings) { s.LoginLockMinutes = 0 },
		"negative":       func(s *Settings) { s.LoginMaxFailures = -1 },
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t)
			e.Settings.set(set)
			u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
			for i := 0; i < 8; i++ {
				testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "wrong-password"}), 401, "auth.invalid_credentials")
			}
			if e.reloadUser(u.ID).Status != StatusActive || e.credential(u.ID).FailedLoginCount != 8 {
				t.Fatalf("lockout disabled but state changed")
			}
			e.signIn("alice", "correct-horse-9")
		})
	}
}

func TestAdminLockHasNoRetryAfter(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Status: StatusLocked})
	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 423, "auth.account_locked")
	if w.Header().Get("Retry-After") != "" {
		t.Fatalf("no unlock time is known")
	}
}

func TestInactiveAccountsAreInvalidCredentials(t *testing.T) {
	e := newEnv(t)
	for _, st := range []string{StatusSuspended, StatusDeactivated, StatusPendingActivation} {
		e.seedUser(userSpec{Username: "user-" + st[:4], Password: "correct-horse-9", Status: st})
		testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "user-" + st[:4], "password": "correct-horse-9"}), 401, "auth.invalid_credentials")
	}
}

func TestPasswordlessUserCannotLogin(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "sso-user"})
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "sso-user", "password": "anything-at-all"}), 401, "auth.invalid_credentials")
	if e.credential(u.ID).FailedLoginCount != 0 {
		t.Fatalf("no counter is touched for passwordless accounts")
	}
}

func TestLongPasswordsAreFullyCompared(t *testing.T) {
	e := newEnv(t)
	long := strings.Repeat("a", 80) + "X"
	e.seedUser(userSpec{Username: "alice", Password: long})
	e.signIn("alice", long)
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": strings.Repeat("a", 80) + "Y"}), 401, "auth.invalid_credentials")
}

// ---- tokens and the bearer gate -----------------------------------------------------

func TestBearerGate(t *testing.T) {
	e := newEnv(t)
	_, access := e.session(userSpec{Username: "alice", Onboarded: true})
	testkit.Expect(t, e.request("GET", "/me", access, nil), 200, "")
	cases := []struct {
		name    string
		headers []string
		msg     string
	}{
		{"missing", nil, "authorization header is required"},
		{"wrong scheme", []string{"Authorization", "Token abc"}, "invalid authorization header"},
		{"empty token", []string{"Authorization", "Bearer   "}, "invalid authorization header"},
		{"garbage", []string{"Authorization", "Bearer not.a.jwt"}, "invalid token"},
	}
	for _, c := range cases {
		w := e.request("GET", "/me", "", nil, c.headers...)
		testkit.Expect(t, w, 401, "auth.invalid_token")
		if got := testkit.Envelope(t, w).ErrorMsg; got != c.msg {
			t.Errorf("%s: message %q, want %q", c.name, got, c.msg)
		}
	}
}

func TestRefreshTokenIsNotAnAccessToken(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	_, w := e.signIn("alice", "correct-horse-9")
	w = e.request("GET", "/me", refreshCookie(w).Value, nil)
	testkit.Expect(t, w, 401, "auth.invalid_token")
	if testkit.Envelope(t, w).ErrorMsg != "invalid token type" {
		t.Fatalf("message: %s", w.Body)
	}
}

func TestLogoutRevokesSession(t *testing.T) {
	e := newEnv(t)
	_, access := e.session(userSpec{Username: "alice"})
	testkit.Expect(t, e.request("GET", "/me", access, nil), 200, "")
	w := e.request("POST", "/auth/logout", access, nil)
	testkit.Expect(t, w, 200, "")
	if dataMap(t, w)["revoked"] != true {
		t.Fatalf("body: %s", w.Body)
	}
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("logout must clear the cookie: %+v", c)
	}
	testkit.Expect(t, e.request("GET", "/me", access, nil), 401, "auth.session_invalid")
	var sess Session
	e.DB.First(&sess)
	if sess.RevokedAt == nil || sess.RevokeReason != "user logout" {
		t.Fatalf("session: %+v", sess)
	}
}

func TestSessionExpiryAndTokenAgeRules(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9")
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 200, "")

	// A token that predates its session by more than two minutes is refused.
	var sess Session
	e.DB.Where("user_id = ?", u.ID).First(&sess)
	e.DB.Model(&Session{}).Where("id = ?", sess.ID).Update("created_at", e.Clock.Now().Add(time.Hour))
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 401, "auth.session_invalid")
	e.DB.Model(&Session{}).Where("id = ?", sess.ID).Update("created_at", sess.CreatedAt)
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 200, "")

	// An expired session row is refused even while the token is still fresh.
	e.DB.Model(&Session{}).Where("id = ?", sess.ID).Update("expires_at", e.Clock.Now().Add(-time.Second))
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 401, "auth.session_invalid")

	// A token for a session that does not exist.
	other := e.Bearer(testkit.User(u.ID))
	testkit.Expect(t, e.request("GET", "/me", strings.TrimPrefix(other["Authorization"], "Bearer "), nil), 401, "auth.session_invalid")
}

func TestAccessTokenExpires(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9")
	e.Clock.Advance(25 * time.Hour)
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 401, "auth.invalid_token")
}

func TestSessionTouchRespectsRateAndChanges(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9", "User-Agent", "curl/8.0")
	var s0 Session
	e.DB.Where("user_id = ?", u.ID).First(&s0)

	e.Clock.Advance(10 * time.Second)
	e.request("GET", "/me", d.AccessToken, nil)
	var s1 Session
	e.DB.First(&s1, s0.ID)
	if !s1.LastSeenAt.Equal(*s0.LastSeenAt) {
		t.Fatalf("last_seen moved within a minute without changes")
	}

	e.Clock.Advance(2 * time.Minute)
	e.request("GET", "/me", d.AccessToken, nil, "User-Agent", "curl/8.0")
	e.DB.First(&s1, s0.ID)
	if !s1.LastSeenAt.After(*s0.LastSeenAt) {
		t.Fatalf("last_seen should have moved after a minute")
	}

	// A changed user agent is stored at once.
	e.request("GET", "/me", d.AccessToken, nil, "User-Agent", "Mozilla/5.0 (Windows NT 10.0) Firefox/121.0")
	e.DB.First(&s1, s0.ID)
	if s1.DeviceName != "Firefox on Windows" || s1.BrowserName != "Firefox" {
		t.Fatalf("device snapshot not refreshed: %+v", s1)
	}
}

func TestTrustedProxyGeoHeadersAreRecorded(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) { c.Server.TrustedProxies = "203.0.113.5" }))
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9", "X-Forwarded-For", "198.51.100.7", "CF-IPCountry", "SG", "X-Geo-City", "Singapore")
	var s Session
	e.DB.Where("user_id = ?", u.ID).First(&s)
	if s.ClientIP != "198.51.100.7" || s.CountryCode != "SG" || s.CityName != "Singapore" || s.GeoSource != "proxy_header_trusted" || s.GeoAccuracy != "ip" {
		t.Fatalf("session geo: %+v", s)
	}
	// Same IP, no geo headers: stored values are kept.
	e.Clock.Advance(2 * time.Minute)
	e.request("GET", "/me", d.AccessToken, nil, "X-Forwarded-For", "198.51.100.7")
	e.DB.First(&s, s.ID)
	if s.CountryCode != "SG" {
		t.Fatalf("empty incoming geo must not erase stored geo")
	}
	// New IP: geo is not carried over.
	e.request("GET", "/me", d.AccessToken, nil, "X-Forwarded-For", "198.51.100.9")
	e.DB.First(&s, s.ID)
	if s.ClientIP != "198.51.100.9" || s.CountryCode != "" || s.CityName != "" {
		t.Fatalf("geo carried over to a new IP: %+v", s)
	}
}

func TestLoginReturnsPreLoginTimestampSnapshot(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9")
	if d.User["lastLoginAt"] != nil || d.User["lastActiveAt"] != nil {
		t.Fatalf("first login must show the pre-login snapshot: %v %v", d.User["lastLoginAt"], d.User["lastActiveAt"])
	}
	if e.reloadUser(u.ID).LastLoginAt == nil {
		t.Fatalf("last_login_at not stored")
	}
	d, _ = e.signIn("alice", "correct-horse-9")
	if d.User["lastLoginAt"] == nil {
		t.Fatalf("second login must show the previous login time")
	}
}
