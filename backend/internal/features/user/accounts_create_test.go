package user

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/testkit"
)

var hex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func validCreate() CreateInput {
	return CreateInput{Username: "newbie", Password: "longenough99", DisplayName: "New Bie"}
}

func seedPlan(t *testing.T, db *gorm.DB, code string, active bool, interval string, priceActive, priceDefault bool) (planRow, priceRow) {
	t.Helper()
	p := planRow{Code: code, Name: code, IsActive: active}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	pr := priceRow{PlanID: p.ID, BillingInterval: interval, IsActive: priceActive, IsDefault: priceDefault}
	if err := db.Create(&pr).Error; err != nil {
		t.Fatal(err)
	}
	return p, pr
}

func TestCreateMakesNormalActiveUser(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	got, err := acc.Create(context.Background(), validCreate())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID == 0 || got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() {
		t.Fatalf("ids/timestamps: %+v", got)
	}
	if got.Role != RoleUser || got.Status != StatusActive || got.EmailSource != EmailSourceAdminSet {
		t.Fatalf("role/status/source: %s %s %s", got.Role, got.Status, got.EmailSource)
	}
	if !hex32.MatchString(got.PublicID) {
		t.Fatalf("public id %q", got.PublicID)
	}
	if got.Username != "newbie" || got.DisplayName != "New Bie" || got.Timezone != "Etc/UTC" || got.Locale != "en-US" {
		t.Fatalf("fields: %+v", got)
	}
	if got.EmailVerifiedAt != nil || got.PhoneVerifiedAt != nil || got.LastLoginAt != nil {
		t.Fatal("no verification stamps expected")
	}
	var cred Credential
	if err := env.DB.Where("user_id = ?", got.ID).Take(&cred).Error; err != nil {
		t.Fatal(err)
	}
	if cred.PasswordOrigin != PasswordOriginAdminCreated || !cred.PasswordEnabled || cred.MustResetPassword ||
		cred.PasswordAlgo != "bcrypt" || cred.FailedLoginCount != 0 {
		t.Fatalf("credential: %+v", cred)
	}
	if cred.PasswordUpdatedAt == nil || cred.PasswordSetAt == nil || !cred.PasswordSetAt.Equal(env.Clock.Now()) {
		t.Fatalf("stamps: %+v", cred)
	}
	if bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte("longenough99")) != nil {
		t.Fatal("hash does not verify")
	}
	var subs int64
	env.DB.Model(&subscriptionRow{}).Count(&subs)
	if subs != 0 {
		t.Fatal("no subscription without billing mode")
	}
}

func TestCreateNormalizesFieldsAndStoresTrimmedPassword(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	in := CreateInput{
		Username: "  Bob_7 ", Password: "  longenough99 ", DisplayName: "  Sam Lee ", Email: " Bob@Example.com ",
		Phone: "+1 (555) 123-4567", Timezone: " Asia/Shanghai ", Locale: "fr_fr", AvatarURL: " /a.png ",
	}
	got, err := acc.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "bob_7" || got.DisplayName != "Sam Lee" || got.Email != "Bob@Example.com" ||
		got.Phone != "+15551234567" || got.Timezone != "Asia/Shanghai" || got.Locale != "fr-FR" || got.AvatarURL != "/a.png" {
		t.Fatalf("got %+v", got)
	}
	var cred Credential
	env.DB.Where("user_id = ?", got.ID).Take(&cred)
	if bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte("longenough99")) != nil {
		t.Fatal("trimmed password must be what is hashed")
	}
	if bcrypt.CompareHashAndPassword([]byte(cred.PasswordHash), []byte("  longenough99 ")) == nil {
		t.Fatal("untrimmed password must not verify")
	}
}

func TestCreateBlankDisplayNameDefaultsToUsername(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	in := validCreate()
	in.DisplayName = "   "
	got, err := acc.Create(context.Background(), in)
	if err != nil || got.DisplayName != "newbie" {
		t.Fatalf("%+v %v", got, err)
	}
	in = validCreate()
	in.Username, in.DisplayName = "abc", ""
	if got, err = acc.Create(context.Background(), in); err != nil || got.DisplayName != "abc" {
		t.Fatalf("3-char username default: %+v %v", got, err)
	}
	in.Username = "sixteen_chars_ok"
	if got, err = acc.Create(context.Background(), in); err != nil || got.DisplayName != "sixteen_chars_ok" {
		t.Fatalf("16-char username default: %+v %v", got, err)
	}
}

func TestCreateDuplicateUsername(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	in := validCreate()
	in.Username = "bob_7"
	if _, err := acc.Create(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	in.Username = "Bob_7"
	if _, err := acc.Create(context.Background(), in); !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("got %v", err)
	}
	var n int64
	env.DB.Model(&Account{}).Count(&n)
	if n != 1 {
		t.Fatalf("accounts: %d", n)
	}
}

func TestCreateRaceForSameUsernameYieldsOneWinner(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Exec("CREATE UNIQUE INDEX idx_identity_users_username ON identity_users(username)").Error; err != nil {
		t.Fatal(err)
	}
	acc := newAccounts(env, nil)
	const racers = 6
	results := make(chan error, racers)
	for i := 0; i < racers; i++ {
		go func() {
			in := validCreate()
			in.Username = "racer"
			_, err := acc.Create(context.Background(), in)
			results <- err
		}()
	}
	wins := 0
	for i := 0; i < racers; i++ {
		err := <-results
		switch {
		case err == nil:
			wins++
		case errors.Is(err, ErrUsernameExists):
		default:
			t.Errorf("unexpected loser outcome: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d winners", wins)
	}
}

func TestTranslateWriteClassifiesDuplicates(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Exec("CREATE UNIQUE INDEX idx_identity_users_username ON identity_users(username)").Error; err != nil {
		t.Fatal(err)
	}
	seedAccount(t, env.DB, "racer")
	dup := env.DB.Create(&Account{PublicID: newPublicID(), Username: "racer", Role: RoleUser, Status: StatusActive}).Error
	if got := translateWrite(dup, nil); !errors.Is(got, ErrUsernameExists) && !errors.Is(got, ErrDuplicate) {
		t.Fatalf("got %v", got)
	}
	if got := translateWrite(dup, ErrUsernameExists); !errors.Is(got, ErrUsernameExists) {
		t.Fatalf("fallback: %v", got)
	}
	plain := errors.New("disk full")
	if got := translateWrite(plain, ErrUsernameExists); got != plain {
		t.Fatalf("non-duplicate errors pass through: %v", got)
	}
	if translateWrite(nil, nil) != nil {
		t.Fatal("nil stays nil")
	}
	identity := fmt.Errorf("%w: UNIQUE constraint failed: identity_user_links.provider_id, identity_user_links.provider_subject", database.ErrDuplicate)
	if got := translateWrite(identity, ErrUsernameExists); !errors.Is(got, ErrDuplicateIdentity) {
		t.Fatalf("identity: %v", got)
	}
}

func TestCreateValidationOrder(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	seedAccount(t, env.DB, "taken")
	allBad := CreateInput{Username: "ab", Password: "1", AvatarURL: "javascript:x", DisplayName: "x", Email: "bad", Phone: "x", Timezone: "Mars/Base", Locale: "e"}
	steps := []struct {
		mut  func(*CreateInput)
		want error
	}{
		{func(in *CreateInput) {}, ErrInvalidUsername},
		{func(in *CreateInput) { in.Username = "taken" }, ErrUsernameExists},
		{func(in *CreateInput) { in.Username = "fresh1" }, ErrInvalidPassword},
		{func(in *CreateInput) { in.Password = "longenough99" }, ErrInvalidAvatarURL},
		{func(in *CreateInput) { in.AvatarURL = "" }, ErrInvalidDisplayName},
		{func(in *CreateInput) { in.DisplayName = "Fine Name" }, ErrInvalidEmail},
		{func(in *CreateInput) { in.Email = "a@b.co" }, ErrInvalidPhone},
		{func(in *CreateInput) { in.Phone = "123456" }, ErrInvalidTimeZone},
		{func(in *CreateInput) { in.Timezone = "UTC" }, ErrInvalidLocale},
	}
	for i, s := range steps {
		s.mut(&allBad)
		if _, err := acc.Create(context.Background(), allBad); !errors.Is(err, s.want) {
			t.Fatalf("step %d: got %v, want %v", i, err, s.want)
		}
	}
	allBad.Locale = "en"
	if _, err := acc.Create(context.Background(), allBad); err != nil {
		t.Fatalf("final: %v", err)
	}
}

func TestCreateRejectsFileAvatars(t *testing.T) {
	env := newEnv(t, Ports{})
	imgs := newFakeImages()
	acc := newAccounts(env, imgs)
	in := validCreate()
	in.AvatarURL = "file:file_demo_9"
	if _, err := acc.Create(context.Background(), in); !errors.Is(err, ErrInvalidAvatarURL) {
		t.Fatalf("got %v", err)
	}
	for i, ok := range []string{"", "/a.png", "generated:github:octocat", "https://example.com/a.png"} {
		in.AvatarURL = ok
		in.Username = "usr" + string(rune('a'+i))
		if _, err := acc.Create(context.Background(), in); err != nil {
			t.Errorf("avatar %q: %v", ok, err)
		}
	}
	if imgs.calls != 0 {
		t.Fatal("image checker must not be consulted on creation")
	}
}

func TestCreateWithoutBillingModeIgnoresTier(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	for i, mode := range []string{"", "usage", "self", "PERIODIC", "bogus"} {
		in := validCreate()
		in.Username = "mode" + string(rune('a'+i))
		in.BillingMode, in.SubscriptionTier = mode, "nonexistent"
		if _, err := acc.Create(context.Background(), in); err != nil {
			t.Errorf("mode %q: %v", mode, err)
		}
	}
	var n int64
	env.DB.Model(&subscriptionRow{}).Count(&n)
	if n != 0 {
		t.Fatalf("subscriptions: %d", n)
	}
}

func TestCreatePeriodSubscriptionRules(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	pro, proPrice := seedPlan(t, env.DB, "pro", true, "month", true, true)
	seedPlan(t, env.DB, "retired", false, "month", true, true)
	seedPlan(t, env.DB, "noprice", true, "month", false, true)
	seedPlan(t, env.DB, "nodefault", true, "month", true, false)
	now := env.Clock.Now()
	future := now.Add(48 * time.Hour).In(time.FixedZone("x", 5*3600))
	past := now.Add(-time.Hour)

	mk := func(name, tier string, exp *time.Time) CreateInput {
		in := validCreate()
		in.Username, in.BillingMode, in.SubscriptionTier, in.SubscriptionExpiresAt = name, " Period ", tier, exp
		return in
	}
	if _, err := acc.Create(ctx, mk("p_noexp", "pro", nil)); !errors.Is(err, ErrSubscriptionExpiryMissing) {
		t.Fatalf("no expiry: %v", err)
	}
	if _, err := acc.Create(ctx, mk("p_past", "pro", &past)); !errors.Is(err, ErrInvalidSubscriptionExpiry) {
		t.Fatalf("past: %v", err)
	}
	if _, err := acc.Create(ctx, mk("p_now", "pro", &now)); !errors.Is(err, ErrInvalidSubscriptionExpiry) {
		t.Fatalf("expiry equal to now is not strictly future: %v", err)
	}
	for _, tier := range []string{"unknown", "retired", "noprice", "nodefault"} {
		if _, err := acc.Create(ctx, mk("p_"+tier, tier, &future)); !errors.Is(err, ErrInvalidSubscriptionTier) {
			t.Fatalf("tier %s: %v", tier, err)
		}
	}
	var accounts int64
	env.DB.Model(&Account{}).Count(&accounts)
	if accounts != 0 {
		t.Fatalf("failed creates left %d accounts", accounts)
	}

	got, err := acc.Create(ctx, mk("p_ok", " PRO ", &future))
	if err != nil {
		t.Fatal(err)
	}
	var sub subscriptionRow
	if err := env.DB.Where("user_id = ?", got.ID).Take(&sub).Error; err != nil {
		t.Fatal(err)
	}
	if sub.PlanID != pro.ID || sub.PriceID != proPrice.ID || sub.Status != "active" ||
		!sub.StartAt.Equal(now) || !sub.CurrentPeriodStartAt.Equal(now) || sub.CancelAtPeriodEnd || sub.CanceledAt != nil || sub.AutoRenew {
		t.Fatalf("subscription: %+v", sub)
	}
	if sub.CurrentPeriodEndAt == nil || !sub.CurrentPeriodEndAt.Equal(future) || sub.CurrentPeriodEndAt.UTC().Location() != time.UTC && sub.CurrentPeriodEndAt.Location().String() == "x" {
		t.Fatalf("end: %v", sub.CurrentPeriodEndAt)
	}
}

func TestCreateFreeSubscription(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	free, _ := seedPlan(t, env.DB, "free", true, "month", true, true)

	in := validCreate()
	in.BillingMode, in.SubscriptionTier = "period", "   "
	got, err := acc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	var sub subscriptionRow
	env.DB.Where("user_id = ?", got.ID).Take(&sub)
	if sub.PlanID != free.ID || sub.CurrentPeriodEndAt != nil || !sub.AutoRenew || sub.Status != "active" {
		t.Fatalf("free sub: %+v", sub)
	}

	// A lifetime default price turns auto-renew off; an expiry is ignored for free.
	env.DB.Model(&priceRow{}).Where("plan_id = ?", free.ID).Update("billing_interval", "lifetime")
	sub = subscriptionRow{}
	in.Username = "lifer"
	exp := env.Clock.Now().Add(time.Hour)
	in.SubscriptionExpiresAt = &exp
	got, err = acc.Create(ctx, in)
	if err != nil {
		t.Fatal(err)
	}
	env.DB.Where("user_id = ?", got.ID).Take(&sub)
	if sub.AutoRenew || sub.CurrentPeriodEndAt != nil {
		t.Fatalf("lifetime free sub: %+v", sub)
	}
}

func TestCreateIsAtomicWhenSubscriptionInsertFails(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	seedPlan(t, env.DB, "free", true, "month", true, true)
	if err := env.DB.Exec("DROP TABLE billing_subscriptions").Error; err != nil {
		t.Fatal(err)
	}
	in := validCreate()
	in.BillingMode = "period"
	if _, err := acc.Create(context.Background(), in); err == nil {
		t.Fatal("expected failure")
	}
	var accounts, creds int64
	env.DB.Model(&Account{}).Count(&accounts)
	env.DB.Model(&Credential{}).Count(&creds)
	if accounts != 0 || creds != 0 {
		t.Fatalf("not atomic: %d accounts, %d credentials", accounts, creds)
	}
}

func TestCreateLongPasswordIsStoredVerifiably(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	in := validCreate()
	in.Password = strings.Repeat("ab", 64) // 128 characters, more than bcrypt's 72 bytes
	got, err := acc.Create(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	var cred Credential
	env.DB.Where("user_id = ?", got.ID).Take(&cred)
	if cred.PasswordHash == "" || cred.PasswordHash == in.Password {
		t.Fatal("password must be hashed")
	}
	in.Password = strings.Repeat("ab", 64) + "x"
	in.Username = "toolong"
	if _, err := acc.Create(context.Background(), in); !errors.Is(err, ErrInvalidPassword) {
		t.Fatalf("129 characters: %v", err)
	}
}

func TestHashCostIsTwelveInProduction(t *testing.T) {
	if bcryptCost != 12 {
		t.Fatalf("bcryptCost = %d", bcryptCost)
	}
	h, err := hashPassword("longenough99")
	if err != nil {
		t.Fatal(err)
	}
	if cost, _ := bcrypt.Cost([]byte(h)); cost != 12 {
		t.Fatalf("cost %d", cost)
	}
}

func TestGetByIDAndPublicID(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	u := seedAccount(t, env.DB, "findme")
	if got, err := acc.GetByID(ctx, u.ID); err != nil || got.Username != "findme" {
		t.Fatalf("%v %v", got, err)
	}
	if got, err := acc.GetByPublicID(ctx, " "+u.PublicID+" "); err != nil || got.ID != u.ID {
		t.Fatalf("%v %v", got, err)
	}
	for _, id := range []string{"", "   ", "nope"} {
		if _, err := acc.GetByPublicID(ctx, id); !errors.Is(err, ErrNotFound) {
			t.Errorf("public id %q: %v", id, err)
		}
	}
	if _, err := acc.GetByID(ctx, 999); !errors.Is(err, ErrNotFound) {
		t.Fatal("unknown id")
	}
	env.DB.Delete(&Account{}, u.ID)
	if _, err := acc.GetByID(ctx, u.ID); !errors.Is(err, ErrNotFound) {
		t.Fatal("soft-deleted accounts are excluded")
	}
	_ = testkit.User
}
