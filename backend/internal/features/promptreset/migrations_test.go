package promptreset

import (
	"context"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
)

func TestSchemaAndUniqueConstraint(t *testing.T) {
	db := dbtest.New(t)
	for _, s := range Migrations() {
		if err := s.Up(context.Background(), db); err != nil {
			t.Fatalf("%s: %v", s.Name, err)
		}
	}
	m := db.Migrator()
	for _, col := range []string{"id", "created_at", "updated_at", "scope", "owner_user_id", "title", "trigger",
		"description", "content", "enabled", "sort_order", "created_by_user_id", "updated_by_user_id"} {
		if !m.HasColumn(&PromptPreset{}, col) {
			t.Fatalf("missing column %s", col)
		}
	}
	if m.HasColumn(&PromptPreset{}, "deleted_at") {
		t.Fatal("hard-delete table must not have deleted_at")
	}
	// Defaults come from the column definitions.
	if err := db.Exec(`INSERT INTO prompt_presets (created_at, updated_at) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP)`).Error; err != nil {
		t.Fatal(err)
	}
	var row PromptPreset
	if err := db.First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Scope != "user" || row.OwnerUserID != 0 || row.Title != "" || !row.Enabled || row.SortOrder != 0 {
		t.Fatalf("defaults: %+v", row)
	}
	// The unique constraint spans scope, owner and trigger.
	ins := func(scope string, owner uint, trig string) error {
		return db.Create(&PromptPreset{Scope: scope, OwnerUserID: owner, Trigger: trig, Title: "t", Content: "c"}).Error
	}
	if err := ins("user", 1, "x"); err != nil {
		t.Fatal(err)
	}
	if err := ins("user", 1, "x"); err == nil {
		t.Fatal("duplicate (scope, owner, trigger) accepted")
	}
	for _, c := range []struct {
		scope string
		owner uint
		trig  string
	}{{"user", 2, "x"}, {"builtin", 0, "x"}, {"user", 1, "X"}} {
		if err := ins(c.scope, c.owner, c.trig); err != nil {
			t.Fatalf("%+v: %v", c, err)
		}
	}
}

// Scenario 16.
func TestLegacyColumnsAreDroppedIdempotently(t *testing.T) {
	db := dbtest.New(t)
	ctx := context.Background()
	// A database created by an older release: today's table plus the retired columns.
	if err := db.AutoMigrate(&PromptPreset{}); err != nil {
		t.Fatal(err)
	}
	stmts := []string{
		`ALTER TABLE prompt_presets ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0`,
		`ALTER TABLE prompt_presets ADD COLUMN last_used_at DATETIME`,
		`ALTER TABLE prompt_presets ADD COLUMN category VARCHAR(32) NOT NULL DEFAULT ''`,
		`ALTER TABLE prompt_presets ADD COLUMN tags_json TEXT`,
		`CREATE INDEX idx_prompt_presets_category ON prompt_presets(category)`,
		`INSERT INTO prompt_presets (created_at, updated_at, scope, owner_user_id, title, "trigger", content, category) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 'user', 3, 'Keep', 'keep', 'body', 'old')`,
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	for run := 1; run <= 2; run++ {
		for _, s := range Migrations() {
			if err := s.Up(ctx, db); err != nil {
				t.Fatalf("run %d step %s: %v", run, s.Name, err)
			}
		}
		for _, col := range legacyColumns {
			if db.Migrator().HasColumn(&PromptPreset{}, col) {
				t.Fatalf("run %d: legacy column %s still present", run, col)
			}
		}
	}
	var row PromptPreset
	if err := db.Where(`"trigger" = ?`, "keep").First(&row).Error; err != nil {
		t.Fatal(err)
	}
	if row.Title != "Keep" || row.OwnerUserID != 3 || row.Content != "body" {
		t.Fatalf("data lost: %+v", row)
	}
}

func TestMigrationsAreNamedUniquely(t *testing.T) {
	seen := map[string]bool{}
	for _, s := range Migrations() {
		if seen[s.Name] {
			t.Fatalf("duplicate step %s", s.Name)
		}
		seen[s.Name] = true
	}
}
