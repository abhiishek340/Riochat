package channel

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// ---- paging ---------------------------------------------------------------------------

func TestPagingSurvivesAbsurdPageNumbers(t *testing.T) {
	s := newSuite(t)
	up, row := s.mkChat("pg", "pg-model")
	s.ok("POST", "/api/v1/admin/llm/model-vendors", map[string]any{"key": "pg-vendor", "name": "Pg"})
	s.ok("POST", "/api/v1/admin/llm/model-display-groups", map[string]any{"name": "PgGroup"})
	w := s.upload(pngBytes(8, 8))
	s.expect(w, 200, "")

	q := "?page=9223372036854775807&page_size=1000"
	paths := []string{
		upPath, fmt.Sprintf("%s/%d/models", upPath, up.ID), "/api/v1/admin/llm/models",
		fmt.Sprintf("/api/v1/admin/llm/models/%d/sources", row.PlatformModelID), "/api/v1/admin/llm/model-vendors",
		"/api/v1/admin/llm/model-display-groups", "/api/v1/admin/llm/icon-assets",
	}
	for _, p := range paths {
		w := s.do("GET", p+q, nil)
		s.expect(w, 200, "")
		pg := decodeData[page[map[string]any]](t, w)
		if pg.Results == nil {
			t.Errorf("%s: results must be an array even when empty", p)
		}
		if len(pg.Results) != 0 {
			t.Errorf("%s: a page past the end must be empty, got %d rows", p, len(pg.Results))
		}
	}
}

func TestPagingCircuitFilterPastTheEnd(t *testing.T) {
	s := newSuite(t)
	s.mkUpstream("pgc", "openai", nil)
	s.setBreaker(`{"enabled":true}`)
	w := s.do("GET", upPath+"?status=circuit&page=9223372036854775807&page_size=1000", nil)
	s.expect(w, 200, "")
	if pg := decodeData[page[map[string]any]](t, w); pg.Results == nil || len(pg.Results) != 0 {
		t.Fatalf("%+v", pg)
	}
}

func TestPageWindowBounds(t *testing.T) {
	cases := []struct{ page, size, n, from, to int }{
		{1, 20, 5, 0, 5}, {2, 20, 25, 20, 25}, {3, 10, 25, 20, 25}, {4, 10, 25, 25, 25},
		{0, 0, 3, 0, 1}, {int(^uint(0) >> 1), 1000, 7, 7, 7}, {2, int(^uint(0) >> 1), 7, 7, 7},
	}
	for _, c := range cases {
		if from, to := pageWindow(c.page, c.size, c.n); from != c.from || to != c.to {
			t.Errorf("pageWindow(%d,%d,%d) = %d,%d want %d,%d", c.page, c.size, c.n, from, to, c.from, c.to)
		}
	}
}

// ---- endpoint vocabulary --------------------------------------------------------------------

func TestEndpointKindsStayInTheDocumentedVocabulary(t *testing.T) {
	allowed := map[string]bool{"chat_completions": true, "responses": true, "image_generations": true, "image_edits": true,
		"images": true, "video_generations": true, "video_extensions": true, "interactions": true}
	for p := range knownProtocols {
		if got := protocolEndpoint(p); !allowed[got] {
			t.Errorf("%s: endpoint %q is outside the documented set", p, got)
		}
	}
}

// ---- manual circuit open has no probe phase -------------------------------------------------------

func TestManualCircuitClosesWithoutProbePhase(t *testing.T) {
	s := newSuite(t)
	u, row := s.mkChat("man", "man-model")
	s.setBreaker(`{"enabled":true}`)
	s.ok("POST", fmt.Sprintf("%s/%d/circuit/open", upPath, u.ID), nil)
	if _, err := s.resolve("man-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("open upstream: %v", err)
	}
	s.env.Clock.Advance(23 * time.Hour)
	if _, err := s.resolve("man-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("still open after 23h: %v", err)
	}
	s.env.Clock.Advance(time.Hour + time.Second)
	r, err := s.resolve("man-model")
	if err != nil {
		t.Fatalf("closed after 24h: %v", err)
	}
	if r.UpstreamProbeGranted || r.BindingProbeGranted {
		t.Fatalf("a manual open must close without a half-open probe: %+v", r)
	}

	// The same holds for a manually opened binding.
	s.ok("POST", fmt.Sprintf("%s/%d/models/%d/circuit/open", upPath, u.ID, row.RouteID), nil)
	if _, err := s.resolve("man-model"); !errors.Is(err, ErrRoutesUnavailable) {
		t.Fatalf("open binding: %v", err)
	}
	s.env.Clock.Advance(24*time.Hour + time.Second)
	r, err = s.resolve("man-model")
	if err != nil || r.UpstreamProbeGranted || r.BindingProbeGranted {
		t.Fatalf("binding closed without probe: %+v %v", r, err)
	}
}

// ---- fail-over advice for wrapped transport errors ------------------------------------------------------

func TestShouldFailOverLooksInsideStatuslessUpstreamErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"eof inside upstream error", &UpstreamError{Err: io.EOF}, true},
		{"deadline inside upstream error", &UpstreamError{Err: context.DeadlineExceeded}, true},
		{"cancel inside upstream error", &UpstreamError{Err: context.Canceled}, false},
		{"plain failure", &UpstreamError{Err: errors.New("bad payload")}, false},
		{"status wins", &UpstreamError{StatusCode: 400, Err: io.EOF}, false},
		{"status 502", &UpstreamError{StatusCode: 502, Err: errors.New("x")}, true},
	}
	for _, c := range cases {
		if got := ShouldFailOver(c.err); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// ---- failure bookkeeping ----------------------------------------------------------------------------------

func TestFailureMetadataIsRecordedBeforeClassification(t *testing.T) {
	s := newSuite(t)
	u, _ := s.mkChat("meta", "meta-model")
	r, err := s.resolve("meta-model")
	if err != nil {
		t.Fatal(err)
	}
	prefix := upstreamPrefix(u.ID)
	read := func(name string) string {
		raw, found, _ := s.svc.circuits.kv.Get(bg, prefix+":"+name)
		if !found {
			return ""
		}
		return string(raw)
	}
	s.svc.MarkFailure(bg, r, context.Canceled)
	if read("last_failure_at") == "" || read("last_error") != "upstream request failed" {
		t.Fatalf("a cancelled call still leaves its trace: %q %q", read("last_failure_at"), read("last_error"))
	}
	s.svc.MarkFailure(bg, r, failure(404))
	if read("last_error") != "upstream request failed with status 404" {
		t.Fatalf("ignored 4xx is still summarized: %q", read("last_error"))
	}
	s.svc.MarkFailure(bg, r, context.DeadlineExceeded)
	if read("last_error") != "upstream request timed out" {
		t.Fatalf("timeout summary: %q", read("last_error"))
	}
}

// ---- sync report ----------------------------------------------------------------------------------------------

func TestSyncReportsReactivatedModelsAsActive(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("rea", "openai", nil)
	s.client.models = []RemoteModel{{ID: "back", OwnedBy: "openai"}}
	s.addLocal(up.ID, "back", "sync", "inactive")
	res := decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if len(res.SyncedModels) != 1 {
		t.Fatalf("synced: %+v", res.SyncedModels)
	}
	m := res.SyncedModels[0]
	if !m.Reactivated || m.Status != StatusActive {
		t.Fatalf("a reactivated model is reported with its new status: %+v", m)
	}
}

// ---- key masking -------------------------------------------------------------------------------------------------

func TestMaskKeyCountsCharactersNotBytes(t *testing.T) {
	cases := map[string]string{
		// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
		"\u5bc6\u94a5\u5bc6\u94a5\u5bc6\u94a5\u5bc6\u94a5": "****", "\u5bc6\u94a5\u5bc6\u94a5\u5bc6\u94a5\u5bc6\u94a5\u5bc6": "\u5bc6\u94a5\u5bc6\u94a5****\u94a5\u5bc6\u94a5\u5bc6", "  abcdefgh  ": "****", "abcdefghi": "abcd****fghi",
	}
	for in, want := range cases {
		if got := maskKey(in); got != want {
			t.Errorf("maskKey(%q) = %q want %q", in, got, want)
		}
	}
}

// ---- source errors -----------------------------------------------------------------------------------------------------

func TestUpdateSourceNotFoundCodes(t *testing.T) {
	s := newSuite(t)
	_, a := s.mkChat("srcA", "src-model-a")
	_, b := s.mkChat("srcB", "src-model-b")
	// A route that does not exist at all, and a route of another model.
	s.expect(s.do("PATCH", fmt.Sprintf("%s/%d/sources/99999", mdPath, a.PlatformModelID), map[string]any{"priority": 2}), 404, "upstream_model.not_found")
	s.expect(s.do("PATCH", fmt.Sprintf("%s/%d/sources/%d", mdPath, a.PlatformModelID, b.RouteID), map[string]any{"priority": 2}), 404, "model.not_found")
	s.expect(s.do("PATCH", fmt.Sprintf("%s/99999/sources/%d", mdPath, a.RouteID), map[string]any{"priority": 2}), 404, "model.not_found")
}

// ---- group listings with subscription figures ---------------------------------------------------------------------------

type fakeSubscriptions struct{ users map[uint][]uint }

func (f fakeSubscriptions) SubscriptionGroupID(context.Context, uint, time.Time) (uint, bool, error) {
	return 0, false, nil
}

func (f fakeSubscriptions) SubscriptionGroupUserIDs(_ context.Context, g uint, _ time.Time) ([]uint, error) {
	return f.users[g], nil
}

func TestGroupListingCountsSubscriptionUsers(t *testing.T) {
	fs := fakeSubscriptions{users: map[uint][]uint{}}
	s := newSuite(t, func(o *Options) { o.Subscriptions = fs })
	g, err := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "Plan"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.svc.SetGroupUsers(bg, g.ID, []uint{1, 2, 3}); err != nil {
		t.Fatal(err)
	}
	fs.users[g.ID] = []uint{3, 4, 4, 5}
	list, err := s.svc.ListPermissionGroups(bg)
	if err != nil {
		t.Fatal(err)
	}
	var got *GroupSummary
	for i := range list {
		if list[i].ID == g.ID {
			got = &list[i]
		}
	}
	if got == nil {
		t.Fatal("group missing")
	}
	if got.ManualUserCount != 3 || got.SubscriptionUserCount != 3 || got.UserCount != 5 {
		t.Fatalf("counts: manual %d subscription %d total %d", got.ManualUserCount, got.SubscriptionUserCount, got.UserCount)
	}
}

// ---- upstream list with the circuit filter and the breaker off ------------------------------------------------------------------

func TestCircuitFilterWithBreakerOffIsAnEmptyArray(t *testing.T) {
	s := newSuite(t)
	s.mkUpstream("cf", "openai", nil)
	w := s.do("GET", upPath+"?status=circuit", nil)
	s.expect(w, 200, "")
	pg := decodeData[page[map[string]any]](t, w)
	if pg.Total != 0 || pg.Results == nil || len(pg.Results) != 0 {
		t.Fatalf("circuit filter with the breaker off: %+v", pg)
	}
}

// ---- path ids -------------------------------------------------------------------------------------------------------------------

func TestPathIDsAreValidatedPerParameter(t *testing.T) {
	s := newSuite(t)
	for _, c := range []struct{ method, path, code string }{
		{"PATCH", upPath + "/abc", "upstream.invalid_id"},
		{"DELETE", upPath + "/-1", "upstream.invalid_id"},
		{"POST", upPath + "/99999999999999999999999/circuit/open", "upstream.invalid_id"},
		{"GET", upPath + "/x/models", "upstream.invalid_id"},
		{"DELETE", upPath + "/1/models/zz", "route.invalid_id"},
		{"PATCH", upPath + "/1/models/zz/enable", "route.invalid_id"},
		{"PATCH", mdPath + "/abc", "model.invalid_id"},
		{"GET", mdPath + "/abc/sources", "model.invalid_id"},
		{"PATCH", mdPath + "/1/sources/abc", "route.invalid_id"},
		{"PATCH", "/api/v1/admin/llm/model-display-groups/abc", "model_display_group.invalid_id"},
		{"DELETE", "/api/v1/admin/llm/model-display-groups/-3", "model_display_group.invalid_id"},
	} {
		s.expect(s.do(c.method, c.path, map[string]any{}), 400, c.code)
	}
}

// ---- native tool definitions ---------------------------------------------------------------------------------------------

func TestNativeToolDefinitionsIncludeBuiltIns(t *testing.T) {
	s := newSuite(t, func(o *Options) {
		o.BuiltinNativeTools = []json.RawMessage{json.RawMessage(`{"name":"web_search","type":"builtin"}`), json.RawMessage(`{"name":"image","type":"builtin"}`)}
	})
	s.mkModel("tools-d", map[string]any{"capabilitiesJSON": `{"nativeTools":[{"name":"web_search","type":"model"},{"name":"code","type":"model"}]}`})
	defs, err := s.svc.NativeToolDefinitions(bg)
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, d := range defs {
		var x struct {
			Name, Type string
		}
		_ = json.Unmarshal(d, &x)
		got = append(got, x.Name+":"+x.Type)
	}
	if strings.Join(got, ",") != "web_search:builtin,image:builtin,code:model" {
		t.Fatalf("built-ins come first and win over duplicates: %v", got)
	}
}
