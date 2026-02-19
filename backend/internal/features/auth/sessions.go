package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/middleware"
	"github.com/riochat/riochat/backend/internal/platform/token"
)

const (
	// TypeTwoFactorChallenge is the token_type of a login second-factor challenge.
	TypeTwoFactorChallenge = "2fa_challenge"

	challengeTTL        = 5 * time.Minute
	refreshGrace        = 15 * time.Second
	sessionTouchEvery   = time.Minute
	sessionIssuedLeeway = 2 * time.Minute
)

// Revoke reasons.
const (
	reasonLogout        = "user logout"
	reasonLogoutAll     = "logout of all sessions"
	reasonReuse         = "refresh token reuse"
	reasonPasswordReset = "password reset"
	reasonPasswordSet   = "password change"
)

// authPayload is the success (or challenge) body of every sign-in endpoint.
type authPayload struct {
	AccessToken             string    `json:"accessToken"`
	SessionID               string    `json:"sessionID"`
	ExpiresAt               time.Time `json:"expiresAt"`
	RefreshExpiresAt        time.Time `json:"refreshExpiresAt"`
	User                    userView  `json:"user"`
	TwoFactorRequired       bool      `json:"twoFactorRequired"`
	TwoFactorChallengeToken string    `json:"twoFactorChallengeToken,omitempty"`
	VerificationMethods     []string  `json:"verificationMethods,omitempty"`
	RefreshToken            string    `json:"refreshToken,omitempty"`
}

// signIn is a service result: the payload plus the refresh token the HTTP layer
// delivers according to the client platform.
type signIn struct {
	Payload authPayload
	Refresh string
}

// issueTokens signs an access/refresh pair for a session row.
func (s *Service) issueTokens(u *User, sess *Session) (access, refresh, jti string, accessExp, refreshExp time.Time, err error) {
	now := s.now()
	accessTTL, refreshTTL := s.tokenTTLs()
	claims := token.Claims{UserID: u.ID, Username: u.Username, Role: u.Role, SessionID: sess.ID}
	access, err = s.d.Tokens.Issue(claims, accessTTL)
	if err != nil {
		return
	}
	if parsed, perr := s.d.Tokens.Parse(access); perr == nil {
		jti = parsed.ID
	}
	claims.TokenType = token.TypeRefresh
	refresh, err = s.d.Tokens.Issue(claims, refreshTTL)
	accessExp, refreshExp = now.Add(accessTTL), now.Add(refreshTTL)
	return
}

// fillGeo adds a GeoIP lookup to ci when the request carries no geo at all.
func (s *Service) fillGeo(ctx context.Context, ci *clientInfo) {
	if s.h.GeoIP == nil || ci.ClientIP == "" {
		return
	}
	if ci.CountryCode != "" || ci.Region != "" || ci.City != "" || ci.TimeZone != "" {
		return
	}
	g, err := s.h.GeoIP.Lookup(ctx, ci.ClientIP)
	if err != nil {
		return
	}
	if ci.CountryCode == "" {
		ci.CountryCode = g.CountryCode
	}
	if ci.Region == "" {
		ci.Region = g.Region
	}
	if ci.City == "" {
		ci.City = g.City
	}
	if ci.TimeZone == "" {
		ci.TimeZone = g.TimeZone
	}
	if ci.Latitude == nil {
		ci.Latitude = g.Latitude
	}
	if ci.Longitude == nil {
		ci.Longitude = g.Longitude
	}
	if ci.GeoSource == "" && (g.CountryCode != "" || g.Region != "" || g.City != "") {
		ci.GeoSource, ci.GeoAccuracy = "geoip", "ip"
	}
}

// newSession creates a session for u and returns the tokens.
func (s *Service) newSession(ctx context.Context, u *User) (signIn, *Session, error) {
	now := s.now()
	ci := clientFrom(ctx)
	s.fillGeo(ctx, &ci)
	dev := parseDevice(ci.UserAgent)
	_, refreshTTL := s.tokenTTLs()
	sess := Session{
		SessionID: uuid.NewString(), UserID: u.ID,
		ClientIP: clip(ci.ClientIP, 64), UserAgent: clip(ci.UserAgent, 512),
		DeviceName: dev.Label, BrowserName: dev.Browser, OSName: dev.OS, DeviceType: dev.Type,
		GeoSource: ci.GeoSource, GeoAccuracy: ci.GeoAccuracy, CountryCode: clip(ci.CountryCode, 32),
		RegionName: clip(ci.Region, 64), CityName: clip(ci.City, 64), TimezoneName: clip(ci.TimeZone, 64),
		IPLatitude: ci.Latitude, IPLongitude: ci.Longitude,
		IssuedAt: now, LastSeenAt: &now, ExpiresAt: now.Add(refreshTTL),
	}
	var (
		access, refresh       string
		accessExp, refreshExp time.Time
	)
	err := s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&sess).Error; err != nil {
			return err
		}
		var jti string
		var err error
		access, refresh, jti, accessExp, refreshExp, err = s.issueTokens(u, &sess)
		if err != nil {
			return err
		}
		sess.RefreshTokenHash = sha256Hex(refresh)
		sess.AccessJTI = jti
		if err := tx.Model(&Session{}).Where("id = ?", sess.ID).Updates(map[string]any{
			"refresh_token_hash": sess.RefreshTokenHash, "access_jti": jti,
		}).Error; err != nil {
			return err
		}
		return tx.Model(&User{}).Where("id = ?", u.ID).Update("last_login_at", now).Error
	})
	if err != nil {
		return signIn{}, nil, err
	}
	// The view reads other tables, so it is built after the transaction ends
	// (SQLite hands out a single connection). It is the pre-login snapshot:
	// lastLoginAt and lastActiveAt still show the previous login (null on the
	// first one), which is what clients of the original contract receive.
	view, err := s.viewUser(ctx, u)
	if err != nil {
		return signIn{}, nil, err
	}
	u.LastLoginAt = &now
	return signIn{
		Payload: authPayload{
			AccessToken: access, SessionID: sess.SessionID, ExpiresAt: accessExp,
			RefreshExpiresAt: refreshExp, User: view,
		},
		Refresh: refresh,
	}, &sess, nil
}

// buildChallenge answers a successful first factor when a second one is due.
func (s *Service) buildChallenge(ctx context.Context, u *User) (signIn, error) {
	tok, err := s.d.Tokens.Issue(token.Claims{
		UserID: u.ID, Username: u.Username, Role: u.Role, TokenType: TypeTwoFactorChallenge,
	}, challengeTTL)
	if err != nil {
		return signIn{}, err
	}
	methods, err := s.availableMethods(ctx, u)
	if err != nil {
		return signIn{}, err
	}
	var vm []string
	for _, m := range methods {
		if m != methodNone {
			vm = append(vm, m)
		}
	}
	view, err := s.viewUser(ctx, u)
	if err != nil {
		return signIn{}, err
	}
	return signIn{Payload: authPayload{
		TwoFactorRequired: true, TwoFactorChallengeToken: tok, VerificationMethods: vm, User: view,
	}}, nil
}

// ---- access-token session validation -----------------------------------------

// ValidateSession implements middleware.SessionValidator.
func (s *Service) ValidateSession(ctx context.Context, check middleware.SessionCheck) error {
	if check.UserID == 0 || check.SessionID == 0 || check.IssuedAt.IsZero() {
		return ErrSessionInvalid
	}
	var sess Session
	err := s.conn(ctx).Where("id = ? AND user_id = ?", check.SessionID, check.UserID).First(&sess).Error
	if err != nil {
		if database.IsNotFound(err) {
			return ErrSessionInvalid
		}
		return err
	}
	now := s.now()
	if sess.RevokedAt != nil || !sess.ExpiresAt.After(now) {
		return ErrSessionInvalid
	}
	if check.IssuedAt.Add(sessionIssuedLeeway).Before(sess.CreatedAt) {
		return ErrSessionInvalid
	}
	s.touchSession(ctx, &sess, check.Audit)
	return nil
}

// touchSession refreshes last-seen and the device/IP/geo snapshot, at most once
// a minute unless something changed.
func (s *Service) touchSession(ctx context.Context, sess *Session, a middleware.AuditContext) {
	now := s.now()
	upd := map[string]any{}
	ip := clip(a.ClientIP, 64)
	ua := clip(a.UserAgent, 512)
	dev := parseDevice(a.UserAgent)
	ipChanged := ip != "" && ip != sess.ClientIP

	setStr := func(col string, cur *string, in string) {
		if in != *cur {
			upd[col] = in
			*cur = in
		}
	}
	if ip != "" {
		setStr("client_ip", &sess.ClientIP, ip)
	}
	if ua != "" {
		setStr("user_agent", &sess.UserAgent, ua)
		setStr("device_name", &sess.DeviceName, dev.Label)
		setStr("browser_name", &sess.BrowserName, dev.Browser)
		setStr("os_name", &sess.OSName, dev.OS)
		setStr("device_type", &sess.DeviceType, dev.Type)
	}
	geoStr := func(col string, cur *string, in string, max int) {
		in = clip(in, max)
		if ipChanged || in != "" {
			setStr(col, cur, in)
		}
	}
	// Precise (browser) geolocation outranks proxy-header geo for the same IP.
	if sess.GeoSource != "browser_geolocation" || ipChanged {
		geoStr("geo_source", &sess.GeoSource, a.GeoSource, 32)
		geoStr("geo_accuracy", &sess.GeoAccuracy, a.GeoAccuracy, 32)
	}
	geoStr("country_code", &sess.CountryCode, a.CountryCode, 32)
	geoStr("region_name", &sess.RegionName, a.Region, 64)
	geoStr("city_name", &sess.CityName, a.City, 64)
	geoStr("timezone_name", &sess.TimezoneName, a.TimeZone, 64)
	if ipChanged || a.Latitude != nil {
		if !floatEq(sess.IPLatitude, a.Latitude) {
			upd["ip_latitude"] = a.Latitude
			sess.IPLatitude = a.Latitude
		}
	}
	if ipChanged || a.Longitude != nil {
		if !floatEq(sess.IPLongitude, a.Longitude) {
			upd["ip_longitude"] = a.Longitude
			sess.IPLongitude = a.Longitude
		}
	}
	stale := sess.LastSeenAt == nil || now.Sub(*sess.LastSeenAt) >= sessionTouchEvery
	if len(upd) == 0 && !stale {
		return
	}
	upd["last_seen_at"] = now
	_ = s.conn(context.WithoutCancel(ctx)).Model(&Session{}).Where("id = ?", sess.ID).Updates(upd).Error
}

func floatEq(a, b *float64) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// ---- refresh rotation ----------------------------------------------------------

// refreshOutcome carries rotation results out of the transaction.
type refreshOutcome struct {
	in    signIn
	sess  Session
	user  User
	reuse bool
}
