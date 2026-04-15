package settings_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/features"
	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/testkit"
)

func TestAuxiliaryCache(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	ctx := ctxBG()
	c := h.env.Deps.Cache

	// loading the runtime writes every row through
	raw, found, err := c.Get(ctx, "settings:auth:token_ttl_hours")
	if err != nil || !found || string(raw) != "24" {
		t.Fatalf("write-through on load: %q %v %v", raw, found, err)
	}
	// sensitive values are never cached in clear
	h.mustPatch(set("auth", "smtp_password", "hunter2"))
	if _, found, _ := c.Get(ctx, "settings:auth:smtp_password"); found {
		// after a patch the entry is invalidated and the reload re-stores the ciphertext only
		raw, _, _ := c.Get(ctx, "settings:auth:smtp_password")
		if string(raw) == "hunter2" {
			t.Fatal("plaintext secret cached")
		}
	}
	// a patch invalidates the entry, then the reload stores the fresh value
	h.mustPatch(set("auth", "token_ttl_hours", "40"))
	raw, found, _ = c.Get(ctx, "settings:auth:token_ttl_hours")
	if !found || string(raw) != "40" {
		t.Fatalf("cache after patch: %q %v", raw, found)
	}
}

func TestCacheFailuresAreIgnored(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	if err := h.env.Deps.Cache.Close(); err != nil {
		t.Fatal(err)
	}
	h.mustPatch(set("auth", "token_ttl_hours", "50"))
	if h.svc.Runtime().Auth.TokenTTLHours != 50 {
		t.Fatal("write must succeed with a broken cache")
	}
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatalf("load must succeed with a broken cache: %v", err)
	}
}

func TestEmptyNamespaceIsAbsentFromListing(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.env.DB.Where("namespace = ?", "circuit").Delete(&settings.SystemSetting{})
	if _, ok := h.listAll()["circuit"]; ok {
		t.Fatal("namespace without rows must be absent")
	}
	w := h.do("GET", "/api/v1/admin/settings/circuit", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	var items []item
	testkit.Decode(t, w, &items)
	if items == nil || len(items) != 0 {
		t.Fatalf("namespace listing must be an empty array: %s", w.Body)
	}
	// module-read values fall back to registry defaults when the rows are gone
	vals, err := h.svc.NamespaceValues(ctxBG(), "circuit")
	if err != nil || vals["channel_failure_threshold"] != "3" || vals["channel_circuit_open_seconds"] != "60" {
		t.Fatalf("defaults: %v %v", vals, err)
	}
	// a patch re-creates a missing row with the registry type and description
	h.mustPatch(set("circuit", "channel_failure_threshold", "9"))
	it := h.find("circuit", "channel_failure_threshold")
	if it.Value != "9" || it.ValueType != "int" || it.Description == "" {
		t.Fatalf("recreated row: %+v", it)
	}
}

func TestPatchKeepsDescriptionAndType(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.env.DB.Model(&settings.SystemSetting{}).Where("namespace = ? AND \"key\" = ?", "auth", "token_ttl_hours").Update("description", "operator note")
	h.advance(time.Hour)
	h.mustPatch(set("auth", "token_ttl_hours", "60"))
	var row settings.SystemSetting
	h.env.DB.Where("namespace = ? AND \"key\" = ?", "auth", "token_ttl_hours").First(&row)
	if row.Description != "operator note" || row.ValueType != "int" || row.Value != "60" {
		t.Fatalf("row: %+v", row)
	}
	if !row.UpdatedAt.After(row.CreatedAt) {
		t.Fatalf("updated_at must move: %v %v", row.CreatedAt, row.UpdatedAt)
	}
}

func TestConcurrentPatchesAndReads(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			w := h.patch(set("auth", "login_max_failures", fmt.Sprint(i)), set("chat", "max_context_messages", fmt.Sprint(10+i)))
			if w.Code != 200 {
				t.Errorf("patch %d: %d %s", i, w.Code, w.Body)
			}
		}(i)
		go func() {
			defer wg.Done()
			_ = h.svc.Runtime().Auth.LoginMaxFailures
			if w := h.do("GET", "/api/v1/admin/settings", h.admin, nil); w.Code != 200 {
				t.Errorf("list: %d", w.Code)
			}
		}()
	}
	wg.Wait()
	rt := h.svc.Runtime()
	if rt.Auth.LoginMaxFailures < 0 || rt.Auth.LoginMaxFailures > 7 || rt.Chat.MaxContextMessages != 10+rt.Auth.LoginMaxFailures {
		t.Fatalf("batches must apply atomically: failures=%d messages=%d", rt.Auth.LoginMaxFailures, rt.Chat.MaxContextMessages)
	}
}

func TestSnapshotsAreImmutable(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	before := h.svc.Runtime()
	h.mustPatch(set("auth", "token_ttl_hours", "99"))
	if before.Auth.TokenTTLHours == 99 {
		t.Fatal("a snapshot already handed out must never change")
	}
	if h.svc.Runtime().Auth.TokenTTLHours != 99 {
		t.Fatal("new snapshot published")
	}
}

func TestReloadFollowsExternalChanges(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.setRaw("storage", "max_message_files", "33")
	if h.svc.Runtime().Storage.MaxMessageFiles == 33 {
		t.Fatal("not yet applied")
	}
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
	if h.svc.Runtime().Storage.MaxMessageFiles != 33 {
		t.Fatal("reload applies stored rows")
	}
	// an unparseable stored value keeps the previous value
	h.setRaw("storage", "max_message_files", "many")
	_ = h.svc.Reload(ctxBG())
	if h.svc.Runtime().Storage.MaxMessageFiles != 33 {
		t.Fatalf("parse failure must keep the previous value: %d", h.svc.Runtime().Storage.MaxMessageFiles)
	}
	// unregistered rows never reach the runtime
	h.env.DB.Create(&settings.SystemSetting{Namespace: "storage", Key: "ghost", Value: "1", ValueType: "int"})
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
}

func TestEveryRuntimeFieldFollowsItsSetting(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	rt := h.svc.Runtime()
	if rt.Chat.RAGMinSimilarity != 0.45 || rt.Chat.RAGTokenBudget != 2000 || rt.Storage.UserStorageQuotaBytes != 104857600 ||
		rt.File.EmbeddingOutputDimensions != 1536 || rt.File.EmbeddingProtocol != "openai" || rt.Extract.Engine != "builtin" ||
		rt.Extract.TikaBaseURL != "http://127.0.0.1:9998" || rt.Extract.MinerUFileTypes != "pdf,word,presentation" ||
		rt.MCP.MaxConcurrentCalls != 8 || rt.PersonalProvider.MaxPerUser != 10 || !rt.KnowledgeBase.Enabled || rt.Chat.ConversationTaskModel != "follow" {
		t.Fatalf("runtime defaults: %+v", rt)
	}
	h.mustPatch(
		set("chat", "rag_min_similarity", "0.7"), set("extract", "engine", "docling"), set("extract", "docling_auth_token", "tok"),
		set("mcp", "mcp_enable", "true"), set("personal_provider", "enabled", "true"), set("personal_provider", "blocked_hosts", "a.com"),
	)
	rt = h.svc.Runtime()
	if rt.Chat.RAGMinSimilarity != 0.7 || rt.Extract.Engine != "docling" || rt.Extract.DoclingAuthToken != "tok" ||
		!rt.MCP.Enable || !rt.PersonalProvider.Enabled || rt.PersonalProvider.BlockedHosts != "a.com" {
		t.Fatalf("runtime after patch: %+v", rt)
	}
	// secrets are decrypted in the runtime
	if rt.Extract.DoclingAuthToken == h.stored("extract", "docling_auth_token") {
		t.Fatal("runtime must hold plaintext, storage ciphertext")
	}
}

func TestUserSettingsRoutesCanBeLeftToTheDedicatedArea(t *testing.T) {
	var svc *settings.Service
	env := testkit.New(t, []features.Feature{{
		Name: "settings", Migrations: settings.Migrations,
		Mount: func(d deps.Deps) error {
			var err error
			svc, err = settings.MountWith(d, settings.Ports{}, settings.WithoutUserSettingsRoutes())
			return err
		},
	}})
	user := env.Bearer(testkit.User(7))
	testkit.Expect(t, env.Do("GET", "/api/v1/user/settings", user, nil), 404, "")
	testkit.Expect(t, env.Do("GET", "/api/v1/settings/mcp-policy", user, nil), 200, "")
	got, err := svc.UserSettings(ctxBG(), 7)
	if err != nil || len(got) != 23 {
		t.Fatalf("service still works: %v %v", len(got), err)
	}
}

// The billing area keeps its own audit trail, so the in-process batch helper it
// uses must not add a second, actor-less settings.update entry.
func TestPatchValuesWritesNoGenericAuditEntry(t *testing.T) {
	audit := &fakeAudit{}
	h := newHarness(t, settings.Ports{Audit: audit})
	if err := h.svc.PatchValues(ctxBG(), "billing", map[string]string{"mode": "usage"}); err != nil {
		t.Fatal(err)
	}
	if n := len(audit.all()); n != 0 {
		t.Fatalf("PatchValues audited %d entries", n)
	}
	h.mustPatch(set("billing", "mode", "period"))
	if n := len(audit.all()); n != 1 {
		t.Fatalf("administrator patch must still be audited, got %d", n)
	}
}

func TestBlockedHostsAcceptIPLiterals(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("personal_provider", "blocked_hosts", "10.0.0.1 ::1 2001:db8::7 *.evil.com"))
	h.expectInvalid(h.patch(set("personal_provider", "blocked_hosts", "[::1]:80")),
		"settings.invalid_value", "personal_provider:blocked_hosts", "domain", "-")
	h.expectInvalid(h.patch(set("personal_provider", "blocked_hosts", "host:8080")),
		"settings.invalid_value", "personal_provider:blocked_hosts", "domain", "-")
}
