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
