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

// UpdateConfig merges the request into the stored configuration, validates the
// result and saves it atomically.
func (s *Service) UpdateConfig(ctx context.Context, actor Actor, req UpdateRequest) (*ConfigView, error) {
	if err := actor.requireSuper(); err != nil {
		return nil, err
	}
	s.updateMu.Lock()
	defer s.updateMu.Unlock()

	cur, err := s.store.read(ctx)
	if err != nil {
		return nil, err
	}
	next := cur

	if req.BaseURL != nil {
		v := strings.TrimSpace(*req.BaseURL)
		if v == "" {
			v = DefaultBaseURL
		}
		if err := ValidateBaseURL(v); err != nil {
			return nil, invalidConfig("base url")
		}
		next.BaseURL = v
	}
	switch {
	case req.ClearAPIKey != nil && *req.ClearAPIKey:
		next.APIKey = ""
	case req.APIKey != nil:
		next.APIKey = strings.TrimSpace(*req.APIKey)
	}
	if req.Model != nil {
		v := strings.TrimSpace(*req.Model)
		if v == "" {
			return nil, invalidConfig("model is empty")
		}
		next.Model = v
	}
	if req.TimeoutSeconds != nil {
		if v := *req.TimeoutSeconds; v < 1 || v > maxTimeoutSeconds {
			return nil, invalidConfig("timeoutSeconds out of range")
		}
		next.TimeoutSeconds = *req.TimeoutSeconds
	}
	if req.MaxConcurrency != nil {
		if v := *req.MaxConcurrency; v < 1 || v > maxMaxConcurrency {
			return nil, invalidConfig("maxConcurrency out of range")
		}
		next.MaxConcurrency = *req.MaxConcurrency
	}
	if req.QueueCapacity != nil {
		if v := *req.QueueCapacity; v < 1 || v > maxQueueCapacity {
			return nil, invalidConfig("queueCapacity out of range")
		}
		next.QueueCapacity = *req.QueueCapacity
	}
	if req.Policy != nil {
		p, err := Policy{
			InputTextCategories:   req.Policy.InputTextCategories,
			OutputTextCategories:  req.Policy.OutputTextCategories,
			InputImageCategories:  req.Policy.InputImageCategories,
			OutputImageCategories: req.Policy.OutputImageCategories,
		}.normalize()
		if err != nil {
			return nil, invalidConfig(err.Error())
		}
		p.Version = cur.Policy.Version
		if !p.sameCategories(cur.Policy) {
			p.Version++
		}
		next.Policy = p.nonNil()
	}
	if req.Enabled != nil {
		next.Enabled = *req.Enabled
	}

	if next.Enabled {
		if err := ValidateBaseURL(next.BaseURL); err != nil {
			return nil, invalidConfig("base url")
		}
		if !next.Policy.AnySelected() || strings.TrimSpace(next.BaseURL) == "" ||
			strings.TrimSpace(next.Model) == "" || next.APIKey == "" {
			return nil, ErrConfigRequired
		}
	}

	if err := s.store.write(ctx, next); err != nil {
		return nil, err
	}
	s.exec.resize(next.MaxConcurrency, next.QueueCapacity)
	v := viewOf(next)
	return &v, nil
}

// currentConfig is the runtime view of the configuration (cached briefly).
func (s *Service) currentConfig(ctx context.Context) (Config, error) { return s.store.get(ctx) }

// ---- probe ------------------------------------------------------------------

// ProbeTarget is the outcome of probing one modality.
type ProbeTarget struct {
	Valid     bool   `json:"valid"`
	Model     string `json:"model,omitempty"`
	LatencyMS int64  `json:"latencyMS"`
	Error     string `json:"error,omitempty"`
}

// ProbeResult is the data of POST /probe.
type ProbeResult struct {
	Text  ProbeTarget `json:"text"`
	Image ProbeTarget `json:"image"`
}

// probePNG is a 1x1 transparent PNG.
const probePNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAAEklEQVR4nAAFAPr/AgAAAAADAAAPAANCp/UOAAAAAElFTkSuQmCC"

const probeImageError = "moderation response missing image category_applied_input_types"

// Probe checks the saved configuration against the provider with one text and
// one image request. Provider failures are reported per target, not as errors.
func (s *Service) Probe(ctx context.Context, actor Actor) (*ProbeResult, error) {
	if err := actor.requireSuper(); err != nil {
		return nil, err
	}
	if s.provider == nil {
		return nil, errors.New("content moderation: no provider available")
	}
	cfg, err := s.store.read(ctx)
	if err != nil {
		return nil, err
	}
	cc := cfg.clientConfig()
	res := &ProbeResult{}

	start := s.clock.Now()
	resp, perr := s.provider.CheckText(ctx, cc, "hello", Scope{Direction: DirectionInput, Modality: ModalityText})
	res.Text = probeOutcome(resp, perr, cfg.Model, s.clock.Now().Sub(start), false)

	pngBytes, _ := base64.StdEncoding.DecodeString(probePNG)
	start = s.clock.Now()
	resp, perr = s.provider.CheckImages(ctx, cc, []Image{{Data: pngBytes, MIME: "image/png"}},
		Scope{Direction: DirectionInput, Modality: ModalityImage})
	res.Image = probeOutcome(resp, perr, cfg.Model, s.clock.Now().Sub(start), true)
	return res, nil
}

func probeOutcome(resp *Response, err error, configured string, took time.Duration, needImage bool) ProbeTarget {
	t := ProbeTarget{LatencyMS: took.Milliseconds()}
	if err != nil {
		t.Error = probeErrorText(err)
		return t
	}
	valid := false
	imageDeclared := false
	if resp != nil {
		for _, r := range resp.Results {
			if r.Categories != nil {
				valid = true
			}
			for _, types := range r.AppliedInputTypes {
				for _, ty := range types {
					if strings.EqualFold(strings.TrimSpace(ty), ModalityImage) {
						imageDeclared = true
					}
				}
			}
		}
	}
	if !valid {
		t.Error = publicErrorText[CodeInvalidResponse]
		return t
	}
	if needImage && !imageDeclared {
		t.Error = probeImageError
		return t
	}
	t.Valid = true
	t.Model = resp.Model
	if t.Model == "" {
		t.Model = configured
	}
	return t
}

func probeErrorText(err error) string {
	var pe *ProviderError
	switch {
	case errors.As(err, &pe):
		return pe.Error()
	case errors.Is(err, context.DeadlineExceeded):
		return publicErrorText[CodeTimeout]
	}
	return "content moderation probe failed"
}

// ---- statistics ---------------------------------------------------------------

// Stats returns the daily aggregates in range. Nil bounds use the defaults.
func (s *Service) Stats(ctx context.Context, actor Actor, from, to *time.Time) ([]StatItem, error) {
	if !actor.isAdmin() {
		return nil, ErrAdminRequired
	}
	start, end, ok := statsWindow(s.clock.Now(), from, to)
	if !ok {
		return nil, ErrInvalidQuery
	}
	return s.listStats(ctx, start, end)
}

// ---- events -----------------------------------------------------------------

// ListEvents pages through recorded events.
func (s *Service) ListEvents(ctx context.Context, actor Actor, f EventFilter, page, pageSize int) (*EventPage, error) {
	if err := actor.requireSuper(); err != nil {
		return nil, err
	}
	res, err := s.listEvents(ctx, f, page, pageSize)
	if err != nil {
		return nil, err
	}
	return &res, nil
}

// GetEvent returns the detail of one event and audits the view.
func (s *Service) GetEvent(ctx context.Context, actor Actor, info AuditInfo, publicID string) (*EventDetail, error) {
	if err := actor.requireSuper(); err != nil {
		return nil, err
	}
	e, err := s.eventByPublicID(ctx, publicID)
	if err != nil {
		return nil, err
	}
	d := s.buildDetail(ctx, e)
	s.recordAudit(ctx, actor, info, AuditActionEventView, e.PublicID, map[string]any{"retainedTextAvailable": d.TextAvailable})
	return &d, nil
}

// EventImage returns the decrypted bytes of an isolated image and audits the
// read.
func (s *Service) EventImage(ctx context.Context, actor Actor, info AuditInfo, publicID string, index int) (data []byte, mime string, err error) {
	if err := actor.requireSuper(); err != nil {
		return nil, "", err
	}
	e, err := s.eventByPublicID(ctx, publicID)
	if err != nil {
		return nil, "", err
	}
	if !s.contentRetained(e) || s.objects == nil {
		return nil, "", ErrEventNotFound
	}
	var meta *imageMeta
	for _, m := range imagesFromJSON(e.ImageMetaJSON) {
		if m.Index == index {
			m := m
			meta = &m
			break
		}
	}
	if meta == nil {
		return nil, "", ErrEventNotFound
	}
	payload, err := s.objects.Get(ctx, meta.StoragePath)
	if err != nil {
		return nil, "", err
	}
	plain, _, err := s.secrets.DecryptBound(string(payload), imageBinding(e.PublicID, meta.Index))
	if err != nil {
		return nil, "", err
	}
	data, err = base64.StdEncoding.DecodeString(plain)
	if err != nil {
		return nil, "", err
	}
	mime = meta.MIMEType
	if mime == "" {
		mime = "image/png"
	}
	s.recordAudit(ctx, actor, info, AuditActionEventImageView, e.PublicID, map[string]any{"imageIndex": index})
	return data, mime, nil
}

func (s *Service) recordAudit(ctx context.Context, actor Actor, info AuditInfo, action, resourceID string, detail map[string]any) {
	if s.audit == nil {
		return
	}
	err := s.audit.Record(ctx, AuditEntry{
		ActorID: actor.UserID, RequestID: info.RequestID, ClientIP: info.ClientIP, UserAgent: info.UserAgent,
		Action: action, ResourceType: AuditResourceEvent, ResourceID: resourceID, Detail: detail,
	})
	if err != nil {
		s.log.Warn("content_moderation_audit_failed", logx.F("action", action), errField(err))
	}
}

// ReencryptSecrets moves the stored API key and the retained flagged texts to
// the active encryption key. Isolated images are not re-encrypted: they expire
// on their own and need the previous key until then.
func (s *Service) ReencryptSecrets(ctx context.Context) (changed int, err error) {
	if ok, err := s.store.reencrypt(ctx); err != nil {
		return changed, err
	} else if ok {
		changed++
	}
	var rows []Event
	if err := s.db.WithContext(ctx).Where("encrypted_text <> ''").Find(&rows).Error; err != nil {
		return changed, err
	}
	for _, e := range rows {
		next, did, err := s.secrets.ReencryptBound(e.EncryptedText, textBinding(e.PublicID))
		if err != nil {
			return changed, err
		}
		if !did {
			continue
		}
		if err := s.db.WithContext(ctx).Model(&Event{}).Where("id = ?", e.ID).Update("encrypted_text", next).Error; err != nil {
			return changed, err
		}
		changed++
	}
	return changed, nil
}
