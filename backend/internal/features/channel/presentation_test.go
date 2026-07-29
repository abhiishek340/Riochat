package channel

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"image"
	"image/gif"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

const (
	vendPath = "/api/v1/admin/llm/model-vendors"
	grpPath  = "/api/v1/admin/llm/model-display-groups"
	icoPath  = "/api/v1/admin/llm/icon-assets"
)

func (s *suite) vendor(key string) (VendorView, bool) {
	for _, v := range decodeInto[page[VendorView]](s.t, s.ok("GET", vendPath+"?page_size=100", nil)).Results {
		if v.Key == key {
			return v, true
		}
	}
	return VendorView{}, false
}

func TestVendorCatalogSeedAndList(t *testing.T) {
	s := newSuite(t)
	pg := decodeInto[page[VendorView]](t, s.ok("GET", vendPath+"?page_size=100", nil))
	if pg.Total != 25 {
		t.Fatalf("builtin vendors: %d", pg.Total)
	}
	first, last := pg.Results[0], pg.Results[len(pg.Results)-1]
	if first.Key != "openai" || first.Name != "OpenAI" || first.Icon != "openai" || !first.BuiltIn || first.SortOrder != 100 {
		t.Fatalf("first: %+v", first)
	}
	if last.Key != "unknown" || last.Name != "Unknown" || last.Icon != "" || last.SortOrder != 2500 {
		t.Fatalf("last: %+v", last)
	}
	if v, _ := s.vendor("amazon"); v.Icon != "aws" {
		t.Fatalf("amazon: %+v", v)
	}
	if v, _ := s.vendor("xiaomi"); v.Icon != "xiaomimimo" || v.Name != "Xiaomi" {
		t.Fatalf("xiaomi: %+v", v)
	}
	// Filtering by key or name, case-insensitive.
	got := decodeInto[page[VendorView]](t, s.ok("GET", vendPath+"?q=GITHUB", nil))
	if got.Total != 1 || got.Results[0].Key != "copilot" {
		t.Fatalf("q name: %+v", got)
	}
	got = decodeInto[page[VendorView]](t, s.ok("GET", vendPath+"?q=iflytekcl", nil))
	if got.Total != 0 {
		t.Fatalf("q must not search icons: %+v", got)
	}
	got = decodeInto[page[VendorView]](t, s.ok("GET", vendPath+"?q=deep&page_size=1", nil))
	if got.Total != 1 || got.Results[0].Key != "deepseek" {
		t.Fatalf("q key: %+v", got)
	}
	// Seeding is idempotent and never overwrites administrator edits.
	s.ok("PATCH", vendPath+"/openai", map[string]any{"name": "Open AI", "icon": "custom"})
	if err := seedVendors(bg, s.env.DB); err != nil {
		t.Fatal(err)
	}
	if v, _ := s.vendor("openai"); v.Name != "Open AI" || v.Icon != "custom" || !v.BuiltIn {
		t.Fatalf("seed overwrote an edit: %+v", v)
	}
	// A vendor used by a model but missing from the catalog gets a row; an existing non-built-in row is promoted only for built-in keys.
	s.env.DB.Create(&PlatformModel{Name: "legacy", Vendor: "legacyco", KindsJSON: `["chat"]`, Status: "active", AccessScope: "public"})
	s.env.DB.Where("\"key\" = ?", "mistral").Delete(&ModelVendor{})
	s.env.DB.Create(&ModelVendor{Key: "mistral", Name: "Mistral custom", BuiltIn: false, SortOrder: 7})
	if err := seedVendors(bg, s.env.DB); err != nil {
		t.Fatal(err)
	}
	if v, ok := s.vendor("legacyco"); !ok || v.Name != "legacyco" || v.BuiltIn {
		t.Fatalf("legacy vendor: %+v %v", v, ok)
	}
	if v, _ := s.vendor("mistral"); !v.BuiltIn || v.Name != "Mistral custom" || v.SortOrder != 7 {
		t.Fatalf("promotion only: %+v", v)
	}
}

func TestVendorCreateUpdateDelete(t *testing.T) {
	s := newSuite(t)
	post := func(body map[string]any) (VendorView, int, string) {
		w := s.do("POST", vendPath, body)
		if w.Code != 200 {
			return VendorView{}, w.Code, errCode(s, w)
		}
		return decodeData[struct {
			Vendor VendorView `json:"vendor"`
		}](t, w).Vendor, 200, ""
	}
	if _, st, code := post(map[string]any{"key": "Claude", "name": "Claude Again"}); st != 409 || code != "model_vendor.already_exists" {
		t.Errorf("alias of builtin: %d %s", st, code)
	}
	v, st, _ := post(map[string]any{"key": "my_vendor-1", "name": " My Vendor ", "icon": "Mine"})
	if st != 200 || v.Key != "my_vendor-1" || v.BuiltIn || v.Name != "My Vendor" || v.Icon != "mine" || v.SortOrder != 2600 {
		t.Errorf("custom vendor: %+v (%d)", v, st)
	}
	for _, c := range []struct {
		body map[string]any
		st   int
		code string
	}{
		{map[string]any{"key": "-x", "name": "X"}, 400, "request.invalid_model_vendor"},
		{map[string]any{"key": "UPPER Case", "name": "X"}, 400, "request.invalid_model_vendor"},
		{map[string]any{"key": "ok-key", "name": "   "}, 400, "request.invalid_model_vendor"},
		{map[string]any{"key": "ok-key", "name": strings.Repeat("n", 65)}, 400, "request.invalid_body"},
		{map[string]any{"key": "", "name": "X"}, 400, "request.invalid_body"},
		{map[string]any{"key": "ok-key", "name": "X", "icon": "data:image/png;base64,AA"}, 400, "llm.model_icon_invalid"},
		{map[string]any{"key": "ok-key", "name": "X", "icon": "asset:ico_" + strings.Repeat("0", 32)}, 400, "llm.model_icon_asset_not_found"},
		{map[string]any{"key": "my_vendor-1", "name": "dup"}, 409, "model_vendor.already_exists"},
	} {
		if _, st, code := post(c.body); st != c.st || code != c.code {
			t.Errorf("%v: got %d %s want %d %s", c.body, st, code, c.st, c.code)
		}
	}
	// Display names are not unique.
	if _, st, _ := post(map[string]any{"key": "same-name", "name": "My Vendor"}); st != 200 {
		t.Errorf("duplicate display names are allowed: %d", st)
	}
	// Update: the key can never change.
	got := decodeInto[struct {
		Vendor VendorView `json:"vendor"`
	}](t, s.ok("PATCH", vendPath+"/my_vendor-1", map[string]any{"name": "Renamed", "icon": "", "key": "other"})).Vendor
	if got.Key != "my_vendor-1" || got.Name != "Renamed" || got.Icon != "" {
		t.Errorf("updated: %+v", got)
	}
	s.expect(s.do("PATCH", vendPath+"/nope", map[string]any{"name": "x"}), 404, "model_vendor.not_found")
	s.expect(s.do("PATCH", vendPath+"/my_vendor-1", map[string]any{"name": " "}), 400, "request.invalid_model_vendor")
	s.expect(s.do("PATCH", vendPath+"/my_vendor-1", map[string]any{"icon": "data:x"}), 400, "llm.model_icon_invalid")
	// An alias in the path resolves to the canonical key.
	if got := decodeInto[struct {
		Vendor VendorView `json:"vendor"`
	}](t, s.ok("PATCH", vendPath+"/claude", map[string]any{"icon": "claude-2"})).Vendor; got.Key != "anthropic" {
		t.Errorf("alias path: %+v", got)
	}

	// Delete rules.
	w := s.do("DELETE", vendPath+"/openai", nil)
	s.expect(w, 409, "llm.model_vendor_builtin")
	env := decodeInto[struct {
		Details VendorUsage `json:"details"`
	}](t, []byte(w.Body.String()))
	if env.Details.Reason != "built_in" {
		t.Errorf("builtin details: %+v", env.Details)
	}
	s.mkModel("used-1", map[string]any{"vendor": "same-name"})
	s.mkModel("used-2", map[string]any{"vendor": "same-name"})
	w = s.do("DELETE", vendPath+"/same-name", nil)
	s.expect(w, 409, "llm.model_vendor_in_use")
	env = decodeInto[struct {
		Details VendorUsage `json:"details"`
	}](t, []byte(w.Body.String()))
	if env.Details.Reason != "referenced_models" || env.Details.ReferenceCount != 2 || len(env.Details.Models) != 2 ||
		env.Details.Models[0].PlatformModelName != "used-1" {
		t.Errorf("in-use details: %+v", env.Details)
	}
	s.ok("DELETE", vendPath+"/my_vendor-1", nil)
	s.expect(s.do("DELETE", vendPath+"/my_vendor-1", nil), 404, "model_vendor.not_found")
	if _, ok := s.vendor("my_vendor-1"); ok {
		t.Error("vendor must be gone")
	}
	// The in-use preview is limited to 20 models.
	for i := 0; i < 25; i++ {
		s.mkModel(fmt.Sprintf("bulk-%02d", i), map[string]any{"vendor": "same-name"})
	}
	w = s.do("DELETE", vendPath+"/same-name", nil)
	env = decodeInto[struct {
		Details VendorUsage `json:"details"`
	}](t, []byte(w.Body.String()))
	if env.Details.ReferenceCount != 27 || len(env.Details.Models) != 20 {
		t.Errorf("preview limit: %+v", env.Details)
	}
}

func TestDisplayGroups(t *testing.T) {
	s := newSuite(t)
	m1 := s.mkModel("grp-a", nil)
	m2 := s.mkModel("grp-b", nil)
	m3 := s.mkModel("grp-c", nil)
	group := func(id uint) *uint {
		var m PlatformModel
		s.env.DB.First(&m, id)
		return m.DisplayGroupID
	}
	fast := s.mkGroup("Fast", []uint{m1.ID, m2.ID})
	if fast.SortOrder != 100 || fast.Name != "Fast" {
		t.Fatalf("group: %+v", fast)
	}
	if g := group(m1.ID); g == nil || *g != fast.ID {
		t.Fatal("members assigned")
	}
	slow := s.mkGroup("Slow", []uint{m2.ID})
	if slow.SortOrder != 200 {
		t.Fatalf("second group order: %+v", slow)
	}
	if g := group(m2.ID); g == nil || *g != slow.ID {
		t.Fatal("a model moves to the new group")
	}
	if g := group(m1.ID); g == nil || *g != fast.ID {
		t.Fatal("others stay")
	}
	// Admin model view carries the group.
	mv := decodeInto[page[ModelView]](t, s.ok("GET", mdPath+"?q=grp-b", nil)).Results[0]
	if mv.DisplayGroupID == nil || *mv.DisplayGroupID != slow.ID || mv.DisplayGroupName != "Slow" {
		t.Fatalf("model view: %+v", mv)
	}
	// Errors.
	s.expect(s.do("POST", grpPath, map[string]any{"name": "Fast"}), 409, "model_display_group.already_exists")
	s.expect(s.do("POST", grpPath, map[string]any{"name": "X", "modelIDs": []uint{0}}), 400, "request.invalid_model_display_group")
	s.expect(s.do("POST", grpPath, map[string]any{"name": "X", "modelIDs": []uint{999}}), 400, "request.invalid_model_display_group")
	s.expect(s.do("POST", grpPath, map[string]any{"name": "  "}), 400, "request.invalid_model_display_group")
	s.expect(s.do("POST", grpPath, map[string]any{"name": strings.Repeat("g", 65)}), 400, "request.invalid_body")
	s.expect(s.do("POST", grpPath, map[string]any{"name": "Icon", "icon": "javascript:alert(1)"}), 400, "llm.model_icon_invalid")
	s.expect(s.do("PATCH", grpPath+"/abc", map[string]any{"name": "Q"}), 400, "model_display_group.invalid_id")
	s.expect(s.do("DELETE", grpPath+"/abc", nil), 400, "model_display_group.invalid_id")
	s.expect(s.do("PATCH", grpPath+"/9999", map[string]any{"name": "Q"}), 404, "model_display_group.not_found")
	s.expect(s.do("DELETE", grpPath+"/9999", nil), 404, "model_display_group.not_found")
	s.expect(s.do("PATCH", fmt.Sprintf("%s/%d", grpPath, slow.ID), map[string]any{"name": "Fast"}), 409, "model_display_group.already_exists")
	s.expect(s.do("PATCH", fmt.Sprintf("%s/%d", grpPath, slow.ID), map[string]any{"modelIDs": []uint{12345}}), 400, "request.invalid_model_display_group")

	// Replacing members: listed move in, unlisted leave, absent list keeps.
	upd := func(id uint, body map[string]any) GroupView {
		return decodeInto[struct {
			Group GroupView `json:"group"`
		}](t, s.ok("PATCH", fmt.Sprintf("%s/%d", grpPath, id), body)).Group
	}
	upd(fast.ID, map[string]any{"name": "Faster"})
	if g := group(m1.ID); g == nil || *g != fast.ID {
		t.Fatal("absent modelIDs keeps members")
	}
	upd(fast.ID, map[string]any{"modelIDs": []uint{m3.ID, m2.ID}})
	if g := group(m1.ID); g != nil {
		t.Fatal("unlisted former members are un-grouped")
	}
	if g := group(m2.ID); g == nil || *g != fast.ID {
		t.Fatal("listed models move in from other groups")
	}
	upd(fast.ID, map[string]any{"modelIDs": []uint{}})
	for _, id := range []uint{m1.ID, m2.ID, m3.ID} {
		if g := group(id); g != nil && *g == fast.ID {
			t.Fatal("empty list un-groups everything")
		}
	}
	// Delete returns members to vendor display.
	upd(slow.ID, map[string]any{"modelIDs": []uint{m1.ID}})
	s.ok("DELETE", fmt.Sprintf("%s/%d", grpPath, slow.ID), nil)
	if g := group(m1.ID); g != nil {
		t.Fatal("deleted group's members become ungrouped")
	}
	pg := decodeInto[page[GroupView]](t, s.ok("GET", grpPath+"?q=FAST", nil))
	if pg.Total != 1 || pg.Results[0].Name != "Faster" {
		t.Fatalf("list: %+v", pg)
	}
}

func TestDisplayOrderPresentationGroups(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("ord", "openai", nil)
	mk := func(name string) uint {
		return s.bind(up.ID, name, name, []string{ProtoOpenAIChat}, nil).PlatformModelID
	}
	a1 := mk("gpt-1")
	c1 := mk("claude-1")
	a2 := mk("gpt-2")
	c2 := mk("claude-2")
	d1 := mk("kimi-1")
	list := func() string {
		var out []string
		for _, m := range decodeInto[page[ModelView]](t, s.ok("GET", mdPath, nil)).Results {
			out = append(out, m.PlatformModelName)
		}
		return strings.Join(out, " ")
	}
	// Vendors stay together; groups appear in the order of their first model.
	if got, want := list(), "gpt-1 gpt-2 claude-1 claude-2 kimi-1"; got != want {
		t.Fatalf("vendor grouping:\n got %s\nwant %s", got, want)
	}
	// A display group pulls models of several vendors together.
	g := s.mkGroup("Mixed", []uint{c2, d1})
	_ = g
	if got := list(); got != "gpt-1 gpt-2 claude-1 claude-2 kimi-1" {
		t.Fatalf("group order unexpected: %s", got)
	}
	// Reorder moves claude-2 first: its group (Mixed) now leads, with its members together.
	s.ok("POST", mdPath+"/order", map[string]any{"modelIDs": []uint{c2, a1, c1, a2, d1}})
	if got, want := list(), "claude-2 kimi-1 gpt-1 gpt-2 claude-1"; got != want {
		t.Fatalf("after reorder:\n got %s\nwant %s", got, want)
	}
	// Availability rank dominates: a model without usable sources sinks.
	s.env.DB.Model(&UpstreamModel{}).Where("upstream_model_name = ?", "claude-2").Update("status", "inactive")
	if got := list(); !strings.HasSuffix(got, "claude-2") && !strings.Contains(got, "claude-2") {
		t.Fatalf("model missing: %s", got)
	}
	names := strings.Fields(list())
	if names[len(names)-1] != "claude-2" {
		t.Fatalf("an unusable model must rank last: %v", names)
	}
}

// ---- icons -------------------------------------------------------------------------------------

func (s *suite) uploadOK(data []byte) IconUploadResult {
	s.t.Helper()
	w := s.upload(data)
	testkitExpect(s.t, w, 200, "")
	return decodeData[IconUploadResult](s.t, w)
}

func testkitExpect(t *testing.T, w *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if w.Code != status {
		t.Fatalf("status %d want %d: %s", w.Code, status, w.Body.String())
	}
	if code != "" && !strings.Contains(w.Body.String(), `"errorCode":"`+code+`"`) {
		t.Fatalf("errorCode want %q: %s", code, w.Body.String())
	}
}

func TestIconUpload(t *testing.T) {
	s := newSuite(t)
	data := pngBytes(64, 64)
	res := s.uploadOK(data)
	if !strings.HasPrefix(res.Ref, "asset:ico_") || res.PublicID != strings.TrimPrefix(res.Ref, "asset:") || len(res.PublicID) != 36 ||
		res.ContentType != "image/png" || res.SizeBytes != int64(len(data)) || res.Width != 64 || res.Height != 64 || res.Reused {
		t.Fatalf("upload: %+v", res)
	}
	again := s.uploadOK(data)
	if !again.Reused || again.PublicID != res.PublicID {
		t.Fatalf("dedup: %+v", again)
	}
	var row IconAsset
	s.env.DB.Where("public_id = ?", res.PublicID).First(&row)
	if row.StoragePath != "model-icons/"+row.SHA256[:2]+"/"+row.SHA256+".png" || row.ReadyAt == nil || row.CreatedByUserID != 1 {
		t.Fatalf("row: %+v", row)
	}
	if !s.store.has(row.StoragePath) {
		t.Fatal("object must be stored")
	}
	if !row.LeaseExpiresAt.Equal(s.env.Clock.Now().Add(24*time.Hour)) || row.UnreferencedAt == nil {
		t.Fatalf("lease: %+v", row)
	}

	// Invalid files.
	var g bytes.Buffer
	_ = gif.Encode(&g, image.NewPaletted(image.Rect(0, 0, 4, 4), nil), nil)
	for name, body := range map[string][]byte{
		"gif":     g.Bytes(),
		"svg":     []byte(`<svg xmlns="http://www.w3.org/2000/svg"/>`),
		"garbage": []byte("definitely not an image"),
		"empty":   {},
		"wide":    pngBytes(3000, 10),
		"tall":    pngBytes(10, 3000),
		"truncated": func() []byte {
			b := pngBytes(32, 32)
			return b[:len(b)-30]
		}(),
	} {
		w := s.upload(body)
		if w.Code != 400 || errCode(s, w) != "llm.model_icon_file_invalid" {
			t.Errorf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	// Size limits.
	big := make([]byte, 1<<20+1)
	copy(big, pngBytes(8, 8))
	testkitExpect(t, s.upload(big), 413, "llm.model_icon_file_too_large")
	huge := make([]byte, 2<<20)
	testkitExpect(t, s.upload(huge), 413, "llm.model_icon_file_too_large")
	// Missing file part or not multipart.
	buf, ct := multipartFile(t, "", "", nil)
	h := s.adm()
	h["Content-Type"] = ct
	testkitExpect(t, s.env.Do("POST", icoPath, h, buf), 400, "llm.model_icon_file_invalid")
	buf, ct = multipartFile(t, "other", "x.png", data)
	h["Content-Type"] = ct
	testkitExpect(t, s.env.Do("POST", icoPath, h, buf), 400, "llm.model_icon_file_invalid")
	testkitExpect(t, s.do("POST", icoPath, map[string]string{"a": "b"}), 400, "llm.model_icon_file_invalid")
	// Needs an admin.
	buf, ct = multipartFile(t, "file", "i.png", data)
	h = s.usr(3)
	h["Content-Type"] = ct
	testkitExpect(t, s.env.Do("POST", icoPath, h, buf), 403, "")
	// Library listing, newest first.
	other := s.uploadOK(pngBytes(16, 16))
	list := decodeInto[page[IconAssetView]](t, s.ok("GET", icoPath, nil))
	if list.Total != 2 || list.Results[0].PublicID != other.PublicID || list.Results[1].Ref != res.Ref || list.Results[0].Width != 16 {
		t.Fatalf("list: %+v", list)
	}
}

// brokenStore is an object store that is down.
type brokenStore struct{}

func (brokenStore) Put(context.Context, string, string, []byte) error { return errStorageMissing }
func (brokenStore) Get(context.Context, string) ([]byte, error)       { return nil, errStorageMissing }
func (brokenStore) Delete(context.Context, string) error              { return errStorageMissing }

func TestIconUploadWithoutStorage(t *testing.T) {
	s := newSuite(t, func(o *Options) { o.IconStore = brokenStore{} })
	w := s.upload(pngBytes(8, 8))
	if w.Code != 500 {
		t.Fatalf("no storage: %d %s", w.Code, w.Body.String())
	}
}

func TestIconPublicRead(t *testing.T) {
	s := newSuite(t)
	data := pngBytes(32, 32)
	res := s.uploadOK(data)
	var row IconAsset
	s.env.DB.Where("public_id = ?", res.PublicID).First(&row)
	url := "/api/v1/llm/icon-assets/" + res.PublicID

	w := s.env.Do("GET", url, nil, nil)
	if w.Code != 200 || !bytes.Equal(w.Body.Bytes(), data) {
		t.Fatalf("read: %d", w.Code)
	}
	hd := w.Header()
	if hd.Get("Content-Type") != "image/png" || hd.Get("Content-Disposition") != "inline" || hd.Get("Content-Length") != fmt.Sprint(len(data)) ||
		hd.Get("Cache-Control") != "public, max-age=31536000, immutable" || hd.Get("ETag") != `"`+row.SHA256+`"` ||
		hd.Get("X-Content-Type-Options") != "nosniff" || hd.Get("Cross-Origin-Resource-Policy") != "cross-origin" {
		t.Fatalf("headers: %v", hd)
	}
	// Conditional request.
	w = s.env.Do("GET", url, map[string]string{"If-None-Match": `"` + row.SHA256 + `"`}, nil)
	if w.Code != 304 || w.Body.Len() != 0 || w.Header().Get("ETag") != `"`+row.SHA256+`"` || w.Header().Get("Cache-Control") == "" {
		t.Fatalf("304: %d %q %v", w.Code, w.Body.String(), w.Header())
	}
	w = s.env.Do("GET", url, map[string]string{"If-None-Match": row.SHA256}, nil)
	if w.Code != 200 {
		t.Fatalf("an unquoted etag must not match: %d", w.Code)
	}
	// Case-insensitive, trimmed id.
	if w = s.env.Do("GET", "/api/v1/llm/icon-assets/"+strings.ToUpper(res.PublicID), nil, nil); w.Code != 200 {
		t.Fatalf("upper-case id: %d", w.Code)
	}
	// Invalid or unknown ids.
	for _, id := range []string{"ico_short", "nope", "ico_" + strings.Repeat("0", 32), "ico_" + strings.Repeat("g", 32)} {
		testkitExpect(t, s.env.Do("GET", "/api/v1/llm/icon-assets/"+id, nil, nil), 404, "llm.model_icon_asset_not_found")
	}
	// Not ready assets are not served.
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("ready_at", nil)
	testkitExpect(t, s.env.Do("GET", url, nil, nil), 404, "llm.model_icon_asset_not_found")
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("ready_at", s.env.Clock.Now())
	// Integrity: the storage path must be the canonical one; the size must be sane.
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("storage_path", "model-icons/zz/other.png")
	testkitExpect(t, s.env.Do("GET", url, nil, nil), 404, "llm.model_icon_asset_not_found")
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("storage_path", row.StoragePath)
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("size_bytes", 0)
	testkitExpect(t, s.env.Do("GET", url, nil, nil), 404, "llm.model_icon_asset_not_found")
	s.env.DB.Model(&IconAsset{}).Where("id = ?", row.ID).Update("size_bytes", row.SizeBytes)
	// A missing object is a 404, even with a matching If-None-Match.
	_ = s.store.Delete(bg, row.StoragePath)
	w = s.env.Do("GET", url, map[string]string{"If-None-Match": `"` + row.SHA256 + `"`}, nil)
	testkitExpect(t, w, 404, "llm.model_icon_asset_not_found")
	// Storage unavailable is a server error.
	down := newSuite(t, func(o *Options) { o.IconStore = brokenStore{} })
	down.env.DB.Create(&IconAsset{PublicID: res.PublicID, SHA256: row.SHA256, StoragePath: row.StoragePath, ContentType: row.ContentType,
		SizeBytes: row.SizeBytes, Width: 32, Height: 32, ReadyAt: row.ReadyAt, LeaseExpiresAt: row.LeaseExpiresAt})
	if w = down.env.Do("GET", url, nil, nil); w.Code != 500 {
		t.Fatalf("no store: %d", w.Code)
	}
}

func TestIconReferencesAndRemoval(t *testing.T) {
	s := newSuite(t)
	res := s.uploadOK(pngBytes(24, 24))
	ref := res.Ref
	// Reference from a model, a vendor and a group.
	m := s.mkModel("icon-model", map[string]any{"icon": strings.ToUpper(ref[:6]) + ref[6:]})
	if m.Icon != ref {
		t.Fatalf("normalized reference: %q", m.Icon)
	}
	s.ok("PATCH", vendPath+"/openai", map[string]any{"icon": ref})
	grp := s.mkGroup("IconGroup", nil)
	s.ok("PATCH", fmt.Sprintf("%s/%d", grpPath, grp.ID), map[string]any{"icon": ref})

	w := s.do("DELETE", icoPath+"/"+res.PublicID, nil)
	s.expect(w, 409, "llm.model_icon_asset_in_use")
	det := decodeInto[struct {
		Details IconReferences `json:"details"`
	}](t, []byte(w.Body.String())).Details
	if det.ReferenceCount != 3 || det.Models != 1 || det.Vendors != 1 || det.DisplayGroups != 1 || det.ConversationRuns != 0 {
		t.Fatalf("details: %+v", det)
	}
	// Conversation runs held by another area count too.
	s.svc.opts.IconUsage = iconUsageFn(func(string) int64 { return 4 })
	det = decodeInto[struct {
		Details IconReferences `json:"details"`
	}](t, []byte(s.do("DELETE", icoPath+"/"+res.PublicID, nil).Body.String())).Details
	if det.ConversationRuns != 4 || det.ReferenceCount != 7 {
		t.Fatalf("run references: %+v", det)
	}
	s.svc.opts.IconUsage = nil

	// Drop the references; removal now works and hides the asset at once.
	s.ok("PATCH", fmt.Sprintf("%s/%d", mdPath, m.ID), map[string]any{"icon": ""})
	s.ok("PATCH", vendPath+"/openai", map[string]any{"icon": "openai"})
	s.ok("PATCH", fmt.Sprintf("%s/%d", grpPath, grp.ID), map[string]any{"icon": ""})
	s.ok("DELETE", icoPath+"/"+res.PublicID, nil)
	if list := decodeInto[page[IconAssetView]](t, s.ok("GET", icoPath, nil)); list.Total != 0 {
		t.Fatalf("removed asset must leave the library at once: %+v", list)
	}
	testkitExpect(t, s.do("DELETE", icoPath+"/"+res.PublicID, nil), 404, "llm.model_icon_asset_not_found")
	testkitExpect(t, s.do("DELETE", icoPath+"/ico_bad", nil), 404, "llm.model_icon_asset_not_found")
	// It is still served during the grace period.
	if w := s.env.Do("GET", "/api/v1/llm/icon-assets/"+res.PublicID, nil, nil); w.Code != 200 {
		t.Fatalf("grace read: %d", w.Code)
	}
	// Saving a reference within the grace period resurrects it.
	s.env.Clock.Advance(23 * time.Hour)
	s.mkModel("resurrect", map[string]any{"icon": ref})
	s.env.Clock.Advance(2 * time.Hour)
	s.svc.RunIconCleanup(bg)
	var n int64
	s.env.DB.Model(&IconAsset{}).Where("public_id = ?", res.PublicID).Count(&n)
	if n != 1 {
		t.Fatal("a referenced asset must survive cleanup")
	}
	var row IconAsset
	s.env.DB.Where("public_id = ?", res.PublicID).First(&row)
	if row.DeleteRequestedAt != nil || row.UnreferencedAt != nil || !row.LeaseExpiresAt.After(s.env.Clock.Now()) {
		t.Fatalf("resurrected row: %+v", row)
	}
	if list := decodeInto[page[IconAssetView]](t, s.ok("GET", icoPath, nil)); list.Total != 1 {
		t.Fatalf("resurrected asset is back in the library: %+v", list)
	}
}

type iconUsageFn func(string) int64

func (f iconUsageFn) CountIconReferences(_ context.Context, ref string) (int64, error) {
	return f(ref), nil
}

func TestIconCleanup(t *testing.T) {
	s := newSuite(t)
	mk := func(seed int) (IconUploadResult, IconAsset) {
		res := s.uploadOK(pngBytes(8+seed, 8))
		var row IconAsset
		s.env.DB.Where("public_id = ?", res.PublicID).First(&row)
		return res, row
	}
	expire := func(id uint, unref bool) {
		upd := map[string]any{"lease_expires_at": s.env.Clock.Now().Add(-time.Minute)}
		if !unref {
			upd["unreferenced_at"] = nil
		}
		s.env.DB.Model(&IconAsset{}).Where("id = ?", id).Updates(upd)
	}
	load := func(id uint) (IconAsset, bool) {
		var rows []IconAsset
		s.env.DB.Where("id = ?", id).Find(&rows)
		if len(rows) == 0 {
			return IconAsset{}, false
		}
		return rows[0], true
	}

	// Not yet expired: untouched.
	_, fresh := mk(0)
	s.svc.RunIconCleanup(bg)
	if _, ok := load(fresh.ID); !ok || !s.store.has(fresh.StoragePath) {
		t.Fatal("a live lease must not be cleaned")
	}

	// Expired and referenced: lease renewed, kept.
	resRef, refRow := mk(1)
	s.mkModel("cleanup-ref", map[string]any{"icon": resRef.Ref})
	expire(refRow.ID, true)
	s.svc.RunIconCleanup(bg)
	got, ok := load(refRow.ID)
	if !ok || !got.LeaseExpiresAt.After(s.env.Clock.Now().Add(23*time.Hour)) || got.UnreferencedAt != nil {
		t.Fatalf("referenced: %+v", got)
	}

	// Expired, unreferenced, never marked: the grace period starts, nothing is deleted.
	_, unmarked := mk(2)
	expire(unmarked.ID, false)
	s.svc.RunIconCleanup(bg)
	got, ok = load(unmarked.ID)
	if !ok || got.UnreferencedAt == nil || got.DeletingAt != nil || !s.store.has(unmarked.StoragePath) ||
		!got.LeaseExpiresAt.After(s.env.Clock.Now().Add(23*time.Hour)) {
		t.Fatalf("grace start: %+v", got)
	}

	// Grace passed: claimed and deleted (object and row).
	s.env.Clock.Advance(25 * time.Hour)
	// Keep the referenced one alive across the jump.
	s.svc.RunIconCleanup(bg)
	if _, ok := load(unmarked.ID); ok || s.store.has(unmarked.StoragePath) {
		t.Fatal("expired grace must delete object and row")
	}
	if _, ok := load(refRow.ID); !ok {
		t.Fatal("referenced asset must remain")
	}
	if _, ok := load(fresh.ID); ok {
		t.Fatal("the never-referenced fresh asset is also past its grace now")
	}

	// A failing object delete leaves the row in the deleting state and is retried.
	_, bad := mk(3)
	s.env.DB.Model(&IconAsset{}).Where("id = ?", bad.ID).Updates(map[string]any{"lease_expires_at": s.env.Clock.Now().Add(-time.Minute)})
	s.store.failDelete = true
	s.svc.RunIconCleanup(bg)
	got, ok = load(bad.ID)
	if !ok || got.DeletingAt == nil || !s.store.has(bad.StoragePath) {
		t.Fatalf("failed delete: %+v ok=%v", got, ok)
	}
	s.store.failDelete = false
	s.svc.RunIconCleanup(bg)
	if _, ok := load(bad.ID); ok || s.store.has(bad.StoragePath) {
		t.Fatal("retry must finish the deletion")
	}
	// A missing object counts as deleted.
	_, gone := mk(4)
	_ = s.store.Delete(bg, gone.StoragePath)
	s.env.DB.Model(&IconAsset{}).Where("id = ?", gone.ID).Updates(map[string]any{"lease_expires_at": s.env.Clock.Now().Add(-time.Minute)})
	s.svc.RunIconCleanup(bg)
	if _, ok := load(gone.ID); ok {
		t.Fatal("a missing object counts as deleted")
	}
}

func TestIconUploadRestoresRemovedAsset(t *testing.T) {
	s := newSuite(t)
	data := pngBytes(20, 20)
	res := s.uploadOK(data)
	s.ok("DELETE", icoPath+"/"+res.PublicID, nil)
	again := s.uploadOK(data)
	if !again.Reused || again.PublicID != res.PublicID {
		t.Fatalf("restore: %+v", again)
	}
	if list := decodeInto[page[IconAssetView]](t, s.ok("GET", icoPath, nil)); list.Total != 1 {
		t.Fatalf("restored asset is listed: %+v", list)
	}
	// A not-ready (interrupted) upload recovers on retry.
	s.env.DB.Model(&IconAsset{}).Where("public_id = ?", res.PublicID).Update("ready_at", nil)
	_ = s.store.Delete(bg, iconStoragePath(sha256Hex(data), ".png"))
	rec := s.uploadOK(data)
	if rec.PublicID != res.PublicID {
		t.Fatalf("recovery: %+v", rec)
	}
	if w := s.env.Do("GET", "/api/v1/llm/icon-assets/"+res.PublicID, nil, nil); w.Code != 200 {
		t.Fatalf("recovered asset must be served: %d", w.Code)
	}
}

func TestIconRefParsing(t *testing.T) {
	good := map[string]string{
		"":                                     "",
		"  ":                                   "",
		"openai":                               "openai",
		"My_Icon.v2":                           "my_icon.v2",
		"https://x.test/a.png":                 "https://x.test/a.png",
		"http://x.test":                        "http://x.test",
		"/static/i.svg":                        "/static/i.svg",
		"ASSET:ICO_" + strings.Repeat("A", 32): "asset:ico_" + strings.Repeat("a", 32),
	}
	for in, want := range good {
		got, err := parseIconRef(in)
		if err != nil || got.Value != want {
			t.Errorf("%q: %+v %v", in, got, err)
		}
	}
	for _, in := range []string{
		"data:image/png;base64,AA", "javascript:alert(1)", "//host/a.png", "/a\\b", "ftp://x.test/a.png", "https://u@x.test/a",
		"https:///nohost", "asset:ico_short", "asset:other", "has space", "tab\there", "-leading", strings.Repeat("a", 129),
	} {
		if _, err := parseIconRef(in); err == nil {
			t.Errorf("%q must be refused", in)
		}
	}
	if _, err := parseIconRef(strings.Repeat("a", 2049)); err == nil {
		t.Error("too long")
	}
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}
