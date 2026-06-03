package integration_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// session registers a user, optionally gives it a role, and returns its bearer
// header from a fresh login.
func session(t *testing.T, env *testkit.Env, email string, role string) map[string]string {
	t.Helper()
	w := env.Do("POST", "/api/v1/auth/register/email/complete", nil, map[string]string{"email": email, "password": "Passw0rd!123"})
	testkit.Expect(t, w, 200, "")
	var reg struct {
		Data struct {
			User struct{ Username string } `json:"user"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &reg); err != nil {
		t.Fatal(err)
	}
	if role != "" {
		if err := env.DB.Exec("UPDATE identity_users SET role = ? WHERE username = ?", role, reg.Data.User.Username).Error; err != nil {
			t.Fatal(err)
		}
	}
	w = env.Do("POST", "/api/v1/auth/login", nil, map[string]string{"username": reg.Data.User.Username, "password": "Passw0rd!123"})
	testkit.Expect(t, w, 200, "")
	var login struct {
		Data struct{ AccessToken string } `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &login); err != nil {
		t.Fatal(err)
	}
	return map[string]string{"Authorization": "Bearer " + login.Data.AccessToken}
}

// mockUpstream answers OpenAI chat completions, streaming or buffered. status
// lets a test make it fail.
func mockUpstream(t *testing.T, status int, calls *atomic.Int32) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls != nil {
			calls.Add(1)
		}
		var req map[string]any
		_ = json.NewDecoder(r.Body).Decode(&req)
		if status != 200 {
			http.Error(w, `{"error":{"message":"boom"}}`, status)
			return
		}
		if req["stream"] == true {
			w.Header().Set("Content-Type", "text/event-stream")
			for _, part := range []string{"Hello ", "from ", "mock"} {
				fmt.Fprintf(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":%q}}]}\n\n", part)
			}
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{},\"finish_reason\":\"stop\"}]}\n\n")
			fmt.Fprint(w, "data: {\"id\":\"c1\",\"object\":\"chat.completion.chunk\",\"choices\":[],\"usage\":{\"prompt_tokens\":11,\"completion_tokens\":3,\"total_tokens\":14}}\n\n")
			fmt.Fprint(w, "data: [DONE]\n\n")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"id":"c1","object":"chat.completion","choices":[{"index":0,"message":{"role":"assistant","content":"Short Title"},"finish_reason":"stop"}],"usage":{"prompt_tokens":5,"completion_tokens":2,"total_tokens":7}}`)
	}))
	t.Cleanup(srv.Close)
	return srv
}

// addModel creates an upstream and a bound chat model through the admin API.
func addModel(t *testing.T, env *testkit.Env, admin map[string]string, baseURL, model string) {
	t.Helper()
	w := env.Do("POST", "/api/v1/admin/llm/upstreams", admin, map[string]any{
		"name": "up-" + model, "baseURL": baseURL, "status": "active",
		"apiKeys": `{"strategy":"failover","keys":[{"key":"sk-abcdefghijk"}]}`,
	})
	testkit.Expect(t, w, 200, "")
	var up struct {
		Data struct {
			Upstream struct{ ID uint } `json:"upstream"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &up); err != nil {
		t.Fatal(err)
	}
	w = env.Do("POST", fmt.Sprintf("/api/v1/admin/llm/upstreams/%d/models", up.Data.Upstream.ID), admin, map[string]any{
		"platformModelName": model, "upstreamModelName": "mock-model",
		"protocols": []string{"openai_chat_completions"}, "kindsJSON": `["chat"]`,
	})
	testkit.Expect(t, w, 200, "")
}

func TestEveryAreaServesARoute(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "ann@example.com", "")
	admin := session(t, env, "root@example.com", "admin")
	super := session(t, env, "sup@example.com", "superadmin")

	cases := []struct {
		area, method, path string
		hdr                map[string]string
	}{
		{"auth", "GET", "/api/v1/me", user},
		{"user", "GET", "/api/v1/user/settings", user},
		{"settings", "GET", "/api/v1/admin/settings", admin},
		{"audit", "GET", "/api/v1/admin/audit-logs", admin},
		{"errorlog", "GET", "/api/v1/admin/error-logs", admin},
		{"announcement", "GET", "/api/v1/announcements", user},
		{"channel", "GET", "/api/v1/models", user},
		{"billing", "GET", "/api/v1/billing/config", user},
		{"personalprovider", "GET", "/api/v1/admin/model-providers", admin},
		{"knowledgebase", "GET", "/api/v1/knowledge-bases/mine", user},
		{"memory", "GET", "/api/v1/memories/profile", user},
		{"mcp", "GET", "/api/v1/settings/mcp-policy", user},
		{"skill", "GET", "/api/v1/skills", user},
		{"uicomponent", "GET", "/api/v1/ui-components", user},
		{"contentmoderation", "GET", "/api/v1/admin/content-moderation/config", super},
		{"promptreset", "GET", "/api/v1/prompt-presets", user},
		{"conversation", "GET", "/api/v1/conversations", user},
	}
	for _, c := range cases {
		w := env.Do(c.method, c.path, c.hdr, nil)
		if w.Code != 200 {
			t.Errorf("%s: %s %s answered %d: %s", c.area, c.method, c.path, w.Code, w.Body.String())
		}
	}
}

func TestChatRunsThroughChannelBillingAndAudit(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "bea@example.com", "")
	admin := session(t, env, "boss@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "mock-chat")

	// The default permission group lets everyone see the model.
	w := env.Do("GET", "/api/v1/models", user, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"platformModelName":"mock-chat"`) {
		t.Fatalf("model not listed: %s", w.Body.String())
	}

	w = env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "mock-chat"})
	testkit.Expect(t, w, 200, "")
	var conv struct {
		Data struct{ PublicID string } `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}

	w = env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", user, map[string]any{
		"content": "hi there", "contentType": "text", "model": "mock-chat", "clientRunID": "run-1",
	})
	body := w.Body.String()
	for _, want := range []string{`"delta":"Hello "`, `"delta":"mock"`, `"content":"Hello from mock"`} {
		if !strings.Contains(body, want) {
			t.Fatalf("stream lacks %s:\n%s", want, body)
		}
	}

	var rows int64
	if err := env.DB.Table("billing_usage_ledgers").Where("platform_model_name = ? AND input_tokens = 11 AND output_tokens = 3", "mock-chat").Count(&rows).Error; err != nil || rows != 1 {
		t.Fatalf("ledger rows for the run: %d (%v)", rows, err)
	}
	var convID int64
	if err := env.DB.Table("billing_usage_ledgers").Where("platform_model_name = ?", "mock-chat").Select("conversation_id").Scan(&convID).Error; err != nil || convID == 0 {
		t.Fatalf("ledger row carries conversation id %d (%v)", convID, err)
	}
}
