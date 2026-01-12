package migrate_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

type gadget struct {
	database.Model
	Name string `gorm:"size:32;uniqueIndex"`
}

func (gadget) TableName() string { return "system_gadgets" }

type badTable struct{ ID uint }

func (badTable) TableName() string { return "gadgets_unprefixed" }

type noTableName struct{ ID uint }

func TestSchemaBeforeSeedAcrossAreas(t *testing.T) {
	db := dbtest.New(t)
	var order []string
	rec := func(s string) func(context.Context, *gorm.DB) error {
		return func(context.Context, *gorm.DB) error { order = append(order, s); return nil }
	}
	sets := []migrate.Set{
		{Area: "a", Steps: []migrate.Step{
			migrate.Seed("a-seed", rec("a-seed")),
			{Name: "a-schema", Up: rec("a-schema")},
		}},
		{Area: "b", Steps: []migrate.Step{
			{Name: "b-schema", Phase: migrate.PhaseSchema, Up: rec("b-schema")},
			migrate.Seed("b-seed", rec("b-seed")),
		}},
	}
	if err := migrate.Run(context.Background(), db, nil, sets...); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(order, ","); got != "a-schema,b-schema,a-seed,b-seed" {
		t.Fatalf("order: %s", got)
	}
}

func TestRunIsIdempotent(t *testing.T) {
	db := dbtest.New(t)
	sets := []migrate.Set{{Area: "gadget", Steps: []migrate.Step{
		migrate.AutoMigrate("tables", &gadget{}),
		migrate.Exec("extra-index", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_gadget_name_x ON system_gadgets(name)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_gadget_name_x ON system_gadgets(name)",
		}),
		migrate.Exec("pg-only", migrate.PhaseSchema, map[string]string{"postgres": "SELECT 1/0"}),
		migrate.Seed("rows", func(ctx context.Context, db *gorm.DB) error {
			return db.Where(gadget{Name: "builtin"}).FirstOrCreate(&gadget{Name: "builtin"}).Error
		}),
	}}}
	for i := 0; i < 3; i++ {
		if err := migrate.Run(context.Background(), db, nil, sets...); err != nil {
			t.Fatalf("run %d: %v", i+1, err)
		}
	}
	var n int64
	db.Model(&gadget{}).Count(&n)
	if n != 1 {
		t.Fatalf("seed must not duplicate: %d rows", n)
	}
}

func TestFailureNamesAreaAndStep(t *testing.T) {
	db := dbtest.New(t)
	boom := errors.New("boom")
	err := migrate.Run(context.Background(), db, nil, migrate.Set{Area: "x", Steps: []migrate.Step{
		{Name: "bad", Up: func(context.Context, *gorm.DB) error { return boom }},
	}})
	if !errors.Is(err, boom) || !strings.Contains(err.Error(), "x/bad") {
		t.Fatalf("error: %v", err)
	}
}

func TestAutoMigrateEnforcesTableNames(t *testing.T) {
	db := dbtest.New(t)
	for _, model := range []any{&badTable{}, &noTableName{}} {
		err := migrate.Run(context.Background(), db, nil, migrate.Set{Area: "z", Steps: []migrate.Step{migrate.AutoMigrate("t", model)}})
		if err == nil {
			t.Errorf("%T must be rejected", model)
		}
	}
}

func TestValidation(t *testing.T) {
	db := dbtest.New(t)
	ok := func(context.Context, *gorm.DB) error { return nil }
	cases := map[string][]migrate.Set{
		"no area":        {{Steps: []migrate.Step{{Name: "a", Up: ok}}}},
		"duplicate area": {{Area: "a"}, {Area: "a"}},
		"dup step":       {{Area: "a", Steps: []migrate.Step{{Name: "s", Up: ok}, {Name: "s", Up: ok}}}},
		"nameless step":  {{Area: "a", Steps: []migrate.Step{{Up: ok}}}},
		"no up":          {{Area: "a", Steps: []migrate.Step{{Name: "s"}}}},
	}
	for name, sets := range cases {
		if err := migrate.Run(context.Background(), db, nil, sets...); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
}
