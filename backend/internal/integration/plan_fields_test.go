package integration_test

import (
	"fmt"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type planFields struct {
	SubscriptionTier     string  `json:"subscriptionTier"`
	SubscriptionPlanID   *uint   `json:"subscriptionPlanID"`
	SubscriptionPlanName string  `json:"subscriptionPlanName"`
	SubscriptionStatus   string  `json:"subscriptionStatus"`
	Username             string  `json:"username"`
	ID                   float64 `json:"id"`
}

func TestFreeTierUserHasNoPlanIDAndLowercaseName(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "plan-admin@example.com", "admin")
	alice := session(t, env, "plan-alice@example.com", "")
	aliceID := userID(t, env, "plan-alice@example.com")

	check := func(where string, f planFields) {
		t.Helper()
		if f.SubscriptionTier != "free" || f.SubscriptionStatus != "free" || f.SubscriptionPlanID != nil || f.SubscriptionPlanName != "free" {
			t.Fatalf("%s: %+v", where, f)
		}
	}
	w := env.Do("GET", "/api/v1/me", alice, nil)
	testkit.Expect(t, w, 200, "")
	var me struct{ User planFields }
	dataOf(t, w.Body.Bytes(), &me)
	check("/me", me.User)

	w = env.Do("GET", "/api/v1/admin/users?q=plan-alice", admin, nil)
	testkit.Expect(t, w, 200, "")
	var list struct{ Results []planFields }
	dataOf(t, w.Body.Bytes(), &list)
	if len(list.Results) != 1 {
		t.Fatalf("list: %s", w.Body.String())
	}
	check("admin list", list.Results[0])

	// A paid plan still reports its id and display name.
	testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": "period"}), 200, "")
	expiry := env.Clock.Now().Add(30 * 24 * time.Hour).UTC().Format(time.RFC3339)
	w = env.Do("PATCH", fmt.Sprintf("/api/v1/admin/users/%d", aliceID), admin, map[string]any{"subscriptionTier": "pro", "subscriptionExpiresAt": expiry})
	testkit.Expect(t, w, 200, "")
	w = env.Do("GET", "/api/v1/me", alice, nil)
	testkit.Expect(t, w, 200, "")
	dataOf(t, w.Body.Bytes(), &me)
	if me.User.SubscriptionTier != "pro" || me.User.SubscriptionPlanID == nil || me.User.SubscriptionPlanName != "Pro" {
		t.Fatalf("paid plan: %+v", me.User)
	}
}

func TestAdminListCarriesRealCredentialState(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "cred-admin@example.com", "admin")
	testkit.Expect(t, env.Do("POST", "/api/v1/admin/users", admin, map[string]any{"username": "credmade", "password": "abcdefgh"}), 200, "")
	w := env.Do("GET", "/api/v1/admin/users?q=credmade", admin, nil)
	testkit.Expect(t, w, 200, "")
	var list struct {
		Results []struct {
			PasswordEnabled         bool   `json:"passwordEnabled"`
			PasswordOrigin          string `json:"passwordOrigin"`
			MustResetPassword       bool   `json:"mustResetPassword"`
			InitialSecurityRequired bool   `json:"initialSecurityRequired"`
		}
	}
	dataOf(t, w.Body.Bytes(), &list)
	if len(list.Results) != 1 || !list.Results[0].PasswordEnabled || list.Results[0].PasswordOrigin != "admin_created" ||
		list.Results[0].MustResetPassword || !list.Results[0].InitialSecurityRequired {
		t.Fatalf("list row: %s", w.Body.String())
	}
}
