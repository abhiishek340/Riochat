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

// Refresh rotates a refresh token and returns a new pair for the same session.
func (s *Service) Refresh(ctx context.Context, raw string) (signIn, error) {
	raw = strings.TrimSpace(raw)
	fail := func(reason string, detail map[string]any, uid uint) (signIn, error) {
		s.event(ctx, eventInput{Type: "token_refresh", Result: resultFailure, Reason: reason, UserID: uid, Detail: detail})
		return signIn{}, ErrInvalidRefreshToken
	}
	if raw == "" {
		return fail("missing token", nil, 0)
	}
	claims, err := s.d.Tokens.Parse(raw)
	if err != nil {
		return fail("invalid_refresh_token_parse", nil, 0)
	}
	if claims.TokenType != token.TypeRefresh || claims.SessionID == 0 || claims.UserID == 0 {
		return fail("invalid claims", nil, claims.UserID)
	}
	digest := sha256Hex(raw)
	now := s.now()
	var o refreshOutcome
	var failReason string
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		var sess Session
		q := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("id = ? AND user_id = ?", claims.SessionID, claims.UserID)
		if err := q.First(&sess).Error; err != nil {
			if database.IsNotFound(err) {
				failReason = "session_not_found"
				return nil
			}
			return err
		}
		if sess.RevokedAt != nil || !sess.ExpiresAt.After(now) {
			failReason = "session_revoked_or_expired"
			return nil
		}
		var u User
		if err := tx.First(&u, claims.UserID).Error; err != nil {
			if database.IsNotFound(err) {
				failReason = "user not found"
				return nil
			}
			return err
		}
		if u.Status != StatusActive {
			failReason = "user not active"
			return nil
		}
		matchesCurrent := sess.RefreshTokenHash != "" && constEq(sess.RefreshTokenHash, digest)
		matchesPrevious := sess.PreviousRefreshTokenHash != "" && constEq(sess.PreviousRefreshTokenHash, digest)
		switch {
		case matchesCurrent:
		case matchesPrevious && sess.RefreshRotatedAt != nil && now.Sub(*sess.RefreshRotatedAt) <= refreshGrace:
		case matchesPrevious:
			// A rotated-out token came back after the grace window: someone is
			// replaying a stolen token. Kill the session and keep that change.
			if err := tx.Model(&Session{}).Where("id = ?", sess.ID).
				Updates(map[string]any{"revoked_at": now, "revoke_reason": reasonReuse}).Error; err != nil {
				return err
			}
			o.reuse, o.sess = true, sess
			return nil
		default:
			failReason = "token digest mismatch"
			return nil
		}
		access, refresh, jti, accessExp, refreshExp, err := s.issueTokens(&u, &sess)
		if err != nil {
			return err
		}
		_, refreshTTL := s.tokenTTLs()
		upd := map[string]any{
			"previous_refresh_token_hash": sess.RefreshTokenHash,
			"refresh_token_hash":          sha256Hex(refresh),
			"refresh_rotated_at":          now,
			"access_jti":                  jti,
			"issued_at":                   now,
			"expires_at":                  now.Add(refreshTTL),
			"revoked_at":                  nil,
			"revoke_reason":               "",
		}
		if err := tx.Model(&Session{}).Where("id = ?", sess.ID).Updates(upd).Error; err != nil {
			return err
		}
		sess.ExpiresAt = now.Add(refreshTTL)
		o.sess, o.user = sess, u
		o.in = signIn{Payload: authPayload{
			AccessToken: access, SessionID: sess.SessionID, ExpiresAt: accessExp, RefreshExpiresAt: refreshExp,
		}, Refresh: refresh}
		return nil
	})
	if err != nil {
		return signIn{}, err
	}
	if o.reuse {
		s.event(ctx, eventInput{Type: "token_refresh", Result: resultFailure, Reason: "refresh token reuse detected",
			UserID: o.sess.UserID, Detail: sessionDetail(&o.sess)})
		return signIn{}, ErrInvalidRefreshToken
	}
	if failReason != "" {
		return fail(failReason, nil, claims.UserID)
	}
	s.touchSession(ctx, &o.sess, clientFrom(ctx).AuditContext)
	view, err := s.viewUser(ctx, &o.user)
	if err != nil {
		return signIn{}, err
	}
	o.in.Payload.User = view
	s.event(ctx, eventInput{Type: "token_refresh", Result: resultSuccess, UserID: o.user.ID, Detail: sessionDetail(&o.sess)})
	return o.in, nil
}

// ---- revocation and listing --------------------------------------------------------

func (s *Service) revokeByRowID(ctx context.Context, userID, rowID uint, reason string) error {
	return s.conn(ctx).Model(&Session{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", rowID, userID).
		Updates(map[string]any{"revoked_at": s.now(), "revoke_reason": reason}).Error
}

// RevokeUserSessions revokes every unrevoked session of a user. Other areas call
// it when they suspend or deactivate an account.
func RevokeUserSessions(ctx context.Context, db *gorm.DB, userID uint, reason string, at time.Time) error {
	return db.WithContext(ctx).Model(&Session{}).
		Where("user_id = ? AND revoked_at IS NULL", userID).
		Updates(map[string]any{"revoked_at": at, "revoke_reason": reason}).Error
}

func (s *Service) revokeAll(ctx context.Context, userID uint, reason string) error {
	return RevokeUserSessions(ctx, s.db, userID, reason, s.now())
}

// Logout revokes the caller's current session.
func (s *Service) Logout(ctx context.Context, userID, rowID uint) error {
	if userID == 0 || rowID == 0 {
		return ErrUnauthorized
	}
	var sess Session
	_ = s.conn(ctx).Where("id = ? AND user_id = ?", rowID, userID).First(&sess).Error
	if err := s.revokeByRowID(ctx, userID, rowID, reasonLogout); err != nil {
		s.event(ctx, eventInput{Type: "logout", Result: resultFailure, Reason: "revoke failed", UserID: userID})
		return err
	}
	s.event(ctx, eventInput{Type: "logout", Result: resultSuccess, UserID: userID, Detail: sessionDetail(&sess)})
	return nil
}

// LogoutAll revokes every session of the caller.
func (s *Service) LogoutAll(ctx context.Context, userID uint) error {
	if err := s.revokeAll(ctx, userID, reasonLogoutAll); err != nil {
		s.event(ctx, eventInput{Type: "logout_all", Result: resultFailure, Reason: "revoke failed", UserID: userID})
		return err
	}
	s.event(ctx, eventInput{Type: "logout_all", Result: resultSuccess, UserID: userID})
	return nil
}

// LogoutSession revokes one of the caller's sessions by its public id. Unknown,
// foreign and already revoked ids succeed without changing anything.
func (s *Service) LogoutSession(ctx context.Context, userID uint, publicID string) error {
	var sess Session
	found := s.conn(ctx).Where("session_id = ? AND user_id = ?", publicID, userID).First(&sess).Error == nil
	err := s.conn(ctx).Model(&Session{}).
		Where("session_id = ? AND user_id = ? AND revoked_at IS NULL", publicID, userID).
		Updates(map[string]any{"revoked_at": s.now(), "revoke_reason": reasonLogout}).Error
	if err != nil {
		return err
	}
	detail := map[string]any{"session_id": publicID}
	if found {
		detail = sessionDetail(&sess)
	}
	s.event(ctx, eventInput{Type: "logout", Result: resultSuccess, UserID: userID, Detail: detail})
	return nil
}

type sessionView struct {
	SessionID        string     `json:"sessionID"`
	Current          bool       `json:"current"`
	DeviceLabel      string     `json:"deviceLabel"`
	DeviceName       string     `json:"deviceName"`
	BrowserName      string     `json:"browserName"`
	OSName           string     `json:"osName"`
	DeviceType       string     `json:"deviceType"`
	ClientIP         string     `json:"clientIP"`
	LocationLabel    string     `json:"locationLabel"`
	GeoSource        string     `json:"geoSource"`
	GeoAccuracy      string     `json:"geoAccuracy"`
	CountryCode      string     `json:"countryCode"`
	RegionName       string     `json:"regionName"`
	CityName         string     `json:"cityName"`
	TimezoneName     string     `json:"timezoneName"`
	IPLatitude       *float64   `json:"ipLatitude"`
	IPLongitude      *float64   `json:"ipLongitude"`
	PreciseLatitude  *float64   `json:"preciseLatitude"`
	PreciseLongitude *float64   `json:"preciseLongitude"`
	PreciseAccuracyM *float64   `json:"preciseAccuracyMeters"`
	PreciseLocatedAt *time.Time `json:"preciseLocatedAt"`
	CreatedAt        time.Time  `json:"createdAt"`
	UpdatedAt        time.Time  `json:"updatedAt"`
	LastSeenAt       *time.Time `json:"lastSeenAt"`
	ExpiresAt        time.Time  `json:"expiresAt"`
}

func toSessionView(sess *Session, currentRow uint) sessionView {
	return sessionView{
		SessionID: sess.SessionID, Current: sess.ID == currentRow,
		DeviceLabel: sess.DeviceName, DeviceName: sess.DeviceName, BrowserName: sess.BrowserName,
		OSName: sess.OSName, DeviceType: sess.DeviceType, ClientIP: sess.ClientIP,
		LocationLabel: locationLabel(sess.CityName, sess.RegionName, sess.CountryCode),
		GeoSource:     sess.GeoSource, GeoAccuracy: sess.GeoAccuracy, CountryCode: sess.CountryCode,
		RegionName: sess.RegionName, CityName: sess.CityName, TimezoneName: sess.TimezoneName,
		IPLatitude: sess.IPLatitude, IPLongitude: sess.IPLongitude,
		PreciseLatitude: sess.PreciseLatitude, PreciseLongitude: sess.PreciseLongitude,
		PreciseAccuracyM: sess.PreciseAccuracyM, PreciseLocatedAt: sess.PreciseLocatedAt,
		CreatedAt: sess.CreatedAt, UpdatedAt: sess.UpdatedAt, LastSeenAt: sess.LastSeenAt,
		ExpiresAt: sess.ExpiresAt,
	}
}

// ListSessions returns the caller's live sessions, current first.
func (s *Service) ListSessions(ctx context.Context, userID, currentRow uint) ([]sessionView, error) {
	var rows []Session
	err := s.conn(ctx).Where("user_id = ? AND revoked_at IS NULL AND expires_at > ?", userID, s.now()).
		Order("updated_at DESC, id DESC").Find(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make([]sessionView, 0, len(rows))
	for i := range rows {
		sess := &rows[i]
		if sess.CountryCode == "" && sess.RegionName == "" && sess.CityName == "" && s.h.GeoIP != nil && sess.ClientIP != "" {
			if g, gerr := s.h.GeoIP.Lookup(ctx, sess.ClientIP); gerr == nil && (g.CountryCode != "" || g.Region != "" || g.City != "") {
				sess.CountryCode, sess.RegionName, sess.CityName = clip(g.CountryCode, 32), clip(g.Region, 64), clip(g.City, 64)
				if sess.TimezoneName == "" {
					sess.TimezoneName = clip(g.TimeZone, 64)
				}
				if sess.GeoSource == "" {
					sess.GeoSource, sess.GeoAccuracy = "geoip", "ip"
				}
				sess.IPLatitude, sess.IPLongitude = g.Latitude, g.Longitude
				_ = s.conn(ctx).Model(&Session{}).Where("id = ?", sess.ID).Updates(map[string]any{
					"country_code": sess.CountryCode, "region_name": sess.RegionName, "city_name": sess.CityName,
					"timezone_name": sess.TimezoneName, "geo_source": sess.GeoSource, "geo_accuracy": sess.GeoAccuracy,
					"ip_latitude": sess.IPLatitude, "ip_longitude": sess.IPLongitude,
				}).Error
			}
		}
		out = append(out, toSessionView(sess, currentRow))
	}
	// Current session first, the rest keep their recency order.
	for i := range out {
		if out[i].Current {
			cur := out[i]
			copy(out[1:i+1], out[:i])
			out[0] = cur
			break
		}
	}
	return out, nil
}

type locationInput struct {
	Latitude, Longitude float64
	AccuracyMeters      *float64 // nil when the client sent none
	Timezone            string
}

// UpdateLocation stores browser-reported coordinates on the current session.
func (s *Service) UpdateLocation(ctx context.Context, userID, rowID uint, in locationInput) (sessionView, error) {
	if in.Latitude < -90 || in.Latitude > 90 || in.Longitude < -180 || in.Longitude > 180 || (in.AccuracyMeters != nil && *in.AccuracyMeters < 0) {
		return sessionView{}, ErrInvalidLocation
	}
	tz := strings.TrimSpace(in.Timezone)
	if tz != "" && !validTimeZone(tz) {
		return sessionView{}, ErrInvalidTimeZone
	}
	now := s.now()
	upd := map[string]any{
		"geo_source": "browser_geolocation", "geo_accuracy": "precise",
		"precise_latitude": in.Latitude, "precise_longitude": in.Longitude,
		"precise_accuracy_m": in.AccuracyMeters, "precise_located_at": now, "last_seen_at": now,
	}
	if tz != "" {
		upd["timezone_name"] = tz
	}
	res := s.conn(ctx).Model(&Session{}).
		Where("id = ? AND user_id = ? AND revoked_at IS NULL", rowID, userID).Updates(upd)
	if res.Error != nil {
		return sessionView{}, res.Error
	}
	if res.RowsAffected == 0 {
		return sessionView{}, ErrSessionInvalid
	}
	var sess Session
	if err := s.conn(ctx).Where("id = ? AND user_id = ?", rowID, userID).First(&sess).Error; err != nil {
		return sessionView{}, errors.Join(ErrInternal, err)
	}
	detail := sessionDetail(&sess)
	detail["precise_latitude"] = in.Latitude
	detail["precise_longitude"] = in.Longitude
	if in.AccuracyMeters != nil {
		detail["precise_accuracy_meters"] = *in.AccuracyMeters
	}
	s.event(ctx, eventInput{Type: "session_location_update", Result: resultSuccess, UserID: userID, Detail: detail})
	return toSessionView(&sess, rowID), nil
}
