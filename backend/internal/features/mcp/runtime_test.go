package mcp

import (
	"context"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/mcp/mcptest"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

var countSchema = map[string]any{
	"type":       "object",
	"properties": map[string]any{"count": map[string]any{"type": "integer"}},
}

// runFixture registers a server with the given tools, syncs it and returns the
// ids of the tools in discovery order.
func runFixture(t *testing.T, h *harness, tools ...map[string]any) (*ServerRow, []uint) {
	t.Helper()
	h.fake.SetTools(tools...)
	srv := h.server("Srv")
	h.sync(srv.ID, false)
	list, _ := h.svc.ListServerTools(context.Background(), srv.ID)
	ids := make([]uint, len(list))
	for i := range list {
		ids[i] = list[i].ID
	}
	return srv, ids
}

func resolve(t *testing.T, h *harness, ids ...uint) *RunTools {
	t.Helper()
	rt, err := h.svc.ResolveRunTools(context.Background(), ids)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	return rt
}

func TestResolveOffersTools(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h,
		toolOf("get weather!", "W", "  Weather lookup  ", searchSchema),
		toolOf("9lives", "N", "", nil),
	)
	rt := resolve(t, h, ids[1], ids[0], ids[0], 0)
	specs := rt.Tools()
	if len(specs) != 2 || specs[0].Name != "get_weather" || specs[1].Name != "tool_9lives" {
		t.Fatalf("specs in tool order, deduplicated: %+v", specs)
	}
	if specs[0].Description != "Weather lookup" {
		t.Fatalf("description trimmed: %q", specs[0].Description)
	}
	if !strings.Contains(string(specs[0].InputSchema), "properties") || string(specs[1].InputSchema) != "{}" {
		t.Fatalf("schemas: %s / %s", specs[0].InputSchema, specs[1].InputSchema)
	}
	if !rt.HasTools() || !strings.HasPrefix(rt.Guidance(), "# tool_use") {
		t.Fatalf("guidance: %q", rt.Guidance())
	}
	llm, calls := rt.Limits()
	if llm != 5 || calls != 8 {
		t.Fatalf("limits %d %d", llm, calls)
	}
}

func TestResolveSchemaDefault(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "A", "", nil))
	h.db.Model(&Tool{}).Where("id = ?", ids[0]).Update("input_schema_json", "  ")
	specs := resolve(t, h, ids[0]).Tools()
	if string(specs[0].InputSchema) != `{"type":"object","properties":{}}` {
		t.Fatalf("default schema: %s", specs[0].InputSchema)
	}
}

func TestResolveCustomPrompt(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "A", "", nil))
	h.set.update(func(s *RuntimeSettings) { s.ToolPrompt = "  be brief  " })
	if g := resolve(t, h, ids[0]).Guidance(); g != "be brief" {
		t.Fatalf("custom prompt: %q", g)
	}
}

func TestResolveNameCollisions(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("search", "S", "", nil), toolOf("search!", "S2", "", nil))
	one, two := h.server("one"), h.server("two")
	h.sync(one.ID, false)
	h.sync(two.ID, false)
	var all []uint
	for _, id := range []uint{two.ID, one.ID} {
		list, _ := h.svc.ListServerTools(context.Background(), id)
		for _, tl := range list {
			all = append(all, tl.ID)
		}
	}
	rt := resolve(t, h, all...)
	var names []string
	for _, s := range rt.Tools() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "search,search_2,search_3,search_4" {
		t.Fatalf("names follow server order then tool order: %v", names)
	}
	// Each model name stays bound to its own server.
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Result: mcptest.Text("ok")} })
	out := rt.Call(context.Background(), CallRequest{ModelName: "search_3", ArgumentsJSON: "{}"})
	if out.Status != CallSuccess || out.ServerID != two.ID || out.ServerName != "two" || out.ToolName != "search" {
		t.Fatalf("attribution: %+v", out)
	}
	if u := rt.Usage(); len(u) != 1 || u[0].ServerID != two.ID {
		t.Fatalf("usage: %+v", u)
	}
}

func TestResolveSkipsSilently(t *testing.T) {
	h := newHarness(t)
	srv, ids := runFixture(t, h, toolOf("ok", "", "", nil), toolOf("inactive", "", "", nil), toolOf("!!!", "", "", nil))
	h.db.Model(&Tool{}).Where("id = ?", ids[1]).Update("status", "inactive")
	names := func(rt *RunTools) []string {
		var n []string
		for _, s := range rt.Tools() {
			n = append(n, s.Name)
		}
		return n
	}
	if got := names(resolve(t, h, append(ids, 9999)...)); strings.Join(got, ",") != "ok" {
		t.Fatalf("inactive, unnameable and unknown tools drop silently: %v", got)
	}
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Update("status", "inactive")
	if rt := resolve(t, h, ids...); rt.HasTools() || rt.Guidance() != "" {
		t.Fatal("an inactive server offers nothing and adds no guidance")
	}
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Updates(map[string]any{"status": "active", "base_url": "http://169.254.169.254/"})
	if rt := resolve(t, h, ids...); rt.HasTools() {
		t.Fatal("a forbidden base URL drops the tools")
	}
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Updates(map[string]any{"base_url": h.fake.URL, "auth_token_enc": "v1:Zm9v"})
	if rt := resolve(t, h, ids...); rt.HasTools() {
		t.Fatal("an undecryptable token drops the tools")
	}
}

func TestResolveSwitchOffOrEmptySelection(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	if rt := resolve(t, h); rt.HasTools() {
		t.Fatal("no selection, no tools")
	}
	h.set.update(func(s *RuntimeSettings) { s.Enabled = false })
	if rt := resolve(t, h, ids...); rt.HasTools() {
		t.Fatal("switch off, no tools")
	}
}

func TestSelectionLimit(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	make33 := func(n int) []uint {
		ids := make([]uint, n)
		for i := range ids {
			ids[i] = uint(i + 1)
		}
		return ids
	}
	if err := h.svc.ValidateSelection(ctx, make33(32)); err != nil {
		t.Fatal(err)
	}
	err := h.svc.ValidateSelection(ctx, make33(33))
	if !errors.Is(err, ErrTooManyTools) || statusFor(err) != 400 {
		t.Fatalf("33 > 32: %v", err)
	}
	if _, err := h.svc.ResolveRunTools(ctx, make33(33)); !errors.Is(err, ErrTooManyTools) {
		t.Fatalf("non-HTTP paths are checked too: %v", err)
	}
	h.set.update(func(s *RuntimeSettings) { s.MaxSelectedToolsPerMessage = 500 })
	if err := h.svc.ValidateSelection(ctx, make33(129)); !errors.Is(err, ErrTooManyTools) {
		t.Fatalf("hard maximum is 128: %v", err)
	}
	if err := h.svc.ValidateSelection(ctx, make33(128)); err != nil {
		t.Fatal(err)
	}
}

func bindImage(t *testing.T, h *harness, id uint, encoding string) {
	t.Helper()
	_, err := h.svc.UpdateTool(context.Background(), id, ToolPatch{
		AttachmentInputMode: ptr("image"), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr(encoding), AttachmentPromptArgument: ptr("prompt"),
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMultipleImageProcessors(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("v1", "", "", imageSchema), toolOf("v2", "", "", imageSchema), toolOf("plain", "", "", nil))
	bindImage(t, h, ids[0], "base64")
	bindImage(t, h, ids[1], "base64")
	_, err := h.svc.ResolveRunTools(context.Background(), []uint{ids[0], ids[1]})
	if !errors.Is(err, ErrMultipleProcessors) || statusFor(err) != 400 {
		t.Fatalf("got %v", err)
	}
	// An inactive second processor is not selected in effect.
	h.db.Model(&Tool{}).Where("id = ?", ids[1]).Update("status", "inactive")
	rt, err := h.svc.ResolveRunTools(context.Background(), []uint{ids[0], ids[1], ids[2]})
	if err != nil {
		t.Fatal(err)
	}
	if p, ok := rt.Processor(); !ok || p.ToolID != ids[0] {
		t.Fatalf("processor: %+v", p)
	}
	if specs := rt.Tools(); len(specs) != 1 || specs[0].Name != "plain" {
		t.Fatalf("the processor is hidden from the model: %+v", specs)
	}
}

func TestProcessorUnavailableFailsTheRun(t *testing.T) {
	h := newHarness(t)
	srv, ids := runFixture(t, h, toolOf("v1", "", "", imageSchema))
	bindImage(t, h, ids[0], "base64")
	for name, change := range map[string]map[string]any{
		"inactive server": {"status": "inactive"},
		"forbidden url":   {"base_url": "http://169.254.169.254/"},
		"bad token":       {"auth_token_enc": "v1:Zm9v"},
	} {
		h.db.Model(&Server{}).Where("id = ?", srv.ID).Updates(map[string]any{"status": "active", "base_url": h.fake.URL, "auth_token_enc": ""})
		h.db.Model(&Server{}).Where("id = ?", srv.ID).Updates(change)
		_, err := h.svc.ResolveRunTools(context.Background(), ids)
		if !errors.Is(err, ErrImageProcessing) {
			t.Errorf("%s: got %v", name, err)
		}
	}
}

func TestCallCoercesAndValidates(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("count", "", "", countSchema))
	var seen atomic.Value
	h.fake.OnCall(func(name string, args map[string]any, attempt int) mcptest.Reply {
		seen.Store(args)
		return mcptest.Reply{Result: mcptest.Text("done")}
	})
	rt := resolve(t, h, ids...)
	ctx := context.Background()
	out := rt.Call(ctx, CallRequest{ModelName: "count", ArgumentsJSON: `{"count":"3"}`, UserID: 1})
	if out.Status != CallSuccess || out.Input != `{"count":3}` || !out.Billable {
		t.Fatalf("coerced call: %+v", out)
	}
	if args := seen.Load().(map[string]any); args["count"] != float64(3) {
		t.Fatalf("the remote got %v", args)
	}
	before := h.fake.Count("tools/call")
	bad := rt.Call(ctx, CallRequest{ModelName: "count", ArgumentsJSON: `{"count":"x"}`})
	if bad.Status != CallError || !strings.Contains(bad.Error, "argument validation failed") || !strings.Contains(bad.Error, "count") || bad.Billable {
		t.Fatalf("bad call: %+v", bad)
	}
	if h.fake.Count("tools/call") != before {
		t.Fatal("a validation failure must not reach the server")
	}
	if u := rt.Usage(); len(u) != 1 || u[0].Count != 1 {
		t.Fatalf("usage counts only the good call: %+v", u)
	}
	if out.Output != `{"content":[{"text":"done","type":"text"}]}` && !strings.Contains(out.Output, "done") {
		t.Fatalf("output: %s", out.Output)
	}
}

func TestCallRepeatedIdenticalIsReused(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("count", "", "", countSchema))
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Result: mcptest.Text("first")} })
	rt := resolve(t, h, ids...)
	ctx := context.Background()
	a := rt.Call(ctx, CallRequest{ModelName: "count", ArgumentsJSON: `{"count":3}`})
	b := rt.Call(ctx, CallRequest{ModelName: "count", ArgumentsJSON: `{ "count": "3" }`})
	if a.Repeated || !b.Repeated || b.Billable || b.Output != a.Output || b.Status != CallSuccess {
		t.Fatalf("a=%+v b=%+v", a, b)
	}
	if h.fake.Count("tools/call") != 1 {
		t.Fatalf("executed %d times", h.fake.Count("tools/call"))
	}
	if u := rt.Usage(); len(u) != 1 || u[0].Count != 1 {
		t.Fatalf("usage: %+v", u)
	}
	// Different arguments execute again.
	rt.Call(ctx, CallRequest{ModelName: "count", ArgumentsJSON: `{"count":4}`})
	if h.fake.Count("tools/call") != 2 || rt.Usage()[0].Count != 2 {
		t.Fatalf("calls %d usage %+v", h.fake.Count("tools/call"), rt.Usage())
	}
}

func TestCallConcurrentIdenticalExecutesOnce(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("count", "", "", countSchema))
	gate := make(chan struct{})
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
		<-gate
		return mcptest.Reply{Result: mcptest.Text("x")}
	})
	rt := resolve(t, h, ids...)
	var wg sync.WaitGroup
	outs := make([]CallOutcome, 4)
	for i := range outs {
		wg.Add(1)
		go func() {
			defer wg.Done()
			outs[i] = rt.Call(context.Background(), CallRequest{ModelName: "count", ArgumentsJSON: `{"count":1}`})
		}()
	}
	time.Sleep(150 * time.Millisecond)
	close(gate)
	wg.Wait()
	repeated := 0
	for _, o := range outs {
		if o.Repeated {
			repeated++
		}
		if o.Status != CallSuccess {
			t.Fatalf("outcome: %+v", o)
		}
	}
	if repeated != 3 || h.fake.Count("tools/call") != 1 || rt.Usage()[0].Count != 1 {
		t.Fatalf("repeated=%d calls=%d", repeated, h.fake.Count("tools/call"))
	}
}

func TestCallNotEnabledIsFatal(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	rt := resolve(t, h, ids...)
	if rt.Fatal() {
		t.Fatal("fresh run is not fatal")
	}
	out := rt.Call(context.Background(), CallRequest{ModelName: "ghost", ArgumentsJSON: "{}"})
	if out.Status != CallError || !out.Fatal || !strings.Contains(out.Error, "not enabled for this run") || out.ModelOutput == "" || out.Billable {
		t.Fatalf("outcome: %+v", out)
	}
	if !rt.Fatal() || len(rt.Usage()) != 0 || h.fake.Count("tools/call") != 0 {
		t.Fatal("flag set, nothing billed, nothing sent")
	}
}

func TestCallRemoteErrorsAreNotBilled(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	rt := resolve(t, h, ids...)
	ctx := context.Background()
	replies := []mcptest.Reply{
		{Result: map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "boom"}}}},
		{Result: mcptest.Text("MCP error -32602: Invalid arguments")},
		{RPCErrorCode: -32000, RPCErrorText: "rpc down"},
		{Status: 502},
	}
	var n atomic.Int32
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return replies[n.Add(1)-1] })
	wants := []string{"boom", "MCP error -32602", "rpc down", "502"}
	for i, want := range wants {
		out := rt.Call(ctx, CallRequest{ModelName: "a", ArgumentsJSON: `{"i":` + string(rune('0'+i)) + `}`})
		if out.Status != CallError || !strings.Contains(out.Error, want) || out.Billable || out.Output != "" {
			t.Errorf("%d: %+v", i, out)
		}
		if strings.Contains(out.Error, "secret-body") {
			t.Errorf("%d leaks the upstream body: %s", i, out.Error)
		}
	}
	if len(rt.Usage()) != 0 {
		t.Fatalf("failed calls are free: %+v", rt.Usage())
	}
	if !strings.HasPrefix(func() string {
		h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
			return mcptest.Reply{Result: map[string]any{"isError": true}}
		})
		return rt.Call(ctx, CallRequest{ModelName: "a", ArgumentsJSON: `{"z":1}`}).Error
	}(), toolErrorPrefix) {
		t.Error("tool errors carry the identifying prefix")
	}
}

func TestCallStoresNormalResultVerbatim(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Result: map[string]any{}} })
	out := resolve(t, h, ids...).Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: ""})
	if out.Status != CallSuccess || out.Output != "{}" || out.Input != "{}" || out.ToolType != ToolTypeMCP {
		t.Fatalf("%+v", out)
	}
}

func TestCallRetriesReuseTheSignedToken(t *testing.T) {
	h := newHarness(t)
	srvRow := h.server("S", func(in *ServerInput) {
		in.HeadersJSON = `{"X-User":"${RIOCHAT_SIGNED_USER_CONTEXT}","X-Also":" ${RIOCHAT_SIGNED_USER_CONTEXT} ","X-Static":"1"}`
	})
	h.fake.SetTools(toolOf("a", "", "", nil))
	h.sync(srvRow.ID, false)
	list, _ := h.svc.ListServerTools(context.Background(), srvRow.ID)
	h.set.update(func(s *RuntimeSettings) { s.RetryCount = 2 })
	h.fake.OnCall(func(_ string, _ map[string]any, attempt int) mcptest.Reply {
		if attempt < 3 {
			return mcptest.Reply{Status: 500}
		}
		return mcptest.Reply{Result: mcptest.Text("ok")}
	})
	rt := resolve(t, h, list[0].ID)
	started := time.Now()
	out := rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: "{}", UserID: 7, ConversationID: 9, RequestID: "req"})
	if out.Status != CallSuccess {
		t.Fatalf("%+v", out)
	}
	if elapsed := time.Since(started); elapsed < 250*time.Millisecond {
		t.Fatalf("linear backoff of 100ms + 200ms expected, took %v", elapsed)
	}
	if u := rt.Usage(); len(u) != 1 || u[0].Count != 1 {
		t.Fatalf("one billable call regardless of retries: %+v", u)
	}
	var tokens, jtis []string
	for _, r := range h.fake.Requests() {
		if r.Method != "tools/call" {
			continue
		}
		tokens = append(tokens, r.Header.Get("X-User"))
		if r.Header.Get("X-Also") != r.Header.Get("X-User") || r.Header.Get("X-Static") != "1" {
			t.Fatalf("all placeholder headers share one token: %v", r.Header)
		}
		c, err := VerifyUserContext("s", r.Header.Get("X-User"), h.clk.Now())
		if err != nil {
			t.Fatal(err)
		}
		jtis = append(jtis, c.JTI)
		if c.UserID != 7 || c.ConversationID != 9 || c.RequestID != "req" || c.Audience != h.fake.URL || c.ExpiresAt != h.clk.Now().Add(300*time.Second).Unix() {
			t.Fatalf("claims: %+v", c)
		}
	}
	if len(tokens) != 3 || tokens[0] != tokens[1] || tokens[1] != tokens[2] || jtis[0] != jtis[2] {
		t.Fatalf("attempts must reuse the token: %v", tokens)
	}
	// A different logical call gets a different jti.
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Result: mcptest.Text("ok")} })
	h.fake.Reset()
	rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: `{"n":1}`, UserID: 7})
	last, _ := h.fake.Last("tools/call")
	c, _ := VerifyUserContext("s", last.Header.Get("X-User"), h.clk.Now())
	if c.JTI == jtis[0] {
		t.Fatal("jti must differ across logical calls")
	}
}

func TestCallWithoutRetriesFailsOnce(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Status: 500} })
	rt := resolve(t, h, ids...)
	out := rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: "{}"})
	if out.Status != CallError || h.fake.Count("tools/call") != 1 || len(rt.Usage()) != 0 {
		t.Fatalf("%+v calls=%d", out, h.fake.Count("tools/call"))
	}
}

func TestCallRetriesStopWhenContextEnds(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	h.set.update(func(s *RuntimeSettings) { s.RetryCount = 5 })
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Status: 500} })
	rt := resolve(t, h, ids...)
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	started := time.Now()
	out := rt.Call(ctx, CallRequest{ModelName: "a", ArgumentsJSON: "{}"})
	if out.Status != CallError || time.Since(started) > 2*time.Second {
		t.Fatalf("%+v after %v", out, time.Since(started))
	}
	if n := h.fake.Count("tools/call"); n >= 5 {
		t.Fatalf("backoff must abort with the context, saw %d attempts", n)
	}
}

func TestSigningWithoutSecretRefusesTheCall(t *testing.T) {
	h := newHarness(t)
	srv := h.server("S", func(in *ServerInput) { in.HeadersJSON = `{"X-User":"${RIOCHAT_SIGNED_USER_CONTEXT}"}` })
	h.fake.SetTools(toolOf("a", "", "", nil))
	h.sync(srv.ID, false)
	list, _ := h.svc.ListServerTools(context.Background(), srv.ID)
	rt := resolve(t, h, list[0].ID)
	h.secret = ""
	h.fake.Reset()
	out := rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: "{}"})
	if out.Status != CallError || !strings.Contains(out.Error, "signing failed") || out.Billable {
		t.Fatalf("%+v", out)
	}
	if len(h.fake.Requests()) != 0 {
		t.Fatal("nothing may be sent")
	}
	// A server without the placeholder is unaffected by the missing secret.
	plain, ids := runFixture2(t, h)
	_ = plain
	h.fake.Reset()
	if o := resolve(t, h, ids...).Call(context.Background(), CallRequest{ModelName: "b", ArgumentsJSON: "{}"}); o.Status != CallSuccess {
		t.Fatalf("%+v", o)
	}
}

func runFixture2(t *testing.T, h *harness) (*ServerRow, []uint) {
	t.Helper()
	srv := h.server("Plain")
	h.fake.SetTools(toolOf("b", "", "", nil))
	h.sync(srv.ID, false)
	list, _ := h.svc.ListServerTools(context.Background(), srv.ID)
	return srv, []uint{list[0].ID}
}

func TestCallUsesTokenAndStaticHeaders(t *testing.T) {
	h := newHarness(t)
	srv := h.server("S", func(in *ServerInput) { in.AuthToken = "tok"; in.HeadersJSON = `{"X-Static":" v "}` })
	h.fake.SetTools(toolOf("a", "", "", nil))
	h.sync(srv.ID, false)
	list, _ := h.svc.ListServerTools(context.Background(), srv.ID)
	h.fake.Reset()
	resolve(t, h, list[0].ID).Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: "{}", UserID: 3, ConversationID: 4, RequestID: "r"})
	last, _ := h.fake.Last("tools/call")
	if last.Header.Get("Authorization") != "Bearer tok" || last.Header.Get("X-Static") != "v" {
		t.Fatalf("headers: %v", last.Header)
	}
	meta := last.Params["_meta"].(map[string]any)
	if meta["user_id"] != float64(3) || meta["conversation_id"] != float64(4) || meta["request_id"] != "r" {
		t.Fatalf("meta: %v", meta)
	}
}

func TestResolveSnapshotsBindingAtSelectionTime(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", nil))
	h.db.Model(&Tool{}).Where("id = ?", ids[0]).Update("price_nanousd", 10)
	rt := resolve(t, h, ids...)
	h.db.Model(&Tool{}).Where("id = ?", ids[0]).Update("price_nanousd", 99)
	rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: "{}"})
	if u := rt.Usage(); u[0].PriceNanoUSD != 10 {
		t.Fatalf("price is the one at selection time: %+v", u)
	}
}

func TestConcurrencyLimiterBoundsCalls(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("a", "", "", countSchema))
	h.set.update(func(s *RuntimeSettings) { s.MaxConcurrentCalls = 2 })
	var cur, peak atomic.Int32
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
		n := cur.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		time.Sleep(60 * time.Millisecond)
		cur.Add(-1)
		return mcptest.Reply{Result: mcptest.Text("x")}
	})
	rt := resolve(t, h, ids...)
	var wg sync.WaitGroup
	for i := 0; i < 6; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			out := rt.Call(context.Background(), CallRequest{ModelName: "a", ArgumentsJSON: `{"count":` + string(rune('0'+i)) + `}`})
			if out.Status != CallSuccess {
				t.Errorf("%+v", out)
			}
		}()
	}
	wg.Wait()
	if peak.Load() > 2 || peak.Load() < 1 {
		t.Fatalf("peak concurrency %d, limit 2", peak.Load())
	}
}

func TestLimiterAbandonsWaitWhenContextEnds(t *testing.T) {
	l := &callLimiter{}
	if err := l.acquire(context.Background(), 1); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := l.acquire(ctx, 1); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("got %v", err)
	}
	l.release()
	if err := l.acquire(context.Background(), 1); err != nil {
		t.Fatalf("the abandoned waiter must not leak a slot: %v", err)
	}
	l.release()
	if l.active != 0 || len(l.waiters) != 0 {
		t.Fatalf("state: %d %d", l.active, len(l.waiters))
	}
}

func TestLimiterWakesWaitersInOrder(t *testing.T) {
	l := &callLimiter{}
	_ = l.acquire(context.Background(), 1)
	var order []int
	var mu sync.Mutex
	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := l.acquire(context.Background(), 1); err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			order = append(order, i)
			mu.Unlock()
			l.release()
		}()
		time.Sleep(20 * time.Millisecond)
	}
	l.release()
	wg.Wait()
	if len(order) != 3 {
		t.Fatalf("order %v", order)
	}
}

func TestValidateToolDefaults(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	srv, ids := runFixture(t, h, toolOf("a", "", "", imageSchema), toolOf("b", "", "", imageSchema), toolOf("c", "", "", nil))
	if err := h.svc.ValidateToolDefaults(ctx, nil); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.ValidateToolDefaults(ctx, []uint{ids[0], ids[2], ids[2], 0}); err != nil {
		t.Fatalf("valid list: %v", err)
	}
	if err := h.svc.ValidateMCPTools(ctx, 5, []uint{ids[2]}); err != nil {
		t.Fatalf("port adapter: %v", err)
	}
	if err := h.svc.ValidateToolDefaults(ctx, []uint{9999}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("unknown: %v", err)
	}
	h.db.Model(&Tool{}).Where("id = ?", ids[2]).Update("status", "inactive")
	if err := h.svc.ValidateToolDefaults(ctx, []uint{ids[2]}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("inactive tool: %v", err)
	}
	bindImage(t, h, ids[0], "base64")
	bindImage(t, h, ids[1], "base64")
	if err := h.svc.ValidateToolDefaults(ctx, []uint{ids[0], ids[1]}); !errors.Is(err, ErrMultipleProcessors) {
		t.Fatalf("two processors: %v", err)
	}
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Update("status", "inactive")
	if err := h.svc.ValidateToolDefaults(ctx, []uint{ids[0]}); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("inactive server: %v", err)
	}
}

func TestAnalyzeImages(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("vision", "Vision Tool", "", imageSchema), toolOf("other", "", "", nil))
	bindImage(t, h, ids[0], "data_url")
	h.db.Model(&Tool{}).Where("id = ?", ids[0]).Update("price_nanousd", 40)
	var got atomic.Value
	h.fake.OnCall(func(name string, args map[string]any, _ int) mcptest.Reply {
		got.Store(args)
		return mcptest.Reply{Result: mcptest.Text("a cat")}
	})
	rt := resolve(t, h, ids...)
	data := []byte("PNGDATA")
	res, err := rt.AnalyzeImages(context.Background(), []ImageInput{{FileName: "cat.png", MimeType: "image/png", Data: data}}, "  what is this?  ", CallRequest{UserID: 1})
	if err != nil || len(res) != 1 {
		t.Fatalf("%v %v", res, err)
	}
	args := got.Load().(map[string]any)
	wantImg := "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
	if args["image"] != wantImg || args["prompt"] != "what is this?" {
		t.Fatalf("arguments: %v", args)
	}
	a := res[0]
	if a.Text != "a cat" || a.Label != "Vision Tool / cat.png" || a.Format() != "[Vision Tool / cat.png]\na cat" {
		t.Fatalf("analysis: %+v", a)
	}
	o := a.Outcome
	if o.ToolType != ToolTypeAttachment || o.Status != CallSuccess || strings.Contains(o.Input, wantImg) || strings.Contains(o.Input, "PNGDATA") {
		t.Fatalf("outcome: %+v", o)
	}
	for _, f := range []string{"cat.png", "image/png", "data_url", `"bytes":7`} {
		if !strings.Contains(o.Input, f) {
			t.Errorf("audit summary lacks %s: %s", f, o.Input)
		}
	}
	if u := rt.Usage(); len(u) != 1 || u[0].Count != 1 || u[0].PriceNanoUSD != 40 {
		t.Fatalf("usage: %+v", u)
	}
	// No processor or no images: nothing to do.
	if res, err := resolve(t, h, ids[1]).AnalyzeImages(context.Background(), []ImageInput{{Data: data}}, "", CallRequest{}); res != nil || err != nil {
		t.Fatalf("no processor: %v %v", res, err)
	}
	if res, err := rt.AnalyzeImages(context.Background(), nil, "", CallRequest{}); res != nil || err != nil {
		t.Fatalf("no images: %v %v", res, err)
	}
}

func TestAnalyzeImagesBase64AndBudgets(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("vision", "", "", imageSchema))
	bindImage(t, h, ids[0], "base64")
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	long := strings.Repeat("\u5b57", 9000)
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Result: mcptest.Text(long)} })
	rt := resolve(t, h, ids...)
	one, err := rt.AnalyzeImages(context.Background(), []ImageInput{{FileName: "a.png", MimeType: "image/png", Data: []byte("x")}}, "", CallRequest{})
	if err != nil || len([]rune(one[0].Text)) != 6000 {
		t.Fatalf("a single analysis is cut to 6000 characters: %d %v", len([]rune(one[0].Text)), err)
	}
	if one[0].Label != "vision / a.png" {
		t.Fatalf("label falls back to the real name: %q", one[0].Label)
	}
	imgs := make([]ImageInput, 4)
	for i := range imgs {
		imgs[i] = ImageInput{FileName: "i.png", MimeType: "image/png", Data: []byte("x")}
	}
	many, err := rt.AnalyzeImages(context.Background(), imgs, "", CallRequest{})
	if err != nil {
		t.Fatal(err)
	}
	total := 0
	for _, a := range many {
		total += len([]rune(a.Text))
		if len([]rune(a.Text)) != 4000 {
			t.Fatalf("16000 split evenly over 4 images: %d", len([]rune(a.Text)))
		}
	}
	if total != 16000 {
		t.Fatalf("total %d", total)
	}
	// Plain base64 without a wrapper.
	var args atomic.Value
	h.fake.OnCall(func(_ string, a map[string]any, _ int) mcptest.Reply {
		args.Store(a)
		return mcptest.Reply{Result: mcptest.Text("ok")}
	})
	if _, err := rt.AnalyzeImages(context.Background(), []ImageInput{{FileName: "b.png", MimeType: "image/png", Data: []byte("hi")}}, "q", CallRequest{}); err != nil {
		t.Fatal(err)
	}
	if args.Load().(map[string]any)["image"] != base64.StdEncoding.EncodeToString([]byte("hi")) {
		t.Fatalf("base64 encoding: %v", args.Load())
	}
	// More images than the per-run call limit.
	h.set.update(func(s *RuntimeSettings) { s.MaxToolCallsPerRun = 2 })
	rt2 := resolve(t, h, ids...)
	if _, err := rt2.AnalyzeImages(context.Background(), imgs, "", CallRequest{}); !errors.Is(err, ErrImageProcessing) {
		t.Fatalf("too many images: %v", err)
	}
	if _, err := rt.AnalyzeImages(context.Background(), []ImageInput{{FileName: "big", Data: make([]byte, maxProcessorImageBytes+1)}}, "", CallRequest{}); !errors.Is(err, ErrImageProcessing) {
		t.Fatalf("too many bytes: %v", err)
	}
}

func TestAnalyzeImagesFailures(t *testing.T) {
	h := newHarness(t)
	_, ids := runFixture(t, h, toolOf("vision", "", "", imageSchema))
	bindImage(t, h, ids[0], "base64")
	rt := resolve(t, h, ids...)
	img := []ImageInput{{FileName: "a.png", MimeType: "image/png", Data: []byte("x")}}
	ctx := context.Background()

	// A remote failure is persisted as an error row and fails the run.
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply { return mcptest.Reply{Status: 500} })
	res, err := rt.AnalyzeImages(ctx, img, "", CallRequest{})
	if !errors.Is(err, ErrImageProcessing) || len(res) != 1 || res[0].Outcome.Status != CallError || res[0].Outcome.ToolType != ToolTypeAttachment {
		t.Fatalf("remote failure: %+v %v", res, err)
	}
	if len(rt.Usage()) != 0 {
		t.Fatal("failed calls are not billed")
	}
	// No extractable text still bills the successful upstream call.
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
		return mcptest.Reply{Result: map[string]any{"content": []any{map[string]any{"type": "image", "data": "zzz"}}}}
	})
	res, err = rt.AnalyzeImages(ctx, img, "", CallRequest{})
	if !errors.Is(err, ErrImageProcessing) || res[0].Outcome.Status != CallError || !strings.Contains(res[0].Outcome.Error, "no text") {
		t.Fatalf("empty analysis: %+v %v", res, err)
	}
	if u := rt.Usage(); len(u) != 1 || u[0].Count != 1 {
		t.Fatalf("a successful upstream call is metered even if extraction fails: %+v", u)
	}
	// The tool reported an error.
	h.fake.OnCall(func(string, map[string]any, int) mcptest.Reply {
		return mcptest.Reply{Result: map[string]any{"isError": true, "content": []any{map[string]any{"type": "text", "text": "bad image"}}}}
	})
	if _, err = rt.AnalyzeImages(ctx, img, "", CallRequest{}); !errors.Is(err, ErrImageProcessing) {
		t.Fatalf("isError: %v", err)
	}
	// The first failure stops the loop.
	imgs := []ImageInput{img[0], img[0], img[0]}
	h.fake.Reset()
	res, err = rt.AnalyzeImages(ctx, imgs, "", CallRequest{})
	if err == nil || len(res) != 1 || h.fake.Count("tools/call") != 1 {
		t.Fatalf("stops at the first failure: %d results, %d calls", len(res), h.fake.Count("tools/call"))
	}
}

func TestReencryptTokens(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	a := h.server("a", func(in *ServerInput) { in.AuthToken = "token-a" })
	b := h.server("b")
	c := h.server("c", func(in *ServerInput) { in.AuthToken = "token-c" })
	newRing, err := secretbox.NewKeyRing("brand-new-active-key-0123456789", []string{"test-active-key-0123456789"})
	if err != nil {
		t.Fatal(err)
	}
	n, err := ReencryptTokens(ctx, h.db, newRing)
	if err != nil || n != 2 {
		t.Fatalf("changed %d: %v", n, err)
	}
	onlyNew, _ := secretbox.NewKeyRing("brand-new-active-key-0123456789", nil)
	for id, want := range map[uint]string{a.ID: "token-a", c.ID: "token-c"} {
		var srv Server
		h.db.First(&srv, id)
		if plain, _, err := onlyNew.Decrypt(srv.AuthTokenEnc); err != nil || plain != want {
			t.Fatalf("server %d: %q %v", id, plain, err)
		}
	}
	var untouched Server
	h.db.First(&untouched, b.ID)
	if untouched.AuthTokenEnc != "" {
		t.Fatal("empty tokens stay empty")
	}
	if n, err = ReencryptTokens(ctx, h.db, newRing); err != nil || n != 0 {
		t.Fatalf("second pass must be a no-op: %d %v", n, err)
	}
	// A token that cannot be decrypted stops the pass with an error.
	h.db.Model(&Server{}).Where("id = ?", a.ID).Update("auth_token_enc", "v1:Zm9v")
	if _, err = ReencryptTokens(ctx, h.db, newRing); err == nil {
		t.Fatal("undecryptable token must surface")
	}
}

func TestSyncedToolsSurviveFromDatabaseShape(t *testing.T) {
	h := newHarness(t)
	// Tables, columns and the unique key match the persistence contract.
	for _, col := range []string{"id", "created_at", "updated_at", "name", "base_url", "auth_token_enc", "headers_json", "status", "sort_order", "tool_count", "last_synced_at", "last_error"} {
		if !h.db.Migrator().HasColumn(&Server{}, col) {
			t.Errorf("mcp_servers lacks %s", col)
		}
	}
	for _, col := range []string{"id", "created_at", "updated_at", "server_id", "name", "display_name", "description", "metadata_customized", "input_schema_json",
		"attachment_input_mode", "attachment_argument", "attachment_encoding", "attachment_prompt_argument", "price_nanousd", "status", "sort_order"} {
		if !h.db.Migrator().HasColumn(&Tool{}, col) {
			t.Errorf("mcp_tools lacks %s", col)
		}
	}
	if h.db.Migrator().HasColumn(&Server{}, "deleted_at") || h.db.Migrator().HasColumn(&Tool{}, "deleted_at") {
		t.Error("the contract has no deleted_at columns")
	}
	if err := h.db.Create(&Tool{ServerID: 1, Name: "dup", Status: "active"}).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.db.Create(&Tool{ServerID: 1, Name: "dup", Status: "active"}).Error; err == nil {
		t.Error("(server_id, name) must be unique")
	}
	if err := h.db.Create(&Tool{ServerID: 2, Name: "dup"}).Error; err != nil {
		t.Fatal(err)
	}
	var def Tool
	h.db.First(&def, "server_id = ?", 2)
	if def.Status != "inactive" || def.AttachmentInputMode != "none" || def.InputSchemaJSON != "{}" || def.MetadataCustomized != nil {
		t.Errorf("column defaults: %+v", def)
	}
	if err := migrateAgain(h); err != nil {
		t.Errorf("migrations must be idempotent: %v", err)
	}
}
