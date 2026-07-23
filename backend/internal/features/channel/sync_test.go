package channel

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

func (s *suite) remotePath(upID uint, tail string) string {
	return fmt.Sprintf("%s/%d/models%s", upPath, upID, tail)
}

func (s *suite) addLocal(upID uint, name, source, status string) UpstreamModel {
	s.t.Helper()
	um := UpstreamModel{UpstreamID: upID, BindingCode: "upm_" + strings.Repeat("0", 28) + fmt.Sprintf("%04d", len(name)*100+int(upID)),
		UpstreamModelName: name, Status: status, Source: source, KindsJSON: `["chat"]`, SuggestedProtocol: ProtoOpenAIChat, Vendor: "unknown", RawJSON: "{}"}
	um.BindingCode = "upm_" + fmt.Sprintf("%032x", s.countUpstreamModels()+1)
	if err := s.env.DB.Create(&um).Error; err != nil {
		s.t.Fatal(err)
	}
	return um
}

func (s *suite) countUpstreamModels() int64 {
	var n int64
	s.env.DB.Model(&UpstreamModel{}).Count(&n)
	return n
}

func TestRemotePreviewAndSyncPlan(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("sync", "openai", nil)
	s.client.models = []RemoteModel{{ID: "a", OwnedBy: "openai"}, {ID: "b", OwnedBy: "openai"}}
	s.addLocal(up.ID, "a", "manual", "active")
	b := s.addLocal(up.ID, "b", "sync", "inactive")
	c := s.addLocal(up.ID, "c", "sync", "active")

	pv := decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	if pv.Total != 2 || pv.SnapshotID == "" || len(pv.Items) != 2 {
		t.Fatalf("preview: %+v", pv)
	}
	p := pv.SyncPlan
	if len(p.AddedModels) != 0 || strings.Join(p.ProtectedModels, ",") != "a" || strings.Join(p.ReactivatedModels, ",") != "b" ||
		strings.Join(p.InactivatedModels, ",") != "c" || p.UpdatedModels == nil || p.UnchangedModels == nil || p.UnresolvedProtocolModels == nil {
		t.Fatalf("plan: %+v", p)
	}
	if !pv.Items[0].AlreadySynced || pv.Items[0].SuggestedProtocol != ProtoOpenAIChat || pv.Items[0].SuggestedKindsJSON != `["chat"]` ||
		len(pv.Items[0].SuggestedProtocols) != 1 || pv.Items[0].BoundPlatformModels == nil {
		t.Fatalf("item: %+v", pv.Items[0])
	}
	// The call carries the upstream's chat protocol, key and attribution; nothing was written.
	call := s.client.listCalls[0]
	if call.Protocol != ProtoOpenAIChat || call.APIKey != "sk-sync-abcdefgh" || call.BaseURL != "https://api.sync.example.com/v1" {
		t.Fatalf("call: %+v", call)
	}
	if s.countUpstreamModels() != 3 {
		t.Fatal("preview must not write")
	}

	// Sync applies it.
	res := decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if res.TotalUpstream != 2 || res.ProtectedUpstreamModels != 1 || res.ReactivatedModels != 1 || res.InactivatedModels != 1 ||
		res.CreatedUpstreamModels != 0 || res.ExistingUpstreamModels != 2 || res.SkippedUpstreamModels != 0 || res.SnapshotID != pv.SnapshotID {
		t.Fatalf("sync result: %+v", res)
	}
	if res.SyncedModels == nil || res.UnresolvedProtocolModels == nil {
		t.Fatalf("lists must not be null: %+v", res)
	}
	var gotB, gotC UpstreamModel
	s.env.DB.First(&gotB, b.ID)
	if gotB.Status != "active" || gotB.LastSyncedAt == nil {
		t.Fatalf("b must be reactivated: %+v", gotB)
	}
	s.env.DB.First(&gotC, c.ID)
	if gotC.Status != "inactive" {
		t.Fatalf("c must be inactivated: %+v", gotC)
	}
	var manual UpstreamModel
	s.env.DB.Where("upstream_model_name = ?", "a").First(&manual)
	if manual.Status != "active" || manual.Source != "manual" || manual.LastSyncedAt != nil {
		t.Fatalf("protected entry untouched: %+v", manual)
	}
}

func TestSyncCreatesModels(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("create", "openai", nil)
	s.client.models = []RemoteModel{{ID: "gpt-4o", OwnedBy: "openai"}, {ID: " dup ", OwnedBy: "x"}, {ID: "dup"}, {ID: ""}, {ID: "  "}, {ID: "claude-3-opus", OwnedBy: "anthropic"}}
	res := decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if res.TotalUpstream != 3 || res.CreatedUpstreamModels != 3 || res.ExistingUpstreamModels != 0 || len(res.SyncedModels) != 3 {
		t.Fatalf("created: %+v", res)
	}
	var rows []UpstreamModel
	s.env.DB.Where("upstream_id = ?", up.ID).Order("upstream_model_name").Find(&rows)
	if len(rows) != 3 || rows[0].UpstreamModelName != "claude-3-opus" || rows[0].Vendor != "anthropic" || rows[0].Icon != "claude" ||
		rows[0].Source != "sync" || rows[0].Status != "active" || rows[0].LastSyncedAt == nil || rows[0].SuggestedProtocol != ProtoOpenAIChat ||
		!strings.HasPrefix(rows[0].BindingCode, "upm_") || rows[0].RawJSON != `{"id":"claude-3-opus","owned_by":"anthropic"}` {
		t.Fatalf("rows: %+v", rows[0])
	}
	// "dup" keeps the first occurrence (trimmed ids; duplicates collapse).
	if rows[1].UpstreamModelName != "dup" || rows[1].Vendor != "unknown" {
		t.Fatalf("dup: %+v", rows[1])
	}
	// A second identical sync is all unchanged.
	again := decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if again.UnchangedUpstreamModels != 3 || again.CreatedUpstreamModels != 0 || again.UpdatedUpstreamModels != 0 || again.ExistingUpstreamModels != 3 || again.SnapshotID != res.SnapshotID {
		t.Fatalf("second sync: %+v", again)
	}
	// Derived metadata changes count as updates, raw blob changes alone do not.
	s.env.DB.Model(&UpstreamModel{}).Where("upstream_model_name = ?", "gpt-4o").Update("vendor", "meta")
	s.env.DB.Model(&UpstreamModel{}).Where("upstream_model_name = ?", "dup").Update("raw_json", `{"changed":true}`)
	pv := decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	if strings.Join(pv.SyncPlan.UpdatedModels, ",") != "gpt-4o" || len(pv.SyncPlan.UnchangedModels) != 2 {
		t.Fatalf("update plan: %+v", pv.SyncPlan)
	}
	// The public catalog is invalidated by a sync (routes unaffected here, but the call must not fail).
	s.svc.invalidateCatalog()
}

func TestSyncGuards(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("guard", "openai", nil)
	path := s.remotePath(up.ID, "/sync")
	old := s.addLocal(up.ID, "old", "sync", "active")

	// Empty list needs confirmation.
	s.client.models = nil
	s.expect(s.do("POST", path, nil), 409, "llm.remote_models_empty_confirmation_required")
	var local UpstreamModel
	s.env.DB.First(&local, old.ID)
	if local.Status != "active" {
		t.Fatal("nothing may change without confirmation")
	}
	res := decodeInto[SyncResult](t, s.ok("POST", path+"?allow_empty=true", nil))
	if res.InactivatedModels != 1 {
		t.Fatalf("confirmed empty sync: %+v", res)
	}
	s.expect(s.do("POST", path+"?allow_empty=yes", nil), 409, "llm.remote_models_empty_confirmation_required")

	// A stale snapshot id is refused.
	s.client.models = []RemoteModel{{ID: "m1"}}
	pv := decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	s.client.models = []RemoteModel{{ID: "m1"}, {ID: "m2"}}
	s.expect(s.do("POST", path+"?expected_snapshot="+pv.SnapshotID, nil), 409, "llm.remote_models_snapshot_changed")
	if s.countUpstreamModels() != 1 {
		t.Fatal("stale snapshot must not write")
	}
	pv = decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	s.ok("POST", path+"?expected_snapshot="+pv.SnapshotID, nil)
	if s.countUpstreamModels() != 3 {
		t.Fatalf("matching snapshot syncs: %d", s.countUpstreamModels())
	}
	// Identical lists produce identical ids, different owners a different one.
	a := snapshotID([]RemoteModel{{ID: "x", OwnedBy: "o"}})
	b := snapshotID([]RemoteModel{{ID: "x", OwnedBy: "o"}})
	c := snapshotID([]RemoteModel{{ID: "x", OwnedBy: "p"}})
	if a != b || a == c {
		t.Fatal("snapshot id")
	}
	// A uniqueness collision during insert aborts everything with snapshot_changed.
	s.client.models = []RemoteModel{{ID: "n1"}, {ID: "n2"}}
	err := s.env.DB.Callback().Create().Before("gorm:create").Register("test:collide", func(tx *gorm.DB) {
		um, ok := tx.Statement.Dest.(*UpstreamModel)
		if !ok || um.UpstreamModelName != "n2" || tx.Error != nil {
			return
		}
		// Someone else creates the same name first, inside the same transaction.
		_, tx.Error = tx.Statement.ConnPool.ExecContext(tx.Statement.Context,
			"INSERT INTO llm_upstream_models (upstream_id, binding_code, upstream_model_name, created_at, updated_at) VALUES (?, ?, ?, ?, ?)",
			um.UpstreamID, "upm_rival", "n2", um.CreatedAt, um.CreatedAt)
	})
	if err != nil {
		t.Fatal(err)
	}
	before := s.countUpstreamModels()
	w := s.do("POST", path, nil)
	s.expect(w, 409, "llm.remote_models_snapshot_changed")
	if s.countUpstreamModels() != before {
		t.Fatalf("a failing apply must roll back: %d vs %d", s.countUpstreamModels(), before)
	}
}

func TestRemoteErrors(t *testing.T) {
	s := newSuite(t)
	path := func(id uint) string { return s.remotePath(id, "/remote") }
	s.expect(s.do("GET", path(9999), nil), 404, "upstream.not_found")
	s.expect(s.do("GET", upPath+"/x/models/remote", nil), 400, "upstream.invalid_id")
	// A style that yields no chat protocol.
	up := s.mkUpstream("nochat", "openai", nil)
	s.env.DB.Model(&Upstream{}).Where("id = ?", up.ID).Updates(map[string]any{"compatible": "mystery", "protocol_defaults_json": "{}"})
	s.expect(s.do("GET", path(up.ID), nil), 400, "llm.protocol_required")
	s.expect(s.do("POST", s.remotePath(up.ID, "/sync"), nil), 400, "llm.protocol_required")
	// No active key.
	k := s.mkUpstream("nokey", "openai", nil)
	enc, _ := s.svc.encryptKeys(&keyDoc{Strategy: "failover", Keys: []keyEntry{{Key: "k", Status: "disabled"}}})
	s.env.DB.Model(&Upstream{}).Where("id = ?", k.ID).Update("api_keys_enc", enc)
	s.expect(s.do("GET", path(k.ID), nil), 400, "llm.no_active_api_key")
	s.env.DB.Model(&Upstream{}).Where("id = ?", k.ID).Update("api_keys_enc", "undecryptable")
	s.expect(s.do("GET", path(k.ID), nil), 400, "llm.no_active_api_key")
	// Adapter failure and missing adapter.
	ok := s.mkUpstream("adapter", "openai", nil)
	s.client.listErr = errors.New("secret upstream detail sk-leak")
	w := s.do("GET", path(ok.ID), nil)
	s.expect(w, 502, "llm.remote_models_unavailable")
	if strings.Contains(w.Body.String(), "sk-leak") {
		t.Fatal("the adapter's text must not be returned")
	}
	s.expect(s.do("POST", s.remotePath(ok.ID, "/sync"), nil), 502, "llm.remote_models_unavailable")
	s.expect(s.do("POST", s.remotePath(ok.ID, "/import"), map[string]any{"items": []map[string]any{{"platformModelName": "gpt-4o", "upstreamModelName": "gpt-4o"}}}), 200, "")

	noClient := newSuite(t, func(o *Options) { o.Client = nil })
	u := noClient.mkUpstream("nc", "openai", nil)
	noClient.expect(noClient.do("GET", noClient.remotePath(u.ID, "/remote"), nil), 502, "llm.remote_models_unavailable")
}

func TestSyncProtocolResolutionIsLenient(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("anth", "anthropic", nil)
	s.client.models = []RemoteModel{{ID: "claude-3-opus"}, {ID: "gpt-image-1"}}
	pv := decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	if strings.Join(pv.SyncPlan.UnresolvedProtocolModels, ",") != "gpt-image-1" {
		t.Fatalf("unresolved: %+v", pv.SyncPlan)
	}
	for _, it := range pv.Items {
		if it.UpstreamModelName == "gpt-image-1" && (it.SuggestedProtocol != "" || len(it.SuggestedProtocols) != 0 || it.SuggestedProtocols == nil) {
			t.Fatalf("image item: %+v", it)
		}
	}
	res := decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if res.CreatedUpstreamModels != 2 || strings.Join(res.UnresolvedProtocolModels, ",") != "gpt-image-1" {
		t.Fatalf("sync: %+v", res)
	}
	var um UpstreamModel
	s.env.DB.Where("upstream_model_name = ?", "gpt-image-1").First(&um)
	if um.SuggestedProtocol != "" {
		t.Fatalf("suggested protocol: %q", um.SuggestedProtocol)
	}
	// Binding it needs an explicit protocol.
	w := s.do("POST", s.remotePath(up.ID, ""), map[string]any{"platformModelName": "gpt-image-1", "upstreamModelName": "gpt-image-1", "protocols": []string{}})
	s.expect(w, 400, "llm.protocol_required")
	// After the upstream gets an image default the re-sync fills the suggestion.
	s.ok("PATCH", fmt.Sprintf("%s/%d", upPath, up.ID), map[string]any{"protocolDefaultsJSON": `{"chat":"anthropic_messages","image_gen":"openai_image_generations","image_edit":"openai_image_edits"}`})
	res = decodeInto[SyncResult](t, s.ok("POST", s.remotePath(up.ID, "/sync"), nil))
	if res.UpdatedUpstreamModels != 1 || len(res.UnresolvedProtocolModels) != 0 {
		t.Fatalf("resync: %+v", res)
	}
	s.env.DB.Where("upstream_model_name = ?", "gpt-image-1").First(&um)
	if um.SuggestedProtocol != ProtoOpenAIImageGen {
		t.Fatalf("suggested protocol: %q", um.SuggestedProtocol)
	}
	// Protected entries are not reported as unresolved.
	s.addLocal(up.ID, "manual-img", "manual", "active")
	s.client.models = []RemoteModel{{ID: "manual-img"}}
	s.env.DB.Model(&UpstreamModel{}).Where("upstream_model_name = ?", "manual-img").Update("suggested_protocol", "")
	pv = decodeInto[RemotePreview](t, s.ok("GET", s.remotePath(up.ID, "/remote"), nil))
	if len(pv.SyncPlan.UnresolvedProtocolModels) != 0 {
		t.Fatalf("protected must be excluded: %+v", pv.SyncPlan)
	}
}

func TestImportModels(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("imp", "openai", nil)
	path := s.remotePath(up.ID, "/import")
	items := []map[string]any{
		{"platformModelName": "imp-one", "upstreamModelName": "up-one"},
		{"platformModelName": "imp-two", "upstreamModelName": "up-two", "protocol": "bogus"},
		{"platformModelName": "gpt-image-1", "upstreamModelName": "up-img", "status": "inactive", "priority": 5},
	}
	res := decodeInto[ImportResult](t, s.ok("POST", path, map[string]any{"items": items}))
	if res.Total != 3 || res.ImportedCount != 2 || res.FailedCount != 1 || res.CreatedPlatform != 2 || res.CreatedRoutes != 3 || res.ExistingRoutes != 0 {
		t.Fatalf("import: %+v", res)
	}
	if res.Results[0].Status != "created" || res.Results[1].Status != "failed" || res.Results[1].Error == "" || res.Results[2].Status != "created" {
		t.Fatalf("results: %+v", res.Results)
	}
	if res.Results[1].Error != "invalid adapter" {
		t.Fatalf("public error message: %q", res.Results[1].Error)
	}
	if !res.Results[0].CreatedRoute || res.Results[0].CreatedRoutes != 1 || !res.Results[0].CreatedPlatform || len(res.Results[0].Protocols) != 1 || res.Results[0].BindingCode == "" {
		t.Fatalf("item 0: %+v", res.Results[0])
	}
	if len(res.Results[2].Protocols) != 2 || res.Results[2].CreatedRoutes != 2 {
		t.Fatalf("item 2: %+v", res.Results[2])
	}
	// The import route settings.
	var routes []ModelRoute
	s.env.DB.Order("id").Find(&routes)
	for _, r := range routes {
		if r.Source != "import" || r.Weight != 1 {
			t.Fatalf("route: %+v", r)
		}
	}
	last := routes[len(routes)-1]
	if last.Status != "inactive" || last.Priority != 5 {
		t.Fatalf("status/priority: %+v", last)
	}
	var um UpstreamModel
	s.env.DB.Where("upstream_model_name = ?", "up-one").First(&um)
	if um.Source != "sync" {
		t.Fatalf("catalog source: %+v", um)
	}
	// Re-importing reports existing.
	res = decodeInto[ImportResult](t, s.ok("POST", path, map[string]any{"items": items[:1]}))
	if res.Results[0].Status != "existing" || res.CreatedRoutes != 0 || res.ExistingRoutes != 1 || res.CreatedPlatform != 0 || res.Results[0].CreatedPlatform {
		t.Fatalf("re-import: %+v", res)
	}
	// Whole-call validation.
	s.expect(s.do("POST", path, map[string]any{"items": []map[string]any{}}), 400, "request.invalid_body")
	s.expect(s.do("POST", path, map[string]any{"items": []map[string]any{{"platformModelName": "x", "upstreamModelName": "y"}}}), 400, "request.invalid_body")
	s.expect(s.do("POST", s.remotePath(9999, "/import"), map[string]any{"items": items[:1]}), 404, "upstream.not_found")
	// Item-level problems stay in the item.
	res = decodeInto[ImportResult](t, s.ok("POST", path, map[string]any{"items": []map[string]any{
		{"platformModelName": "personal:abc", "upstreamModelName": "z"},
		{"platformModelName": "ok-name", "upstreamModelName": "   "},
		{"platformModelName": "mix-model", "upstreamModelName": "mx", "protocols": []string{"openai_image_generations", "anthropic_messages"}},
	}}))
	if res.FailedCount != 3 {
		t.Fatalf("item errors: %+v", res)
	}
	if res.Results[0].Error != "invalid platform model name" || res.Results[2].Error != "invalid route protocol combination" {
		t.Fatalf("messages: %+v", res.Results)
	}
}

func TestImportPermissionGroups(t *testing.T) {
	s := newSuite(t)
	up := s.mkUpstream("impg", "openai", nil)
	path := s.remotePath(up.ID, "/import")
	item := []map[string]any{{"platformModelName": "grp-model", "upstreamModelName": "grp-up"}}
	s.expect(s.do("POST", path, map[string]any{"items": item, "permissionGroupIDs": []uint{99}}), 400, "admin.invalid_permission_group_models")
	s.expect(s.do("POST", path, map[string]any{"items": item, "permissionGroupIDs": []uint{0}}), 400, "admin.invalid_permission_group_models")
	var n int64
	s.env.DB.Model(&PlatformModel{}).Count(&n)
	if n != 0 {
		t.Fatal("no item may run when the groups are invalid")
	}
	g1, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "g1"})
	g2, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "g2"})
	g3, _ := s.svc.CreatePermissionGroup(bg, PermissionGroup{Name: "g3"})
	s.ok("POST", path, map[string]any{"items": item, "permissionGroupIDs": []uint{g2.ID}})
	var pm PlatformModel
	s.env.DB.Where("name = ?", "grp-model").First(&pm)
	// Union with the existing manual groups.
	_ = s.svc.SetModelGroups(bg, pm.ID, []uint{g3.ID})
	s.ok("POST", path, map[string]any{"items": item, "permissionGroupIDs": []uint{g1.ID, g2.ID}})
	got, _ := s.svc.ManualModelGroups(bg, pm.ID)
	if fmt.Sprint(got) != fmt.Sprint([]uint{g1.ID, g2.ID, g3.ID}) {
		t.Fatalf("groups: %v", got)
	}
}
