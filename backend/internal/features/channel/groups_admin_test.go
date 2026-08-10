package channel

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type planRefs map[uint]int64

func (p planRefs) PlansReferencingGroup(_ context.Context, id uint) (int64, error) { return p[id], nil }

type subUsers map[uint][]uint

func (s subUsers) SubscriptionGroupID(context.Context, uint, time.Time) (uint, bool, error) {
	return 0, false, nil
}
func (s subUsers) SubscriptionGroupUserIDs(_ context.Context, g uint, _ time.Time) ([]uint, error) {
	return s[g], nil
}

type groupDoc struct {
	ID                    uint   `json:"id"`
	Name                  string `json:"name"`
	IsDefault             bool   `json:"isDefault"`
	RateMultiplierPercent int    `json:"rateMultiplierPercent"`
	ModelCount            int64  `json:"modelCount"`
	ManualModelCount      int64  `json:"manualModelCount"`
	RuleModelCount        int64  `json:"ruleModelCount"`
	UserCount             int64  `json:"userCount"`
	ManualUserCount       int64  `json:"manualUserCount"`
	SubscriptionUserCount int64  `json:"subscriptionUserCount"`
}

func (s *suite) mkPermGroup(body map[string]any) groupDoc {
	s.t.Helper()
	data := s.ok("POST", "/api/v1/admin/permission-groups", body)
	return decodeInto[struct {
		Group groupDoc `json:"group"`
	}](s.t, data).Group
}

func (s *suite) users(ids ...uint) {
	s.t.Helper()
	if err := s.env.DB.Exec("CREATE TABLE IF NOT EXISTS identity_users (id integer primary key, deleted_at datetime)").Error; err != nil {
		s.t.Fatal(err)
	}
	for _, id := range ids {
		s.env.DB.Exec("INSERT OR IGNORE INTO identity_users (id) VALUES (?)", id)
	}
}

func TestAdminGroupCreateAndUpdate(t *testing.T) {
	s := newSuite(t)
	g := s.mkPermGroup(map[string]any{"name": "  Gold  ", "rateMultiplierPercent": 150, "description": " d "})
	if g.Name != "Gold" || g.RateMultiplierPercent != 150 || g.IsDefault || g.ModelCount != 0 || g.UserCount != 0 {
		t.Fatalf("created: %+v", g)
	}
	if again := s.mkPermGroup(map[string]any{"name": "Gold"}); again.RateMultiplierPercent != 100 || again.ID == g.ID {
		t.Fatalf("omitted rate is 100 and names need not be unique: %+v", again)
	}
	for _, rate := range []int{10001, -1} {
		s.expect(s.do("POST", "/api/v1/admin/permission-groups", map[string]any{"name": "x", "rateMultiplierPercent": rate}), 400, "request.invalid_body")
	}
	s.expect(s.do("POST", "/api/v1/admin/permission-groups", map[string]any{"name": "   "}), 400, "admin.invalid_permission_group_name")
	s.expect(s.do("POST", "/api/v1/admin/permission-groups", map[string]any{"name": strings.Repeat("n", 129)}), 400, "request.invalid_body")
	s.expect(s.do("POST", "/api/v1/admin/permission-groups", nil), 400, "request.invalid_body")

	// PATCH replaces all three fields; an omitted rate resets to 100.
	data := s.ok("PATCH", fmt.Sprintf("/api/v1/admin/permission-groups/%d", g.ID), map[string]any{"name": "Silver"})
	up := decodeInto[struct {
		Group groupDoc `json:"group"`
	}](t, data).Group
	if up.Name != "Silver" || up.RateMultiplierPercent != 100 {
		t.Fatalf("patched: %+v", up)
	}
	s.expect(s.do("PATCH", "/api/v1/admin/permission-groups/99999", map[string]any{"name": "x"}), 404, "admin.permission_group_not_found")
	s.expect(s.do("PATCH", "/api/v1/admin/permission-groups/abc", map[string]any{"name": "x"}), 400, "permission_group.invalid_id")
	s.expect(s.do("PATCH", "/api/v1/admin/permission-groups/0", map[string]any{"name": "x"}), 400, "permission_group.invalid_id")
	s.expect(s.do("PATCH", fmt.Sprintf("/api/v1/admin/permission-groups/%d", g.ID), map[string]any{"name": " "}), 400, "admin.invalid_permission_group_name")
}

func TestAdminGroupListOrderAndCounts(t *testing.T) {
	var subs subUsers
	s := newSuite(t, func(o *Options) {
		subs = subUsers{}
		o.Subscriptions = subs
		o.Users = userCount(7)
	})
	_, a := s.mkChat("lc1", "lc-a")
	_, b := s.mkChat("lc2", "lc-b")
	first := s.mkPermGroup(map[string]any{"name": "first"})
	def := s.mkPermGroup(map[string]any{"name": "def"})
	if err := s.env.DB.Model(&PermissionGroup{}).Where("id = ?", def.ID).Update("is_default", true).Error; err != nil {
		t.Fatal(err)
	}
	s.ok("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/models", first.ID), map[string]any{
		"modelIDs": []uint{a.PlatformModelID},
		"rules":    []map[string]any{{"type": "vendor", "value": "nobody"}, {"type": "protocol", "value": ProtoOpenAIChat}},
	})
	s.users(5, 6, 7)
	s.ok("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", first.ID), map[string]any{"userIDs": []uint{5, 6}})
	subs[first.ID] = []uint{6, 7, 7}
	// Memberships inserted for the default group by hand do not count.
	s.env.DB.Create(&PermissionGroupUserAccess{GroupID: def.ID, UserID: 5})

	data := s.ok("GET", "/api/v1/admin/permission-groups", nil)
	list := decodeInto[struct {
		Results []groupDoc `json:"results"`
	}](t, data).Results
	if len(list) != 2 || !list[0].IsDefault || list[1].ID != first.ID {
		t.Fatalf("default group first: %+v", list)
	}
	if list[0].UserCount != 7 || list[0].ManualUserCount != 0 || list[0].SubscriptionUserCount != 0 {
		t.Fatalf("default counts every account: %+v", list[0])
	}
	f := list[1]
	if f.ManualModelCount != 1 || f.RuleModelCount != 2 || f.ModelCount != 2 {
		t.Fatalf("model figures: %+v", f)
	}
	if f.ManualUserCount != 2 || f.SubscriptionUserCount != 2 || f.UserCount != 3 {
		t.Fatalf("user figures: %+v", f)
	}
	_ = b
}

func TestAdminGroupDelete(t *testing.T) {
	s := newSuite(t, func(o *Options) { o.Plans = planRefs{} })
	_, a := s.mkChat("gd", "gd-a")
	def := s.mkPermGroup(map[string]any{"name": "d"})
	s.env.DB.Model(&PermissionGroup{}).Where("id = ?", def.ID).Update("is_default", true)
	s.expect(s.do("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", def.ID), nil), 409, "admin.default_permission_group_delete_not_allowed")

	bound := s.mkPermGroup(map[string]any{"name": "bound"})
	s.svc.opts.Plans = planRefs{bound.ID: 1}
	s.expect(s.do("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", bound.ID), nil), 409, "admin.permission_group_referenced_by_plan")

	plain := s.mkPermGroup(map[string]any{"name": "plain"})
	s.users(3)
	s.ok("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/models", plain.ID), map[string]any{
		"modelIDs": []uint{a.PlatformModelID}, "rules": []map[string]any{{"type": "all"}, {"type": "vendor", "value": "x"}}})
	s.ok("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", plain.ID), map[string]any{"userIDs": []uint{3}})
	data := s.ok("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", plain.ID), nil)
	out := decodeInto[struct {
		Deleted bool `json:"deleted"`
		Summary struct {
			ManualModelCount, RuleCount, ManualUserCount, PlanCount int64
		} `json:"summary"`
	}](t, data)
	if !out.Deleted || out.Summary.ManualModelCount != 1 || out.Summary.RuleCount != 2 || out.Summary.ManualUserCount != 1 || out.Summary.PlanCount != 0 {
		t.Fatalf("summary: %+v", out)
	}
	for _, m := range []any{&PermissionGroupModelAccess{}, &PermissionGroupModelRule{}, &PermissionGroupUserAccess{}} {
		var n int64
		s.env.DB.Model(m).Where("group_id = ?", plain.ID).Count(&n)
		if n != 0 {
			t.Fatalf("%T rows remain", m)
		}
	}
	s.expect(s.do("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", plain.ID), nil), 404, "admin.permission_group_not_found")
}

func TestAdminGroupModelsReplacement(t *testing.T) {
	s := newSuite(t)
	_, a := s.mkChat("gm1", "gm-a")
	_, b := s.mkChat("gm2", "gm-b")
	g := s.mkPermGroup(map[string]any{"name": "g"})
	path := fmt.Sprintf("/api/v1/admin/permission-groups/%d/models", g.ID)
	for _, bad := range []map[string]any{
		{"modelIDs": []uint{99999}},
		{"modelIDs": []uint{0}},
		{"rules": []map[string]any{{"type": "vendor"}}},
		{"rules": []map[string]any{{"type": "color", "value": "x"}}},
		{"rules": []map[string]any{{"type": "protocol", "value": "  "}}},
	} {
		s.expect(s.do("PUT", path, bad), 400, "admin.invalid_permission_group_models")
	}
	s.expect(s.do("PUT", path, map[string]any{"rules": []map[string]any{{"value": "x"}}}), 400, "request.invalid_body")
	s.expect(s.do("PUT", "/api/v1/admin/permission-groups/99999/models", map[string]any{}), 404, "admin.permission_group_not_found")

	data := s.ok("PUT", path, map[string]any{
		"modelIDs": []uint{b.PlatformModelID, a.PlatformModelID, a.PlatformModelID},
		"rules": []map[string]any{{"type": "all", "value": "ignored"}, {"type": "all"}, {"type": " vendor ", "value": " openai "},
			{"type": "vendor", "value": "openai"}},
	})
	type set struct {
		ModelIDs []uint `json:"modelIDs"`
		Rules    []struct{ Type, Value string }
	}
	got := decodeInto[set](t, data)
	if fmt.Sprint(got.ModelIDs) != fmt.Sprint([]uint{a.PlatformModelID, b.PlatformModelID}) {
		t.Fatalf("ids ascending, deduplicated: %v", got.ModelIDs)
	}
	if len(got.Rules) != 2 || got.Rules[0].Type != "all" || got.Rules[0].Value != "" || got.Rules[1].Type != "vendor" || got.Rules[1].Value != "openai" {
		t.Fatalf("rules: %+v", got.Rules)
	}
	again := decodeInto[set](t, s.ok("GET", path, nil))
	if fmt.Sprint(again) != fmt.Sprint(got) {
		t.Fatalf("GET returns the stored state: %+v vs %+v", again, got)
	}
	empty := decodeInto[set](t, s.ok("PUT", path, map[string]any{}))
	if empty.ModelIDs == nil || len(empty.ModelIDs) != 0 || len(empty.Rules) != 0 {
		t.Fatalf("omitting both empties the group: %+v", empty)
	}
	if !strings.Contains(string(s.ok("GET", path, nil)), `"rules":[]`) {
		t.Fatal("arrays are never null")
	}
}

func TestAdminGroupMembers(t *testing.T) {
	s := newSuite(t)
	s.users(4, 9, 2)
	g := s.mkPermGroup(map[string]any{"name": "g"})
	def := s.mkPermGroup(map[string]any{"name": "d"})
	s.env.DB.Model(&PermissionGroup{}).Where("id = ?", def.ID).Update("is_default", true)
	dpath := fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", def.ID)
	s.expect(s.do("GET", dpath, nil), 409, "admin.default_permission_group_users_implicit")
	s.expect(s.do("PUT", dpath, map[string]any{}), 409, "admin.default_permission_group_users_implicit")

	path := fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", g.ID)
	s.expect(s.do("PUT", path, map[string]any{"userIDs": []uint{4, 12345}}), 400, "admin.invalid_permission_group_users")
	s.expect(s.do("PUT", path, map[string]any{"userIDs": []uint{0}}), 400, "admin.invalid_permission_group_users")
	s.env.DB.Exec("UPDATE identity_users SET deleted_at = ? WHERE id = 2", time.Now())
	s.expect(s.do("PUT", path, map[string]any{"userIDs": []uint{2}}), 400, "admin.invalid_permission_group_users")
	s.expect(s.do("GET", "/api/v1/admin/permission-groups/99999/users", nil), 404, "admin.permission_group_not_found")

	data := s.ok("PUT", path, map[string]any{"userIDs": []uint{9, 4, 9}})
	if got := decodeInto[struct{ UserIDs []uint }](t, data).UserIDs; fmt.Sprint(got) != "[4 9]" {
		t.Fatalf("ids: %v", got)
	}
	if got := decodeInto[struct{ UserIDs []uint }](t, s.ok("GET", path, nil)).UserIDs; fmt.Sprint(got) != "[4 9]" {
		t.Fatalf("stored: %v", got)
	}
	data = s.ok("PUT", path, map[string]any{})
	if !strings.Contains(string(data), `"userIDs":[]`) {
		t.Fatalf("omitted removes everybody: %s", data)
	}
}

func TestAdminModelGroups(t *testing.T) {
	s := newSuite(t)
	_, m := s.mkChat("mg", "mg-model")
	mid := m.PlatformModelID
	if err := s.env.DB.Model(&PlatformModel{}).Where("id = ?", mid).Update("vendor", "acme").Error; err != nil {
		t.Fatal(err)
	}
	g1 := s.mkPermGroup(map[string]any{"name": "g1"})
	g2 := s.mkPermGroup(map[string]any{"name": "g2"})
	path := fmt.Sprintf("/api/v1/admin/models/%d/permission-groups", mid)
	get := func() map[string]any {
		var out map[string]any
		testkit.Decode(t, s.do("GET", path, nil), &out)
		return out
	}
	if g := get(); g["unassigned"] != true || fmt.Sprint(g["effectiveGroupIDs"]) != "[]" {
		t.Fatalf("a model without groups is unassigned: %v", g)
	}
	s.ok("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/models", g2.ID), map[string]any{
		"rules": []map[string]any{{"type": "vendor", "value": "acme"}}})
	s.ok("PUT", path, map[string]any{"groupIDs": []uint{g1.ID, g1.ID}})
	g := get()
	if fmt.Sprint(g["manualGroupIDs"]) != fmt.Sprintf("[%d]", g1.ID) || fmt.Sprint(g["matchedGroupIDs"]) != fmt.Sprintf("[%d]", g2.ID) ||
		fmt.Sprint(g["effectiveGroupIDs"]) != fmt.Sprintf("[%d %d]", g1.ID, g2.ID) || g["unassigned"] != false {
		t.Fatalf("reach: %v", g)
	}
	// Clearing the manual grants leaves the rule-matched group alone.
	s.ok("PUT", path, map[string]any{"groupIDs": []uint{}})
	g = get()
	if fmt.Sprint(g["manualGroupIDs"]) != "[]" || fmt.Sprint(g["matchedGroupIDs"]) != fmt.Sprintf("[%d]", g2.ID) {
		t.Fatalf("cleared: %v", g)
	}
	rules, _ := s.svc.AdminGroupModels(context.Background(), g2.ID)
	if len(rules.Rules) != 1 {
		t.Fatalf("rules untouched: %+v", rules)
	}
	s.expect(s.do("PUT", path, map[string]any{"groupIDs": []uint{0}}), 400, "admin.invalid_permission_group_models")
	s.expect(s.do("PUT", path, map[string]any{"groupIDs": []uint{424242}}), 400, "admin.invalid_permission_group_models")
	s.expect(s.do("GET", "/api/v1/admin/models/424242/permission-groups", nil), 400, "admin.invalid_permission_group_models")
	s.expect(s.do("PUT", "/api/v1/admin/models/424242/permission-groups", map[string]any{}), 400, "admin.invalid_permission_group_models")
	s.expect(s.do("GET", "/api/v1/admin/models/0/permission-groups", nil), 400, "model.invalid_id")
	s.expect(s.do("GET", "/api/v1/admin/models/x/permission-groups", nil), 400, "model.invalid_id")
}

func TestAdminGroupRoutesNeedAdminAndMultiUser(t *testing.T) {
	s := newSuite(t)
	s.expect(s.env.Do("GET", "/api/v1/admin/permission-groups", s.usr(5), nil), 403, "auth.admin_required")

	local := testkit.New(t, []features.Feature{{Name: "channel", Migrations: Migrations, Mount: Mount}},
		testkit.WithConfig(func(c *config.Config) { c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"} }))
	adm := local.Bearer(testkit.Admin(1))
	for _, p := range []string{"/api/v1/admin/permission-groups", "/api/v1/admin/permission-groups/1/models", "/api/v1/admin/models/1/permission-groups"} {
		w := local.Do("GET", p, adm, nil)
		testkit.Expect(t, w, 404, "feature.disabled")
		if !strings.Contains(w.Body.String(), `"feature":"multiUser"`) {
			t.Fatalf("details: %s", w.Body.String())
		}
	}
	// The role check precedes the gate.
	testkit.Expect(t, local.Do("GET", "/api/v1/admin/permission-groups", local.Bearer(testkit.User(5)), nil), 403, "auth.admin_required")
}
