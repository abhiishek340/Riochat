package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

func TestCreateServerNormalizesInput(t *testing.T) {
	h := newHarness(t)
	row, err := h.svc.CreateServer(context.Background(), ServerInput{Name: "  Search  ", BaseURL: "https://mcp.example.com/mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if row.Name != "Search" || row.Status != StatusActive || row.HeadersJSON != "{}" || row.SortOrder != 100 ||
		row.ToolCount != 0 || row.LastSyncedAt != nil || row.AuthTokenEnc != "" {
		t.Fatalf("unexpected server: %+v", row.Server)
	}
	view, _ := json.Marshal(toServerView(*row))
	if strings.Contains(string(view), "authToken") || strings.Contains(string(view), "auth_token") {
		t.Fatalf("token field leaked: %s", view)
	}
	if !strings.Contains(string(view), `"lastSyncedAt":null`) {
		t.Fatalf("lastSyncedAt must be an explicit null: %s", view)
	}
}

func TestServerInputValidation(t *testing.T) {
	h := newHarness(t)
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	long := func(n int) string { return strings.Repeat("\u5b57", n) }
	cases := []struct {
		name string
		in   ServerInput
		want *apperr.Error
	}{
		{"scheme ftp", ServerInput{Name: "a", BaseURL: "ftp://x"}, ErrInvalidServerURL},
		{"metadata ip", ServerInput{Name: "a", BaseURL: "http://169.254.169.254/"}, ErrInvalidServerURL},
		{"alibaba metadata", ServerInput{Name: "a", BaseURL: "http://100.100.100.200/"}, ErrInvalidServerURL},
		{"ipv6 metadata", ServerInput{Name: "a", BaseURL: "http://[fd00:ec2::254]/"}, ErrInvalidServerURL},
		{"userinfo", ServerInput{Name: "a", BaseURL: "https://user:pw@host/"}, ErrInvalidServerURL},
		{"metadata host", ServerInput{Name: "a", BaseURL: "http://metadata.google.internal/"}, ErrInvalidServerURL},
		{"metadata host dot", ServerInput{Name: "a", BaseURL: "http://METADATA.google.internal./x"}, ErrInvalidServerURL},
		{"empty url", ServerInput{Name: "a", BaseURL: ""}, ErrInvalidServerURL},
		{"blank url", ServerInput{Name: "a", BaseURL: "   "}, ErrInvalidServerURL},
		{"unspecified", ServerInput{Name: "a", BaseURL: "http://0.0.0.0/"}, ErrInvalidServerURL},
		{"multicast", ServerInput{Name: "a", BaseURL: "http://224.0.0.1/"}, ErrInvalidServerURL},
		{"link local v6", ServerInput{Name: "a", BaseURL: "http://[fe80::1]/"}, ErrInvalidServerURL},
		{"no host", ServerInput{Name: "a", BaseURL: "http:///path"}, ErrInvalidServerURL},
		{"bad host", ServerInput{Name: "a", BaseURL: "http://bad host/"}, ErrInvalidServerURL},
		{"url too long", ServerInput{Name: "a", BaseURL: "https://example.com/" + strings.Repeat("a", 2100)}, ErrInvalidServerURL},
		{"empty name", ServerInput{Name: "  ", BaseURL: "https://a.example"}, ErrInvalidServerName},
		{"name 129", ServerInput{Name: long(129), BaseURL: "https://a.example"}, ErrInvalidServerName},
		{"status paused", ServerInput{Name: "a", BaseURL: "https://a.example", Status: "paused"}, ErrInvalidServerState},
		{"headers array", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `["a"]`}, ErrInvalidHeaders},
		{"headers number", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `{"a":1}`}, ErrInvalidHeaders},
		{"headers null value", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `{"a":null}`}, ErrInvalidHeaders},
		{"headers nested", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `{"a":{"b":"c"}}`}, ErrInvalidHeaders},
		{"headers null", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `null`}, ErrInvalidHeaders},
		{"headers junk", ServerInput{Name: "a", BaseURL: "https://a.example", HeadersJSON: `{`}, ErrInvalidHeaders},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := h.svc.CreateServer(context.Background(), tc.in)
			if !errors.Is(err, tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want.Code)
			}
			if statusFor(err) != 400 {
				t.Fatalf("status %d", statusFor(err))
			}
		})
	}
	var n int64
	h.db.Model(&Server{}).Count(&n)
	if n != 0 {
		t.Fatalf("invalid input must not create rows, have %d", n)
	}
}

func TestServerInputAccepts(t *testing.T) {
	h := newHarness(t)
	for _, in := range []ServerInput{
		{Name: strings.Repeat("\u5b57", 128), BaseURL: "https://a.example"},
		{Name: "a", BaseURL: "http://10.0.0.5:8080/mcp"},
		{Name: "b", BaseURL: "http://localhost:3000/mcp"},
		{Name: "c", BaseURL: "http://[::1]:3000/mcp", Status: " inactive "},
		{Name: "d", BaseURL: "  https://a.example/x  ", HeadersJSON: ` {"X-A":"b"} `},
	} {
		if _, err := h.svc.CreateServer(context.Background(), in); err != nil {
			t.Fatalf("%+v: %v", in, err)
		}
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[3].Status != StatusInactive || rows[4].BaseURL != "https://a.example/x" || rows[4].HeadersJSON != `{"X-A":"b"}` {
		t.Fatalf("normalization: %+v %+v", rows[3].Server, rows[4].Server)
	}
}

func TestServerLimit(t *testing.T) {
	h := newHarness(t)
	for i := 0; i < maxServers; i++ {
		if err := h.db.Create(&Server{Name: "s", BaseURL: "https://a.example", Status: StatusActive, SortOrder: (i + 1) * 100}).Error; err != nil {
			t.Fatal(err)
		}
	}
	_, err := h.svc.CreateServer(context.Background(), ServerInput{Name: "one more", BaseURL: "https://a.example"})
	if !errors.Is(err, ErrServerLimit) || statusFor(err) != 400 {
		t.Fatalf("got %v", err)
	}
}

func TestSortOrderIsMaxPlusStep(t *testing.T) {
	h := newHarness(t)
	a, b, c := h.server("a"), h.server("b"), h.server("c")
	if a.SortOrder != 100 || b.SortOrder != 200 || c.SortOrder != 300 {
		t.Fatalf("orders %d %d %d", a.SortOrder, b.SortOrder, c.SortOrder)
	}
	if err := h.svc.DeleteServer(context.Background(), b.ID); err != nil {
		t.Fatal(err)
	}
	if d := h.server("d"); d.SortOrder != 400 {
		t.Fatalf("after delete: %d", d.SortOrder)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if len(rows) != 3 || rows[0].Name != "a" || rows[2].Name != "d" {
		t.Fatalf("list order: %+v", rows)
	}
}

func TestListOrderTiesBreakById(t *testing.T) {
	h := newHarness(t)
	a, b := h.server("a"), h.server("b")
	h.db.Model(&Server{}).Where("id IN ?", []uint{a.ID, b.ID}).Update("sort_order", 5)
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].ID != a.ID || rows[1].ID != b.ID {
		t.Fatalf("tie order: %+v", rows)
	}
}

func TestRedactHeaders(t *testing.T) {
	cases := []struct{ in, want string }{
		{`{"X-Api-Key":"abc","X-Trace":"1","Cookie":"s"}`, `{"Cookie":"********","X-Api-Key":"********","X-Trace":"1"}`},
		{`{"x_auth_token":"t","Set-Cookie":"c","My_Secret":"z","APIKEY":"k","Proxy-Authorization":"p","Accept":"a"}`,
			`{"APIKEY":"********","Accept":"a","My_Secret":"********","Proxy-Authorization":"********","Set-Cookie":"********","x_auth_token":"********"}`},
		{`not json`, `{}`},
		{`null`, `{}`},
		{``, `{}`},
		{`{}`, `{}`},
	}
	for _, tc := range cases {
		if got := redactHeaders(tc.in); got != tc.want {
			t.Errorf("redact(%q) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestUpdateServerKeepsAndReplacesToken(t *testing.T) {
	h := newHarness(t)
	srv := h.server("a", func(in *ServerInput) { in.AuthToken = "  tok-1  " })
	if srv.AuthTokenEnc == "" || strings.Contains(srv.AuthTokenEnc, "tok-1") {
		t.Fatalf("token must be stored encrypted: %q", srv.AuthTokenEnc)
	}
	plain, _, err := h.ring.Decrypt(srv.AuthTokenEnc)
	if err != nil || plain != "tok-1" {
		t.Fatalf("decrypt: %q %v", plain, err)
	}
	upd, err := h.svc.UpdateServer(context.Background(), srv.ID, ServerInput{Name: "renamed", BaseURL: "https://other.example", HeadersJSON: `{"A":"b"}`, Status: "inactive"})
	if err != nil {
		t.Fatal(err)
	}
	if upd.AuthTokenEnc != srv.AuthTokenEnc || upd.Name != "renamed" || upd.BaseURL != "https://other.example" ||
		upd.HeadersJSON != `{"A":"b"}` || upd.Status != StatusInactive {
		t.Fatalf("update without token: %+v", upd.Server)
	}
	upd, err = h.svc.UpdateServer(context.Background(), srv.ID, ServerInput{Name: "renamed", BaseURL: "https://other.example", AuthToken: "tok-2"})
	if err != nil {
		t.Fatal(err)
	}
	if plain, _, _ := h.ring.Decrypt(upd.AuthTokenEnc); plain != "tok-2" {
		t.Fatalf("token not replaced: %q", plain)
	}
	// An update replaces headers and status wholesale.
	if upd.HeadersJSON != "{}" || upd.Status != StatusActive {
		t.Fatalf("wholesale replace: %+v", upd.Server)
	}
	if _, err := h.svc.UpdateServer(context.Background(), 9999, ServerInput{Name: "x", BaseURL: "https://a.example"}); err == nil || statusFor(err) != 500 {
		t.Fatalf("missing server must be a generic 500, got %v", err)
	}
}

func TestEmptyTokenNeedsNoKey(t *testing.T) {
	h := newHarness(t)
	h.svc.secrets = nil
	if _, err := h.svc.CreateServer(context.Background(), ServerInput{Name: "a", BaseURL: "https://a.example"}); err != nil {
		t.Fatalf("no token must not need a key: %v", err)
	}
	_, err := h.svc.CreateServer(context.Background(), ServerInput{Name: "b", BaseURL: "https://a.example", AuthToken: "x"})
	if err == nil || statusFor(err) != 500 {
		t.Fatalf("a token without a key must fail with 500, got %v", err)
	}
}

func TestDeleteServerRemovesToolsAndLinks(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(
		toolOf("a", "A", "", searchSchema),
		toolOf("b", "B", "", searchSchema),
	)
	keep := h.server("keep")
	doomed := h.server("doomed")
	h.sync(keep.ID, false)
	h.sync(doomed.ID, false)
	ta, tb := h.toolByName(doomed.ID, "a"), h.toolByName(keep.ID, "b")
	h.link(1, ta.ID)
	h.link(1, tb.ID)

	if err := h.svc.DeleteServer(context.Background(), doomed.ID); err != nil {
		t.Fatal(err)
	}
	var tools, servers int64
	h.db.Model(&Tool{}).Where("server_id = ?", doomed.ID).Count(&tools)
	h.db.Model(&Server{}).Where("id = ?", doomed.ID).Count(&servers)
	if tools != 0 || servers != 0 {
		t.Fatalf("remaining tools=%d servers=%d", tools, servers)
	}
	if h.linkCount() != 1 {
		t.Fatalf("only the link to the kept server's tool may remain, have %d", h.linkCount())
	}
	if err := h.svc.DeleteServer(context.Background(), doomed.ID); err != nil {
		t.Fatalf("deleting a missing server is a no-op success, got %v", err)
	}
}

func TestDeleteWithoutConversationTable(t *testing.T) {
	h := newHarness(t)
	h.db.Exec("DROP TABLE chat_conversation_project_mcp_tools")
	srv := h.server("a")
	if err := h.svc.DeleteServer(context.Background(), srv.ID); err != nil {
		t.Fatalf("delete must tolerate a missing link table: %v", err)
	}
}

func TestListCarriesLiveCounts(t *testing.T) {
	h := newHarness(t)
	h.fake.SetTools(toolOf("a", "A", "", nil), toolOf("b", "B", "", nil), toolOf("c", "C", "", nil))
	srv := h.server("a")
	h.sync(srv.ID, false)
	if _, err := h.svc.SetToolsStatus(context.Background(), srv.ID, []uint{h.toolByName(srv.ID, "b").ID}, StatusInactive); err != nil {
		t.Fatal(err)
	}
	rows, _ := h.svc.ListServers(context.Background())
	if rows[0].ToolCount != 3 || rows[0].ActiveToolCount != 2 || rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatalf("counts: %+v", rows[0])
	}
	// A legacy row with an unknown flag asks for confirmation.
	h.db.Model(&Tool{}).Where("name = ?", "a").Update("metadata_customized", nil)
	rows, _ = h.svc.ListServers(context.Background())
	if !rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatal("null flag must require confirmation")
	}
	h.db.Model(&Tool{}).Where("name = ?", "a").Update("metadata_customized", false)
	h.db.Model(&Tool{}).Where("name = ?", "c").Update("metadata_customized", true)
	rows, _ = h.svc.ListServers(context.Background())
	if !rows[0].RequiresToolMetadataSyncConfirmation {
		t.Fatal("customized flag must require confirmation")
	}
}

func TestListWithNoServersIsEmptyNotNil(t *testing.T) {
	h := newHarness(t)
	rows, err := h.svc.ListServers(context.Background())
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("%v %v", rows, err)
	}
}

func TestRuntimeSettingsNormalization(t *testing.T) {
	cases := []struct {
		in, want int
	}{{0, 32}, {-3, 32}, {500, 128}, {12, 12}, {1, 1}, {128, 128}, {129, 128}}
	for _, tc := range cases {
		s := RuntimeSettings{MaxSelectedToolsPerMessage: tc.in}.normalized()
		if s.MaxSelectedToolsPerMessage != tc.want {
			t.Errorf("selected %d -> %d, want %d", tc.in, s.MaxSelectedToolsPerMessage, tc.want)
		}
	}
	s := RuntimeSettings{ToolTimeoutSeconds: 0, MaxConcurrentCalls: 0, MaxLLMCallsPerRun: 1, MaxToolCallsPerRun: 0}.normalized()
	if s.ToolTimeoutSeconds != 10 || s.MaxConcurrentCalls != 8 || s.MaxLLMCallsPerRun != 5 || s.MaxToolCallsPerRun != 8 {
		t.Fatalf("fallbacks: %+v", s)
	}
	if s := (RuntimeSettings{ToolTimeoutSeconds: 99999}).normalized(); s.ToolTimeoutSeconds != 1800 {
		t.Fatalf("timeout cap: %d", s.ToolTimeoutSeconds)
	}
}
