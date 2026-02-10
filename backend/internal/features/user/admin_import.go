package user

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/riochat/riochat/backend/internal/platform/httpx"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
)

const (
	importSource       = "openwebui"
	importDedupeField  = "email"
	importDedupeRule   = "Existing users are matched by email and skipped without modification."
	importRefNo        = "openwebui_import"
	importTimezone     = DefaultTimezone
	importLocale       = DefaultLocale
	importAppearance   = `{"theme":"system","preset":"default","fontSize":"standard","font":"default","fontWeight":"default"}`
	importNameMax      = 16
	importNameMin      = 3
	importUsernameMax  = 16
	importFallbackBase = "owui-"
)

type importBody struct {
	DSN              string   `json:"dsn" binding:"required,max=2048"`
	CreditMultiplier *float64 `json:"creditMultiplier" binding:"required"`
	DryRun           bool     `json:"dryRun"`
}

// sourceUser is one row of the OpenWebUI user table with its credit balance.
type sourceUser struct {
	ID     string
	Name   string
	Email  string
	Credit float64
}

// ImportReport carries the counters of an import run.
type ImportReport struct {
	Source                      string `json:"source"`
	DedupeField                 string `json:"dedupeField"`
	DedupeRule                  string `json:"dedupeRule"`
	Scanned                     int    `json:"scanned"`
	Imported                    int    `json:"imported"`
	SkippedExistingEmail        int    `json:"skippedExistingEmail"`
	SkippedDuplicateSourceEmail int    `json:"skippedDuplicateSourceEmail"`
	SkippedInvalidEmail         int    `json:"skippedInvalidEmail"`
	SkippedInvalidRow           int    `json:"skippedInvalidRow"`
}

func (a *adminAPI) importOpenWebUI(c *gin.Context) {
	var req importBody
	if !httpx.BindJSON(c, &req) {
		return
	}
	mult := *req.CreditMultiplier
	if mult < 0 {
		httpx.Fail(c, http.StatusBadRequest, ErrInvalidImportMultiplier)
		return
	}
	if _, ok := a.actorForImport(c); !ok {
		return
	}
	ctx := c.Request.Context()
	rows, err := a.readSource(ctx, req.DSN)
	if err != nil {
		fail(c, err)
		return
	}
	report, records, err := a.planImport(ctx, rows, mult)
	if err != nil {
		fail(c, err)
		return
	}
	if !req.DryRun {
		created, err := a.accounts.ImportWithBalances(ctx, records)
		if err != nil {
			fail(c, err)
			return
		}
		report.Imported = len(created)
		a.recordResource(c, "admin_import_openwebui_users", importSource, gin.H{
			"source": importSource, "dedupe_field": importDedupeField, "scanned": report.Scanned, "imported": report.Imported,
			"skipped_existing_email": report.SkippedExistingEmail, "skipped_duplicate_source_email": report.SkippedDuplicateSourceEmail,
			"skipped_invalid_email": report.SkippedInvalidEmail, "skipped_invalid_row": report.SkippedInvalidRow,
		})
	}
	httpx.OK(c, report)
}

// actorForImport requires an admin like the other routes, except that a missing
// acting row is an internal error here.
func (a *adminAPI) actorForImport(c *gin.Context) (*Account, bool) {
	acting, err := a.accounts.GetByID(c.Request.Context(), middleware.MustPrincipal(c).UserID)
	if err != nil {
		if err == ErrNotFound {
			httpx.Fail(c, http.StatusInternalServerError, errors.New("import: acting user not found"))
			return nil, false
		}
		fail(c, err)
		return nil, false
	}
	if !isAdminRole(acting.Role) {
		httpx.Fail(c, http.StatusForbidden, ErrAdminOnly)
		return nil, false
	}
	return acting, true
}

// ---- source ------------------------------------------------------------------------------

// openSource connects to the source database named by the DSN.
func (a *adminAPI) openSource(dsn string) (*gorm.DB, error) {
	dsn = strings.TrimSpace(dsn)
	if dsn == "" {
		return nil, ErrInvalidImportDSN
	}
	lower := strings.ToLower(dsn)
	gcfg := &gorm.Config{Logger: gormlogger.Discard}
	switch {
	case strings.HasPrefix(lower, "postgres://") || strings.HasPrefix(lower, "postgresql://"):
		if a.cfg != nil && a.cfg.Load().IsLocal() {
			return nil, ErrInvalidImportDSN
		}
		if u, err := url.Parse(dsn); err != nil || u.Host == "" && u.Path == "" {
			return nil, ErrInvalidImportDSN
		}
		return gorm.Open(postgres.Open(dsn), gcfg)
	default:
		path := dsn
		if strings.HasPrefix(lower, "sqlite://") {
			path = strings.TrimSpace(dsn[len("sqlite://"):])
			if path == "" {
				return nil, ErrInvalidImportDSN
			}
		}
		return gorm.Open(sqlite.Open(sqliteReadOnly(path)), gcfg)
	}
}

// sqliteReadOnly turns a file path into a read-only URI so that a wrong path is
// reported instead of creating an empty database.
func sqliteReadOnly(path string) string {
	esc := strings.NewReplacer("%", "%25", "?", "%3f", "#", "%23").Replace(path)
	return "file:" + esc + "?mode=ro"
}

// readSource loads the user rows (and credit balances) of the source.
func (a *adminAPI) readSource(ctx context.Context, dsn string) ([]sourceUser, error) {
	src, err := a.openSource(dsn)
	if err != nil {
		return nil, err
	}
	defer func() {
		if sdb, err := src.DB(); err == nil {
			_ = sdb.Close()
		}
	}()
	db := src.WithContext(ctx)
	type row struct {
		ID    sql.NullString
		Name  sql.NullString
		Email sql.NullString
	}
	var users []row
	if err := db.Raw(`SELECT CAST(id AS TEXT) AS id, name, email FROM "user" ORDER BY 1`).Scan(&users).Error; err != nil {
		return nil, fmt.Errorf("read import source: %w", err)
	}
	credit := map[string]float64{}
	if db.Migrator().HasTable("credit") {
		type crow struct {
			UserID sql.NullString
			Credit sql.NullFloat64
		}
		var cs []crow
		if err := db.Raw(`SELECT CAST(user_id AS TEXT) AS user_id, credit FROM credit`).Scan(&cs).Error; err != nil {
			return nil, fmt.Errorf("read import credits: %w", err)
		}
		for _, r := range cs {
			if r.UserID.Valid && r.Credit.Valid {
				credit[r.UserID.String] = r.Credit.Float64
			}
		}
	}
	out := make([]sourceUser, 0, len(users))
	for _, u := range users {
		out = append(out, sourceUser{ID: u.ID.String, Name: u.Name.String, Email: u.Email.String, Credit: credit[u.ID.String]})
	}
	return out, nil
}

// ---- planning -----------------------------------------------------------------------------

type candidate struct {
	key, email string
	row        sourceUser
}

// planImport classifies the source rows and builds the records to create.
func (a *adminAPI) planImport(ctx context.Context, rows []sourceUser, multiplier float64) (ImportReport, []ImportRecord, error) {
	report := ImportReport{Source: importSource, DedupeField: importDedupeField, DedupeRule: importDedupeRule, Scanned: len(rows)}
	seen := map[string]bool{}
	var cands []candidate
	for _, r := range rows {
		email, err := NormalizeEmail(r.Email)
		if err != nil || email == "" {
			report.SkippedInvalidEmail++
			continue
		}
		lower := strings.ToLower(email)
		if seen[lower] {
			report.SkippedDuplicateSourceEmail++
			continue
		}
		seen[lower] = true
		key := strings.TrimSpace(r.ID)
		if key == "" {
			key = email
		}
		if key == "" {
			report.SkippedInvalidRow++
			continue
		}
		cands = append(cands, candidate{key: key, email: email, row: r})
	}
	emails := make([]string, 0, len(cands))
	for _, c := range cands {
		emails = append(emails, c.email)
	}
	existing, err := a.accounts.UsersByEmails(ctx, emails)
	if err != nil {
		return report, nil, err
	}
	taken, err := a.accounts.AllUsernames(ctx)
	if err != nil {
		return report, nil, err
	}
	used := make(map[string]bool, len(taken))
	for n := range taken {
		used[strings.ToLower(n)] = true
	}

	var records []ImportRecord
	for _, c := range cands {
		if _, dup := existing[strings.ToLower(c.email)]; dup {
			report.SkippedExistingEmail++
			continue
		}
		digest := sha256.Sum256([]byte(c.key + "\x00" + strings.ToLower(c.email)))
		hexHash := hex.EncodeToString(digest[:])
		username := pickUsername(c.row.ID, hexHash, used)
		used[username] = true

		hash, err := randomPasswordHash()
		if err != nil {
			return report, nil, err
		}
		records = append(records, ImportRecord{
			Account: Account{
				PublicID: "ow" + hexHash[:30], Username: username, DisplayName: importDisplayName(c.row.Name),
				Email: c.email, Role: RoleUser, Status: StatusActive, Timezone: importTimezone, Locale: importLocale,
				EmailSource: EmailSourceProviderVerified, AppearancePreferences: importAppearance,
			},
			Credential:     Credential{PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: true, PasswordOrigin: PasswordOriginAdminCreated},
			BalanceNanoUSD: creditToNano(c.row.Credit, multiplier),
			RefNo:          importRefNo,
		})
	}
	report.Imported = len(records)
	return report, records, nil
}

// randomPasswordHash hashes 32 random bytes: nobody knows the password.
func randomPasswordHash() (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hashPassword(base64.RawStdEncoding.EncodeToString(raw))
}

// creditToNano converts a source credit into nano-USD, never negative and
// saturating at the largest int64.
func creditToNano(credit, multiplier float64) int64 {
	if credit <= 0 || multiplier <= 0 || math.IsNaN(credit) || math.IsInf(credit, 0) || math.IsNaN(multiplier) || math.IsInf(multiplier, 0) {
		return 0
	}
	v := math.Round(credit * multiplier * 1e9)
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v >= float64(math.MaxInt64) {
		return math.MaxInt64
	}
	return int64(v)
}

// importDisplayName trims the source name, falls back to "user", cuts it to 16
// characters and pads with underscores up to three.
func importDisplayName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "user"
	}
	if r := []rune(name); len(r) > importNameMax {
		name = string(r[:importNameMax])
	}
	for utf8.RuneCountInString(name) < importNameMin {
		name += "_"
	}
	return name
}

// sanitizeUsername derives a login candidate from a source id.
func sanitizeUsername(id string) string {
	var b strings.Builder
	pendingDash := false
	for _, r := range strings.ToLower(id) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			if pendingDash {
				b.WriteByte('-')
				pendingDash = false
			}
			b.WriteRune(r)
		case r == ' ' || r == '.' || r == ':' || r == '/' || r == '-':
			pendingDash = true
		}
	}
	if pendingDash {
		b.WriteByte('-')
	}
	out := strings.Trim(b.String(), "-_")
	if r := []rune(out); len(r) > importUsernameMax {
		out = string(r[:importUsernameMax])
	}
	return out
}

// pickUsername chooses a free username for an imported account.
func pickUsername(sourceID, hexHash string, used map[string]bool) string {
	free := func(n string) bool {
		if n == "" {
			return false
		}
		if _, err := NormalizeUsername(n); err != nil {
			return false
		}
		return !used[strings.ToLower(n)]
	}
	base := sanitizeUsername(sourceID)
	if _, err := NormalizeUsername(base); err != nil {
		base = importFallbackBase + hexHash[:8]
	}
	if free(base) {
		return base
	}
	stem := strings.Trim(base, "-_")
	if r := []rune(stem); len(r) > importUsernameMax-5 {
		stem = strings.Trim(string(r[:importUsernameMax-5]), "-_")
	}
	for i := 0; i+4 <= len(hexHash); i += 4 {
		if cand := stem + "-" + hexHash[i:i+4]; free(cand) {
			return cand
		}
	}
	for i := 0; i+11 <= len(hexHash); i++ {
		if cand := importFallbackBase + hexHash[i:i+11]; free(cand) {
			return cand
		}
	}
	// Practically unreachable: the hash windows are exhausted.
	for n := 1; ; n++ {
		if cand := fmt.Sprintf("%s%d", importFallbackBase, n); free(cand) {
			return cand
		}
	}
}
