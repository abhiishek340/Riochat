package conversation

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/testkit"
)

type adminSeed struct {
	t *testing.T
	e *tenv
}

func (s adminSeed) conversation(userID uint, title string) Conversation {
	s.t.Helper()
	c := Conversation{UserID: userID, PublicID: fmt.Sprintf("c%031d", time.Now().UnixNano()%1e9+int64(len(title))), Title: title,
		SessionKey: title + fmt.Sprint(userID, time.Now().UnixNano()), Status: statusActive}
	if err := s.e.DB.Create(&c).Error; err != nil {
		s.t.Fatal(err)
	}
	return c
}

func (s adminSeed) run(userID, convID uint, runID, requestID string) Run {
	s.t.Helper()
	r := Run{RunID: runID, RequestID: requestID, UserID: userID, ConversationID: convID, TaskType: "chat", ProviderProtocol: "openai_chat_completions",
		UpstreamName: "up-a", PlatformModelName: "plat", RoutedBindingCode: "bind-1", UpstreamModelName: "up-model", Status: runSuccess,
		StartedAt: time.Now()}
	if err := s.e.DB.Create(&r).Error; err != nil {
		s.t.Fatal(err)
	}
	return r
}

func (s adminSeed) message(c Conversation, runID, role, status, content string, parent *uint) Message {
	s.t.Helper()
	m := Message{ConversationID: c.ID, UserID: c.UserID, PublicID: fmt.Sprintf("m%031d", time.Now().UnixNano()%1e12), ParentMessageID: parent,
		RunID: runID, Role: role, ContentType: "markdown", Content: content, Status: status, BranchReason: branchDefault}
	if err := s.e.DB.Create(&m).Error; err != nil {
		s.t.Fatal(err)
	}
	time.Sleep(time.Microsecond)
	return m
}

func (s adminSeed) event(runID, scope, id string, mod func(*RunEvent)) RunEvent {
	s.t.Helper()
	started := s.e.Clock.Now()
	ev := RunEvent{RunID: runID, EventScope: scope, EventID: id, EventType: "step", Phase: "process", Stage: "think", Status: "completed",
		Title: "Title " + id, Summary: "sum " + id, StartedAt: &started}
	if mod != nil {
		mod(&ev)
	}
	if err := s.e.DB.Create(&ev).Error; err != nil {
		s.t.Fatal(err)
	}
	return ev
}

func (s adminSeed) users(names ...string) {
	s.t.Helper()
	if err := s.e.DB.Exec(`CREATE TABLE IF NOT EXISTS identity_users (id integer primary key, username text, display_name text, deleted_at datetime)`).Error; err != nil {
		s.t.Fatal(err)
	}
	for i, n := range names {
		parts := strings.SplitN(n, "|", 2)
		s.e.DB.Exec(`INSERT INTO identity_users (id, username, display_name) VALUES (?, ?, ?)`, i+1, parts[0], parts[1])
	}
}

type eventPage struct {
	Total   int64        `json:"total"`
	Results []AdminEvent `json:"results"`
}

func (e *tenv) events(q string) eventPage {
	e.t.Helper()
	w := e.call("GET", "/admin/conversation-events"+q, e.admin, nil)
	testkit.Expect(e.t, w, 200, "")
	var p eventPage
	testkit.Decode(e.t, w, &p)
	return p
}

func TestAdminEventListFiltersAndSorting(t *testing.T) {
	e := newEnv(t)
	s := adminSeed{t, e}
	s.users("alice|Alice A", "bob|")
	c1 := s.conversation(1, "one")
	c2 := s.conversation(2, "two")
	s.run(1, c1.ID, "run-a", "req-aaa")
	s.run(2, c2.ID, "run-b", "req-bbb")
	am := s.message(c1, "run-a", roleAssistant, msgSuccess, "hi", nil)

	e.Clock.Advance(time.Minute)
	s.event("run-a", scopeTraceEvent, "e1", func(r *RunEvent) { r.Seq = 2; r.LatencyMS = 50; r.PayloadJSON = `{"a":1}` })
	e.Clock.Advance(time.Minute)
	s.event("run-a", scopeToolCall, "call-1", func(r *RunEvent) {
		r.Seq = 1
		r.LatencyMS = 900
		r.ToolName = "WebSearch"
		r.Status = "error"
		r.InputJSON = `{"q":"x"}`
		r.ContentMarkdown = "body"
	})
	e.Clock.Advance(time.Minute)
	s.event("run-b", scopeTraceBlock, "blk", func(r *RunEvent) { r.Seq = 1; r.LatencyMS = 5; r.EventType = "other" })

	all := e.events("")
	if all.Total != 3 || all.Results[0].EventID != "blk" || all.Results[2].EventID != "e1" {
		t.Fatalf("newest first: %+v", all)
	}
	top := all.Results[2]
	if top.UserID != 1 || top.ConversationID != c1.ID || top.MessageID != am.ID || top.Username != "alice" || top.UserDisplayName != "Alice A" ||
		top.UserLabel != "Alice A" || top.ProviderProtocol != "openai_chat_completions" || top.UpstreamName != "up-a" ||
		top.PlatformModelName != "plat" || top.RoutedBindingCode != "bind-1" || top.UpstreamModelName != "up-model" {
		t.Fatalf("run facts and labels: %+v", top)
	}
	if all.Results[0].UserLabel != "bob" || all.Results[0].MessageID != 0 {
		t.Fatalf("label falls back to the username: %+v", all.Results[0])
	}
	if top.PayloadSizeBytes != 7 || top.PayloadOmitted || top.PayloadJSON != "" || top.ContentMarkdown != "" {
		t.Fatalf("list items blank the heavy bodies: %+v", top)
	}
	if all.Results[1].InputJSON != "" || all.Results[1].InputSizeBytes != 0 || all.Results[1].InputOmitted {
		t.Fatalf("input pair is 0/false in lists: %+v", all.Results[1])
	}

	check := func(q string, want ...string) {
		t.Helper()
		var got []string
		for _, ev := range e.events(q).Results {
			got = append(got, ev.EventID)
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("%s: got %v want %v", q, got, want)
		}
	}
	check("?sort=created_asc", "e1", "call-1", "blk")
	check("?sort=latency_desc", "call-1", "e1", "blk")
	check("?sort=seq_asc", "call-1", "e1", "blk")
	check("?sort=whatever", "blk", "call-1", "e1")
	check("?event_scope=tool_call", "call-1")
	check("?event_type=other", "blk")
	check("?status=error", "call-1")
	check("?user_id=2", "blk")
	check(fmt.Sprintf("?conversation_id=%d", c1.ID), "call-1", "e1")
	check("?query=WEBSEARCH", "call-1")
	check("?query=req-aaa", "call-1", "e1")
	check("?query=run-b", "blk")
	check("?query=call_1", "call-1")
	check("?page_size=1&page=2", "call-1")
	from := e.Clock.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	to := e.Clock.Now().Add(-time.Minute).UTC().Format(time.RFC3339)
	check("?created_from="+from, "blk", "call-1")
	check("?created_to="+to, "call-1", "e1")
	check("?created_from="+from+"&created_to="+to, "call-1")
}

func TestAdminEventQueryValidation(t *testing.T) {
	e := newEnv(t)
	for _, q := range []string{"user_id=0", "user_id=abc", "conversation_id=-1", "created_from=yesterday", "created_to=2026-01-01"} {
		w := e.call("GET", "/admin/conversation-events?"+q, e.admin, nil)
		testkit.Expect(t, w, 400, "request.invalid_query")
		name := strings.SplitN(q, "=", 2)[0]
		if !strings.Contains(w.Body.String(), "invalid "+name) {
			t.Fatalf("%s: %s", q, w.Body.String())
		}
	}
	e.expectErr("GET", "/admin/conversation-events", e.u1, nil, 403, "auth.admin_required")
	if p := e.events(""); p.Total != 0 || p.Results == nil {
		t.Fatalf("empty page: %+v", p)
	}
}

func TestAdminEventDetailAndOmission(t *testing.T) {
	e := newEnv(t)
	s := adminSeed{t, e}
	c := s.conversation(1, "d")
	s.run(1, c.ID, "run-d", "req-d")
	huge := strings.Repeat("x", 2<<20)
	exact := strings.Repeat("y", 1<<20)
	ev := s.event("run-d", scopeToolCall, "big", func(r *RunEvent) {
		r.InputJSON, r.OutputJSON, r.PayloadJSON, r.ContentMarkdown = huge, exact, "{}", "# md"
		r.ErrorJSON = `{"e":1}`
	})

	w := e.call("GET", fmt.Sprintf("/admin/conversation-events/%d", ev.ID), e.admin, nil)
	testkit.Expect(t, w, 200, "")
	var got AdminEvent
	testkit.Decode(t, w, &got)
	if got.InputJSON != "" || !got.InputOmitted || got.InputSizeBytes != int64(len(huge)) {
		t.Fatalf("2 MiB input is omitted but its size is reported: omitted=%v size=%d", got.InputOmitted, got.InputSizeBytes)
	}
	if got.OutputJSON != exact || got.OutputOmitted || got.OutputSizeBytes != 1<<20 {
		t.Fatalf("exactly 1 MiB is returned in full: omitted=%v size=%d", got.OutputOmitted, got.OutputSizeBytes)
	}
	if got.ContentMarkdown != "# md" || got.PayloadJSON != "{}" || got.ErrorJSON != `{"e":1}` || got.RunID != "run-d" || got.UserID != 1 {
		t.Fatalf("detail: %+v", got)
	}
	// The list reports the payload pair only.
	big := s.event("run-d", scopeTraceEvent, "bigpayload", func(r *RunEvent) { r.PayloadJSON = huge })
	list := e.events("?query=bigpayload")
	if len(list.Results) != 1 || list.Results[0].ID != big.ID || !list.Results[0].PayloadOmitted || list.Results[0].PayloadSizeBytes != int64(len(huge)) {
		t.Fatalf("list payload pair: %+v", list.Results)
	}
	e.expectErr("GET", "/admin/conversation-events/abc", e.admin, nil, 400, "conversation_event.invalid_id")
	e.expectErr("GET", "/admin/conversation-events/0", e.admin, nil, 400, "conversation_event.invalid_id")
	e.expectErr("GET", "/admin/conversation-events/999999", e.admin, nil, 404, "conversation_event.not_found")
}

func TestTraceRowsRecordLatency(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(1500 * time.Millisecond)
	rows := traceRows("r", &TraceData{Events: []TraceEvent{{EventID: "a", StartedAt: start, EndedAt: &end}, {EventID: "b", StartedAt: start}}}, start)
	if rows[0].LatencyMS != 1500 || rows[1].LatencyMS != 0 {
		t.Fatalf("latency: %d %d", rows[0].LatencyMS, rows[1].LatencyMS)
	}
}

func exportLines(t *testing.T, w *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var out []map[string]any
	sc := bufio.NewScanner(w.Body)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		if strings.TrimSpace(sc.Text()) == "" {
			t.Fatal("blank line in the export")
		}
		var m map[string]any
		if err := json.Unmarshal(sc.Bytes(), &m); err != nil {
			t.Fatalf("line %q: %v", sc.Text(), err)
		}
		out = append(out, m)
	}
	return out
}

func TestAdminExportAllConversations(t *testing.T) {
	e := newEnv(t)
	s := adminSeed{t, e}
	for i := 0; i < 120; i++ {
		c := s.conversation(uint(i%3+1), fmt.Sprintf("conv-%03d", i))
		s.message(c, "", roleUser, msgSuccess, "hello", nil)
	}
	w := e.call("GET", "/admin/conversations/export", e.admin, nil)
	testkit.Expect(t, w, 200, "")
	h := w.Header()
	if h.Get("Content-Type") != "application/x-ndjson" || h.Get("Cache-Control") != "no-store" ||
		!strings.HasPrefix(h.Get("Content-Disposition"), `attachment; filename="conversations-export-`) || !strings.HasSuffix(h.Get("Content-Disposition"), `.jsonl"`) {
		t.Fatalf("headers: %v", h)
	}
	lines := exportLines(t, w)
	if len(lines) != 121 {
		t.Fatalf("lines: %d", len(lines))
	}
	last := lines[120]
	if last["_type"] != "export_manifest" || last["complete"] != true || last["exported"] != float64(120) || last["failed"] != float64(0) {
		t.Fatalf("manifest: %v", last)
	}
	if _, has := last["failedIDs"]; has {
		t.Fatal("failedIDs omitted when none")
	}
	for i, l := range lines[:120] {
		if title := l["conversation"].(map[string]any)["title"]; title != fmt.Sprintf("conv-%03d", i) {
			t.Fatalf("ordered by conversation id: line %d is %v", i, title)
		}
		compat := l["compatibility"].(map[string]any)
		if l["version"] != float64(1) || l["exportScope"] != "full" || compat["format"] != "riochat.conversation.export" ||
			!strings.Contains(compat["notes"].(string), "redacts blocked content") {
			t.Fatalf("line: %v", l)
		}
	}
	if !e.audit.hasAction("admin_export_conversations") {
		t.Fatal("audit entry written")
	}
	if got := e.audit.find("admin_export_conversations"); got.Resource != "conversation" || got.UserID != 99 {
		t.Fatalf("audit: %+v", got)
	}
	e.expectErr("GET", "/admin/conversations/export", e.u1, nil, 403, "auth.admin_required")
}

func (a *fakeAudit) hasAction(action string) bool { return a.find(action) != nil }

func TestAdminExportEmptySystem(t *testing.T) {
	e := newEnv(t)
	lines := exportLines(t, e.call("GET", "/admin/conversations/export", e.admin, nil))
	if len(lines) != 1 || lines[0]["_type"] != "export_manifest" || lines[0]["exported"] != float64(0) || lines[0]["complete"] != true {
		t.Fatalf("only the manifest: %v", lines)
	}
}

func TestAdminExportRedactsBlockedMessages(t *testing.T) {
	e := newEnv(t)
	s := adminSeed{t, e}
	c := s.conversation(1, "blocked")
	r := s.run(1, c.ID, "run-x", "req-x")
	e.DB.Model(&Run{}).Where("id = ?", r.ID).Update("moderation_event_id", "evt_run")
	um := s.message(c, "run-x", roleUser, "BLOCKED", "secret prompt", nil)
	e.DB.Model(&Message{}).Where("id = ?", um.ID).Update("moderation_event_id", "evt_msg")
	e.DB.Create(&Attachment{ConversationID: c.ID, MessageID: um.ID, UserID: 1, FileID: "f1", Kind: "image", FileName: "a.png", Status: "active"})
	am := s.message(c, "run-x", roleAssistant, "blocked", "bad answer", &um.ID)
	e.DB.Model(&Message{}).Where("id = ?", am.ID).Update("error_message", "")
	ok := s.message(c, "run-x", roleAssistant, msgSuccess, "fine", &um.ID)
	_ = ok
	s.event("run-x", scopeTraceBlock, "process", nil)

	lines := exportLines(t, e.call("GET", "/admin/conversations/export", e.admin, nil))
	doc := lines[0]
	var userMsg, blockedAssistant, fine map[string]any
	for _, m := range doc["messages"].([]any) {
		mm := m.(map[string]any)
		switch {
		case mm["role"] == "user":
			userMsg = mm
		case mm["status"] == "blocked":
			blockedAssistant = mm
		default:
			fine = mm
		}
	}
	if userMsg["content"] != "[blocked by content moderation; event evt_msg]" || userMsg["attachments"] != "[]" {
		t.Fatalf("user message: %v", userMsg)
	}
	if blockedAssistant["content"] != "" || blockedAssistant["attachments"] != "[]" || blockedAssistant["processTrace"] != nil ||
		blockedAssistant["errorMessage"] != "[blocked by content moderation; event evt_run]" {
		t.Fatalf("assistant message: %v", blockedAssistant)
	}
	if fine["content"] != "fine" {
		t.Fatalf("other messages are untouched: %v", fine)
	}
	if runs := doc["runs"].([]any); len(runs) != 1 || doc["totalRuns"] != float64(1) || doc["totalMessages"] != float64(3) {
		t.Fatalf("runs: %v", doc["runs"])
	}
}

func TestRedactBlockedPlaceholderWithoutEvent(t *testing.T) {
	msgs := []MessageDTO{{Role: roleUser, Status: "Blocked", Content: "x", Attachments: "[1]"}, {Role: roleAssistant, Status: msgBlocked, ErrorMessage: "kept"}}
	redactBlocked(msgs, nil)
	if msgs[0].Content != "[blocked by content moderation]" || msgs[0].Attachments != "[]" || msgs[1].ErrorMessage != "kept" {
		t.Fatalf("%+v", msgs)
	}
}

func TestAdminExportSkipsUnreadableConversation(t *testing.T) {
	e := newEnv(t)
	s := adminSeed{t, e}
	good := s.conversation(1, "good")
	s.message(good, "", roleUser, msgSuccess, "hi", nil)
	// An orphan conversation (no owner) is still exported, with no runs.
	orphan := s.conversation(0, "orphan")
	s.message(orphan, "", roleUser, msgSuccess, "hi", nil)
	lines := exportLines(t, e.call("GET", "/admin/conversations/export", e.admin, nil))
	if len(lines) != 3 || lines[1]["runs"] == nil || len(lines[1]["runs"].([]any)) != 0 {
		t.Fatalf("ownerless conversation has an empty runs array: %v", lines)
	}
}

func TestAdminConversationRoutesWorkWithoutMultiUser(t *testing.T) {
	e := newEnv(t, localOpt())
	testkit.Expect(t, e.call("GET", "/admin/conversation-events", e.admin, nil), 200, "")
	testkit.Expect(t, e.call("GET", "/admin/conversations/export", e.admin, nil), 200, "")
}
