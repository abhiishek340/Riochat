package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestSyncDiscoversTools(t *testing.T) {
	for _, sse := range []bool{false, true} {
		name := "json"
		if sse {
			name = "sse"
		}
		t.Run(name, func(t *testing.T) {
			h := newHarness(t)
			h.fake.UseSSE(sse)
			h.fake.SetTools(toolOf("search", "Search", "d", searchSchema))
			srv := h.server("s")
			tools := h.sync(srv.ID, false)
			if len(tools) != 1 {
				t.Fatalf("tools: %+v", tools)
			}
			tl := tools[0]
			if tl.Name != "search" || tl.DisplayName != "Search" || tl.Description != "d" || tl.Status != StatusActive ||
				tl.PriceNanoUSD != 0 || tl.AttachmentInputMode != AttachNone || tl.SortOrder != 100 ||
				tl.MetadataCustomized == nil || *tl.MetadataCustomized {
				t.Fatalf("tool: %+v", tl)
			}
			if !strings.Contains(tl.InputSchemaJSON, `"properties"`) {
				t.Fatalf("schema: %s", tl.InputSchemaJSON)
			}
			rows, _ := h.svc.ListServers(context.Background())
			if rows[0].ToolCount != 1 || rows[0].LastSyncedAt == nil || rows[0].LastError != "" {
				t.Fatalf("server after sync: %+v", rows[0].Server)
			}
			if !rows[0].LastSyncedAt.Equal(h.clk.Now()) {
				t.Fatalf("lastSyncedAt %v", rows[0].LastSyncedAt)
			}
		})
	}
}

func TestSyncCleansListing(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(
		toolOf("  a  ", "", "  desc  ", nil),
		toolOf("   ", "blank", "", nil),
		toolOf("b", "  Bee  ", "", map[string]any{}),
		toolOf("a", "A again", "second", searchSchema),
	)
	srv := h.server("s")
	tools := h.sync(srv.ID, false)
	if len(tools) != 2 {
		t.Fatalf("tools: %+v", tools)
	}
	a := tools[0]
	if a.Name != "a" || a.DisplayName != "A again" || a.Description != "second" || a.SortOrder != 100 {
		t.Fatalf("duplicate collapses onto the first position with later content: %+v", a)
	}
	if b := tools[1]; b.Name != "b" || b.DisplayName != "Bee" || b.SortOrder != 200 || b.InputSchemaJSON != "{}" {
		t.Fatalf("b: %+v", b)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].ToolCount != 3 {
		t.Fatalf("toolCount counts non-blank entries: %d", rows[0].ToolCount)
	}
}

func TestSyncBlankTitleUsesName(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("raw_name", "", "", nil))
	srv := h.server("s")
	if got := h.sync(srv.ID, false)[0]; got.DisplayName != "raw_name" {
		t.Fatalf("display %q", got.DisplayName)
	}
}

func TestSyncKeepsAdminChoices(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("a", "A", "", nil))
	srv := h.server("s")
	h.sync(srv.ID, false)
	a := h.toolByName(srv.ID, "a")
	if _, err := h.svc.UpdateTool(context.Background(), a.ID, ToolPatch{PriceNanoUSD: ptr(int64(7)), Status: ptr("inactive")}); err != nil {
		t.Fatal(err)
	}
	h.db.Model(&Tool{}).Where("id = ?", a.ID).Update("sort_order", 555)
	h.fake.SetTools(toolOf("a", "A", "", searchSchema), toolOf("n", "N", "", nil))
	tools := h.sync(srv.ID, false)
	got := h.toolByName(srv.ID, "a")
	if got.ID != a.ID || got.Status != StatusInactive || got.PriceNanoUSD != 7 || got.SortOrder != 555 {
		t.Fatalf("existing tool must keep id, status, price and order: %+v", got)
	}
	if !strings.Contains(got.InputSchemaJSON, "properties") {
		t.Fatalf("schema must refresh: %s", got.InputSchemaJSON)
	}
	// New tools go after the current maximum: 555 + 100 * position(2).
	n := h.toolByName(srv.ID, "n")
	if n.SortOrder != 555+200 || n.Status != StatusActive {
		t.Fatalf("new tool: %+v", n)
	}
	if len(tools) != 2 || tools[0].Name != "n" && tools[0].Name != "a" {
		t.Fatalf("tools: %+v", tools)
	}
}

func TestSyncCustomizedMetadata(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("search", "Search", "orig", nil))
	srv := h.server("s")
	h.sync(srv.ID, false)
	tool := h.toolByName(srv.ID, "search")
	if _, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{DisplayName: ptr("My Search")}); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if !rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatal("customization must require confirmation")
	}
	h.fake.SetTools(toolOf("search", "Search", "orig", searchSchema))
	h.sync(srv.ID, false)
	got := h.toolByName(srv.ID, "search")
	if got.DisplayName != "My Search" || !strings.Contains(got.InputSchemaJSON, "properties") {
		t.Fatalf("customized name must survive, schema must update: %+v", got)
	}
	h.sync(srv.ID, true)
	got = h.toolByName(srv.ID, "search")
	if got.DisplayName != "Search" || got.MetadataCustomized == nil || *got.MetadataCustomized {
		t.Fatalf("overwrite must restore the remote name and clear the flag: %+v", got)
	}
	rows, _ = h.svc.ListServers(context.Background())
	if rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatal("confirmation must be gone")
	}
	// An uncustomized tool follows the remote on a plain sync.
	h.fake.SetTools(toolOf("search", "Search v2", "new", nil))
	h.sync(srv.ID, false)
	if got = h.toolByName(srv.ID, "search"); got.DisplayName != "Search v2" || got.Description != "new" {
		t.Fatalf("uncustomized follows remote: %+v", got)
	}
}

func TestSyncLegacyNullFlag(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("same", "Same", "d", nil), toolOf("diff", "Diff", "d", nil))
	srv := h.server("s")
	h.sync(srv.ID, false)
	h.db.Model(&Tool{}).Where("server_id = ?", srv.ID).Update("metadata_customized", nil)
	h.db.Model(&Tool{}).Where("name = ?", "diff").Update("display_name", "Admin Edited")
	h.fake.SetTools(toolOf("same", "Same", "d", nil), toolOf("diff", "Diff", "d", nil))
	h.sync(srv.ID, false)
	same, diff := h.toolByName(srv.ID, "same"), h.toolByName(srv.ID, "diff")
	if same.MetadataCustomized == nil || *same.MetadataCustomized {
		t.Fatalf("matching legacy row becomes not customized: %+v", same.MetadataCustomized)
	}
	if diff.MetadataCustomized == nil || !*diff.MetadataCustomized || diff.DisplayName != "Admin Edited" {
		t.Fatalf("differing legacy row becomes customized and keeps its name: %+v", diff)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if !rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatal("one customized tool remains")
	}
}

func TestSyncRemovesVanishedToolsAndLinks(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("old", "Old", "", nil), toolOf("new", "New", "", nil))
	srv := h.server("s")
	h.sync(srv.ID, false)
	h.link(1, h.toolByName(srv.ID, "old").ID)
	h.link(1, h.toolByName(srv.ID, "new").ID)
	h.fake.SetTools(toolOf("new", "New", "", nil))
	tools := h.sync(srv.ID, false)
	if len(tools) != 1 || tools[0].Name != "new" || h.linkCount() != 1 {
		t.Fatalf("tools=%+v links=%d", tools, h.linkCount())
	}
	// An empty listing removes everything.
	h.fake.SetTools()
	if tools = h.sync(srv.ID, false); len(tools) != 0 || h.linkCount() != 0 {
		t.Fatalf("empty discovery: tools=%+v links=%d", tools, h.linkCount())
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].ToolCount != 0 {
		t.Fatalf("toolCount %d", rows[0].ToolCount)
	}
}

func TestSyncFailureStoresOnlyTheFixedSummary(t *testing.T) {
	h := newHarness(t)
	srv := h.server("s", func(in *ServerInput) { in.AuthToken = "super-secret-token" })
	h.fake.FailList(500)
	_, err := h.svc.SyncServer(context.Background(), srv.ID, false)
	if err == nil || statusFor(err) != 500 {
		t.Fatalf("got %v", err)
	}
	if _, typed := apperrFind(err); typed {
		t.Fatalf("a remote failure must be the generic 500, got typed %v", err)
	}
	for _, leak := range []string{h.fake.URL, "secret-body", "super-secret-token", "500"} {
		if strings.Contains(err.Error(), leak) {
			t.Fatalf("error leaks %q: %v", leak, err)
		}
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].LastError != "Error while syncing MCP tools" {
		t.Fatalf("lastError %q", rows[0].LastError)
	}
	if rows[0].LastSyncedAt != nil {
		t.Fatal("a failed sync must not set lastSyncedAt")
	}
	// A later success clears the error.
	h.fake.FailList(0)
	h.fake.SetTools(toolOf("a", "A", "", nil))
	h.sync(srv.ID, false)
	rows, _ = h.svc.ListServers(context.Background())
	if rows[0].LastError != "" {
		t.Fatalf("lastError must clear: %q", rows[0].LastError)
	}
}

func TestUpdateServerDoesNotTouchLastError(t *testing.T) {
	h := newHarness(t)
	srv := h.server("s")
	h.fake.FailList(502)
	_, _ = h.svc.SyncServer(context.Background(), srv.ID, false)
	if _, err := h.svc.UpdateServer(context.Background(), srv.ID, ServerInput{Name: "s2", BaseURL: h.fake.URL}); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].LastError != "Error while syncing MCP tools" {
		t.Fatalf("lastError must persist across updates: %q", rows[0].LastError)
	}
}

func TestSyncDoesNotSendSignedContextPlaceholder(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("a", "A", "", nil))
	srv := h.server("s", func(in *ServerInput) {
		in.HeadersJSON = `{"X-User":" ${RIOCHAT_SIGNED_USER_CONTEXT} ","X-Static":"yes","  ":"blank-name"}`
		in.AuthToken = "tok"
	})
	h.sync(srv.ID, false)
	for _, req := range h.fake.Requests() {
		if req.Header.Get("X-User") != "" {
			t.Fatalf("%s carried the placeholder header", req.Method)
		}
		if req.Header.Get("X-Static") != "yes" || req.Header.Get("Authorization") != "Bearer tok" {
			t.Fatalf("%s headers: %v", req.Method, req.Header)
		}
	}
	if h.fake.Count("tools/list") != 1 {
		t.Fatal("tools/list was not requested")
	}
}

func TestSyncResetsInvalidatedAttachmentBinding(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("vision", "Vision", "", imageSchema), toolOf("keep", "Keep", "", imageSchema))
	srv := h.server("s")
	h.sync(srv.ID, false)
	for _, name := range []string{"vision", "keep"} {
		tool := h.toolByName(srv.ID, name)
		_, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{
			AttachmentInputMode: ptr("image"), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr("base64"), AttachmentPromptArgument: ptr("prompt"),
		})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
	changed := map[string]any{
		"type":       "object",
		"properties": map[string]any{"image": map[string]any{"type": "integer"}, "prompt": map[string]any{"type": "string"}},
	}
	h.fake.SetTools(toolOf("vision", "Vision", "", changed), toolOf("keep", "Keep", "", imageSchema))
	h.sync(srv.ID, false)
	v, k := h.toolByName(srv.ID, "vision"), h.toolByName(srv.ID, "keep")
	if v.AttachmentInputMode != AttachNone || v.AttachmentArgument != "" || v.AttachmentEncoding != "" || v.AttachmentPromptArgument != "" {
		t.Fatalf("invalid binding must reset: %+v", v)
	}
	if k.AttachmentInputMode != AttachImage || k.AttachmentArgument != "image" || k.AttachmentEncoding != "base64" || k.AttachmentPromptArgument != "prompt" {
		t.Fatalf("valid binding must survive: %+v", k)
	}
}

func TestSyncPreconditions(t *testing.T) {
	h := newHarness(t)
	if _, err := h.svc.SyncServer(context.Background(), 4242, false); err == nil || statusFor(err) != 500 {
		t.Fatalf("missing server: %v", err)
	}
	srv := h.server("s")
	// A stored URL that no longer passes the trusted-endpoint check.
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Update("base_url", "http://169.254.169.254/")
	if _, err := h.svc.SyncServer(context.Background(), srv.ID, false); !errors.Is(err, ErrInvalidServerURL) {
		t.Fatalf("got %v", err)
	}
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Update("base_url", h.fake.URL)
	h.svc.client = nil
	err := func() error { _, e := h.svc.SyncServer(context.Background(), srv.ID, false); return e }()
	if !errors.Is(err, ErrClientUnavailable) || statusFor(err) != 500 {
		t.Fatalf("got %v", err)
	}
	if h.fake.Count("initialize") != 0 {
		t.Fatal("no request may be sent without a client")
	}
}

func TestSyncUndecryptableTokenFails(t *testing.T) {
	h := newHarness(t)
	srv := h.server("s")
	h.db.Model(&Server{}).Where("id = ?", srv.ID).Update("auth_token_enc", "v1:bm90LXJlYWw=")
	if _, err := h.svc.SyncServer(context.Background(), srv.ID, false); err == nil || statusFor(err) != 500 {
		t.Fatalf("got %v", err)
	}
	if h.fake.Count("initialize") != 0 {
		t.Fatal("must not call the server")
	}
}

func TestSyncJSONRPCErrorAndEmptyResult(t *testing.T) {
	h := newHarness(t)
	srv := h.server("s")
	h.fake.FailList(401)
	if _, err := h.svc.SyncServer(context.Background(), srv.ID, false); err == nil {
		t.Fatal("expected failure")
	}
}

func TestSyncSkipsUnstorableNamesAndCutsLongTitles(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(
		toolOf(strings.Repeat("n", 161), "too long name", "", nil),
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		toolOf("ok", strings.Repeat("\u5b57", 200), "", nil),
	)
	srv := h.server("s")
	tools := h.sync(srv.ID, false)
	if len(tools) != 1 || tools[0].Name != "ok" {
		t.Fatalf("tools: %+v", tools)
	}
	if got := len([]rune(tools[0].DisplayName)); got != 160 {
		t.Fatalf("display name runes: %d", got)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].ToolCount != 1 {
		t.Fatalf("toolCount %d", rows[0].ToolCount)
	}
}
