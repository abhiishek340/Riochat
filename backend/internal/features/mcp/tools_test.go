package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

func syncedTool(t *testing.T, h *harness, schema any) (*ServerRow, Tool) {
	t.Helper()
	h.fake.SetTools(toolOf("t", "T", "desc", schema))
	srv := h.server("srv")
	h.sync(srv.ID, false)
	return srv, h.toolByName(srv.ID, "t")
}

func TestUpdateToolValidation(t *testing.T) {
	h := newHarness(t)
	_, tool := syncedTool(t, h, imageSchema)
	cases := []struct {
		name  string
		patch ToolPatch
		want  *apperr.Error
	}{
		{"negative price", ToolPatch{PriceNanoUSD: ptr(int64(-1))}, ErrInvalidToolPrice},
		{"name 161", ToolPatch{DisplayName: ptr(strings.Repeat("x", 161))}, ErrInvalidToolName},
		{"desc 4097", ToolPatch{Description: ptr(strings.Repeat("x", 4097))}, ErrInvalidToolDesc},
		{"status on", ToolPatch{Status: ptr("on")}, ErrInvalidToolStatus},
		{"mode weird", ToolPatch{AttachmentInputMode: ptr("audio")}, ErrInvalidAttachment},
		{"encoding hex", ToolPatch{AttachmentInputMode: ptr("image"), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr("hex")}, ErrInvalidAttachment},
		{"argument 129", ToolPatch{AttachmentArgument: ptr(strings.Repeat("a", 129))}, ErrInvalidAttachment},
		{"prompt 129", ToolPatch{AttachmentPromptArgument: ptr(strings.Repeat("a", 129))}, ErrInvalidAttachment},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.UpdateTool(context.Background(), tool.ID, tc.patch)
			if !errors.Is(err, tc.want) || statusFor(err) != 400 {
				t.Fatalf("got %v", err)
			}
		})
	}
	// Boundary values are accepted.
	if _, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		DisplayName: ptr(strings.Repeat("\u5b57", 160)), Description: ptr(strings.Repeat("\u5b57", 4096)), PriceNanoUSD: ptr(int64(0)),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestUpdateToolPublicAttachmentMessage(t *testing.T) {
	h := newHarness(t)
	_, tool := syncedTool(t, h, imageSchema)
	_, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{AttachmentInputMode: ptr("image")})
	e, ok := apperr.Find(err)
	if !ok || e.Message != "invalid MCP tool attachment configuration" {
		t.Fatalf("public message: %+v", e)
	}
	if err.Error() != "invalid mcp tool attachment configuration" {
		t.Fatalf("internal text: %q", err.Error())
	}
}

func TestUpdateToolPriceAndFlags(t *testing.T) {
	h := newHarness(t)
	_, tool := syncedTool(t, h, nil)
	got, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{PriceNanoUSD: ptr(int64(2500000)), Status: ptr(" inactive ")})
	if err != nil || got.PriceNanoUSD != 2500000 || got.Status != StatusInactive {
		t.Fatalf("%+v %v", got, err)
	}
	if got.MetadataCustomized == nil || *got.MetadataCustomized {
		t.Fatal("price and status changes must not mark the metadata customized")
	}
	// Same display name: no change to the flag.
	got, _ = h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{DisplayName: ptr(" T ")})
	if *got.MetadataCustomized {
		t.Fatal("an unchanged name must not set the flag")
	}
	got, _ = h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{DisplayName: ptr("")})
	if got.DisplayName != "" || !*got.MetadataCustomized {
		t.Fatalf("an empty display name is allowed and customizes: %+v", got)
	}
	got, _ = h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{Description: ptr("  new desc ")})
	if got.Description != "new desc" {
		t.Fatalf("description trimmed: %q", got.Description)
	}
}

func TestUpdateToolNoChangeKeepsTimestamp(t *testing.T) {
	h := newHarness(t)
	_, tool := syncedTool(t, h, nil)
	got, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{})
	if err != nil || !got.UpdatedAt.Equal(tool.UpdatedAt) {
		t.Fatalf("empty patch must change nothing: %v", err)
	}
	got, err = h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{Status: ptr("active"), PriceNanoUSD: ptr(int64(0))})
	if err != nil || !got.UpdatedAt.Equal(tool.UpdatedAt) {
		t.Fatalf("identical values must not write: %v", err)
	}
	if _, err := h.svc.UpdateTool(context.Background(), 9999, ToolPatch{}); err == nil || statusFor(err) != 500 {
		t.Fatalf("missing tool is a generic 500: %v", err)
	}
}

func TestUpdateToolAttachmentConfiguration(t *testing.T) {
	h := newHarness(t)
	_, tool := syncedTool(t, h, imageSchema)
	ctx := context.Background()
	apply := func(p ToolPatch) (*Tool, error) { return h.svc.UpdateTool(ctx, tool.ID, p) }

	got, err := apply(ToolPatch{AttachmentInputMode: ptr(" IMAGE "), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr("Base64")})
	if err != nil || got.AttachmentInputMode != "image" || got.AttachmentEncoding != "base64" {
		t.Fatalf("basic: %+v %v", got, err)
	}
	if got, err = apply(ToolPatch{AttachmentPromptArgument: ptr("prompt")}); err != nil || got.AttachmentPromptArgument != "prompt" || got.AttachmentArgument != "image" {
		t.Fatalf("prompt merge: %+v %v", got, err)
	}
	for name, p := range map[string]ToolPatch{
		"prompt equals image": {AttachmentPromptArgument: ptr("image")},
		"missing argument":    {AttachmentArgument: ptr("missing")},
		"bad encoding":        {AttachmentEncoding: ptr("hex")},
		"empty encoding":      {AttachmentEncoding: ptr("")},
		"empty argument":      {AttachmentArgument: ptr("")},
		"missing prompt":      {AttachmentPromptArgument: ptr("nope")},
	} {
		if _, err := apply(p); !errors.Is(err, ErrInvalidAttachment) {
			t.Errorf("%s: got %v", name, err)
		}
	}
	// A failed update leaves the stored configuration alone.
	cur := h.toolByName(tool.ServerID, "t")
	if cur.AttachmentArgument != "image" || cur.AttachmentPromptArgument != "prompt" || cur.AttachmentEncoding != "base64" {
		t.Fatalf("configuration changed by failed updates: %+v", cur)
	}
	// Data URL is the other legal encoding.
	if got, err = apply(ToolPatch{AttachmentEncoding: ptr("data_url")}); err != nil || got.AttachmentEncoding != "data_url" {
		t.Fatalf("data_url: %+v %v", got, err)
	}
	// Switching to none blanks everything even if the request repeats the old fields.
	got, err = apply(ToolPatch{AttachmentInputMode: ptr("none"), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr("base64")})
	if err != nil || got.AttachmentInputMode != "none" || got.AttachmentArgument != "" || got.AttachmentEncoding != "" || got.AttachmentPromptArgument != "" {
		t.Fatalf("none must clear: %+v %v", got, err)
	}
	// Stray fields without image mode are invalid.
	if _, err := apply(ToolPatch{AttachmentArgument: ptr("image")}); !errors.Is(err, ErrInvalidAttachment) {
		t.Fatalf("argument under mode none: %v", err)
	}
}

func TestUpdateToolAttachmentSchemaRules(t *testing.T) {
	cases := []struct {
		name   string
		schema any
		ok     bool
	}{
		{"valid", imageSchema, true},
		{"integer argument", map[string]any{"properties": map[string]any{"image": map[string]any{"type": "integer"}}}, false},
		{"extra required", map[string]any{"properties": map[string]any{"image": map[string]any{"type": "string"}}, "required": []any{"image", "other"}}, false},
		{"no properties", map[string]any{"type": "object"}, false},
		{"ref to string def", map[string]any{
			"properties": map[string]any{"image": map[string]any{"$ref": "#/$defs/img"}},
			"$defs":      map[string]any{"img": map[string]any{"type": "string"}},
		}, true},
		{"unresolved ref", map[string]any{
			"properties": map[string]any{"image": map[string]any{"$ref": "#/$defs/none"}},
		}, false},
		{"optional only", map[string]any{"properties": map[string]any{"image": map[string]any{}}}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newHarness(t)
			_, tool := syncedTool(t, h, tc.schema)
			_, err := h.svc.UpdateTool(context.Background(), tool.ID, ToolPatch{
				AttachmentInputMode: ptr("image"), AttachmentArgument: ptr("image"), AttachmentEncoding: ptr("base64"),
			})
			if tc.ok && err != nil {
				t.Fatal(err)
			}
			if !tc.ok && !errors.Is(err, ErrInvalidAttachment) {
				t.Fatalf("got %v", err)
			}
		})
	}
}

func TestBulkStatus(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("a", "A", "", nil), toolOf("b", "B", "", nil))
	one, two := h.server("one"), h.server("two")
	h.sync(one.ID, false)
	h.sync(two.ID, false)
	ctx := context.Background()
	if _, err := h.svc.SetToolsStatus(ctx, one.ID, nil, "inactive"); !errors.Is(err, ErrInvalidSelection) {
		t.Fatalf("empty ids: %v", err)
	}
	if _, err := h.svc.SetToolsStatus(ctx, one.ID, []uint{1}, "paused"); !errors.Is(err, ErrInvalidToolStatus) {
		t.Fatalf("status: %v", err)
	}
	// Status is checked before the selection.
	if _, err := h.svc.SetToolsStatus(ctx, one.ID, nil, "paused"); !errors.Is(err, ErrInvalidToolStatus) {
		t.Fatalf("status first: %v", err)
	}
	foreign := h.toolByName(two.ID, "a")
	own := h.toolByName(one.ID, "a")
	tools, err := h.svc.SetToolsStatus(ctx, one.ID, []uint{own.ID, foreign.ID, 9999}, "inactive")
	if err != nil || len(tools) != 2 {
		t.Fatalf("%+v %v", tools, err)
	}
	if tools[0].Status != StatusInactive || tools[1].Status != StatusActive {
		t.Fatalf("only the listed own tool changes: %+v", tools)
	}
	if h.toolByName(two.ID, "a").Status != StatusActive {
		t.Fatal("a foreign tool must be ignored")
	}
}

func TestReorder(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("a", "A", "", nil), toolOf("b", "B", "", nil))
	s1, s2 := h.server("one"), h.server("two")
	h.sync(s1.ID, false)
	h.sync(s2.ID, false)
	ctx := context.Background()
	t1 := []uint{h.toolByName(s1.ID, "a").ID, h.toolByName(s1.ID, "b").ID}
	t2 := []uint{h.toolByName(s2.ID, "a").ID, h.toolByName(s2.ID, "b").ID}

	out, err := h.svc.Reorder(ctx, []OrderEntry{{ServerID: s2.ID, ToolIDs: []uint{t2[1], t2[0]}}, {ServerID: s1.ID, ToolIDs: t1}})
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || out[0].Server.ID != s2.ID || out[0].Server.SortOrder != 100 || out[1].Server.SortOrder != 200 {
		t.Fatalf("servers: %+v", out)
	}
	if out[0].Tools[0].ID != t2[1] || out[0].Tools[0].SortOrder != 100 || out[0].Tools[1].SortOrder != 200 || out[0].Tools[0].ServerName != "two" {
		t.Fatalf("tools: %+v", out[0].Tools)
	}
	rows, _ := h.svc.ListServers(ctx)
	if rows[0].ID != s2.ID {
		t.Fatalf("persisted order: %+v", rows)
	}
	tools, _ := h.svc.ListServerTools(ctx, s2.ID)
	if tools[0].ID != t2[1] {
		t.Fatalf("persisted tool order: %+v", tools)
	}

	bad := map[string][]OrderEntry{
		"empty":           {},
		"missing server":  {{ServerID: s1.ID, ToolIDs: t1}},
		"duplicate":       {{ServerID: s1.ID, ToolIDs: t1}, {ServerID: s1.ID, ToolIDs: t1}},
		"zero server":     {{ServerID: 0, ToolIDs: t1}, {ServerID: s2.ID, ToolIDs: t2}},
		"unknown server":  {{ServerID: 999, ToolIDs: t1}, {ServerID: s2.ID, ToolIDs: t2}},
		"missing tool":    {{ServerID: s1.ID, ToolIDs: t1[:1]}, {ServerID: s2.ID, ToolIDs: t2}},
		"foreign tool":    {{ServerID: s1.ID, ToolIDs: []uint{t1[0], t2[0]}}, {ServerID: s2.ID, ToolIDs: []uint{t2[1], t1[1]}}},
		"zero tool":       {{ServerID: s1.ID, ToolIDs: []uint{t1[0], 0}}, {ServerID: s2.ID, ToolIDs: t2}},
		"duplicate tool":  {{ServerID: s1.ID, ToolIDs: []uint{t1[0], t1[0]}}, {ServerID: s2.ID, ToolIDs: t2}},
		"unknown tool":    {{ServerID: s1.ID, ToolIDs: []uint{t1[0], 9999}}, {ServerID: s2.ID, ToolIDs: t2}},
		"no tools listed": {{ServerID: s1.ID}, {ServerID: s2.ID}},
	}
	for name, entries := range bad {
		if _, err := h.svc.Reorder(ctx, entries); !errors.Is(err, ErrInvalidSelection) || statusFor(err) != 400 {
			t.Errorf("%s: got %v", name, err)
		}
	}
	rows, _ = h.svc.ListServers(ctx)
	if rows[0].ID != s2.ID || rows[0].SortOrder != 100 {
		t.Fatal("a rejected reorder must change nothing")
	}
}

func TestListEnabledTools(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	h.fake.SetTools(toolOf("a", "A", "", nil), toolOf("b", "B", "", nil), toolOf("c", "C", "", nil))
	off := h.server("off")
	on := h.server("on")
	h.sync(off.ID, false)
	h.sync(on.ID, false)
	h.db.Model(&Server{}).Where("id = ?", off.ID).Update("status", "inactive")
	h.db.Model(&Tool{}).Where("server_id = ?", on.ID).Update("price_nanousd", 5)
	h.svc.SetToolsStatus(ctx, on.ID, []uint{h.toolByName(on.ID, "b").ID}, "inactive")

	h.set.update(func(s *RuntimeSettings) { s.Enabled = false })
	rows, err := h.svc.ListEnabledTools(ctx)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("switch off: %+v %v", rows, err)
	}

	h.set.update(func(s *RuntimeSettings) { s.Enabled = true })
	check := func(label string, want int64) {
		t.Helper()
		rows, err := h.svc.ListEnabledTools(ctx)
		if err != nil || len(rows) != 2 {
			t.Fatalf("%s: %+v %v", label, rows, err)
		}
		if rows[0].Name != "a" || rows[1].Name != "c" || rows[0].ServerName != "on" {
			t.Fatalf("%s: %+v", label, rows)
		}
		if rows[0].PriceNanoUSD != want || rows[1].PriceNanoUSD != want {
			t.Fatalf("%s: price %d want %d", label, rows[0].PriceNanoUSD, want)
		}
	}
	check("no billing source", 5)
	h.svc.billing = fakeBilling{mode: "usage"}
	check("paid mode", 5)
	h.svc.billing = fakeBilling{mode: "self"}
	check("self mode", 0)
	if h.toolByName(on.ID, "a").PriceNanoUSD != 5 {
		t.Fatal("the stored price must not change")
	}
	h.svc.billing = fakeBilling{err: errors.New("down")}
	if _, err := h.svc.ListEnabledTools(ctx); err == nil || statusFor(err) != 500 {
		t.Fatalf("billing failure: %v", err)
	}
}
