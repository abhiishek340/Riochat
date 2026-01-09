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
