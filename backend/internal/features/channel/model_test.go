package channel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const mdPath = "/api/v1/admin/llm/models"

func TestModelCreateDefaultsAndInference(t *testing.T) {
	s := newSuite(t)
	m := s.mkModel("gpt-image-1", nil)
	if m.KindsJSON != `["image_gen","image_edit"]` || m.Vendor != "openai" || m.Icon != "openai" {
		t.Fatalf("inferred: %+v", m)
	}
	if m.Status != "active" || m.AccessScope != "public" || m.CBPolicyMode != "default" || m.CapabilitiesJSON != "{}" {
		t.Fatalf("defaults: %+v", m)
	}
	if m.VendorName != "OpenAI" || m.SortOrder != 100 || m.DisplayGroupID != nil {
		t.Fatalf("presentation: %+v", m)
	}
	if m.ProtocolsJSON != "[]" || m.UpstreamNamesJSON != "[]" || m.SourceCount != 0 || m.ActiveSourceCount != 0 {
		t.Fatalf("aggregates: %+v", m)
	}
	s.expect(s.do("POST", mdPath, map[string]any{"platformModelName": "gpt-image-1"}), 409, "llm.platform_model_name_exists")
	for _, name := range []string{"personal:abc/x", "PERSONAL:x", "bad\tname", "bad\nname"} {
		s.expect(s.do("POST", mdPath, map[string]any{"platformModelName": name}), 400, "llm.invalid_platform_model_name")
	}
	// Inner spaces are fine, case is preserved and uniqueness is exact.
	a := s.mkModel("GPT 4o", nil)
	b := s.mkModel("gpt 4o", nil)
	if a.PlatformModelName != "GPT 4o" || b.ID == a.ID {
		t.Fatalf("names: %+v %+v", a, b)
	}
	// Sort order grows by 100.
	if b.SortOrder != a.SortOrder+100 {
		t.Fatalf("sort order: %d %d", a.SortOrder, b.SortOrder)
	}
	// Each inference rule from the spec.
	for name, want := range map[string]string{
		"gemini-omni-flash-x":  `["chat","image_gen","image_edit","video_gen"]`,
		"dall-e-3":             `["image_gen"]`,
		"imagen-4":             `["image_gen"]`,
		"grok-imagine-video-1": `["video_gen","video_extension"]`,
		"sora":                 `["video_gen"]`,
		"kling-v2":             `["video_gen"]`,
		"veo-3":                `["video_gen"]`,
		"gpt-4o-audio-preview": `["audio"]`,
		"grok-imagine-image-x": `["image_gen","image_edit"]`,
		"nano-banana":          `["image_gen","image_edit"]`,
		"gemini-3-pro-image":   `["image_gen","image_edit"]`,
		"chatgpt-image-latest": `["image_gen","image_edit"]`,
		"claude-3-opus":        `["chat"]`,
		"something-else":       `["chat"]`,
	} {
		if got := s.mkModel(name, nil).KindsJSON; got != want {
			t.Errorf("kinds of %s: %s want %s", name, got, want)
		}
	}
}

func TestModelCreateValidation(t *testing.T) {
	s := newSuite(t)
	seq := 0
	post := func(extra map[string]any) (int, string) {
		seq++
		body := map[string]any{"platformModelName": fmt.Sprintf("model-%d", seq)}
		for k, v := range extra {
			body[k] = v
		}
		w := s.do("POST", mdPath, body)
		return w.Code, errCode(s, w)
	}
	cases := []struct {
		name   string
		extra  map[string]any
		status int
		code   string
	}{
		{"unknown vendor", map[string]any{"vendor": "nonexistent"}, 400, "model_vendor.not_found"},
		{"vendor alias", map[string]any{"vendor": "Claude"}, 200, ""},
		{"vendor bad key", map[string]any{"vendor": "-x"}, 400, "request.invalid_model_vendor"},
		{"data uri icon", map[string]any{"icon": "data:image/png;base64,AAA"}, 400, "llm.model_icon_invalid"},
		{"missing asset", map[string]any{"icon": "asset:ico_" + strings.Repeat("a", 32)}, 400, "llm.model_icon_asset_not_found"},
		{"malformed asset", map[string]any{"icon": "asset:ico_zz"}, 400, "llm.model_icon_invalid"},
		{"slug icon", map[string]any{"icon": "My-Slug"}, 200, ""},
		{"url icon", map[string]any{"icon": "https://cdn.example.com/a.png"}, 200, ""},
		{"userinfo icon", map[string]any{"icon": "https://u:p@cdn.example.com/a.png"}, 400, "llm.model_icon_invalid"},
		{"rooted icon", map[string]any{"icon": "/static/a.png"}, 200, ""},
		{"protocol relative icon", map[string]any{"icon": "//evil.example/a.png"}, 400, "llm.model_icon_invalid"},
		{"icon too long", map[string]any{"icon": strings.Repeat("a", 2049)}, 400, "request.invalid_body"},
		{"bad json caps", map[string]any{"capabilitiesJSON": "{"}, 400, "config.invalid_json"},
		{"bad kinds", map[string]any{"kindsJSON": `["chat","telepathy"]`}, 400, "llm.invalid_kinds"},
		{"bare kind", map[string]any{"kindsJSON": `Image_Gen`}, 200, ""},
		{"empty kinds array", map[string]any{"kindsJSON": `[]`}, 400, "llm.invalid_kinds"},
		{"prompt too long", map[string]any{"systemPrompt": strings.Repeat("x", 20001)}, 400, "llm.system_prompt_too_long"},
		{"prompt unicode counts code points", map[string]any{"systemPrompt": strings.Repeat("é", 20000)}, 200, ""},
		{"bad scope", map[string]any{"accessScope": "private"}, 400, "request.invalid_model_access_scope"},
		{"internal scope", map[string]any{"accessScope": "Internal"}, 200, ""},
		{"bad status", map[string]any{"status": "paused"}, 400, "request.invalid_body"},
		{"bad policy", map[string]any{"cbPolicyMode": "strict"}, 400, "request.invalid_body"},
		{"negative breaker", map[string]any{"cbFailureThreshold": -1}, 400, "request.invalid_body"},
		{"unknown group", map[string]any{"displayGroupID": 9999}, 400, "model_display_group.not_found"},
	}
	for _, c := range cases {
		status, code := post(c.extra)
		if status != c.status || code != c.code {
			t.Errorf("%s: got %d %q want %d %q", c.name, status, code, c.status, c.code)
		}
	}
	// Stored values are normalized.
	v := s.mkModel("vendor-alias", map[string]any{"vendor": "Claude", "accessScope": "INTERNAL", "kindsJSON": " Image_Gen "})
	if v.Vendor != "anthropic" || v.AccessScope != "internal" || v.KindsJSON != `["image_gen"]` {
		t.Fatalf("normalized: %+v", v)
	}
}

func TestModelUpdateIdentityRecomputation(t *testing.T) {
	s := newSuite(t)
	m := s.mkModel("claude-3-opus", nil)
	if m.Vendor != "anthropic" || m.Icon != "claude" {
		t.Fatalf("created: %+v", m)
	}
	path := fmt.Sprintf("%s/%d", mdPath, m.ID)
	upd := func(body map[string]any) ModelView {
		return decodeInto[struct {
			Model ModelView `json:"model"`
		}](t, s.ok("PATCH", path, body)).Model
	}
	got := upd(map[string]any{"platformModelName": "gpt-4o-mini"})
	if got.Vendor != "openai" || got.Icon != "openai" || got.PlatformModelName != "gpt-4o-mini" {
		t.Fatalf("renamed: %+v", got)
	}
	// A hand-set icon survives later renames.
	got = upd(map[string]any{"icon": "custom-slug"})
	if got.Icon != "custom-slug" {
		t.Fatalf("custom icon: %+v", got)
	}
	got = upd(map[string]any{"platformModelName": "claude-sonnet-4"})
	if got.Vendor != "anthropic" || got.Icon != "custom-slug" {
		t.Fatalf("custom icon must be retained: %+v", got)
	}
	// An explicit empty icon resets to auto.
	got = upd(map[string]any{"icon": ""})
	if got.Icon != "claude" {
		t.Fatalf("auto icon: %+v", got)
	}
	// Explicit vendor in the same request wins over detection.
	got = upd(map[string]any{"platformModelName": "gpt-9", "vendor": "deepseek"})
	if got.Vendor != "deepseek" {
		t.Fatalf("explicit vendor: %+v", got)
	}
	// Absent vs present-empty: kinds blank normalizes to chat on update.
	got = upd(map[string]any{"kindsJSON": `["image_gen"]`})
	if got.KindsJSON != `["image_gen"]` {
		t.Fatalf("kinds: %+v", got)
	}
	got = upd(map[string]any{"description": "hello"})
	if got.KindsJSON != `["image_gen"]` || got.Description != "hello" {
		t.Fatalf("absent keeps: %+v", got)
	}
	got = upd(map[string]any{"kindsJSON": ""})
	if got.KindsJSON != `["chat"]` {
		t.Fatalf("blank kinds on update: %+v", got)
	}
	// Name collisions and unknown ids.
	s.mkModel("other-model", nil)
	s.expect(s.do("PATCH", path, map[string]any{"platformModelName": "other-model"}), 409, "llm.platform_model_name_exists")
	s.expect(s.do("PATCH", mdPath+"/9999", map[string]any{"description": "x"}), 404, "model.not_found")
	s.expect(s.do("PATCH", mdPath+"/zz", map[string]any{"description": "x"}), 400, "model.invalid_id")
	s.expect(s.do("PATCH", path, map[string]any{"vendor": "ghost"}), 400, "model_vendor.not_found")
	s.expect(s.do("PATCH", path, map[string]any{"status": "weird"}), 400, "request.invalid_body")
}

func TestModelUpdateContextWindowHousekeeping(t *testing.T) {
	s := newSuite(t)
	auto := s.mkModel("auto-window", map[string]any{"capabilitiesJSON": `{"contextWindow":50000,"context_window":1,"_riochatContextWindowMode":"AUTO","maxOutputTokens":4000}`})
	manual := s.mkModel("manual-window", map[string]any{"capabilitiesJSON": `{"contextWindow":50000}`})
	rename := func(m ModelView, name string, extra map[string]any) ModelView {
		body := map[string]any{"platformModelName": name}
		for k, v := range extra {
			body[k] = v
		}
		return decodeInto[struct {
			Model ModelView `json:"model"`
		}](t, s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, m.ID), body)).Model
	}
	a := rename(auto, "auto-window-2", nil)
	var caps map[string]any
	_ = json.Unmarshal([]byte(a.CapabilitiesJSON), &caps)
	if _, ok := caps["contextWindow"]; ok || caps["_riochatContextWindowMode"] != nil || caps["maxOutputTokens"] == nil {
		t.Fatalf("auto window must be stripped: %s", a.CapabilitiesJSON)
	}
	b := rename(manual, "manual-window-2", nil)
	if !strings.Contains(b.CapabilitiesJSON, "50000") || b.ContextWindow != 50000 {
		t.Fatalf("manual window must be kept: %+v", b)
	}
	// Supplying new capabilities replaces them verbatim.
	c := rename(b, "manual-window-3", map[string]any{"capabilitiesJSON": `{"contextWindow":70000}`})
	if c.ContextWindow != 70000 {
		t.Fatalf("explicit caps: %+v", c)
	}
	// An emptied object becomes the empty string.
	only := s.mkModel("only-auto", map[string]any{"capabilitiesJSON": `{"contextWindow":50000,"_riochatContextWindowMode":"auto"}`})
	o := rename(only, "only-auto-2", nil)
	if o.CapabilitiesJSON != "" {
		t.Fatalf("emptied caps: %q", o.CapabilitiesJSON)
	}
}

func TestModelCapabilityValidation(t *testing.T) {
	s := newSuite(t)
	cases := []struct {
		name string
		caps string
		code string
	}{
		{"window too small", `{"contextWindow":1000}`, "llm.invalid_model_capabilities"},
		{"window too large", `{"contextWindow":17000000}`, "llm.invalid_model_capabilities"},
		{"window as string ok", `{"contextWindow":"8192"}`, ""},
		{"output not below window", `{"contextWindow":8192,"maxOutputTokens":8192}`, "llm.invalid_model_capabilities"},
		{"output zero", `{"maxOutputTokens":0}`, "llm.invalid_model_capabilities"},
		{"output too big", `{"maxOutputTokens":1000001}`, "llm.invalid_model_capabilities"},
		{"output ok alone", `{"maxOutputTokens":4096}`, ""},
		{"ultra on openai", `{"reasoning":{"format":"openai","levels":["low","ultra"],"default":"low"}}`, ""},
		{"minimal on anthropic effort", `{"reasoning":{"format":"anthropic_effort","levels":["minimal"],"default":"minimal"}}`, "llm.reasoning_invalid_levels"},
		{"unknown format", `{"reasoning":{"format":"magic","levels":["low"],"default":"low"}}`, "llm.reasoning_invalid_format"},
		{"reasoning not object", `{"reasoning":"yes"}`, "llm.reasoning_invalid_format"},
		{"default not in levels", `{"reasoning":{"format":"openai","levels":["low"],"default":"high"}}`, "llm.reasoning_invalid_default"},
		{"duplicate levels", `{"reasoning":{"format":"openai","levels":["low","low"],"default":"low"}}`, "llm.reasoning_invalid_levels"},
		{"empty levels", `{"reasoning":{"format":"openai","levels":[],"default":"low"}}`, "llm.reasoning_invalid_levels"},
		{"toggle needs none", `{"reasoning":{"format":"toggle","levels":["low","high"],"default":"low"}}`, "llm.reasoning_invalid_levels"},
		{"toggle ok", `{"reasoning":{"format":"toggle","levels":["none","high"],"default":"high"}}`, ""},
		{"anthropic budget too low", `{"reasoning":{"format":"anthropic_budget","levels":["low"],"default":"low","budgets":{"low":500}}}`, "llm.reasoning_invalid_budgets"},
		{"anthropic budget missing", `{"reasoning":{"format":"anthropic_budget","levels":["low"],"default":"low"}}`, "llm.reasoning_invalid_budgets"},
		{"anthropic budget ok", `{"reasoning":{"format":"anthropic_budget","levels":["low"],"default":"low","budgets":{"low":2048}}}`, ""},
		{"gemini level max", `{"reasoning":{"format":"gemini_level","levels":["low","max"],"default":"low"}}`, "llm.reasoning_invalid_levels"},
		{"control forbidden path", `{"controls":[{"id":"x","type":"select","options":[{"value":"a","patch":{"stream":true}}]}]}`, "llm.model_control_forbidden_path"},
		{"control nested forbidden", `{"controls":[{"id":"x","type":"number","path":"messages.0.content"}]}`, "llm.model_control_forbidden_path"},
		{"control ok", `{"controls":[{"id":"temp","type":"number","path":"temperature","min":0,"max":2,"step":0.1}]}`, ""},
		{"control bad id", `{"controls":[{"id":"-x","type":"toggle"}]}`, "llm.model_controls_invalid"},
		{"control duplicate ids", `{"controls":[{"id":"a","type":"toggle"},{"id":"a","type":"toggle"}]}`, "llm.model_controls_invalid"},
		{"select without options", `{"controls":[{"id":"a","type":"select"}]}`, "llm.model_controls_invalid"},
		{"select default not option", `{"controls":[{"id":"a","type":"select","default":"z","options":[{"value":"a"}]}]}`, "llm.model_controls_invalid"},
		{"number min>max", `{"controls":[{"id":"a","type":"number","path":"p","min":5,"max":1}]}`, "llm.model_controls_invalid"},
		{"input modalities ok", `{"inputModalities":["image","text"]}`, ""},
		{"input modalities unknown", `{"inputModalities":["smell"]}`, "llm.invalid_model_capabilities"},
		{"input modalities not array", `{"inputModalities":"image"}`, "llm.invalid_model_capabilities"},
	}
	for i, c := range cases {
		w := s.do("POST", mdPath, map[string]any{"platformModelName": fmt.Sprintf("caps-%02d", i), "capabilitiesJSON": c.caps})
		wantStatus := 200
		if c.code != "" {
			wantStatus = 400
		}
		if w.Code != wantStatus || errCode(s, w) != c.code {
			t.Errorf("%s: got %d %q (%s) want %d %q", c.name, w.Code, errCode(s, w), w.Body.String(), wantStatus, c.code)
		}
	}
}

func TestModelOrderAndDeleteAndGroups(t *testing.T) {
	s := newSuite(t)
	var ids []uint
	for i := 0; i < 5; i++ {
		ids = append(ids, s.mkModel(fmt.Sprintf("order-%d", i), nil).ID)
	}
	order := func() map[uint]int {
		var ms []PlatformModel
		s.env.DB.Find(&ms)
		out := map[uint]int{}
		for _, m := range ms {
			out[m.ID] = m.SortOrder
		}
		return out
	}
	before := order()
	s.expect(s.do("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{ids[4], ids[4]}}), 400, "request.invalid_model_order")
	s.expect(s.do("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{ids[4], 0}}), 400, "request.invalid_model_order")
	s.expect(s.do("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{}}), 400, "request.invalid_body")
	s.expect(s.do("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{ids[4], 999999}}), 404, "model.not_found")
	if fmt.Sprint(order()) != fmt.Sprint(before) {
		t.Fatalf("a failed reorder must change nothing")
	}
	s.ok("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{ids[4], ids[2]}})
	after := order()
	if after[ids[4]] != 100 || after[ids[2]] != 200 || after[ids[0]] != before[ids[0]] || after[ids[1]] != before[ids[1]] {
		t.Fatalf("reorder: %v (before %v)", after, before)
	}

	// Display group assignment.
	g := s.mkGroup("Fast", nil)
	s.ok("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{ids[0], ids[1]}, "displayGroupID": g.ID})
	var m PlatformModel
	m = PlatformModel{}
	s.env.DB.First(&m, ids[0])
	if m.DisplayGroupID == nil || *m.DisplayGroupID != g.ID {
		t.Fatalf("assign: %+v", m)
	}
	s.expect(s.do("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{ids[0], 99999}, "displayGroupID": g.ID}), 400, "request.invalid_model_display_group")
	s.expect(s.do("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{0}, "displayGroupID": g.ID}), 400, "request.invalid_model_display_group")
	s.expect(s.do("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{ids[0]}, "displayGroupID": 9999}), 404, "model_display_group.not_found")
	s.expect(s.do("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{ids[0]}}), 400, "request.invalid_body")
	s.ok("PATCH", mdPath+"/display-group", map[string]any{"modelIDs": []uint{ids[0]}, "displayGroupID": 0})
	m = PlatformModel{}
	s.env.DB.First(&m, ids[0])
	if m.DisplayGroupID != nil {
		t.Fatalf("0 clears: %+v", m)
	}
	// Updating with displayGroupID 0 clears; absent keeps.
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, ids[1]), map[string]any{"description": "d"})
	var m1 PlatformModel
	s.env.DB.First(&m1, ids[1])
	if m1.DisplayGroupID == nil {
		t.Fatal("absent displayGroupID must keep the group")
	}
	data := s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, ids[1]), map[string]any{"displayGroupID": 0})
	if mv := decodeInto[struct {
		Model ModelView `json:"model"`
	}](t, data).Model; mv.DisplayGroupID != nil {
		t.Fatalf("cleared: %+v", mv)
	}

	// Deleting a model removes routes and manual group links but keeps upstream models.
	up := s.mkUpstream("delm", "openai", nil)
	row := s.bind(up.ID, "to-delete", "to-delete-up", []string{ProtoOpenAIChat}, nil)
	pg := PermissionGroup{Name: "grp", RateMultiplierPercent: 100}
	s.env.DB.Create(&pg)
	s.env.DB.Create(&PermissionGroupModelAccess{GroupID: pg.ID, PlatformModelID: row.PlatformModelID})
	s.ok("DELETE", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), nil)
	var n int64
	s.env.DB.Model(&ModelRoute{}).Where("platform_model_id = ?", row.PlatformModelID).Count(&n)
	if n != 0 {
		t.Fatal("routes must go")
	}
	s.env.DB.Model(&PermissionGroupModelAccess{}).Count(&n)
	if n != 0 {
		t.Fatal("group links must go")
	}
	s.env.DB.Model(&UpstreamModel{}).Where("id = ?", row.ID).Count(&n)
	if n != 1 {
		t.Fatal("upstream model must stay")
	}
	rows := decodeInto[page[UpstreamModelRow]](t, s.ok("GET", fmt.Sprintf("%s/%d/models", upPath, up.ID), nil))
	if rows.Total != 1 || rows.Results[0].RouteID != 0 || rows.Results[0].PlatformModelName != "" {
		t.Fatalf("unbound row expected: %+v", rows)
	}
	s.expect(s.do("DELETE", fmt.Sprintf("%s/%d", mdPath, row.PlatformModelID), nil), 404, "model.not_found")
	res := decodeInto[BatchResult](t, s.ok("POST", mdPath+"/batch-delete", map[string]any{"ids": []uint{ids[0], 99999}}))
	if res.SuccessCount != 1 || res.NotFoundCount != 1 {
		t.Fatalf("batch: %+v", res)
	}
}

func (s *suite) mkGroup(name string, modelIDs []uint) GroupView {
	s.t.Helper()
	body := map[string]any{"name": name}
	if modelIDs != nil {
		body["modelIDs"] = modelIDs
	}
	return decodeInto[struct {
		Group GroupView `json:"group"`
	}](s.t, s.ok("POST", "/api/v1/admin/llm/model-display-groups", body)).Group
}

func TestModelListFiltersAndSorting(t *testing.T) {
	s := newSuite(t)
	up1 := s.mkUpstream("one", "openai", nil)
	up2 := s.mkUpstream("two", "anthropic", nil)
	s.bind(up1.ID, "gpt-a", "gpt-a", []string{ProtoOpenAIChat}, nil)
	s.bind(up1.ID, "gpt-b", "gpt-b", []string{ProtoOpenAIResponses}, nil)
	s.bind(up2.ID, "gpt-b", "claude-x", []string{ProtoAnthropicMessages}, nil)
	s.bind(up2.ID, "claude-a", "claude-a", []string{ProtoAnthropicMessages}, nil)
	s.mkModel("lonely", nil)                                             // no sources
	s.mkModel("internal-one", map[string]any{"accessScope": "internal"}) // no sources
	s.mkModel("sleepy", map[string]any{"status": "inactive", "description": "zzz-needle"})

	names := func(q string) []string {
		var out []string
		for _, m := range decodeInto[page[ModelView]](t, s.ok("GET", mdPath+q, nil)).Results {
			out = append(out, m.PlatformModelName)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	// Default order: available first (grouped by vendor in first-seen order), then no sources.
	eq(names(""), "gpt-a", "gpt-b", "claude-a", "lonely", "internal-one", "sleepy")
	eq(names("?sort=id_desc"), "sleepy", "internal-one", "lonely", "claude-a", "gpt-b", "gpt-a")
	eq(names("?sort=platformModelName_asc"), "claude-a", "gpt-a", "gpt-b", "internal-one", "lonely", "sleepy")
	eq(names("?sort=sourceCount_desc&page_size=1"), "gpt-b")
	eq(names("?q=NEEDLE"), "sleepy")
	eq(names("?q=openai&sort=platformModelName_asc"), "gpt-a", "gpt-b")
	eq(names("?q=Anthropic&sort=platformModelName_asc"), "claude-a")
	eq(names("?status=inactive"), "sleepy")
	eq(names("?status=bogus&only_active=true&sort=platformModelName_asc"), "claude-a", "gpt-a", "gpt-b", "internal-one", "lonely")
	eq(names("?only_available=true&sort=platformModelName_asc"), "claude-a", "gpt-a", "gpt-b")
	eq(names("?vendor=anthropic"), "claude-a")
	eq(names("?protocol=openai_responses"), "gpt-b")
	eq(names(fmt.Sprintf("?upstream=%d&sort=platformModelName_asc", up2.ID)), "claude-a", "gpt-b")
	eq(names("?upstream=abc&only_available=true&sort=platformModelName_asc"), "claude-a", "gpt-a", "gpt-b")

	// Aggregates on the dual-source model.
	var gb ModelView
	for _, m := range decodeInto[page[ModelView]](t, s.ok("GET", mdPath+"?q=gpt-b", nil)).Results {
		gb = m
	}
	if gb.SourceCount != 2 || gb.ActiveSourceCount != 2 {
		t.Fatalf("counts: %+v", gb)
	}
	if gb.ProtocolsJSON != `["anthropic_messages","openai_responses"]` || gb.UpstreamNamesJSON != `["one","two"]` {
		t.Fatalf("aggregates: %s %s", gb.ProtocolsJSON, gb.UpstreamNamesJSON)
	}
	// An inactive model reports zero active sources; an open circuit is not counted.
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, gb.ID), map[string]any{"status": "inactive"})
	for _, m := range decodeInto[page[ModelView]](t, s.ok("GET", mdPath+"?q=gpt-b", nil)).Results {
		if m.ActiveSourceCount != 0 || m.SourceCount != 2 {
			t.Fatalf("inactive model: %+v", m)
		}
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, gb.ID), map[string]any{"status": "active"})
	s.setBreaker(`{"enabled":true}`)
	s.ok("POST", fmt.Sprintf("%s/%d/circuit/open", upPath, up2.ID), nil)
	for _, m := range decodeInto[page[ModelView]](t, s.ok("GET", mdPath+"?q=gpt-b", nil)).Results {
		if m.ActiveSourceCount != 1 || m.SourceCount != 2 {
			t.Fatalf("open circuit must reduce active sources: %+v", m)
		}
	}
}

func TestModelContextWindowResolution(t *testing.T) {
	s := newSuite(t)
	cases := map[string]int{
		"gpt-4.1": 1047576, "gpt_4_1": 1047576, "openai/gpt-4.1-mini": 1047576, "gpt-4.10": 128000,
		"claude-3.7-sonnet": 200000, "o3-mini": 200000, "gemini-2.5-pro": 1000000, "grok-3-beta": 131072,
		"gpt-4o": 128000, "gpt-3.5-turbo": 16385, "unknown-model": 128000, "foo-o3": 128000,
	}
	for name, want := range cases {
		if got := s.mkModel(name, nil).ContextWindow; got != want {
			t.Errorf("%s: window %d want %d", name, got, want)
		}
	}
	if got := s.mkModel("gpt-4.1-override", map[string]any{"capabilitiesJSON": `{"contextWindow":50000}`}).ContextWindow; got != 50000 {
		t.Errorf("override: %d", got)
	}
	// The configured fallback applies to unknown models.
	s.env.DB.Create(&settingRow{Namespace: "chat", Key: "context_window_fallback_tokens", Value: "200000", ValueType: "int"})
	if got := s.mkModel("another-unknown", nil).ContextWindow; got != 200000 {
		t.Errorf("fallback: %d", got)
	}
	s.env.DB.Model(&settingRow{}).Where("namespace = ? AND \"key\" = ?", "chat", "context_window_fallback_tokens").Update("value", "10")
	if got := s.mkModel("third-unknown", nil).ContextWindow; got != 128000 {
		t.Errorf("out-of-range fallback: %d", got)
	}
}

func TestEffectiveBudgets(t *testing.T) {
	if got := EffectiveInputBudget(200000, 100000); got != 200000-20000-13000 {
		t.Errorf("budget: %d", got)
	}
	if got := EffectiveInputBudget(8000, 8192); got != 4000 {
		t.Errorf("floor: %d", got)
	}
	if got := CompactionThreshold(100000, 80); got != 80000 {
		t.Errorf("threshold: %d", got)
	}
	if got := CompactionThreshold(100000, 150); got != 100000 {
		t.Errorf("clamped percent: %d", got)
	}
	if got := CompactionThreshold(100000, 0); got != 0 {
		t.Errorf("disabled: %d", got)
	}
	if got := CompactionThreshold(5000, 10); got != 4000 {
		t.Errorf("minimum: %d", got)
	}
}
