package conversation

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/platform/migrate"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestEveryRouteRequiresAuthentication(t *testing.T) {
	e := newEnv(t)
	routes := []struct{ m, p string }{
		{"POST", "/conversations"}, {"GET", "/conversations"}, {"GET", "/conversations/search"}, {"GET", "/conversations/default-model-candidate"},
		{"GET", "/conversations/export"}, {"POST", "/conversations/project"}, {"GET", "/conversations/x"}, {"PATCH", "/conversations/x/title"},
		{"POST", "/conversations/x/title/regenerate"}, {"PATCH", "/conversations/x/labels"}, {"PATCH", "/conversations/x/star"},
		{"PATCH", "/conversations/x/archive"}, {"DELETE", "/conversations/x"}, {"PATCH", "/conversations/x/project"}, {"GET", "/conversations/x/export"},
		{"GET", "/conversations/x/messages"}, {"GET", "/conversations/x/messages/preview"}, {"POST", "/conversations/x/messages"},
		{"POST", "/conversations/x/messages/stream"}, {"DELETE", "/conversations/x/messages/y"}, {"POST", "/conversations/x/messages/y/fork"},
		{"PUT", "/conversations/x/messages/y/ui-state"}, {"PATCH", "/messages/x"}, {"GET", "/messages/x/trace"}, {"PUT", "/messages/x/feedback"},
		{"GET", "/context-artifacts/1"}, {"GET", "/conversations/x/runs"}, {"POST", "/conversation-runs/statuses"}, {"GET", "/conversation-runs/stream"},
		{"GET", "/conversation-runs/r/stream"}, {"POST", "/conversation-runs/r/cancel"}, {"GET", "/conversation-runs/r/tool-calls/t"},
		{"POST", "/conversations/x/media/images/generations/stream"}, {"POST", "/conversations/x/media/images/edits/stream"},
		{"POST", "/conversations/x/media/videos/generations/stream"}, {"POST", "/conversations/x/media/videos/extensions/stream"},
		{"POST", "/temporary-chat/messages/stream"}, {"GET", "/conversation-projects"}, {"POST", "/conversation-projects"},
		{"POST", "/conversation-projects/reorder"}, {"PATCH", "/conversation-projects/x"}, {"DELETE", "/conversation-projects/x"},
		{"GET", "/conversations/x/share"}, {"POST", "/conversations/x/share"}, {"POST", "/conversations/x/share/regenerate"},
		{"DELETE", "/conversations/x/share"}, {"POST", "/conversations/shares/revoke"}, {"POST", "/shared-conversations/x/clone"},
		{"POST", "/files"}, {"GET", "/files"}, {"POST", "/files/processing/statuses"}, {"POST", "/files/embeddings"}, {"GET", "/files/x/content"},
		{"GET", "/files/x/extract"}, {"GET", "/files/x/processing"}, {"PATCH", "/files/x"}, {"DELETE", "/files/x"}, {"GET", "/runtime/chat-file-policy"},
	}
	for _, r := range routes {
		w := e.call(r.m, r.p, nil, nil)
		if w.Code != 401 {
			t.Errorf("%s %s without a token: %d", r.m, r.p, w.Code)
		}
	}
	// the public share routes need no token
	if w := e.call("GET", "/shared-conversations/x", nil, nil); w.Code != 404 {
		t.Errorf("public share read: %d", w.Code)
	}
}

func TestMigrationsAreIdempotentAndNamed(t *testing.T) {
	e := newEnv(t)
	// all tables exist with their contractual names
	want := []string{"chat_conversations", "chat_conversation_projects", "chat_conversation_project_mcp_tools", "chat_conversation_project_skills",
		"chat_conversation_project_knowledge_bases", "chat_conversation_shares", "chat_messages", "chat_feedback", "chat_attachments", "chat_runs",
		"chat_run_events", "chat_context_records", "chat_message_chunks", "chat_message_ui_states", "file_objects", "file_chunks", "file_storage_quotas"}
	for _, tbl := range want {
		if !e.DB.Migrator().HasTable(tbl) {
			t.Errorf("table %s missing", tbl)
		}
	}
	cols := map[string][]string{
		"chat_conversations":         {"user_id", "project_id", "public_id", "title", "labels_json", "labels_manually_managed", "model", "provider", "session_key", "is_starred", "starred_at", "message_count", "status", "context_policy", "last_compacted_at", "last_response_id", "last_prompt_fingerprint", "deleted_at"},
		"chat_conversation_projects": {"user_id", "public_id", "name", "description", "system_prompt", "default_model", "mcp_default_mode", "color", "icon", "sort_order", "status"},
		"chat_conversation_shares":   {"share_id", "conversation_id", "user_id", "status", "title_snapshot", "model_snapshot", "message_ids_json", "default_message_ids_json", "revoked_at", "regenerated_at", "last_accessed_at"},
		"chat_messages":              {"conversation_id", "user_id", "public_id", "parent_message_id", "run_id", "role", "content_type", "content", "reasoning_content", "branch_reason", "source_message_id", "token_usage", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "latency_ms", "reasoning_effort", "billed_currency", "billed_nanousd", "pricing_snapshot", "status", "error_code", "error_message", "moderation_event_id", "moderation_categories_json", "knowledge_sources_json", "edited_at"},
		"chat_feedback":              {"user_id", "conversation_id", "message_id", "feedback"},
		"chat_attachments":           {"conversation_id", "message_id", "user_id", "file_id", "kind", "file_name", "mime_type", "file_size", "sha256", "storage_path", "status", "meta_json", "uploaded_at"},
		"chat_runs":                  {"run_id", "request_id", "user_id", "conversation_id", "task_type", "endpoint", "provider", "provider_protocol", "upstream_id", "upstream_model_id", "upstream_name", "requested_model_name", "platform_model_name", "routed_binding_code", "model_vendor", "model_icon", "upstream_model_name", "input_tokens", "output_tokens", "cache_read_tokens", "cache_write_tokens", "reasoning_tokens", "reasoning_effort", "tool_calls_count", "first_token_latency_ms", "total_latency_ms", "status", "error_code", "error_message", "moderation_state", "moderation_event_id", "moderation_categories_json", "started_at", "ended_at"},
		"chat_run_events":            {"run_id", "event_scope", "event_id", "tool_name", "mcp_server_snapshot", "input_json", "output_json", "error_json", "seq"},
		"chat_context_records":       {"record_type", "kind", "source_type", "source_id", "source_title", "content", "content_hash", "token_estimate", "score", "metadata_json", "expires_at"},
		"chat_message_chunks":        {"conversation_id", "message_id", "user_id", "role", "chunk_index", "content", "token_count", "embedding_signature"},
		"chat_message_ui_states":     {"user_id", "conversation_id", "message_id", "block_id", "state_json", "created_at", "updated_at"},
	}
	for tbl, cs := range cols {
		for _, c := range cs {
			if !e.DB.Migrator().HasColumn(tbl, c) {
				t.Errorf("%s.%s missing", tbl, c)
			}
		}
	}
	if e.DB.Migrator().HasColumn("chat_message_ui_states", "deleted_at") {
		t.Error("ui states are hard-deleted: no deleted_at column")
	}
	for _, tbl := range []string{"chat_conversation_project_mcp_tools", "chat_conversation_project_skills", "chat_conversation_project_knowledge_bases"} {
		if e.DB.Migrator().HasColumn(tbl, "deleted_at") {
			t.Errorf("link table %s has no soft delete", tbl)
		}
	}
	// unique constraints are real
	conv := &Conversation{UserID: 1, PublicID: newPublicID(), SessionKey: "dup-key", Status: "active"}
	if err := e.DB.Create(conv).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Create(&Conversation{UserID: 1, PublicID: newPublicID(), SessionKey: "dup-key", Status: "active"}).Error; err == nil {
		t.Error("session_key must be unique")
	}
	if err := e.DB.Create(&Feedback{UserID: 1, ConversationID: 1, MessageID: 5, Feedback: "up"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Create(&Feedback{UserID: 1, ConversationID: 1, MessageID: 5, Feedback: "down"}).Error; err == nil {
		t.Error("(user_id, message_id) must be unique")
	}
	if err := e.DB.Create(&UIState{UserID: 1, MessageID: 5, BlockID: "b"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := e.DB.Create(&UIState{UserID: 1, MessageID: 5, BlockID: "b"}).Error; err == nil {
		t.Error("(message_id, block_id) must be unique")
	}
	// running the steps again changes nothing and does not fail
	for round := 0; round < 2; round++ {
		if err := migrate.Run(context.Background(), e.DB, nil, migrate.Set{Area: "conversation", Steps: Migrations()}); err != nil {
			t.Fatalf("re-running migrations: %v", err)
		}
	}
	var n int64
	e.DB.Model(&Conversation{}).Count(&n)
	if n != 1 {
		t.Fatalf("data preserved: %d", n)
	}
}

func TestCoreHelpers(t *testing.T) {
	if got := inferProvider("  GPT-4o"); got != "openai" {
		t.Fatal(got)
	}
	if got := inferProvider("Claude-3"); got != "anthropic" {
		t.Fatal(got)
	}
	if got := inferProvider("gemini"); got != "internal" {
		t.Fatal(got)
	}
	if escapeLike(`a%b_c!d`) != `a!%b!_c!!d` {
		t.Fatal(escapeLike(`a%b_c!d`))
	}
	if estimateTokens("") != 0 || estimateTokens("abc") != 1 || estimateTokens("abcd") != 2 {
		t.Fatal("estimateTokens")
	}
	if fallbackTitle("  \"Hello   World, this is long text\" ") != "Hello World, th" {
		t.Fatalf("fallbackTitle: %q", fallbackTitle("  \"Hello   World, this is long text\" "))
	}
	if got := fallbackTitle("Explain quantum computing"); got != "Explain quantum" {
		t.Fatalf("fallbackTitle must not end with a space: %q", got)
	}
	if fallbackTitle("   ") != "" {
		t.Fatal("blank")
	}
	if !isPlaceholderTitle(" NEW CHAT ") || !isPlaceholderTitle("Untitled chat") || !isPlaceholderTitle("untitled chat") || isPlaceholderTitle("My chat") || isPlaceholderTitle("") {
		t.Fatal("placeholder")
	}
	if got := truncateRunes("\u4f60\u597d\u4e16\u754c", 2); got != "\u4f60\u597d" {
		t.Fatal(got)
	}
	if !reflect.DeepEqual(dedupeUints([]uint{0, 3, 3, 1}, true), []uint{3, 1}) || !reflect.DeepEqual(dedupeUints([]uint{0, 0}, false), []uint{0}) {
		t.Fatal("dedupeUints")
	}
	ch := chunk([]int{1, 2, 3, 4, 5}, 2)
	if len(ch) != 3 || len(ch[2]) != 1 {
		t.Fatalf("chunk: %v", ch)
	}
	if len(newPublicID()) != 32 || strings.Contains(newPublicID(), "-") {
		t.Fatal("public id")
	}
	if id := newFileID(); !strings.HasPrefix(id, "file_") || len(id) != 37 {
		t.Fatal(id)
	}
}

func TestNormalizeLabels(t *testing.T) {
	cases := []struct {
		in   []string
		out  []string
		fail bool
	}{
		{[]string{"#AI", "ai", " Work  Notes "}, []string{"AI", "Work Notes"}, false},
		{[]string{}, []string{}, false},
		{[]string{"##x"}, []string{"x"}, false},
		{[]string{"", "a"}, nil, true},
		{[]string{" # "}, nil, true},
		{[]string{strings.Repeat("a", 25)}, nil, true},
		{[]string{"a", "b", "c", "d", "e", "f", "g"}, nil, true},
		{[]string{"A", "a", "B", "b", "C", "c", "D", "d", "E", "e", "F", "f", "G"}, nil, true},
	}
	for _, c := range cases {
		got, ok := normalizeLabels(c.in)
		if c.fail {
			if ok {
				t.Errorf("%q should fail, got %v", c.in, got)
			}
			continue
		}
		if !ok || !reflect.DeepEqual(got, c.out) {
			t.Errorf("%q -> %v (%v), want %v", c.in, got, ok, c.out)
		}
	}
}

func TestParseGeneratedTitle(t *testing.T) {
	cases := map[string]string{
		`{"title":"Hello"}`:                           "Hello",
		"```json\n{\"title\": \"Fenced\"}\n```":       "Fenced",
		`{'title': 'Single'}`:                         "Single",
		`title: "Loose quoting"`:                      "Loose quoting",
		`{"title":"  “Curly”  "}`:                     "Curly",
		`{"title":"a   b \n c"}`:                      "a b c",
		`not json at all`:                             "",
		`{"title":""}`:                                "",
		`{"title":"` + strings.Repeat("x", 90) + `"}`: strings.Repeat("x", 80),
	}
	for in, want := range cases {
		if got := parseGeneratedTitle(in); got != want {
			t.Errorf("parseGeneratedTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseMetadataReplyAndLabelCleaning(t *testing.T) {
	title, labels := parseMetadataReply("```json\n{\"title\":\"T\",\"labels\":[\"#Go\",\"go\",\"\\\"Quoted\\\"\",\"" + strings.Repeat("l", 30) + "\",\"a\",\"b\",\"c\",\"d\",\"e\"]}\n```")
	if title != "T" || !reflect.DeepEqual(labels, []string{"Go", "Quoted", "a", "b", "c", "d"}) {
		t.Fatalf("%q %v", title, labels)
	}
	if title, labels = parseMetadataReply("garbage"); title != "" || len(labels) != 0 {
		t.Fatalf("garbage: %q %v", title, labels)
	}
}

func TestTitleTranscriptAndPrompt(t *testing.T) {
	msgs := []Message{{Role: "user", Content: "first   question"}, {Role: "assistant", Content: "answer"}}
	if got := titleTranscript(msgs, 5000); got != "user: first question\nassistant: answer" {
		t.Fatalf("transcript: %q", got)
	}
	long := []Message{{Role: "user", Content: strings.Repeat("a", 30000)}, {Role: "assistant", Content: "recent"}}
	got := titleTranscript(long, 100)
	if !strings.Contains(got, truncatedMarker) || !strings.Contains(got, "assistant: recent") {
		t.Fatalf("budget: %q", got)
	}
	if strings.Index(got, "assistant: recent") < strings.Index(got, "user:") {
		t.Fatal("oldest to newest order")
	}
	if buildTitlePrompt("T {{MESSAGES}} E", "X") != "T X E" || buildTitlePrompt("T", "X") != "T\n\nX" || !strings.Contains(buildTitlePrompt("", "X"), "X") {
		t.Fatal("buildTitlePrompt")
	}
}

func TestLatestVisibleBranch(t *testing.T) {
	one, two, three, four := uint(1), uint(2), uint(3), uint(4)
	msgs := []Message{
		{PublicID: "a"}, {PublicID: "b", ParentMessageID: &one}, {PublicID: "c", ParentMessageID: &one}, {PublicID: "d", ParentMessageID: &three},
	}
	for i := range msgs {
		msgs[i].ID = uint(i + 1)
	}
	if got := latestVisibleBranch(msgs); !reflect.DeepEqual(got, []string{"a", "c", "d"}) {
		t.Fatalf("branch: %v", got)
	}
	_ = two
	_ = four
	// cycles terminate
	cyc := []Message{{PublicID: "x", ParentMessageID: &two}, {PublicID: "y", ParentMessageID: &one}}
	cyc[0].ID, cyc[1].ID = 1, 2
	if got := latestVisibleBranch(cyc); len(got) > 2 {
		t.Fatalf("cycle: %v", got)
	}
	if got := latestVisibleBranch(nil); got == nil || len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
	// a missing parent makes the message a root
	missing := uint(99)
	if got := latestVisibleBranch([]Message{{PublicID: "z", ParentMessageID: &missing}}); !reflect.DeepEqual(got, []string{"z"}) {
		t.Fatalf("orphan: %v", got)
	}
}

func TestSanitizePayload(t *testing.T) {
	raw := `{"upstreamName":"x","upstream":{"name":"n","displayName":"d","id":1},"authorization":"a","Set-Cookie":"c","api_key":"k","clientSecretKey":"s",
	"nested":[{"file_id":"f","fileName":"n","Preview":"p","citations":[1],"upstream_debug":{},"keep":true,"toolCallId":"t"}]}`
	owner := sanitizePayload(raw, false)
	var o map[string]any
	if err := json.Unmarshal([]byte(owner), &o); err != nil {
		t.Fatal(err)
	}
	if _, ok := o["upstreamName"]; ok {
		t.Fatal("upstreamName removed for owner")
	}
	up := o["upstream"].(map[string]any)
	if _, ok := up["name"]; ok || up["id"] == nil {
		t.Fatalf("upstream.name removed: %v", up)
	}
	if _, ok := up["displayName"]; ok {
		t.Fatalf("displayName removed")
	}
	if o["authorization"] == nil || o["api_key"] == nil {
		t.Fatalf("owner keeps credentials-like keys: %v", o)
	}
	pub := sanitizePayload(raw, true)
	for _, banned := range []string{"authorization", "Set-Cookie", "api_key", "clientSecretKey", "file_id", "fileName", "Preview", "citations", "upstream_debug", "upstreamName"} {
		if strings.Contains(pub, banned) {
			t.Errorf("public payload still has %q: %s", banned, pub)
		}
	}
	if !strings.Contains(pub, `"keep":true`) || !strings.Contains(pub, `"toolCallId":"t"`) {
		t.Fatalf("other fields remain (public shares keep tool call ids): %s", pub)
	}
	// Copies for another account drop credentials, debug data and tool call ids
	// but keep file references.
	cl := sanitizePayloadFor(raw, scrubClone)
	for _, banned := range []string{"authorization", "Set-Cookie", "api_key", "clientSecretKey", "upstream_debug", "upstreamName", "toolCallId"} {
		if strings.Contains(cl, banned) {
			t.Errorf("clone payload still has %q: %s", banned, cl)
		}
	}
	if !strings.Contains(cl, "fileName") || !strings.Contains(cl, `"keep":true`) {
		t.Fatalf("clone keeps display data: %s", cl)
	}
	if sanitizePayload("{broken", true) != "" || sanitizePayload("{broken", false) != "{broken" || sanitizePayload("", true) != "" {
		t.Fatal("invalid json handling")
	}
}

func TestNormalizeUIState(t *testing.T) {
	ok := func(raw string, wantClear bool) {
		t.Helper()
		_, clr, err := normalizeUIState(json.RawMessage(raw))
		if err != nil || clr != wantClear {
			t.Errorf("%s: clear=%v err=%v", raw, clr, err)
		}
	}
	bad := func(raw string) {
		t.Helper()
		if _, _, err := normalizeUIState(json.RawMessage(raw)); err == nil {
			t.Errorf("%s must be invalid", raw)
		}
	}
	ok(`null`, true)
	ok(``, true)
	ok(`{}`, true)
	ok(`  { }  `, false == false)
	ok(`{"a":1}`, false)
	ok(`{"a":{"b":{"c":1}}}`, false)
	bad(`{"a":{"b":{"c":{"d":1}}}}`)
	bad(`[1]`)
	bad(`"s"`)
	bad(`{"bad key":1}`)
	bad(`{"a":` + `"` + strings.Repeat("x", 4100) + `"}`)
	bad(`{broken`)
	c, _, _ := normalizeUIState(json.RawMessage(`{ "a" : "<&>" }`))
	if c != `{"a":"<&>"}` {
		t.Fatalf("compact: %s", c)
	}
	if !validUIKey("a-b_C9") || validUIKey("") || validUIKey("a b") || validUIKey(strings.Repeat("a", 65)) || !validUIKey(strings.Repeat("a", 64)) {
		t.Fatal("validUIKey")
	}
}

func TestSanitizeControlsAndOptionsLimits(t *testing.T) {
	in := map[string]any{}
	for i := 0; i < 100; i++ {
		in[fmt.Sprintf("k%03d", i)] = true
	}
	if got := sanitizeControls(in); len(got) != 64 {
		t.Fatalf("at most 64 controls: %d", len(got))
	}
	got := sanitizeControls(map[string]any{" trimmed ": "v", strings.Repeat("k", 65): "v", "n": 1.5, "nil": nil, "obj": map[string]any{}, "long": strings.Repeat("v", 129), "ok": strings.Repeat("v", 128)})
	if len(got) != 3 || got["trimmed"] != "v" || got["n"] != 1.5 || got["ok"] == nil {
		t.Fatalf("controls: %v", got)
	}
	if sanitizeOptions(map[string]any{"stream": true, "prompt": "x"}, true) != nil {
		t.Fatal("empty options become absent")
	}
	for _, key := range []string{"contents", "instructions", "input", "messages", "model", "prompt", "stream", "system", "systemInstruction"} {
		if o := sanitizeOptions(map[string]any{key: 1, "x": 1}, true); o[key] != nil || o["x"] == nil {
			t.Errorf("reserved key %s", key)
		}
	}
	if reasoningEffortOf(nil, map[string]any{"reasoning_effort": "ULTRA"}) != "ultra" || reasoningEffortOf(map[string]any{"reasoning": "bogus"}, nil) != "" {
		t.Fatal("reasoning effort")
	}
}

func TestReasoningEffortSurfacesOnMessages(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	u, a := pair(e.send(e.u1, id, "q", map[string]any{"controls": map[string]any{"reasoning": "High"}, "options": map[string]any{"reasoningEffort": "medium"}}))
	if u["reasoningEffort"] != "medium" && u["reasoningEffort"] != "high" {
		t.Fatalf("effort: %v", u["reasoningEffort"])
	}
	if a["reasoningEffort"] == nil {
		t.Fatalf("assistant effort: %v", a)
	}
	_, rs := results(t, e.call("GET", "/conversations/"+id+"/runs", e.u1, nil))
	if rs[0]["reasoningEffort"] == nil {
		t.Fatalf("run effort: %v", rs[0])
	}
}

func TestReasoningPassbackAndTurnLimit(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task != taskChat {
			return defaultGenerate(ctx, req, emit)
		}
		return GenerateResult{Content: "ans", Reasoning: "think", Usage: Usage{Input: 1, Output: 1}}, nil
	})
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "q1", nil)
	e.send(e.u1, id, "q2", nil)
	reqs := chatRequests(e)
	if reqs[1].Messages[1].Reasoning != "" {
		t.Fatalf("no passback by default: %+v", reqs[1].Messages)
	}
	e.set.users["1:chat.reasoning_content_passback"] = "true"
	e.send(e.u1, id, "q3", nil)
	reqs = chatRequests(e)
	if reqs[2].Messages[1].Reasoning != "think" {
		t.Fatalf("passback enabled: %+v", reqs[2].Messages)
	}
	// history is trimmed to the configured number of turns
	e.set.mutate(func(r *RuntimeSettings) { r.ContextMaxTurns = 1 })
	e.send(e.u1, id, "q4", nil)
	reqs = chatRequests(e)
	if got := len(reqs[3].Messages); got != 3 { // 1 turn (2 messages) + the new message
		t.Fatalf("turn limit: %d messages", got)
	}
}

func chatRequests(e *tenv) []GenerateRequest {
	var out []GenerateRequest
	for _, r := range e.gen.requests() {
		if r.Task == taskChat {
			out = append(out, r)
		}
	}
	return out
}

func TestMetadataBillingFailureStopsUpdates(t *testing.T) {
	e := newEnv(t)
	e.bill.basicErr = fmt.Errorf("ledger down")
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskLabels {
			return GenerateResult{Content: `{"title":"Billed Title","labels":["x"]}`, Usage: Usage{Input: 5, Output: 5}}, nil
		}
		return defaultGenerate(ctx, req, emit)
	})
	id := e.newConv(e.u1, "", "gpt-4o")
	e.send(e.u1, id, "hello there", nil)
	eventually(t, "metadata call", func() bool { return len(e.bill.charges()) == 1 })
	if c := e.bill.charges(); c[0].Service != "title_labels" {
		t.Fatalf("service: %+v", c[0])
	}
	// give the background task time to (not) update
	e.Deps.Lifecycle.Drain().Begin()
	if got := e.convRow(id); got.Title == "Billed Title" || got.LabelsJSON != "[]" {
		t.Fatalf("billing failure must stop metadata: %+v", got)
	}
}

func TestMetadataWithoutLabelsLeavesThemEmpty(t *testing.T) {
	e := newEnv(t)
	e.gen.set(func(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error) {
		if req.Task == taskLabels {
			return GenerateResult{Content: `{"title":"Only Title"}`}, nil
		}
		return defaultGenerate(ctx, req, emit)
	})
	id := e.newConv(e.u1, "", "gpt-4o")
	e.send(e.u1, id, "hmm", nil)
	eventually(t, "title", func() bool { return e.convRow(id).Title == "Only Title" })
	if got := e.convRow(id).LabelsJSON; got != "[]" {
		t.Fatalf("labels: %s", got)
	}
}

func TestMessageTreeCountsAndIsolationOfDeletedConversation(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	e.ok("DELETE", "/conversations/"+id, e.u1, nil)
	e.expectErr("GET", "/conversations/"+id+"/messages", e.u1, nil, 404, "conversation.not_found")
	e.expectErr("PUT", "/messages/"+str(a, "publicID")+"/feedback", e.u1, map[string]string{"feedback": "up"}, 404, "message.not_found")
	e.expectErr("GET", "/messages/"+str(a, "publicID")+"/trace", e.u1, nil, 404, "message.not_found")
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", nil), 404, "conversation.not_found")
}

func TestAdminSeesOnlyOwnConversations(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "")
	e.expectErr("GET", "/conversations/"+id, e.admin, nil, 404, "conversation.not_found")
}

func TestDefaultMountWorksStandalone(t *testing.T) {
	env := testkit.New(t, []features.Feature{{Name: "conversation", Migrations: Migrations, Mount: Mount}})
	hdr := env.Bearer(testkit.User(1))
	w := env.Do("POST", "/api/v1/conversations", hdr, map[string]string{"title": "standalone", "model": "gpt-4o"})
	testkit.Expect(t, w, 200, "")
	var c map[string]any
	testkit.Decode(t, w, &c)
	id := str(c, "publicID")
	// without a model layer the send path reports the route as unconfigured
	w = env.Do("POST", "/api/v1/conversations/"+id+"/messages", hdr, sendBody("hi", nil))
	testkit.Expect(t, w, 503, "llm.model_route_not_configured")
	w = env.Do("GET", "/api/v1/conversations/"+id+"/messages", hdr, nil)
	testkit.Expect(t, w, 200, "")
}

func TestBlankPathIDsGiveResourceSpecificCodes(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a := pair(e.send(e.u1, id, "q", nil))
	cases := []struct {
		m, p, code string
		body       any
	}{
		{"GET", "/conversations/%20", "conversation.invalid_id", nil},
		{"PATCH", "/conversations/%20/title", "conversation.invalid_id", map[string]string{"title": "x"}},
		{"DELETE", "/conversations/%20", "conversation.invalid_id", nil},
		{"GET", "/messages/%20/trace", "message.invalid_id", nil},
		{"PATCH", "/messages/%20", "message.invalid_id", map[string]string{"content": "x"}},
		{"PUT", "/messages/%20/feedback", "message.invalid_id", map[string]string{"feedback": "up"}},
		{"DELETE", "/conversations/" + id + "/messages/%20", "message.invalid_id", nil},
		{"PATCH", "/conversation-projects/%20", "conversation_project.invalid_id", map[string]any{"name": "x"}},
		{"DELETE", "/conversation-projects/%20", "conversation_project.invalid_id", nil},
		{"GET", "/shared-conversations/%20", "share.invalid_id", nil},
		{"GET", "/files/%20/processing", "file.invalid_id", nil},
		{"PATCH", "/files/%20", "file.invalid_id", map[string]any{"fileName": "x"}},
		{"DELETE", "/files/%20", "file.invalid_id", nil},
		{"GET", "/files/%20/extract", "file.invalid_id", nil},
		{"PUT", "/conversations/" + id + "/messages/" + str(a, "publicID") + "/ui-state", "", nil},
	}
	for _, c := range cases {
		if c.code == "" {
			continue
		}
		w := e.call(c.m, c.p, e.u1, c.body)
		testkit.Expect(t, w, 400, c.code)
	}
}

func TestUIStateHintsAreAdvisory(t *testing.T) {
	e := newEnv(t)
	id := e.newConv(e.u1, "t", "gpt-4o")
	_, a1 := pair(e.send(e.u1, id, "q1", nil))
	other := e.newConv(e.u1, "o", "gpt-4o")
	_, ao := pair(e.send(e.u1, other, "q", nil))
	big := map[string]any{"v": strings.Repeat("x", 5000)}
	e.send(e.u1, id, "q2", map[string]any{
		"uiComponentIDs": []uint{1, 2}, "uiBlockVersion": 2,
		"uiStates": []map[string]any{
			{"messagePublicID": str(a1, "publicID"), "blockID": "good", "title": "T", "state": map[string]any{"a": 1}},
			{"messagePublicID": str(ao, "publicID"), "blockID": "foreign", "state": map[string]any{"a": 1}},
			{"messagePublicID": str(a1, "publicID"), "blockID": "huge", "state": big},
			{"messagePublicID": str(a1, "publicID"), "blockID": "empty", "state": map[string]any{}},
			{"messagePublicID": "nonexistent", "blockID": "missing", "state": map[string]any{"a": 1}},
		},
	})
	reqs := chatRequests(e)
	last := reqs[len(reqs)-1]
	if len(last.UIStates) != 1 || last.UIStates[0].BlockID != "good" || last.UIStates[0].Title != "T" {
		t.Fatalf("only valid on-branch hints survive: %+v", last.UIStates)
	}
	if last.UIBlockVersion != 2 || len(last.UIComponentIDs) != 2 {
		t.Fatalf("ui settings: %+v", last)
	}
	// count / length violations fail the request
	e.expectErr("POST", "/conversations/"+id+"/messages", e.u1, sendBody("x", map[string]any{
		"uiStates": []map[string]any{{"messagePublicID": strings.Repeat("m", 65), "blockID": "b"}}}), 400, "request.invalid_body")
	// absent uiBlockVersion means static components only
	e.send(e.u1, id, "q3", map[string]any{"uiComponentIDs": []uint{1}})
	reqs = chatRequests(e)
	if reqs[len(reqs)-1].UIBlockVersion != 1 {
		t.Fatalf("default version: %d", reqs[len(reqs)-1].UIBlockVersion)
	}
	// the global switch removes components and hints
	e.set.mutate(func(r *RuntimeSettings) { r.UIComponentsEnabled = false })
	e.send(e.u1, id, "q4", map[string]any{"uiComponentIDs": []uint{1}, "uiStates": []map[string]any{{"messagePublicID": str(a1, "publicID"), "blockID": "good", "state": map[string]any{"a": 1}}}})
	reqs = chatRequests(e)
	if l := reqs[len(reqs)-1]; len(l.UIComponentIDs) != 0 || len(l.UIStates) != 0 {
		t.Fatalf("disabled components: %+v", l)
	}
	s := e.ok("POST", "/conversations/"+id+"/share", e.u1, nil)
	if pub := e.ok("GET", "/shared-conversations/"+str(s, "shareID"), nil, nil); pub["uiComponentsEnabled"] != false {
		t.Fatalf("public flag mirrors the switch: %v", pub["uiComponentsEnabled"])
	}
}

func TestToolSelectionDeduplicated(t *testing.T) {
	e := newEnv(t)
	e.set.mutate(func(r *RuntimeSettings) { r.RAGAvailable = true })
	id := e.newConv(e.u1, "t", "gpt-4o")
	e.send(e.u1, id, "x", map[string]any{"selectedToolIDs": []uint{3, 1, 3}, "skillIDs": []uint{0, 2, 2}, "knowledgeBaseIDs": []string{" k ", "k"}})
	r := chatRequests(e)[0]
	if fmt.Sprint(r.ToolIDs) != "[3 1]" || fmt.Sprint(r.SkillIDs) != "[2]" || fmt.Sprint(r.KnowledgeBaseIDs) != "[k]" {
		t.Fatalf("selections: %+v", r)
	}
}
