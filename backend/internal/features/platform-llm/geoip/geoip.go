// Package geoip resolves coarse locations (country, region, city, time zone,
// coordinates) for client addresses, from an HTTP lookup service or a local
// MaxMind-format database. Failures are returned to the caller, who is
// expected to ignore them: a lookup never decides an authentication flow.
package geoip

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// Provider names.
const (
	ProviderIPWhois = "ipwhois"
	ProviderIPInfo  = "ipinfo"
	ProviderMMDB    = "mmdb"
)

// Sources recorded with a result; accuracy is always "ip".
const (
	SourceAPI  = "geoip_api"
	SourceMMDB = "geoip_mmdb"
	Accuracy   = "ip"
)

// DefaultTimeout is the HTTP timeout when none is configured.
const DefaultTimeout = 2500 * time.Millisecond

// maxLookupBody bounds a lookup response.
const maxLookupBody = 1 << 20

// Location is a lookup result. Its field list is deliberately plain so that
// callers can convert it to their own identical struct.
type Location struct {
	CountryCode string
	Region      string
	City        string
	TimeZone    string
	Latitude    *float64
	Longitude   *float64
}

// Errors.
var (
	ErrSkipped      = errors.New("address is not eligible for lookup")
	ErrUnknownKind  = errors.New("unknown geoip provider")
	ErrNoDatabase   = errors.New("geoip database is not loaded")
	ErrLookupFailed = errors.New("geoip lookup failed")
)

// Doer executes requests through the outbound pool.
type Doer interface {
	Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error)
}

// Settings selects and parameterises a resolver.
type Settings struct {
	Provider string
	BaseURL  string
	Token    string
	Timeout  time.Duration
	// DatabasePath is the MaxMind file for the mmdb provider.
	DatabasePath string
	// Languages is the localized-name preference order (default "en").
	Languages []string
}

// Disabled reports provider values that mean "no GeoIP".
func Disabled(provider string) bool {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "none", "disabled":
		return true
	}
	return false
}

// Resolver looks up addresses.
type Resolver struct {
	kind  string
	base  string
	token string
	http  Doer
	to    time.Duration
	langs []string
	db    atomic.Pointer[Database]
}

// New builds a resolver. It returns (nil, nil) when GeoIP is disabled, so
// callers can treat a nil resolver as "skip silently". The mmdb provider opens
// its database file when it exists; a missing file is not an error here (the
// refresher may fetch it later and Lookup answers ErrNoDatabase until then).
func New(s Settings, h Doer) (*Resolver, error) {
	if Disabled(s.Provider) {
		return nil, nil
	}
	kind := strings.ToLower(strings.TrimSpace(s.Provider))
	r := &Resolver{kind: kind, token: strings.TrimSpace(s.Token), http: h, to: s.Timeout, langs: s.Languages}
	if r.to <= 0 {
		r.to = DefaultTimeout
	}
	if len(r.langs) == 0 {
		r.langs = []string{"en"}
	}
	switch kind {
	case ProviderIPWhois:
		r.base = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		if r.base == "" {
			r.base = "https://ipwho.is"
		}
	case ProviderIPInfo:
		r.base = strings.TrimRight(strings.TrimSpace(s.BaseURL), "/")
		if r.base == "" {
			r.base = "https://ipinfo.io"
		}
	case ProviderMMDB:
		if b, err := os.ReadFile(s.DatabasePath); err == nil {
			if db, err := OpenDatabase(b); err == nil {
				r.db.Store(db)
			}
		}
	default:
		return nil, fmt.Errorf("%w %q", ErrUnknownKind, s.Provider)
	}
	return r, nil
}

// Source is the value to record as the geo source of a result.
func (r *Resolver) Source() string {
	if r != nil && r.kind == ProviderMMDB {
		return SourceMMDB
	}
	return SourceAPI
}

// SetDatabase swaps the mmdb reader atomically.
func (r *Resolver) SetDatabase(db *Database) { r.db.Store(db) }

// Eligible parses an address (an "ip:port" form is accepted) and reports
// whether it is a public global-unicast address worth looking up.
func Eligible(raw string) (netip.Addr, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return netip.Addr{}, false
	}
	var a netip.Addr
	if ap, err := netip.ParseAddrPort(raw); err == nil {
		a = ap.Addr()
	} else if x, err := netip.ParseAddr(strings.Trim(raw, "[]")); err == nil {
		a = x
	} else {
		return netip.Addr{}, false
	}
	a = a.WithZone("").Unmap()
	if !a.IsGlobalUnicast() || a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsMulticast() || a.IsUnspecified() {
		return netip.Addr{}, false
	}
	return a, true
}

// Lookup resolves ip. Ineligible addresses return ErrSkipped.
func (r *Resolver) Lookup(ctx context.Context, ip string) (Location, error) {
	if r == nil {
		return Location{}, ErrSkipped
	}
	a, ok := Eligible(ip)
	if !ok {
		return Location{}, ErrSkipped
	}
	switch r.kind {
	case ProviderIPWhois:
		return r.ipwhois(ctx, a)
	case ProviderIPInfo:
		return r.ipinfo(ctx, a)
	default:
		return r.mmdb(a)
	}
}

func (r *Resolver) get(ctx context.Context, target string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, r.to)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, ErrLookupFailed
	}
	req.Header.Set("Accept", "application/json")
	resp, err := r.http.Do(ctx, outbound.Endpoint{URL: r.base, Kind: outbound.KindTrusted, Integration: "geoip"}, req)
	if err != nil {
		var ue *url.Error
		if errors.As(err, &ue) && ue.Err != nil {
			err = ue.Err
		}
		return nil, fmt.Errorf("%w: %v", ErrLookupFailed, err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxLookupBody))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrLookupFailed, err)
	}
	if resp.StatusCode/100 != 2 {
		return nil, fmt.Errorf("%w: status %d", ErrLookupFailed, resp.StatusCode)
	}
	return body, nil
}

func (r *Resolver) ipwhois(ctx context.Context, a netip.Addr) (Location, error) {
	body, err := r.get(ctx, r.base+"/"+url.PathEscape(a.String()))
	if err != nil {
		return Location{}, err
	}
	var doc struct {
		Success     *bool    `json:"success"`
		CountryCode string   `json:"country_code"`
		Region      string   `json:"region"`
		City        string   `json:"city"`
		Latitude    *float64 `json:"latitude"`
		Longitude   *float64 `json:"longitude"`
		Timezone    struct {
			ID string `json:"id"`
		} `json:"timezone"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return Location{}, fmt.Errorf("%w: unreadable response", ErrLookupFailed)
	}
	if doc.Success == nil || !*doc.Success {
		return Location{}, fmt.Errorf("%w: the service reported failure", ErrLookupFailed)
	}
	return Location{
		CountryCode: strings.ToUpper(strings.TrimSpace(doc.CountryCode)), Region: strings.TrimSpace(doc.Region),
		City: strings.TrimSpace(doc.City), TimeZone: strings.TrimSpace(doc.Timezone.ID),
		Latitude: doc.Latitude, Longitude: doc.Longitude,
	}, nil
}

func (r *Resolver) ipinfo(ctx context.Context, a netip.Addr) (Location, error) {
	target := r.base + "/" + url.PathEscape(a.String()) + "/json"
	if r.token != "" {
		target += "?token=" + url.QueryEscape(r.token)
	}
	body, err := r.get(ctx, target)
	if err != nil {
		return Location{}, err
	}
	var doc struct {
		Country  string `json:"country"`
		Region   string `json:"region"`
		City     string `json:"city"`
		Timezone string `json:"timezone"`
		Loc      string `json:"loc"`
		Bogon    bool   `json:"bogon"`
	}
	if json.Unmarshal(body, &doc) != nil {
		return Location{}, fmt.Errorf("%w: unreadable response", ErrLookupFailed)
	}
	if doc.Bogon {
		return Location{}, ErrSkipped
	}
	loc := Location{
		CountryCode: strings.ToUpper(strings.TrimSpace(doc.Country)), Region: strings.TrimSpace(doc.Region),
		City: strings.TrimSpace(doc.City), TimeZone: strings.TrimSpace(doc.Timezone),
	}
	if lat, lon, ok := strings.Cut(doc.Loc, ","); ok {
		la, e1 := strconv.ParseFloat(strings.TrimSpace(lat), 64)
		lo, e2 := strconv.ParseFloat(strings.TrimSpace(lon), 64)
		if e1 == nil && e2 == nil {
			loc.Latitude, loc.Longitude = &la, &lo
		}
	}
	return loc, nil
}

func (r *Resolver) mmdb(a netip.Addr) (Location, error) {
	db := r.db.Load()
	if db == nil {
		return Location{}, ErrNoDatabase
	}
	v, err := db.Lookup(a)
	if err != nil {
		return Location{}, err
	}
	m, _ := v.(map[string]any)
	loc := Location{}
	if c, ok := m["country"].(map[string]any); ok {
		loc.CountryCode, _ = c["iso_code"].(string)
	}
	if subs, ok := m["subdivisions"].([]any); ok && len(subs) > 0 {
		if first, ok := subs[0].(map[string]any); ok {
			loc.Region = r.localized(first["names"])
		}
	}
	if c, ok := m["city"].(map[string]any); ok {
		loc.City = r.localized(c["names"])
	}
	if l, ok := m["location"].(map[string]any); ok {
		if la, ok := l["latitude"].(float64); ok {
			if lo, ok := l["longitude"].(float64); ok {
				loc.Latitude, loc.Longitude = &la, &lo
			}
		}
		loc.TimeZone, _ = l["time_zone"].(string)
	}
	return loc, nil
}

// localized picks a name by language preference, then the first available in
// alphabetical language order.
func (r *Resolver) localized(v any) string {
	names, ok := v.(map[string]any)
	if !ok {
		return ""
	}
	for _, l := range r.langs {
		if s, ok := names[l].(string); ok && s != "" {
			return s
		}
	}
	keys := make([]string, 0, len(names))
	for k := range names {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if s, ok := names[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}
