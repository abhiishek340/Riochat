package middleware

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

// AuditContext describes where a session request came from. Client address and
// user agent are always captured; geo headers are only trusted when the
// request arrived through a trusted proxy.
type AuditContext struct {
	ClientIP    string
	UserAgent   string
	GeoSource   string // "proxy_header_trusted" or ""
	GeoAccuracy string // "ip" or ""
	CountryCode string
	Region      string
	City        string
	TimeZone    string
	Latitude    *float64
	Longitude   *float64
}

// Location joins city, region and country code with ", ", skipping blanks.
func (a AuditContext) Location() string {
	var parts []string
	for _, p := range []string{a.City, a.Region, a.CountryCode} {
		if p != "" {
			parts = append(parts, p)
		}
	}
	return strings.Join(parts, ", ")
}

var (
	geoCountryHeaders   = []string{"CF-IPCountry", "X-Vercel-IP-Country", "X-Appengine-Country", "X-Geo-Country", "X-Country-Code"}
	geoRegionHeaders    = []string{"X-Vercel-IP-Country-Region", "X-Appengine-Region", "X-Geo-Region", "X-Region"}
	geoCityHeaders      = []string{"X-Vercel-IP-City", "X-Appengine-City", "X-Geo-City", "X-City"}
	geoTimezoneHeaders  = []string{"CF-Timezone", "X-Vercel-IP-Timezone", "X-Timezone"}
	geoLatitudeHeaders  = []string{"CF-IPLatitude", "X-Vercel-IP-Latitude", "X-Geo-Latitude", "X-Latitude"}
	geoLongitudeHeaders = []string{"CF-IPLongitude", "X-Vercel-IP-Longitude", "X-Geo-Longitude", "X-Longitude"}
)

func firstHeader(h http.Header, names []string) string {
	for _, n := range names {
		if v := strings.TrimSpace(h.Get(n)); v != "" {
			return v
		}
	}
	return ""
}

func parseCoord(s string) *float64 {
	if s == "" {
		return nil
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil
	}
	return &f
}

// AuditFromRequest builds the AuditContext of r. It relies on the state set up
// by TrustedProxy.
func AuditFromRequest(r *http.Request) AuditContext {
	state := reqctx.FromRequest(r)
	a := AuditContext{
		ClientIP:  strings.TrimSpace(state.ClientIP()),
		UserAgent: strings.TrimSpace(r.UserAgent()),
	}
	if !state.FromTrustedProxy() {
		return a
	}
	h := r.Header
	a.CountryCode = firstHeader(h, geoCountryHeaders)
	a.Region = firstHeader(h, geoRegionHeaders)
	a.City = firstHeader(h, geoCityHeaders)
	a.TimeZone = firstHeader(h, geoTimezoneHeaders)
	a.Latitude = parseCoord(firstHeader(h, geoLatitudeHeaders))
	a.Longitude = parseCoord(firstHeader(h, geoLongitudeHeaders))
	if a.CountryCode != "" || a.Region != "" || a.City != "" || a.TimeZone != "" || a.Latitude != nil || a.Longitude != nil {
		a.GeoSource, a.GeoAccuracy = "proxy_header_trusted", "ip"
	}
	return a
}
