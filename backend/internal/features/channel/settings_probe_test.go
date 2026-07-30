package channel

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const setPath = "/api/v1/admin/llm/settings"

func TestSettingsSeedAndList(t *testing.T) {
	s := newSuite(t)
	list := decodeData[[]SettingView](t, s.do("GET", setPath, nil))
	var keys []string
	for _, v := range list {
		keys = append(keys, v.Key)
	}
	if strings.Join(keys, ",") != "circuit_breaker.error_classification,circuit_breaker.defaults,rate_limit.defaults,load_balance.defaults" {
		t.Fatalf("keys: %v", keys)
	}
	if list[0].Description != "Error type classification for circuit breaking" || list[1].Description != "Default circuit breaker settings" || list[2].Description != "Default wait settings for retries after rate limiting" || list[3].Description != "Default load balancing settings" {
		t.Fatalf("descriptions: %+v", list)
	}
	if list[2].Value != `{"backoff_base_sec":5,"backoff_max_sec":60,"backoff_multiplier":2}` || list[3].Value != `{"algorithm":"weighted_random"}` {
		t.Fatalf("values: %+v", list)
	}
	if !strings.Contains(list[1].Value, `"enabled":false`) || !strings.Contains(list[1].Value, `"upstream_window_min":5`) {
		t.Fatalf("breaker default: %s", list[1].Value)
	}
	var rows []settingRow
	s.env.DB.Find(&rows)
	for _, r := range rows {
		if r.Namespace != "llm" || r.ValueType != "json" {
			t.Fatalf("row: %+v", r)
		}
	}
	// Foreign namespaces are invisible and their keys cannot be edited here.
	s.env.DB.Create(&settingRow{Namespace: "chat", Key: "other", Value: "1", ValueType: "int"})
	if got := decodeData[[]SettingView](t, s.do("GET", setPath, nil)); len(got) != 4 {
		t.Fatalf("namespace leak: %d", len(got))
	}
	s.expect(s.do("PATCH", setPath+"/other", map[string]string{"value": "2"}), 404, "settings.not_found")
	// Seeding never overwrites an edited value.
	s.ok("PATCH", setPath+"/rate_limit.defaults", map[string]string{"value": `{"backoff_base_sec":9}`})
	if err := seedSettings(bg, s.env.DB); err != nil {
		t.Fatal(err)
	}
	if got := decodeData[[]SettingView](t, s.do("GET", setPath, nil)); got[2].Value != `{"backoff_base_sec":9}` {
		t.Fatalf("seed overwrote an edit: %s", got[2].Value)
	}
}

func TestSettingsPatch(t *testing.T) {
	s := newSuite(t)
	patch := func(key, value string) (int, string) {
		w := s.do("PATCH", setPath+"/"+key, map[string]string{"value": value})
		return w.Code, errCode(s, w)
	}
	if st, code := patch("circuit_breaker.defaults", `{"enabled":true,"model_failure_threshold":-1}`); st != 400 || code != "config.invalid_json" {
		t.Errorf("negative: %d %s", st, code)
	}
	if st, code := patch("circuit_breaker.defaults", `{"enabled":true,"model_failure_threshold":null}`); st != 400 || code != "config.invalid_json" {
		t.Errorf("null: %d %s", st, code)
	}
	for _, bad := range []string{`[]`, `"x"`, `{`, `{"enabled":"yes"}`, `{"upstream_threshold_logic":"xor"}`, `{"model_duration_min":99999999999}`, `{"model_window_min":1.5}`, `{"model_duration_min":"3"}`} {
		if st, code := patch("circuit_breaker.defaults", bad); st != 400 || code != "config.invalid_json" {
			t.Errorf("%s: %d %s", bad, st, code)
		}
	}
	if st, code := patch("rate_limit.defaults", `{`); st != 400 || code != "config.invalid_json" {
		t.Errorf("invalid json: %d %s", st, code)
	}
	if st, code := patch("no.such.key", `{}`); st != 404 || code != "settings.not_found" {
		t.Errorf("unknown: %d %s", st, code)
	}
	w := s.do("PATCH", setPath+"/rate_limit.defaults", map[string]string{"value": ""})
	s.expect(w, 400, "request.invalid_body")
	w = s.do("PATCH", setPath+"/rate_limit.defaults", map[string]any{})
	s.expect(w, 400, "request.invalid_body")

	// {"enabled":true} is stored exactly as submitted; missing numbers resolve to defaults on read.
	v := decodeData[SettingView](t, s.do("PATCH", setPath+"/circuit_breaker.defaults", map[string]string{"value": `  {"enabled":true}  `}))
	if v.Key != "circuit_breaker.defaults" || v.Value != `{"enabled":true}` || v.ID == 0 || v.Description != "Default circuit breaker settings" {
		t.Fatalf("patched: %+v", v)
	}
	def, err := parseBreakerDefaults(v.Value)
	if err != nil || !def.Enabled || def.ModelFailureThreshold != 5 || def.UpstreamDurationMin != 30 || def.UpstreamThresholdLogic != "or" {
		t.Fatalf("resolved defaults: %+v %v", def, err)
	}
	if !s.svc.BreakerEnabled(bg) {
		t.Fatal("the local cache must refresh immediately")
	}
	// A different number is honored, zero keeps the built-in.
	s.ok("PATCH", setPath+"/circuit_breaker.defaults", map[string]string{"value": `{"enabled":true,"model_failure_threshold":9,"model_window_min":0,"upstream_threshold_logic":"and"}`})
	d := s.svc.breakerSettings(bg)
	if d.ModelFailureThreshold != 9 || d.ModelWindowMin != 3 || d.UpstreamThresholdLogic != "and" {
		t.Fatalf("effective: %+v", d)
	}
	// Disabling again.
	s.ok("PATCH", setPath+"/circuit_breaker.defaults", map[string]string{"value": `{"enabled":false}`})
	if s.svc.BreakerEnabled(bg) {
		t.Fatal("disabled")
	}
}

func TestBreakerStateClearedOnToggle(t *testing.T) {
	s := newSuite(t)
	up, row := s.mkChat("tog", "tog-model")
	bind := bindingPrefix(up.ID, row.BindingCode)
	_ = s.svc.circuits.openManual(bg, upstreamPrefix(up.ID))
	_ = s.svc.circuits.openManual(bg, bind)
	_ = s.svc.circuits.setMeta(bg, upstreamPrefix(up.ID), "last_error", "kept")
	_, _ = s.svc.circuits.recordBackoff(bg, up.ID, row.RouteID, defaultBackoff, 0)
	// Enabling clears all circuit state first.
	s.setBreaker(`{"enabled":true}`)
	if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(up.ID)); o {
		t.Fatal("upstream circuit must be cleared on enable")
	}
	if o, _, _ := s.svc.circuits.peekOpen(bg, bind); o {
		t.Fatal("binding circuit must be cleared on enable")
	}
	// Rate-limit back-off keys survive the sweep.
	if d, _ := s.svc.circuits.backoffRemaining(bg, up.ID, row.RouteID); d <= 0 {
		t.Fatal("back-off must survive")
	}
	// Disabling also clears.
	_ = s.svc.circuits.openManual(bg, upstreamPrefix(up.ID))
	s.setBreaker(`{"enabled":false}`)
	if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(up.ID)); o {
		t.Fatal("state cleared on disable")
	}
	// Re-saving enabled -> enabled does not clear.
	s.setBreaker(`{"enabled":true}`)
	_ = s.svc.circuits.openManual(bg, upstreamPrefix(up.ID))
	s.setBreaker(`{"enabled":true,"model_failure_threshold":7}`)
	if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(up.ID)); !o {
		t.Fatal("an unchanged flag must not clear state")
	}
	// A stored invalid previous value counts as disabled and can be repaired.
	s.env.DB.Model(&settingRow{}).Where("\"key\" = ?", "circuit_breaker.defaults").Update("value", `{"enabled":true,"model_failure_threshold":-5}`)
	s.ok("PATCH", setPath+"/circuit_breaker.defaults", map[string]string{"value": `{"enabled":true}`})
	if o, _, _ := s.svc.circuits.peekOpen(bg, upstreamPrefix(up.ID)); o {
		t.Fatal("repair from an invalid document counts as a disabled -> enabled transition")
	}
}

func TestBreakerSettingsCaching(t *testing.T) {
	s := newSuite(t)
	set := func(v string) {
		s.env.DB.Model(&settingRow{}).Where("\"key\" = ?", "circuit_breaker.defaults").Update("value", v)
	}
	s.setBreaker(`{"enabled":true}`) // primes the cache
	set(`{"enabled":false}`)
	if !s.svc.BreakerEnabled(bg) {
		t.Fatal("cached for 5 seconds")
	}
	s.env.Clock.Advance(6 * time.Second)
	if s.svc.BreakerEnabled(bg) {
		t.Fatal("cache expired")
	}
	// A read failure keeps the last good value and retries after a second.
	set(`{"enabled":true}`)
	s.env.Clock.Advance(6 * time.Second)
	if !s.svc.BreakerEnabled(bg) {
		t.Fatal("reloaded")
	}
	set(`{"enabled":true,"model_failure_threshold":-1}`)
	s.env.Clock.Advance(6 * time.Second)
	if !s.svc.BreakerEnabled(bg) {
		t.Fatal("keeps the last known good value on an invalid document")
	}
	set(`{"enabled":false}`)
	s.env.Clock.Advance(2 * time.Second)
	if s.svc.BreakerEnabled(bg) {
		t.Fatal("retries after one second")
	}
	// Without a stored row: defaults, disabled.
	s.env.DB.Where("\"key\" = ?", "circuit_breaker.defaults").Delete(&settingRow{})
	s.env.Clock.Advance(6 * time.Second)
	if d := s.svc.breakerSettings(bg); d.Enabled || d.ModelFailureThreshold != 5 {
		t.Fatalf("missing key: %+v", d)
	}
}

func TestClassificationAndBackoffParsing(t *testing.T) {
	c := parseClassification(`{"circuit_errors":[],"rate_limit_errors":["429"]}`)
	if !listHas(c.CircuitErrors, "5xx") || !listHas(c.IgnoreErrors, "4xx") || !listHas(c.RateLimitErrors, "429") {
		t.Errorf("empty lists fall back per list: %+v", c)
	}
	c = parseClassification(`garbage`)
	if c.RateLimitErrors == nil || !listHas(c.RateLimitErrors, "429") {
		t.Errorf("garbage: %+v", c)
	}
	if !listHas([]string{" 4XX "}, "4xx") {
		t.Error("trim + case-insensitive")
	}
	b := parseBackoff(`{"backoff_base_sec":0,"backoff_max_sec":120,"backoff_multiplier":-1}`)
	if b.BaseSec != 5 || b.MaxSec != 120 || b.Multiplier != 2 {
		t.Errorf("per-field fallback: %+v", b)
	}
	if b = parseBackoff(`{`); b != defaultBackoff {
		t.Errorf("invalid json: %+v", b)
	}
	n := backoffConfig{BaseSec: 100, MaxSec: 10, Multiplier: 1}.normalized()
	if n.BaseSec != 10 || n.Multiplier != 2 {
		t.Errorf("normalized: %+v", n)
	}
}

// ---- probes -------------------------------------------------------------------------------------------

func (s *suite) probeSetup(protocol string) (UpstreamView, UpstreamModelRow) {
	up := s.mkUpstream("probe", "openai", nil)
	row := s.bind(up.ID, "probe-model", "probe-up", []string{protocol}, nil)
	return up, row
}

func (s *suite) probeRoute(up uint, route uint, body any) ProbeResult {
	s.t.Helper()
	return decodeData[ProbeResult](s.t, s.do("POST", fmt.Sprintf("%s/%d/models/%d/test", upPath, up, route), body))
}

func TestProbeSuccessRequestShape(t *testing.T) {
	s := newSuite(t)
	up, row := s.probeSetup(ProtoOpenAIChat)
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"headersJSON": `{"X-U":"1","X-Both":"u"}`, "readTimeoutMS": 90000})
	s.ok("POST", fmt.Sprintf("%s/%d/models", upPath, up.ID), map[string]any{"platformModelName": "probe-model", "upstreamModelName": "probe-up",
		"protocols": []string{ProtoOpenAIChat}, "headersJSON": `{"X-Both":"r"}`})
	res := s.probeRoute(up.ID, row.RouteID, nil)
	if !res.Success || res.Status != "success" || res.ErrorCode != "" || res.Protocol != ProtoOpenAIChat || res.Endpoint != "chat_completions" ||
		res.PlatformModelName != "probe-model" || res.UpstreamName != "probe" || res.UpstreamModelName != "probe-up" || res.RouteID != row.RouteID ||
		res.BindingCode != row.BindingCode || res.UpstreamID != up.ID || res.PlatformModelID != row.PlatformModelID {
		t.Fatalf("result: %+v", res)
	}
	s.client.mu.Lock()
	call := s.client.probes[0]
	s.client.mu.Unlock()
	if call.Protocol != ProtoOpenAIChat || call.APIKey != "sk-probe-abcdefgh" || call.ReadTimeout != 30*time.Second || call.ConnectTimeout != 10*time.Second ||
		!strings.Contains(call.HeadersJSON, `"X-Both":"r"`) || !strings.Contains(call.HeadersJSON, `"X-U":"1"`) {
		t.Fatalf("call: %+v", call)
	}
	// The probe is read-only: no data changes and no circuit/back-off touched.
	var um UpstreamModel
	s.env.DB.First(&um, row.ID)
	if d, _ := s.svc.circuits.backoffRemaining(bg, up.ID, row.RouteID); d != 0 {
		t.Fatal("probes never touch back-off")
	}
	// The request the adapter receives.
	var seen ProbeRequest
	s.client.probeFn = func(_ UpstreamCall, req ProbeRequest) (*ProbeExchange, error) {
		seen = req
		return &ProbeExchange{}, nil
	}
	s.probeRoute(up.ID, row.RouteID, nil)
	if seen.Prompt != "Reply with OK." || seen.MaxTokens != 1 || seen.Temperature != 0 || seen.UpstreamModel != "probe-up" || seen.Task != "chat" {
		t.Fatalf("request: %+v", seen)
	}
}

func TestProbeUsesFirstActiveKeyNotStrategy(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("keys", "openai", map[string]any{"apiKeys": `{"strategy":"round_robin","keys":[{"key":"off-key-0000","status":"disabled"},{"key":"first-active-1"},{"key":"second-active-2"}]}`})
	row := s.bind(up.ID, "keys-model", "keys-up", []string{ProtoOpenAIChat}, nil)
	for i := 0; i < 3; i++ {
		s.probeRoute(up.ID, row.RouteID, nil)
	}
	for _, c := range s.client.probes {
		if c.APIKey != "first-active-1" {
			t.Fatalf("key: %s", c.APIKey)
		}
	}
}

func TestProbeLocalChecks(t *testing.T) {
	s := newSuite(t)
	up, row := s.probeSetup(ProtoOpenAIChat)
	check := func(label string, wantStatus, wantCode string, success bool) ProbeResult {
		t.Helper()
		res := s.probeRoute(up.ID, row.RouteID, nil)
		if res.Status != wantStatus || res.ErrorCode != wantCode || res.Success != success {
			t.Fatalf("%s: %+v", label, res)
		}
		return res
	}
	calls := func() int { s.client.mu.Lock(); defer s.client.mu.Unlock(); return len(s.client.probes) }

	// Unsupported media protocol: not an error but unsupported, with no network call.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", row.RouteID).Update("protocol", ProtoOpenAIImageGen)
	s.env.DB.Model(&PlatformModel{}).Where("id = ?", row.PlatformModelID).Update("kinds_json", `["image_gen"]`)
	res := check("media protocol", "unsupported", "unsupported_protocol", false)
	if res.ErrorMessage != "this protocol requires media input or may create billable media output" || res.Endpoint != "image_generations" {
		t.Fatalf("media message: %+v", res)
	}
	// Known but not implemented, and blank protocols fail.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", row.RouteID).Update("protocol", ProtoOpenAIVideoGen)
	res = check("not implemented", "failed", "unsupported_protocol", false)
	if res.ErrorMessage != "route protocol is not supported" {
		t.Fatalf("message: %+v", res)
	}
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", row.RouteID).Update("protocol", "")
	check("blank protocol", "failed", "unsupported_protocol", false)
	// A protocol that cannot serve the task type.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", row.RouteID).Update("protocol", ProtoOpenAIChat)
	res = s.probeRoute(up.ID, row.RouteID, map[string]any{"taskType": "image_generation"})
	if res.ErrorCode != "protocol_mismatch" || res.ErrorMessage != "route protocol does not match the model type" || res.Status != "failed" {
		t.Fatalf("mismatch: %+v", res)
	}
	s.env.DB.Model(&PlatformModel{}).Where("id = ?", row.PlatformModelID).Update("kinds_json", `["chat"]`)
	// Empty upstream model name.
	s.env.DB.Model(&UpstreamModel{}).Where("id = ?", row.ID).Update("upstream_model_name", "  ")
	check("empty model", "failed", "model_not_found", false)
	s.env.DB.Model(&UpstreamModel{}).Where("id = ?", row.ID).Update("upstream_model_name", "probe-up")
	// Unsafe base URL.
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("base_url", "http://169.254.169.254")
	res = check("unsafe url", "failed", "config_invalid", false)
	if res.ErrorMessage != "upstream base url is invalid" {
		t.Fatalf("message: %+v", res)
	}
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("base_url", "https://ok.example.com")
	// Undecryptable and key-less configurations.
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", "bad")
	check("undecryptable", "failed", "config_invalid", false)
	enc, _ := s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "k", Status: "disabled"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", enc)
	check("no active key", "failed", "no_active_key", false)
	if calls() != 0 {
		t.Fatalf("local checks must happen before any network call, got %d calls", calls())
	}
	enc, _ = s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "good-key-1"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Update("api_keys_enc", enc)
	check("recovered", "success", "", true)
	if calls() != 1 {
		t.Fatalf("calls: %d", calls())
	}
	// An invalid task type is a body error; an empty body is fine.
	s.expect(s.do("POST", fmt.Sprintf("%s/%d/models/%d/test", upPath, up.ID, row.RouteID), map[string]any{"taskType": "telepathy"}), 400, "request.invalid_body")
	s.expect(s.do("POST", fmt.Sprintf("%s/%d/models/%d/test", upPath, up.ID, row.RouteID), ""), 200, "")
}

func TestProbeUpstreamFailureClassification(t *testing.T) {
	s := newSuite(t)
	up, row := s.probeSetup(ProtoOpenAIChat)
	cases := []struct {
		name string
		err  error
		code string
		msg  string
		http int
	}{
		{"401", &UpstreamError{StatusCode: 401, Err: errors.New("bad sk-live-secret")}, "auth_failed", "upstream authentication failed", 401},
		{"403", &UpstreamError{StatusCode: 403}, "auth_failed", "upstream authentication failed", 403},
		{"404", &UpstreamError{StatusCode: 404}, "model_not_found", "upstream model or endpoint was not found", 404},
		{"408", &UpstreamError{StatusCode: 408}, "timeout", "upstream request timed out", 408},
		{"504", &UpstreamError{StatusCode: 504}, "timeout", "upstream request timed out", 504},
		{"429", &UpstreamError{StatusCode: 429}, "rate_limited", "upstream rate limit reached", 429},
		{"400", &UpstreamError{StatusCode: 400}, "request_invalid", "upstream rejected the test request", 400},
		{"422", &UpstreamError{StatusCode: 422}, "request_invalid", "upstream rejected the test request", 422},
		{"500", &UpstreamError{StatusCode: 500}, "upstream_unavailable", "upstream service is unavailable", 500},
		{"502", &UpstreamError{StatusCode: 502}, "upstream_unavailable", "upstream service is unavailable", 502},
		{"418", &UpstreamError{StatusCode: 418}, "upstream_request_failed", "upstream request failed", 418},
		{"deadline", context.DeadlineExceeded, "timeout", "upstream request timed out", 0},
		{"wrapped deadline", fmt.Errorf("dial: %w", context.DeadlineExceeded), "timeout", "upstream request timed out", 0},
		{"network", errors.New("connection refused"), "network_error", "upstream request failed", 0},
		{"unsupported adapter", ErrUnsupportedAdapter, "unsupported_protocol", "route protocol is not supported", 0},
	}
	for _, c := range cases {
		err := c.err
		s.client.probeFn = func(UpstreamCall, ProbeRequest) (*ProbeExchange, error) { return nil, err }
		res := s.probeRoute(up.ID, row.RouteID, nil)
		if res.Success || res.Status != "failed" || res.ErrorCode != c.code || res.ErrorMessage != c.msg || res.UpstreamStatusCode != c.http {
			t.Errorf("%s: %+v", c.name, res)
		}
		if strings.Contains(res.ErrorMessage, "sk-live-secret") {
			t.Errorf("%s: the upstream's text must never be returned", c.name)
		}
	}
	// 2xx with an unusable body.
	s.client.probeFn = func(UpstreamCall, ProbeRequest) (*ProbeExchange, error) {
		return &ProbeExchange{Incompatible: true}, nil
	}
	res := s.probeRoute(up.ID, row.RouteID, nil)
	if res.ErrorCode != "response_incompatible" || res.ErrorMessage != "upstream response format is incompatible" {
		t.Errorf("incompatible: %+v", res)
	}
	// No adapter at all.
	nc := newSuite(t, func(o *Options) { o.Client = nil })
	u2, r2 := nc.probeSetup(ProtoOpenAIChat)
	if res := nc.probeRoute(u2.ID, r2.RouteID, nil); res.Success || res.ErrorCode != "unsupported_protocol" {
		t.Errorf("no client: %+v", res)
	}
}

func TestProbeDebugRedaction(t *testing.T) {
	s := newSuite(t)
	up, row := s.probeSetup(ProtoOpenAIChat)
	key := "sk-probe-abcdefgh"
	long := strings.Repeat("x", 9000)
	dbg := &ProbeDebug{
		Request: ProbeDebugRequest{Method: "POST", Path: "/v1/chat/completions", Body: `{"key":"` + key + `","url":"https://api.probe.example.com/v1","host":"api.probe.example.com","pad":"` + long + `"}`,
			Headers: map[string]string{"Content-Type": "application/json", "Accept": "*/*", "Authorization": "Bearer " + key, "X-Other": "no"}},
		Response: ProbeDebugResponse{StatusCode: 401, Body: `echo ` + key + ` at api.probe.example.com`,
			Headers: map[string]string{"content-type": "text/plain", "Set-Cookie": "a=b", "X-Request-Id": "abc"}},
	}
	s.client.probeFn = func(UpstreamCall, ProbeRequest) (*ProbeExchange, error) {
		return nil, &UpstreamError{StatusCode: 401, Err: errors.New("denied"), Debug: dbg}
	}
	w := s.do("POST", fmt.Sprintf("%s/%d/models/%d/test", upPath, up.ID, row.RouteID), nil)
	body := w.Body.String()
	for _, secret := range []string{key, "api.probe.example.com"} {
		if strings.Contains(body, secret) {
			t.Fatalf("%q leaked: %s", secret, body)
		}
	}
	res := decodeData[ProbeResult](t, w)
	if res.Debug == nil || res.ErrorCode != "auth_failed" || res.UpstreamStatusCode != 401 {
		t.Fatalf("result: %+v", res)
	}
	d := res.Debug
	if d.Request.Method != "POST" || d.Request.Path != "/v1/chat/completions" || len(d.Request.Headers) != 2 ||
		d.Request.Headers["Content-Type"] != "application/json" || d.Request.Headers["Accept"] != "*/*" {
		t.Fatalf("request headers must be limited: %+v", d.Request)
	}
	if len(d.Response.Headers) != 1 || d.Response.Headers["content-type"] != "text/plain" || d.Response.StatusCode != 401 {
		t.Fatalf("response headers must be limited: %+v", d.Response)
	}
	if !strings.HasSuffix(d.Request.Body, "...[truncated]") || len([]rune(d.Request.Body)) != 8192+len("...[truncated]") {
		t.Fatalf("truncation: %d", len(d.Request.Body))
	}
	if !strings.Contains(d.Request.Body, "[redacted]") || d.Response.Body != "echo [redacted] at [redacted]" {
		t.Fatalf("redaction: %q / %q", d.Request.Body[:80], d.Response.Body)
	}
	// A successful exchange can carry a snapshot as well.
	s.client.probeFn = func(UpstreamCall, ProbeRequest) (*ProbeExchange, error) {
		return &ProbeExchange{Debug: &ProbeDebug{Response: ProbeDebugResponse{StatusCode: 200, Body: "ok " + key}}}, nil
	}
	ok := s.probeRoute(up.ID, row.RouteID, nil)
	if !ok.Success || ok.Debug == nil || ok.Debug.Response.Body != "ok [redacted]" {
		t.Fatalf("success debug: %+v", ok)
	}
}

func TestProbeTaskTypeDerivation(t *testing.T) {
	cases := []struct {
		kinds    []string
		protocol string
		want     string
	}{
		{[]string{"image_gen", "image_edit"}, ProtoOpenAIImageGen, TaskImageGeneration},
		{[]string{"image_edit"}, ProtoOpenAIImageEdit, TaskImageEdit},
		{[]string{"chat"}, ProtoOpenAIChat, TaskChat},
		{[]string{"image_gen", "image_edit"}, ProtoOpenAIImageEdit, TaskImageEdit},
		{nil, ProtoXAIVideoExtension, TaskVideoExtension},
		{[]string{"video_gen"}, ProtoXAIVideo, TaskVideoGeneration},
		{nil, ProtoOpenAIImageEdit, TaskImageEdit},
		{[]string{"video_gen"}, "unknown_protocol", TaskVideoGeneration},
		{nil, "", TaskChat},
	}
	for i, c := range cases {
		if got := deriveRouteTask(c.kinds, c.protocol); got != c.want {
			t.Errorf("case %d: got %s want %s", i, got, c.want)
		}
	}
}

func TestModelProbe(t *testing.T) {
	s := newSuite(t)
	u1 := s.mkUpstream("mp1", "openai", nil)
	u2 := s.mkUpstream("mp2", "openai", nil)
	m := s.mkModel("mp-model", nil)
	path := fmt.Sprintf("%s/%d/test", mdPath, m.ID)
	// No routes at all.
	res := decodeData[ProbeResult](t, s.do("POST", path, nil))
	if res.Success || res.Status != "failed" || res.ErrorCode != "route_not_found" || res.ErrorMessage != "no active route is available for this model" || res.PlatformModelID != m.ID {
		t.Fatalf("no routes: %+v", res)
	}
	s.expect(s.do("POST", mdPath+"/9999/test", nil), 404, "model.not_found")
	s.expect(s.do("POST", mdPath+"/9999/test-all", nil), 404, "model.not_found")
	s.expect(s.do("POST", mdPath+"/x/test", nil), 400, "model.invalid_id")

	r1 := s.bind(u1.ID, "mp-model", "a", []string{ProtoOpenAIChat}, map[string]any{"priority": 2})
	r2 := s.bind(u2.ID, "mp-model", "b", []string{ProtoOpenAIResponses}, map[string]any{"priority": 1})
	res = decodeData[ProbeResult](t, s.do("POST", path, nil))
	if !res.Success || res.RouteID != r2.RouteID {
		t.Fatalf("the first route in routing order is probed: %+v (want %d)", res, r2.RouteID)
	}
	// Not matching the task.
	res = decodeData[ProbeResult](t, s.do("POST", path, map[string]any{"taskType": "image_edit"}))
	if res.ErrorCode != "route_not_found" || res.ErrorMessage != "no active route matches the selected test type" {
		t.Fatalf("no match: %+v", res)
	}
	// First route fails locally: the next suitable one is tried.
	enc, _ := s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "k", Status: "disabled"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", u2.ID).Update("api_keys_enc", enc)
	res = decodeData[ProbeResult](t, s.do("POST", path, nil))
	if !res.Success || res.RouteID != r1.RouteID {
		t.Fatalf("fall to the next route: %+v", res)
	}
	// All fail locally: the first failure is returned.
	s.env.DB.Model(&Upstream{}).Where("id = ?", u1.ID).Update("api_keys_enc", enc)
	res = decodeData[ProbeResult](t, s.do("POST", path, nil))
	if res.Success || res.ErrorCode != "no_active_key" || res.RouteID != r2.RouteID {
		t.Fatalf("first local failure: %+v", res)
	}
	// Image model default task.
	img := s.mkModel("mp-image", map[string]any{"kindsJSON": `["chat","image_gen"]`})
	u3 := s.mkUpstream("mp3", "openai", nil)
	s.bind(u3.ID, img.PlatformModelName, "c", []string{ProtoOpenAIChat}, nil)
	res = decodeData[ProbeResult](t, s.do("POST", fmt.Sprintf("%s/%d/test", mdPath, img.ID), nil))
	if res.ErrorCode != "route_not_found" || res.ErrorMessage != "no active route matches the selected test type" {
		t.Fatalf("image models default to the image task: %+v", res)
	}
	res = decodeData[ProbeResult](t, s.do("POST", fmt.Sprintf("%s/%d/test", mdPath, img.ID), map[string]any{"taskType": "chat"}))
	if !res.Success {
		t.Fatalf("explicit chat: %+v", res)
	}
}

func TestModelProbeAll(t *testing.T) {
	s := newSuite(t)
	m := s.mkModel("pa-model", nil)
	path := fmt.Sprintf("%s/%d/test-all", mdPath, m.ID)
	res := decodeData[ProbeAllResult](t, s.do("POST", path, nil))
	if res.TotalCount != 1 || res.FailedCount != 1 || res.SuccessCount != 0 || res.Results[0].ErrorCode != "route_not_found" {
		t.Fatalf("no routes: %+v", res)
	}
	var rows []UpstreamModelRow
	for i := 0; i < 6; i++ {
		u := s.mkUpstream(fmt.Sprintf("pa%d", i), "openai", nil)
		rows = append(rows, s.bind(u.ID, "pa-model", fmt.Sprintf("m%d", i), []string{ProtoOpenAIChat}, map[string]any{"priority": i + 1}))
	}
	// A media route is reported as unsupported.
	um := UpstreamModel{UpstreamID: 1, BindingCode: "upm_media", UpstreamModelName: "media", Status: "active", Source: "manual", KindsJSON: `["chat"]`}
	s.env.DB.Create(&um)
	var running, peak int32
	release := make(chan struct{})
	s.client.probeFn = func(call UpstreamCall, _ ProbeRequest) (*ProbeExchange, error) {
		n := atomic.AddInt32(&running, 1)
		for {
			p := atomic.LoadInt32(&peak)
			if n <= p || atomic.CompareAndSwapInt32(&peak, p, n) {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt32(&running, -1)
		_ = release
		if strings.Contains(call.APIKey, "pa3") {
			return nil, &UpstreamError{StatusCode: 500}
		}
		return &ProbeExchange{}, nil
	}
	res = decodeData[ProbeAllResult](t, s.do("POST", path, nil))
	if res.TotalCount != 6 || res.SuccessCount != 5 || res.FailedCount != 1 || res.UnsupportedCount != 0 || len(res.Results) != 6 {
		t.Fatalf("summary: %+v", res)
	}
	for i, r := range res.Results {
		if r.RouteID != rows[i].RouteID {
			t.Fatalf("results must be in route order: %d -> %d", i, r.RouteID)
		}
	}
	if peak > 4 {
		t.Fatalf("at most 4 concurrent probes, saw %d", peak)
	}
	if peak < 2 {
		t.Logf("probes did not overlap (peak %d)", peak)
	}
	// Explicit task filtering and unsupported counting.
	s.env.DB.Model(&ModelRoute{}).Where("id = ?", rows[0].RouteID).Update("protocol", ProtoOpenAIImageGen)
	s.env.DB.Model(&PlatformModel{}).Where("id = ?", m.ID).Update("kinds_json", `["chat","image_gen"]`)
	res = decodeData[ProbeAllResult](t, s.do("POST", path, nil))
	if res.TotalCount != 6 || res.UnsupportedCount != 1 || res.FailedCount != 2 || res.SuccessCount != 4 {
		t.Fatalf("with a media route: %+v", res)
	}
	res = decodeData[ProbeAllResult](t, s.do("POST", path, map[string]any{"taskType": "chat"}))
	if res.TotalCount != 5 {
		t.Fatalf("explicit task filters routes: %+v", res)
	}
	res = decodeData[ProbeAllResult](t, s.do("POST", path, map[string]any{"taskType": "video_extension"}))
	if res.TotalCount != 1 || res.Results[0].ErrorMessage != "no active route matches the selected test type" {
		t.Fatalf("no match: %+v", res)
	}
	// A cancelled context aborts the whole call.
	cctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.svc.ProbeModelAll(cctx, m.ID, ""); err == nil {
		t.Fatal("cancellation must abort")
	}
}

func TestProbeRoutesAreRateLimited(t *testing.T) {
	s := newSuite(t)
	up, row := s.probeSetup(ProtoOpenAIChat)
	path := fmt.Sprintf("%s/%d/models/%d/test", upPath, up.ID, row.RouteID)
	var last int
	for i := 0; i < probeRatePerMinute+5; i++ {
		last = s.do("POST", path, nil).Code
		if last == http.StatusTooManyRequests {
			if i != probeRatePerMinute {
				t.Fatalf("limited at call %d, limit is %d", i, probeRatePerMinute)
			}
			return
		}
	}
	t.Fatalf("expected a 429 after %d calls, last status %d", probeRatePerMinute, last)
}
