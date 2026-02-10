package user

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

type srcUser struct {
	id, name, email string
	credit          *float64
}

// openWebUISource writes a SQLite file shaped like the source system.
func openWebUISource(t *testing.T, users []srcUser, withCredit bool) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "webui.db")
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	stmts := []string{`CREATE TABLE "user" (id text, name text, email text)`}
	if withCredit {
		stmts = append(stmts, `CREATE TABLE credit (user_id text, credit real)`)
	}
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, u := range users {
		db.Exec(`INSERT INTO "user" (id, name, email) VALUES (?, ?, ?)`, u.id, u.name, u.email)
		if withCredit && u.credit != nil {
			db.Exec(`INSERT INTO credit (user_id, credit) VALUES (?, ?)`, u.id, *u.credit)
		}
	}
	sdb, _ := db.DB()
	_ = sdb.Close()
	return path
}

func f64(v float64) *float64 { return &v }

type importDoc struct {
	Source                      string
	DedupeField                 string
	DedupeRule                  string
	Scanned                     int
	Imported                    int
	SkippedExistingEmail        int
	SkippedDuplicateSourceEmail int
	SkippedInvalidEmail         int
	SkippedInvalidRow           int
}

func TestAdminImportDryRunAndRealRun(t *testing.T) {
	ae := newAdminEnv(t, "usage")
	ae.seed("already", func(a *Account) { a.Email = "Taken@Example.com" })
	src := openWebUISource(t, []srcUser{
		{"u-1", "Fresh Face", "fresh@example.com", f64(1.5)},
		{"u-2", "No Mail", "  ", f64(3)},
		{"u-3", "Twin", "FRESH@example.com", f64(9)},
		{"u-4", "Exists", "taken@example.com", f64(2)},
	}, true)

	w := ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": src, "creditMultiplier": 2, "dryRun": true})
	ae.expect(w, 200, "")
	var dry importDoc
	testkit.Decode(t, w, &dry)
	if dry.Scanned != 4 || dry.Imported != 1 || dry.SkippedInvalidEmail != 1 || dry.SkippedDuplicateSourceEmail != 1 || dry.SkippedExistingEmail != 1 ||
		dry.Source != "openwebui" || dry.DedupeField != "email" || !strings.Contains(dry.DedupeRule, "skipped without modification") {
		t.Fatalf("dry run: %+v", dry)
	}
	var n int64
	ae.env.DB.Model(&Account{}).Count(&n)
	if n != 2 || len(ae.audits("admin_import_openwebui_users")) != 0 {
		t.Fatalf("dry run creates nothing (%d accounts) and audits nothing", n)
	}

	w = ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "sqlite://" + src, "creditMultiplier": 2})
	ae.expect(w, 200, "")
	var real importDoc
	testkit.Decode(t, w, &real)
	if real.Imported != 1 || real.Scanned != 4 {
		t.Fatalf("real run: %+v", real)
	}
	var fresh Account
	if err := ae.env.DB.Where("email = ?", "fresh@example.com").Take(&fresh).Error; err != nil {
		t.Fatal(err)
	}
	if fresh.Timezone != DefaultTimezone || fresh.Locale != DefaultLocale || fresh.Role != RoleUser || fresh.Status != StatusActive ||
		fresh.EmailSource != EmailSourceProviderVerified || fresh.EmailVerifiedAt != nil || fresh.DisplayName != "Fresh Face" ||
		fresh.Username != "u-1" || !strings.HasPrefix(fresh.PublicID, "ow") || len(fresh.PublicID) != 32 ||
		!strings.Contains(fresh.AppearancePreferences, `"theme":"system"`) {
		t.Fatalf("imported account: %+v", fresh)
	}
	var acc billingAccountRow
	ae.env.DB.Where("user_id = ?", fresh.ID).Take(&acc)
	if acc.BalanceNanousd != 3_000_000_000 || acc.Currency != "USD" || acc.Status != "active" {
		t.Fatalf("balance: %+v", acc)
	}
	var tx balanceTxRow
	ae.env.DB.Where("user_id = ?", fresh.ID).Take(&tx)
	if tx.RefType != "admin_import" || tx.RefNo != "openwebui_import" || tx.Type != "admin_set" || tx.AmountNanousd != 3_000_000_000 ||
		tx.BalanceAfterNanousd != 3_000_000_000 || tx.Description != "OpenWebUI import" {
		t.Fatalf("balance transaction: %+v", tx)
	}
	var cred Credential
	ae.env.DB.Where("user_id = ?", fresh.ID).Take(&cred)
	if cred.PasswordOrigin != PasswordOriginAdminCreated || cred.MustResetPassword || cred.PasswordHash == "" {
		t.Fatalf("credential: %+v", cred)
	}
	logs := ae.audits("admin_import_openwebui_users")
	if len(logs) != 1 || logs[0].ResourceID != "openwebui" || !strings.Contains(logs[0].DetailJSON, `"skipped_existing_email":1`) {
		t.Fatalf("audit: %+v", logs)
	}

	// Running again imports nothing: the account now exists.
	w = ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": src, "creditMultiplier": 2})
	var again importDoc
	testkit.Decode(t, w, &again)
	if again.Imported != 0 || again.SkippedExistingEmail != 2 {
		t.Fatalf("second run: %+v", again)
	}
	if len(ae.audits("admin_import_openwebui_users")) != 2 {
		t.Fatal("every real run is audited, even one that imports nothing")
	}
}

func TestAdminImportWithoutCreditTable(t *testing.T) {
	ae := newAdminEnv(t, "usage")
	src := openWebUISource(t, []srcUser{{"solo", "Solo", "solo@example.com", nil}}, false)
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": src, "creditMultiplier": 5}), 200, "")
	var u Account
	ae.env.DB.Where("email = ?", "solo@example.com").Take(&u)
	var acc billingAccountRow
	ae.env.DB.Where("user_id = ?", u.ID).Take(&acc)
	var txs int64
	ae.env.DB.Model(&balanceTxRow{}).Where("user_id = ?", u.ID).Count(&txs)
	if acc.BalanceNanousd != 0 || txs != 0 {
		t.Fatalf("no credit: zero balance and no transaction: %+v %d", acc, txs)
	}
}

func TestAdminImportRejections(t *testing.T) {
	ae := newAdminEnv(t, "usage")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "x", "creditMultiplier": -1}), 400, "request.invalid_import_credit_multiplier")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "x"}), 400, "request.invalid_body")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"creditMultiplier": 1}), 400, "request.invalid_body")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "   ", "creditMultiplier": 1}), 400, "request.invalid_import_dsn")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "sqlite://", "creditMultiplier": 1}), 400, "request.invalid_import_dsn")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": filepath.Join(t.TempDir(), "missing.db"), "creditMultiplier": 1}), 500, "internal.error")
	ae.expect(ae.do("POST", "/admin/users/import/openwebui", map[string]any{"dsn": "postgres://nobody@127.0.0.1:1/none?sslmode=disable&connect_timeout=1", "creditMultiplier": 1}), 500, "internal.error")
	// A missing acting row is an internal error on this route.
	ghost := Account{Role: RoleAdmin}
	ghost.ID = 4321
	ae.expect(ae.doAs(ghost, "POST", "/admin/users/import/openwebui", map[string]any{"dsn": "x", "creditMultiplier": 1}), 500, "internal.error")
	plain := ae.seed("plainjane")
	ae.expect(ae.doAs(plain, "POST", "/admin/users/import/openwebui", map[string]any{"dsn": "x", "creditMultiplier": 1}), 403, "auth.admin_required")
}

func TestImportUsernameDerivation(t *testing.T) {
	hash := "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"
	used := map[string]bool{}
	cases := []struct{ id, want string }{
		{"Alice.Smith", "alice-smith"},
		{"a..b:/c  d", "a-b-c-d"},
		{"--x__y--", "x__y"},
		{"UPPER_case9", "upper_case9"},
		{"ünï", "owui-01234567"},
		{"admin", "owui-01234567"},
		{"ab", "owui-01234567"},
		{"a-very-long-source-identifier-here", "a-very-long-sour"},
	}
	for _, tc := range cases {
		if got := pickUsername(tc.id, hash, map[string]bool{}); got != tc.want {
			t.Errorf("%q -> %q, want %q", tc.id, got, tc.want)
		}
	}
	first := pickUsername("same.name", hash, used)
	used[first] = true
	second := pickUsername("same-name", hash, used)
	used[second] = true
	if first != "same-name" || second != "same-name-0123" {
		t.Fatalf("collision appends a hash chunk: %q %q", first, second)
	}
	third := pickUsername("same_name_is_long_enough", hash, map[string]bool{"same_name_is_lo": true})
	if third == "same_name_is_lo" {
		t.Fatal("must not reuse")
	}
	used["owui-01234567"] = true
	got := pickUsername("admin", hash, used)
	if got == "owui-01234567" || !strings.HasPrefix(got, "owui-") || len(got) != 16 {
		t.Fatalf("the fallback name is taken too, so an 11-hex window is used: %q", got)
	}
}

func TestImportHelpers(t *testing.T) {
	if got := creditToNano(1.5, 2); got != 3_000_000_000 {
		t.Fatalf("credit: %d", got)
	}
	for _, c := range [][2]float64{{0, 5}, {-1, 5}, {5, 0}} {
		if creditToNano(c[0], c[1]) != 0 {
			t.Fatalf("%v should be 0", c)
		}
	}
	if creditToNano(1e30, 1e30) != 1<<63-1 {
		t.Fatal("saturates")
	}
	for in, want := range map[string]string{"": "user", "  Bo  ": "Bo_", "x": "x__", "abcdefghijklmnopqrstuvwxyz": "abcdefghijklmnop"} {
		if got := importDisplayName(in); got != want {
			t.Errorf("%q -> %q, want %q", in, got, want)
		}
	}
}

func TestAdminRoutesGatedWhenSingleUser(t *testing.T) {
	env := newEnv(t, Ports{}, testkit.WithConfig(func(c *config.Config) { c.Local = config.LocalConfig{Enabled: true, DataDir: "/x"} }))
	admin := env.Bearer(testkit.Admin(1))
	for _, p := range []struct{ method, path string }{
		{"GET", "/admin/users"}, {"POST", "/admin/users"}, {"PATCH", "/admin/users/1"}, {"DELETE", "/admin/users/1"},
		{"PATCH", "/admin/users/1/status"}, {"POST", "/admin/users/1/reset-password"}, {"POST", "/admin/users/1/reset-2fa"},
		{"POST", "/admin/users/1/revoke-sessions"}, {"POST", "/admin/users/import/openwebui"}, {"GET", "/admin/user-auth-events"},
	} {
		w := env.Do(p.method, "/api/v1"+p.path, admin, nil)
		testkit.Expect(t, w, 404, "feature.disabled")
		if !strings.Contains(w.Body.String(), fmt.Sprintf(`"feature":%q`, "multiUser")) {
			t.Fatalf("%s %s details: %s", p.method, p.path, w.Body.String())
		}
	}
	testkit.Expect(t, env.Do("GET", "/api/v1/admin/users", env.Bearer(testkit.User(2)), nil), 403, "auth.admin_required")
}
