package personalprovider

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// This file lists what the area needs from the outside. Every port has a
// working default, so Mount alone yields a functional area; the integrator
// replaces ports through MountWith.

// ---- settings --------------------------------------------------------------------

// SettingsNamespace is the namespace of this area's rows in system_settings.
const SettingsNamespace = "personal_provider"

// Setting keys.
const (
	SettingEnabled      = "enabled"
	SettingMaxPerUser   = "max_per_user"
	SettingBlockedHosts = "blocked_hosts"
)

// DefaultMaxPerUser applies when the stored value is missing or not positive.
const DefaultMaxPerUser = 10

// Settings is the effective configuration of the feature.
type Settings struct {
	Enabled      bool
	MaxPerUser   int
	BlockedHosts string
}

// normalized applies the runtime fallbacks.
func (s Settings) normalized() Settings {
	if s.MaxPerUser <= 0 {
		s.MaxPerUser = DefaultMaxPerUser
	}
	return s
}

// SettingsSource supplies the live settings; it is read on every request.
type SettingsSource interface {
	PersonalProviderSettings(ctx context.Context) (Settings, error)
}

// dbSettings reads the namespace from system_settings. A missing table or
// missing rows give the defaults (feature off).
type dbSettings struct{ db *gorm.DB }

func (s dbSettings) PersonalProviderSettings(ctx context.Context) (Settings, error) {
	out := Settings{MaxPerUser: DefaultMaxPerUser}
	var rows []settingRow
	if err := s.db.WithContext(ctx).Where("namespace = ?", SettingsNamespace).Find(&rows).Error; err != nil {
		return out, database.Translate(err)
	}
	for _, r := range rows {
		v := strings.TrimSpace(r.Value)
		switch r.Key {
		case SettingEnabled:
			b, err := strconv.ParseBool(v)
			out.Enabled = err == nil && b
		case SettingMaxPerUser:
			if n, err := strconv.Atoi(v); err == nil {
				out.MaxPerUser = n
			}
		case SettingBlockedHosts:
			out.BlockedHosts = r.Value
		}
	}
	return out.normalized(), nil
}

// ---- upstream catalog -------------------------------------------------------------

// ListRequest asks an upstream for its model catalog.
type ListRequest struct {
	Protocol string
	BaseURL  string
	APIKey   string
}

// ModelLister fetches the model names an endpoint offers. Implementations must
// enforce the outbound address policy at connect time, honor ctx, and report a
// non-success HTTP answer as *UpstreamStatusError. Raw upstream text must never
// be put into returned errors.
type ModelLister interface {
	ListModels(ctx context.Context, req ListRequest) ([]string, error)
}

// UpstreamStatusError is a non-success answer of the upstream.
type UpstreamStatusError struct{ Status int }

func (e *UpstreamStatusError) Error() string {
	return fmt.Sprintf("upstream answered with status %d", e.Status)
}

// classifyUpstream maps a lister failure to the public error. 401 and 403 mean
// the key was rejected; everything else is "unavailable".
func classifyUpstream(err error) error {
	var se *UpstreamStatusError
	if errors.As(err, &se) && (se.Status == 401 || se.Status == 403) {
		return ErrUpstreamRejected.WithCause(err)
	}
	return ErrUpstreamDown.WithCause(err)
}

// ---- owners ----------------------------------------------------------------------

// Owner is the identity of a provider's owner as shown to administrators.
type Owner struct {
	PublicID    string
	Username    string
	DisplayName string
	Email       string
	AvatarURL   string
}

// OwnerDirectory resolves owners for the administrator listing.
type OwnerDirectory interface {
	// Lookup returns the owners that exist and are not deleted, by user id.
	Lookup(ctx context.Context, ids []uint) (map[uint]Owner, error)
	// SearchIDs returns ids of users whose username, display name or email
	// contains q (case-insensitive). Deleted users are excluded.
	SearchIDs(ctx context.Context, q string) ([]uint, error)
}

// ---- audit ------------------------------------------------------------------------

// Auditor records audit entries after a successful operation. It must never
// fail the caller.
type Auditor interface {
	RecordRequest(c *gin.Context, action, resource, resourceID string, detail any)
}

// Options replaces the default ports.
type Options struct {
	Settings SettingsSource
	Lister   ModelLister
	Owners   OwnerDirectory
	Audit    Auditor
	Policy   OutboundPolicy
}
