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
