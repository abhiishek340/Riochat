package user

import (
	"testing"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestMountsThroughThePlainContract(t *testing.T) {
	env := testkit.New(t, []features.Feature{{Name: "user", Migrations: Migrations, Mount: Mount}})
	// Settings work even though the identity tables were never migrated: this
	// area only owns user_settings.
	testkit.Expect(t, env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(1)), nil), 200, "")
	if !env.DB.Migrator().HasTable("user_settings") {
		t.Fatal("user_settings missing")
	}
	for _, other := range []string{"identity_users", "billing_plans"} {
		if env.DB.Migrator().HasTable(other) {
			t.Fatalf("%s belongs to another area and must not be migrated here", other)
		}
	}
}

func TestFeatureDefinitionIsValid(t *testing.T) {
	list := []features.Feature{{Name: "user", Migrations: Migrations, Mount: func(d deps.Deps) error { return Mount(d) }}}
	if err := features.Validate(list); err != nil {
		t.Fatal(err)
	}
}

func TestMigrationsAreIdempotent(t *testing.T) {
	env := newEnv(t, Ports{})
	env.DB.Create(&Setting{UserID: 1, Key: "chat.file_mode", Value: "rag"})
	for i := 0; i < 2; i++ {
		for _, step := range Migrations() {
			if err := step.Up(t.Context(), env.DB); err != nil {
				t.Fatalf("run %d: %v", i, err)
			}
		}
	}
	var n int64
	env.DB.Model(&Setting{}).Count(&n)
	if n != 1 {
		t.Fatalf("rows: %d", n)
	}
}

func TestRoutesAreOnTheDocumentedGroups(t *testing.T) {
	env := newEnv(t, Ports{})
	// Avatar: public (no 401), the others need a bearer token.
	if w := env.Do("GET", "/api/v1/users/abc/avatar", nil, nil); w.Code == 401 {
		t.Fatal("avatar must be public")
	}
	for _, r := range [][2]string{{"GET", "/api/v1/user/settings"}, {"PATCH", "/api/v1/user/settings"}, {"GET", "/api/v1/user/stats/activity"}} {
		testkit.Expect(t, env.Do(r[0], r[1], nil, nil), 401, "auth.invalid_token")
	}
	// No management routes of its own.
	for _, r := range [][2]string{{"GET", "/api/v1/users"}, {"POST", "/api/v1/users"}, {"DELETE", "/api/v1/user/settings"}} {
		if w := env.Do(r[0], r[1], env.Bearer(testkit.Admin(1)), nil); w.Code != 404 && w.Code != 405 {
			t.Errorf("%s %s answered %d", r[0], r[1], w.Code)
		}
	}
}

func TestSettingsRoutesCanBeOmittedForCoexistenceWithAnotherArea(t *testing.T) {
	env := newEnv(t, Ports{OmitSettingsRoutes: true})
	if w := env.Do("GET", "/api/v1/user/settings", env.Bearer(testkit.User(1)), nil); w.Code != 404 {
		t.Fatalf("settings route should be absent, got %d", w.Code)
	}
	testkit.Expect(t, env.Do("GET", "/api/v1/user/stats/activity", env.Bearer(testkit.User(1)), nil), 500, "internal.error") // no ledger table, but the route exists
}
