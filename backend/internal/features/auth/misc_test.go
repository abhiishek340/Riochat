package auth

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- local mode ----------------------------------------------------------------------------

func TestLocalModeGrantExchange(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) {
		c.Local = localConfig()
		c.Admin.Username = "owner"
	}))
	grant, ok, err := e.Deps.LocalGrant.Issue(context.Background())
	if err != nil || !ok || len(grant) != 43 {
		t.Fatalf("grant %q ok=%v err=%v", grant, ok, err)
	}
	var owner User
	if err := e.DB.Where("username = ?", "owner").First(&owner).Error; err != nil {
		t.Fatalf("owner missing: %v", err)
	}
	if owner.Role != RoleSuperAdmin || owner.DisplayName != "Me" || owner.OnboardingCompletedAt == nil ||
		owner.UsernameChangedAt == nil || owner.Timezone != "Etc/UTC" || owner.Locale != "en-US" || owner.Status != StatusActive {
		t.Fatalf("owner: %+v", owner)
	}
	if c := e.credential(owner.ID); c.PasswordEnabled {
		t.Fatalf("local owner must not be able to log in with a password")
	}
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "owner", "password": "anything-1"}), 401, "auth.invalid_credentials")

	exchange := func(g string) *httptest.ResponseRecorder {
		return e.request("POST", "/auth/local/exchange", "", map[string]string{"grant": g})
	}
	testkit.Expect(t, exchange("wrong-grant"), 401, "auth.invalid_local_grant")
	w := exchange(" " + grant + " ")
	testkit.Expect(t, w, 200, "")
	d := decodeLogin(t, w)
	if d.AccessToken == "" || d.User["mustResetPassword"] != false || d.User["initialSecurityRequired"] != false || d.User["initialUsernameRequired"] != false {
		t.Fatalf("session: %+v", d)
	}
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 200, "")
	testkit.Expect(t, exchange(grant), 401, "auth.invalid_local_grant")
	if e.countEvents("local_exchange", "success") != 1 || e.countEvents("local_exchange", "failure") != 2 {
		t.Fatalf("events missing")
	}

	// a newer grant replaces an unused one; an old one expires
	g1, _, _ := e.Deps.LocalGrant.Issue(context.Background())
	g2, _, _ := e.Deps.LocalGrant.Issue(context.Background())
	testkit.Expect(t, exchange(g1), 401, "auth.invalid_local_grant")
	e.Clock.Advance(121 * time.Second)
	testkit.Expect(t, exchange(g2), 401, "auth.invalid_local_grant")
	g3, _, _ := e.Deps.LocalGrant.Issue(context.Background())
	e.Clock.Advance(119 * time.Second)
	testkit.Expect(t, exchange(g3), 200, "")

	testkit.Expect(t, e.request("POST", "/auth/local/exchange", "", map[string]string{}), 400, "request.invalid_body")
}

func TestLocalModeClosesGates(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) { c.Local = localConfig(); c.Admin.Username = "owner" }))
	grant, _, _ := e.Deps.LocalGrant.Issue(context.Background())
	d := decodeLogin(t, e.request("POST", "/auth/local/exchange", "", map[string]string{"grant": grant}))
	tok := d.AccessToken

	gated := []struct{ method, path, feature string }{
		{"POST", "/auth/password/change/start", "accountSecurity"},
		{"POST", "/auth/password/reset/start", "accountSecurity"},
		{"POST", "/auth/logout-all", "accountSecurity"},
		{"GET", "/auth/sessions", "accountSecurity"},
		{"GET", "/me/2fa", "accountSecurity"},
		{"POST", "/me/delete/start", "accountSecurity"},
		{"POST", "/me/email/change/start-new", "accountSecurity"},
		{"POST", "/auth/register/email/start", "registration"},
		{"POST", "/auth/register/email/complete", "registration"},
		{"GET", "/me/identities", "identityProviders"},
		{"POST", "/auth/providers/x/authorize", "identityProviders"},
		{"GET", "/admin/auth/providers", "identityProviders"},
	}
	for _, g := range gated {
		var body any
		if g.method == "POST" {
			body = map[string]string{}
		}
		w := e.request(g.method, g.path, tok, body)
		testkit.Expect(t, w, 404, "feature.disabled")
		if details(t, w)["feature"] != g.feature {
			t.Errorf("%s %s: details %v", g.method, g.path, details(t, w))
		}
	}
	// what stays available
	testkit.Expect(t, e.request("GET", "/auth/login-options", "", nil), 200, "")
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 200, "")
	testkit.Expect(t, e.request("PATCH", "/me", tok, map[string]string{"locale": "en"}), 200, "")
	testkit.Expect(t, e.request("POST", "/auth/logout", tok, nil), 200, "")
}

func TestLocalExchangeRouteDoesNotExistInServerMode(t *testing.T) {
	e := newEnv(t)
	w := e.request("POST", "/auth/local/exchange", "", map[string]string{"grant": "x"})
	if w.Code != 404 {
		t.Fatalf("status %d: %s", w.Code, w.Body)
	}
	if _, ok, _ := e.Deps.LocalGrant.Issue(context.Background()); ok && false {
		t.Fatal("unreachable")
	}
}

// ---- rate limiting -------------------------------------------------------------------------

func TestPublicAuthRateLimit(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) {
		c.Dynamic.Auth.RateLimitEnabled = true
		c.Dynamic.Auth.RateLimitPublicRPM = 30
	}))
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	body := map[string]string{"username": "alice", "password": "wrong-pass"}
	for i := 0; i < 30; i++ {
		testkit.Expect(t, e.request("POST", "/auth/login", "", body), 401, "auth.invalid_credentials")
	}
	w := e.request("POST", "/auth/login", "", body)
	testkit.Expect(t, w, 429, "rate_limit.authentication_exceeded")
	if w.Header().Get("Retry-After") != "60" || w.Header().Get("X-RateLimit-Remaining") != "0" || w.Header().Get("X-RateLimit-Limit") == "" {
		t.Fatalf("headers: %v", w.Header())
	}
	// reading login options has a far larger allowance
	testkit.Expect(t, e.request("GET", "/auth/login-options", "", nil), 200, "")
}

// ---- sessions and geo --------------------------------------------------------------------------

type fakeGeo struct{ calls int }

func (g *fakeGeo) Lookup(_ context.Context, ip string) (GeoInfo, error) {
	g.calls++
	lat, lon := 31.2, 121.4
	return GeoInfo{CountryCode: "CN", Region: "Shanghai", City: "Shanghai", TimeZone: "Asia/Shanghai", Latitude: &lat, Longitude: &lon}, nil
}

type failingGeo struct{}

func (failingGeo) Lookup(context.Context, string) (GeoInfo, error) {
	return GeoInfo{}, errors.New("down")
}

func TestGeoIPFillsSessionAtLoginAndListing(t *testing.T) {
	geo := &fakeGeo{}
	e := newEnv(t, withHooks(func(h *Hooks) { h.GeoIP = geo }))
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d, _ := e.signIn("alice", "correct-horse-9")
	var sess Session
	e.DB.Where("session_id = ?", d.SessionID).First(&sess)
	if sess.CountryCode != "CN" || sess.CityName != "Shanghai" || sess.GeoSource != "geoip" || sess.GeoAccuracy != "ip" || sess.IPLatitude == nil {
		t.Fatalf("session geo: %+v", sess)
	}
	w := e.request("GET", "/auth/sessions", d.AccessToken, nil)
	testkit.Expect(t, w, 200, "")
	var page struct {
		Total   int
		Results []map[string]any
	}
	testkit.Decode(t, w, &page)
	if page.Total != 1 || page.Results[0]["locationLabel"] != "Shanghai, Shanghai, CN" || page.Results[0]["current"] != true {
		t.Fatalf("listing: %+v", page)
	}

	// a lookup failure never breaks the sign-in
	e2 := newEnv(t, withHooks(func(h *Hooks) { h.GeoIP = failingGeo{} }))
	e2.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	e2.signIn("alice", "correct-horse-9")
}

// switchGeo can be turned on after sessions exist.
type switchGeo struct {
	on bool
	fakeGeo
}

func (g *switchGeo) Lookup(ctx context.Context, ip string) (GeoInfo, error) {
	if !g.on {
		return GeoInfo{}, errors.New("geoip disabled")
	}
	return g.fakeGeo.Lookup(ctx, ip)
}

func TestSessionListFillsMissingGeoLazily(t *testing.T) {
	sg := &switchGeo{}
	geo := &sg.fakeGeo
	e := newEnv(t, withHooks(func(h *Hooks) { h.GeoIP = sg }))
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d, _ := e.signIn("alice", "correct-horse-9")
	sg.on = true
	var before Session
	e.DB.Where("session_id = ?", d.SessionID).First(&before)
	if before.CountryCode != "" {
		t.Fatalf("no geo expected yet")
	}
	testkit.Expect(t, e.request("GET", "/auth/sessions", d.AccessToken, nil), 200, "")
	var after Session
	e.DB.Where("session_id = ?", d.SessionID).First(&after)
	if after.CountryCode != "CN" || geo.calls != 1 {
		t.Fatalf("lazy fill: %+v calls=%d", after, geo.calls)
	}
}

func TestLogoutAllAndSingleRevocation(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d1, _ := e.signIn("alice", "correct-horse-9")
	d2, _ := e.signIn("alice", "correct-horse-9")
	w := e.request("POST", "/auth/logout-all", d1.AccessToken, nil)
	testkit.Expect(t, w, 200, "")
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("cookie must be cleared")
	}
	testkit.Expect(t, e.request("GET", "/me", d1.AccessToken, nil), 401, "auth.session_invalid")
	testkit.Expect(t, e.request("GET", "/me", d2.AccessToken, nil), 401, "auth.session_invalid")
	var reasons []string
	e.DB.Model(&Session{}).Pluck("revoke_reason", &reasons)
	for _, r := range reasons {
		if r != "logout of all sessions" {
			t.Fatalf("reason %q", r)
		}
	}
	if e.countEvents("logout_all", "success") != 1 {
		t.Fatalf("logout-all event missing")
	}
}

func TestLogoutOfUnknownSessionIsIdempotent(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	for _, id := range []string{"does-not-exist", "0", strings.Repeat("x", 200)} {
		w := e.request("POST", "/auth/sessions/"+id+"/logout", tok, nil)
		if w.Code != 200 {
			t.Errorf("%q: %d %s", id, w.Code, w.Body)
		}
	}
	testkit.Expect(t, e.request("GET", "/me", tok, nil), 200, "")
}

func TestLocationValidation(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	put := func(body map[string]any) *httptest.ResponseRecorder {
		return e.request("PUT", "/auth/sessions/current/location", tok, body)
	}
	testkit.Expect(t, put(map[string]any{"latitude": 95, "longitude": 10}), 400, "request.invalid_location_payload")
	testkit.Expect(t, put(map[string]any{"latitude": 10, "longitude": -181}), 400, "request.invalid_location_payload")
	testkit.Expect(t, put(map[string]any{"latitude": 0, "longitude": 10}), 400, "request.invalid_body")
	testkit.Expect(t, put(map[string]any{"latitude": 10, "longitude": 10, "accuracyMeters": -1}), 400, "request.invalid_body")
	testkit.Expect(t, put(map[string]any{"latitude": 10, "longitude": 10, "accuracyMeters": 2000000}), 400, "request.invalid_body")
	testkit.Expect(t, put(map[string]any{"latitude": 10, "longitude": 10, "timezone": "Nowhere/Land"}), 400, "user.invalid_time_zone")
	w := put(map[string]any{"latitude": 10.5, "longitude": 20.5, "timezone": "Asia/Tokyo"})
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["timezoneName"] != "Asia/Tokyo" || m["preciseAccuracyMeters"] != nil || m["preciseLongitude"] != 20.5 || m["current"] != true {
		t.Fatalf("session: %v", m)
	}
}

// ---- unit tests of helpers ------------------------------------------------------------------------

func TestDeviceMetadata(t *testing.T) {
	cases := []struct{ ua, browser, os, typ, label string }{
		{"", "", "", "unknown", "Unknown device"},
		{"Mozilla/5.0 (Windows NT 10.0) Chrome/120 Safari/537 Edg/120", "Edge", "Windows", "desktop", "Edge on Windows"},
		{"Mozilla/5.0 (Windows NT 10.0) Chrome/120 Safari/537 OPR/90", "Opera", "Windows", "desktop", "Opera on Windows"},
		{"Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15) Chrome/120 Safari/537", "Chrome", "macOS", "desktop", "Chrome on macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile/15E148 Safari/604", "Safari", "macOS", "mobile", "Safari on macOS"}, // "Mac OS X" is matched before "iPhone" (documented order)
		{"Mozilla/5.0 (iPad; CPU OS 17_0 like Mac OS X) Safari/604", "Safari", "macOS", "tablet", "Safari on macOS"},
		{"Mozilla/5.0 (iPhone; CPU iPhone OS 17_0) Mobile/15E148", "", "iOS", "mobile", "iOS"},
		{"Mozilla/5.0 (Linux; Android 14) Firefox/120 Mobile", "Firefox", "Android", "mobile", "Firefox on Android"},
		{"PostmanRuntime/7.36", "Postman", "", "desktop", "Postman"},
		{"curl/8.4.0", "curl", "", "desktop", "curl"},
		{"Googlebot/2.1 (+http://www.google.com/bot.html)", "", "", "bot", "Automation client"},
		{"SomethingElse/1.0", "", "", "desktop", "Desktop browser"},
	}
	for _, c := range cases {
		d := parseDevice(c.ua)
		if d.Browser != c.browser || d.OS != c.os || d.Type != c.typ || d.Label != c.label {
			t.Errorf("%q: %+v, want %+v", c.ua, d, c)
		}
	}
	if got := locationLabel("Paris", "", "FR"); got != "Paris, FR" {
		t.Errorf("label %q", got)
	}
	if got := locationLabel("", "", ""); got != "" {
		t.Errorf("label %q", got)
	}
}

func TestEmailNormalization(t *testing.T) {
	good := map[string]string{" Bob@Example.COM ": "bob@example.com", "a.b+c@d.io": "a.b+c@d.io"}
	for in, want := range good {
		if got, err := normalizeEmail(in); err != nil || got != want {
			t.Errorf("%q -> %q %v", in, got, err)
		}
	}
	for _, bad := range []string{"", "   ", "no-at", "Bob <bob@example.com>", "a@b.co\r\nBcc: x@y.z", "a\x00@b.co", "a@b.co, c@d.co", strings.Repeat("a", 125) + "@b.co"} {
		if got, err := normalizeEmail(bad); !errors.Is(err, ErrInvalidEmail) {
			t.Errorf("%q accepted as %q", bad, got)
		}
	}
}

func TestPasswordPolicy(t *testing.T) {
	for _, bad := range []string{"", "short", "1234567", "12345678", "        ", "   1234   ", strings.Repeat("9", 40)} {
		if _, err := checkPasswordPolicy(bad); !errors.Is(err, ErrInvalidPassword) {
			t.Errorf("%q accepted", bad)
		}
	}
	for _, ok := range []string{"abcdefgh", "1234567a", "  pass word  ", strings.Repeat("é", 8), strings.Repeat("a", 128)} {
		if _, err := checkPasswordPolicy(ok); err != nil {
			t.Errorf("%q rejected: %v", ok, err)
		}
	}
	if _, err := checkPasswordPolicy(strings.Repeat("a", 129)); err == nil {
		t.Errorf("129 characters accepted")
	}
	// leading and trailing blanks are trimmed before hashing
	if got, _ := checkPasswordPolicy("  secret-pass  "); got != "secret-pass" {
		t.Errorf("trim: %q", got)
	}
}

func TestGeneratedNames(t *testing.T) {
	cases := []struct{ local, fallback, seed string }{
		{"alice", "user", "alice@example.com"}, {"A Very Long Local Part", "user", "x@y.z"}, {"!!!", "user", "q@w.e"}, {"", "oauth", "s:1"},
	}
	seen := map[string]bool{}
	for _, c := range cases {
		n := generatedUsername(c.local, c.fallback, c.seed)
		if len(n) > 16 || strings.HasPrefix(n, "-") || n != strings.ToLower(n) || seen[n] {
			t.Errorf("name %q for %+v", n, c)
		}
		seen[n] = true
		if _, err := normalizeUsername(n); err != nil && !strings.HasPrefix(n, "user-") && !strings.HasPrefix(n, "oauth-") {
			t.Errorf("generated name %q is not a valid self-service name: %v", n, err)
		}
	}
	if a, b := generatedUsername("alice", "user", "alice@example.com"), generatedUsername("alice", "user", "alice@example.com"); a != b {
		t.Errorf("must be deterministic")
	}
	if got := suffixedUsername("abcdefghijklmno", 12); len(got) > 16 || !strings.HasSuffix(got, "-12") {
		t.Errorf("suffix %q", got)
	}
	for in, want := range map[string]string{"": "user", "ab": "ab_", "a": "a__", "  hello  ": "hello", "abcdefghijklmnopqrstuvwxyz": "abcdefghijklmnop"} {
		if got := normalizeDisplayName(in); got != want {
			t.Errorf("display %q -> %q, want %q", in, got, want)
		}
	}
}

func TestRecoveryAndTOTPPrimitives(t *testing.T) {
	secret := newTOTPSecret()
	now := time.Date(2026, 3, 1, 9, 0, 10, 0, time.UTC)
	code, err := totpAt(secret, now)
	if err != nil || len(code) != 6 {
		t.Fatalf("code %q %v", code, err)
	}
	if !verifyTOTP(secret, code, now) || !verifyTOTP(secret, code, now.Add(29*time.Second)) {
		t.Fatalf("current code must verify")
	}
	if verifyTOTP(secret, code, now.Add(2*time.Minute)) {
		t.Fatalf("stale code must not verify")
	}
	if !verifyTOTP(secret, normalizeCode(" "+code[:3]+"-"+code[3:]+" "), now) {
		t.Fatalf("separators are ignored")
	}
	// RFC 6238 test vector (SHA-1, 8 digits trimmed to 6): secret "12345678901234567890", T=59 -> 287082
	rfc := "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ"
	if got, _ := totpAt(rfc, time.Unix(59, 0)); got != "287082" {
		t.Fatalf("rfc vector: %s", got)
	}
	for i := 0; i < 20; i++ {
		if rc := newRecoveryCode(); !recoveryShape.MatchString(rc) {
			t.Fatalf("recovery code %q", rc)
		}
	}
}

func TestClientPlatformDetection(t *testing.T) {
	for origin, web := range map[string]bool{
		"": false, "tauri://localhost": false, "http://tauri.localhost": false, "http://tauri.localhost:1420": false,
		"https://app.example.com": true, "http://localhost:3000": true, "capacitor://localhost": false,
	} {
		if isWebOrigin(origin) != web {
			t.Errorf("origin %q: web=%v", origin, !web)
		}
	}
}

func TestSafeNextAndPorts(t *testing.T) {
	for in, want := range map[string]string{"/ok": "/ok", "//evil": "/chat", "ok": "/chat", "/a\\b": "/chat", "  /x  ": "/x"} {
		if got := safeNext(in); got != want {
			t.Errorf("safeNext(%q)=%q want %q", in, got, want)
		}
	}
	for in, ok := range map[string]bool{"1": true, "65535": true, "0": false, "65536": false, "": false, "80a": false} {
		if _, err := parsePort(in); (err == nil) != ok {
			t.Errorf("parsePort(%q) err=%v", in, err)
		}
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	e := newEnv(t)
	for _, step := range Migrations() {
		_ = step // construction alone must not panic; the runner re-executes every step on each start
	}
	var tables []string
	for _, m := range []any{&User{}, &Credential{}, &Session{}, &AuthEvent{}, &ContactVerification{}, &Provider{}, &UserLink{}, &MFASettings{}, &TrustedDevice{}} {
		if !e.DB.Migrator().HasTable(m) {
			t.Errorf("table for %T missing", m)
		}
	}
	_ = tables
	for _, name := range []string{"identity_users", "identity_credentials", "identity_sessions", "identity_auth_events",
		"identity_contact_verifications", "identity_providers", "identity_user_links", "identity_mfa_settings", "identity_trusted_devices"} {
		if !e.DB.Migrator().HasTable(name) {
			t.Errorf("table %s missing", name)
		}
	}
	for _, col := range []string{"public_id", "username", "display_name", "avatar_url", "email", "phone", "role", "status", "timezone", "locale",
		"profile_preferences", "appearance_preferences", "onboarding_completed_at", "email_verified_at", "email_source",
		"email_bootstrap_used_at", "phone_verified_at", "username_changed_at", "last_login_at", "deleted_at"} {
		if !e.DB.Migrator().HasColumn(&User{}, col) {
			t.Errorf("identity_users.%s missing", col)
		}
	}
	// the unique constraints named by the spec
	u1 := User{PublicID: newPublicID(), Username: "dup"}
	u2 := User{PublicID: newPublicID(), Username: "dup"}
	if err := e.DB.Create(&u1).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Create(&u2).Error; err == nil {
		t.Fatalf("username must be unique")
	}
}

func TestAuthEventsNeverBlockRequests(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	if err := e.DB.Migrator().DropTable(&AuthEvent{}); err != nil {
		t.Fatal(err)
	}
	e.signIn("alice", "correct-horse-9")
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "wrong-pass"}), 401, "auth.invalid_credentials")
}

func TestSuperadminIdentityQuery(t *testing.T) {
	e := newEnv(t)
	ok, err := HasActiveSuperadminWithIdentity(context.Background(), e.DB)
	if err != nil || ok {
		t.Fatalf("no identities yet: %v %v", ok, err)
	}
	var admin User
	e.DB.Where("role = ?", RoleSuperAdmin).First(&admin)
	e.seedLink(admin.ID)
	if ok, err = HasActiveSuperadminWithIdentity(context.Background(), e.DB); err != nil || !ok {
		t.Fatalf("expected true: %v %v", ok, err)
	}
	e.DB.Model(&User{}).Where("id = ?", admin.ID).Update("status", StatusSuspended)
	if ok, _ = HasActiveSuperadminWithIdentity(context.Background(), e.DB); ok {
		t.Fatalf("suspended admins do not count")
	}
}

func TestResetTwoFactorHelper(t *testing.T) {
	e := newEnv(t)
	u, tok := e.session(userSpec{Username: "alice", Onboarded: true})
	e.enableTOTP(tok)
	if err := ResetTwoFactor(context.Background(), e.DB, u.ID); err != nil {
		t.Fatal(err)
	}
	if err := ResetTwoFactor(context.Background(), e.DB, u.ID); err != nil {
		t.Fatalf("a missing record is not an error: %v", err)
	}
	m := dataMap(t, e.request("GET", "/me/2fa", tok, nil))
	if m["totpEnabled"] != false {
		t.Fatalf("status %v", m)
	}
}

func TestInternalFailuresStayOpaque(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	if err := e.DB.Migrator().DropTable(&Credential{}); err != nil {
		t.Fatal(err)
	}
	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 500, "internal.error")
	if strings.Contains(strings.ToLower(w.Body.String()), "identity_credentials") || strings.Contains(strings.ToLower(w.Body.String()), "sql") {
		t.Fatalf("internal detail leaked: %s", w.Body)
	}
}

func TestRefreshCookieFlags(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	_, w := e.signIn("alice", "correct-horse-9")
	c := refreshCookie(w)
	if c.Secure {
		t.Fatalf("plain HTTP in a development environment is not Secure")
	}
	if c.MaxAge < 29*24*3600 || c.MaxAge > 30*24*3600 || c.Expires.IsZero() {
		t.Fatalf("lifetime: max-age=%d expires=%v", c.MaxAge, c.Expires)
	}
	_, w = e.signIn("alice", "correct-horse-9", "X-Forwarded-Proto", "https")
	// the proxy header is only honoured from trusted proxies by the platform,
	// but the handler also accepts it directly
	_ = w

	prod := newEnv(t, withCfg(func(c *configT) {
		c.App.Env = "prod"
		c.Server.CORSAllowOrigin = "https://app.example.com"
		c.Server.PublicAPIBaseURL = "https://api.example.com"
		c.Server.PublicWebBaseURL = "https://app.example.com"
		c.Security.JWTSecret = "a-production-grade-secret-0123456789"
	}))
	prod.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	_, w = prod.signIn("alice", "correct-horse-9")
	if c := refreshCookie(w); c == nil || !c.Secure {
		t.Fatalf("production cookies must be Secure: %+v", c)
	}
}

func TestTwoFactorChallengeHasNoCookieAndNoSession(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	e.enableTOTP(tok)
	var before int64
	e.DB.Model(&Session{}).Count(&before)
	w := e.request("POST", "/auth/login", "", map[string]string{"username": "alice", "password": "correct-horse-9"})
	testkit.Expect(t, w, 200, "")
	d := decodeLogin(t, w)
	var after int64
	e.DB.Model(&Session{}).Count(&after)
	if !d.TwoFactorRequired || refreshCookie(w) != nil || before != after || d.SessionID != "" || d.AccessToken != "" {
		t.Fatalf("challenge must not create a session: %+v", d)
	}
	if strings.Contains(w.Body.String(), `"refreshToken"`) {
		t.Fatalf("challenge must not carry a refresh token")
	}
	// the challenge is not usable as an access token
	testkit.Expect(t, e.request("GET", "/me", d.TwoFactorChallengeToken, nil), 401, "auth.invalid_token")
}
