package user

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

func page(p, size int) pagination.Page { return pagination.Page{Page: p, PageSize: size} }

func usernames(rows []Account) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Username
	}
	return out
}

func TestListPaginatesNewestFirst(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	for i := 1; i <= 25; i++ {
		seedAccount(t, env.DB, fmt.Sprintf("user%02d", i))
	}
	rows, total, err := acc.List(context.Background(), ListFilter{}, pagination.Parse("", ""))
	if err != nil || total != 25 || len(rows) != 20 {
		t.Fatalf("%d rows, total %d, %v", len(rows), total, err)
	}
	if rows[0].Username != "user25" || rows[19].Username != "user06" {
		t.Fatalf("order: %v", usernames(rows))
	}
	rows, total, _ = acc.List(context.Background(), ListFilter{}, pagination.Parse("2", ""))
	if total != 25 || len(rows) != 5 || rows[0].Username != "user05" || rows[4].Username != "user01" {
		t.Fatalf("page 2: %v total %d", usernames(rows), total)
	}
	rows, total, _ = acc.List(context.Background(), ListFilter{}, pagination.Parse("9", "10"))
	if total != 25 || len(rows) != 0 {
		t.Fatalf("beyond the end: %d rows, total %d", len(rows), total)
	}
	rows, _, _ = acc.List(context.Background(), ListFilter{}, pagination.Parse("1", "5000"))
	if len(rows) != 25 {
		t.Fatalf("size cap should still return everything here: %d", len(rows))
	}
}

func TestListExcludesSoftDeletedAndHandlesEmpty(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	rows, total, err := acc.List(context.Background(), ListFilter{}, page(1, 20))
	if err != nil || total != 0 || len(rows) != 0 {
		t.Fatalf("empty: %v %d %v", rows, total, err)
	}
	a := seedAccount(t, env.DB, "alice")
	seedAccount(t, env.DB, "bobby")
	env.DB.Delete(&Account{}, a.ID)
	rows, total, _ = acc.List(context.Background(), ListFilter{}, page(1, 20))
	if total != 1 || rows[0].Username != "bobby" {
		t.Fatalf("%v %d", usernames(rows), total)
	}
}

func TestListFreeTextQueryMatchesFourFieldsCaseInsensitively(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	byName := seedAccount(t, env.DB, "alice_one")
	byDisplay := seedAccount(t, env.DB, "bobby", func(a *Account) { a.DisplayName = "Big ALIce" })
	byEmail := seedAccount(t, env.DB, "carol", func(a *Account) { a.Email = "Malik@example.com" })
	byPublic := seedAccount(t, env.DB, "dave", func(a *Account) { a.PublicID = "00ali00000000000000000000000beef" })
	seedAccount(t, env.DB, "erin", func(a *Account) { a.Phone = "ali" }) // phone is not searched

	rows, total, err := acc.List(ctx, ListFilter{Query: "  ALI "}, page(1, 20))
	if err != nil || total != 4 {
		t.Fatalf("total %d err %v: %v", total, err, usernames(rows))
	}
	got := map[uint]bool{}
	for _, r := range rows {
		got[r.ID] = true
	}
	for _, want := range []Account{byName, byDisplay, byEmail, byPublic} {
		if !got[want.ID] {
			t.Errorf("%s missing", want.Username)
		}
	}
	if _, total, _ := acc.List(ctx, ListFilter{Query: "   "}, page(1, 20)); total != 5 {
		t.Fatalf("blank query is no filter: %d", total)
	}
}

func TestListSearchRunsBeforePagination(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	for i := 0; i < 60; i++ {
		name := fmt.Sprintf("filler%02d", i)
		if i == 2 {
			name = "needle"
		}
		seedAccount(t, env.DB, name)
	}
	rows, total, _ := acc.List(context.Background(), ListFilter{Query: "needle"}, page(1, 20))
	if total != 1 || len(rows) != 1 || rows[0].Username != "needle" {
		t.Fatalf("%v total %d", usernames(rows), total)
	}
	// Page 6 of the unfiltered list contains it, page 1 of the filtered does too.
	rows, _, _ = acc.List(context.Background(), ListFilter{}, page(6, 10))
	found := false
	for _, r := range rows {
		found = found || r.Username == "needle"
	}
	if !found {
		t.Fatalf("test setup: needle not on page 6: %v", usernames(rows))
	}
}

func TestListWildcardsMatchLiterally(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	lit := seedAccount(t, env.DB, "lit", func(a *Account) { a.DisplayName = "10%_x" })
	seedAccount(t, env.DB, "decoy1", func(a *Account) { a.DisplayName = "10abx" })
	seedAccount(t, env.DB, "decoy2", func(a *Account) { a.DisplayName = "10zzzzx" })
	slash := seedAccount(t, env.DB, "slashy", func(a *Account) { a.DisplayName = `a\b` })
	seedAccount(t, env.DB, "decoy3", func(a *Account) { a.DisplayName = "ab" })

	for q, want := range map[string]Account{"10%_x": lit, `a\b`: slash, "%_": lit} {
		rows, total, err := acc.List(ctx, ListFilter{Query: q}, page(1, 20))
		if err != nil || total != 1 || rows[0].ID != want.ID {
			t.Errorf("query %q: %v total %d err %v", q, usernames(rows), total, err)
		}
	}
	for _, q := range []string{"%", "_", `\`} {
		rows, total, err := acc.List(ctx, ListFilter{Query: q}, page(1, 20))
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if !strings.Contains(strings.ToLower(r.Username+r.DisplayName+r.Email+r.PublicID), q) {
				t.Errorf("query %q matched %s", q, r.Username)
			}
		}
		_ = total
	}
}

func TestListProviderSlugFilter(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	gh := providerRow{PublicID: newPublicID(), Type: "oauth2", Name: "GitHub", Slug: "github", SortOrder: 2}
	okta := providerRow{PublicID: newPublicID(), Type: "oidc", Name: "Okta", Slug: "okta", SortOrder: 1}
	env.DB.Create(&gh)
	env.DB.Create(&okta)
	u1 := seedAccount(t, env.DB, "user1")
	u2 := seedAccount(t, env.DB, "user2")
	seedAccount(t, env.DB, "user3")
	now := env.Clock.Now()
	env.DB.Create(&linkRow{UserID: u1.ID, ProviderID: gh.ID, ProviderSubject: "s1", LinkedAt: now})
	env.DB.Create(&linkRow{UserID: u2.ID, ProviderID: okta.ID, ProviderSubject: "s2", LinkedAt: now})
	env.DB.Create(&linkRow{UserID: u2.ID, ProviderID: gh.ID, ProviderSubject: "s3", LinkedAt: now})

	rows, total, _ := acc.List(ctx, ListFilter{ProviderSlug: "github"}, page(1, 20))
	if total != 2 || rows[0].ID != u2.ID || rows[1].ID != u1.ID {
		t.Fatalf("github: %v total %d", usernames(rows), total)
	}
	rows, total, _ = acc.List(ctx, ListFilter{ProviderSlug: "okta"}, page(1, 20))
	if total != 1 || rows[0].ID != u2.ID {
		t.Fatalf("okta: %v", usernames(rows))
	}
	if _, total, _ = acc.List(ctx, ListFilter{ProviderSlug: "GitHub"}, page(1, 20)); total != 0 {
		t.Fatal("slug match is exact")
	}
	if _, total, _ = acc.List(ctx, ListFilter{ProviderSlug: "nope"}, page(1, 20)); total != 0 {
		t.Fatal("unknown slug")
	}
	// A deleted link no longer counts.
	env.DB.Where("provider_subject = ?", "s1").Delete(&linkRow{})
	if _, total, _ = acc.List(ctx, ListFilter{ProviderSlug: "github"}, page(1, 20)); total != 1 {
		t.Fatalf("after unlink: %d", total)
	}
}

func TestListSubscriptionFilters(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	free, freePrice := seedPlan(t, env.DB, "free", true, "month", true, true)
	pro, proPrice := seedPlan(t, env.DB, "pro", true, "month", true, true)
	now := env.Clock.Now()
	day := 24 * time.Hour
	end := func(d time.Duration) *time.Time { e := now.Add(d); return &e }

	paid := seedAccount(t, env.DB, "paiduser")
	openEnded := seedAccount(t, env.DB, "openended")
	expired := seedAccount(t, env.DB, "expireduser")
	future := seedAccount(t, env.DB, "futureuser")
	canceled := seedAccount(t, env.DB, "canceleduser")
	onFree := seedAccount(t, env.DB, "freeuser")
	none := seedAccount(t, env.DB, "nosubuser")
	_ = none
	mk := func(u Account, p planRow, pr priceRow, status string, start time.Time, e *time.Time) {
		env.DB.Create(&subscriptionRow{UserID: u.ID, PlanID: p.ID, PriceID: pr.ID, Status: status, StartAt: start, CurrentPeriodStartAt: start, CurrentPeriodEndAt: e})
	}
	mk(paid, pro, proPrice, "active", now.Add(-day), end(day))
	mk(openEnded, pro, proPrice, "active", now.Add(-day), nil)
	mk(expired, pro, proPrice, "active", now.Add(-3*day), end(-day))
	mk(future, pro, proPrice, "active", now.Add(day), end(2*day))
	mk(canceled, pro, proPrice, "canceled", now.Add(-day), end(day))
	mk(onFree, free, freePrice, "active", now.Add(-day), nil)

	names := func(f ListFilter) map[string]bool {
		rows, _, err := acc.List(ctx, f, page(1, 100))
		if err != nil {
			t.Fatal(err)
		}
		m := map[string]bool{}
		for _, r := range rows {
			m[r.Username] = true
		}
		return m
	}
	active := names(ListFilter{SubscriptionStatus: "active"})
	if len(active) != 2 || !active["paiduser"] || !active["openended"] {
		t.Fatalf("active: %v", active)
	}
	freeSet := names(ListFilter{SubscriptionStatus: "free"})
	if len(freeSet) != 5 || freeSet["paiduser"] || freeSet["openended"] || !freeSet["freeuser"] || !freeSet["nosubuser"] || !freeSet["expireduser"] || !freeSet["futureuser"] || !freeSet["canceleduser"] {
		t.Fatalf("free: %v", freeSet)
	}
	cancel := names(ListFilter{SubscriptionStatus: "canceled"})
	if len(cancel) != 1 || !cancel["canceleduser"] {
		t.Fatalf("canceled: %v", cancel)
	}
	if got := names(ListFilter{SubscriptionStatus: "never-used-status"}); len(got) != 0 {
		t.Fatalf("unknown status: %v", got)
	}
	// Combined with other filters (AND).
	if got := names(ListFilter{SubscriptionStatus: "active", Query: "paid"}); len(got) != 1 || !got["paiduser"] {
		t.Fatalf("combined: %v", got)
	}
	// Advance the clock: the open-ended and the 1-day subscription's state changes.
	env.Clock.Advance(day + time.Hour)
	active = names(ListFilter{SubscriptionStatus: "active"})
	if len(active) != 2 || !active["openended"] || !active["futureuser"] {
		t.Fatalf("active later: %v", active)
	}
	_ = pro
}

func TestIdentitiesForUsers(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	if got, err := acc.IdentitiesForUsers(ctx, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty input: %v %v", got, err)
	}
	gh := providerRow{PublicID: newPublicID(), Type: "oauth2", Name: "GitHub", Slug: "github", LogoURL: "https://x/gh.png", SortOrder: 2}
	okta := providerRow{PublicID: newPublicID(), Type: "oidc", Name: "Okta", Slug: "okta", SortOrder: 1}
	env.DB.Create(&gh)
	env.DB.Create(&okta)
	u1 := seedAccount(t, env.DB, "user1")
	u2 := seedAccount(t, env.DB, "user2")
	u3 := seedAccount(t, env.DB, "user3")
	now := env.Clock.Now()
	env.DB.Create(&linkRow{UserID: u1.ID, ProviderID: gh.ID, ProviderSubject: "s1", LinkedAt: now})
	env.DB.Create(&linkRow{UserID: u1.ID, ProviderID: okta.ID, ProviderSubject: "s2", LinkedAt: now})
	env.DB.Create(&linkRow{UserID: u2.ID, ProviderID: gh.ID, ProviderSubject: "s3", LinkedAt: now})

	got, err := acc.IdentitiesForUsers(ctx, []uint{u1.ID, u2.ID, u3.ID})
	if err != nil || len(got) != 2 {
		t.Fatalf("%v %v", got, err)
	}
	if l := got[u1.ID]; len(l) != 2 || l[0].ProviderSlug != "okta" || l[1].ProviderSlug != "github" || l[1].ProviderLogoURL != "https://x/gh.png" ||
		l[1].ProviderPublicID != gh.PublicID || l[1].ProviderType != "oauth2" || l[1].ProviderName != "GitHub" || l[1].ProviderID != gh.ID {
		t.Fatalf("u1: %+v", l)
	}
	if len(got[u2.ID]) != 1 || len(got[u3.ID]) != 0 {
		t.Fatalf("grouping: %+v", got)
	}
	if only, _ := acc.IdentitiesForUsers(ctx, []uint{u2.ID}); len(only) != 1 {
		t.Fatal("only the requested users")
	}
}

func TestLatestSessionActivity(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	if got, err := acc.LatestSessionActivity(ctx, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	u1 := seedAccount(t, env.DB, "user1")
	u2 := seedAccount(t, env.DB, "user2")
	u3 := seedAccount(t, env.DB, "user3")
	base := env.Clock.Now()
	at := func(h int) time.Time { return base.Add(time.Duration(h) * time.Hour) }
	seen := at(5)
	revoked := at(9)
	exp := at(100)
	env.DB.Create(&sessionRow{SessionID: "a", UserID: u1.ID, IssuedAt: at(1), LastSeenAt: &seen, ExpiresAt: exp})
	env.DB.Create(&sessionRow{SessionID: "b", UserID: u1.ID, IssuedAt: at(3), ExpiresAt: exp}) // no last-seen: issued time
	env.DB.Create(&sessionRow{SessionID: "c", UserID: u2.ID, IssuedAt: at(2), ExpiresAt: at(-50), RevokedAt: &revoked})
	env.DB.Create(&sessionRow{SessionID: "d", UserID: u2.ID, IssuedAt: at(1), ExpiresAt: exp})

	got, err := acc.LatestSessionActivity(ctx, []uint{u1.ID, u2.ID, u3.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !got[u1.ID].Equal(seen) {
		t.Fatalf("u1 latest = %v, want %v", got[u1.ID], seen)
	}
	if !got[u2.ID].Equal(at(2)) {
		t.Fatalf("u2 latest = %v (revoked and expired sessions count)", got[u2.ID])
	}
	if _, ok := got[u3.ID]; ok {
		t.Fatal("users without sessions are absent")
	}
}

func TestUsersByEmails(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	if got, err := acc.UsersByEmails(ctx, nil); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("nil: %v %v", got, err)
	}
	if got, _ := acc.UsersByEmails(ctx, []string{"", "  ", "\t"}); len(got) != 0 {
		t.Fatalf("blank: %v", got)
	}
	a := seedAccount(t, env.DB, "alice", func(x *Account) { x.Email = "Alice@Example.com" })
	seedAccount(t, env.DB, "bobby", func(x *Account) { x.Email = "bob@example.com" })
	got, err := acc.UsersByEmails(ctx, []string{" ALICE@example.COM ", "alice@example.com", "nobody@example.com", ""})
	if err != nil || len(got) != 1 || got["alice@example.com"].ID != a.ID {
		t.Fatalf("%v %v", got, err)
	}
	// Shared emails return one of the accounts.
	seedAccount(t, env.DB, "alias", func(x *Account) { x.Email = "ALICE@example.com" })
	got, _ = acc.UsersByEmails(ctx, []string{"alice@example.com"})
	if len(got) != 1 || (got["alice@example.com"].Username != "alice" && got["alice@example.com"].Username != "alias") {
		t.Fatalf("shared: %v", got)
	}
}

func TestAllUsernames(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	if got, err := acc.AllUsernames(ctx); err != nil || got == nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	seedAccount(t, env.DB, "alice")
	gone := seedAccount(t, env.DB, "gone")
	env.DB.Delete(&Account{}, gone.ID)
	got, _ := acc.AllUsernames(ctx)
	if len(got) != 2 {
		t.Fatalf("%v", got)
	}
	if _, ok := got["alice"]; !ok {
		t.Fatal("alice missing")
	}
	if _, ok := got["gone"]; !ok {
		t.Fatal("soft-deleted names stay reserved by the storage uniqueness rule")
	}
}

func TestAuthEventsRecordAndList(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	rec := func(uid uint, typ, result string) {
		t.Helper()
		env.Clock.Advance(time.Second)
		err := acc.RecordAuthEvent(ctx, AuthEvent{RequestID: "req", UserID: uid, EventType: typ, Result: result, Reason: "r",
			ClientIP: "1.2.3.4", UserAgent: "ua", DetailJSON: `{"k":1}`})
		if err != nil {
			t.Fatal(err)
		}
	}
	rec(1, "login", "success")
	rec(1, "login", "failure")
	rec(2, "login", "success")
	rec(0, "refresh", "failure") // not attributable to an account
	rec(1, "password_change", "success")

	rows, total, err := acc.ListAuthEvents(ctx, AuthEventFilter{}, page(1, 20))
	if err != nil || total != 5 || len(rows) != 5 {
		t.Fatalf("%d %d %v", len(rows), total, err)
	}
	for i := 1; i < len(rows); i++ {
		if rows[i-1].OccurredAt.Before(rows[i].OccurredAt) {
			t.Fatal("not newest first")
		}
	}
	if rows[0].EventType != "password_change" || rows[0].RequestID != "req" || rows[0].DetailJSON != `{"k":1}` || rows[0].ClientIP != "1.2.3.4" || rows[0].UserAgent != "ua" || rows[0].Reason != "r" {
		t.Fatalf("stored fields: %+v", rows[0])
	}
	if !rows[0].OccurredAt.Equal(env.Clock.Now()) {
		t.Fatalf("occurred_at %v", rows[0].OccurredAt)
	}

	check := func(f AuthEventFilter, want int) {
		t.Helper()
		got, total, err := acc.ListAuthEvents(ctx, f, page(1, 20))
		if err != nil || int(total) != want || len(got) != want {
			t.Fatalf("%+v: %d rows total %d err %v, want %d", f, len(got), total, err, want)
		}
	}
	check(AuthEventFilter{UserID: 1}, 3)
	check(AuthEventFilter{EventType: " login "}, 3)
	check(AuthEventFilter{Result: "failure"}, 2)
	check(AuthEventFilter{UserID: 1, EventType: "login", Result: "success"}, 1)
	check(AuthEventFilter{EventType: "Login"}, 0)
	check(AuthEventFilter{UserID: 99}, 0)

	small, total, _ := acc.ListAuthEvents(ctx, AuthEventFilter{}, page(2, 2))
	if total != 5 || len(small) != 2 {
		t.Fatalf("total must not depend on page size: %d %d", len(small), total)
	}
}

func TestAuthEventsWithEqualTimestampsAreOrderedByCreation(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	for i := 0; i < 4; i++ {
		acc.RecordAuthEvent(ctx, AuthEvent{EventType: fmt.Sprintf("e%d", i), Result: "success"})
	}
	rows, _, _ := acc.ListAuthEvents(ctx, AuthEventFilter{}, page(1, 10))
	if rows[0].EventType != "e3" || rows[3].EventType != "e0" {
		t.Fatalf("order: %s ... %s", rows[0].EventType, rows[3].EventType)
	}
}
