package auth

import (
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestRefreshRotatesBrowserCookie(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d0, w0 := e.signIn("alice", "correct-horse-9")
	c0 := refreshCookie(w0)

	e.Clock.Advance(time.Second)
	w := e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0))
	testkit.Expect(t, w, 200, "")
	d1 := decodeLogin(t, w)
	c1 := refreshCookie(w)
	if d1.AccessToken == "" || d1.AccessToken == d0.AccessToken || d1.SessionID != d0.SessionID {
		t.Fatalf("refresh body: %+v", d1)
	}
	if c1 == nil || c1.Value == c0.Value || d1.RefreshToken != "" {
		t.Fatalf("a new cookie is expected, and no body token")
	}
	// the access token issued before rotation stays valid until it expires
	testkit.Expect(t, e.request("GET", "/me", d0.AccessToken, nil), 200, "")
	testkit.Expect(t, e.request("GET", "/me", d1.AccessToken, nil), 200, "")
	if e.countEvents("token_refresh", "success") != 1 || e.reloadUser(u.ID).Status != StatusActive {
		t.Fatalf("refresh success event missing")
	}
}

func TestRefreshGraceWindowAllowsOneLostResponse(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	_, w0 := e.signIn("alice", "correct-horse-9")
	c0 := refreshCookie(w0)

	e.Clock.Advance(time.Second)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0)), 200, "")
	e.Clock.Advance(5 * time.Second)
	w := e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0))
	testkit.Expect(t, w, 200, "") // replay of the old token inside the 15 s grace
	newest := refreshCookie(w)

	// the original token matches neither the current nor the previous digest now
	e.Clock.Advance(time.Minute)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0)), 401, "auth.invalid_refresh_token")
	var revoked int64
	e.DB.Model(&Session{}).Where("revoked_at IS NOT NULL").Count(&revoked)
	if revoked != 0 {
		t.Fatalf("a plain mismatch must not revoke the session")
	}
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(newest)), 200, "")
}

func TestRefreshReuseAfterGraceRevokesSession(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d0, w0 := e.signIn("alice", "correct-horse-9")
	c0 := refreshCookie(w0)

	w := e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0))
	testkit.Expect(t, w, 200, "")
	c1 := refreshCookie(w)

	e.Clock.Advance(16 * time.Second)
	w = e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0))
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("browsers get the cookie cleared on failure")
	}
	var sess Session
	e.DB.First(&sess)
	if sess.RevokedAt == nil || sess.RevokeReason != "refresh token reuse" {
		t.Fatalf("session must be revoked: %+v", sess)
	}
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c1)), 401, "auth.invalid_refresh_token")
	testkit.Expect(t, e.request("GET", "/me", d0.AccessToken, nil), 401, "auth.session_invalid")
}

func TestRefreshWithoutTokenClearsCookie(t *testing.T) {
	e := newEnv(t)
	w := e.request("POST", "/auth/refresh", "", nil)
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	c := refreshCookie(w)
	if c == nil || c.MaxAge >= 0 || c.Value != "" {
		t.Fatalf("cookie must be cleared: %+v", c)
	}
	// Native clients get no cookie traffic at all.
	w = e.request("POST", "/auth/refresh", "", nil, "X-Client-Platform", "desktop")
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	if refreshCookie(w) != nil {
		t.Fatalf("native clients must not receive Set-Cookie")
	}
}

func TestRefreshNativeUsesBodyOnly(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d0, w0 := e.signIn("alice", "correct-horse-9", "X-Client-Platform", "mobile")
	if refreshCookie(w0) != nil || d0.RefreshToken == "" {
		t.Fatalf("native login delivery")
	}
	w := e.request("POST", "/auth/refresh", "", map[string]string{"refreshToken": d0.RefreshToken}, "X-Client-Platform", "mobile")
	testkit.Expect(t, w, 200, "")
	d1 := decodeLogin(t, w)
	if d1.RefreshToken == "" || d1.RefreshToken == d0.RefreshToken || refreshCookie(w) != nil {
		t.Fatalf("native refresh delivery: %+v", d1)
	}
	// a cookie alone is not accepted for native clients
	_, wb := e.signIn("alice", "correct-horse-9")
	w = e.request("POST", "/auth/refresh", "", nil, "X-Client-Platform", "mobile", "Cookie", cookieHeader(refreshCookie(wb)))
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	// ...and a body alone is not accepted for browsers
	w = e.request("POST", "/auth/refresh", "", map[string]string{"refreshToken": d1.RefreshToken})
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	// a malformed body counts as no token
	w = e.request("POST", "/auth/refresh", "", "{broken", "X-Client-Platform", "mobile")
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
}

func TestRefreshRejectsWrongKindsOfToken(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, _ := e.signIn("alice", "correct-horse-9")
	// an access token is not a refresh token
	w := e.request("POST", "/auth/refresh", "", nil, "Cookie", refreshCookieName+"="+d.AccessToken)
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
	w = e.request("POST", "/auth/refresh", "", nil, "Cookie", refreshCookieName+"=garbage")
	testkit.Expect(t, w, 401, "auth.invalid_refresh_token")
}

func TestRefreshFailsForInactiveUserAndRevokedSession(t *testing.T) {
	e := newEnv(t)
	u := e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	_, w0 := e.signIn("alice", "correct-horse-9")
	c0 := refreshCookie(w0)
	e.DB.Model(&User{}).Where("id = ?", u.ID).Update("status", StatusSuspended)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0)), 401, "auth.invalid_refresh_token")
	e.DB.Model(&User{}).Where("id = ?", u.ID).Update("status", StatusActive)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0)), 200, "")
	e.DB.Model(&Session{}).Where("user_id = ?", u.ID).Update("revoked_at", e.Clock.Now())
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(c0)), 401, "auth.invalid_refresh_token")
}

func TestRefreshHonorsConfiguredLifetimes(t *testing.T) {
	e := newEnv(t, withCfg(func(c *configT) {
		c.Dynamic.Auth.AccessTokenTTL = time.Hour
		c.Dynamic.Auth.RefreshTokenTTL = 2 * time.Hour
	}))
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, w := e.signIn("alice", "correct-horse-9")
	if got := d.ExpiresAt.Sub(e.Clock.Now()); got != time.Hour {
		t.Fatalf("access lifetime %v", got)
	}
	if got := d.RefreshExpiresAt.Sub(e.Clock.Now()); got != 2*time.Hour {
		t.Fatalf("refresh lifetime %v", got)
	}
	e.Clock.Advance(3 * time.Hour)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookieHeader(refreshCookie(w))), 401, "auth.invalid_refresh_token")
}

// ---- session management ------------------------------------------------------------------

func TestSessionListAndRevocation(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d1, _ := e.signIn("alice", "correct-horse-9", "User-Agent", "Mozilla/5.0 (iPhone; CPU iPhone OS 17_0 like Mac OS X) Mobile Safari/604.1")
	e.Clock.Advance(time.Minute)
	d2, w2 := e.signIn("alice", "correct-horse-9", "User-Agent", "Mozilla/5.0 (Windows NT 10.0) Edg/120.0 Chrome/120.0")

	w := e.request("GET", "/auth/sessions", d1.AccessToken, nil)
	testkit.Expect(t, w, 200, "")
	var page struct {
		Total   int
		Results []map[string]any
	}
	testkit.Decode(t, w, &page)
	if page.Total != 2 || page.Results[0]["sessionID"] != d1.SessionID || page.Results[0]["current"] != true || page.Results[1]["current"] != false {
		t.Fatalf("list: %s", w.Body)
	}
	if page.Results[1]["deviceLabel"] != "Edge on Windows" || page.Results[0]["deviceType"] != "mobile" {
		t.Fatalf("device info: %v", page.Results)
	}
	for _, k := range []string{"preciseLatitude", "preciseLongitude", "preciseAccuracyMeters", "preciseLocatedAt", "lastSeenAt", "ipLatitude"} {
		if _, ok := page.Results[0][k]; !ok {
			t.Errorf("field %s must be present (null)", k)
		}
	}

	w = e.request("POST", "/auth/sessions/"+d2.SessionID+"/logout", d1.AccessToken, nil)
	testkit.Expect(t, w, 200, "")
	if refreshCookie(w) != nil {
		t.Fatalf("revoking another session must not touch the cookie")
	}
	testkit.Expect(t, e.request("GET", "/me", d2.AccessToken, nil), 401, "auth.session_invalid")
	testkit.Decode(t, e.request("GET", "/auth/sessions", d1.AccessToken, nil), &page)
	if page.Total != 1 {
		t.Fatalf("expected one live session, got %d", page.Total)
	}
	// unknown, foreign and repeated ids are all fine
	testkit.Expect(t, e.request("POST", "/auth/sessions/"+d2.SessionID+"/logout", d1.AccessToken, nil), 200, "")
	testkit.Expect(t, e.request("POST", "/auth/sessions/does-not-exist/logout", d1.AccessToken, nil), 200, "")

	_ = w2
	w = e.request("POST", "/auth/logout-all", d1.AccessToken, nil)
	testkit.Expect(t, w, 200, "")
	if c := refreshCookie(w); c == nil || c.MaxAge >= 0 {
		t.Fatalf("logout-all clears the cookie")
	}
	testkit.Expect(t, e.request("GET", "/me", d1.AccessToken, nil), 401, "auth.session_invalid")
	var live int64
	e.DB.Model(&Session{}).Where("revoked_at IS NULL").Count(&live)
	if live != 0 {
		t.Fatalf("live sessions after logout-all: %d", live)
	}
}

func TestSessionRevocationCannotTouchOthersSessions(t *testing.T) {
	e := newEnv(t)
	_, tokA := e.session(userSpec{Username: "alice"})
	dB, _ := e.signIn2(t, "bob")
	testkit.Expect(t, e.request("POST", "/auth/sessions/"+dB.SessionID+"/logout", tokA, nil), 200, "")
	testkit.Expect(t, e.request("GET", "/me", dB.AccessToken, nil), 200, "")
}

// signIn2 seeds and logs in another user.
func (e *testEnv) signIn2(t *testing.T, username string) (loginData, string) {
	e.seedUser(userSpec{Username: username, Password: "correct-horse-9"})
	d, _ := e.signIn(username, "correct-horse-9")
	return d, d.AccessToken
}

func TestRecordPreciseLocation(t *testing.T) {
	e := newEnv(t)
	_, tok := e.session(userSpec{Username: "alice"})
	w := e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 31.2, "longitude": 121.4, "accuracyMeters": 25, "timezone": "Asia/Shanghai"})
	testkit.Expect(t, w, 200, "")
	m := dataMap(t, w)
	if m["preciseLatitude"] != 31.2 || m["preciseLongitude"] != 121.4 || m["geoSource"] != "browser_geolocation" ||
		m["geoAccuracy"] != "precise" || m["preciseAccuracyMeters"] != float64(25) || m["timezoneName"] != "Asia/Shanghai" || m["current"] != true {
		t.Fatalf("location: %v", m)
	}
	if m["preciseLocatedAt"] == nil {
		t.Fatalf("located-at missing")
	}
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 95, "longitude": 10}), 400, "request.invalid_location_payload")
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 10, "longitude": 181}), 400, "request.invalid_location_payload")
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 0, "longitude": 10}), 400, "request.invalid_body")
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 10, "longitude": 10, "timezone": "Mars/Base"}), 400, "user.invalid_time_zone")
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", tok, map[string]any{"latitude": 10, "longitude": 10, "accuracyMeters": 2000000}), 400, "request.invalid_body")
	if e.countEvents("session_location_update", "success") != 1 {
		t.Fatalf("location event")
	}
}
