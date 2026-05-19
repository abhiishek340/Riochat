package uicomponent

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func builtinRows(t *testing.T, env *testkit.Env) []UIComponent {
	t.Helper()
	var rows []UIComponent
	if err := env.DB.Where("scope = ?", ScopeBuiltin).Order("sort_order ASC").Find(&rows).Error; err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestBuiltinCatalogInvariants(t *testing.T) {
	cat := builtinCatalog()
	wantNames := []string{"card-grid", "chart", "function-plot", "stat-grid", "image-gallery", "gantt",
		"data-table", "calculator", "decision-tree", "diff", "quiz", "panel", "widget"}
	if len(cat) != len(wantNames) {
		t.Fatalf("catalog size %d", len(cat))
	}
	seen := map[string]bool{}
	imageRe := regexp.MustCompile(`/api/v1/files/`)
	for i, spec := range cat {
		if spec.Name != wantNames[i] {
			t.Errorf("position %d: %s, want %s", i, spec.Name, wantNames[i])
		}
		if seen[spec.Name] {
			t.Errorf("duplicate %s", spec.Name)
		}
		seen[spec.Name] = true
		// Switch rows are the only builtin names that are reserved.
		if spec.Name != switchPanel && spec.Name != switchWidget && !validName(spec.Name) {
			t.Errorf("%s is not a valid component name", spec.Name)
		}
		if n := utf8.RuneCountInString(spec.Description); n == 0 || n > maxDescription {
			t.Errorf("%s: description length %d", spec.Name, n)
		}
		if n := utf8.RuneCountInString(spec.PropsSummary); n == 0 || n > maxPropsSummary {
			t.Errorf("%s: props summary length %d", spec.Name, n)
		}
		if !strings.Contains(spec.Description, interactionMarker) {
			t.Errorf("%s: description lacks the interaction part", spec.Name)
		}
		if !strings.Contains(spec.Description, ". Use when: ") {
			t.Errorf("%s: description lacks the usage part", spec.Name)
		}
		if strings.Count(spec.Description, interactionMarker) != 1 || strings.Count(spec.PropsSummary, conventionMarker) > 1 {
			t.Errorf("%s: markers must appear once", spec.Name)
		}
	}
	for _, name := range []string{"card-grid", "image-gallery", "data-table", "quiz"} {
		for _, spec := range cat {
			if spec.Name == name && !imageRe.MatchString(spec.PropsSummary) {
				t.Errorf("%s: image convention missing", name)
			}
		}
	}
}

// Scenario 11.
func TestSeedCreatesBuiltinRows(t *testing.T) {
	env := newEnv(t)
	rows := builtinRows(t, env)
	cat := builtinCatalog()
	if len(rows) != len(cat) {
		t.Fatalf("rows %d, catalog %d", len(rows), len(cat))
	}
	for i, r := range rows {
		spec := cat[i]
		if r.Name != spec.Name || r.SortOrder != i+1 || r.Version != 1 || !r.Enabled || r.OwnerUserID != 0 ||
			r.RendererKind != RendererBuiltin || r.Description != spec.Description || r.PropsSummary != spec.PropsSummary {
			t.Errorf("row %d: %+v", i, r)
		}
	}
}

func TestSeedIsIdempotentAndPreservesAdminDecisions(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	chart := builtinID(t, env, "chart")
	quiz := builtinID(t, env, "quiz")

	// An admin disables a built-in and edits nothing else.
	testkit.Expect(t, env.Do("PATCH", idURL(adminBase, chart), env.Bearer(testkit.Admin(1)), map[string]any{"enabled": false}), 200, "")
	// Direct database tampering with seeded fields.
	env.DB.Model(&UIComponent{}).Where("id = ?", quiz).Updates(map[string]any{
		"description": "hacked", "props_summary": "hacked", "sort_order": 99, "version": 7, "renderer_kind": "sandbox",
	})
	// Platform and user rows must not be touched.
	plat := mk(t, env, adminBase, testkit.Admin(1), with(validBody("plat"), "enabled", true))
	mine := mk(t, env, mineURL, testkit.User(3), with(validBody("mine"), "enabled", true))
	var beforeIDs []uint
	env.DB.Model(&UIComponent{}).Order("id").Pluck("id", &beforeIDs)

	for i := 0; i < 2; i++ {
		if err := SyncBuiltins(ctx, env.DB); err != nil {
			t.Fatal(err)
		}
	}
	var afterIDs []uint
	env.DB.Model(&UIComponent{}).Order("id").Pluck("id", &afterIDs)
	if !sameUints(beforeIDs, afterIDs) {
		t.Fatalf("ids changed: %v -> %v", beforeIDs, afterIDs)
	}
	var c UIComponent
	env.DB.Take(&c, chart)
	if c.Enabled {
		t.Fatal("admin's disable was reverted")
	}
	var q UIComponent
	env.DB.Take(&q, quiz)
	spec := builtinCatalog()[10]
	if q.Description != spec.Description || q.PropsSummary != spec.PropsSummary || q.SortOrder != 11 || q.Version != 1 || q.RendererKind != RendererBuiltin {
		t.Fatalf("quiz not restored: %+v", q)
	}
	var p, m UIComponent
	env.DB.Take(&p, plat.ID)
	env.DB.Take(&m, mine.ID)
	if p.Name != "plat" || p.RendererKind != RendererSandbox || m.Name != "mine" || !p.Enabled || !m.Enabled {
		t.Fatalf("custom rows touched: %+v %+v", p, m)
	}
	if got := len(builtinRows(t, env)); got != 13 {
		t.Fatalf("builtin rows: %d", got)
	}
}

func TestSeedRemovesRetiredAndAddsNewBuiltins(t *testing.T) {
	env := newEnv(t)
	ctx := context.Background()
	// A row for a component the product no longer ships.
	env.DB.Create(&UIComponent{Scope: ScopeBuiltin, Name: "retired", Version: 1, Description: "x", PropsSummary: "x", RendererKind: RendererBuiltin, Enabled: true, SortOrder: 3})
	// A user row sharing the retired name stays.
	env.DB.Create(&UIComponent{Scope: ScopeUser, OwnerUserID: 4, Name: "retired", Version: 1, Description: "x", PropsSummary: "x", RendererKind: RendererSandbox, Enabled: true})
	// A missing built-in is re-created enabled.
	env.DB.Where("scope = ? AND name = ?", ScopeBuiltin, "diff").Delete(&UIComponent{})

	if err := SyncBuiltins(ctx, env.DB); err != nil {
		t.Fatal(err)
	}
	var n int64
	env.DB.Model(&UIComponent{}).Where("scope = ? AND name = ?", ScopeBuiltin, "retired").Count(&n)
	if n != 0 {
		t.Fatal("retired builtin must be removed")
	}
	env.DB.Model(&UIComponent{}).Where("scope = ? AND name = ?", ScopeUser, "retired").Count(&n)
	if n != 1 {
		t.Fatal("user row with the same name must survive")
	}
	var d UIComponent
	if err := env.DB.Where("scope = ? AND name = ?", ScopeBuiltin, "diff").Take(&d).Error; err != nil || !d.Enabled || d.SortOrder != 10 {
		t.Fatalf("diff: %+v %v", d, err)
	}
	// A shrunk catalog deletes the dropped names and keeps enabled flags.
	env.DB.Model(&UIComponent{}).Where("name = ? AND scope = ?", "chart", ScopeBuiltin).Update("enabled", false)
	small := builtinCatalog()[:3] // card-grid, chart, function-plot
	if err := syncCatalog(ctx, env.DB, small); err != nil {
		t.Fatal(err)
	}
	rows := builtinRows(t, env)
	if len(rows) != 3 || rows[1].Name != "chart" || rows[1].Enabled {
		t.Fatalf("after shrink: %+v", rows)
	}
}

func TestMigrationsRunTwiceOnAnExistingTable(t *testing.T) {
	env := newEnv(t)
	mk(t, env, mineURL, testkit.User(1), validBody("keep"))
	steps := Migrations()
	for i := 0; i < 2; i++ {
		for _, s := range steps {
			if err := s.Up(context.Background(), env.DB); err != nil {
				t.Fatalf("step %s: %v", s.Name, err)
			}
		}
	}
	if got := list(t, env, mineURL, testkit.User(1)); got.Total != 1 {
		t.Fatalf("existing row lost: %+v", got)
	}
}

func TestMigrationShape(t *testing.T) {
	steps := Migrations()
	if len(steps) == 0 {
		t.Fatal("no steps")
	}
	if err := features.Validate([]features.Feature{{Name: "uicomponent", Migrations: Migrations, Mount: Mount}}); err != nil {
		t.Fatal(err)
	}
	sets := features.MigrationSets([]features.Feature{{Name: "uicomponent", Migrations: Migrations, Mount: Mount}})
	if len(sets) != 1 {
		t.Fatal("sets")
	}
	seeds := 0
	for _, s := range sets[0].Steps {
		if s.Phase == migrate.PhaseSeed {
			seeds++
		}
	}
	if seeds != 1 {
		t.Fatalf("seed steps: %d", seeds)
	}
}

func TestSchema(t *testing.T) {
	env := newEnv(t)
	m := env.DB.Migrator()
	for _, col := range []string{"id", "created_at", "updated_at", "scope", "owner_user_id", "name", "version", "description",
		"props_summary", "props_schema", "renderer_kind", "renderer_source", "enabled", "sort_order", "created_by_user_id", "updated_by_user_id"} {
		if !m.HasColumn("ui_components", col) {
			t.Errorf("column %s missing", col)
		}
	}
	if !m.HasIndex(&UIComponent{}, "ux_ui_components_scope_owner_name") {
		t.Error("unique index missing")
	}
	// The unique constraint is enforced by the database itself.
	row := UIComponent{Scope: ScopeUser, OwnerUserID: 5, Name: "same", Version: 1, RendererKind: RendererSandbox}
	if err := env.DB.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	dup := UIComponent{Scope: ScopeUser, OwnerUserID: 5, Name: "same", Version: 1, RendererKind: RendererSandbox}
	if err := env.DB.Create(&dup).Error; err == nil {
		t.Fatal("duplicate (scope, owner, name) accepted")
	}
	ok := UIComponent{Scope: ScopeUser, OwnerUserID: 6, Name: "same", Version: 1, RendererKind: RendererSandbox}
	if err := env.DB.Create(&ok).Error; err != nil {
		t.Fatal(err)
	}
	// A disabled row is stored as disabled (no column default flips it).
	var back UIComponent
	env.DB.Take(&back, row.ID)
	if back.Enabled {
		t.Fatal("enabled default must be false for explicit false")
	}
}

func sameUints(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
