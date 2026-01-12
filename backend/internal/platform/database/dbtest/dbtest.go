// Package dbtest opens throwaway SQLite databases for tests.
package dbtest

import (
	"context"
	"path/filepath"
	"testing"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// New returns a fresh SQLite database in a temporary directory. It is closed
// automatically when the test ends.
func New(t testing.TB) *gorm.DB {
	t.Helper()
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.Database.Driver = config.DriverSQLite
	cfg.Database.SQLite.Path = filepath.Join(t.TempDir(), "test.db")
	return Open(t, &cfg)
}

// Open opens the database described by cfg and closes it when the test ends.
func Open(t testing.TB, cfg *config.Config) *gorm.DB {
	t.Helper()
	db, err := database.Open(context.Background(), cfg, logx.Nop(), nil)
	if err != nil {
		t.Fatalf("dbtest: open: %v", err)
	}
	t.Cleanup(func() { _ = database.Close(db) })
	return db
}
