package config

import (
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// LoadOptions controls where Load reads from. The zero value reads the real
// process environment and searches the usual YAML locations.
type LoadOptions struct {
	// LookupEnv replaces os.LookupEnv (tests).
	LookupEnv func(string) (string, bool)
	// ConfigFile overrides the CONFIG_FILE environment variable when set.
	ConfigFile string
	// WorkDir is the directory used for the default YAML search (default: cwd).
	WorkDir string
	// Warn receives non-fatal loader warnings (default: os.Stderr).
	Warn io.Writer
}

// flexList accepts either a YAML sequence or a comma separated scalar.
type flexList []string

func (f *flexList) UnmarshalYAML(n *yaml.Node) error {
	switch n.Kind {
	case yaml.ScalarNode:
		*f = SplitList(n.Value)
	case yaml.SequenceNode:
		var items []string
		if err := n.Decode(&items); err != nil {
			return err
		}
		*f = (*f)[:0]
		for _, it := range items {
			if it = strings.TrimSpace(it); it != "" {
				*f = append(*f, it)
			}
		}
	default:
		return fmt.Errorf("line %d: expected a list or comma separated string", n.Line)
	}
	return nil
}

func (f flexList) joined() string { return strings.Join(f, ",") }

type yamlFile struct {
	App struct {
		Name string `yaml:"name"`
		Env  string `yaml:"env"`
	} `yaml:"app"`
	Branding struct {
		Title                 string `yaml:"title"`
		ShortName             string `yaml:"short_name"`
		Description           string `yaml:"description"`
		LogoURL               string `yaml:"logo_url"`
		FaviconURL            string `yaml:"favicon_url"`
		PWAIcon192URL         string `yaml:"pwa_icon_192_url"`
		PWAIcon512URL         string `yaml:"pwa_icon_512_url"`
		PWAMaskableIcon512URL string `yaml:"pwa_maskable_icon_512_url"`
		AppleTouchIcon180URL  string `yaml:"apple_touch_icon_180_url"`
	} `yaml:"branding"`
	Server struct {
		HTTPPort              string   `yaml:"http_port"`
		CORSAllowOrigin       flexList `yaml:"cors_allow_origin"`
		TrustedProxies        flexList `yaml:"trusted_proxies"`
		PublicAPIBaseURL      string   `yaml:"public_api_base_url"`
		PublicWebBaseURL      string   `yaml:"public_web_base_url"`
		FrontendDistDir       string   `yaml:"frontend_dist_dir"`
		ReadHeaderTimeoutSecs int      `yaml:"read_header_timeout_seconds"`
		ReadTimeoutSecs       int      `yaml:"read_timeout_seconds"`
		IdleTimeoutSecs       int      `yaml:"idle_timeout_seconds"`
		MaxHeaderBytes        int      `yaml:"max_header_bytes"`
		ShutdownTimeoutSecs   int      `yaml:"shutdown_timeout_seconds"`
	} `yaml:"server"`
	Security struct {
		JWTSecret              string   `yaml:"jwt_secret"`
		MCPUserContextSecret   string   `yaml:"mcp_user_context_secret"`
		DataEncryptionKey      string   `yaml:"data_encryption_key"`
		DataEncryptionKeysPrev flexList `yaml:"data_encryption_keys_previous"`
		SSRFProtectionEnabled  *bool    `yaml:"ssrf_protection_enabled"`
		SSRFAllowedHosts       flexList `yaml:"ssrf_allowed_hosts"`
		SSRFAllowedCIDRs       flexList `yaml:"ssrf_allowed_cidrs"`
		TurnstileSiteverifyURL string   `yaml:"turnstile_siteverify_url"`
	} `yaml:"security"`
	Database struct {
		Driver   string `yaml:"driver"`
		Postgres struct {
			DSN                string `yaml:"dsn"`
			MaxOpenConns       int    `yaml:"max_open_conns"`
			MaxIdleConns       int    `yaml:"max_idle_conns"`
			ConnMaxLifetimeMin int    `yaml:"conn_max_lifetime_minutes"`
			ConnMaxIdleTimeMin int    `yaml:"conn_max_idle_time_minutes"`
		} `yaml:"postgres"`
		SQLite struct {
			Path          string `yaml:"path"`
			DSN           string `yaml:"dsn"`
			MaxOpenConns  int    `yaml:"max_open_conns"`
			BusyTimeoutMS int    `yaml:"busy_timeout_ms"`
			CacheSizeKB   int    `yaml:"cache_size_kb"`
			MmapSizeBytes int64  `yaml:"mmap_size_bytes"`
			Synchronous   string `yaml:"synchronous"`
			TempStore     string `yaml:"temp_store"`
		} `yaml:"sqlite"`
		Redis struct {
			Addr                  string `yaml:"addr"`
			Username              string `yaml:"username"`
			Password              string `yaml:"password"`
			DB                    int    `yaml:"db"`
			TLSEnabled            *bool  `yaml:"tls_enabled"`
			TLSInsecureSkipVerify *bool  `yaml:"tls_insecure_skip_verify"`
		} `yaml:"redis"`
	} `yaml:"database"`
	Cache struct {
		Driver string `yaml:"driver"`
	} `yaml:"cache"`
	Storage struct {
		Backend string `yaml:"backend"`
		Local   struct {
			RootDir string `yaml:"root_dir"`
		} `yaml:"local"`
		S3 struct {
			Endpoint        string `yaml:"endpoint"`
			Region          string `yaml:"region"`
			Bucket          string `yaml:"bucket"`
			Prefix          string `yaml:"prefix"`
			AccessKeyID     string `yaml:"access_key_id"`
			SecretAccessKey string `yaml:"secret_access_key"`
			ForcePathStyle  *bool  `yaml:"force_path_style"`
		} `yaml:"s3"`
	} `yaml:"storage"`
	GeoIP struct {
		Provider             string `yaml:"provider"`
		BaseURL              string `yaml:"base_url"`
		Token                string `yaml:"token"`
		TimeoutMS            int    `yaml:"timeout_ms"`
		DatabaseURL          string `yaml:"database_url"`
		DatabasePath         string `yaml:"database_path"`
		DatabaseMaxBytes     int64  `yaml:"database_max_bytes"`
		RefreshIntervalHours int    `yaml:"refresh_interval_hours"`
	} `yaml:"geoip"`
	Observability struct {
		Tracing struct {
			Enabled      *bool   `yaml:"enabled"`
			Endpoint     string  `yaml:"endpoint"`
			Headers      string  `yaml:"headers"`
			Insecure     *bool   `yaml:"insecure"`
			Protocol     string  `yaml:"protocol"`
			SamplingRate float64 `yaml:"sampling_rate"`
		} `yaml:"tracing"`
	} `yaml:"observability"`
}

// resolver implements the precedence rules: environment > YAML > default.
type resolver struct {
	env     func(string) (string, bool)
	yamlDir string // directory of the YAML file, "" when none was used
}

// str: a set environment variable wins even when empty; otherwise a non-empty
// YAML value; otherwise the default.
func (r resolver) str(envKey, yamlVal, def string) string {
	if v, ok := r.env(envKey); ok {
		return v
	}
	if yamlVal != "" {
		return yamlVal
	}
	return def
}

// intv: an unparsable environment value is ignored; a YAML 0 means "unset".
func (r resolver) intv(envKey string, yamlVal, def int) int {
	if v, ok := r.env(envKey); ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	if yamlVal != 0 {
		return yamlVal
	}
	return def
}

func (r resolver) int64v(envKey string, yamlVal, def int64) int64 {
	if v, ok := r.env(envKey); ok {
		if n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64); err == nil {
			return n
		}
	}
	if yamlVal != 0 {
		return yamlVal
	}
	return def
}

func (r resolver) floatv(envKeys []string, yamlVal, def float64) float64 {
	for _, k := range envKeys {
		if v, ok := r.env(k); ok {
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
				return f
			}
		}
	}
	if yamlVal != 0 {
		return yamlVal
	}
	return def
}

// boolv: an unparsable environment value is ignored; an explicit YAML boolean
// (including false) is honored.
func (r resolver) boolv(envKey string, yamlVal *bool, def bool) bool {
	if v, ok := r.env(envKey); ok {
		if b, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			return b
		}
	}
	if yamlVal != nil {
		return *yamlVal
	}
	return def
}

// path: an environment value is used as given; a relative YAML path is
// resolved against the YAML file's directory.
func (r resolver) path(envKey, yamlVal, def string) string {
	if v, ok := r.env(envKey); ok {
		return v
	}
	if yamlVal != "" {
		if !filepath.IsAbs(yamlVal) && r.yamlDir != "" {
			return filepath.Join(r.yamlDir, yamlVal)
		}
		return yamlVal
	}
	return def
}

// list: a set environment value is split on commas (trimmed, blanks dropped);
// otherwise the YAML list.
func (r resolver) list(envKey string, yamlVal flexList) []string {
	if v, ok := r.env(envKey); ok {
		return SplitList(v)
	}
	var out []string
	for _, s := range yamlVal {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (r resolver) csv(envKey string, yamlVal flexList, def string) string {
	if v, ok := r.env(envKey); ok {
		return v
	}
	if len(yamlVal) > 0 {
		return yamlVal.joined()
	}
	return def
}

// NormalizeEnv maps APP_ENV spellings to "dev"/"prod". Unknown values are
// returned lower-cased so validation can report them.
func NormalizeEnv(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "development":
		return EnvDev
	case "production":
		return EnvProd
	default:
		return s
	}
}

// NormalizeDatabaseDriver maps driver aliases to "postgres"/"sqlite".
func NormalizeDatabaseDriver(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "", "postgres", "postgresql", "pg":
		return DriverPostgres
	case "sqlite", "sqlite3":
		return DriverSQLite
	default:
		return s
	}
}

// NormalizeCacheDriver maps cache driver aliases to "redis"/"memory".
func NormalizeCacheDriver(v string) string {
	switch s := strings.ToLower(strings.TrimSpace(v)); s {
	case "", "redis":
		return CacheRedis
	case "memory", "mem", "inmemory", "in-memory":
		return CacheMemory
	default:
		return s
	}
}

// NormalizePostgresDSN decodes a percent-encoded TimeZone value in both the
// key=value and URL forms, so "Asia%2FShanghai" becomes "Asia/Shanghai".
func NormalizePostgresDSN(dsn string) string {
	if strings.HasPrefix(dsn, "postgres://") || strings.HasPrefix(dsn, "postgresql://") {
		i := strings.IndexByte(dsn, '?')
		if i < 0 {
			return dsn
		}
		parts := strings.Split(dsn[i+1:], "&")
		for j, p := range parts {
			k, v, ok := strings.Cut(p, "=")
			if ok && strings.EqualFold(k, "timezone") {
				if dec, err := url.QueryUnescape(v); err == nil {
					parts[j] = k + "=" + dec
				}
			}
		}
		return dsn[:i+1] + strings.Join(parts, "&")
	}
	fields := strings.Fields(dsn)
	changed := false
	for j, f := range fields {
		k, v, ok := strings.Cut(f, "=")
		if ok && strings.EqualFold(k, "timezone") {
			if dec, err := url.QueryUnescape(v); err == nil && dec != v {
				fields[j] = k + "=" + dec
				changed = true
			}
		}
	}
	if !changed {
		return dsn
	}
	return strings.Join(fields, " ")
}

func normalizeTracingProtocol(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "http", "http/protobuf":
		return "http"
	default:
		return "grpc"
	}
}

// Load builds the static configuration: environment, then YAML, then defaults.
// The result is normalized but not validated; call Validate next.
func Load(opts LoadOptions) (*Config, error) {
	env := opts.LookupEnv
	if env == nil {
		env = os.LookupEnv
	}
	warn := opts.Warn
	if warn == nil {
		warn = os.Stderr
	}
	workDir := opts.WorkDir
	if workDir == "" {
		workDir, _ = os.Getwd()
	}

	explicit := opts.ConfigFile
	if explicit == "" {
		if v, ok := env("CONFIG_FILE"); ok {
			explicit = strings.TrimSpace(v)
		}
	}
	var yf yamlFile
	yamlDir := ""
	loaded := false
	if explicit != "" {
		if dir, err := readYAML(explicit, &yf); err == nil {
			yamlDir, loaded = dir, true
		} else {
			fmt.Fprintf(warn, "config: ignoring CONFIG_FILE %q: %v\n", explicit, err)
			yf = yamlFile{}
		}
	}
	if !loaded {
		for _, candidate := range []string{
			filepath.Join(workDir, "config.yaml"),
			filepath.Join(filepath.Dir(workDir), "config.yaml"),
		} {
			if _, err := os.Stat(candidate); err != nil {
				continue
			}
			if dir, err := readYAML(candidate, &yf); err == nil {
				yamlDir, loaded = dir, true
				break
			} else {
				fmt.Fprintf(warn, "config: ignoring %q: %v\n", candidate, err)
				yf = yamlFile{}
			}
		}
	}

	r := resolver{env: env, yamlDir: yamlDir}
	d := Default()
	c := Default()

	// app
	c.App.Name = r.str("APP_NAME", yf.App.Name, d.App.Name)
	c.App.Env = d.App.Env
	if v, ok := env("APP_ENV"); ok && strings.TrimSpace(v) != "" {
		c.App.Env = NormalizeEnv(v)
	} else if strings.TrimSpace(yf.App.Env) != "" {
		c.App.Env = NormalizeEnv(yf.App.Env)
	}

	// branding (YAML only; blanks fall back)
	b := &c.Branding
	pick := func(v, def string) string {
		if strings.TrimSpace(v) != "" {
			return v
		}
		return def
	}
	b.Title = pick(yf.Branding.Title, d.Branding.Title)
	b.ShortName = pick(yf.Branding.ShortName, d.Branding.ShortName)
	b.Description = pick(yf.Branding.Description, d.Branding.Description)
	b.LogoURL = pick(yf.Branding.LogoURL, d.Branding.LogoURL)
	b.FaviconURL = pick(yf.Branding.FaviconURL, d.Branding.FaviconURL)
	b.PWAIcon192URL = pick(yf.Branding.PWAIcon192URL, d.Branding.PWAIcon192URL)
	b.PWAIcon512URL = pick(yf.Branding.PWAIcon512URL, d.Branding.PWAIcon512URL)
	b.PWAMaskableIcon512URL = pick(yf.Branding.PWAMaskableIcon512URL, d.Branding.PWAMaskableIcon512URL)
	b.AppleTouchIcon180URL = pick(yf.Branding.AppleTouchIcon180URL, d.Branding.AppleTouchIcon180URL)

	// server
	s := &c.Server
	s.HTTPPort = r.str("HTTP_PORT", yf.Server.HTTPPort, d.Server.HTTPPort)
	s.CORSAllowOrigin = r.csv("CORS_ALLOW_ORIGIN", yf.Server.CORSAllowOrigin, d.Server.CORSAllowOrigin)
	s.TrustedProxies = r.csv("TRUSTED_PROXIES", yf.Server.TrustedProxies, d.Server.TrustedProxies)
	s.PublicAPIBaseURL = r.str("PUBLIC_API_BASE_URL", yf.Server.PublicAPIBaseURL, "")
	s.PublicWebBaseURL = r.str("PUBLIC_WEB_BASE_URL", yf.Server.PublicWebBaseURL, "")
	s.FrontendDistDir = r.path("FRONTEND_DIST_DIR", yf.Server.FrontendDistDir, d.Server.FrontendDistDir)
	s.ReadHeaderTimeoutSecs = r.intv("HTTP_READ_HEADER_TIMEOUT_SECONDS", yf.Server.ReadHeaderTimeoutSecs, d.Server.ReadHeaderTimeoutSecs)
	s.ReadTimeoutSecs = r.intv("HTTP_READ_TIMEOUT_SECONDS", yf.Server.ReadTimeoutSecs, d.Server.ReadTimeoutSecs)
	s.IdleTimeoutSecs = r.intv("HTTP_IDLE_TIMEOUT_SECONDS", yf.Server.IdleTimeoutSecs, d.Server.IdleTimeoutSecs)
	s.MaxHeaderBytes = r.intv("HTTP_MAX_HEADER_BYTES", yf.Server.MaxHeaderBytes, d.Server.MaxHeaderBytes)
	s.ShutdownTimeoutSecs = r.intv("HTTP_SHUTDOWN_TIMEOUT_SECONDS", yf.Server.ShutdownTimeoutSecs, d.Server.ShutdownTimeoutSecs)

	// security
	sec := &c.Security
	sec.JWTSecret = r.str("JWT_SECRET", yf.Security.JWTSecret, d.Security.JWTSecret)
	sec.MCPUserContextSecret = r.str("MCP_USER_CONTEXT_SECRET", yf.Security.MCPUserContextSecret, "")
	sec.DataEncryptionKey = r.str("DATA_ENCRYPTION_KEY", yf.Security.DataEncryptionKey, d.Security.DataEncryptionKey)
	sec.DataEncryptionKeysPrev = r.list("DATA_ENCRYPTION_KEYS_PREVIOUS", yf.Security.DataEncryptionKeysPrev)
	sec.SSRFProtectionEnabled = r.boolv("SSRF_PROTECTION_ENABLED", yf.Security.SSRFProtectionEnabled, false)
	sec.SSRFAllowedHosts = r.csv("SSRF_ALLOWED_HOSTS", yf.Security.SSRFAllowedHosts, "")
	sec.SSRFAllowedCIDRs = r.csv("SSRF_ALLOWED_CIDRS", yf.Security.SSRFAllowedCIDRs, "")
	sec.TurnstileSiteverifyURL = r.str("TURNSTILE_SITEVERIFY_URL", yf.Security.TurnstileSiteverifyURL, d.Security.TurnstileSiteverifyURL)

	// database
	db := &c.Database
	db.Driver = NormalizeDatabaseDriver(r.str("DATABASE_DRIVER", yf.Database.Driver, d.Database.Driver))
	pg := &db.Postgres
	pg.DSN = NormalizePostgresDSN(r.str("POSTGRES_DSN", yf.Database.Postgres.DSN, d.Database.Postgres.DSN))
	pg.MaxOpenConns = r.intv("POSTGRES_MAX_OPEN_CONNS", yf.Database.Postgres.MaxOpenConns, d.Database.Postgres.MaxOpenConns)
	pg.MaxIdleConns = r.intv("POSTGRES_MAX_IDLE_CONNS", yf.Database.Postgres.MaxIdleConns, d.Database.Postgres.MaxIdleConns)
	pg.ConnMaxLifetimeMin = r.intv("POSTGRES_CONN_MAX_LIFETIME_MINUTES", yf.Database.Postgres.ConnMaxLifetimeMin, d.Database.Postgres.ConnMaxLifetimeMin)
	pg.ConnMaxIdleTimeMin = r.intv("POSTGRES_CONN_MAX_IDLE_TIME_MINUTES", yf.Database.Postgres.ConnMaxIdleTimeMin, d.Database.Postgres.ConnMaxIdleTimeMin)
	sq := &db.SQLite
	sq.Path = r.path("SQLITE_PATH", yf.Database.SQLite.Path, d.Database.SQLite.Path)
	sq.DSN = r.str("SQLITE_DSN", yf.Database.SQLite.DSN, "")
	sq.MaxOpenConns = r.intv("SQLITE_MAX_OPEN_CONNS", yf.Database.SQLite.MaxOpenConns, d.Database.SQLite.MaxOpenConns)
	sq.BusyTimeoutMS = r.intv("SQLITE_BUSY_TIMEOUT_MS", yf.Database.SQLite.BusyTimeoutMS, d.Database.SQLite.BusyTimeoutMS)
	sq.CacheSizeKB = r.intv("SQLITE_CACHE_SIZE_KB", yf.Database.SQLite.CacheSizeKB, d.Database.SQLite.CacheSizeKB)
	sq.MmapSizeBytes = r.int64v("SQLITE_MMAP_SIZE_BYTES", yf.Database.SQLite.MmapSizeBytes, d.Database.SQLite.MmapSizeBytes)
	sq.Synchronous = strings.ToUpper(strings.TrimSpace(r.str("SQLITE_SYNCHRONOUS", yf.Database.SQLite.Synchronous, d.Database.SQLite.Synchronous)))
	sq.TempStore = strings.ToUpper(strings.TrimSpace(r.str("SQLITE_TEMP_STORE", yf.Database.SQLite.TempStore, d.Database.SQLite.TempStore)))
	rd := &db.Redis
	rd.Addr = r.str("REDIS_ADDR", yf.Database.Redis.Addr, d.Database.Redis.Addr)
	rd.Username = r.str("REDIS_USERNAME", yf.Database.Redis.Username, "")
	rd.Password = r.str("REDIS_PASSWORD", yf.Database.Redis.Password, "")
	rd.DB = r.intv("REDIS_DB", yf.Database.Redis.DB, 0)
	rd.TLSEnabled = r.boolv("REDIS_TLS_ENABLED", yf.Database.Redis.TLSEnabled, false)
	rd.TLSInsecureSkipVerify = r.boolv("REDIS_TLS_INSECURE_SKIP_VERIFY", yf.Database.Redis.TLSInsecureSkipVerify, false)

	// cache
	c.Cache.Driver = NormalizeCacheDriver(r.str("CACHE_DRIVER", yf.Cache.Driver, d.Cache.Driver))

	// storage
	st := &c.Storage
	st.Backend = strings.ToLower(strings.TrimSpace(r.str("STORAGE_BACKEND", yf.Storage.Backend, d.Storage.Backend)))
	if st.Backend == "" {
		st.Backend = StorageLocal
	}
	st.Local.RootDir = r.path("STORAGE_ROOT_DIR", yf.Storage.Local.RootDir, d.Storage.Local.RootDir)
	st.S3.Endpoint = r.str("STORAGE_S3_ENDPOINT", yf.Storage.S3.Endpoint, "")
	st.S3.Region = r.str("STORAGE_S3_REGION", yf.Storage.S3.Region, d.Storage.S3.Region)
	st.S3.Bucket = r.str("STORAGE_S3_BUCKET", yf.Storage.S3.Bucket, "")
	st.S3.Prefix = r.str("STORAGE_S3_PREFIX", yf.Storage.S3.Prefix, "")
	st.S3.AccessKeyID = r.str("STORAGE_S3_ACCESS_KEY_ID", yf.Storage.S3.AccessKeyID, "")
	st.S3.SecretAccessKey = r.str("STORAGE_S3_SECRET_ACCESS_KEY", yf.Storage.S3.SecretAccessKey, "")
	st.S3.ForcePathStyle = r.boolv("STORAGE_S3_FORCE_PATH_STYLE", yf.Storage.S3.ForcePathStyle, d.Storage.S3.ForcePathStyle)

	// geoip
	g := &c.GeoIP
	g.Provider = strings.ToLower(strings.TrimSpace(r.str("GEOIP_PROVIDER", yf.GeoIP.Provider, d.GeoIP.Provider)))
	g.BaseURL = r.str("GEOIP_BASE_URL", yf.GeoIP.BaseURL, "")
	if g.BaseURL == "" {
		if g.Provider == "ipinfo" {
			g.BaseURL = "https://ipinfo.io"
		} else {
			g.BaseURL = d.GeoIP.BaseURL
		}
	}
	g.Token = r.str("GEOIP_TOKEN", yf.GeoIP.Token, "")
	g.TimeoutMS = r.intv("GEOIP_TIMEOUT_MS", yf.GeoIP.TimeoutMS, d.GeoIP.TimeoutMS)
	g.DatabaseURL = r.str("GEOIP_DATABASE_URL", yf.GeoIP.DatabaseURL, "")
	g.DatabasePath = r.path("GEOIP_DATABASE_PATH", yf.GeoIP.DatabasePath, d.GeoIP.DatabasePath)
	g.DatabaseMaxBytes = r.int64v("GEOIP_DATABASE_MAX_BYTES", yf.GeoIP.DatabaseMaxBytes, d.GeoIP.DatabaseMaxBytes)
	g.RefreshIntervalHours = r.intv("GEOIP_REFRESH_INTERVAL_HOURS", yf.GeoIP.RefreshIntervalHours, d.GeoIP.RefreshIntervalHours)

	// tracing
	t := &c.Tracing
	t.Enabled = nil
	if v, ok := env("OTEL_ENABLED"); ok {
		if bv, err := strconv.ParseBool(strings.TrimSpace(v)); err == nil {
			t.Enabled = &bv
		}
	}
	if t.Enabled == nil && yf.Observability.Tracing.Enabled != nil {
		bv := *yf.Observability.Tracing.Enabled
		t.Enabled = &bv
	}
	t.Endpoint = r.str("OTEL_EXPORTER_OTLP_ENDPOINT", yf.Observability.Tracing.Endpoint, "")
	t.Headers = r.str("OTEL_EXPORTER_OTLP_HEADERS", yf.Observability.Tracing.Headers, "")
	t.Insecure = r.boolv("OTEL_EXPORTER_OTLP_INSECURE", yf.Observability.Tracing.Insecure, false)
	t.Protocol = normalizeTracingProtocol(r.str("OTEL_EXPORTER_OTLP_PROTOCOL", yf.Observability.Tracing.Protocol, d.Tracing.Protocol))
	t.SamplingRate = r.floatv([]string{"OTEL_TRACES_SAMPLER_ARG", "OTEL_SAMPLING_RATE"}, yf.Observability.Tracing.SamplingRate, d.Tracing.SamplingRate)

	return c.Normalize(), nil
}

func readYAML(path string, into *yamlFile) (dir string, err error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	if err := yaml.Unmarshal(raw, into); err != nil {
		return "", err
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		abs = path
	}
	return filepath.Dir(abs), nil
}
