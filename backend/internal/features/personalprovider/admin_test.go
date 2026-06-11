package personalprovider

import (
	"fmt"
	"strings"
	"testing"

	"github.com/riochat/riochat/backend/internal/testkit"
)

const adm = "/admin/model-providers"

type adminList struct {
	Total   int64
	Results []AdminView
}

func (h *harness) adminList(query string) adminList {
	h.t.Helper()
	w := h.do("GET", adm+query, h.admin(99), nil)
	testkit.Expect(h.t, w, 200, "")
	var out adminList
	testkit.Decode(h.t, w, &out)
	return out
}

func decodeAffected(t *testing.T, h *harness, path string, body any) int {
	t.Helper()
	w := h.do("POST", adm+path, h.admin(99), body)
	testkit.Expect(t, w, 200, "")
	var out struct{ Affected int }
	testkit.Decode(t, w, &out)
	return out.Affected
}

func TestAdminRoutesRequireAdmin(t *testing.T) {
	h := newHarness(t).enable()
	testkit.Expect(t, h.do("GET", adm, h.user(1), nil), 403, "")
	testkit.Expect(t, h.do("GET", adm, nil, nil), 401, "")
	testkit.Expect(t, h.do("POST", adm+"/suspend", h.user(1), map[string]any{"ids": []string{"x"}, "suspended": true}), 403, "")
	testkit.Expect(t, h.do("POST", adm+"/batch-delete", h.user(1), map[string]any{"ids": []string{"x"}}), 403, "")
	testkit.Expect(t, h.do("POST", adm+"/suspend-host", h.user(1), map[string]any{"host": "x.com"}), 403, "")
}

func TestAdminListOwnersFiltersAndPaging(t *testing.T) {
	h := newHarness(t).enable()
	h.owners.byID[1] = Owner{PublicID: "own1pub", Username: "alice", DisplayName: "Alice A", Email: "alice@corp.test", AvatarURL: "avatars/a.png"}
	h.owners.byID[2] = Owner{Username: "bob"}
	a := h.create(1, map[string]any{"name": "Alpha"})
	b := h.create(2, map[string]any{"name": "Beta", "baseURL": "https://other.example.org/v1"})
	c := h.create(1, map[string]any{"name": "Gamma", "baseURL": "https://third.example.net", "apiKey": "sk-another-key-value"})

	all := h.adminList("")
	if all.Total != 3 || len(all.Results) != 3 {
		t.Fatalf("%+v", all)
	}
	if all.Results[0].ID != c.ID || all.Results[2].ID != a.ID {
		t.Fatalf("newest first expected: %v", all.Results)
	}
	first := all.Results[2]
	if first.OwnerUserID != 1 || first.OwnerLabel != "Alice A" || first.OwnerPublicID != "own1pub" || first.OwnerUsername != "alice" ||
		first.OwnerDisplayName != "Alice A" || first.OwnerEmail != "alice@corp.test" || first.OwnerAvatarURL != "avatars/a.png" {
		t.Fatalf("%+v", first)
	}
	if first.KeyHint != "sk-••••WXYZ" || first.ModelCount != 0 || first.LastCheckedAt == nil || first.Host != "api.example.com" {
		t.Fatalf("%+v", first)
	}
	if all.Results[1].OwnerLabel != "bob" {
		t.Fatalf("label falls back to username: %+v", all.Results[1])
	}

	// q matches name, host, and owner details.
	if r := h.adminList("?q=BETA"); r.Total != 1 || r.Results[0].ID != b.ID {
		t.Fatalf("q name: %+v", r)
	}
	if r := h.adminList("?q=other.example"); r.Total != 1 || r.Results[0].ID != b.ID {
		t.Fatalf("q host: %+v", r)
	}
	if r := h.adminList("?q=ALICE@corp"); r.Total != 2 {
		t.Fatalf("q owner email: %+v", r)
	}
	if r := h.adminList("?q=nomatch"); r.Total != 0 || r.Results == nil {
		t.Fatalf("q none: %+v", r)
	}
	if r := h.adminList("?host=OTHER.example.org"); r.Total != 1 || r.Results[0].ID != b.ID {
		t.Fatalf("host: %+v", r)
	}
	if r := h.adminList("?owner_user_id=1"); r.Total != 2 {
		t.Fatalf("owner: %+v", r)
	}
	if r := h.adminList("?status=suspended"); r.Total != 0 {
		t.Fatalf("status: %+v", r)
	}
	if r := h.adminList("?page=2&page_size=2"); r.Total != 3 || len(r.Results) != 1 || r.Results[0].ID != a.ID {
		t.Fatalf("paging: %+v", r)
	}
	testkit.Expect(t, h.do("GET", adm+"?owner_user_id=abc", h.admin(99), nil), 400, "request.invalid_query")
}

func TestAdminListOwnerLookupFailureIsTolerated(t *testing.T) {
	h := newHarness(t).enable()
	h.owners.byID[1] = Owner{Username: "alice"}
	h.create(1, nil)
	h.owners.fail = true
	r := h.adminList("")
	if r.Total != 1 || r.Results[0].OwnerLabel != "" || r.Results[0].OwnerUsername != "" {
		t.Fatalf("%+v", r)
	}
	// With the search failing, name/host matching still works.
	if r := h.adminList("?q=api.example"); r.Total != 1 {
		t.Fatalf("%+v", r)
	}
}

func TestAdminListDoesNotLeakKeys(t *testing.T) {
	h := newHarness(t).enable()
	h.create(1, nil)
	w := h.do("GET", adm, h.admin(99), nil)
	body := w.Body.String()
	if strings.Contains(body, "abcdefghij") || strings.Contains(strings.ToLower(body), "apikey") || strings.Contains(body, "api_key") {
		t.Fatalf("leak: %s", body)
	}
}

func TestAdminListWithRealOwnerDirectory(t *testing.T) {
	h := newHarness(t).enable()
	h.svc.owners = dbOwners{db: h.env.DB}
	if err := h.env.DB.Exec(`CREATE TABLE identity_users (id integer primary key, public_id text, username text, display_name text, email text, avatar_url text, deleted_at datetime)`).Error; err != nil {
		t.Fatal(err)
	}
	h.env.DB.Exec(`INSERT INTO identity_users (id, public_id, username, display_name, email, avatar_url) VALUES (1,'pub1','alice','Alice','alice@x.test','a.png')`)
	h.env.DB.Exec(`INSERT INTO identity_users (id, public_id, username, display_name, email, avatar_url, deleted_at) VALUES (2,'pub2','gone','Gone','gone@x.test','', '2026-01-01')`)
	h.create(1, nil)
	h.create(2, nil)
	r := h.adminList("")
	byOwner := map[uint]AdminView{}
	for _, v := range r.Results {
		byOwner[v.OwnerUserID] = v
	}
	if byOwner[1].OwnerLabel != "Alice" || byOwner[1].OwnerEmail != "alice@x.test" {
		t.Fatalf("%+v", byOwner[1])
	}
	if byOwner[2].OwnerLabel != "" {
		t.Fatalf("soft-deleted owner must not resolve: %+v", byOwner[2])
	}
	if r := h.adminList("?q=alice@x"); r.Total != 1 || r.Results[0].OwnerUserID != 1 {
		t.Fatalf("%+v", r)
	}
	if r := h.adminList("?q=gone@x"); r.Total != 0 {
		t.Fatalf("deleted owners are excluded from user matches: %+v", r)
	}
	if r := h.adminList("?q=%25"); r.Total != 0 {
		t.Fatalf("wildcards must be escaped: %+v", r)
	}
}

func TestAdminSuspendAndResume(t *testing.T) {
	h := newHarness(t).enable()
	a := h.create(1, nil)
	b := h.create(2, nil)
	testkit.Expect(t, h.do("PATCH", mp+"/"+a.ID, h.user(1), map[string]any{"enabled": false}), 200, "")

	n := decodeAffected(t, h, "/suspend", map[string]any{"ids": []string{" " + a.ID + " ", b.ID, a.ID, "", "  ", "unknownid1"}, "suspended": true})
	if n != 2 {
		t.Fatalf("affected %d", n)
	}
	for _, r := range h.rows() {
		if r.Status != "suspended" {
			t.Fatalf("%s %s", r.PublicID, r.Status)
		}
	}
	e := h.audit.all()
	last := e[len(e)-1]
	if last.Action != "personal_provider.admin_suspend" || last.Resource != "personal_providers" ||
		last.ResourceID != a.ID+","+b.ID+",unknownid1" || last.Detail["affected"] != float64(2) {
		t.Fatalf("%+v", last)
	}

	// Resume re-activates everything, even providers the user had disabled.
	n = decodeAffected(t, h, "/suspend", map[string]any{"ids": []string{a.ID}, "suspended": false})
	if n != 1 || h.rows()[0].Status != "active" {
		t.Fatalf("%d %+v", n, h.rows()[0])
	}
	e = h.audit.all()
	if e[len(e)-1].Action != "personal_provider.admin_resume" {
		t.Fatalf("%+v", e[len(e)-1])
	}
}

func TestAdminSuspendWorksWithFeatureOff(t *testing.T) {
	h := newHarness(t).enable()
	p := h.create(1, nil)
	h.set(SettingEnabled, "false")
	if n := decodeAffected(t, h, "/suspend", map[string]any{"ids": []string{p.ID}, "suspended": true}); n != 1 {
		t.Fatalf("%d", n)
	}
	h.adminList("")
}

func TestAdminBulkNormalization(t *testing.T) {
	h := newHarness(t).enable()
	a := h.create(1, nil)
	if n := decodeAffected(t, h, "/suspend", map[string]any{"ids": []string{"", "   "}, "suspended": true}); n != 0 {
		t.Fatalf("blank only: %d", n)
	}
	if n := decodeAffected(t, h, "/batch-delete", map[string]any{"ids": []string{" ", ""}}); n != 0 {
		t.Fatalf("blank only: %d", n)
	}
	// 501 entries fail request validation; 500 blanks/dups normalize down.
	many := make([]string, 501)
	for i := range many {
		many[i] = fmt.Sprintf("id%06d", i)
	}
	testkit.Expect(t, h.do("POST", adm+"/suspend", h.admin(99), map[string]any{"ids": many, "suspended": true}), 400, "request.invalid_body")
	testkit.Expect(t, h.do("POST", adm+"/batch-delete", h.admin(99), map[string]any{"ids": many}), 400, "request.invalid_body")
	dups := make([]string, 500)
	for i := range dups {
		dups[i] = a.ID
	}
	dups[499] = ""
	if n := decodeAffected(t, h, "/batch-delete", map[string]any{"ids": dups}); n != 1 {
		t.Fatalf("dups: %d", n)
	}
	// Missing / empty ids and a missing flag are invalid bodies.
	testkit.Expect(t, h.do("POST", adm+"/suspend", h.admin(99), map[string]any{"ids": []string{}, "suspended": true}), 400, "request.invalid_body")
	testkit.Expect(t, h.do("POST", adm+"/suspend", h.admin(99), map[string]any{"ids": []string{"x"}}), 400, "request.invalid_body")
	testkit.Expect(t, h.do("POST", adm+"/batch-delete", h.admin(99), map[string]any{}), 400, "request.invalid_body")
}

func TestAdminNormalizeIDsTooMany(t *testing.T) {
	ids := make([]string, 501)
	for i := range ids {
		ids[i] = fmt.Sprint("x", i)
	}
	if _, err := normalizeIDs(ids); err == nil {
		t.Fatal("expected error")
	}
	got, err := normalizeIDs([]string{" a ", "a", "", "b"})
	if err != nil || strings.Join(got, ",") != "a,b" {
		t.Fatalf("%v %v", got, err)
	}
}

func TestAdminSuspendHost(t *testing.T) {
	h := newHarness(t).enable()
	h.create(1, nil)
	h.create(2, nil)
	other := h.create(1, map[string]any{"baseURL": "https://other.example.org", "apiKey": "sk-another-key-value"})
	if n := decodeAffected(t, h, "/suspend-host", map[string]any{"host": "API.Example.com "}); n != 2 {
		t.Fatalf("affected %d", n)
	}
	for _, r := range h.rows() {
		want := "suspended"
		if r.PublicID == other.ID {
			want = "active"
		}
		if r.Status != want {
			t.Fatalf("%s: %s", r.Host, r.Status)
		}
	}
	e := h.audit.all()
	last := e[len(e)-1]
	if last.Action != "personal_provider.admin_suspend_host" || last.ResourceID != "api.example.com" || last.Detail["affected"] != float64(2) {
		t.Fatalf("%+v", last)
	}
	testkit.Expect(t, h.do("POST", adm+"/suspend-host", h.admin(99), map[string]any{"host": "  "}), 400, "personal_provider.invalid_base_url")
	testkit.Expect(t, h.do("POST", adm+"/suspend-host", h.admin(99), map[string]any{"host": ""}), 400, "request.invalid_body")
	testkit.Expect(t, h.do("POST", adm+"/suspend-host", h.admin(99), map[string]any{"host": strings.Repeat("a", 256)}), 400, "request.invalid_body")
	// Suspension does not touch the deny-list.
	var n int64
	h.env.DB.Model(&settingRow{}).Where("key = ?", SettingBlockedHosts).Count(&n)
	if n != 0 {
		t.Fatal("deny-list modified")
	}
}

func TestAdminBatchDelete(t *testing.T) {
	h := newHarness(t).enable()
	a := h.create(1, nil)
	b := h.create(2, nil)
	if n := decodeAffected(t, h, "/batch-delete", map[string]any{"ids": []string{a.ID, b.ID, "nope"}}); n != 2 {
		t.Fatalf("%d", n)
	}
	if len(h.rows()) != 0 {
		t.Fatal("rows remain")
	}
	e := h.audit.all()
	if last := e[len(e)-1]; last.Action != "personal_provider.admin_delete" || last.Detail["affected"] != float64(2) {
		t.Fatalf("%+v", last)
	}
}

func TestJoinIDsTruncates(t *testing.T) {
	ids := make([]string, 40)
	for i := range ids {
		ids[i] = strings.Repeat("a", 20)
	}
	if got := joinIDs(ids); len(got) != 255 {
		t.Fatalf("len %d", len(got))
	}
}

func TestPurgeUserTx(t *testing.T) {
	h := newHarness(t).enable()
	h.create(1, nil)
	h.create(1, map[string]any{"apiKey": "sk-another-key-value"})
	h.create(2, nil)
	if err := PurgeUserTx(h.env.DB, 1); err != nil {
		t.Fatal(err)
	}
	rows := h.rows()
	if len(rows) != 1 || rows[0].UserID != 2 {
		t.Fatalf("%+v", rows)
	}
}
