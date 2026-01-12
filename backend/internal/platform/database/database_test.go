package database_test

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/database/dbtest"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

type widget struct {
	database.Model
	Name string `gorm:"size:64;uniqueIndex"`
}

func (widget) TableName() string { return "system_widgets" }

func TestSQLiteOpenAppliesSettings(t *testing.T) {
	db := dbtest.New(t)
	if database.Dialect(db) != "sqlite" {
		t.Fatalf("dialect %q", database.Dialect(db))
	}
	var fk, busy int
	var journal string
	db.Raw("PRAGMA foreign_keys").Scan(&fk)
	db.Raw("PRAGMA busy_timeout").Scan(&busy)
	db.Raw("PRAGMA journal_mode").Scan(&journal)
	if fk != 1 || busy != 5000 || !strings.EqualFold(journal, "wal") {
		t.Fatalf("pragmas: fk=%d busy=%d journal=%s", fk, busy, journal)
	}
	if err := database.Ping(context.Background(), db); err != nil {
		t.Fatal(err)
	}
}

func TestSQLiteCreatesParentDirectoryAndRejectsEmpty(t *testing.T) {
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.Database.Driver = config.DriverSQLite
	cfg.Database.SQLite.Path = filepath.Join(t.TempDir(), "nested", "deeper", "x.db")
	db := dbtest.Open(t, &cfg)
	if err := db.Exec("CREATE TABLE t (id integer)").Error; err != nil {
		t.Fatal(err)
	}
	cfg.Database.SQLite.Path = ""
	cfg.Database.SQLite.DSN = ""
	if _, err := database.Open(context.Background(), &cfg, logx.Nop(), nil); err == nil {
		t.Fatal("empty path and dsn must fail")
	}
	cfg.Database.Driver = "oracle"
	if _, err := database.Open(context.Background(), &cfg, logx.Nop(), nil); err == nil {
		t.Fatal("unsupported driver must fail")
	}
}

func TestSQLiteDSNDerivation(t *testing.T) {
	cfg := config.Default().Database.SQLite
	cfg.Path = "/tmp/x.db"
	dsn := database.SQLiteDSN(cfg)
	for _, want := range []string{"file:/tmp/x.db?", "_busy_timeout=5000", "_foreign_keys=on", "_journal_mode=WAL", "_synchronous=NORMAL", "_cache_size=-20480"} {
		if !strings.Contains(dsn, want) {
			t.Errorf("dsn %q missing %q", dsn, want)
		}
	}
	cfg.DSN = "file:custom.db?mode=rwc"
	if database.SQLiteDSN(cfg) != "file:custom.db?mode=rwc" {
		t.Fatal("explicit DSN must win verbatim")
	}
}

func TestErrorTranslation(t *testing.T) {
	db := dbtest.New(t)
	if err := db.AutoMigrate(&widget{}); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&widget{Name: "a"}).Error; err != nil {
		t.Fatal(err)
	}

	dup := database.Translate(db.Create(&widget{Name: "a"}).Error)
	if !errors.Is(dup, database.ErrDuplicate) || !database.IsDuplicate(dup) {
		t.Fatalf("duplicate not translated: %v", dup)
	}

	var w widget
	missing := database.Translate(db.First(&w, "name = ?", "nope").Error)
	if !errors.Is(missing, database.ErrNotFound) || !database.IsNotFound(missing) {
		t.Fatalf("not found not translated: %v", missing)
	}
	if !errors.Is(missing, gorm.ErrRecordNotFound) {
		t.Fatal("original cause must stay in the chain")
	}

	other := fmt.Errorf("boom")
	if database.Translate(other) != other {
		t.Fatal("other errors pass through unchanged")
	}
	if database.Translate(nil) != nil {
		t.Fatal("nil stays nil")
	}
	if database.Translate(dup) != dup {
		t.Fatal("already translated errors are idempotent")
	}
}

func TestSoftDeleteAndForeignKeysNotCreated(t *testing.T) {
	db := dbtest.New(t)
	if err := db.AutoMigrate(&widget{}); err != nil {
		t.Fatal(err)
	}
	w := widget{Name: "gone"}
	db.Create(&w)
	db.Delete(&w)
	var n int64
	db.Model(&widget{}).Where("name = ?", "gone").Count(&n)
	if n != 0 {
		t.Fatal("soft deleted rows must be excluded by default")
	}
	db.Unscoped().Model(&widget{}).Where("name = ?", "gone").Count(&n)
	if n != 1 {
		t.Fatal("row must still exist physically")
	}
}

func TestAdvisoryLockSQLiteJustRuns(t *testing.T) {
	db := dbtest.New(t)
	ran := false
	err := database.WithAdvisoryLock(context.Background(), db, "x", func(tx *gorm.DB) error { ran = true; return nil })
	if err != nil || !ran {
		t.Fatalf("lock: %v ran=%v", err, ran)
	}
}

func TestCheckTableName(t *testing.T) {
	for _, ok := range []string{"announcement_user_states", "system_settings", "user_settings", "audit_logs", "identity_users", "users", "prompt_presets"} {
		if err := database.CheckTableName(ok); err != nil {
			t.Errorf("%s rejected: %v", ok, err)
		}
	}
	for _, bad := range []string{"widgets", "foo_bar", "", "tmp_users"} {
		if database.CheckTableName(bad) == nil {
			t.Errorf("%q must be rejected", bad)
		}
	}
}

func TestCloseIsIdempotentEnough(t *testing.T) {
	cfg := config.Default()
	cfg.App.Env = config.EnvDev
	cfg.Database.Driver = config.DriverSQLite
	cfg.Database.SQLite.Path = filepath.Join(t.TempDir(), "c.db")
	db, err := database.Open(context.Background(), &cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(db); err != nil {
		t.Fatal(err)
	}
	if err := database.Ping(context.Background(), db); err == nil {
		t.Fatal("ping after close must fail")
	}
}
