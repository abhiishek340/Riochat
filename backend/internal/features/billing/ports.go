package billing

import (
	"context"
	"net/http"
	"sync"
	"time"
)

// The interfaces below are the seams to other areas. The billing area never
// reaches into tables it does not own: the integrator supplies adapters
// (see MountWith). Every port is optional; a missing port degrades as
// documented on each interface.

// SettingsStore reads and writes the `billing` namespace of the system
// settings. Keys are given without the namespace prefix ("mode", not
// "billing.mode"). Sensitive values are returned decrypted and must be stored
// encrypted by the implementation. When no store is wired a process-local
// MemorySettings is used, so changes do not survive a restart.
type SettingsStore interface {
	LoadBilling(ctx context.Context) (map[string]string, error)
	UpdateBilling(ctx context.Context, values map[string]string) error
}

// AuditEntry is one audit record request.
type AuditEntry struct {
	ActorUserID uint
	RequestID   string
	Action      string
	Resource    string
	ResourceID  string
	ClientIP    string
	UserAgent   string
	Detail      map[string]any
}

// AuditSink records audit entries. A missing sink never blocks an action.
type AuditSink interface {
	Record(ctx context.Context, e AuditEntry)
}

// ModelInfo describes one active platform model.
type ModelInfo struct {
	Name          string
	Vendor        string
	Icon          string
	SupportsVideo bool
}

// ModelCatalog answers questions about the platform model catalog. Without a
// catalog, prices are accepted for any model name and duration pricing cannot
// be confirmed.
type ModelCatalog interface {
	// Model returns the active platform model with the given name.
	Model(ctx context.Context, name string) (ModelInfo, bool, error)
	// ActiveModels lists every active platform model.
	ActiveModels(ctx context.Context) ([]ModelInfo, error)
}

// RateResolver returns the permission-group rate multiplier in percent for a
// user and model (100 = unchanged). extraGroupID is the permission group of the
// user's current subscription plan, or 0.
type RateResolver interface {
	RatePercent(ctx context.Context, userID uint, platformModel string, extraGroupID uint) (int, error)
}

// GroupMembers describes who belongs to a permission group.
type GroupMembers struct {
	Exists bool
	// Everyone is true for the default group, which every user belongs to.
	Everyone bool
	// UserIDs lists users with manual access to the group.
	UserIDs []uint
}

// PermissionGroups is the permission area's view needed by billing.
type PermissionGroups interface {
	DefaultGroupID(ctx context.Context) (uint, bool, error)
	GroupExists(ctx context.Context, id uint) (bool, error)
	Members(ctx context.Context, id uint) (GroupMembers, error)
}

// UserInfo is the public identity of a user used for labels.
type UserInfo struct {
	Username    string
	DisplayName string
	CreatedAt   time.Time
}

// UserDirectory resolves user identities.
type UserDirectory interface {
	Users(ctx context.Context, ids []uint) (map[uint]UserInfo, error)
}

// RunDirectory lets the call log search find ledger references through the
// conversation area's runs.
type RunDirectory interface {
	// RefNosForRequestID returns the ledger reference numbers of the runs that
	// were started by the given request id.
	RefNosForRequestID(ctx context.Context, requestID string) ([]string, error)
}

// Ports bundles the optional collaborators of the billing area.
type Ports struct {
	Runs     RunDirectory
	Settings SettingsStore
	Audit    AuditSink
	Models   ModelCatalog
	Rates    RateResolver
	Groups   PermissionGroups
	Users    UserDirectory

	// HTTPClient performs outbound calls to Stripe and OpenRouter. Supply one
	// that enforces the strict outbound policy; the default only sets timeouts.
	HTTPClient *http.Client
	// Location is the schedule and calendar-month time zone (default: the
	// process local zone).
	Location *time.Location
	// StripeBaseURL and OpenRouterURL override the upstream endpoints (tests).
	StripeBaseURL string
	OpenRouterURL string
	// BuiltinPricing is the gzip-compressed JSON snapshot of the official price
	// catalog shipped with the release (optional).
	BuiltinPricing []byte
}

// MemorySettings is a process-local SettingsStore.
type MemorySettings struct {
	mu sync.Mutex
	m  map[string]string
}

// NewMemorySettings returns a store seeded with the given values.
func NewMemorySettings(seed map[string]string) *MemorySettings {
	m := make(map[string]string, len(seed))
	for k, v := range seed {
		m[k] = v
	}
	return &MemorySettings{m: m}
}

// LoadBilling implements SettingsStore.
func (s *MemorySettings) LoadBilling(context.Context) (map[string]string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]string, len(s.m))
	for k, v := range s.m {
		out[k] = v
	}
	return out, nil
}

// UpdateBilling implements SettingsStore. The whole patch is validated first.
func (s *MemorySettings) UpdateBilling(_ context.Context, values map[string]string) error {
	if err := ValidateSettingValues(values); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range values {
		s.m[k] = v
	}
	return nil
}
