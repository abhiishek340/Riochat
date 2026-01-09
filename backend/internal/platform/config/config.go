// Package config loads, validates and holds the process configuration.
//
// There are two layers. The static layer (this package's Load) comes from
// environment variables, then a YAML file, then built-in defaults, and needs a
// restart to change. The dynamic layer is stored in the database by the
// settings feature and overlaid onto a copy of the static layer; readers see
// either layer through a Holder, which swaps whole snapshots atomically.
package config

import (
	"strings"
	"time"
)

// Built-in development secrets. They ship in the repository, so production
// rejects them and every other deployment gets a startup warning.
const (
	DevJWTSecret         = "riochat-dev-jwt-secret-do-not-use-in-production"
	DevDataEncryptionKey = "riochat-dev-data-encryption-key-do-not-use-in-production"
)

// Environment names.
const (
	EnvDev  = "dev"
	EnvProd = "prod"
)

// Driver names after normalization.
const (
	DriverPostgres = "postgres"
	DriverSQLite   = "sqlite"
	CacheRedis     = "redis"
	CacheMemory    = "memory"
	StorageLocal   = "local"
	StorageS3      = "s3"
)

// Config is one complete, consistent configuration snapshot. Treat a snapshot
// obtained from a Holder as read-only; use Clone before modifying.
type Config struct {
	App      AppConfig
	Branding BrandingConfig
	Server   ServerConfig
	Security SecurityConfig
	Database DatabaseConfig
	Cache    CacheConfig
	Storage  StorageConfig
	GeoIP    GeoIPConfig
	Tracing  TracingConfig
	Admin    AdminConfig
	Dynamic  DynamicConfig
	Local    LocalConfig
}

// AppConfig identifies the application.
type AppConfig struct {
	Name string
	Env  string // "dev" or "prod" after normalization
}

// BrandingConfig customizes user-facing identity (YAML only).
type BrandingConfig struct {
	Title                 string
	ShortName             string
	Description           string
	LogoURL               string
	FaviconURL            string
	PWAIcon192URL         string
	PWAIcon512URL         string
	PWAMaskableIcon512URL string
	AppleTouchIcon180URL  string
}

// ServerConfig covers the HTTP listener and edge behavior.
type ServerConfig struct {
	HTTPPort              string
	ListenAddr            string // explicit listen address; overrides HTTPPort when set
	CORSAllowOrigin       string // comma separated
	TrustedProxies        string // comma separated IPs or CIDRs
	PublicAPIBaseURL      string
	PublicWebBaseURL      string
	FrontendDistDir       string
	ReadHeaderTimeoutSecs int
	ReadTimeoutSecs       int
	IdleTimeoutSecs       int
	MaxHeaderBytes        int
	ShutdownTimeoutSecs   int
}

// SecurityConfig holds secrets and outbound-safety switches.
type SecurityConfig struct {
	JWTSecret              string
	MCPUserContextSecret   string
	DataEncryptionKey      string
	DataEncryptionKeysPrev []string
	SSRFProtectionEnabled  bool
	SSRFAllowedHosts       string // comma separated
	SSRFAllowedCIDRs       string // comma separated
	TurnstileSiteverifyURL string
}

// DatabaseConfig selects and tunes the relational store.
type DatabaseConfig struct {
	Driver   string
	Postgres PostgresConfig
	SQLite   SQLiteConfig
	Redis    RedisConfig
}

// PostgresConfig tunes the PostgreSQL pool.
type PostgresConfig struct {
	DSN                string
	MaxOpenConns       int
	MaxIdleConns       int
	ConnMaxLifetimeMin int
	ConnMaxIdleTimeMin int
}

// SQLiteConfig tunes the SQLite connection.
type SQLiteConfig struct {
	Path          string
	DSN           string
	MaxOpenConns  int
	BusyTimeoutMS int
	CacheSizeKB   int
	MmapSizeBytes int64
	Synchronous   string
	TempStore     string
}

// RedisConfig describes the Redis connection used by the redis cache driver.
type RedisConfig struct {
	Addr                  string
	Username              string
	Password              string
	DB                    int
	TLSEnabled            bool
	TLSInsecureSkipVerify bool
}

// CacheConfig selects the cache backend.
type CacheConfig struct {
	Driver string
}

// StorageConfig selects object storage.
type StorageConfig struct {
	Backend string
	Local   LocalStorageConfig
	S3      S3Config
}

// LocalStorageConfig roots the local object store.
type LocalStorageConfig struct {
	RootDir string
}

// S3Config configures the S3-compatible object store.
type S3Config struct {
	Endpoint        string
	Region          string
	Bucket          string
	Prefix          string
	AccessKeyID     string
	SecretAccessKey string
	ForcePathStyle  bool
}

// GeoIPConfig configures IP geolocation.
type GeoIPConfig struct {
	Provider             string
	BaseURL              string
	Token                string
	TimeoutMS            int
	DatabaseURL          string
	DatabasePath         string
	DatabaseMaxBytes     int64
	RefreshIntervalHours int
}

// TracingConfig configures OpenTelemetry export.
type TracingConfig struct {
	Enabled      *bool // nil means "enabled iff an endpoint is configured"
	Endpoint     string
	Headers      string
	Insecure     bool
	Protocol     string // "grpc" or "http"
	SamplingRate float64
}

// AdminConfig names the bootstrap administrator.
type AdminConfig struct {
	Username    string
	DisplayName string
}

// LocalConfig describes the desktop-sidecar run mode.
type LocalConfig struct {
	Enabled bool
	DataDir string
}

// DynamicConfig is the subset of database-backed business settings the
// platform itself needs. The settings feature fills it from system_settings
// and publishes the merged snapshot through a Holder.
type DynamicConfig struct {
	Auth AuthDynamic
}

// AuthDynamic holds token lifetimes and global rate-limit switches.
type AuthDynamic struct {
	AccessTokenTTL     time.Duration
	RefreshTokenTTL    time.Duration
	RateLimitEnabled   bool
	RateLimitAuthRPM   int
	RateLimitPublicRPM int
}

// Default dynamic values (a fresh install before any database overlay).
const (
	DefaultAccessTokenTTL  = 24 * time.Hour
	DefaultRefreshTokenTTL = 720 * time.Hour
	DefaultAuthRPM         = 60
	DefaultPublicRPM       = 30
)

// Default returns a configuration populated with every built-in default.
func Default() Config {
	return Config{
		App: AppConfig{Name: "Riochat", Env: EnvProd},
		Branding: BrandingConfig{
			Title:                 "Riochat",
			ShortName:             "Riochat",
			Description:           "Riochat is a multi-model AI conversation system.",
			FaviconURL:            "/favicon.ico",
			PWAIcon192URL:         "/pwa/icon-192.png",
			PWAIcon512URL:         "/pwa/icon-512.png",
			PWAMaskableIcon512URL: "/pwa/icon-maskable-512.png",
			AppleTouchIcon180URL:  "/pwa/apple-touch-icon.png",
		},
		Server: ServerConfig{
			HTTPPort:              "8080",
			CORSAllowOrigin:       "http://127.0.0.1:8080,http://localhost:8080,tauri://localhost,http://tauri.localhost",
			FrontendDistDir:       "../apps/web/out",
			ReadHeaderTimeoutSecs: 10,
			ReadTimeoutSecs:       120,
			IdleTimeoutSecs:       120,
			MaxHeaderBytes:        1 << 20,
			ShutdownTimeoutSecs:   10,
		},
		Security: SecurityConfig{
			JWTSecret:              DevJWTSecret,
			DataEncryptionKey:      DevDataEncryptionKey,
			TurnstileSiteverifyURL: "https://challenges.cloudflare.com/turnstile/v0/siteverify",
		},
		Database: DatabaseConfig{
			Driver: DriverPostgres,
			Postgres: PostgresConfig{
				DSN:                "host=127.0.0.1 port=5432 user=riochat password=riochat dbname=riochat sslmode=disable TimeZone=Asia/Shanghai",
				MaxOpenConns:       30,
				MaxIdleConns:       10,
				ConnMaxLifetimeMin: 60,
				ConnMaxIdleTimeMin: 10,
			},
			SQLite: SQLiteConfig{
				Path:          "./data/riochat.db",
				MaxOpenConns:  1,
				BusyTimeoutMS: 5000,
				CacheSizeKB:   20480,
				MmapSizeBytes: 268435456,
				Synchronous:   "NORMAL",
				TempStore:     "MEMORY",
			},
			Redis: RedisConfig{Addr: "127.0.0.1:6379"},
		},
		Cache: CacheConfig{Driver: CacheRedis},
		Storage: StorageConfig{
			Backend: StorageLocal,
			Local:   LocalStorageConfig{RootDir: "./storage"},
			S3:      S3Config{Region: "auto", ForcePathStyle: true},
		},
		GeoIP: GeoIPConfig{
			Provider:             "ipwhois",
			BaseURL:              "https://ipwho.is",
			TimeoutMS:            2500,
			DatabasePath:         "./data/geoip/geoip.mmdb",
			DatabaseMaxBytes:     104857600,
			RefreshIntervalHours: 168,
		},
		Tracing: TracingConfig{Protocol: "grpc", SamplingRate: 1},
		Admin:   AdminConfig{Username: "admin", DisplayName: "System Admin"},
		Dynamic: DynamicConfig{Auth: AuthDynamic{
			AccessTokenTTL:     DefaultAccessTokenTTL,
			RefreshTokenTTL:    DefaultRefreshTokenTTL,
			RateLimitAuthRPM:   DefaultAuthRPM,
			RateLimitPublicRPM: DefaultPublicRPM,
		}},
	}
}

// Clone returns a copy that shares no mutable memory with c.
func (c *Config) Clone() *Config {
	if c == nil {
		return &Config{}
	}
	out := *c
	out.Security.DataEncryptionKeysPrev = append([]string(nil), c.Security.DataEncryptionKeysPrev...)
	if c.Tracing.Enabled != nil {
		v := *c.Tracing.Enabled
		out.Tracing.Enabled = &v
	}
	return &out
}

// Normalize applies the cross-field rules that must hold for any snapshot,
// whether static or overlaid. It mutates c and returns it for chaining.
func (c *Config) Normalize() *Config {
	if c.Dynamic.Auth.AccessTokenTTL <= 0 {
		c.Dynamic.Auth.AccessTokenTTL = DefaultAccessTokenTTL
	}
	if c.Dynamic.Auth.RefreshTokenTTL <= 0 {
		c.Dynamic.Auth.RefreshTokenTTL = DefaultRefreshTokenTTL
	}
	return c
}

// IsProd reports whether the normalized environment is production.
func (c *Config) IsProd() bool { return c.App.Env == EnvProd }

// IsLocal reports whether the desktop-sidecar mode is active.
func (c *Config) IsLocal() bool { return c.Local.Enabled }

// SplitList splits a comma separated value, trimming blanks and dropping empty
// items.
func SplitList(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// TrustedProxyList returns the configured trusted proxy entries.
func (c *Config) TrustedProxyList() []string { return SplitList(c.Server.TrustedProxies) }

// CORSOrigins returns the configured CORS allowlist entries.
func (c *Config) CORSOrigins() []string { return SplitList(c.Server.CORSAllowOrigin) }

// ListenAddress returns the address the HTTP server binds.
func (c *Config) ListenAddress() string {
	if c.Server.ListenAddr != "" {
		return c.Server.ListenAddr
	}
	return ":" + c.Server.HTTPPort
}

// IsPlaceholderSecret reports whether v offers no protection: empty, one of
// the built-in development defaults, or starting with "change-me".
func IsPlaceholderSecret(v string) bool {
	v = strings.TrimSpace(v)
	if v == "" || v == DevJWTSecret || v == DevDataEncryptionKey {
		return true
	}
	return strings.HasPrefix(strings.ToLower(v), "change-me")
}
