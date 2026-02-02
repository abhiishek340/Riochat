package user

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestUpdateOnlyProvidedFieldsAndAdvancesTimestamp(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice", func(a *Account) {
		a.DisplayName, a.Email, a.Phone, a.Timezone, a.Locale = "Alice A", "a@b.co", "123456", "Asia/Tokyo", "ja"
	})
	env.Clock.Advance(time.Hour)
	got, err := acc.Update(context.Background(), u.ID, UpdateFields{DisplayName: Some("Alice B")})
	if err != nil {
		t.Fatal(err)
	}
	if got.DisplayName != "Alice B" {
		t.Fatalf("name %q", got.DisplayName)
	}
	if got.Email != "a@b.co" || got.Phone != "123456" || got.Timezone != "Asia/Tokyo" || got.Locale != "ja" ||
		got.Role != RoleUser || got.Username != "alice" || got.Status != StatusActive {
		t.Fatalf("other fields changed: %+v", got)
	}
	if !got.UpdatedAt.Equal(env.Clock.Now()) || !got.UpdatedAt.After(u.UpdatedAt) {
		t.Fatalf("updated_at %v (was %v)", got.UpdatedAt, u.UpdatedAt)
	}
	if !got.CreatedAt.Equal(u.CreatedAt) {
		t.Fatal("created_at must not move")
	}
}

func TestUpdateDistinguishesEmptyFromNotProvided(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ts := env.Clock.Now()
	u := seedAccount(t, env.DB, "alice", func(a *Account) {
		a.Email, a.Phone, a.ProfilePreferences, a.AppearancePreferences = "a@b.co", "123456", "p", "{}"
		a.EmailVerifiedAt, a.PhoneVerifiedAt, a.EmailBootstrapUsedAt, a.OnboardingCompletedAt = &ts, &ts, &ts, &ts
		a.AvatarURL = "/a.png"
	})
	later := ts.Add(time.Hour)
	got, err := acc.Update(context.Background(), u.ID, UpdateFields{
		Email: Some(""), AvatarURL: Some(""), EmailVerifiedAt: Some[*time.Time](nil), ProfilePreferences: Some(""),
		OnboardingCompletedAt: Some(&later), EmailSource: Some(EmailSourceUserSet),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Email != "" || got.AvatarURL != "" || got.ProfilePreferences != "" || got.EmailVerifiedAt != nil {
		t.Fatalf("cleared fields: %+v", got)
	}
	if got.Phone != "123456" || got.AppearancePreferences != "{}" || got.PhoneVerifiedAt == nil || got.EmailBootstrapUsedAt == nil {
		t.Fatalf("untouched fields changed: %+v", got)
	}
	if got.OnboardingCompletedAt == nil || !got.OnboardingCompletedAt.Equal(later) || got.EmailSource != EmailSourceUserSet {
		t.Fatalf("set fields: %+v", got)
	}
}

func TestUpdateWritesEveryField(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice")
	at := env.Clock.Now().Add(time.Minute)
	got, err := acc.Update(context.Background(), u.ID, UpdateFields{
		AvatarURL: Some(" https://example.com/x.png "), DisplayName: Some("Zed"), Email: Some("not validated here"),
		EmailVerifiedAt: Some(&at), EmailSource: Some("whatever"), EmailBootstrapUsedAt: Some(&at),
		Phone: Some("abc"), PhoneVerifiedAt: Some(&at), Role: Some("admin"), Timezone: Some("Not/AZone"),
		Locale: Some("zzzzzz"), ProfilePreferences: Some("pp"), AppearancePreferences: Some(`{"a":1}`),
		OnboardingCompletedAt: Some(&at),
	})
	if err != nil {
		t.Fatal(err)
	}
	// Only the avatar is validated; the rest is stored as given.
	if got.AvatarURL != "https://example.com/x.png" || got.DisplayName != "Zed" || got.Email != "not validated here" ||
		got.EmailSource != "whatever" || got.Phone != "abc" || got.Role != "admin" || got.Timezone != "Not/AZone" ||
		got.Locale != "zzzzzz" || got.ProfilePreferences != "pp" || got.AppearancePreferences != `{"a":1}` {
		t.Fatalf("got %+v", got)
	}
	for name, p := range map[string]*time.Time{"ev": got.EmailVerifiedAt, "eb": got.EmailBootstrapUsedAt, "pv": got.PhoneVerifiedAt, "ob": got.OnboardingCompletedAt} {
		if p == nil || !p.Equal(at) {
			t.Errorf("%s = %v", name, p)
		}
	}
}

func TestUpdateEmptyReturnsCurrentUnchanged(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice")
	env.Clock.Advance(time.Hour)
	got, err := acc.Update(context.Background(), u.ID, UpdateFields{})
	if err != nil || got.ID != u.ID || !got.UpdatedAt.Equal(u.UpdatedAt) {
		t.Fatalf("%+v %v", got, err)
	}
	if _, err := acc.Update(context.Background(), 999, UpdateFields{}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestUpdateUnknownAndDeletedUser(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	if _, err := acc.Update(context.Background(), 999, UpdateFields{DisplayName: Some("Zed")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("got %v", err)
	}
	u := seedAccount(t, env.DB, "alice")
	env.DB.Delete(&Account{}, u.ID)
	if _, err := acc.Update(context.Background(), u.ID, UpdateFields{DisplayName: Some("Zed")}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("soft-deleted: %v", err)
	}
}

func TestUpdateAvatarValidation(t *testing.T) {
	env := newEnv(t, Ports{})
	imgs := newFakeImages()
	acc := newAccounts(env, imgs)
	u := seedAccount(t, env.DB, "alice")
	other := seedAccount(t, env.DB, "bobby")
	ctx := context.Background()

	for _, bad := range []string{"javascript:alert(1)", "https:///a.png", "file:invalid", "ftp://x/y", "file: file_x"} {
		if _, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some(bad)}); !errors.Is(err, ErrInvalidAvatarURL) {
			t.Errorf("%q: %v", bad, err)
		}
	}
	for _, good := range []string{"", "/a.png", "generated:github:x", "https://example.com/a.png"} {
		if _, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some(good)}); err != nil {
			t.Errorf("%q: %v", good, err)
		}
	}
	if imgs.calls != 0 {
		t.Fatal("non-file avatars must not hit the image checker")
	}

	// Not owned / inactive / not an image: the checker says no.
	imgs.allow(other.ID, "file_x")
	if _, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some("file:file_x")}); !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("foreign file: %v", err)
	}
	got, _ := acc.GetByID(ctx, u.ID)
	if got.AvatarURL != "https://example.com/a.png" {
		t.Fatalf("avatar changed to %q", got.AvatarURL)
	}
	imgs.allow(u.ID, "file_x")
	got, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some("  file:file_x  ")})
	if err != nil || got.AvatarURL != "file:file_x" {
		t.Fatalf("%+v %v", got, err)
	}
	if !imgs.inTx {
		t.Fatal("the check must run inside the write transaction")
	}
	// The file vanishes before the next write.
	delete(imgs.ok, "1/file_x")
	if _, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some("file:file_x")}); !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("vanished file: %v", err)
	}
	// A checker failure is a plain error, not an invalid avatar.
	imgs.err = errors.New("db down")
	if _, err := acc.Update(ctx, u.ID, UpdateFields{AvatarURL: Some("file:file_x")}); err == nil || errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("checker failure: %v", err)
	}
}

func TestUpdateFileAvatarWithoutCheckerIsInvalid(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice")
	if _, err := acc.Update(context.Background(), u.ID, UpdateFields{AvatarURL: Some("file:file_demo_9")}); !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("got %v", err)
	}
}

func TestUpdateOtherFieldsAlongsideRejectedAvatarWriteNothing(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, newFakeImages())
	u := seedAccount(t, env.DB, "alice")
	_, err := acc.Update(context.Background(), u.ID, UpdateFields{DisplayName: Some("Changed"), AvatarURL: Some("file:file_nope")})
	if !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatal(err)
	}
	got, _ := acc.GetByID(context.Background(), u.ID)
	if got.DisplayName != "alice" {
		t.Fatalf("partial write: %q", got.DisplayName)
	}
}

func TestLastSuperadminCannotBeDemoted(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	sole := seedAccount(t, env.DB, "root1", func(a *Account) { a.Role = RoleSuperAdmin })
	seedAccount(t, env.DB, "adminx", func(a *Account) { a.Role = RoleAdmin })

	for _, role := range []string{RoleAdmin, RoleUser, "", "bogus"} {
		_, err := acc.Update(ctx, sole.ID, UpdateFields{Role: Some(role), DisplayName: Some("Renamed")})
		if !errors.Is(err, ErrLastSuperadmin) {
			t.Fatalf("role %q: %v", role, err)
		}
	}
	got, _ := acc.GetByID(ctx, sole.ID)
	if got.Role != RoleSuperAdmin || got.DisplayName != "root1" {
		t.Fatalf("nothing may persist: %+v", got)
	}
	// Distinct from not-found.
	if _, err := acc.Update(ctx, 999, UpdateFields{Role: Some(RoleUser)}); !errors.Is(err, ErrNotFound) || errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("unknown: %v", err)
	}
	// Setting superadmin on the sole superadmin is fine.
	if _, err := acc.Update(ctx, sole.ID, UpdateFields{Role: Some(RoleSuperAdmin)}); err != nil {
		t.Fatal(err)
	}
	if n, _ := acc.CountSuperadmins(ctx); n != 1 {
		t.Fatalf("count %d", n)
	}
}

func TestOneOfTwoSuperadminsMayBeDemotedAndPromotionIsAlwaysAllowed(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	a := seedAccount(t, env.DB, "root1", func(x *Account) { x.Role = RoleSuperAdmin })
	b := seedAccount(t, env.DB, "root2", func(x *Account) { x.Role = RoleSuperAdmin })
	if _, err := acc.Update(ctx, a.ID, UpdateFields{Role: Some(RoleAdmin)}); err != nil {
		t.Fatal(err)
	}
	if _, err := acc.Update(ctx, b.ID, UpdateFields{Role: Some(RoleAdmin)}); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("second demotion: %v", err)
	}
	if _, err := acc.Update(ctx, a.ID, UpdateFields{Role: Some(RoleSuperAdmin)}); err != nil {
		t.Fatalf("promotion: %v", err)
	}
	c := seedAccount(t, env.DB, "plainu")
	if got, err := acc.Update(ctx, c.ID, UpdateFields{Role: Some(RoleSuperAdmin)}); err != nil || got.Role != RoleSuperAdmin {
		t.Fatalf("%+v %v", got, err)
	}
	if n, _ := acc.CountSuperadmins(ctx); n != 3 {
		t.Fatalf("count %d", n)
	}
}

func TestSoftDeletedSuperadminsDoNotCount(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	gone := seedAccount(t, env.DB, "gone1", func(x *Account) { x.Role = RoleSuperAdmin })
	env.DB.Delete(&Account{}, gone.ID)
	live := seedAccount(t, env.DB, "live1", func(x *Account) { x.Role = RoleSuperAdmin })
	if n, _ := acc.CountSuperadmins(ctx); n != 1 {
		t.Fatalf("count %d", n)
	}
	if _, err := acc.Update(ctx, live.ID, UpdateFields{Role: Some(RoleAdmin)}); !errors.Is(err, ErrLastSuperadmin) {
		t.Fatalf("got %v", err)
	}
}

func TestConcurrentDemotionsLeaveOneSuperadmin(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	a := seedAccount(t, env.DB, "root1", func(x *Account) { x.Role = RoleSuperAdmin })
	b := seedAccount(t, env.DB, "root2", func(x *Account) { x.Role = RoleSuperAdmin })
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i, id := range []uint{a.ID, b.ID} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, errs[i] = acc.Update(ctx, id, UpdateFields{Role: Some(RoleAdmin)})
		}()
	}
	wg.Wait()
	if n, _ := acc.CountSuperadmins(ctx); n != 1 {
		t.Fatalf("%d superadmins left (errors: %v)", n, errs)
	}
	refused := 0
	for _, e := range errs {
		if errors.Is(e, ErrLastSuperadmin) {
			refused++
		}
	}
	if refused != 1 {
		t.Fatalf("errors: %v", errs)
	}
}

func TestSetStatus(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	u := seedAccount(t, env.DB, "alice")
	env.Clock.Advance(time.Minute)
	if err := acc.SetStatus(ctx, u.ID, StatusSuspended); err != nil {
		t.Fatal(err)
	}
	got, _ := acc.GetByID(ctx, u.ID)
	if got.Status != StatusSuspended || !got.UpdatedAt.After(u.UpdatedAt) {
		t.Fatalf("%+v", got)
	}
	// No validation of the value, and unknown ids are a silent no-op.
	if err := acc.SetStatus(ctx, u.ID, "anything-goes"); err != nil {
		t.Fatal(err)
	}
	if err := acc.SetStatus(ctx, 999, StatusActive); err != nil {
		t.Fatalf("unknown id: %v", err)
	}
	var n int64
	env.DB.Model(&Account{}).Count(&n)
	if n != 1 {
		t.Fatal("no rows may be created")
	}
}

func TestUnlock(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	u := seedAccount(t, env.DB, "alice")
	c := seedCredential(t, env.DB, u.ID)
	until := env.Clock.Now().Add(time.Hour)
	env.DB.Model(&Credential{}).Where("id = ?", c.ID).Update("locked_until", until)
	if err := acc.Unlock(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	var got Credential
	env.DB.Where("id = ?", c.ID).Take(&got)
	if got.FailedLoginCount != 0 || got.LockedUntil != nil {
		t.Fatalf("%+v", got)
	}
	if got.PasswordHash != "old-hash" || got.PasswordOrigin != PasswordOriginLocalRegister {
		t.Fatal("unlock must only touch the counter and the lock")
	}
	if err := acc.Unlock(ctx, 999); err != nil {
		t.Fatalf("no credential must be a no-op: %v", err)
	}
}

func TestResetPassword(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	u := seedAccount(t, env.DB, "alice")
	c := seedCredential(t, env.DB, u.ID)
	env.DB.Model(&Credential{}).Where("id = ?", c.ID).Updates(map[string]any{"locked_until": env.Clock.Now().Add(time.Hour), "password_enabled": false})

	if err := acc.ResetPassword(ctx, u.ID, "abc", false); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("weak: %v", err)
	}
	var untouched Credential
	env.DB.Where("id = ?", c.ID).Take(&untouched)
	if untouched.PasswordHash != "old-hash" {
		t.Fatal("rejected password must not change anything")
	}

	env.Clock.Advance(time.Minute)
	if err := acc.ResetPassword(ctx, u.ID, "  brand-new-pass1 ", true); err != nil {
		t.Fatal(err)
	}
	var got Credential
	env.DB.Where("id = ?", c.ID).Take(&got)
	if got.PasswordOrigin != PasswordOriginAdminReset || got.PasswordAlgo != "bcrypt" || !got.PasswordEnabled ||
		!got.MustResetPassword || got.FailedLoginCount != 0 || got.LockedUntil != nil {
		t.Fatalf("%+v", got)
	}
	if got.PasswordSetAt == nil || !got.PasswordSetAt.Equal(env.Clock.Now()) || got.PasswordUpdatedAt == nil || !got.PasswordUpdatedAt.Equal(env.Clock.Now()) {
		t.Fatalf("stamps: %+v", got)
	}
	if bcrypt.CompareHashAndPassword([]byte(got.PasswordHash), []byte("brand-new-pass1")) != nil {
		t.Fatal("hash does not verify")
	}
	if err := acc.ResetPassword(ctx, u.ID, "another-pass-2", false); err != nil {
		t.Fatal(err)
	}
	env.DB.Where("id = ?", c.ID).Take(&got)
	if got.MustResetPassword {
		t.Fatal("must-reset follows the request")
	}
	if err := acc.ResetPassword(ctx, 999, "valid-pass-9", false); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestResetPasswordDoesNotRevokeSessions(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice")
	seedCredential(t, env.DB, u.ID)
	now := env.Clock.Now()
	env.DB.Create(&sessionRow{SessionID: "s1", UserID: u.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	if err := acc.ResetPassword(context.Background(), u.ID, "valid-pass-9", false); err != nil {
		t.Fatal(err)
	}
	var s sessionRow
	env.DB.Take(&s)
	if s.RevokedAt != nil {
		t.Fatal("sessions must stay")
	}
}

func TestRevokeAllSessions(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	u := seedAccount(t, env.DB, "alice")
	other := seedAccount(t, env.DB, "bobby")
	now := env.Clock.Now()
	oldStamp := now.Add(-48 * time.Hour)
	exp := now.Add(time.Hour)
	env.DB.Create(&sessionRow{SessionID: "a1", UserID: u.ID, IssuedAt: now, ExpiresAt: exp})
	env.DB.Create(&sessionRow{SessionID: "a2", UserID: u.ID, IssuedAt: now, ExpiresAt: exp})
	env.DB.Create(&sessionRow{SessionID: "a3", UserID: u.ID, IssuedAt: now, ExpiresAt: exp, RevokedAt: &oldStamp, RevokeReason: "logout"})
	env.DB.Create(&sessionRow{SessionID: "b1", UserID: other.ID, IssuedAt: now, ExpiresAt: exp})

	env.Clock.Advance(time.Minute)
	if err := acc.RevokeAllSessions(ctx, u.ID, "password reset"); err != nil {
		t.Fatal(err)
	}
	byID := map[string]sessionRow{}
	var rows []sessionRow
	env.DB.Find(&rows)
	for _, r := range rows {
		byID[r.SessionID] = r
	}
	for _, id := range []string{"a1", "a2"} {
		if r := byID[id]; r.RevokedAt == nil || !r.RevokedAt.Equal(env.Clock.Now()) || r.RevokeReason != "password reset" {
			t.Errorf("%s: %+v", id, r)
		}
	}
	if r := byID["a3"]; r.RevokedAt == nil || !r.RevokedAt.Equal(oldStamp) || r.RevokeReason != "logout" {
		t.Errorf("already revoked session changed: %+v", r)
	}
	if r := byID["b1"]; r.RevokedAt != nil {
		t.Errorf("other user's session revoked: %+v", r)
	}
	if err := acc.RevokeAllSessions(ctx, 999, "x"); err != nil {
		t.Fatalf("unknown user: %v", err)
	}
}

func TestRevokeReasonIsTruncatedTo128Characters(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "alice")
	now := env.Clock.Now()
	env.DB.Create(&sessionRow{SessionID: "a1", UserID: u.ID, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	long := ""
	for i := 0; i < 200; i++ {
		long += "é"
	}
	if err := acc.RevokeAllSessions(context.Background(), u.ID, long); err != nil {
		t.Fatal(err)
	}
	var s sessionRow
	env.DB.Take(&s)
	if n := len([]rune(s.RevokeReason)); n != 128 {
		t.Fatalf("stored %d characters", n)
	}
}
