package auth

import (
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/token"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestSmokeMountsAndLogsIn(t *testing.T) {
	e := newEnv(t)
	e.seedUser(userSpec{Username: "alice", Password: "correct-horse-9", Onboarded: true})
	d, w := e.signIn("alice", "correct-horse-9")
	if d.AccessToken == "" || d.SessionID == "" || d.TwoFactorRequired {
		t.Fatalf("bad login body: %+v", d)
	}
	if refreshCookie(w) == nil || d.RefreshToken != "" {
		t.Fatalf("cookie/body delivery wrong: %v %q", refreshCookie(w), d.RefreshToken)
	}
	testkit.Expect(t, e.request("GET", "/me", d.AccessToken, nil), 200, "")
	_ = token.TypeAccess
}
