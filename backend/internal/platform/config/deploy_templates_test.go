package config

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// The configuration templates shipped in deploy/ are part of the product: they
// must only use keys the loader knows, load without surprises and pass
// validation as shipped.

const deployDir = "../../../../deploy"

func templatePath(name string) string { return filepath.Join(deployDir, name) }

func loadTemplate(t *testing.T, name string, env map[string]string) *Config {
	t.Helper()
	cfg, err := Load(LoadOptions{
		ConfigFile: templatePath(name),
		LookupEnv: func(k string) (string, bool) {
			v, ok := env[k]
			return v, ok
		},
		Warn: os.Stderr,
	})
	if err != nil {
		t.Fatalf("%s: load: %v", name, err)
	}
	return cfg
}

func TestDeployTemplatesUseOnlyKnownKeys(t *testing.T) {
	for _, name := range []string{"config.example.yaml", "config.sqlite.example.yaml", "config.full.example.yaml"} {
		raw, err := os.ReadFile(templatePath(name))
		if err != nil {
			t.Fatal(err)
		}
		dec := yaml.NewDecoder(bytes.NewReader(raw))
		dec.KnownFields(true)
		var f yamlFile
		if err := dec.Decode(&f); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestDeployTemplatesValidateAsShipped(t *testing.T) {
	for _, name := range []string{"config.example.yaml", "config.sqlite.example.yaml", "config.full.example.yaml"} {
		cfg := loadTemplate(t, name, nil)
		if err := Validate(cfg); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if cfg.App.Env != EnvDev {
			t.Errorf("%s: shipped placeholder secrets require app.env dev, got %q", name, cfg.App.Env)
		}
		// Shipped placeholders must be flagged as such so nobody mistakes them for secrets.
		if len(SecurityWarnings(cfg)) == 0 {
			t.Errorf("%s: expected placeholder-secret warnings", name)
		}
		if cfg.ListenAddress() != ":8080" {
			t.Errorf("%s: listen address %q", name, cfg.ListenAddress())
		}
		for _, p := range cfg.TrustedProxyList() {
			if _, err := ParseProxyEntry(p); err != nil {
				t.Errorf("%s: trusted proxy %q: %v", name, p, err)
			}
		}
	}
}

func TestDeployTemplatesProductionRejectsPlaceholders(t *testing.T) {
	for _, name := range []string{"config.example.yaml", "config.sqlite.example.yaml", "config.full.example.yaml"} {
		cfg := loadTemplate(t, name, map[string]string{"APP_ENV": "prod"})
		if err := Validate(cfg); err == nil {
			t.Errorf("%s: prod with the shipped placeholders must be rejected", name)
		}
	}
}

func TestDeploySQLiteTemplate(t *testing.T) {
	cfg := loadTemplate(t, "config.sqlite.example.yaml", nil)
	if cfg.Database.Driver != DriverSQLite || cfg.Cache.Driver != CacheMemory {
		t.Fatalf("sqlite profile must use sqlite and the memory cache: %+v %+v", cfg.Database.Driver, cfg.Cache.Driver)
	}
	if cfg.Database.SQLite.Path != "/app/data/riochat.db" || cfg.Storage.Local.RootDir != "/app/storage" {
		t.Fatalf("data must live on the container volumes: %q %q", cfg.Database.SQLite.Path, cfg.Storage.Local.RootDir)
	}
	if cfg.Server.FrontendDistDir != "/app/frontend/out" {
		t.Fatalf("frontend dir %q", cfg.Server.FrontendDistDir)
	}
}

func TestDeployFullTemplateAndComposeOverrides(t *testing.T) {
	cfg := loadTemplate(t, "config.full.example.yaml", nil)
	if cfg.Database.Driver != DriverPostgres || cfg.Cache.Driver != CacheRedis {
		t.Fatalf("full profile must use postgres and redis")
	}
	// What docker-compose.full.yml sets must win over the file.
	dsn := "host=postgres port=5432 user=riochat password=x dbname=riochat sslmode=disable TimeZone=Asia/Shanghai"
	over := loadTemplate(t, "config.full.example.yaml", map[string]string{
		"POSTGRES_DSN": dsn, "REDIS_ADDR": "redis:6379", "REDIS_USERNAME": "", "REDIS_PASSWORD": "secret",
	})
	if over.Database.Postgres.DSN != dsn || over.Database.Redis.Password != "secret" || over.Database.Redis.Username != "" {
		t.Fatalf("environment must override the file: %+v", over.Database)
	}
}

func TestDeployDefaultTemplateMatchesSpecDefaults(t *testing.T) {
	cfg := loadTemplate(t, "config.example.yaml", nil)
	d := Default()
	if cfg.Server.ReadTimeoutSecs != d.Server.ReadTimeoutSecs || cfg.Server.MaxHeaderBytes != d.Server.MaxHeaderBytes ||
		cfg.Server.ShutdownTimeoutSecs != d.Server.ShutdownTimeoutSecs {
		t.Fatalf("template drifted from built-in server defaults: %+v", cfg.Server)
	}
	if cfg.Database.Postgres.MaxOpenConns != 30 || cfg.Database.SQLite.CacheSizeKB != 20480 || cfg.Database.SQLite.MmapSizeBytes != 268435456 {
		t.Fatalf("pool/sqlite tuning drifted: %+v", cfg.Database)
	}
	for _, origin := range []string{"http://localhost:3000", "tauri://localhost", "http://tauri.localhost"} {
		if !strings.Contains(cfg.Server.CORSAllowOrigin, origin) {
			t.Errorf("dev template CORS must allow %s", origin)
		}
	}
	if cfg.Server.FrontendDistDir == "" || !filepath.IsAbs(cfg.Server.FrontendDistDir) {
		t.Fatalf("relative dist dir should resolve against the file's directory, got %q", cfg.Server.FrontendDistDir)
	}
	if cfg.Branding.PWAMaskableIcon512URL != d.Branding.PWAMaskableIcon512URL || cfg.Branding.FaviconURL != "/favicon.ico" {
		t.Fatalf("branding defaults drifted: %+v", cfg.Branding)
	}
}
