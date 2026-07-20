package skill_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/features/skill"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

func svc(s *suite) *skill.Service { return skill.NewService(s.env.DB, s.env.Clock) }

func TestResolveSelection(t *testing.T) {
	s := newSuite(t)
	a := s.mine(1, "a", "a")
	b := s.builtin("b", "b")
	c := s.mine(1, "c", "c")
	other := s.mine(2, "o", "o")
	off := s.mine(1, "off", "off")
	s.setEnabled(1, off.ID, false)
	ctx := context.Background()
	sv := svc(s)

	// Zero ids and duplicates are dropped, first-seen order is kept.
	got, err := sv.Resolve(ctx, 1, []uint{b.ID, b.ID, 0, a.ID}, 32)
	if err != nil || len(got) != 2 || got[0].ID != b.ID || got[1].ID != a.ID {
		t.Fatalf("resolve: %v %+v", err, got)
	}
	if got[0].Markdown == "" {
		t.Fatalf("resolved skills carry markdown")
	}
	// Empty after normalization: no context.
	if got, err = sv.Resolve(ctx, 1, []uint{0, 0}, 32); err != nil || got != nil {
		t.Fatalf("empty: %v %v", got, err)
	}
	if got, err = sv.Resolve(ctx, 1, nil, 32); err != nil || got != nil {
		t.Fatalf("nil: %v %v", got, err)
	}
	// Unusable ids are not found: other user's, disabled, missing.
	for name, id := range map[string]uint{"other": other.ID, "disabled": off.ID, "missing": 4242} {
		if _, err = sv.Resolve(ctx, 1, []uint{a.ID, id}, 32); !errors.Is(err, skill.ErrNotFound) {
			t.Errorf("%s: %v", name, err)
		}
	}
	// Too many against the (clamped) limit.
	if _, err = sv.Resolve(ctx, 1, []uint{a.ID, b.ID, c.ID}, 2); !errors.Is(err, skill.ErrTooManySelected) {
		t.Fatalf("limit 2: %v", err)
	}
	if got, err = sv.Resolve(ctx, 1, []uint{a.ID, b.ID, c.ID}, 3); err != nil || len(got) != 3 {
		t.Fatalf("limit 3: %v", err)
	}
	// Non-positive configured limits fall back to 32.
	many := make([]uint, 33)
	for i := range many {
		many[i] = uint(i + 1)
	}
	if _, err = sv.Resolve(ctx, 1, many, 0); !errors.Is(err, skill.ErrTooManySelected) {
		t.Fatalf("limit 0 -> 32: %v", err)
	}
	if _, err = sv.Resolve(ctx, 0, []uint{a.ID}, 32); !errors.Is(err, skill.ErrInvalidSkillUse) {
		t.Fatalf("zero user: %v", err)
	}
	// Disabled between selection and send: rejected at send time.
	s.setEnabled(1, a.ID, false)
	if _, err = sv.Resolve(ctx, 1, []uint{a.ID}, 32); !errors.Is(err, skill.ErrNotFound) {
		t.Fatalf("disabled after selection: %v", err)
	}
	// Error contract.
	if skill.ErrTooManySelected.Code != "message.too_many_selected_skills" || skill.ErrTooManySelected.Message != "too many selected skills" {
		t.Fatalf("too many selected: %+v", skill.ErrTooManySelected)
	}
	if skill.ErrInvalidSkillUse.Code != "request.invalid_skill_use" || skill.ErrInvalidSkillUse.Message != "invalid skill use" {
		t.Fatalf("invalid use: %+v", skill.ErrInvalidSkillUse)
	}
}

func TestValidateUsable(t *testing.T) {
	s := newSuite(t)
	a := s.mine(1, "a", "a")
	b := s.builtin("b", "b")
	o := s.mine(2, "o", "o")
	sv := svc(s)
	ctx := context.Background()
	if err := sv.ValidateUsable(ctx, 1, []uint{a.ID, b.ID, a.ID, 0}); err != nil {
		t.Fatal(err)
	}
	if err := sv.ValidateUsable(ctx, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := sv.ValidateUsable(ctx, 1, []uint{a.ID, o.ID}); !errors.Is(err, skill.ErrNotFound) {
		t.Fatalf("foreign: %v", err)
	}
}

func TestServiceGuards(t *testing.T) {
	s := newSuite(t)
	sv := svc(s)
	ctx := context.Background()
	if _, err := sv.Get(ctx, 0, 1); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("zero caller: %v", err)
	}
	if _, err := sv.Get(ctx, 1, 0); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("zero skill: %v", err)
	}
	if _, _, err := sv.Discover(ctx, 0, nil, "", pagination.Parse("", "")); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("discover zero caller: %v", err)
	}
	in := skill.Input{Title: "t", Trigger: "t", Markdown: "m"}
	if _, err := sv.Create(ctx, skill.ScopeUser, 1, 0, in); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("zero actor: %v", err)
	}
	if _, err := sv.Create(ctx, skill.ScopeUser, 0, 1, in); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("user scope without owner: %v", err)
	}
	if _, err := sv.Create(ctx, "weird", 1, 1, in); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("bad scope: %v", err)
	}
	if err := sv.Delete(ctx, skill.ScopeUser, 1, 0); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("delete zero id: %v", err)
	}
	if _, err := sv.Update(ctx, skill.ScopeUser, 1, 1, 0, skill.Patch{}); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("update zero id: %v", err)
	}
}

func TestProjectDefaultsLifecycle(t *testing.T) {
	s := newSuite(t)
	own := s.mine(1, "own", "own")
	bi := s.builtin("bi", "bi")
	theirs := s.mine(2, "theirs", "theirs")
	off := s.mine(1, "off", "off")
	sv := svc(s)
	ctx := context.Background()

	// Foreign skills are rejected, own and built-in accepted.
	if err := sv.ValidateProjectDefaults(ctx, 1, nil, []uint{own.ID, theirs.ID}, 32); !errors.Is(err, skill.ErrInvalidProjectDefaults) {
		t.Fatalf("foreign: %v", err)
	}
	if err := sv.ValidateProjectDefaults(ctx, 1, nil, []uint{bi.ID, own.ID}, 32); err != nil {
		t.Fatalf("valid: %v", err)
	}
	if skill.ErrInvalidProjectDefaults.Code != "request.invalid_conversation_project" || skill.ErrInvalidProjectDefaults.Message != "invalid conversation project" {
		t.Fatalf("project error contract: %+v", skill.ErrInvalidProjectDefaults)
	}
	// Size limit only applies when the set changed.
	if err := sv.ValidateProjectDefaults(ctx, 1, nil, []uint{bi.ID, own.ID}, 1); !errors.Is(err, skill.ErrInvalidProjectDefaults) {
		t.Fatalf("over limit: %v", err)
	}
	if err := sv.ValidateProjectDefaults(ctx, 1, []uint{bi.ID, own.ID}, []uint{bi.ID, own.ID}, 1); err != nil {
		t.Fatalf("unchanged set is not re-validated: %v", err)
	}

	// Write, read in bulk, replace, clear.
	db := s.env.DB
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.ReplaceProjectDefaults(tx, 10, []uint{bi.ID, 0, own.ID, bi.ID}) }); err != nil {
		t.Fatal(err)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.ReplaceProjectDefaults(tx, 11, []uint{own.ID}) }); err != nil {
		t.Fatal(err)
	}
	got, err := sv.ProjectDefaults(ctx, []uint{10, 11, 12})
	if err != nil {
		t.Fatal(err)
	}
	if !eqIDs(got[10], []uint{bi.ID, own.ID}) || !eqIDs(got[11], []uint{own.ID}) || got[12] == nil || len(got[12]) != 0 {
		t.Fatalf("defaults: %v", got)
	}
	var pos []int
	db.Table("chat_conversation_project_skills").Where("project_id = 10").Order("sort_order").Pluck("sort_order", &pos)
	if len(pos) != 2 || pos[0] != 1 || pos[1] != 2 {
		t.Fatalf("positions: %v", pos)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.ReplaceProjectDefaults(tx, 10, []uint{own.ID}) }); err != nil {
		t.Fatal(err)
	}
	got, _ = sv.ProjectDefaults(ctx, []uint{10})
	if !eqIDs(got[10], []uint{own.ID}) {
		t.Fatalf("replace: %v", got)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.ReplaceProjectDefaults(tx, 10, []uint{}) }); err != nil {
		t.Fatal(err)
	}
	got, _ = sv.ProjectDefaults(ctx, []uint{10})
	if len(got[10]) != 0 {
		t.Fatalf("clear: %v", got)
	}

	// An id added earlier that is disabled later does not block unrelated edits.
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.ReplaceProjectDefaults(tx, 13, []uint{off.ID}) }); err != nil {
		t.Fatal(err)
	}
	s.setEnabled(1, off.ID, false)
	if err := sv.ValidateProjectDefaults(ctx, 1, []uint{off.ID}, []uint{off.ID, bi.ID}, 32); err != nil {
		t.Fatalf("previously saved disabled default: %v", err)
	}
	// ...but adding it again as new is rejected.
	if err := sv.ValidateProjectDefaults(ctx, 1, nil, []uint{off.ID}, 32); !errors.Is(err, skill.ErrInvalidProjectDefaults) {
		t.Fatalf("new disabled default: %v", err)
	}
	// The project keeps reporting it; deleting the skill drops the link only.
	got, _ = sv.ProjectDefaults(ctx, []uint{13})
	if !eqIDs(got[13], []uint{off.ID}) {
		t.Fatalf("project not auto-modified: %v", got)
	}
	testkitDelete(t, s, 1, off.ID)
	got, _ = sv.ProjectDefaults(ctx, []uint{13})
	if len(got[13]) != 0 {
		t.Fatalf("link survived skill deletion: %v", got)
	}

	if err := db.Transaction(func(tx *gorm.DB) error { return skill.DeleteProjectDefaults(tx, 11) }); err != nil {
		t.Fatal(err)
	}
	got, _ = sv.ProjectDefaults(ctx, []uint{11})
	if len(got[11]) != 0 {
		t.Fatalf("project delete: %v", got)
	}
	if m, err := sv.ProjectDefaults(ctx, nil); err != nil || len(m) != 0 {
		t.Fatalf("no projects: %v %v", m, err)
	}
}

func testkitDelete(t *testing.T, s *suite, user, id uint) {
	t.Helper()
	if err := svc(s).Delete(context.Background(), skill.ScopeUser, user, id); err != nil {
		t.Fatal(err)
	}
}

func TestPurgeUserKeepsBuiltins(t *testing.T) {
	s := newSuite(t)
	mine := s.mine(1, "mine", "mine")
	mine2 := s.mine(1, "mine2", "mine2")
	bi := s.builtin("bi", "bi")
	theirs := s.mine(2, "theirs", "theirs")
	db := s.env.DB
	for _, l := range [][3]uint{{1, mine.ID, 1}, {1, bi.ID, 2}, {2, theirs.ID, 1}, {3, mine2.ID, 1}, {4, bi.ID, 1}, {4, theirs.ID, 2}} {
		db.Exec("INSERT INTO chat_conversation_project_skills (project_id, skill_id, sort_order) VALUES (?, ?, ?)", l[0], l[1], l[2])
	}
	// User 1 owned projects 1 and 4.
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.PurgeUser(tx, 1, []uint{1, 4}) }); err != nil {
		t.Fatal(err)
	}
	var skillIDs []uint
	db.Table("skills").Order("id").Pluck("id", &skillIDs)
	if !eqIDs(skillIDs, []uint{bi.ID, theirs.ID}) {
		t.Fatalf("remaining skills: %v", skillIDs)
	}
	var links [][2]uint
	rows, _ := db.Table("chat_conversation_project_skills").Select("project_id, skill_id").Order("project_id, skill_id").Rows()
	defer rows.Close()
	for rows.Next() {
		var p, k uint
		_ = rows.Scan(&p, &k)
		links = append(links, [2]uint{p, k})
	}
	if len(links) != 1 || links[0] != [2]uint{2, theirs.ID} {
		t.Fatalf("remaining links: %v", links)
	}
	if err := db.Transaction(func(tx *gorm.DB) error { return skill.PurgeUser(tx, 0, nil) }); !errors.Is(err, skill.ErrInvalidSkill) {
		t.Fatalf("zero user: %v", err)
	}
}

func TestMigrationsDropLegacyColumnsAndAreIdempotent(t *testing.T) {
	s := newSuite(t)
	db := s.env.DB
	if err := db.Exec("ALTER TABLE skills ADD COLUMN content text").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("ALTER TABLE skills ADD COLUMN sections_json text").Error; err != nil {
		t.Fatal(err)
	}
	if !db.Migrator().HasColumn(&skill.Skill{}, "content") {
		t.Fatal("test setup: legacy column missing")
	}
	sk := s.mine(1, "keep", "keep")
	for round := 0; round < 2; round++ {
		for _, st := range skill.Migrations() {
			if st.Up == nil {
				continue
			}
			if err := st.Up(context.Background(), db); err != nil {
				t.Fatalf("step %s: %v", st.Name, err)
			}
		}
	}
	m := db.Migrator()
	if m.HasColumn(&skill.Skill{}, "content") || m.HasColumn(&skill.Skill{}, "sections_json") {
		t.Fatal("legacy columns still present")
	}
	got := s.one(s.do("GET", "/skills/"+itoa(sk.ID), s.as(1), nil))
	if got.Title != "keep" {
		t.Fatalf("data lost: %+v", got)
	}
}

func TestSchemaHasExpectedIndexes(t *testing.T) {
	s := newSuite(t)
	var names []string
	s.env.DB.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'skills'").Scan(&names)
	joined := strings.Join(names, ",")
	for _, want := range []string{"uq_skills_scope_owner_trigger", "idx_skills_scope", "idx_skills_owner_user_id", "idx_skills_trigger",
		"idx_skills_enabled", "idx_skills_sort_order", "idx_skills_created_by_user_id", "idx_skills_updated_by_user_id"} {
		if !strings.Contains(joined, want) {
			t.Errorf("missing index %s in %s", want, joined)
		}
	}
	names = nil
	s.env.DB.Raw("SELECT name FROM sqlite_master WHERE type = 'index' AND tbl_name = 'chat_conversation_project_skills'").Scan(&names)
	if !strings.Contains(strings.Join(names, ","), "idx_chat_conversation_project_skills_skill_id") {
		t.Errorf("join table skill_id index missing: %v", names)
	}
}

func TestMigrationsAreRunnable(t *testing.T) {
	// The step list must be accepted by the runner's naming checks.
	seen := map[string]bool{}
	for _, st := range skill.Migrations() {
		if st.Name == "" || seen[st.Name] {
			t.Fatalf("bad step name %q", st.Name)
		}
		seen[st.Name] = true
	}
	var _ migrate.Step
}

func itoa(n uint) string {
	const digits = "0123456789"
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{digits[n%10]}, b...)
		n /= 10
	}
	return string(b)
}
