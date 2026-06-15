package channel

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

const upPath = "/api/v1/admin/llm/upstreams"

func TestUpstreamCreateValidation(t *testing.T) {
	s := newSuite(t)
	good := func() map[string]any {
		return map[string]any{"name": "Alpha", "baseURL": "https://api.example.com/v1", "apiKeys": keysDoc("sk-abcdefghijk")}
	}
	cases := []struct {
		name   string
		mutate func(m map[string]any)
		status int
		code   string
	}{
		{"short name", func(m map[string]any) { m["name"] = "A" }, 400, "request.invalid_body"},
		{"not a url", func(m map[string]any) { m["baseURL"] = "not a url" }, 400, "request.invalid_body"},
		{"ftp scheme", func(m map[string]any) { m["baseURL"] = "ftp://example.com" }, 400, "request.invalid_upstream_base_url"},
		{"private address allowed", func(m map[string]any) { m["baseURL"] = "http://10.0.0.5:8080" }, 200, ""},
		{"metadata address", func(m map[string]any) { m["baseURL"] = "http://169.254.169.254" }, 400, "request.invalid_upstream_base_url"},
		{"userinfo", func(m map[string]any) { m["baseURL"] = "https://u:p@example.com" }, 400, "request.invalid_upstream_base_url"},
		{"disabled only key", func(m map[string]any) { m["apiKeys"] = `{"keys":[{"key":"k","status":"disabled"}]}` }, 400, "llm.invalid_api_keys_config"},
		{"no keys", func(m map[string]any) { m["apiKeys"] = `{"keys":[]}` }, 400, "llm.invalid_api_keys_config"},
		{"keys not json", func(m map[string]any) { m["apiKeys"] = `not-json` }, 400, "llm.invalid_api_keys_config"},
		{"missing keys", func(m map[string]any) { delete(m, "apiKeys") }, 400, "request.invalid_body"},
		{"bad compatible", func(m map[string]any) { m["compatible"] = "mystery" }, 400, "llm.invalid_compatible"},
		{"bad status", func(m map[string]any) { m["status"] = "paused" }, 400, "request.invalid_body"},
		{"bad logic", func(m map[string]any) { m["cbThresholdLogic"] = "xor" }, 400, "request.invalid_body"},
		{"bad headers", func(m map[string]any) { m["headersJSON"] = "{" }, 400, "llm.invalid_headers_config"},
		{"bad defaults json", func(m map[string]any) { m["protocolDefaultsJSON"] = "{" }, 400, "llm.invalid_protocol_defaults_config"},
		{"negative breaker", func(m map[string]any) { m["cbFailureThreshold"] = -1 }, 400, "request.invalid_body"},
	}
	for i, c := range cases {
		m := good()
		m["name"] = fmt.Sprintf("Alpha%d", i)
		c.mutate(m)
		w := s.do("POST", upPath, m)
		s.expect(w, c.status, c.code)
		if c.status == 400 && c.code == "request.invalid_body" {
			if env := decodeInto[map[string]any](t, json.RawMessage(w.Body.Bytes())); env["details"] == nil {
				t.Errorf("%s: invalid body must carry details", c.name)
			}
		}
	}
}

func TestUpstreamKeyMaskingAndSecrecy(t *testing.T) {
	s := newSuite(t)
	w := s.do("POST", upPath, map[string]any{"name": "Masked", "baseURL": "https://api.example.com",
		"apiKeys": `{"strategy":"failover","keys":[{"key":"sk-abcdefghijk"},{"key":"short","note":"n"}]}`})
	s.expect(w, 200, "")
	if strings.Contains(w.Body.String(), "sk-abcdefghijk") || strings.Contains(w.Body.String(), "\"short\"") {
		t.Fatalf("raw key leaked: %s", w.Body.String())
	}
	var out struct {
		Upstream UpstreamView `json:"upstream"`
	}
	data := decodeInto[json.RawMessage](t, json.RawMessage(w.Body.Bytes()))
	_ = data
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &env)
	_ = json.Unmarshal(env.Data, &out)
	items := out.Upstream.APIKeyItems
	if len(items) != 2 {
		t.Fatalf("items: %+v", items)
	}
	if items[0].KeyMasked != "sk-a****hijk" || items[0].Index != 0 || items[0].Status != "" {
		t.Fatalf("item0: %+v", items[0])
	}
	if items[1].KeyMasked != "****" || items[1].Note != "n" || items[1].Index != 1 {
		t.Fatalf("item1: %+v", items[1])
	}
	if items[0].ID == "" || items[0].ID == items[1].ID {
		t.Fatalf("ids: %+v", items)
	}
	var row Upstream
	if err := s.env.DB.First(&row, out.Upstream.ID).Error; err != nil {
		t.Fatal(err)
	}
	if strings.Contains(row.APIKeysEnc, "sk-abcdefghijk") || row.APIKeysEnc == "" {
		t.Fatalf("keys must be stored encrypted: %q", row.APIKeysEnc)
	}
	if strings.Contains(out.Upstream.APIKeysMasked, "sk-abcdefghijk") || !strings.Contains(out.Upstream.APIKeysMasked, "sk-a****hijk") {
		t.Fatalf("apiKeysMasked: %s", out.Upstream.APIKeysMasked)
	}
}

func TestUpstreamKeyEditing(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("keyed", "openai", map[string]any{"apiKeys": `{"strategy":"round_robin","keys":[{"key":"key-one-aaaa"},{"key":"key-two-bbbb"}]}`})
	id0, id1 := up.APIKeyItems[0].ID, up.APIKeyItems[1].ID
	path := fmt.Sprintf("%s/%d", upPath, up.ID)

	// Removing every key leaves nothing usable.
	w := s.do("PATCH", path, map[string]any{"deleteAPIKeyIDs": []string{id0, id1}})
	s.expect(w, 400, "llm.invalid_api_keys_config")
	// Unknown id.
	w = s.do("PATCH", path, map[string]any{"deleteAPIKeyIDs": []string{"deadbeefdeadbeef"}})
	s.expect(w, 400, "llm.invalid_api_keys_config")
	// Too short id fails validation first.
	w = s.do("PATCH", path, map[string]any{"deleteAPIKeyIDs": []string{"short"}})
	s.expect(w, 400, "request.invalid_body")

	// Delete one and add one: the new key is last, strategy kept.
	data := s.ok("PATCH", path, map[string]any{"deleteAPIKeyIDs": []string{id0, id0}, "addAPIKeys": `{"keys":[{"key":"key-new-cccc"}]}`})
	got := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if len(got.APIKeyItems) != 2 || got.APIKeyItems[0].ID == id0 || got.APIKeyItems[1].KeyMasked != "key-****cccc" {
		t.Fatalf("after edit: %+v", got.APIKeyItems)
	}
	if !strings.Contains(got.APIKeysMasked, "round_robin") {
		t.Fatalf("strategy must be preserved: %s", got.APIKeysMasked)
	}

	// A full replacement wins over add/delete.
	data = s.ok("PATCH", path, map[string]any{"apiKeys": keysDoc("replacement-key-zzzz"), "addAPIKeys": keysDoc("ignored")})
	got = decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if len(got.APIKeyItems) != 1 || got.APIKeyItems[0].KeyMasked != "repl****zzzz" {
		t.Fatalf("replacement: %+v", got.APIKeyItems)
	}

	// Identical keys at different positions have different ids.
	data = s.ok("PATCH", path, map[string]any{"apiKeys": keysDoc("same-key-12345", "same-key-12345")})
	got = decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if got.APIKeyItems[0].ID == got.APIKeyItems[1].ID {
		t.Fatal("ids must differ per position")
	}
	// The id is stable while key and position are unchanged.
	data = s.ok("PATCH", path, map[string]any{"name": "keyed2"})
	again := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if again.APIKeyItems[0].ID != got.APIKeyItems[0].ID {
		t.Fatal("key id must be stable")
	}
}

func TestUpstreamHeadersValidationAndRedaction(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("hdr", "openai", map[string]any{"headersJSON": `{"Authorization":"Bearer secret","X-Trace":"ok","x_api_key":"k","Cookie":"c","My-Token":"t"}`})
	path := fmt.Sprintf("%s/%d", upPath, up.ID)
	s.expect(s.do("PATCH", path, map[string]any{"headersJSON": "{"}), 400, "llm.invalid_headers_config")

	data := s.ok("GET", upPath+"?q=hdr", nil)
	list := decodeInto[page[UpstreamView]](t, data)
	if len(list.Results) != 1 {
		t.Fatalf("list: %+v", list)
	}
	var hdr map[string]string
	if err := json.Unmarshal([]byte(list.Results[0].HeadersJSON), &hdr); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"Authorization", "x_api_key", "Cookie", "My-Token"} {
		if hdr[k] != "********" {
			t.Errorf("%s must be redacted, got %q", k, hdr[k])
		}
	}
	if hdr["X-Trace"] != "ok" {
		t.Errorf("non-sensitive header changed: %v", hdr)
	}
	// The stored value stays intact.
	var row Upstream
	_ = s.env.DB.First(&row, up.ID)
	if !strings.Contains(row.HeadersJSON, "Bearer secret") {
		t.Fatalf("stored headers: %s", row.HeadersJSON)
	}
}

func TestUpstreamProtocolDefaults(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("anth", "anthropic", nil)
	if up.ProtocolDefaultsJSON != `{"audio":"anthropic_messages","chat":"anthropic_messages"}` &&
		up.ProtocolDefaultsJSON != `{"chat":"anthropic_messages","audio":"anthropic_messages"}` {
		t.Fatalf("defaults: %s", up.ProtocolDefaultsJSON)
	}
	var m map[string]string
	_ = json.Unmarshal([]byte(up.ProtocolDefaultsJSON), &m)
	if len(m) != 2 || m["chat"] != "anthropic_messages" || m["audio"] != "anthropic_messages" {
		t.Fatalf("defaults map: %v", m)
	}
	w := s.do("POST", upPath, map[string]any{"name": "Anth2", "baseURL": "https://a.example.com", "compatible": "anthropic",
		"apiKeys": keysDoc("k-12345678"), "protocolDefaultsJSON": `{"image_gen":"anthropic_messages"}`})
	s.expect(w, 400, "llm.invalid_adapter")
	w = s.do("POST", upPath, map[string]any{"name": "Anth3", "baseURL": "https://a.example.com", "compatible": "openai",
		"apiKeys": keysDoc("k-12345678"), "protocolDefaultsJSON": `{"chat":"bogus"}`})
	s.expect(w, 400, "llm.invalid_adapter")

	// Unknown kind keys are dropped, blank values dropped, values lower-cased.
	data := s.ok("POST", upPath, map[string]any{"name": "Anth4", "baseURL": "https://a.example.com", "compatible": "openai",
		"apiKeys": keysDoc("k-12345678"), "protocolDefaultsJSON": `{"chat":" OpenAI_Responses ","weird":"x","audio":""}`})
	got := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if got.ProtocolDefaultsJSON != `{"chat":"openai_responses"}` {
		t.Fatalf("normalized: %s", got.ProtocolDefaultsJSON)
	}

	// Update rules: only compatible changed keeps defaults.
	path := fmt.Sprintf("%s/%d", upPath, got.ID)
	data = s.ok("PATCH", path, map[string]any{"compatible": "xai"})
	if d := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream.ProtocolDefaultsJSON; d != `{"chat":"openai_responses"}` {
		t.Fatalf("defaults must be untouched: %s", d)
	}
	// Blank defaults with compatible present re-derive from the style.
	data = s.ok("PATCH", path, map[string]any{"compatible": "anthropic", "protocolDefaultsJSON": ""})
	d := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream.ProtocolDefaultsJSON
	_ = json.Unmarshal([]byte(d), &m)
	if m["chat"] != "anthropic_messages" {
		t.Fatalf("re-derived: %s", d)
	}
	// Blank without a style change yields {}.
	data = s.ok("PATCH", path, map[string]any{"protocolDefaultsJSON": ""})
	if d := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream.ProtocolDefaultsJSON; d != "{}" {
		t.Fatalf("blank defaults: %s", d)
	}
	// Legacy invalid entries are omitted when displayed.
	if err := s.env.DB.Model(&Upstream{}).Where("id = ?", got.ID).
		Update("protocol_defaults_json", `{"chat":"openai_chat_completions","image_gen":"anthropic_messages","bogus":"x"}`).Error; err != nil {
		t.Fatal(err)
	}
	v := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?q=anth4", nil))
	if v.Results[0].ProtocolDefaultsJSON != `{"chat":"openai_chat_completions"}` {
		t.Fatalf("display defaults: %s", v.Results[0].ProtocolDefaultsJSON)
	}
}

func TestUpstreamTimeoutsAndNormalization(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("tmo", "openai", nil)
	if up.ConnectTimeoutMS != 10000 || up.ReadTimeoutMS != 120000 || up.StreamIdleTimeoutMS != 60000 {
		t.Fatalf("defaults: %+v", up)
	}
	if up.Status != "active" || up.CBThresholdLogic != "or" || up.Compatible != "openai" {
		t.Fatalf("defaults: %+v", up)
	}
	data := s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"connectTimeoutMS": 2500, "readTimeoutMS": 0, "cbThresholdLogic": "and", "cbFailureThreshold": 7})
	got := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, data).Upstream
	if got.ConnectTimeoutMS != 2500 || got.ReadTimeoutMS != 120000 || got.CBThresholdLogic != "and" || got.CBFailureThreshold != 7 {
		t.Fatalf("patched: %+v", got)
	}
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"connectTimeoutMS": 0})
	v := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?q=tmo", nil)).Results[0]
	if v.ConnectTimeoutMS != 10000 {
		t.Fatalf("connect timeout must reset to default: %d", v.ConnectTimeoutMS)
	}
	// Create with explicit zero timeouts.
	d2 := s.mkUpstream("tmo2", "openai", map[string]any{"connectTimeoutMS": -5, "readTimeoutMS": 0})
	if d2.ConnectTimeoutMS != 10000 || d2.ReadTimeoutMS != 120000 {
		t.Fatalf("create defaults: %+v", d2)
	}
}

func TestUpstreamUpdateAndNotFound(t *testing.T) {
	s := newSuite(t)
	s.expect(s.do("PATCH", upPath+"/999", map[string]any{"name": "Nope"}), 404, "upstream.not_found")
	s.expect(s.do("DELETE", upPath+"/999", nil), 404, "upstream.not_found")
	s.expect(s.do("PATCH", upPath+"/abc", map[string]any{"name": "Nope"}), 400, "upstream.invalid_id")
	s.expect(s.do("PATCH", upPath+"/-1", map[string]any{"name": "Nope"}), 400, "upstream.invalid_id")
	s.expect(s.do("PATCH", upPath+"/99999999999999999999999", map[string]any{"name": "Nope"}), 400, "upstream.invalid_id")
	s.expect(s.do("POST", upPath+"/999/circuit/open", nil), 404, "upstream.not_found")
	s.expect(s.do("POST", upPath+"/x/circuit/reset", nil), 400, "upstream.invalid_id")

	up := s.mkUpstream("edit", "openai", nil)
	path := fmt.Sprintf("%s/%d", upPath, up.ID)
	got := decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, s.ok("PATCH", path, map[string]any{"name": "Renamed", "baseURL": "http://10.1.2.3:9000", "status": "inactive", "headersJSON": `{"A":"1"}`})).Upstream
	if got.Name != "Renamed" || got.BaseURL != "http://10.1.2.3:9000" || got.Status != "inactive" {
		t.Fatalf("patched: %+v", got)
	}
	s.expect(s.do("PATCH", path, map[string]any{"baseURL": "http://169.254.169.254"}), 400, "request.invalid_upstream_base_url")
	s.expect(s.do("PATCH", path, map[string]any{"compatible": "nope"}), 400, "llm.invalid_compatible")
	// An empty patch is accepted and changes nothing.
	got = decodeInto[struct {
		Upstream UpstreamView `json:"upstream"`
	}](t, s.ok("PATCH", path, map[string]any{})).Upstream
	if got.Name != "Renamed" {
		t.Fatalf("empty patch: %+v", got)
	}
}

func TestUpstreamListFiltersAndSorting(t *testing.T) {
	s := newSuite(t)
	a := s.mkUpstream("bravo", "openai", nil)
	b := s.mkUpstream("alpha", "anthropic", nil)
	c := s.mkUpstream("charlie", "openai", map[string]any{"status": "inactive", "baseURL": "https://special-host.example.org"})
	names := func(q string) []string {
		var out []string
		for _, u := range decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+q, nil)).Results {
			out = append(out, u.Name)
		}
		return out
	}
	eq := func(got []string, want ...string) {
		t.Helper()
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("got %v want %v", got, want)
		}
	}
	eq(names(""), "charlie", "alpha", "bravo") // id_desc default
	eq(names("?sort=id_asc"), "bravo", "alpha", "charlie")
	eq(names("?sort=name_asc"), "alpha", "bravo", "charlie")
	eq(names("?status=inactive"), "charlie")
	eq(names("?status=active&sort=name_asc"), "alpha", "bravo")
	eq(names("?status=whatever&sort=name_asc"), "alpha", "bravo", "charlie")
	eq(names("?compatible=anthropic"), "alpha")
	eq(names("?q=SPECIAL-HOST"), "charlie")
	eq(names("?q=ALPH"), "alpha")
	eq(names("?q=%25"), []string{}...) // wildcard characters are literals
	// updated_desc: touch the oldest.
	s.env.Clock.Advance(3600e9)
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, a.ID), map[string]any{"name": "bravo"})
	eq(names("?sort=updated_desc"), "bravo", "charlie", "alpha")
	// Paging.
	pg := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?page_size=2&page=2&sort=id_asc", nil))
	if pg.Total != 3 || len(pg.Results) != 1 || pg.Results[0].ID != c.ID {
		t.Fatalf("page 2: %+v", pg)
	}
	pg = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?page_size=abc&page=-3", nil))
	if len(pg.Results) != 3 {
		t.Fatalf("bad paging falls back to defaults: %+v", pg)
	}
	_ = b
}

func TestUpstreamCountsAndCascadeDelete(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("cnt", "openai", nil)
	s.bind(up.ID, "model-a", "up-a", []string{ProtoOpenAIChat}, nil)
	row := s.bind(up.ID, "model-b", "up-b", []string{ProtoOpenAIResponses}, nil)
	s.bind(up.ID, "gpt-image-1", "gpt-image-1", []string{}, nil) // dual protocol, counts once
	// An upstream model with no route does not count.
	if err := s.env.DB.Create(&UpstreamModel{UpstreamID: up.ID, BindingCode: "upm_x", UpstreamModelName: "orphan", Status: "active", Source: "sync"}).Error; err != nil {
		t.Fatal(err)
	}
	v := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath, nil)).Results[0]
	if v.ModelsCount != 3 || v.ActiveModelsCount != 3 {
		t.Fatalf("counts: %d/%d", v.ModelsCount, v.ActiveModelsCount)
	}
	// Disabling one route lowers the active count only.
	s.ok("PATCH", fmt.Sprintf("%s/%d/models/%d/disable", upPath, up.ID, row.RouteID), nil)
	v = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath, nil)).Results[0]
	if v.ModelsCount != 3 || v.ActiveModelsCount != 2 {
		t.Fatalf("counts after disable: %d/%d", v.ModelsCount, v.ActiveModelsCount)
	}
	// Inactive upstream: active count is zero.
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"status": "inactive"})
	v = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath, nil)).Results[0]
	if v.ModelsCount != 3 || v.ActiveModelsCount != 0 {
		t.Fatalf("inactive counts: %d/%d", v.ModelsCount, v.ActiveModelsCount)
	}

	// Group rule for this upstream is removed with it (scenario 50).
	pm := PermissionGroup{Name: "g", RateMultiplierPercent: 100}
	s.env.DB.Create(&pm)
	other := PermissionGroupModelRule{GroupID: pm.ID, RuleType: RuleUpstream, Value: "9999"}
	mine := PermissionGroupModelRule{GroupID: pm.ID, RuleType: RuleUpstream, Value: fmt.Sprint(up.ID)}
	s.env.DB.Create(&other)
	s.env.DB.Create(&mine)

	s.ok("DELETE", fmt.Sprintf("%s/%d", upPath, up.ID), nil)
	var n int64
	s.env.DB.Model(&ModelRoute{}).Count(&n)
	if n != 0 {
		t.Fatalf("routes must be removed: %d", n)
	}
	s.env.DB.Model(&UpstreamModel{}).Count(&n)
	if n != 0 {
		t.Fatalf("upstream models must be removed: %d", n)
	}
	s.env.DB.Model(&PlatformModel{}).Count(&n)
	if n != 3 {
		t.Fatalf("platform models must stay: %d", n)
	}
	var rules []PermissionGroupModelRule
	s.env.DB.Find(&rules)
	if len(rules) != 1 || rules[0].Value != "9999" {
		t.Fatalf("rules after delete: %+v", rules)
	}
}

func TestUpstreamBatchDelete(t *testing.T) {
	s := newSuite(t)
	a := s.mkUpstream("ba", "openai", nil)
	b := s.mkUpstream("bb", "openai", nil)
	s.expect(s.do("POST", upPath+"/batch-delete", map[string]any{"ids": []uint{}}), 400, "request.invalid_body")
	s.expect(s.do("POST", upPath+"/batch-delete", map[string]any{"ids": []uint{0}}), 400, "request.invalid_body")
	res := decodeInto[BatchResult](t, s.ok("POST", upPath+"/batch-delete", map[string]any{"ids": []uint{a.ID, 9999, b.ID}}))
	if res.Total != 3 || res.SuccessCount != 2 || res.NotFoundCount != 1 || res.FailedCount != 0 || len(res.Results) != 3 {
		t.Fatalf("batch: %+v", res)
	}
	if res.Results[0].Status != "deleted" || res.Results[1].Status != "not_found" || res.Results[1].ID != 9999 {
		t.Fatalf("batch results: %+v", res.Results)
	}
}

func TestUpstreamCircuitControls(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("circ", "openai", nil)
	path := fmt.Sprintf("%s/%d", upPath, up.ID)
	// Breaker disabled: manual open is refused (after the existence check).
	s.expect(s.do("POST", path+"/circuit/open", nil), 409, "llm.circuit_breaker_disabled")
	// Reset is always fine.
	data := s.ok("POST", path+"/circuit/reset", nil)
	if string(data) != `{"reset":true}` {
		t.Fatalf("reset: %s", data)
	}
	s.setBreaker(`{"enabled":true}`)
	s.ok("POST", path+"/circuit/open", nil)
	v := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath, nil)).Results[0]
	if !v.CircuitOpen || v.CircuitUntil == "" {
		t.Fatalf("circuit fields: %+v", v)
	}
	if want := fmt.Sprint(s.env.Clock.Now().Add(24 * 3600e9).Unix()); v.CircuitUntil != want {
		t.Fatalf("circuitUntil %s want %s", v.CircuitUntil, want)
	}
	// status=circuit filter.
	s.mkUpstream("healthy", "openai", nil)
	pg := decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?status=circuit", nil))
	if pg.Total != 1 || pg.Results[0].ID != up.ID {
		t.Fatalf("circuit list: %+v", pg)
	}
	s.ok("POST", path+"/circuit/reset", nil)
	pg = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?status=circuit", nil))
	if pg.Total != 0 || len(pg.Results) != 0 {
		t.Fatalf("after reset: %+v", pg)
	}
	// Breaker globally disabled: circuit status list is empty and fields are blank.
	s.ok("POST", path+"/circuit/open", nil)
	s.setBreaker(`{"enabled":false}`)
	pg = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?status=circuit", nil))
	if pg.Total != 0 {
		t.Fatalf("disabled breaker: %+v", pg)
	}
	v = decodeInto[page[UpstreamView]](t, s.ok("GET", upPath+"?q=circ", nil)).Results[0]
	if v.CircuitOpen || v.CircuitUntil != "" {
		t.Fatalf("circuit fields must be blank when disabled: %+v", v)
	}
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	s := newSuite(t)
	for _, tc := range []struct{ method, path string }{
		{"GET", upPath}, {"POST", upPath}, {"GET", "/api/v1/admin/llm/models"}, {"GET", "/api/v1/admin/llm/settings"},
		{"GET", "/api/v1/admin/llm/model-vendors"}, {"GET", "/api/v1/admin/llm/icon-assets"},
	} {
		w := s.env.Do(tc.method, tc.path, s.usr(5), map[string]any{})
		s.expect(w, 403, "")
		w = s.env.Do(tc.method, tc.path, nil, map[string]any{})
		s.expect(w, 401, "")
	}
}
