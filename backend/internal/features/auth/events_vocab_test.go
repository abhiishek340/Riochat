package auth

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

// lastEvent returns the newest auth event of a type and result.
func (e *testEnv) lastEvent(t *testing.T, eventType, result string) (AuthEvent, map[string]any) {
	t.Helper()
	var ev AuthEvent
	if err := e.DB.Where("event_type = ? AND result = ?", eventType, result).Order("id DESC").First(&ev).Error; err != nil {
		t.Fatalf("no %s/%s event: %v", eventType, result, err)
	}
	detail := map[string]any{}
	if ev.DetailJSON != "" {
		if err := json.Unmarshal([]byte(ev.DetailJSON), &detail); err != nil {
			t.Fatalf("detail %q: %v", ev.DetailJSON, err)
		}
	}
	return ev, detail
}

func TestLoginFailureReasonsShareOneLabel(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	e.seedUser(userSpec{Username: "frozen", Password: "correct-horse-9", Status: StatusSuspended})
	for _, user := range []string{"alice", "nobody", "frozen"} {
		testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": user, "password": "wrong-password"}), 401, "auth.invalid_credentials")
	}
	var n int64
	e.DB.Model(&AuthEvent{}).Where("event_type = ? AND result = ? AND reason = ?", "login", "failure", "invalid_credentials_or_inactive").Count(&n)
	if n != 3 {
		t.Fatalf("expected 3 failures with the shared reason, got %d", n)
	}
}

func TestLockedAccountEventReason(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "locked", Password: "correct-horse-9", Status: StatusLocked})
	testkit.Expect(t, e.request("POST", "/auth/login", "", map[string]string{"username": "locked", "password": "correct-horse-9"}), 423, "auth.account_locked")
	ev, _ := e.lastEvent(t, "login", "failure")
	if ev.Reason != "account_locked" {
		t.Fatalf("reason %q", ev.Reason)
	}
}

func TestRefreshFailureReasonsAreSnakeCase(t *testing.T) {
	e := newEnv(t)
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", refreshCookieName+"=not-a-jwt"), 401, "auth.invalid_refresh_token")
	ev, _ := e.lastEvent(t, "token_refresh", "failure")
	if ev.Reason != "invalid_refresh_token_parse" || ev.UserID != 0 {
		t.Fatalf("unparseable: %+v", ev)
	}

	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d, w := e.signIn("alice", "correct-horse-9")
	cookie := cookieHeader(refreshCookie(w))
	testkit.Expect(t, e.request("POST", "/auth/logout", d.AccessToken, nil), 200, "")
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookie), 401, "auth.invalid_refresh_token")
	ev, _ = e.lastEvent(t, "token_refresh", "failure")
	if ev.Reason != "session_revoked_or_expired" {
		t.Fatalf("revoked: %q", ev.Reason)
	}
	if err := e.DB.Where("1 = 1").Delete(&Session{}).Error; err != nil {
		t.Fatal(err)
	}
	testkit.Expect(t, e.request("POST", "/auth/refresh", "", nil, "Cookie", cookie), 401, "auth.invalid_refresh_token")
	ev, _ = e.lastEvent(t, "token_refresh", "failure")
	if ev.Reason != "session_not_found" {
		t.Fatalf("missing: %q", ev.Reason)
	}
}

func TestSessionEventDetailKeysAreSnakeCase(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9"})
	d1, _ := e.signIn("alice", "correct-horse-9", "User-Agent", "Mozilla/5.0 (Windows NT 10.0) Edg/120.0 Chrome/120.0")
	d2, _ := e.signIn("alice", "correct-horse-9", "User-Agent", "Mozilla/5.0 (Windows NT 10.0) Edg/120.0 Chrome/120.0")

	// Login success detail.
	_, login := e.lastEvent(t, "login", "success")
	for _, k := range []string{"session_id", "client_ip", "location_label", "username", "device_name", "browser_name", "os_name", "device_type"} {
		if _, ok := login[k]; !ok {
			t.Errorf("login detail lacks %s: %v", k, login)
		}
	}
	for k := range login {
		if strings.ToLower(k) != k {
			t.Errorf("login detail key %q is not snake_case", k)
		}
	}

	// Revoking another session is a "logout" event carrying that session's snapshot.
	testkit.Expect(t, e.request("POST", "/auth/sessions/"+d2.SessionID+"/logout", d1.AccessToken, nil), 200, "")
	_, revoke := e.lastEvent(t, "logout", "success")
	if revoke["session_id"] != d2.SessionID || revoke["browser_name"] == nil {
		t.Fatalf("revoke detail: %v", revoke)
	}
	var stale int64
	e.DB.Model(&AuthEvent{}).Where("event_type = ?", "session_revoke").Count(&stale)
	if stale != 0 {
		t.Fatalf("session_revoke is not an event type")
	}

	// Location update carries the precise coordinates.
	testkit.Expect(t, e.request("PUT", "/auth/sessions/current/location", d1.AccessToken,
		map[string]any{"latitude": 31.2, "longitude": 121.4, "accuracyMeters": 25, "timezone": "Asia/Shanghai"}), 200, "")
	_, loc := e.lastEvent(t, "session_location_update", "success")
	if loc["precise_latitude"] != 31.2 || loc["precise_longitude"] != 121.4 || loc["precise_accuracy_meters"] != float64(25) ||
		loc["timezone_name"] != "Asia/Shanghai" || loc["session_id"] != d1.SessionID {
		t.Fatalf("location detail: %v", loc)
	}
}
