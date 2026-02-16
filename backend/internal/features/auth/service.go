package auth

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// bcryptCost is the work factor of stored password hashes. Tests lower it.
var bcryptCost = 12

// Service is the area's business logic. All methods are safe for concurrent
// use.
type Service struct {
	d   deps.Deps
	db  *gorm.DB
	h   Hooks
	log *logx.Logger

	grantMu  sync.Mutex
	grantVal string
	grantExp time.Time
}

func newService(d deps.Deps, h Hooks) *Service {
	return &Service{d: d, db: d.DB, h: h, log: d.Logger}
}

func (s *Service) now() time.Time { return s.d.Clock.Now().UTC() }

func (s *Service) cfg() *config.Config { return s.d.Config.Load() }

func (s *Service) settings(ctx context.Context) Settings {
	if s.h.Settings != nil {
		return s.h.Settings.AuthSettings(ctx)
	}
	return DefaultSettings()
}

func (s *Service) conn(ctx context.Context) *gorm.DB { return s.db.WithContext(ctx) }

// tokenTTLs returns the access and refresh lifetimes with the documented
// fallbacks for non-positive values.
func (s *Service) tokenTTLs() (access, refresh time.Duration) {
	a := s.cfg().Dynamic.Auth
	access, refresh = a.AccessTokenTTL, a.RefreshTokenTTL
	if access <= 0 {
		access = 24 * time.Hour
	}
	if refresh <= 0 {
		refresh = 30 * 24 * time.Hour
	}
	return access, refresh
}

// ---- auth events and audit --------------------------------------------------

// Event results.
const (
	resultSuccess   = "success"
	resultFailure   = "failure"
	resultChallenge = "challenge"
)

type eventInput struct {
	Type   string
	Result string
	Reason string
	UserID uint
	Detail map[string]any
}

// event records an auth event. It never fails the caller.
func (s *Service) event(ctx context.Context, in eventInput) {
	ci := clientFrom(ctx)
	detail := ""
	if len(in.Detail) > 0 {
		if b, err := json.Marshal(in.Detail); err == nil {
			detail = string(b)
		}
	}
	row := AuthEvent{
		RequestID:  clip(ci.RequestID, 64),
		UserID:     in.UserID,
		EventType:  clip(in.Type, 64),
		Result:     in.Result,
		Reason:     clip(in.Reason, 255),
		ClientIP:   clip(ci.ClientIP, 64),
		UserAgent:  clip(ci.UserAgent, 512),
		DetailJSON: detail,
		OccurredAt: s.now(),
	}
	if err := s.db.WithContext(context.WithoutCancel(ctx)).Create(&row).Error; err != nil {
		s.log.Warn("auth_event_write_failed", logx.F("event", in.Type), logx.Err(err))
	}
}

func (s *Service) audit(ctx context.Context, userID uint, action, resourceType, resourceID string, detail map[string]any) {
	if s.h.Audit == nil {
		return
	}
	ci := clientFrom(ctx)
	s.h.Audit.WriteAudit(ctx, AuditEntry{
		UserID: userID, Action: action, ResourceType: resourceType, ResourceID: resourceID,
		Detail: detail, ClientIP: ci.ClientIP, UserAgent: ci.UserAgent, RequestID: ci.RequestID,
	})
}

// sessionDetail is the flat event detail describing a session and its device.
func sessionDetail(sess *Session) map[string]any {
	m := map[string]any{
		"session_id":     sess.SessionID,
		"device_name":    sess.DeviceName,
		"browser_name":   sess.BrowserName,
		"os_name":        sess.OSName,
		"device_type":    sess.DeviceType,
		"geo_source":     sess.GeoSource,
		"geo_accuracy":   sess.GeoAccuracy,
		"client_ip":      sess.ClientIP,
		"country_code":   sess.CountryCode,
		"region_name":    sess.RegionName,
		"city_name":      sess.CityName,
		"timezone_name":  sess.TimezoneName,
		"location_label": locationLabel(sess.CityName, sess.RegionName, sess.CountryCode),
	}
	if sess.IPLatitude != nil {
		m["ip_latitude"] = *sess.IPLatitude
	}
	if sess.IPLongitude != nil {
		m["ip_longitude"] = *sess.IPLongitude
	}
	return m
}

// ---- lookups ----------------------------------------------------------------

func (s *Service) userByID(ctx context.Context, id uint) (*User, error) {
	var u User
	if err := s.conn(ctx).First(&u, id).Error; err != nil {
		return nil, database.Translate(err)
	}
	return &u, nil
}
