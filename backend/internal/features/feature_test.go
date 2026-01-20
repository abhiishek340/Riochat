package features

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

func TestValidate(t *testing.T) {
	ok := func(deps.Deps) error { return nil }
	good := []Feature{{Name: "announcement", Mount: ok}, {Name: "user_settings2", Mount: ok}}
	if err := Validate(good); err != nil {
		t.Fatal(err)
	}
	bad := map[string][]Feature{
		"empty name":  {{Name: "", Mount: ok}},
		"upper":       {{Name: "Announce", Mount: ok}},
		"dash":        {{Name: "a-b", Mount: ok}},
		"digit first": {{Name: "1a", Mount: ok}},
		"duplicate":   {{Name: "a", Mount: ok}, {Name: "a", Mount: ok}},
		"no mount":    {{Name: "a"}},
	}
	for name, list := range bad {
		if Validate(list) == nil {
			t.Errorf("%s must be rejected", name)
		}
	}
}

func TestMigrationSets(t *testing.T) {
	step := func(string) migrate.Step {
		return migrate.Step{Name: "s", Up: func(context.Context, *gorm.DB) error { return nil }}
	}
	list := []Feature{
		{Name: "a", Mount: func(deps.Deps) error { return nil }, Migrations: func() []migrate.Step { return []migrate.Step{step("x")} }},
		{Name: "b", Mount: func(deps.Deps) error { return nil }},
		{Name: "c", Mount: func(deps.Deps) error { return nil }, Migrations: func() []migrate.Step { return nil }},
	}
	sets := MigrationSets(list)
	if len(sets) != 1 || sets[0].Area != "a" {
		t.Fatalf("sets: %+v", sets)
	}
}

func baseDeps(t *testing.T) (deps.Deps, *gin.Engine) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	e := gin.New()
	g := e.Group("/api/v1")
	return deps.Deps{Routes: deps.Routes{Public: g, Auth: g, Admin: g}, Logger: logx.Nop()}, e
}

func TestMountAllOrderAndScopedLogger(t *testing.T) {
	d, _ := baseDeps(t)
	var order []string
	mk := func(name string) Feature {
		return Feature{Name: name, Mount: func(d deps.Deps) error {
			if d.Logger == nil {
				t.Errorf("%s: no logger", name)
			}
			order = append(order, name)
			return nil
		}}
	}
	if err := MountAll(d, []Feature{mk("one"), mk("two"), mk("three")}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(order, ",") != "one,two,three" {
		t.Fatalf("order: %v", order)
	}
}

func TestMountAllReportsErrorsAndPanics(t *testing.T) {
	d, _ := baseDeps(t)
	boom := errors.New("boom")
	err := MountAll(d, []Feature{{Name: "bad", Mount: func(deps.Deps) error { return boom }}})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), `"bad"`) {
		t.Fatalf("error: %v", err)
	}
	err = MountAll(d, []Feature{{Name: "conflict", Mount: func(d deps.Deps) error {
		d.Routes.Public.GET("/x", func(*gin.Context) {})
		d.Routes.Public.GET("/x", func(*gin.Context) {}) // duplicate route panics inside gin
		return nil
	}}})
	if err == nil || !strings.Contains(err.Error(), `"conflict"`) || !strings.Contains(err.Error(), "panicked") {
		t.Fatalf("panic must become a named error: %v", err)
	}
}
