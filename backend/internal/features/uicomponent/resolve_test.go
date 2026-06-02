package uicomponent

import (
	"context"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

func newService(t *testing.T) (*Service, *toggle, *testkit.Env) {
	t.Helper()
	env, sw := newEnvWithSwitch(t)
	return NewService(env.DB, env.Clock, sw), sw, env
}

func idsOf(cs []UIComponent) []uint {
	out := make([]uint, len(cs))
	for i, c := range cs {
		out[i] = c.ID
	}
	return out
}

// Scenario 12.
func TestResolveTickedComponents(t *testing.T) {
	svc, sw, env := newService(t)
	ctx := context.Background()
	three := mk(t, env, mineURL, testkit.User(1), with(validBody("three"), "enabled", true))
	five := mk(t, env, mineURL, testkit.User(1), with(validBody("five"), "enabled", true))
	off := mk(t, env, mineURL, testkit.User(1), validBody("off"))
	foreign := mk(t, env, mineURL, testkit.User(2), with(validBody("foreign"), "enabled", true))
	plat := mk(t, env, adminBase, testkit.Admin(9), with(validBody("plat"), "enabled", true))
	chart := builtinID(t, env, "chart")

	got, err := svc.Resolve(ctx, 1, []uint{three.ID, three.ID, 0, 99999, five.ID})
	if err != nil {
		t.Fatal(err)
	}
	if !sameUints(idsOf(got), []uint{three.ID, five.ID}) {
		t.Fatalf("ids %v", idsOf(got))
	}
	// Order of first appearance, not id order or sort order.
	got, _ = svc.Resolve(ctx, 1, []uint{plat.ID, chart, five.ID, plat.ID, three.ID})
	if !sameUints(idsOf(got), []uint{plat.ID, chart, five.ID, three.ID}) {
		t.Fatalf("order %v", idsOf(got))
	}
	// Disabled, foreign and unknown ids are dropped silently.
	got, err = svc.Resolve(ctx, 1, []uint{off.ID, foreign.ID, 424242})
	if err != nil || len(got) != 0 {
		t.Fatalf("invisible: %v %v", got, err)
	}
	// Disabled built-ins ("switched off" rows) are dropped as well.
	env.DB.Model(&UIComponent{}).Where("id = ?", chart).Update("enabled", false)
	if got, _ = svc.Resolve(ctx, 1, []uint{chart, three.ID}); !sameUints(idsOf(got), []uint{three.ID}) {
		t.Fatalf("disabled builtin: %v", idsOf(got))
	}
	// The other user sees their own.
	if got, _ = svc.Resolve(ctx, 2, []uint{three.ID, foreign.ID}); !sameUints(idsOf(got), []uint{foreign.ID}) {
		t.Fatalf("user 2: %v", idsOf(got))
	}
	// Nothing for user 0, no ids, only zero ids.
	for _, c := range []struct {
		user uint
		ids  []uint
	}{{0, []uint{three.ID}}, {1, nil}, {1, []uint{}}, {1, []uint{0, 0}}} {
		if got, err := svc.Resolve(ctx, c.user, c.ids); err != nil || len(got) != 0 {
			t.Fatalf("%+v: %v %v", c, got, err)
		}
	}
	// Switch off: nothing, and Catalog discards anything handed to it.
	sw.on.Store(false)
	if got, _ := svc.Resolve(ctx, 1, []uint{three.ID}); len(got) != 0 {
		t.Fatalf("switch off resolve: %v", got)
	}
	sw.on.Store(true)
	comps, _ := svc.Resolve(ctx, 1, []uint{three.ID, five.ID})
	if cat := svc.Catalog(ctx, comps, 1); cat.Empty() || len(cat.Listed) != 2 {
		t.Fatalf("catalog: %+v", cat)
	}
	sw.on.Store(false)
	if cat := svc.Catalog(ctx, comps, 2); !cat.Empty() || cat.RoutingGuidance() != "" {
		t.Fatalf("catalog with switch off must be empty: %+v", cat)
	}
	if blocks := svc.States(ctx, []StateInput{{BlockID: "a", State: []byte(`{"x":1}`)}}); blocks != nil {
		t.Fatalf("states with switch off: %v", blocks)
	}
	sw.on.Store(true)
	if blocks := svc.States(ctx, []StateInput{{BlockID: "a", State: []byte(`{"x":1}`)}}); len(blocks) != 1 {
		t.Fatalf("states: %v", blocks)
	}
}

func TestResolveManyIDs(t *testing.T) {
	svc, _, _ := newService(t)
	ids := make([]uint, 0, 1000)
	for i := uint(1); i <= 1000; i++ {
		ids = append(ids, i)
	}
	got, err := svc.Resolve(context.Background(), 1, ids)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 || len(got) > 4*MaxTickedComponents {
		t.Fatalf("resolved %d", len(got))
	}
}

func TestDefaultSettingsKeepSwitchOn(t *testing.T) {
	if !AlwaysEnabled().UIComponentsEnabled(context.Background()) {
		t.Fatal("default must be enabled")
	}
	if !NewService(nil, nil, nil).Enabled(context.Background()) {
		t.Fatal("nil settings must mean enabled")
	}
	f := SettingsFunc(func(context.Context) bool { return false })
	if NewService(nil, nil, f).Enabled(context.Background()) {
		t.Fatal("SettingsFunc ignored")
	}
}

func TestMountDefaultWiringEnablesFeature(t *testing.T) {
	env := newAuditedEnv(t)
	if got := list(t, env, userBase, testkit.User(1)); got.Total != 13 {
		t.Fatalf("default mount catalog: %d", got.Total)
	}
}
