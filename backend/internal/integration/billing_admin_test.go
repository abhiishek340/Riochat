package integration_test

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/audit"
	"github.com/riochat/riochat/backend/internal/features/auth"
	"github.com/riochat/riochat/backend/internal/features/billing"
	"github.com/riochat/riochat/backend/internal/features/contentmoderation"
	"github.com/riochat/riochat/backend/internal/features/conversation"
	"github.com/riochat/riochat/backend/internal/features/errorlog"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/registry"
	"github.com/riochat/riochat/backend/internal/testkit"
)

const cleanupURL = "/api/v1/admin/logs/cleanup"

type cleanupOut struct {
	Type         string `json:"type"`
	Before       string `json:"before"`
	DeletedCount int64  `json:"deletedCount"`
}

func TestLogCleanupValidationAndAccess(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "ann@example.com", "")
	admin := session(t, env, "root@example.com", "admin")

	good := map[string]any{"type": "usage", "before": "2000-01-01T00:00:00Z"}
	w := env.Do("POST", cleanupURL, admin, good)
	testkit.Expect(t, w, 200, "")
	var out cleanupOut
	dataOf(t, w.Body.Bytes(), &out)
	if out.Type != "usage" || out.Before != "2000-01-01T00:00:00Z" || out.DeletedCount != 0 {
		t.Fatalf("dry run: %+v (%s)", out, w.Body)
	}
	testkit.Expect(t, env.Do("POST", cleanupURL, user, good), 403, "auth.admin_required")
	testkit.Expect(t, env.Do("POST", cleanupURL, nil, good), 401, "auth.invalid_token")
	testkit.Expect(t, env.Do("POST", cleanupURL, admin, nil), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", cleanupURL, admin, map[string]any{}), 400, "request.invalid_body")
	testkit.Expect(t, env.Do("POST", cleanupURL, admin, map[string]any{"type": "runs", "before": "2000-01-01T00:00:00Z"}), 400, "request.invalid_log_cleanup_type")
	testkit.Expect(t, env.Do("POST", cleanupURL, admin, map[string]any{"type": "usage", "before": "yesterday"}), 400, "request.invalid_log_cleanup_before")
	future := env.Clock.Now().Add(time.Hour).Format(time.RFC3339)
	testkit.Expect(t, env.Do("POST", cleanupURL, admin, map[string]any{"type": "usage", "before": future}), 400, "log_cleanup.before_in_future")
	// every one of the seven types is known
	for _, typ := range []string{"audit", "auth", "usage", "orders", "conversation", "moderation", "errors", " Errors "} {
		testkit.Expect(t, env.Do("POST", cleanupURL, admin, map[string]any{"type": typ, "before": "2000-01-01T00:00:00Z"}), 200, "")
	}
}

// Each type deletes only its own table, only rows strictly older than the
// cutoff, and the purge is itself audited.
func TestLogCleanupDeletesEachLogType(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "root@example.com", "admin")
	old := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	cut := time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC)
	recent := time.Date(2026, 2, 20, 0, 0, 0, 0, time.UTC)

	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	count := func(table, where string, args ...any) int64 {
		t.Helper()
		var n int64
		must(env.DB.Table(table).Unscoped().Where(where, args...).Count(&n).Error)
		return n
	}

	// seed: one row older than the cutoff, one exactly at it, one newer
	for i, ts := range []time.Time{old, cut, recent} {
		tag := fmt.Sprintf("clean%d", i)
		must(env.DB.Create(&audit.Log{Action: "seed." + tag, Resource: "seed", Model: database.Model{CreatedAt: ts, UpdatedAt: ts}}).Error)
		must(env.DB.Create(&auth.AuthEvent{Model: database.Model{CreatedAt: ts, UpdatedAt: ts}, EventType: "seed." + tag, Result: "success", OccurredAt: ts}).Error)
		must(env.DB.Create(&billing.UsageLedger{Model: database.Model{CreatedAt: ts, UpdatedAt: ts}, RefNo: tag, PlatformModelName: "seed", BillingAt: ts}).Error)
		must(env.DB.Create(&billing.PaymentOrder{Model: database.Model{CreatedAt: ts, UpdatedAt: ts}, OrderNo: "ord-" + tag, Provider: "stripe", Status: "pending"}).Error)
		must(env.DB.Create(&conversation.RunEvent{Model: database.Model{CreatedAt: ts, UpdatedAt: ts}, RunID: "run-" + tag, EventScope: "trace_event", EventID: tag}).Error)
		must(env.DB.Create(&errorlog.Entry{RequestID: "req-" + tag, ErrorCode: "seed.error", CreatedAt: ts}).Error)
		must(env.DB.Create(&contentmoderation.Event{Model: database.Model{CreatedAt: ts, UpdatedAt: ts}, PublicID: "cmev-" + tag, Result: "pass",
			CategoriesJSON: "[]", CategoryScoresJSON: "{}", ImageMetaJSON: "[]", ContentLocationJSON: "{}",
			ContentExpiresAt: recent, MetadataExpiresAt: recent}).Error)
	}

	cases := []struct {
		typ, table, where string
	}{
		{"audit", "audit_logs", "action LIKE 'seed.%'"},
		{"auth", "identity_auth_events", "event_type LIKE 'seed.%'"},
		{"usage", "billing_usage_ledgers", "platform_model_name = 'seed'"},
		{"orders", "billing_payment_orders", "order_no LIKE 'ord-clean%'"},
		{"conversation", "chat_run_events", "run_id LIKE 'run-clean%'"},
		{"errors", "system_error_logs", "error_code = 'seed.error'"},
		{"moderation", "content_moderation_events", "public_id LIKE 'cmev-clean%'"},
	}
	// nothing is touched by a request for another type
	for _, tc := range cases {
		if n := count(tc.table, tc.where); n != 3 {
			t.Fatalf("%s seeded %d rows", tc.typ, n)
		}
	}
	for i, tc := range cases {
		w := env.Do("POST", cleanupURL, admin, map[string]any{"type": tc.typ, "before": cut.Format(time.RFC3339)})
		testkit.Expect(t, w, 200, "")
		var out cleanupOut
		dataOf(t, w.Body.Bytes(), &out)
		if out.Type != tc.typ || out.DeletedCount != 1 || out.Before != cut.Format(time.RFC3339) {
			t.Fatalf("%s: %+v (%s)", tc.typ, out, w.Body)
		}
		if n := count(tc.table, tc.where); n != 2 {
			t.Fatalf("%s: %d rows left, want the cutoff row and the newer row", tc.typ, n)
		}
		for _, other := range cases[i+1:] { // types not purged yet keep all three rows
			if n := count(other.table, other.where); n != 3 {
				t.Fatalf("purging %s touched %s: %d", tc.typ, other.typ, n)
			}
		}
	}
	// the rows older than the cutoff are really gone, bypassing soft delete
	if n := count("audit_logs", "action = 'seed.clean0'"); n != 0 {
		t.Fatalf("soft deleted instead of purged: %d", n)
	}
	// each purge left an audit record
	if n := count("audit_logs", "action = ? AND resource = ?", "admin_cleanup_logs", "logs"); n != int64(len(cases)) {
		t.Fatalf("cleanup audit records: %d", n)
	}
	var detail string
	must(env.DB.Table("audit_logs").Where("action = ? AND resource_id = ?", "admin_cleanup_logs", "errors").Select("detail_json").Scan(&detail).Error)
	if !strings.Contains(detail, `"deleted_count":1`) || !strings.Contains(detail, `"type":"errors"`) {
		t.Fatalf("detail %s", detail)
	}
}

// The configuration the server returns can be saved back unchanged through the
// real settings validation, and a single edited price persists.
func TestBillingConfigNativeToolPricingRoundTrip(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "root@example.com", "admin")

	read := func() (mode string, items []map[string]any) {
		t.Helper()
		w := env.Do("GET", "/api/v1/admin/billing/config", admin, nil)
		testkit.Expect(t, w, 200, "")
		var out struct {
			Config struct {
				Mode              string           `json:"mode"`
				NativeToolPricing []map[string]any `json:"nativeToolPricing"`
			} `json:"config"`
		}
		dataOf(t, w.Body.Bytes(), &out)
		return out.Config.Mode, out.Config.NativeToolPricing
	}
	mode, items := read()
	if len(items) < 20 {
		t.Fatalf("only %d native tools", len(items))
	}
	keys := map[string]bool{}
	for _, it := range items {
		k, _ := it["toolKey"].(string)
		if !strings.Contains(k, ".") {
			t.Fatalf("tool key %q is not provider-prefixed", k)
		}
		keys[k] = true
	}
	for _, want := range []string{"openai.web_search", "anthropic.web_search_20250305", "xai.web_search", "google.url_context"} {
		if !keys[want] {
			t.Fatalf("missing key %s", want)
		}
	}
	w := env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": mode, "nativeToolPricing": items})
	testkit.Expect(t, w, 200, "")
	for _, it := range items {
		single := []map[string]any{{"toolKey": it["toolKey"], "priceNanousd": it["priceNanousd"]}}
		testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": mode, "nativeToolPricing": single}), 200, "")
	}
	edit := []map[string]any{{"toolKey": "openai.web_search", "priceNanousd": 31_000_000}}
	testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": mode, "nativeToolPricing": edit}), 200, "")
	_, items = read()
	for _, it := range items {
		if it["toolKey"] == "openai.web_search" && it["priceNanousd"].(float64) != 31_000_000 {
			t.Fatalf("edited price not kept: %v", it)
		}
	}
	// the raw settings route and the billing route agree on the stored document
	var raw string
	if err := env.DB.Table("system_settings").Where("namespace = ? AND "+"\"key\""+" = ?", "billing", "native_tool_pricing_json").Select("value").Scan(&raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(raw, `"openai.web_search"`) {
		t.Fatalf("stored document: %s", raw)
	}
	testkit.Expect(t, env.Do("PATCH", "/api/v1/admin/billing/config", admin, map[string]any{"mode": mode,
		"nativeToolPricing": []map[string]any{{"toolKey": "web_search", "priceNanousd": 1}}}), 400, "billing.invalid_native_tool_pricing")
}

// Every field of the overview is present whatever the billing mode.
func TestBillingOverviewIsAlwaysTheFullObject(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	user := session(t, env, "ann@example.com", "")
	w := env.Do("GET", "/api/v1/billing/overview", user, nil)
	testkit.Expect(t, w, 200, "")
	var out struct {
		Overview map[string]any `json:"overview"`
	}
	dataOf(t, w.Body.Bytes(), &out)
	for _, k := range []string{"mode", "plan", "periodStartAt", "periodEndAt", "periodCreditUSD", "periodCreditNanousd",
		"periodUsedUSD", "periodUsedNanousd", "periodRemainingUSD", "periodRemainingNanousd", "account",
		"totalSpentUSD", "totalSpentNanousd", "subscriptionEntitlements"} {
		if _, ok := out.Overview[k]; !ok {
			t.Fatalf("overview lacks %s: %s", k, w.Body)
		}
	}
}

// Binding a new platform model to an upstream that already has a binding must
// answer even after the cached breaker settings expired (the SQLite
// connection is single, so a read through the pool inside the transaction
// would wait on itself).
func TestUpstreamBindingNewPlatformModelAnswers(t *testing.T) {
	env := testkit.New(t, registry.Registered())
	admin := session(t, env, "root@example.com", "admin")
	srv := mockUpstream(t, 200, nil)
	addModel(t, env, admin, srv.URL+"/v1", "first-model")
	var upID uint
	if err := env.DB.Table("llm_upstreams").Select("id").Scan(&upID).Error; err != nil || upID == 0 {
		t.Fatalf("upstream: %d %v", upID, err)
	}
	for i := 2; i <= 4; i++ {
		env.Clock.Advance(time.Minute)
		done := make(chan int, 1)
		go func() {
			w := env.Do("POST", fmt.Sprintf("/api/v1/admin/llm/upstreams/%d/models", upID), admin, map[string]any{
				"platformModelName": fmt.Sprintf("billing-admin-m%d", i), "upstreamModelName": fmt.Sprintf("ba-mock-%d", i),
				"protocols": []string{"openai_chat_completions"}, "kindsJSON": `["chat"]`,
			})
			done <- w.Code
		}()
		select {
		case code := <-done:
			if code != 200 {
				t.Fatalf("binding %d answered %d", i, code)
			}
		case <-time.After(15 * time.Second):
			t.Fatalf("binding %d never answered (self-deadlock on the database connection)", i)
		}
	}
}
