package mcp_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/mcp"
	"github.com/riochat/riochat/backend/internal/features/mcp/mcptest"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type settings struct {
	mu sync.Mutex
	s  mcp.RuntimeSettings
}

func (s *settings) MCPSettings(context.Context) mcp.RuntimeSettings {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.s
}

func (s *settings) set(fn func(*mcp.RuntimeSettings)) {
	s.mu.Lock()
	defer s.mu.Unlock()
	fn(&s.s)
}

type billing struct{ mode *string }

func (b billing) BillingMode(context.Context) (string, error) { return *b.mode, nil }

type fixture struct {
	*testkit.Env
	set  *settings
	mode *string
	fake *mcptest.Server
	user map[string]string
	adm  map[string]string
}

func newFixture(t *testing.T, opts ...testkit.Option) *fixture {
	t.Helper()
	f := &fixture{set: &settings{s: mcp.DefaultRuntimeSettings()}, mode: new(string), fake: mcptest.New(t)}
	f.set.s.Enabled = true
	*f.mode = "usage"
	feature := features.Feature{
		Name:       "mcp",
		Migrations: mcp.Migrations,
		Mount: func(d deps.Deps) error {
			_, err := mcp.MountService(d, mcp.Ports{Settings: f.set, BillingModes: billing{mode: f.mode}})
			return err
		},
	}
	f.Env = testkit.New(t, []features.Feature{feature}, opts...)
	f.user = f.Bearer(testkit.User(5))
	f.adm = f.Bearer(testkit.Admin(1))
	return f
}

func (f *fixture) admin(method, path string, body any) *httptest.ResponseRecorder {
	return f.Do(method, "/api/v1"+path, f.adm, body)
}

func (f *fixture) createServer(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	if body["baseURL"] == nil {
		body["baseURL"] = f.fake.URL
	}
	w := f.admin("POST", "/admin/mcp/servers", body)
	testkit.Expect(t, w, 200, "")
	var out struct{ Server map[string]any }
	testkit.Decode(t, w, &out)
	return out.Server
}

func (f *fixture) sync(t *testing.T, id any, query string) []map[string]any {
	t.Helper()
	w := f.admin("POST", fmt.Sprintf("/admin/mcp/servers/%v/sync%s", id, query), nil)
	testkit.Expect(t, w, 200, "")
	var out struct{ Results []map[string]any }
	testkit.Decode(t, w, &out)
	return out.Results
}

func TestAccessControl(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/mcp/tools"}, {"GET", "/settings/mcp-policy"},
		{"GET", "/admin/mcp/servers"}, {"POST", "/admin/mcp/servers"}, {"PATCH", "/admin/mcp/servers/order"},
		{"PATCH", "/admin/mcp/servers/1"}, {"DELETE", "/admin/mcp/servers/1"}, {"GET", "/admin/mcp/servers/1/tools"},
		{"PATCH", "/admin/mcp/servers/1/tools/status"}, {"POST", "/admin/mcp/servers/1/sync"}, {"PATCH", "/admin/mcp/tools/1"},
	} {
		w := f.Do(tc.method, "/api/v1"+tc.path, nil, "{}")
		if w.Code != 401 {
			t.Errorf("%s %s without a token: %d", tc.method, tc.path, w.Code)
		}
		if strings.HasPrefix(tc.path, "/admin") {
			w = f.Do(tc.method, "/api/v1"+tc.path, f.user, "{}")
			testkit.Expect(t, w, 403, "auth.admin_required")
		}
	}
	testkit.Expect(t, f.Do("GET", "/api/v1/mcp/tools", f.user, nil), 200, "")
}

func TestCreateServerThroughHTTP(t *testing.T) {
	f := newFixture(t)
	w := f.admin("POST", "/admin/mcp/servers", map[string]any{"name": "  Search  ", "baseURL": "https://mcp.example.com/mcp"})
	testkit.Expect(t, w, 200, "")
	env := testkit.Envelope(t, w)
	if env.ErrorMsg != "" {
		t.Fatalf("errorMsg %q", env.ErrorMsg)
	}
	var out struct{ Server map[string]any }
	testkit.Decode(t, w, &out)
	s := out.Server
	if s["name"] != "Search" || s["status"] != "active" || s["headersJSON"] != "{}" || s["sortOrder"] != float64(100) ||
		s["toolCount"] != float64(0) || s["activeToolCount"] != float64(0) || s["requiresToolMetadataSyncConfirmation"] != false || s["lastError"] != "" {
		t.Fatalf("server: %v", s)
	}
	if v, present := s["lastSyncedAt"]; !present || v != nil {
		t.Fatalf("lastSyncedAt must be an explicit null: %v", s)
	}
	for k := range s {
		if strings.Contains(strings.ToLower(k), "token") {
			t.Fatalf("token field %q in response", k)
		}
	}
	for _, k := range []string{"id", "baseURL", "createdAt", "updatedAt"} {
		if _, ok := s[k]; !ok {
			t.Errorf("missing %s", k)
		}
	}
}

func TestServerErrorsThroughHTTP(t *testing.T) {
	f := newFixture(t)
	cases := []struct {
		name string
		body map[string]any
		code string
	}{
		{"ftp", map[string]any{"name": "a", "baseURL": "ftp://x"}, "mcp.invalid_server_base_url"},
		{"metadata", map[string]any{"name": "a", "baseURL": "http://169.254.169.254/"}, "mcp.invalid_server_base_url"},
		{"userinfo", map[string]any{"name": "a", "baseURL": "https://user:pw@host/"}, "mcp.invalid_server_base_url"},
		{"google", map[string]any{"name": "a", "baseURL": "http://metadata.google.internal/"}, "mcp.invalid_server_base_url"},
		{"empty url", map[string]any{"name": "a", "baseURL": ""}, "mcp.invalid_server_base_url"},
		{"name 129", map[string]any{"name": strings.Repeat("x", 129), "baseURL": "https://a.example"}, "mcp.invalid_server_name"},
		{"status", map[string]any{"name": "a", "baseURL": "https://a.example", "status": "paused"}, "mcp.invalid_server_status"},
		{"headers array", map[string]any{"name": "a", "baseURL": "https://a.example", "headersJSON": `["a"]`}, "mcp.invalid_server_headers"},
		{"headers number", map[string]any{"name": "a", "baseURL": "https://a.example", "headersJSON": `{"a":1}`}, "mcp.invalid_server_headers"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			testkit.Expect(t, f.admin("POST", "/admin/mcp/servers", tc.body), 400, tc.code)
		})
	}
	w := f.admin("POST", "/admin/mcp/servers", map[string]any{"name": "a", "baseURL": "ftp://x"})
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid mcp server base url" {
		t.Fatalf("message %q", msg)
	}
	// Accepted: private and loopback addresses, a 128 character name.
	for _, u := range []string{"http://10.0.0.5:8080/mcp", "http://localhost:3000/mcp"} {
		f.createServer(t, map[string]any{"name": "ok", "baseURL": u})
	}
	f.createServer(t, map[string]any{"name": strings.Repeat("\u5b57", 128), "baseURL": "https://a.example"})
	testkit.Expect(t, f.admin("POST", "/admin/mcp/servers", "{not json"), 400, "request.invalid_body")
	testkit.Expect(t, f.admin("POST", "/admin/mcp/servers", `{"name":5}`), 400, "request.invalid_body")
	testkit.Expect(t, f.admin("POST", "/admin/mcp/servers", ""), 400, "request.invalid_body")
}

func TestServerLimitThroughHTTP(t *testing.T) {
	f := newFixture(t)
	for i := 0; i < 100; i++ {
		if err := f.DB.Exec("INSERT INTO mcp_servers (created_at, updated_at, name, base_url, headers_json, status, sort_order) VALUES (CURRENT_TIMESTAMP, CURRENT_TIMESTAMP, 's', 'https://a.example', '{}', 'active', ?)", (i+1)*100).Error; err != nil {
			t.Fatal(err)
		}
	}
	testkit.Expect(t, f.admin("POST", "/admin/mcp/servers", map[string]any{"name": "x", "baseURL": "https://a.example"}), 400, "mcp.server_limit_exceeded")
}

func TestPathIDValidation(t *testing.T) {
	f := newFixture(t)
	for _, id := range []string{"abc", "0", "-1", "1.5", "99999999999999999999"} {
		for _, tc := range []struct{ method, path string }{
			{"PATCH", "/admin/mcp/servers/" + id},
			{"DELETE", "/admin/mcp/servers/" + id},
			{"GET", "/admin/mcp/servers/" + id + "/tools"},
			{"PATCH", "/admin/mcp/servers/" + id + "/tools/status"},
			{"POST", "/admin/mcp/servers/" + id + "/sync"},
		} {
			w := f.admin(tc.method, tc.path, map[string]any{"name": "a", "baseURL": "https://a.example", "toolIDs": []int{1}, "status": "active"})
			testkit.Expect(t, w, 400, "mcp.server.invalid_id")
			if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid mcp server id" {
				t.Fatalf("message %q", msg)
			}
		}
		w := f.admin("PATCH", "/admin/mcp/tools/"+id, map[string]any{})
		testkit.Expect(t, w, 400, "mcp.tool.invalid_id")
		if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid mcp tool id" {
			t.Fatalf("message %q", msg)
		}
	}
}

func TestMissingTargetsAreGeneric500(t *testing.T) {
	f := newFixture(t)
	for _, tc := range []struct{ method, path string }{
		{"PATCH", "/admin/mcp/servers/4242"},
		{"POST", "/admin/mcp/servers/4242/sync"}, {"PATCH", "/admin/mcp/tools/4242"},
	} {
		w := f.admin(tc.method, tc.path, map[string]any{"name": "a", "baseURL": "https://a.example"})
		testkit.Expect(t, w, 500, "internal.error")
	}
}

func TestListAndRedaction(t *testing.T) {
	f := newFixture(t)
	f.createServer(t, map[string]any{"name": "a", "headersJSON": `{"X-Api-Key":"abc","X-Trace":"1","Cookie":"s"}`, "authToken": "tok"})
	w := f.admin("GET", "/admin/mcp/servers", nil)
	testkit.Expect(t, w, 200, "")
	if strings.Contains(w.Body.String(), "tok") && strings.Contains(w.Body.String(), `"authToken"`) {
		t.Fatal("token leaked")
	}
	var out struct{ Results []map[string]any }
	testkit.Decode(t, w, &out)
	var headers map[string]string
	if err := json.Unmarshal([]byte(out.Results[0]["headersJSON"].(string)), &headers); err != nil {
		t.Fatal(err)
	}
	if headers["X-Api-Key"] != "********" || headers["Cookie"] != "********" || headers["X-Trace"] != "1" {
		t.Fatalf("headers: %v", headers)
	}
	if strings.Contains(w.Body.String(), "abc") {
		t.Fatal("secret header value leaked")
	}
	// Stored value is unredacted so calls keep working.
	var stored string
	f.DB.Raw("SELECT headers_json FROM mcp_servers LIMIT 1").Scan(&stored)
	if !strings.Contains(stored, "abc") {
		t.Fatalf("stored headers must stay unredacted: %s", stored)
	}
	empty := newFixture(t)
	w = empty.admin("GET", "/admin/mcp/servers", nil)
	if !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatalf("empty list must be []: %s", w.Body)
	}
}

func TestUpdateServerThroughHTTP(t *testing.T) {
	f := newFixture(t)
	s := f.createServer(t, map[string]any{"name": "a", "authToken": "tok-1"})
	id := int(s["id"].(float64))
	var before string
	f.DB.Raw("SELECT auth_token_enc FROM mcp_servers WHERE id = ?", id).Scan(&before)
	w := f.admin("PATCH", fmt.Sprintf("/admin/mcp/servers/%d", id), map[string]any{"name": "b", "baseURL": "https://other.example", "status": "inactive"})
	testkit.Expect(t, w, 200, "")
	var out struct{ Server map[string]any }
	testkit.Decode(t, w, &out)
	if out.Server["name"] != "b" || out.Server["status"] != "inactive" || out.Server["baseURL"] != "https://other.example" {
		t.Fatalf("server: %v", out.Server)
	}
	var after string
	f.DB.Raw("SELECT auth_token_enc FROM mcp_servers WHERE id = ?", id).Scan(&after)
	if after != before || after == "" {
		t.Fatal("token must be kept when omitted")
	}
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/servers/%d", id), map[string]any{"name": "b", "baseURL": "https://other.example", "authToken": "tok-2"})
	f.DB.Raw("SELECT auth_token_enc FROM mcp_servers WHERE id = ?", id).Scan(&after)
	if after == before {
		t.Fatal("token must be replaced")
	}
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/servers/%d", id), map[string]any{"name": "", "baseURL": "https://other.example"}), 400, "mcp.invalid_server_name")
}

func TestSyncAndToolEndpoints(t *testing.T) {
	f := newFixture(t)
	f.fake.UseSSE(true)
	f.fake.SetTools(mcptest.Tool("search", "Search", "d", map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}}))
	s := f.createServer(t, map[string]any{"name": "a"})
	id := int(s["id"].(float64))

	tools := f.sync(t, id, "")
	if len(tools) != 1 {
		t.Fatalf("tools: %v", tools)
	}
	tl := tools[0]
	if tl["name"] != "search" || tl["displayName"] != "Search" || tl["status"] != "active" || tl["priceNanousd"] != float64(0) ||
		tl["attachmentInputMode"] != "none" || tl["sortOrder"] != float64(100) || tl["serverID"] != s["id"] {
		t.Fatalf("tool: %v", tl)
	}
	if _, ok := tl["inputSchemaJSON"].(string); !ok {
		t.Fatalf("inputSchemaJSON must be a string: %v", tl["inputSchemaJSON"])
	}
	for _, k := range []string{"serverName", "description", "attachmentArgument", "attachmentEncoding", "attachmentPromptArgument", "createdAt", "updatedAt"} {
		if _, ok := tl[k]; !ok {
			t.Errorf("tool lacks %s", k)
		}
	}
	w := f.admin("GET", "/admin/mcp/servers", nil)
	var list struct{ Results []map[string]any }
	testkit.Decode(t, w, &list)
	if list.Results[0]["toolCount"] != float64(1) || list.Results[0]["activeToolCount"] != float64(1) ||
		list.Results[0]["lastSyncedAt"] == nil || list.Results[0]["lastError"] != "" {
		t.Fatalf("server: %v", list.Results[0])
	}

	// Update the tool; the response is the tool object itself.
	tid := int(tl["id"].(float64))
	w = f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"displayName": "My Search", "priceNanousd": 2500000})
	testkit.Expect(t, w, 200, "")
	var upd map[string]any
	testkit.Decode(t, w, &upd)
	if upd["displayName"] != "My Search" || upd["priceNanousd"] != float64(2500000) || upd["id"] != tl["id"] {
		t.Fatalf("updated tool: %v", upd)
	}
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"priceNanousd": -1}), 400, "request.invalid_mcp_tool_price")
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"displayName": strings.Repeat("x", 161)}), 400, "mcp.invalid_tool_name")
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"description": strings.Repeat("x", 4097)}), 400, "mcp.invalid_tool_description")
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"status": "on"}), 400, "mcp.invalid_tool_status")
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"attachmentInputMode": "image", "attachmentArgument": "missing", "attachmentEncoding": "base64"}), 400, "mcp.invalid_attachment_configuration")
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"priceNanousd": "x"}), 400, "request.invalid_body")
	w = f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{"attachmentInputMode": "image", "attachmentArgument": "q"})
	if msg := testkit.Envelope(t, w).ErrorMsg; msg != "invalid MCP tool attachment configuration" {
		t.Fatalf("public message %q", msg)
	}
	testkit.Expect(t, f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%d", tid), map[string]any{}), 200, "")

	// Re-sync keeps the customized name unless told otherwise.
	f.sync(t, id, "")
	w = f.admin("GET", fmt.Sprintf("/admin/mcp/servers/%d/tools", id), nil)
	var res struct{ Results []map[string]any }
	testkit.Decode(t, w, &res)
	if res.Results[0]["displayName"] != "My Search" {
		t.Fatalf("customization lost: %v", res.Results[0])
	}
	w = f.admin("GET", "/admin/mcp/servers", nil)
	testkit.Decode(t, w, &list)
	if list.Results[0]["requiresToolMetadataSyncConfirmation"] != true {
		t.Fatalf("confirmation flag: %v", list.Results[0])
	}
	for _, v := range []string{"1", "t", "TRUE", "true", "True"} {
		f.sync(t, id, "?overwrite_customized_metadata="+v)
	}
	w = f.admin("GET", fmt.Sprintf("/admin/mcp/servers/%d/tools", id), nil)
	testkit.Decode(t, w, &res)
	if res.Results[0]["displayName"] != "Search" {
		t.Fatalf("overwrite must restore the remote name: %v", res.Results[0])
	}
	w = f.admin("GET", "/admin/mcp/servers", nil)
	testkit.Decode(t, w, &list)
	if list.Results[0]["requiresToolMetadataSyncConfirmation"] != false {
		t.Fatalf("confirmation flag: %v", list.Results[0])
	}
	for _, v := range []string{"0", "f", "false"} {
		f.sync(t, id, "?overwrite_customized_metadata="+v)
	}
	f.sync(t, id, "?overwrite_customized_metadata=")
	w = f.admin("POST", fmt.Sprintf("/admin/mcp/servers/%d/sync?overwrite_customized_metadata=maybe", id), nil)
	testkit.Expect(t, w, 400, "request.invalid_overwrite_customized_metadata")
}

func TestSyncFailureThroughHTTP(t *testing.T) {
	f := newFixture(t)
	s := f.createServer(t, map[string]any{"name": "a", "authToken": "very-secret"})
	id := int(s["id"].(float64))
	f.fake.FailList(500)
	w := f.admin("POST", fmt.Sprintf("/admin/mcp/servers/%d/sync", id), nil)
	testkit.Expect(t, w, 500, "internal.error")
	for _, leak := range []string{f.fake.URL, "very-secret", "secret-body", "leak.example"} {
		if strings.Contains(w.Body.String(), leak) {
			t.Fatalf("response leaks %q: %s", leak, w.Body)
		}
	}
	w = f.admin("GET", "/admin/mcp/servers", nil)
	var list struct{ Results []map[string]any }
	testkit.Decode(t, w, &list)
	if list.Results[0]["lastError"] != "Error while syncing MCP tools" {
		t.Fatalf("lastError %v", list.Results[0]["lastError"])
	}
	if strings.Contains(f.Logs.String(), "very-secret") {
		t.Fatal("the token must never reach the logs")
	}
}

func TestBulkStatusAndReorderThroughHTTP(t *testing.T) {
	f := newFixture(t)
	f.fake.SetTools(mcptest.Tool("a", "A", "", nil), mcptest.Tool("b", "B", "", nil))
	s1, s2 := f.createServer(t, map[string]any{"name": "one"}), f.createServer(t, map[string]any{"name": "two"})
	id1, id2 := int(s1["id"].(float64)), int(s2["id"].(float64))
	t1, t2 := f.sync(t, id1, ""), f.sync(t, id2, "")
	a1, b1, a2, b2 := t1[0]["id"], t1[1]["id"], t2[0]["id"], t2[1]["id"]

	base := fmt.Sprintf("/admin/mcp/servers/%d/tools/status", id1)
	testkit.Expect(t, f.admin("PATCH", base, map[string]any{"toolIDs": []int{}, "status": "inactive"}), 400, "mcp.invalid_tool_selection")
	testkit.Expect(t, f.admin("PATCH", base, map[string]any{"toolIDs": []any{a1}, "status": "paused"}), 400, "mcp.invalid_tool_status")
	w := f.admin("PATCH", base, map[string]any{"toolIDs": []any{a1, a2}, "status": "inactive"})
	testkit.Expect(t, w, 200, "")
	var res struct{ Results []map[string]any }
	testkit.Decode(t, w, &res)
	if len(res.Results) != 2 || res.Results[0]["status"] != "inactive" || res.Results[1]["status"] != "active" {
		t.Fatalf("bulk: %v", res.Results)
	}
	var st string
	f.DB.Raw("SELECT status FROM mcp_tools WHERE id = ?", a2).Scan(&st)
	if st != "active" {
		t.Fatal("a foreign tool id must be ignored")
	}

	order := func(entries ...map[string]any) *httptest.ResponseRecorder {
		return f.admin("PATCH", "/admin/mcp/servers/order", map[string]any{"servers": entries})
	}
	w = order(map[string]any{"serverID": id2, "toolIDs": []any{b2, a2}}, map[string]any{"serverID": id1, "toolIDs": []any{b1, a1}})
	testkit.Expect(t, w, 200, "")
	var ord struct {
		Results []struct {
			Server map[string]any
			Tools  []map[string]any
		}
	}
	testkit.Decode(t, w, &ord)
	if len(ord.Results) != 2 || ord.Results[0].Server["id"] != s2["id"] || ord.Results[0].Server["sortOrder"] != float64(100) ||
		ord.Results[1].Server["sortOrder"] != float64(200) {
		t.Fatalf("servers: %v", ord.Results)
	}
	tools := ord.Results[0].Tools
	if tools[0]["id"] != b2 || tools[0]["sortOrder"] != float64(100) || tools[1]["sortOrder"] != float64(200) || tools[0]["serverName"] != "two" {
		t.Fatalf("tools: %v", tools)
	}
	for _, bad := range [][]map[string]any{
		{},
		{{"serverID": id1, "toolIDs": []any{a1, b1}}},
		{{"serverID": id1, "toolIDs": []any{a1, b1}}, {"serverID": id1, "toolIDs": []any{a1, b1}}},
		{{"serverID": 0, "toolIDs": []any{a1, b1}}, {"serverID": id2, "toolIDs": []any{a2, b2}}},
		{{"serverID": id1, "toolIDs": []any{a1}}, {"serverID": id2, "toolIDs": []any{a2, b2}}},
		{{"serverID": id1, "toolIDs": []any{a1, a2}}, {"serverID": id2, "toolIDs": []any{b1, b2}}},
		{{"serverID": id1, "toolIDs": []any{a1, 0}}, {"serverID": id2, "toolIDs": []any{a2, b2}}},
	} {
		testkit.Expect(t, order(bad...), 400, "mcp.invalid_tool_selection")
	}
	// The static order path must not be captured by /servers/:id.
	w = f.admin("PATCH", "/admin/mcp/servers/order", map[string]any{"servers": []any{}})
	if testkit.Envelope(t, w).ErrorCode != "mcp.invalid_tool_selection" {
		t.Fatalf("order route captured by :id: %s", w.Body)
	}
}

func TestEnabledToolsAndPolicy(t *testing.T) {
	f := newFixture(t)
	f.fake.SetTools(mcptest.Tool("a", "A", "", nil), mcptest.Tool("b", "B", "", nil), mcptest.Tool("c", "C", "", nil))
	off, on := f.createServer(t, map[string]any{"name": "off"}), f.createServer(t, map[string]any{"name": "on"})
	f.sync(t, off["id"], "")
	tools := f.sync(t, on["id"], "")
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/servers/%v", off["id"]), map[string]any{"name": "off", "baseURL": f.fake.URL, "status": "inactive"})
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%v", tools[0]["id"]), map[string]any{"priceNanousd": 5})
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%v", tools[1]["id"]), map[string]any{"priceNanousd": 5, "status": "inactive"})
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%v", tools[2]["id"]), map[string]any{"priceNanousd": 5})

	get := func() []map[string]any {
		w := f.Do("GET", "/api/v1/mcp/tools", f.user, nil)
		testkit.Expect(t, w, 200, "")
		var out struct{ Results []map[string]any }
		testkit.Decode(t, w, &out)
		if out.Results == nil {
			t.Fatal("results must be an array")
		}
		return out.Results
	}
	f.set.set(func(s *mcp.RuntimeSettings) { s.Enabled = false })
	if r := get(); len(r) != 0 {
		t.Fatalf("switch off: %v", r)
	}
	f.set.set(func(s *mcp.RuntimeSettings) { s.Enabled = true })
	r := get()
	if len(r) != 2 || r[0]["name"] != "a" || r[1]["name"] != "c" || r[0]["serverName"] != "on" || r[0]["priceNanousd"] != float64(5) {
		t.Fatalf("enabled tools: %v", r)
	}
	*f.mode = "self"
	if r = get(); r[0]["priceNanousd"] != float64(0) || r[1]["priceNanousd"] != float64(0) {
		t.Fatalf("self mode prices: %v", r)
	}

	for in, want := range map[int]float64{0: 32, -1: 32, 500: 128, 12: 12} {
		f.set.set(func(s *mcp.RuntimeSettings) { s.MaxSelectedToolsPerMessage = in })
		w := f.Do("GET", "/api/v1/settings/mcp-policy", f.user, nil)
		testkit.Expect(t, w, 200, "")
		var out map[string]any
		testkit.Decode(t, w, &out)
		if out["maxSelectedToolsPerMessage"] != want {
			t.Errorf("configured %d: %v, want %v", in, out, want)
		}
	}
}

func TestDeleteThroughHTTP(t *testing.T) {
	f := newFixture(t)
	f.fake.SetTools(mcptest.Tool("a", "A", "", nil))
	if err := f.DB.Exec("CREATE TABLE chat_conversation_project_mcp_tools (project_id integer NOT NULL, tool_id integer NOT NULL, sort_order integer NOT NULL DEFAULT 0, PRIMARY KEY (project_id, tool_id))").Error; err != nil {
		t.Fatal(err)
	}
	s := f.createServer(t, map[string]any{"name": "a"})
	tools := f.sync(t, s["id"], "")
	f.DB.Exec("INSERT INTO chat_conversation_project_mcp_tools (project_id, tool_id, sort_order) VALUES (1, ?, 1)", tools[0]["id"])
	w := f.admin("DELETE", fmt.Sprintf("/admin/mcp/servers/%v", s["id"]), nil)
	testkit.Expect(t, w, 200, "")
	var out map[string]any
	testkit.Decode(t, w, &out)
	if out["deleted"] != true {
		t.Fatalf("body: %v", out)
	}
	// deleting again (or a missing id) is still a success
	for _, id := range []any{s["id"], 4242} {
		w = f.admin("DELETE", fmt.Sprintf("/admin/mcp/servers/%v", id), nil)
		testkit.Expect(t, w, 200, "")
	}
	var tc, lc int64
	f.DB.Raw("SELECT COUNT(*) FROM mcp_tools").Scan(&tc)
	f.DB.Raw("SELECT COUNT(*) FROM chat_conversation_project_mcp_tools").Scan(&lc)
	if tc != 0 || lc != 0 {
		t.Fatalf("tools=%d links=%d", tc, lc)
	}
}

func TestSyncHeadersThroughHTTP(t *testing.T) {
	f := newFixture(t)
	f.fake.SetTools(mcptest.Tool("a", "A", "", nil))
	s := f.createServer(t, map[string]any{"name": "a", "authToken": "tok", "headersJSON": `{"X-User":"${RIOCHAT_SIGNED_USER_CONTEXT}","X-Static":"yes"}`})
	f.sync(t, s["id"], "")
	for _, r := range f.fake.Requests() {
		if r.Header.Get("X-User") != "" || r.Header.Get("X-Static") != "yes" || r.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("%s: %v", r.Method, r.Header)
		}
	}
}

func TestDefaultMountWorksWithoutPorts(t *testing.T) {
	env := testkit.New(t, []features.Feature{{Name: "mcp", Migrations: mcp.Migrations, Mount: mcp.Mount}})
	user := env.Bearer(testkit.User(1))
	w := env.Do("GET", "/api/v1/mcp/tools", user, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"results":[]`) {
		t.Fatalf("the feature is off by default: %s", w.Body)
	}
	w = env.Do("GET", "/api/v1/settings/mcp-policy", user, nil)
	testkit.Expect(t, w, 200, "")
	if !strings.Contains(w.Body.String(), `"maxSelectedToolsPerMessage":32`) {
		t.Fatalf("policy: %s", w.Body)
	}
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/mcp/servers", env.Bearer(testkit.Admin(1)), nil), 200, "")
}

func TestEndToEndRunThroughService(t *testing.T) {
	f := newFixture(t, testkit.WithConfig(func(c *config.Config) { c.Security.MCPUserContextSecret = "s" }))
	f.fake.SetTools(mcptest.Tool("count", "Count", "", map[string]any{"type": "object", "properties": map[string]any{"count": map[string]any{"type": "integer"}}}))
	s := f.createServer(t, map[string]any{"name": "srv", "headersJSON": `{"X-User":"${RIOCHAT_SIGNED_USER_CONTEXT}"}`})
	tools := f.sync(t, s["id"], "")
	f.admin("PATCH", fmt.Sprintf("/admin/mcp/tools/%v", tools[0]["id"]), map[string]any{"priceNanousd": 100})

	svc := mcp.NewService(f.Deps, mcp.Ports{Settings: f.set})
	rt, err := svc.ResolveRunTools(context.Background(), []uint{uint(tools[0]["id"].(float64))})
	if err != nil || !rt.HasTools() {
		t.Fatalf("%v", err)
	}
	out := rt.Call(context.Background(), mcp.CallRequest{ModelName: "count", ArgumentsJSON: `{"count":"3"}`, UserID: 5, ConversationID: 8, RequestID: "r1"})
	if out.Status != mcp.CallSuccess {
		t.Fatalf("%+v", out)
	}
	last, _ := f.fake.Last("tools/call")
	claims, err := mcp.VerifyUserContext("s", last.Header.Get("X-User"), f.Clock.Now())
	if err != nil || claims.UserID != 5 || claims.ConversationID != 8 || claims.Audience != f.fake.URL {
		t.Fatalf("signed context from the configured secret: %+v %v", claims, err)
	}
	plan := mcp.PlanBilling(rt.Usage(), "usage")
	if plan.TotalNanoUSD != 100 || len(plan.Lines) != 1 {
		t.Fatalf("plan: %+v", plan)
	}
}
