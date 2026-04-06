package settings_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/settings"
)

func (h *harness) setRaw(ns, key, value string) {
	h.t.Helper()
	res := h.env.DB.Model(&settings.SystemSetting{}).Where("namespace = ? AND \"key\" = ?", ns, key).Update("value", value)
	if res.Error != nil || res.RowsAffected != 1 {
		h.t.Fatalf("setRaw %s/%s: %v rows=%d", ns, key, res.Error, res.RowsAffected)
	}
}

func (h *harness) rowCount(ns, key string) int64 {
	h.t.Helper()
	var n int64
	h.env.DB.Model(&settings.SystemSetting{}).Where("namespace = ? AND \"key\" = ?", ns, key).Count(&n)
	return n
}

func (h *harness) seed() {
	h.t.Helper()
	if err := h.svc.Seed(context.Background()); err != nil {
		h.t.Fatalf("seed: %v", err)
	}
	if err := h.svc.Reload(context.Background()); err != nil {
		h.t.Fatalf("reload: %v", err)
	}
}

func TestSeedFreshDatabase(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	all := h.listAll()

	var rows int64
	h.env.DB.Model(&settings.SystemSetting{}).Count(&rows)
	if int(rows) != settings.RegistryCount() {
		t.Fatalf("rows %d, registry %d", rows, settings.RegistryCount())
	}
	listed := 0
	types := settings.RegistryTypes()
	for _, ns := range settings.Namespaces() {
		if len(all[ns]) == 0 {
			t.Errorf("namespace %s missing from listing", ns)
		}
		for _, it := range all[ns] {
			listed++
			id := ns + "/" + it.Key
			if it.ValueType != types[id] {
				t.Errorf("%s: type %q want %q", id, it.ValueType, types[id])
			}
			if it.Description == "" {
				t.Errorf("%s: no description", id)
			}
			if h.rowCount(ns, it.Key) != 1 {
				t.Errorf("%s: not exactly one row", id)
			}
			if it.Sensitive {
				if it.Value != "" || it.Configured {
					t.Errorf("%s: sensitive item %+v", id, it)
				}
				continue
			}
			if id == "file/embedding_model_signature" {
				// Filled in at start-up because a model is configured.
				want := settings.ModelSignature(settings.RegistryDefault("file", "rag_model"), 1536)
				if it.Value != want {
					t.Errorf("signature %q want %q", it.Value, want)
				}
				continue
			}
			if it.Value != settings.RegistryDefault(ns, it.Key) {
				t.Errorf("%s: value %q want default %q", id, it.Value, settings.RegistryDefault(ns, it.Key))
			}
		}
	}
	if listed != settings.RegistryCount() {
		t.Errorf("listed %d of %d", listed, settings.RegistryCount())
	}
	for _, k := range settings.SensitiveKeys() {
		it := h.find(k.Namespace, k.Key)
		if !it.Sensitive || it.Configured || it.Value != "" {
			t.Errorf("%s/%s: %+v", k.Namespace, k.Key, it)
		}
		if h.stored(k.Namespace, k.Key) != "" {
			t.Errorf("%s/%s: blank default must stay blank", k.Namespace, k.Key)
		}
	}
	if it := h.find("billing", "epay_types"); !strings.Contains(it.Value, "Alipay") {
		t.Errorf("epay types default: %s", it.Value)
	}
}

func TestSeedIsIdempotentAndKeepsAdministratorValues(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.advance(time.Hour)
	h.mustPatch(set("auth", "token_ttl_hours", "48"), set("chat", "default_system_prompt", "be brief"))
	h.seed()
	h.seed()
	if h.val("auth", "token_ttl_hours") != "48" || h.val("chat", "default_system_prompt") != "be brief" {
		t.Fatal("seeding overwrote administrator values")
	}
	var rows int64
	h.env.DB.Model(&settings.SystemSetting{}).Count(&rows)
	if int(rows) != settings.RegistryCount() {
		t.Fatalf("rows %d after reseed, want %d", rows, settings.RegistryCount())
	}
	// A deleted default row comes back; description and type are written on insert.
	h.env.DB.Where("namespace = ? AND \"key\" = ?", "mcp", "mcp_tool_prompt").Delete(&settings.SystemSetting{})
	h.seed()
	it := h.find("mcp", "mcp_tool_prompt")
	if it.ValueType != "string" || it.Description == "" {
		t.Fatalf("reinserted row: %+v", it)
	}
}

func TestSeedDoesNotRewriteDescriptionOrType(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.env.DB.Model(&settings.SystemSetting{}).Where("namespace = ? AND \"key\" = ?", "auth", "smtp_host").
		Updates(map[string]any{"description": "operator note", "value_type": "string"})
	h.seed()
	if it := h.find("auth", "smtp_host"); it.Description != "operator note" {
		t.Fatalf("description rewritten: %q", it.Description)
	}
}

func TestSeedMigratesHistoricalMimeDefaults(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	for i, old := range settings.HistoricalMimeLists() {
		h.setRaw("file", "allowed_mime_types", old)
		h.seed()
		if got := h.stored("file", "allowed_mime_types"); got != settings.DefaultMimeTypes() {
			t.Fatalf("generation %d not reset: %s", i, got)
		}
	}
	// Case and order do not matter.
	parts := strings.Split(settings.HistoricalMimeLists()[0], ",")
	for i, j := 0, len(parts)-1; i < j; i, j = i+1, j-1 {
		parts[i], parts[j] = parts[j], parts[i]
	}
	h.setRaw("file", "allowed_mime_types", " "+strings.ToUpper(strings.Join(parts, " , "))+",")
	h.seed()
	if h.stored("file", "allowed_mime_types") != settings.DefaultMimeTypes() {
		t.Fatal("reordered/uppercase historical list should still be reset")
	}

	custom := "image/png,text/plain"
	h.setRaw("file", "allowed_mime_types", custom)
	h.seed()
	if h.stored("file", "allowed_mime_types") != custom {
		t.Fatal("customised list must be kept")
	}
	// An old default plus one extra type is a customisation.
	extra := settings.HistoricalMimeLists()[0] + ",application/zip"
	h.setRaw("file", "allowed_mime_types", extra)
	h.seed()
	if h.stored("file", "allowed_mime_types") != extra {
		t.Fatal("old default with an addition must be kept")
	}
	for _, blank := range []string{"", "   "} {
		h.setRaw("file", "allowed_mime_types", blank)
		h.seed()
		if h.stored("file", "allowed_mime_types") != blank {
			t.Fatalf("blank value %q must be left alone", blank)
		}
	}
}

func TestSeedRemovesObsoleteKeys(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	now := time.Now()
	obsolete := [][2]string{
		{"chat", "context_compact_trigger_tokens"}, {"chat", "context_max_input_tokens"},
		{"mcp", "mcp_connect_timeout_ms"}, {"mcp", "mcp_tool_timeout_ms"},
		{"chat", "process_trace_visible_to_user"}, {"chat", "process_trace_store_upstream_think"},
		{"chat", "models_dev_enabled"}, {"chat", "models_dev_url"},
	}
	for _, k := range obsolete {
		h.env.DB.Create(&settings.SystemSetting{Namespace: k[0], Key: k[1], Value: "65536", ValueType: "int", CreatedAt: now, UpdatedAt: now})
	}
	h.env.DB.Create(&settings.SystemSetting{Namespace: "chat", Key: "unrelated_custom", Value: "keep", ValueType: "string", CreatedAt: now, UpdatedAt: now})
	h.seed()
	for _, k := range obsolete {
		if h.rowCount(k[0], k[1]) != 0 {
			t.Errorf("%s/%s should be gone", k[0], k[1])
		}
	}
	if h.rowCount("chat", "unrelated_custom") != 1 {
		t.Error("unknown non-obsolete rows are not touched")
	}
	if h.val("chat", "context_window_fallback_tokens") != "128000" || h.val("chat", "context_compact_trigger_percent") != "80" {
		t.Error("replacement defaults must exist")
	}
}

func TestSeedMigratesHistoricalAllowedPaths(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	def := settings.MarshalDoc(settings.DefaultAllowedDoc())

	older := settings.DefaultAllowedDoc()
	delete(older, "xai_video")
	h.setRaw("chat", "model_option_allowed_paths", settings.MarshalDoc(older))
	h.seed()
	if got := h.stored("chat", "model_option_allowed_paths"); got != def {
		t.Fatalf("previous default not reset: %s", got)
	}

	custom := settings.DefaultAllowedDoc()
	delete(custom, "xai_video")
	custom["default"] = append(custom["default"], "my_param")
	raw := settings.MarshalDoc(custom)
	h.setRaw("chat", "model_option_allowed_paths", raw)
	h.seed()
	if h.stored("chat", "model_option_allowed_paths") != raw {
		t.Fatal("document with a custom path must be kept")
	}
	h.setRaw("chat", "model_option_allowed_paths", "{not json")
	h.seed()
	if h.stored("chat", "model_option_allowed_paths") != "{not json" {
		t.Fatal("unparseable document must be kept")
	}
}

func insertDup(h *harness, ns, key, value string, created, updated time.Time) uint {
	h.t.Helper()
	row := settings.SystemSetting{Namespace: ns, Key: key, Value: value, ValueType: "int", CreatedAt: created, UpdatedAt: updated}
	if err := h.env.DB.Create(&row).Error; err != nil {
		h.t.Fatalf("insert duplicate: %v", err)
	}
	return row.ID
}

func dropUnique(h *harness) {
	h.t.Helper()
	if err := h.env.DB.Exec("DROP INDEX IF EXISTS ux_system_settings_ns_key").Error; err != nil {
		h.t.Fatal(err)
	}
}

func TestSeedCollapsesDuplicateRows(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	dropUnique(h)
	base := h.env.Clock.Now()

	// A touched duplicate wins over the untouched seeded default.
	touched := insertDup(h, "auth", "token_ttl_hours", "48", base, base.Add(time.Hour))
	h.seed()
	if h.rowCount("auth", "token_ttl_hours") != 1 || h.val("auth", "token_ttl_hours") != "48" {
		t.Fatalf("touched row must survive: rows=%d value=%s", h.rowCount("auth", "token_ttl_hours"), h.val("auth", "token_ttl_hours"))
	}
	var kept settings.SystemSetting
	h.env.DB.Where("namespace = ? AND \"key\" = ?", "auth", "token_ttl_hours").First(&kept)
	if kept.ID != touched {
		t.Fatalf("kept id %d want %d", kept.ID, touched)
	}
	var rows int64
	h.env.DB.Model(&settings.SystemSetting{}).Count(&rows)
	if int(rows) != settings.RegistryCount() {
		t.Fatalf("no new row may be inserted: %d", rows)
	}
	// The unique index was rebuilt in the same step.
	err := h.env.DB.Create(&settings.SystemSetting{Namespace: "auth", Key: "token_ttl_hours", Value: "1", ValueType: "int"}).Error
	if err == nil {
		t.Fatal("unique index should be back")
	}
	if !strings.Contains(h.env.Logs.String(), "settings_duplicate_rows") {
		t.Error("operators must be warned about the duplicates")
	}
}

func TestSeedDuplicateTieBreaks(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	dropUnique(h)
	base := h.env.Clock.Now()
	var orig settings.SystemSetting
	h.env.DB.Where("namespace = ? AND \"key\" = ?", "auth", "login_lock_minutes").First(&orig)
	// Same value, same timestamps: the lowest id stays.
	insertDup(h, "auth", "login_lock_minutes", orig.Value, orig.CreatedAt, orig.UpdatedAt)
	insertDup(h, "auth", "login_lock_minutes", orig.Value, orig.CreatedAt, orig.UpdatedAt)
	h.seed()
	var left []settings.SystemSetting
	h.env.DB.Where("namespace = ? AND \"key\" = ?", "auth", "login_lock_minutes").Find(&left)
	if len(left) != 1 || left[0].ID != orig.ID {
		t.Fatalf("lowest id should stay: %+v (orig %d)", left, orig.ID)
	}

	// Both modified: the more recently updated one stays.
	dropUnique(h)
	a := insertDup(h, "auth", "login_max_failures", "7", base, base.Add(time.Hour))
	insertDup(h, "auth", "login_max_failures", "9", base, base.Add(2*time.Hour))
	h.env.DB.Where("id = ?", a).Delete(&settings.SystemSetting{})
	b := insertDup(h, "auth", "login_max_failures", "8", base, base.Add(time.Hour))
	_ = b
	h.seed()
	if v := h.val("auth", "login_max_failures"); v != "9" {
		t.Fatalf("most recent update should win, got %s", v)
	}
}

func TestListingCollapsesDuplicatesWithoutSeed(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	dropUnique(h)
	base := h.env.Clock.Now()
	insertDup(h, "auth", "rate_limit_rpm", "99", base, base.Add(time.Minute))
	var n int
	for _, it := range h.listAll()["auth"] {
		if it.Key == "rate_limit_rpm" {
			n++
			if it.Value != "99" {
				t.Fatalf("effective row should be the touched one: %+v", it)
			}
		}
	}
	if n != 1 {
		t.Fatalf("duplicates must be collapsed in listings: %d", n)
	}
	if err := h.svc.Reload(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.svc.Runtime().Auth.RateLimitRPM != 99 {
		t.Fatalf("runtime should use the effective row: %d", h.svc.Runtime().Auth.RateLimitRPM)
	}
}

func TestSeedSurvivesFailedDuplicateCleanup(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	dropUnique(h)
	base := h.env.Clock.Now()
	insertDup(h, "auth", "login_lock_minutes", "30", base, base.Add(time.Hour))
	if err := h.env.DB.Exec(`CREATE TRIGGER block_delete BEFORE DELETE ON system_settings BEGIN SELECT RAISE(ABORT, 'blocked'); END`).Error; err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Seed(context.Background()); err != nil {
		t.Fatalf("a failed cleanup must not fail the seed: %v", err)
	}
	if h.rowCount("auth", "login_lock_minutes") != 2 {
		t.Fatal("cleanup was expected to fail and leave both rows")
	}
	var rows int64
	h.env.DB.Model(&settings.SystemSetting{}).Count(&rows)
	if int(rows) != settings.RegistryCount()+1 {
		t.Fatalf("no new rows may be inserted for existing keys: %d", rows)
	}
	if !strings.Contains(h.env.Logs.String(), "settings_duplicate_cleanup_failed") {
		t.Error("the failure must be logged")
	}
}
