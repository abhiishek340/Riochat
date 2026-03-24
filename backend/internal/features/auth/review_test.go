package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"strings"
	"testing"
)

func TestClearedCookieExpiresImmediately(t *testing.T) {
	e := newEnv(t)
	w := e.request("POST", "/auth/refresh", "", nil)
	var got string
	for _, v := range w.Header().Values("Set-Cookie") {
		if strings.HasPrefix(v, refreshCookieName+"=") {
			got = v
		}
	}
	if !strings.Contains(got, "Max-Age=0") || !strings.Contains(got, "Path=/api/v1/auth") ||
		!strings.Contains(got, "HttpOnly") || !strings.Contains(got, "SameSite=Lax") {
		t.Fatalf("clearing cookie: %q", got)
	}
}

func TestRecoveryHashIsPlainKeyedHashOfNormalizedCode(t *testing.T) {
	e := newEnv(t)
	m := hmac.New(sha256.New, []byte(e.Svc.cfg().Security.JWTSecret))
	m.Write([]byte("AB1CD2E3F4G5"))
	want := hex.EncodeToString(m.Sum(nil))
	for _, in := range []string{"AB1C-D2E3-F4G5", "ab1c-d2e3-f4g5", " ab1cd2e3f4g5 "} {
		if got := e.Svc.recoveryHash(in); got != want {
			t.Fatalf("hash of %q = %s, want %s", in, got, want)
		}
	}
}

func TestBindFlowEmailConflictAnswers400WithoutDetails(t *testing.T) {
	err := grantError(&bridgeGrant{Slug: "gh", ErrCode: grantErrEmailConflict, ErrEmail: "a@b.co"}, false)
	typed, ok := asTyped(err)
	if !ok || typed.Status != 400 || typed.Details != nil || typed.Code != "auth.provider_email_conflict" {
		t.Fatalf("bind conflict: %+v", typed)
	}
	err = grantError(&bridgeGrant{Slug: "gh", ErrCode: grantErrEmailConflict, ErrEmail: "a@b.co"}, true)
	typed, _ = asTyped(err)
	if typed.Status != 409 || typed.Details == nil {
		t.Fatalf("public conflict: %+v", typed)
	}
}

func TestColumnDefaultsMatchTheSchemaContract(t *testing.T) {
	e := newEnv(t)
	u := User{PublicID: "pid0000000000000000000000000001", Username: "bare-user"}
	if err := e.DB.Create(&u).Error; err != nil {
		t.Fatal(err)
	}
	var got User
	e.DB.First(&got, u.ID)
	if got.Role != "user" || got.Status != "active" || got.Timezone != "Etc/UTC" || got.Locale != "en-US" {
		t.Fatalf("user defaults: %+v", got)
	}
	c := Credential{UserID: u.ID}
	if err := e.DB.Create(&c).Error; err != nil {
		t.Fatal(err)
	}
	var gc Credential
	e.DB.First(&gc, c.ID)
	if gc.PasswordAlgo != "bcrypt" || gc.PasswordOrigin != "local_register" || gc.FailedLoginCount != 0 {
		t.Fatalf("credential defaults: %+v", gc)
	}
	v := ContactVerification{Token: "tok-defaults", Purpose: PurposeRegister}
	if err := e.DB.Create(&v).Error; err != nil {
		t.Fatal(err)
	}
	var gv ContactVerification
	e.DB.First(&gv, v.ID)
	if gv.Status != verifPending {
		t.Fatalf("verification default status: %q", gv.Status)
	}
	p := Provider{PublicID: "prov0000000000000000000000000001", Type: "oidc", Name: "x", Slug: "x"}
	if err := e.DB.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	var gp Provider
	e.DB.First(&gp, p.ID)
	if gp.DefaultRole != "user" || gp.SubjectField != "sub" || gp.EmailField != "email" ||
		gp.EmailVerifiedField != "email_verified" || gp.NameField != "name" || gp.AvatarField != "picture" || gp.SortOrder != 100 {
		t.Fatalf("provider defaults: %+v", gp)
	}
}
