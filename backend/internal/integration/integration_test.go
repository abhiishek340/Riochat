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

func TestUsageBillingChargesAndReleases(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "cy@example.com", "")
	admin := session(t, env, "chief@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "paid-chat")

	testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": "usage"}), 200, "")
	testkit.Expect(t, env.Do("PUT", "/api/v1/admin/billing/model-prices", admin, map[string]any{
		"platformModelName": "paid-chat", "pricingMode": "token", "isFree": false,
		"inputUSDPerMTokens": 1, "cacheReadUSDPerMTokens": 0, "cacheWriteUSDPerMTokens": 0,
		"outputUSDPerMTokens": 2, "callUSDPerCall": 0, "durationUSDPerSecond": 0,
	}), 200, "")

	var uid uint
	if err := env.DB.Table("identity_users").Where("email = ?", "cy@example.com").Select("id").Scan(&uid).Error; err != nil {
		t.Fatal(err)
	}
	testkit.Expect(t, env.Do("PATCH", fmt.Sprintf("/api/v1/admin/billing/accounts/%d/balance", uid), admin,
		map[string]any{"balanceUSD": 5, "description": "test"}), 200, "")

	w := env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "paid-chat"})
	var conv struct {
		Data struct{ PublicID string } `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	w = env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", user, map[string]any{
		"content": "hello", "contentType": "text", "model": "paid-chat", "clientRunID": "run-2",
	})
	if !strings.Contains(w.Body.String(), `"content":"Hello from mock"`) {
		t.Fatalf("run failed: %s", w.Body.String())
	}

	var balance int64
	if err := env.DB.Table("billing_accounts").Where("user_id = ?", uid).Select("balance_nanousd").Scan(&balance).Error; err != nil {
		t.Fatal(err)
	}
	// 11 input tokens at 1 USD and 3 output tokens at 2 USD per million tokens,
	// plus nothing for the helper calls that report no priced usage.
	if want := int64(5_000_000_000 - 17_000); balance > want || balance < want-1_000_000 {
		t.Fatalf("balance after the run = %d, want about %d", balance, want)
	}
	var open int64
	env.DB.Table("billing_usage_reservations").Where("status = ?", "active").Count(&open)
	if open != 0 {
		t.Fatalf("%d reservations are still active", open)
	}
}

func TestUpstreamFailureIsReportedAsUpstreamError(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "di@example.com", "")
	admin := session(t, env, "head@example.com", "admin")
	var calls atomic.Int32
	srv := mockUpstream(t, 500, &calls)
	addModel(t, env, admin, srv.URL+"/v1", "broken-chat")

	w := env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "broken-chat"})
	var conv struct {
		Data struct{ PublicID string } `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &conv)
	w = env.Do("POST", "/api/v1/conversations/"+conv.Data.PublicID+"/messages/stream", user, map[string]any{
		"content": "hello", "contentType": "text", "model": "broken-chat", "clientRunID": "run-3",
	})
	if !strings.Contains(w.Body.String(), "upstream.unavailable") {
		t.Fatalf("expected upstream.unavailable, got: %s", w.Body.String())
	}
	if calls.Load() == 0 {
		t.Fatal("the upstream was never called")
	}
}

func TestAuditListNamesTheActor(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "lead@example.com", "admin")
	testkit.Expect(t, env.Do("POST", "/api/v1/admin/announcements", admin,
		map[string]any{"title": "Hi", "contentMarkdown": "hello", "type": "info", "status": "active"}), 200, "")
	w := env.Do("GET", "/api/v1/admin/audit-logs", admin, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"actorUsername":"lead`) {
		t.Fatalf("audit list does not name the actor: %s", w.Body.String())
	}
}

func TestChatRecordsProcessTraceAndAdminEvents(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "cal@example.com", "")
	admin := session(t, env, "chief@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "mock-chat")

	w := env.Do("POST", "/api/v1/conversations", user, map[string]string{"model": "mock-chat"})
	testkit.Expect(t, w, 200, "")
	var conv struct {
		Data struct{ PublicID string } `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &conv); err != nil {
		t.Fatal(err)
	}
	base := "/api/v1/conversations/" + conv.Data.PublicID
	w = env.Do("POST", base+"/messages/stream", user, map[string]any{
		"content": "trace me", "contentType": "text", "model": "mock-chat", "clientRunID": "run-trace",
	})
	testkit.Expect(t, w, 200, "")

	w = env.Do("GET", base+"/messages", user, nil)
	testkit.Expect(t, w, 200, "")
	var list struct {
		Data struct {
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil {
		t.Fatal(err)
	}
	var asst map[string]any
	for _, m := range list.Data.Results {
		if m["role"] == "assistant" {
			asst = m
		}
	}
	if asst == nil {
		t.Fatalf("no assistant message: %s", w.Body.String())
	}
	pt, _ := asst["processTrace"].(map[string]any)
	if pt == nil || pt["enabled"] != true || pt["promptTrace"] == nil || pt["process"] == nil {
		t.Fatalf("assistant message lacks processTrace: %v", asst["processTrace"])
	}

	w = env.Do("GET", "/api/v1/messages/"+asst["publicID"].(string)+"/trace", user, nil)
	testkit.Expect(t, w, 200, "")
	var tr struct {
		Data struct {
			ProcessTrace map[string]any `json:"processTrace"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &tr); err != nil {
		t.Fatal(err)
	}
	pr, _ := tr.Data.ProcessTrace["promptTrace"].(map[string]any)
	blocks, _ := pr["blocks"].([]any)
	events, _ := tr.Data.ProcessTrace["events"].([]any)
	if pr == nil || pr["mode"] == "" || len(blocks) == 0 || len(events) == 0 || pr["promptFingerprint"] == "" {
		t.Fatalf("full trace incomplete: %s", w.Body.String())
	}

	w = env.Do("GET", base+"/runs", user, nil)
	testkit.Expect(t, w, 200, "")
	if strings.Contains(w.Body.String(), "http://") || !strings.Contains(w.Body.String(), `"endpoint":"chat_completions"`) {
		t.Fatalf("runs must not expose the upstream base URL: %s", w.Body.String())
	}

	w = env.Do("GET", "/api/v1/admin/conversation-events?page_size=5", admin, nil)
	testkit.Expect(t, w, 200, "")
	var ev struct {
		Data struct {
			Total   int              `json:"total"`
			Results []map[string]any `json:"results"`
		} `json:"data"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &ev); err != nil {
		t.Fatal(err)
	}
	if ev.Data.Total == 0 || len(ev.Data.Results) == 0 || ev.Data.Results[0]["runID"] == "" || ev.Data.Results[0]["eventType"] == "" {
		t.Fatalf("admin events not recorded: %s", w.Body.String())
	}
}

func init() {
	// Uploads finish their text processing before the request returns, so a
	// test can attach a file right after sending it.
	conversation.SetInlineFileProcessing(true)
}
