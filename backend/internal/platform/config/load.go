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
