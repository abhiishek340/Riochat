package config

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func envMap(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) { v, ok := m[k]; return v, ok }
}

func loadWith(t *testing.T, env map[string]string, yaml string) *Config {
	t.Helper()
	dir := t.TempDir()
	opts := LoadOptions{LookupEnv: envMap(env), WorkDir: dir, Warn: &bytes.Buffer{}}
	if yaml != "" {
		p := filepath.Join(dir, "cfg.yaml")
		if err := os.WriteFile(p, []byte(yaml), 0o644); err != nil {
			t.Fatal(err)
		}
		opts.ConfigFile = p
	}
	c, err := Load(opts)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAppEnvNormalization(t *testing.T) {
	if got := loadWith(t, map[string]string{"APP_ENV": "production"}, "").App.Env; got != "prod" {
		t.Fatalf("production -> %q", got)
	}
	if got := loadWith(t, map[string]string{"APP_ENV": "development"}, "").App.Env; got != "dev" {
		t.Fatalf("development -> %q", got)
	}
	if got := loadWith(t, map[string]string{"APP_ENV": "  "}, "").App.Env; got != "prod" {
		t.Fatalf("blank must be ignored, got %q", got)
	}
	if got := loadWith(t, nil, "app:\n  env: development\n").App.Env; got != "dev" {
		t.Fatalf("yaml env: %q", got)
	}
}

func TestPrecedenceRules(t *testing.T) {
	yaml := "server:\n  http_port: 8081\n  idle_timeout_seconds: 300\n  read_timeout_seconds: 0\n"
	// env set to empty wins over YAML
	if got := loadWith(t, map[string]string{"HTTP_PORT": ""}, yaml).Server.HTTPPort; got != "" {
		t.Fatalf("empty env must win, got %q", got)
	}
	if got := loadWith(t, nil, yaml).Server.HTTPPort; got != "8081" {
		t.Fatalf("yaml port: %q", got)
	}
	// unparsable env int falls through to YAML; YAML 0 means default
	c := loadWith(t, map[string]string{"HTTP_IDLE_TIMEOUT_SECONDS": "abc"}, yaml)
	if c.Server.IdleTimeoutSecs != 300 || c.Server.ReadTimeoutSecs != 120 {
		t.Fatalf("int fallthrough: idle=%d read=%d", c.Server.IdleTimeoutSecs, c.Server.ReadTimeoutSecs)
	}
}

func TestBoolListAndPathSemantics(t *testing.T) {
	yaml := "security:\n  ssrf_protection_enabled: false\n  data_encryption_keys_previous: [' a ', '', b]\nstorage:\n  local:\n    root_dir: rel/store\n"
	c := loadWith(t, nil, yaml)
	if c.Security.SSRFProtectionEnabled {
		t.Fatal("explicit yaml false must be honored")
	}
	if got := strings.Join(c.Security.DataEncryptionKeysPrev, "|"); got != "a|b" {
		t.Fatalf("yaml list: %q", got)
	}
	if !filepath.IsAbs(c.Storage.Local.RootDir) || !strings.HasSuffix(c.Storage.Local.RootDir, filepath.Join("rel", "store")) {
		t.Fatalf("relative yaml path must resolve against the yaml dir: %q", c.Storage.Local.RootDir)
	}
	c = loadWith(t, map[string]string{
		"SSRF_PROTECTION_ENABLED":       "notabool",
		"DATA_ENCRYPTION_KEYS_PREVIOUS": " x , ,y ",
		"STORAGE_ROOT_DIR":              "./env/store",
	}, yaml)
	if c.Security.SSRFProtectionEnabled {
		t.Fatal("unparsable env bool must be ignored")
	}
	if got := strings.Join(c.Security.DataEncryptionKeysPrev, "|"); got != "x|y" {
		t.Fatalf("env list: %q", got)
	}
	if c.Storage.Local.RootDir != "./env/store" {
		t.Fatalf("env path used as given: %q", c.Storage.Local.RootDir)
	}
}

func TestConfigFileFallbackWarns(t *testing.T) {
	var warn bytes.Buffer
	dir := t.TempDir()
	_, err := Load(LoadOptions{LookupEnv: envMap(map[string]string{"CONFIG_FILE": filepath.Join(dir, "missing.yaml")}), WorkDir: dir, Warn: &warn})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(warn.String(), "ignoring CONFIG_FILE") {
		t.Fatalf("expected warning, got %q", warn.String())
	}
	// default discovery: config.yaml in the working directory
	if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte("app:\n  name: Found\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	c, _ := Load(LoadOptions{LookupEnv: envMap(nil), WorkDir: dir})
	if c.App.Name != "Found" {
		t.Fatalf("default discovery failed: %q", c.App.Name)
	}
}

func prodBase() *Config {
	c := Default()
	c.Security.JWTSecret = strings.Repeat("j", 32)
	c.Security.DataEncryptionKey = strings.Repeat("k", 32)
	c.Server.PublicAPIBaseURL = "https://api.example.com"
	c.Server.PublicWebBaseURL = "https://app.example.com"
	return &c
}

func TestValidateProduction(t *testing.T) {
	if err := Validate(prodBase()); err != nil {
		t.Fatalf("valid prod config rejected: %v", err)
	}
	type tc struct {
		name   string
		mutate func(*Config)
		want   string
	}
	cases := []tc{
		{"default jwt", func(c *Config) { c.Security.JWTSecret = DevJWTSecret }, "JWT_SECRET must be explicitly set"},
		{"short jwt", func(c *Config) { c.Security.JWTSecret = strings.Repeat("j", 15) }, "JWT_SECRET is too short"},
		{"short key", func(c *Config) { c.Security.DataEncryptionKey = strings.Repeat("k", 31) }, "DATA_ENCRYPTION_KEY is too short"},
		{"wildcard cors", func(c *Config) { c.Server.CORSAllowOrigin = "*" }, "CORS_ALLOW_ORIGIN"},
		{"empty cors", func(c *Config) { c.Server.CORSAllowOrigin = "" }, "CORS_ALLOW_ORIGIN"},
		{"http public url", func(c *Config) { c.Server.PublicAPIBaseURL = "http://x" }, "must be an https URL"},
		{"sqlite no path", func(c *Config) {
			c.Database.Driver = DriverSQLite
			c.Database.SQLite.Path = ""
			c.Database.SQLite.DSN = ""
		}, "SQLITE_PATH"},
		{"sqlite sync", func(c *Config) { c.Database.Driver = DriverSQLite; c.Database.SQLite.Synchronous = "BOGUS" }, "SQLITE_SYNCHRONOUS"},
		{"s3 no bucket", func(c *Config) { c.Storage.Backend = StorageS3 }, "STORAGE_S3_BUCKET"},
		{"bad cache", func(c *Config) { c.Cache.Driver = "etcd" }, "unsupported cache driver"},
		{"bad db", func(c *Config) { c.Database.Driver = "oracle" }, "unsupported database driver"},
		{"bad env", func(c *Config) { c.App.Env = "staging" }, "APP_ENV"},
		{"ssrf host ip", func(c *Config) { c.Security.SSRFAllowedHosts = "10.0.0.5" }, "SSRF_ALLOWED_HOSTS"},
		{"ssrf mapped cidr", func(c *Config) { c.Security.SSRFAllowedCIDRs = "::ffff:10.0.0.0/104" }, "SSRF_ALLOWED_CIDRS"},
		{"ssrf metadata", func(c *Config) { c.Security.SSRFAllowedHosts = "metadata.google.internal" }, "SSRF_ALLOWED_HOSTS"},
		{"turnstile userinfo", func(c *Config) { c.Security.TurnstileSiteverifyURL = "https://u:p@x.example/v" }, "TURNSTILE_SITEVERIFY_URL"},
		{"turnstile linklocal", func(c *Config) { c.Security.TurnstileSiteverifyURL = "http://169.254.169.254/v" }, "TURNSTILE_SITEVERIFY_URL"},
		{"proxy entry", func(c *Config) { c.Server.TrustedProxies = "not-an-ip" }, "TRUSTED_PROXIES"},
	}
	for _, c := range cases {
		cfg := prodBase()
		c.mutate(cfg)
		err := Validate(cfg)
		if err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s: got %v, want substring %q", c.name, err, c.want)
		}
	}
}

func TestValidatePlaceholderPassesButWarns(t *testing.T) {
	c := prodBase()
	c.Security.JWTSecret = "change-me-dev-jwt-secret"
	if err := Validate(c); err != nil {
		t.Fatalf("placeholder with enough length must pass validation: %v", err)
	}
	warns := SecurityWarnings(c)
	if len(warns) != 1 || !strings.Contains(warns[0], "JWT_SECRET") {
		t.Fatalf("warnings: %v", warns)
	}
	c.Security.DataEncryptionKey = DevDataEncryptionKey
	c.Security.DataEncryptionKeysPrev = []string{"change-me-old", "change-me-older"}
	if got := len(SecurityWarnings(c)); got != 3 {
		t.Fatalf("expected 3 warnings (previous keys reported once), got %d", got)
	}
}
