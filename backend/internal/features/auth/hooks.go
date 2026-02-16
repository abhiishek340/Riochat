package auth

import (
	"context"
	"net/http"
	"time"

	"gorm.io/gorm"
)

// Settings is the slice of the `auth` settings namespace this area reads. The
// token lifetimes and rate-limit switches are not here: they come from
// Config.Dynamic.Auth, which the settings area keeps up to date.
type Settings struct {
	LoginMaxFailures             int
	LoginLockMinutes             int
	LoginDefaultNextPath         string
	UsernameLoginEnabled         bool
	EmailLoginEnabled            bool
	ThirdPartyLoginEnabled       bool
	EmailRegistrationEnabled     bool
	EmailVerificationEnabled     bool
	PasswordResetEnabled         bool
	SMTPHost                     string
	SMTPPort                     int
	SMTPUsername                 string
	SMTPPassword                 string
	SMTPFrom                     string
	EmailAllowedDomains          string
	EmailBlockPlusAlias          bool
	AutoLinkVerifiedEmail        bool
	TurnstileRegistrationEnabled bool
	TurnstileSiteKey             string
	TurnstileSecretKey           string
}

// DefaultSettings returns the documented defaults.
func DefaultSettings() Settings {
	return Settings{
		LoginMaxFailures:         5,
		LoginLockMinutes:         15,
		LoginDefaultNextPath:     "/chat",
		UsernameLoginEnabled:     true,
		EmailLoginEnabled:        true,
		ThirdPartyLoginEnabled:   true,
		EmailRegistrationEnabled: true,
		SMTPPort:                 587,
		AutoLinkVerifiedEmail:    true,
	}
}

// SettingsSource supplies the live auth settings. The settings area provides
// the production implementation (decrypting sensitive values); it must be
// cheap because it is consulted on every request that needs a setting.
type SettingsSource interface {
	AuthSettings(ctx context.Context) Settings
}

// StaticSettings is a SettingsSource returning fixed values.
type StaticSettings Settings

// AuthSettings implements SettingsSource.
func (s StaticSettings) AuthSettings(context.Context) Settings { return Settings(s) }

// Mail is one outgoing message.
type Mail struct {
	To      string
	Subject string
	Text    string
	HTML    string
}

// Mailer delivers verification mail. The default implementation speaks SMTP
// using the SMTP settings.
type Mailer interface {
	Send(ctx context.Context, m Mail) error
}

// GeoInfo is a GeoIP lookup result.
type GeoInfo struct {
	CountryCode string
	Region      string
	City        string
	TimeZone    string
	Latitude    *float64
	Longitude   *float64
}

// GeoResolver looks up an IP address. Absent means no GeoIP is configured.
type GeoResolver interface {
	Lookup(ctx context.Context, ip string) (GeoInfo, error)
}

// AuditEntry is an entry for the generic audit trail.
type AuditEntry struct {
	UserID       uint
	Action       string
	ResourceType string
	ResourceID   string
	Detail       map[string]any
	ClientIP     string
	UserAgent    string
	RequestID    string
}

// AuditWriter receives audit entries. Writing is best-effort.
type AuditWriter interface {
	WriteAudit(ctx context.Context, e AuditEntry)
}

// Subscription is the billing view shown on the user object.
type Subscription struct {
	Tier      string
	PlanID    *uint
	PlanName  string
	Status    string
	ExpiresAt *time.Time
}

// SubscriptionLookup is implemented by the billing area.
type SubscriptionLookup interface {
	SubscriptionFor(ctx context.Context, userID uint) (Subscription, bool)
}

// AvatarFileChecker validates `file:` avatar references against file metadata.
type AvatarFileChecker interface {
	IsOwnedImage(ctx context.Context, userID uint, fileID string) bool
}

// AccountPurger removes the data other areas keep for a user when the account
// is deleted.
//
// BeforeDelete runs first and may veto the deletion (for example with
// knowledge_base.owner_file_reference). Purge runs inside the deletion
// transaction. AfterCommit runs after commit to remove stored objects and
// reports how many objects it tried and the paths it failed to delete.
type AccountPurger interface {
	BeforeDelete(ctx context.Context, userID uint) error
	Purge(ctx context.Context, tx *gorm.DB, userID uint) error
	AfterCommit(ctx context.Context, userID uint) (total int, failedPaths []string)
}

// Hooks connects collaborators that live outside this area. Every field is
// optional; the zero Hooks gives a self-contained area with spec defaults.
type Hooks struct {
	Settings      SettingsSource
	Mailer        Mailer
	GeoIP         GeoResolver
	Audit         AuditWriter
	Subscriptions SubscriptionLookup
	AvatarFiles   AvatarFileChecker
	Purgers       []AccountPurger
	// HTTPClient, when set, replaces the hardened outbound client (tests).
	HTTPClient *http.Client
}
