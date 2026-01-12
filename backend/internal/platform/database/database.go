// Package database opens the relational store (PostgreSQL or SQLite) through
// GORM, applies pool and pragma settings, and translates driver errors into a
// small set of generic conditions.
package database

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gormlogger "gorm.io/gorm/logger"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// SlowQueryThreshold marks statements worth a warning.
const SlowQueryThreshold = 200 * time.Millisecond

// Open connects according to cfg.Database.Driver and returns a ready handle.
// The caller owns the handle and should Close it on shutdown.
func Open(ctx context.Context, cfg *config.Config, log *logx.Logger, clk clock.Clock) (*gorm.DB, error) {
	if clk == nil {
		clk = clock.System{}
	}
	gcfg := &gorm.Config{
		Logger:                                   newGormLogger(log, cfg.IsProd()),
		NowFunc:                                  func() time.Time { return clk.Now() },
		TranslateError:                           true,
		DisableForeignKeyConstraintWhenMigrating: true,
		PrepareStmt:                              false,
	}

	var (
		db  *gorm.DB
		err error
	)
	switch cfg.Database.Driver {
	case config.DriverPostgres:
		db, err = openPostgres(cfg.Database.Postgres, gcfg)
	case config.DriverSQLite:
		db, err = openSQLite(cfg.Database.SQLite, gcfg)
	default:
		return nil, fmt.Errorf("unsupported database driver %q", cfg.Database.Driver)
	}
	if err != nil {
		return nil, err
	}
	if err := Ping(ctx, db); err != nil {
		_ = Close(db)
		return nil, fmt.Errorf("database ping: %w", err)
	}
	return db, nil
}

func openPostgres(cfg config.PostgresConfig, gcfg *gorm.Config) (*gorm.DB, error) {
	db, err := gorm.Open(postgres.Open(cfg.DSN), gcfg)
	if err != nil {
		return nil, fmt.Errorf("open postgres: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 30
	}
	maxIdle := cfg.MaxIdleConns
	if maxIdle <= 0 {
		maxIdle = 10
	}
	if maxIdle > maxOpen {
		maxIdle = maxOpen
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxIdle)
	if cfg.ConnMaxLifetimeMin > 0 {
		sqlDB.SetConnMaxLifetime(time.Duration(cfg.ConnMaxLifetimeMin) * time.Minute)
	}
	if cfg.ConnMaxIdleTimeMin > 0 {
		sqlDB.SetConnMaxIdleTime(time.Duration(cfg.ConnMaxIdleTimeMin) * time.Minute)
	}
	return db, nil
}

// SQLiteDSN derives the connection string for cfg: SQLITE_DSN verbatim when
// set, otherwise "file:<path>" with busy timeout, negative-KiB cache size,
// foreign keys, WAL journal and the synchronous mode as query parameters.
func SQLiteDSN(cfg config.SQLiteConfig) string {
	if dsn := strings.TrimSpace(cfg.DSN); dsn != "" {
		return dsn
	}
	q := url.Values{}
	q.Set("_busy_timeout", fmt.Sprint(cfg.BusyTimeoutMS))
	q.Set("_foreign_keys", "on")
	q.Set("_journal_mode", "WAL")
	q.Set("_synchronous", cfg.Synchronous)
	q.Set("_cache_size", fmt.Sprint(-cfg.CacheSizeKB))
	return "file:" + cfg.Path + "?" + q.Encode()
}

func openSQLite(cfg config.SQLiteConfig, gcfg *gorm.Config) (*gorm.DB, error) {
	if strings.TrimSpace(cfg.DSN) == "" {
		if strings.TrimSpace(cfg.Path) == "" {
			return nil, errors.New("sqlite: path and dsn are both empty")
		}
		if err := os.MkdirAll(filepath.Dir(cfg.Path), 0o755); err != nil {
			return nil, fmt.Errorf("sqlite: create directory: %w", err)
		}
	}
	db, err := gorm.Open(sqlite.Open(SQLiteDSN(cfg)), gcfg)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	maxOpen := cfg.MaxOpenConns
	if maxOpen <= 0 {
		maxOpen = 1
	}
	sqlDB.SetMaxOpenConns(maxOpen)
	sqlDB.SetMaxIdleConns(maxOpen)
	sqlDB.SetConnMaxLifetime(0)

	pragmas := []string{
		"PRAGMA foreign_keys = ON",
		"PRAGMA journal_mode = WAL",
		"PRAGMA synchronous = " + cfg.Synchronous,
		fmt.Sprintf("PRAGMA busy_timeout = %d", cfg.BusyTimeoutMS),
		fmt.Sprintf("PRAGMA cache_size = %d", -cfg.CacheSizeKB),
		fmt.Sprintf("PRAGMA mmap_size = %d", cfg.MmapSizeBytes),
		"PRAGMA temp_store = " + cfg.TempStore,
		"PRAGMA optimize",
	}
	for _, p := range pragmas {
		if err := db.Exec(p).Error; err != nil {
			_ = sqlDB.Close()
			return nil, fmt.Errorf("sqlite %s: %w", p, err)
		}
	}
	return db, nil
}

// Dialect returns "postgres" or "sqlite".
func Dialect(db *gorm.DB) string { return db.Dialector.Name() }

// Ping verifies the connection.
func Ping(ctx context.Context, db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.PingContext(ctx)
}

// Close closes the underlying connection pool.
func Close(db *gorm.DB) error {
	sqlDB, err := db.DB()
	if err != nil {
		return err
	}
	return sqlDB.Close()
}

// SQL exposes the underlying *sql.DB (pool statistics, raw access).
func SQL(db *gorm.DB) (*sql.DB, error) { return db.DB() }

// WithAdvisoryLock runs fn while holding a named cross-process lock so that
// concurrent instances serialize work such as migrations. On SQLite (a single
// process) it simply runs fn.
func WithAdvisoryLock(ctx context.Context, db *gorm.DB, name string, fn func(tx *gorm.DB) error) error {
	if Dialect(db) != "postgres" {
		return fn(db.WithContext(ctx))
	}
	return db.WithContext(ctx).Connection(func(conn *gorm.DB) error {
		if err := conn.Exec("SELECT pg_advisory_lock(hashtext(?))", name).Error; err != nil {
			return fmt.Errorf("acquire advisory lock %q: %w", name, err)
		}
		defer conn.Exec("SELECT pg_advisory_unlock(hashtext(?))", name)
		return fn(conn)
	})
}

// gormLogger adapts logx to GORM's logger interface. Statements are only
// logged when they fail or run slowly (and, outside production, at debug).
type gormLogger struct {
	log  *logx.Logger
	prod bool
}

func newGormLogger(log *logx.Logger, prod bool) gormlogger.Interface {
	if log == nil {
		log = logx.Nop()
	}
	return &gormLogger{log: log, prod: prod}
}

// ParamsFilter strips bound values so SQL logs never contain user data or
// secrets.
func (l *gormLogger) ParamsFilter(_ context.Context, sql string, _ ...any) (string, []any) {
	return sql, nil
}

func (l *gormLogger) LogMode(gormlogger.LogLevel) gormlogger.Interface { return l }
func (l *gormLogger) Info(_ context.Context, msg string, args ...any) {
	l.log.Info(fmt.Sprintf(msg, args...))
}
func (l *gormLogger) Warn(_ context.Context, msg string, args ...any) {
	l.log.Warn(fmt.Sprintf(msg, args...))
}
func (l *gormLogger) Error(_ context.Context, msg string, args ...any) {
	l.log.Error(fmt.Sprintf(msg, args...))
}

func (l *gormLogger) Trace(_ context.Context, begin time.Time, fc func() (string, int64), err error) {
	elapsed := time.Since(begin)
	switch {
	case err != nil && !errors.Is(err, gormlogger.ErrRecordNotFound):
		sql, rows := fc()
		l.log.Error("db_query_failed", logx.F("error", err), logx.F("sql", sql), logx.F("rows", rows), logx.F("elapsed", elapsed))
	case elapsed > SlowQueryThreshold:
		sql, rows := fc()
		l.log.Warn("db_slow_query", logx.F("sql", sql), logx.F("rows", rows), logx.F("elapsed", elapsed))
	case !l.prod && l.log.Enabled(logx.LevelDebug):
		sql, rows := fc()
		l.log.Debug("db_query", logx.F("sql", sql), logx.F("rows", rows), logx.F("elapsed", elapsed))
	}
}
