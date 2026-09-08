package contentmoderation

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/capability"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/lifecycle"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Service is the moderation engine and the admin operations around it.
type Service struct {
	db      *gorm.DB
	clock   clock.Clock
	secrets *secretbox.KeyRing
	log     *logx.Logger
	caps    capability.Provider
	lc      *lifecycle.Manager

	store    *configStore
	provider Provider
	runs     RunStore
	files    FileStore
	objects  ObjectStore
	audit    AuditWriter
	users    UserDirectory
	notifier Notifier

	exec        *executor
	imageMaxDim int

	// budgetFn computes the wait budget of a turn; replaceable in tests.
	budgetFn func(timeoutSeconds int) time.Duration

	updateMu sync.Mutex

	trackMu       sync.Mutex
	tracked       map[string]*Run
	pendingBlocks map[string]BlockSpec
	notified      map[string]bool

	closed atomic.Bool
}

var shared atomic.Pointer[Service]

// Shared returns the service created by the most recent Mount, for the chat
// code that needs to begin runs. It is nil before Mount.
func Shared() *Service { return shared.Load() }

// Actor identifies the administrator behind an operation.
type Actor struct {
	UserID uint
	Role   string
}

func (a Actor) isSuperAdmin() bool {
	return strings.EqualFold(strings.TrimSpace(a.Role), reqctx.RoleSuperAdmin)
}

func (a Actor) isAdmin() bool {
	return a.isSuperAdmin() || strings.EqualFold(strings.TrimSpace(a.Role), reqctx.RoleAdmin)
}

func (a Actor) requireSuper() error {
	if !a.isSuperAdmin() {
		return ErrSuperAdminRequired
	}
	return nil
}

// AuditInfo carries the request facts recorded with an audited view.
type AuditInfo struct {
	RequestID string
	ClientIP  string
	UserAgent string
}

func newService(d deps.Deps, ports Ports) *Service {
	s := &Service{
		db: d.DB, clock: d.Clock, secrets: d.Secrets, log: d.Logger, caps: d.Capabilities, lc: d.Lifecycle,
		provider: ports.Provider, runs: ports.Runs, files: ports.Files, objects: ports.Objects,
		audit: ports.Audit, users: ports.Users, notifier: ports.Notifier,
		imageMaxDim:   ports.ImageMaxDimension,
		tracked:       map[string]*Run{},
		pendingBlocks: map[string]BlockSpec{},
		notified:      map[string]bool{},
		budgetFn:      defaultWaitBudget,
	}
	if s.imageMaxDim <= 0 {
		s.imageMaxDim = defaultImageMaxPx
	}
	if s.log == nil {
		s.log = logx.Nop()
	}
	s.store = &configStore{db: d.DB, secrets: d.Secrets, clock: d.Clock}
	if s.provider == nil {
		doer := ports.HTTP
		if doer == nil && d.Config != nil {
			holder := d.Config
			doer = newOutboundClient(func() config.SecurityConfig { return holder.Load().Security })
		}
		if doer != nil {
			s.provider = newHTTPProvider(doer, d.Clock.Now)
		}
	}
	cfg, err := s.store.read(context.Background())
	maxConc, queueCap := DefaultMaxConcurrency, DefaultQueueCapacity
	if err == nil {
		maxConc, queueCap = cfg.MaxConcurrency, cfg.QueueCapacity
	}
	s.exec = newExecutor(maxConc, queueCap, func(r any) {
		s.log.Error("content_moderation_task_panic", logx.F("panic", r))
	})
	return s
}

// defaultWaitBudget is twice the check timeout, at most one minute.
func defaultWaitBudget(timeoutSeconds int) time.Duration {
	if timeoutSeconds <= 0 {
		timeoutSeconds = DefaultTimeoutSeconds
	}
	d := 2 * time.Duration(timeoutSeconds) * time.Second
	if d > 60*time.Second {
		d = 60 * time.Second
	}
	return d
}

func errField(err error) logx.Field { return logx.Err(err) }

// detachedCtx returns a context that survives the caller's cancellation and has
// its own deadline.
func detachedCtx(ctx context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(reqctx.Detach(ctx), d)
}

// ---- configuration ----------------------------------------------------------

// ConfigView is the configuration as shown to administrators; the API key is
// only ever present in masked form.
type ConfigView struct {
	Enabled        bool   `json:"enabled"`
	BaseURL        string `json:"baseUrl"`
	APIKeyMasked   string `json:"apiKeyMasked,omitempty"`
	HasAPIKey      bool   `json:"hasAPIKey"`
	Model          string `json:"model"`
	TimeoutSeconds int    `json:"timeoutSeconds"`
	MaxConcurrency int    `json:"maxConcurrency"`
	QueueCapacity  int    `json:"queueCapacity"`
	Policy         Policy `json:"policy"`
}

// CategoryCatalog lists the categories that can be selected.
type CategoryCatalog struct {
	Text  []string `json:"text"`
	Image []string `json:"image"`
}

// ConfigResponse is the data of GET /config.
type ConfigResponse struct {
	Config     ConfigView      `json:"config"`
	Categories CategoryCatalog `json:"categories"`
}

func viewOf(c Config) ConfigView {
	return ConfigView{
		Enabled: c.Enabled, BaseURL: c.BaseURL, APIKeyMasked: maskKey(c.APIKey), HasAPIKey: c.APIKey != "",
		Model: c.Model, TimeoutSeconds: c.TimeoutSeconds, MaxConcurrency: c.MaxConcurrency,
		QueueCapacity: c.QueueCapacity, Policy: c.Policy.nonNil(),
	}
}

// GetConfig returns the masked configuration and the category catalog.
func (s *Service) GetConfig(ctx context.Context, actor Actor) (*ConfigResponse, error) {
	if err := actor.requireSuper(); err != nil {
		return nil, err
	}
	cfg, err := s.store.read(ctx)
	if err != nil {
		return nil, err
	}
	return &ConfigResponse{
		Config:     viewOf(cfg),
		Categories: CategoryCatalog{Text: TextCategories(), Image: ImageCategories()},
	}, nil
}

// PolicyInput is the policy part of an update. The version is never accepted
// from clients.
type PolicyInput struct {
	InputTextCategories   []string `json:"inputTextCategories"`
	OutputTextCategories  []string `json:"outputTextCategories"`
	InputImageCategories  []string `json:"inputImageCategories"`
	OutputImageCategories []string `json:"outputImageCategories"`
}

// UpdateRequest is a partial configuration update; nil fields stay unchanged.
type UpdateRequest struct {
	Enabled        *bool        `json:"enabled"`
	BaseURL        *string      `json:"baseUrl"`
	APIKey         *string      `json:"apiKey"`
	ClearAPIKey    *bool        `json:"clearAPIKey"`
	Model          *string      `json:"model"`
	TimeoutSeconds *int         `json:"timeoutSeconds"`
	MaxConcurrency *int         `json:"maxConcurrency"`
	QueueCapacity  *int         `json:"queueCapacity"`
	Policy         *PolicyInput `json:"policy"`
}

// UnmarshalJSON decodes the request so that a numeric limit that is not a whole
// number (1.5, 1e100) reaches validation as an out-of-range value, answering
// 400 content_moderation.invalid_config instead of a generic body error. A
// value that is not a number at all still fails decoding.
func (r *UpdateRequest) UnmarshalJSON(b []byte) error {
	var aux struct {
		Enabled        *bool        `json:"enabled"`
		BaseURL        *string      `json:"baseUrl"`
		APIKey         *string      `json:"apiKey"`
		ClearAPIKey    *bool        `json:"clearAPIKey"`
		Model          *string      `json:"model"`
		TimeoutSeconds *float64     `json:"timeoutSeconds"`
		MaxConcurrency *float64     `json:"maxConcurrency"`
		QueueCapacity  *float64     `json:"queueCapacity"`
		Policy         *PolicyInput `json:"policy"`
	}
	if err := json.Unmarshal(b, &aux); err != nil {
		return err
	}
	*r = UpdateRequest{
		Enabled: aux.Enabled, BaseURL: aux.BaseURL, APIKey: aux.APIKey, ClearAPIKey: aux.ClearAPIKey,
		Model: aux.Model, TimeoutSeconds: wholeInt(aux.TimeoutSeconds), MaxConcurrency: wholeInt(aux.MaxConcurrency),
		QueueCapacity: wholeInt(aux.QueueCapacity), Policy: aux.Policy,
	}
	return nil
}

// wholeInt converts a decoded number; anything that is not a whole number in a
// sane range becomes 0, which every limit rejects.
func wholeInt(f *float64) *int {
	if f == nil {
		return nil
	}
	v := 0
	if *f == math.Trunc(*f) && math.Abs(*f) < 1e9 {
		v = int(*f)
	}
	return &v
}

func invalidConfig(reason string) error {
	return ErrInvalidConfig.WithCause(errors.New(reason))
}
