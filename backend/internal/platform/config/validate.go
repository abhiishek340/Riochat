package config

import (
	"errors"
	"fmt"
	"net"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
)

const metadataHostname = "metadata.google.internal"

var dnsLabelRe = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)

var allowedSynchronous = map[string]bool{"OFF": true, "NORMAL": true, "FULL": true, "EXTRA": true}
var allowedTempStore = map[string]bool{"DEFAULT": true, "FILE": true, "MEMORY": true}

// Validate checks a static configuration. Any returned error is fatal at
// startup. The leading text of each message is a contract for operators.
func Validate(c *Config) error {
	if c == nil {
		return errors.New("config is nil")
	}
	if c.App.Env != EnvDev && c.App.Env != EnvProd {
		return fmt.Errorf("APP_ENV must be dev or prod, got %q", c.App.Env)
	}

	switch c.Database.Driver {
	case DriverPostgres:
	case DriverSQLite:
		if strings.TrimSpace(c.Database.SQLite.Path) == "" && strings.TrimSpace(c.Database.SQLite.DSN) == "" {
			return errors.New("sqlite requires SQLITE_PATH or SQLITE_DSN")
		}
		if !allowedSynchronous[c.Database.SQLite.Synchronous] {
			return fmt.Errorf("unsupported SQLITE_SYNCHRONOUS %q (allowed: OFF, NORMAL, FULL, EXTRA)", c.Database.SQLite.Synchronous)
		}
		if !allowedTempStore[c.Database.SQLite.TempStore] {
			return fmt.Errorf("unsupported SQLITE_TEMP_STORE %q (allowed: DEFAULT, FILE, MEMORY)", c.Database.SQLite.TempStore)
		}
	default:
		return fmt.Errorf("unsupported database driver %q", c.Database.Driver)
	}

	switch c.Cache.Driver {
	case CacheRedis, CacheMemory:
	default:
		return fmt.Errorf("unsupported cache driver %q (use redis or memory)", c.Cache.Driver)
	}

	switch c.Storage.Backend {
	case StorageLocal:
	case StorageS3:
		if strings.TrimSpace(c.Storage.S3.Bucket) == "" {
			return errors.New("STORAGE_S3_BUCKET is required when STORAGE_BACKEND is s3")
		}
		if strings.TrimSpace(c.Storage.S3.Region) == "" {
			return errors.New("STORAGE_S3_REGION is required when STORAGE_BACKEND is s3")
		}
	default:
		return fmt.Errorf("unsupported storage backend %q (use local or s3)", c.Storage.Backend)
	}

	if err := ValidateSSRFAllowlists(c.Security.SSRFAllowedHosts, c.Security.SSRFAllowedCIDRs); err != nil {
		return err
	}
	if err := validateTurnstileURL(c.Security.TurnstileSiteverifyURL); err != nil {
		return err
	}
	if strings.TrimSpace(c.Security.DataEncryptionKey) == "" {
		return errors.New("DATA_ENCRYPTION_KEY must not be empty")
	}
	for _, p := range c.TrustedProxyList() {
		if _, err := ParseProxyEntry(p); err != nil {
			return fmt.Errorf("TRUSTED_PROXIES entry %q is invalid: %w", p, err)
		}
	}

	if c.IsProd() {
		jwt := strings.TrimSpace(c.Security.JWTSecret)
		if jwt == "" || jwt == DevJWTSecret {
			return errors.New("JWT_SECRET must be explicitly set in production")
		}
		if len(jwt) < 16 {
			return errors.New("JWT_SECRET is too short (minimum 16 characters)")
		}
		key := strings.TrimSpace(c.Security.DataEncryptionKey)
		if key == DevDataEncryptionKey {
			return errors.New("DATA_ENCRYPTION_KEY must be explicitly set in production")
		}
		if len(key) < 32 {
			return errors.New("DATA_ENCRYPTION_KEY is too short (minimum 32 characters)")
		}
		origins := c.CORSOrigins()
		if len(origins) == 0 {
			return errors.New("CORS_ALLOW_ORIGIN must be set in production")
		}
		for _, o := range origins {
			if o == "*" {
				return errors.New("CORS_ALLOW_ORIGIN must not be * in production")
			}
		}
		if !c.IsLocal() {
			if err := requireHTTPS("PUBLIC_API_BASE_URL", c.Server.PublicAPIBaseURL); err != nil {
				return err
			}
			if err := requireHTTPS("PUBLIC_WEB_BASE_URL", c.Server.PublicWebBaseURL); err != nil {
				return err
			}
		}
	}
	return nil
}

func requireHTTPS(name, raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("%s must be an https URL with a host in production", name)
	}
	return nil
}

// ParseProxyEntry parses a trusted-proxy entry: a bare IP (treated as /32 or
// /128) or a CIDR.
func ParseProxyEntry(entry string) (netip.Prefix, error) {
	entry = strings.TrimSpace(entry)
	if strings.Contains(entry, "/") {
		return netip.ParsePrefix(entry)
	}
	addr, err := netip.ParseAddr(entry)
	if err != nil {
		return netip.Prefix{}, err
	}
	addr = addr.Unmap()
	return netip.PrefixFrom(addr, addr.BitLen()), nil
}

// ValidateSSRFAllowlists checks the outbound allowlists: hosts must be
// syntactically valid DNS names (not IP literals, not the metadata hostname)
// and CIDRs must parse and not be IPv4-mapped IPv6.
func ValidateSSRFAllowlists(hosts, cidrs string) error {
	for _, h := range SplitList(hosts) {
		if err := validateAllowedHost(h); err != nil {
			return fmt.Errorf("SSRF_ALLOWED_HOSTS entry %q is invalid: %w", h, err)
		}
	}
	for _, c := range SplitList(cidrs) {
		p, err := netip.ParsePrefix(c)
		if err != nil {
			return fmt.Errorf("SSRF_ALLOWED_CIDRS entry %q is invalid: %w", c, err)
		}
		if p.Addr().Is4In6() {
			return fmt.Errorf("SSRF_ALLOWED_CIDRS entry %q must not be an IPv4-mapped IPv6 range", c)
		}
	}
	return nil
}

func validateAllowedHost(h string) error {
	h = strings.TrimSuffix(strings.ToLower(h), ".")
	if h == "" || len(h) > 253 {
		return errors.New("not a valid host name")
	}
	if _, err := netip.ParseAddr(strings.Trim(h, "[]")); err == nil {
		return errors.New("must be a DNS name, not an IP address")
	}
	if h == metadataHostname {
		return errors.New("the cloud metadata host cannot be allowlisted")
	}
	for _, label := range strings.Split(h, ".") {
		if !dnsLabelRe.MatchString(label) {
			return errors.New("not a valid DNS name")
		}
	}
	return nil
}

func validateTurnstileURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return errors.New("TURNSTILE_SITEVERIFY_URL must be an http(s) URL")
	}
	if u.User != nil {
		return errors.New("TURNSTILE_SITEVERIFY_URL must not contain user info")
	}
	host := strings.ToLower(u.Hostname())
	if host == metadataHostname {
		return errors.New("TURNSTILE_SITEVERIFY_URL must not target a metadata address")
	}
	if ip := net.ParseIP(host); ip != nil && (ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast()) {
		return errors.New("TURNSTILE_SITEVERIFY_URL must not target a link-local address")
	}
	return nil
}

// SecurityWarnings lists the non-fatal startup warnings (log each with the
// message "security_config_warning" and the text as its detail field).
func SecurityWarnings(c *Config) []string {
	var out []string
	if IsPlaceholderSecret(c.Security.DataEncryptionKey) {
		out = append(out, "DATA_ENCRYPTION_KEY is a placeholder: anyone with the source can decrypt stored credentials; set a random value of at least 32 characters")
	}
	for _, k := range c.Security.DataEncryptionKeysPrev {
		if IsPlaceholderSecret(k) {
			out = append(out, "DATA_ENCRYPTION_KEYS_PREVIOUS contains a placeholder: data encrypted with it stays readable to anyone with the source until re-encrypted; remove it after rotation")
			break
		}
	}
	if IsPlaceholderSecret(c.Security.JWTSecret) {
		out = append(out, "JWT_SECRET is a placeholder: anyone with the source can forge session tokens; set a random value of at least 32 characters (existing sessions will be invalidated)")
	}
	return out
}
