package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/integration"
	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func dataOf(t *testing.T, raw []byte, out any) {
	t.Helper()
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(env.Data, out); err != nil {
		t.Fatalf("%v: %s", err, env.Data)
	}
}

func userID(t *testing.T, env *testkit.Env, email string) uint {
	t.Helper()
	var id uint
	if err := env.DB.Table("identity_users").Where("email = ?", email).Select("id").Scan(&id).Error; err != nil || id == 0 {
		t.Fatalf("user %s: %d %v", email, id, err)
	}
	return id
}

type groupRow struct {
	ID                    uint   `json:"id"`
	Name                  string `json:"name"`
	IsDefault             bool   `json:"isDefault"`
	RateMultiplierPercent int    `json:"rateMultiplierPercent"`
	ModelCount            int64  `json:"modelCount"`
	RuleModelCount        int64  `json:"ruleModelCount"`
	UserCount             int64  `json:"userCount"`
	ManualUserCount       int64  `json:"manualUserCount"`
	SubscriptionUserCount int64  `json:"subscriptionUserCount"`
}

func listGroups(t *testing.T, env *testkit.Env, admin map[string]string) []groupRow {
	t.Helper()
	w := env.Do("GET", "/api/v1/admin/permission-groups", admin, nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Results []groupRow `json:"results"`
	}
	dataOf(t, w.Body.Bytes(), &out)
	return out.Results
}

func visibleModels(t *testing.T, env *testkit.Env, hdr map[string]string) string {
	t.Helper()
	w := env.Do("GET", "/api/v1/models", hdr, nil)
	testkit.Expect(t, w, 200, "")
	return w.Body.String()
}

func TestDefaultGroupSeedAndModelVisibility(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	alice := session(t, env, "alice@example.com", "")
	admin := session(t, env, "root2@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "gated-chat")

	groups := listGroups(t, env, admin)
	if len(groups) != 1 || !groups[0].IsDefault || groups[0].Name != "Default" || groups[0].RateMultiplierPercent != 100 ||
		groups[0].RuleModelCount != 1 || groups[0].UserCount < 2 {
		t.Fatalf("fresh install: %+v", groups)
	}
	def := groups[0]
	if !strings.Contains(visibleModels(t, env, alice), "gated-chat") {
		t.Fatal("the default group shows the model to everybody")
	}

	// Empty the default group's access: the model is hidden from everybody.
	testkit.Expect(t, env.Do("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/models", def.ID), admin, map[string]any{}), 200, "")
	if strings.Contains(visibleModels(t, env, alice), "gated-chat") || strings.Contains(visibleModels(t, env, admin), "gated-chat") {
		t.Fatal("a model without groups is invisible to every user, administrators included")
	}
	var modelID uint
	env.DB.Table("llm_platform_models").Where("name = ?", "gated-chat").Select("id").Scan(&modelID)
	reach := env.Do("GET", fmt.Sprintf("/api/v1/admin/models/%d/permission-groups", modelID), admin, nil)
	if !strings.Contains(reach.Body.String(), `"unassigned":true`) {
		t.Fatalf("reach: %s", reach.Body.String())
	}

	// A group granted manually; its member sees the model and may use it.
	w := env.Do("POST", "/api/v1/admin/permission-groups", admin, map[string]any{"name": "VIP", "rateMultiplierPercent": 150})
	testkit.Expect(t, w, 200, "")
	var created struct{ Group groupRow }
	dataOf(t, w.Body.Bytes(), &created)
	vip := created.Group
	testkit.Expect(t, env.Do("PUT", fmt.Sprintf("/api/v1/admin/models/%d/permission-groups", modelID), admin, map[string]any{"groupIDs": []uint{vip.ID}}), 200, "")
	if strings.Contains(visibleModels(t, env, alice), "gated-chat") {
		t.Fatal("not a member yet")
	}
	w = env.Do("POST", "/api/v1/conversations", alice, map[string]string{"model": "gated-chat"})
	var conv struct{ Data struct{ PublicID string } }
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	w = env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", alice, map[string]any{
		"content": "hello", "contentType": "text", "model": "gated-chat", "clientRunID": "gate-1",
	})
	if !strings.Contains(w.Body.String(), "llm.model_access_denied") {
		t.Fatalf("generation must be refused: %s", w.Body.String())
	}

	aliceID := userID(t, env, "alice@example.com")
	testkit.Expect(t, env.Do("PUT", fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", vip.ID), admin, map[string]any{"userIDs": []uint{aliceID}}), 200, "")
	if !strings.Contains(visibleModels(t, env, alice), "gated-chat") {
		t.Fatal("membership restores visibility")
	}
	w = env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", alice, map[string]any{
		"content": "hello", "contentType": "text", "model": "gated-chat", "clientRunID": "gate-2",
	})
	if !strings.Contains(w.Body.String(), `"content":"Hello from mock"`) {
		t.Fatalf("member may generate: %s", w.Body.String())
	}
	for _, g := range listGroups(t, env, admin) {
		if g.ID == vip.ID && (g.ManualUserCount != 1 || g.UserCount != 1 || g.ModelCount != 1) {
			t.Fatalf("vip figures: %+v", g)
		}
	}
}

func TestSubscriptionMembersAndPlanReferencedGroups(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	alice := session(t, env, "sub@example.com", "")
	admin := session(t, env, "root3@example.com", "admin")
	aliceID := userID(t, env, "sub@example.com")
	testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": "period"}), 200, "")

	w := env.Do("POST", "/api/v1/admin/permission-groups", admin, map[string]any{"name": "Pro members"})
	var created struct{ Group groupRow }
	dataOf(t, w.Body.Bytes(), &created)
	g := created.Group
	if err := env.DB.Exec("UPDATE billing_plans SET permission_group_id = ? WHERE code = 'pro'", g.ID).Error; err != nil {
		t.Fatal(err)
	}
	expiry := env.Clock.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	w = env.Do("PATCH", fmt.Sprintf("/api/v1/admin/users/%d", aliceID), admin, map[string]any{"subscriptionTier": "pro", "subscriptionExpiresAt": expiry})
	testkit.Expect(t, w, 200, "")
	var patched struct {
		User struct{ SubscriptionTier, SubscriptionStatus string }
	}
	dataOf(t, w.Body.Bytes(), &patched)
	if patched.User.SubscriptionTier != "pro" || patched.User.SubscriptionStatus != "active" {
		t.Fatalf("subscription not applied: %s", w.Body.String())
	}

	for _, row := range listGroups(t, env, admin) {
		if row.ID == g.ID && (row.SubscriptionUserCount != 1 || row.UserCount != 1 || row.ManualUserCount != 0) {
			t.Fatalf("subscription members: %+v", row)
		}
	}
	w = env.Do("GET", fmt.Sprintf("/api/v1/admin/permission-groups/%d/users", g.ID), admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"userIDs":[]`) {
		t.Fatalf("subscription-derived members are not listed: %s", w.Body.String())
	}
	testkit.Expect(t, env.Do("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", g.ID), admin, nil), 409, "admin.permission_group_referenced_by_plan")

	// The seeded plans are bound to the default group, which cannot be deleted.
	groups := listGroups(t, env, admin)
	testkit.Expect(t, env.Do("DELETE", fmt.Sprintf("/api/v1/admin/permission-groups/%d", groups[0].ID), admin, nil), 409, "admin.default_permission_group_delete_not_allowed")
	_ = alice
}

func TestDefaultGroupSeedIsIdempotent(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "root4@example.com", "admin")
	groups := listGroups(t, env, admin)
	def := groups[0]

	// A stray membership of the default group disappears and a second default
	// flag is demoted at the next start.
	env.DB.Exec("INSERT INTO permission_group_user_access (group_id, user_id) VALUES (?, 1)", def.ID)
	env.DB.Exec("INSERT INTO permission_groups (name, description, is_default, rate_multiplier_percent, created_at, updated_at) VALUES ('Other', '', 1, 100, ?, ?)", env.Clock.Now(), env.Clock.Now())
	seed := integration.PermissionMigrations()[0]
	for i := 0; i < 2; i++ {
		if err := seed.Up(context.Background(), env.DB); err != nil {
			t.Fatal(err)
		}
	}
	var defaults, memberships, rules int64
	env.DB.Table("permission_groups").Where("is_default = ?", true).Count(&defaults)
	env.DB.Table("permission_group_user_access").Where("group_id = ?", def.ID).Count(&memberships)
	env.DB.Table("permission_group_model_rules").Count(&rules)
	if defaults != 1 || memberships != 0 || rules != 1 {
		t.Fatalf("defaults=%d memberships=%d rules=%d", defaults, memberships, rules)
	}
	after := listGroups(t, env, admin)
	if len(after) != 2 || after[0].ID != def.ID || !after[0].IsDefault || after[1].IsDefault {
		t.Fatalf("the lowest id keeps the flag: %+v", after)
	}
}

func TestAdminUserLifecycleAgainstTheRealStack(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "ops@example.com", "admin")
	victim := session(t, env, "victim@example.com", "")
	victimID := userID(t, env, "victim@example.com")

	// The list is served and names the account.
	w := env.Do("GET", "/api/v1/admin/users?q=victim", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"total":1`) || !strings.Contains(w.Body.String(), `"twoFactorAvailable":true`) {
		t.Fatalf("list: %s", w.Body.String())
	}

	// Password reset: old password stops working, the new one works, sessions end.
	w = env.Do("POST", fmt.Sprintf("/api/v1/admin/users/%d/reset-password", victimID), admin, map[string]any{"newPassword": "NewPass123", "mustResetPassword": false})
	testkit.Expect(t, w, 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/me", victim, nil), 401, "")
	var name string
	env.DB.Table("identity_users").Where("id = ?", victimID).Select("username").Scan(&name)
	testkit.Expect(t, env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": name, "password": "Passw0rd!123"}), 401, "")
	w = env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": name, "password": "NewPass123"})
	testkit.Expect(t, w, 200, "")
	w = env.Do("GET", fmt.Sprintf("/api/v1/admin/user-auth-events?user_id=%d&event_type=password_reset", victimID), admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), "admin_reset_password") {
		t.Fatalf("auth events: %s", w.Body.String())
	}
	if n := countAudit(env, "admin_reset_user_password"); n != 1 {
		t.Fatalf("audit entries: %d", n)
	}

	// Suspending ends the sessions and blocks login; activating restores it.
	var refreshed struct{ Data struct{ AccessToken string } }
	_ = json.Unmarshal(env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": name, "password": "NewPass123"}).Body.Bytes(), &refreshed)
	live := map[string]string{"Authorization": "Bearer " + refreshed.Data.AccessToken}
	testkit.Expect(t, env.Do("GET", "/api/v1/me", live, nil), 200, "")
	testkit.Expect(t, env.Do("PATCH", fmt.Sprintf("/api/v1/admin/users/%d/status", victimID), admin, map[string]any{"status": "suspended"}), 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/me", live, nil), 401, "")
	testkit.Expect(t, env.Do("PATCH", fmt.Sprintf("/api/v1/admin/users/%d/status", victimID), admin, map[string]any{"status": "active"}), 200, "")
	testkit.Expect(t, env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": name, "password": "NewPass123"}), 200, "")

	// Creating a user, then removing them for good.
	w = env.Do("POST", "/api/v1/admin/users", admin, map[string]any{"username": "created1", "password": "abcdefgh"})
	testkit.Expect(t, w, 200, "")
	var made struct{ User struct{ ID uint } }
	dataOf(t, w.Body.Bytes(), &made)
	testkit.Expect(t, env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": "created1", "password": "abcdefgh"}), 200, "")
	testkit.Expect(t, env.Do("DELETE", fmt.Sprintf("/api/v1/admin/users/%d", made.User.ID), admin, nil), 200, "")
	testkit.Expect(t, env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": "created1", "password": "abcdefgh"}), 401, "")
}

func countAudit(env *testkit.Env, action string) int64 {
	var n int64
	env.DB.Table("audit_logs").Where("action = ?", action).Count(&n)
	return n
}

func TestConversationEventsAndExportServedInTheFullStack(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "talker@example.com", "")
	admin := session(t, env, "ops2@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "export-chat")
	w := env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "export-chat"})
	var conv struct{ Data struct{ PublicID string } }
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", user, map[string]any{
		"content": "hello", "contentType": "text", "model": "export-chat", "clientRunID": "exp-1",
	})

	w = env.Do("GET", "/api/v1/admin/conversations/export", admin, nil)
	testkit.Expect(t, w, 200, "")
	lines := strings.Split(strings.TrimSpace(w.Body.String()), "\n")
	if len(lines) < 2 || !strings.Contains(lines[len(lines)-1], `"_type":"export_manifest"`) || !strings.Contains(w.Body.String(), "Hello from mock") {
		t.Fatalf("export: %s", w.Body.String())
	}
	if countAudit(env, "admin_export_conversations") != 1 {
		t.Fatal("export is audited")
	}
	w = env.Do("GET", "/api/v1/admin/conversation-events?query=exp-1", admin, nil)
	testkit.Expect(t, w, 200, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/conversation-events", user, nil), 403, "auth.admin_required")
}
