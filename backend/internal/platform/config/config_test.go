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

func TestValidateDevAllowsDefaults(t *testing.T) {
	c := Default()
	c.App.Env = EnvDev
	if err := Validate(&c); err != nil {
		t.Fatalf("dev defaults must validate: %v", err)
	}
}

func TestPostgresDSNTimeZoneDecoding(t *testing.T) {
	kv := NormalizePostgresDSN("host=h dbname=d TimeZone=Asia%2FShanghai sslmode=disable")
	if !strings.Contains(kv, "TimeZone=Asia/Shanghai") {
		t.Fatalf("key-value form: %q", kv)
	}
	u := NormalizePostgresDSN("postgres://u:p@h:5432/d?sslmode=disable&timezone=Asia%2FShanghai")
	if !strings.HasSuffix(u, "timezone=Asia/Shanghai") {
		t.Fatalf("url form: %q", u)
	}
	same := "host=h dbname=d"
	if NormalizePostgresDSN(same) != same {
		t.Fatal("untouched DSN must be unchanged")
	}
}

func TestDriverAliases(t *testing.T) {
	for in, want := range map[string]string{"": "postgres", "PG": "postgres", "postgresql": "postgres", "sqlite3": "sqlite"} {
		if got := NormalizeDatabaseDriver(in); got != want {
			t.Errorf("db %q -> %q, want %q", in, got, want)
		}
	}
	for in, want := range map[string]string{"": "redis", "mem": "memory", "In-Memory": "memory", "inmemory": "memory", "x": "x"} {
		if got := NormalizeCacheDriver(in); got != want {
			t.Errorf("cache %q -> %q, want %q", in, got, want)
		}
	}
}

func TestTracingEnvPrecedence(t *testing.T) {
	c := loadWith(t, map[string]string{"OTEL_SAMPLING_RATE": "0.5", "OTEL_TRACES_SAMPLER_ARG": "0.25", "OTEL_EXPORTER_OTLP_PROTOCOL": "http/protobuf"}, "")
	if c.Tracing.SamplingRate != 0.25 || c.Tracing.Protocol != "http" {
		t.Fatalf("tracing: %+v", c.Tracing)
	}
	if loadWith(t, map[string]string{"OTEL_EXPORTER_OTLP_PROTOCOL": "carrier-pigeon"}, "").Tracing.Protocol != "grpc" {
		t.Fatal("unknown protocol must become grpc")
	}
	if loadWith(t, nil, "").Tracing.Enabled != nil {
		t.Fatal("tracing enabled must default to unset")
	}
}

func TestPlaceholderSecret(t *testing.T) {
	for _, s := range []string{"", "  ", DevJWTSecret, DevDataEncryptionKey, "Change-Me-please"} {
		if !IsPlaceholderSecret(s) {
			t.Errorf("%q should be a placeholder", s)
		}
	}
	if IsPlaceholderSecret("a-real-secret-value") {
		t.Fatal("real secret flagged")
	}
}

func TestNormalizeTTLs(t *testing.T) {
	c := Default()
	c.Dynamic.Auth.AccessTokenTTL = 0
	c.Dynamic.Auth.RefreshTokenTTL = -1
	c.Normalize()
	if c.Dynamic.Auth.AccessTokenTTL != DefaultAccessTokenTTL || c.Dynamic.Auth.RefreshTokenTTL != DefaultRefreshTokenTTL {
		t.Fatalf("ttl normalization failed: %+v", c.Dynamic.Auth)
	}
}

func TestHolderSnapshotsAreImmutable(t *testing.T) {
	var nilHolder *Holder
	if nilHolder.Load() == nil {
		t.Fatal("nil holder must return an empty config")
	}
	base := Default()
	h := NewHolder(&base)
	before := h.Load()
	h.Update(func(c *Config) { c.App.Name = "Changed"; c.Dynamic.Auth.RateLimitEnabled = true })
	if before.App.Name != "Riochat" || before.Dynamic.Auth.RateLimitEnabled {
		t.Fatal("a snapshot handed out must never change")
	}
	if h.Load().App.Name != "Changed" {
		t.Fatal("update not published")
	}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			h.Update(func(c *Config) { c.Dynamic.Auth.RateLimitAuthRPM++ })
			_ = h.Load()
		}()
	}
	wg.Wait()
	if got := h.Load().Dynamic.Auth.RateLimitAuthRPM; got != DefaultAuthRPM+50 {
		t.Fatalf("lost updates: %d", got)
	}
}

func TestLocalModeSecretsAndOverrides(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "data")
	s1, err := LoadOrCreateLocalSecrets(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(s1.JWTSecret) != 64 || len(s1.DataEncryptionKey) != 64 {
		t.Fatalf("secrets must be 64 hex chars: %d/%d", len(s1.JWTSecret), len(s1.DataEncryptionKey))
	}
	info, err := os.Stat(filepath.Join(dir, "secrets.json"))
	if err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("secrets file mode: %v %v", info, err)
	}
	if di, _ := os.Stat(dir); di.Mode().Perm() != 0o700 {
		t.Fatalf("data dir mode: %v", di.Mode().Perm())
	}
	s2, err := LoadOrCreateLocalSecrets(dir)
	if err != nil || s1 != s2 {
		t.Fatalf("second start must reuse the file: %v", err)
	}

	cfg := Default()
	if err := ApplyLocal(&cfg, dir, s1); err != nil {
		t.Fatal(err)
	}
	if !cfg.IsProd() || cfg.Database.Driver != DriverSQLite || cfg.Cache.Driver != CacheMemory ||
		cfg.Admin.Username != "owner" || cfg.Server.ListenAddr != "127.0.0.1:0" ||
		cfg.Database.SQLite.Path != filepath.Join(dir, "riochat.db") || cfg.GeoIP.Provider != "none" {
		t.Fatalf("local overrides wrong: %+v", cfg)
	}
	if err := Validate(&cfg); err != nil {
		t.Fatalf("local production config with empty public URLs must validate: %v", err)
	}
	if err := ApplyLocal(&cfg, "relative/dir", s1); err == nil {
		t.Fatal("relative data dir must be rejected")
	}
}

func TestLocalSecretsCorruptAndShort(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "secrets.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateLocalSecrets(dir); err == nil || !strings.Contains(err.Error(), "corrupt") {
		t.Fatalf("corrupt file: %v", err)
	}
	raw, _ := json.Marshal(LocalSecrets{JWTSecret: "short-secret", DataEncryptionKey: strings.Repeat("a", 64)})
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadOrCreateLocalSecrets(dir); err == nil || !strings.Contains(err.Error(), "delete the file") {
		t.Fatalf("short secret: %v", err)
	}
}

func TestListenAddressAndLists(t *testing.T) {
	c := Default()
	if c.ListenAddress() != ":8080" {
		t.Fatalf("listen: %q", c.ListenAddress())
	}
	c.Server.ListenAddr = "127.0.0.1:0"
	if c.ListenAddress() != "127.0.0.1:0" {
		t.Fatal("explicit listen address must win")
	}
	if got := SplitList(" a, ,b ,"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("SplitList: %v", got)
	}
}

func TestExampleConfigFileLoadsAndValidatesInDev(t *testing.T) {
	path := filepath.Join("..", "..", "..", "config.example.yaml")
	c, err := Load(LoadOptions{LookupEnv: envMap(nil), ConfigFile: path, Warn: &bytes.Buffer{}})
	if err != nil {
		t.Fatal(err)
	}
	if c.App.Env != EnvProd || c.Server.HTTPPort != "8080" || c.Server.CORSAllowOrigin != "https://chat.example.com" {
		t.Fatalf("example config parsed wrong: %+v", c.Server)
	}
	if err := Validate(c); err != nil {
		t.Fatalf("example config must be a valid production shape: %v", err)
	}
}
