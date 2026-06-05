package personalprovider

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"
)

// Service implements the user-facing and administrator operations and the
// runtime integration for personal model providers.
type Service struct {
	db       *gorm.DB
	clk      clock.Clock
	secrets  *secretbox.KeyRing
	log      *logx.Logger
	settings SettingsSource
	lister   ModelLister
	owners   OwnerDirectory
	policy   OutboundPolicy

	userLocks [64]sync.Mutex
}

// NewService builds a service. Nil ports in opts get their defaults; a nil
// Lister means every upstream call fails as "unavailable".
func NewService(db *gorm.DB, clk clock.Clock, secrets *secretbox.KeyRing, log *logx.Logger, opts Options) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	if log == nil {
		log = logx.Nop()
	}
	s := &Service{db: db, clk: clk, secrets: secrets, log: log,
		settings: opts.Settings, lister: opts.Lister, owners: opts.Owners, policy: opts.Policy}
	if s.settings == nil {
		s.settings = dbSettings{db: db}
	}
	if s.owners == nil {
		s.owners = dbOwners{db: db}
	}
	if s.policy == nil {
		s.policy = PublicOnlyPolicy{}
	}
	return s
}

func (s *Service) lockUser(userID uint) func() {
	m := &s.userLocks[userID%uint(len(s.userLocks))]
	m.Lock()
	return m.Unlock
}

// ---- views ------------------------------------------------------------------------

// AccessInfo is what GET /me/model-providers/access reports.
type AccessInfo struct {
	Enabled        bool     `json:"enabled"`
	MaxPerUser     int      `json:"maxPerUser"`
	Protocols      []string `json:"protocols"`
	ModelProtocols []string `json:"modelProtocols"`
}

// ProviderView is the user-facing rendition of a provider. It carries no key
// material except the masked hint.
type ProviderView struct {
	ID            string         `json:"id"`
	Name          string         `json:"name"`
	Icon          string         `json:"icon"`
	Protocol      string         `json:"protocol"`
	BaseURL       string         `json:"baseURL"`
	Host          string         `json:"host"`
	KeyHint       string         `json:"keyHint"`
	Models        []EnabledModel `json:"models"`
	Status        string         `json:"status"`
	Source        string         `json:"source"`
	LastError     string         `json:"lastError"`
	LastCheckedAt *time.Time     `json:"lastCheckedAt"`
	CreatedAt     time.Time      `json:"createdAt"`
	UpdatedAt     time.Time      `json:"updatedAt"`
}

func viewOf(p Provider) ProviderView {
	v := ProviderView{
		ID: p.PublicID, Name: p.Name, Icon: p.Icon, Protocol: p.Protocol, BaseURL: p.BaseURL, Host: p.Host,
		KeyHint: p.KeyHint, Models: p.Models(), Status: p.Status, Source: p.Source, LastError: p.LastError,
		CreatedAt: p.CreatedAt.UTC(), UpdatedAt: p.UpdatedAt.UTC(),
	}
	if p.LastCheckedAt != nil {
		t := p.LastCheckedAt.UTC()
		v.LastCheckedAt = &t
	}
	return v
}

// CatalogModel is one model reported by an upstream.
type CatalogModel struct {
	Name               string   `json:"name"`
	SuggestedProtocols []string `json:"suggestedProtocols"`
}

// ---- settings and policy helpers ------------------------------------------------------

func (s *Service) loadSettings(ctx context.Context) (Settings, error) {
	st, err := s.settings.PersonalProviderSettings(ctx)
	if err != nil {
		return Settings{}, err
	}
	return st.normalized(), nil
}

func (s *Service) requireEnabled(ctx context.Context) (Settings, error) {
	st, err := s.loadSettings(ctx)
	if err != nil {
		return st, err
	}
	if !st.Enabled {
		return st, ErrDisabled
	}
	return st, nil
}

// checkURL parses raw and applies the outbound policy and the deny-list.
func (s *Service) checkURL(raw string, st Settings) (parsedURL, error) {
	u, err := parseBaseURL(raw)
	if err != nil {
		return u, err
	}
	if err := s.policy.CheckHost(u.Host); err != nil {
		return u, ErrBlockedHost
	}
	if parseDenyList(st.BlockedHosts).matches(u.Host) {
		return u, ErrBlockedHost
	}
	return u, nil
}

func (s *Service) now() time.Time { return s.clk.Now().UTC() }

// ---- upstream -----------------------------------------------------------------------

// fetchCatalog asks the upstream for its models. The result holds valid,
// distinct names in ascending order.
func (s *Service) fetchCatalog(ctx context.Context, protocol, baseURL, key string) ([]string, error) {
	if s.lister == nil {
		return nil, ErrUpstreamDown.WithCause(errors.New("no upstream client available"))
	}
	names, err := s.lister.ListModels(ctx, ListRequest{Protocol: protocol, BaseURL: baseURL, APIKey: key})
	if err != nil {
		return nil, classifyUpstream(err)
	}
	valid := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); validModelName(n) {
			valid = append(valid, n)
		}
	}
	return sortedUnique(valid), nil
}

func catalogViews(protocol string, names []string) []CatalogModel {
	out := make([]CatalogModel, 0, len(names))
	for _, n := range names {
		out = append(out, CatalogModel{Name: n, SuggestedProtocols: suggestProtocols(protocol, n)})
	}
	return out
}

// ModelInput is one requested model: a bare name or an object with protocols.
type ModelInput struct {
	Name      string   `json:"name" binding:"required,max=200"`
	Protocols []string `json:"protocols" binding:"max=2,dive,max=64"`
}

// UnmarshalJSON accepts a plain string (legacy) or an object.
func (m *ModelInput) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err == nil {
		*m = ModelInput{Name: name}
		return nil
	}
	type plain ModelInput
	var p plain
	dec := json.NewDecoder(strings.NewReader(string(b)))
	if err := dec.Decode(&p); err != nil {
		return err
	}
	*m = ModelInput(p)
	return nil
}

// selectModels validates the requested models against the live catalog.
func selectModels(req []ModelInput, catalog []string, providerProtocol string) ([]EnabledModel, error) {
	if len(req) > maxModelsPerReq {
		return nil, ErrInvalidModels
	}
	known := make(map[string]bool, len(catalog))
	for _, n := range catalog {
		known[n] = true
	}
	out := make([]EnabledModel, 0, len(req))
	seen := map[string]bool{}
	for _, m := range req {
		// Names are taken literally: surrounding whitespace is not a valid name.
		name := m.Name
		if !validModelName(name) || !known[name] {
			return nil, ErrInvalidModels
		}
		if seen[name] {
			continue
		}
		seen[name] = true
		protos := m.Protocols
		if len(protos) == 0 {
			protos = suggestProtocols(providerProtocol, name)
		}
		norm, err := normalizeModelProtocols(protos)
		if err != nil {
			return nil, err
		}
		out = append(out, EnabledModel{Name: name, Protocols: norm})
	}
	return out, nil
}

// revalidateSaved checks already enabled models against a fresh catalog.
func revalidateSaved(saved []EnabledModel, catalog []string) ([]EnabledModel, error) {
	in := make([]ModelInput, 0, len(saved))
	for _, m := range saved {
		in = append(in, ModelInput{Name: m.Name, Protocols: m.Protocols})
	}
	return selectModels(in, catalog, "")
}

// ---- reads ----------------------------------------------------------------------------

// Access reports the feature switch, the cap and the protocol lists.
func (s *Service) Access(ctx context.Context) (AccessInfo, error) {
	st, err := s.loadSettings(ctx)
	if err != nil {
		return AccessInfo{}, err
	}
	return AccessInfo{Enabled: st.Enabled, MaxPerUser: st.MaxPerUser,
		Protocols: ProviderProtocols(), ModelProtocols: ModelProtocols()}, nil
}

// List returns the caller's providers in creation order.
func (s *Service) List(ctx context.Context, userID uint) ([]ProviderView, error) {
	if _, err := s.requireEnabled(ctx); err != nil {
		return nil, err
	}
	var rows []Provider
	if err := s.db.WithContext(ctx).Where("user_id = ?", userID).Order("id ASC").Find(&rows).Error; err != nil {
		return nil, database.Translate(err)
	}
	out := make([]ProviderView, 0, len(rows))
	for _, r := range rows {
		out = append(out, viewOf(r))
	}
	return out, nil
}

// find loads a provider by public id, scoped to its owner in the query. A
// malformed id is "not found".
func (s *Service) find(ctx context.Context, userID uint, publicID string) (*Provider, error) {
	if userID == 0 || !validPublicID(publicID) {
		return nil, ErrNotFound
	}
	var p Provider
	err := s.db.WithContext(ctx).Where("public_id = ? AND user_id = ?", publicID, userID).First(&p).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrNotFound
		}
		return nil, database.Translate(err)
	}
	return &p, nil
}

func (s *Service) decryptKey(p *Provider) (string, error) {
	if s.secrets == nil {
		return "", secretbox.ErrNotConfigured
	}
	key, _, err := s.secrets.DecryptBound(p.APIKeyEncrypted, keyBinding(p.UserID, p.PublicID))
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(key) == "" {
		return "", errors.New("empty api key")
	}
	return key, nil
}

// ---- probe ----------------------------------------------------------------------------

// ProbeInput is the body of an unsaved probe.
type ProbeInput struct {
	Protocol, BaseURL, APIKey string
}

// Probe lists the models of an endpoint without saving anything.
func (s *Service) Probe(ctx context.Context, userID uint, in ProbeInput) ([]CatalogModel, error) {
	st, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	protocol, err := normalizeProviderProtocol(in.Protocol)
	if err != nil {
		return nil, err
	}
	u, err := s.checkURL(in.BaseURL, st)
	if err != nil {
		return nil, err
	}
	key, err := normalizeAPIKey(in.APIKey)
	if err != nil {
		return nil, err
	}
	names, err := s.fetchCatalog(ctx, protocol, u.BaseURL, key)
	if err != nil {
		return nil, err
	}
	return catalogViews(protocol, names), nil
}

// ---- create ---------------------------------------------------------------------------

// CreateInput is a new provider request.
type CreateInput struct {
	Name, Icon, Protocol, BaseURL, APIKey, Source string
	Models                                        []ModelInput
}

// Create validates, probes and stores a provider for userID.
func (s *Service) Create(ctx context.Context, userID uint, in CreateInput) (*Provider, error) {
	st, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	protocol, err := normalizeProviderProtocol(in.Protocol)
	if err != nil {
		return nil, err
	}
	u, err := s.checkURL(in.BaseURL, st)
	if err != nil {
		return nil, err
	}
	key, err := normalizeAPIKey(in.APIKey)
	if err != nil {
		return nil, err
	}
	icon, err := normalizeIcon(in.Icon)
	if err != nil {
		return nil, err
	}
	name, err := normalizeName(in.Name)
	if err != nil {
		return nil, err
	}
	if name == "" {
		name = u.Host
		if utf8.RuneCountInString(name) > maxNameRunes {
			name = string([]rune(name)[:maxNameRunes])
		}
	}
	source := SourceManual
	if strings.TrimSpace(in.Source) == SourceLink {
		source = SourceLink
	}

	if err := s.checkQuota(ctx, userID, u.BaseURL, key, st.MaxPerUser); err != nil {
		return nil, err
	}
	names, err := s.fetchCatalog(ctx, protocol, u.BaseURL, key)
	if err != nil {
		return nil, err
	}
	models, err := selectModels(in.Models, names, protocol)
	if err != nil {
		return nil, err
	}

	now := s.now()
	row := &Provider{
		UserID: userID, Name: name, Icon: icon, Protocol: protocol, BaseURL: u.BaseURL, Host: u.Host,
		KeyHint: keyHint(key), EnabledModels: encodeModels(models), Status: StatusActive, Source: source,
		LastCheckedAt: &now,
	}
	row.CreatedAt, row.UpdatedAt = now, now

	for attempt := 0; attempt < 5; attempt++ {
		id, err := newPublicID()
		if err != nil {
			return nil, err
		}
		row.PublicID = id
		row.ID = 0
		enc, err := s.secrets.EncryptBound(key, keyBinding(userID, id))
		if err != nil {
			return nil, err
		}
		row.APIKeyEncrypted = enc

		unlock := s.lockUser(userID)
		err = s.checkQuota(ctx, userID, u.BaseURL, key, st.MaxPerUser)
		if err == nil {
			err = database.Translate(s.db.WithContext(ctx).Create(row).Error)
		}
		unlock()
		if err == nil {
			return row, nil
		}
		if !database.IsDuplicate(err) {
			return nil, err
		}
	}
	return nil, errors.New("personalprovider: could not allocate a public id")
}

// checkQuota enforces the per-user cap and the (address, key) uniqueness.
func (s *Service) checkQuota(ctx context.Context, userID uint, baseURL, key string, max int) error {
	var count int64
	if err := s.db.WithContext(ctx).Model(&Provider{}).Where("user_id = ?", userID).Count(&count).Error; err != nil {
		return database.Translate(err)
	}
	if count >= int64(max) {
		return ErrLimitReached
	}
	var same []Provider
	if err := s.db.WithContext(ctx).Where("user_id = ? AND base_url = ?", userID, baseURL).Find(&same).Error; err != nil {
		return database.Translate(err)
	}
	for i := range same {
		existing, err := s.decryptKey(&same[i])
		if err != nil {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(existing), []byte(key)) == 1 {
			return ErrDuplicate
		}
	}
	return nil
}

// ---- update ---------------------------------------------------------------------------

// UpdateInput is a partial update; nil fields are left alone.
type UpdateInput struct {
	Name    *string
	Icon    *string
	APIKey  *string
	Models  *[]ModelInput
	Enabled *bool
}

// UpdateResult tells the caller what changed, for the audit trail.
type UpdateResult struct {
	Provider       *Provider
	KeyRotated     bool
	ModelsChanged  bool
	EnabledChanged bool
}

// Update applies a partial update to the caller's provider.
func (s *Service) Update(ctx context.Context, userID uint, publicID string, in UpdateInput) (*UpdateResult, error) {
	st, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, err
	}
	p, err := s.find(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	if in.Enabled != nil && p.Status == StatusSuspended {
		return nil, ErrSuspended
	}

	changes := map[string]any{}
	res := &UpdateResult{Provider: p}
	if in.Name != nil {
		name, err := normalizeName(*in.Name)
		if err != nil {
			return nil, err
		}
		if name == "" {
			return nil, ErrInvalidName
		}
		changes["name"] = name
	}
	if in.Icon != nil {
		icon, err := normalizeIcon(*in.Icon)
		if err != nil {
			return nil, err
		}
		changes["icon"] = icon
	}
	var newKey string
	if in.APIKey != nil {
		if newKey, err = normalizeAPIKey(*in.APIKey); err != nil {
			return nil, err
		}
	}

	if in.APIKey != nil || in.Models != nil {
		if _, err := s.checkURL(p.BaseURL, st); err != nil {
			return nil, err
		}
		key := newKey
		if in.APIKey == nil {
			if key, err = s.decryptKey(p); err != nil {
				return nil, ErrModelUnavailable.WithCause(err)
			}
		}
		names, err := s.fetchCatalog(ctx, p.Protocol, p.BaseURL, key)
		if err != nil {
			return nil, err
		}
		var models []EnabledModel
		if in.Models != nil {
			models, err = selectModels(*in.Models, names, p.Protocol)
		} else {
			models, err = revalidateSaved(p.Models(), names)
		}
		if err != nil {
			return nil, err
		}
		encoded := encodeModels(models)
		res.ModelsChanged = in.Models != nil && encoded != encodeModels(p.Models())
		changes["enabled_models"] = encoded
		now := s.now()
		changes["last_checked_at"] = now
		changes["last_error"] = ""
		if in.APIKey != nil {
			enc, err := s.secrets.EncryptBound(key, keyBinding(p.UserID, p.PublicID))
			if err != nil {
				return nil, err
			}
			changes["api_key_encrypted"] = enc
			changes["key_hint"] = keyHint(key)
			res.KeyRotated = true
		}
	}
	if in.Enabled != nil {
		next := StatusDisabled
		if *in.Enabled {
			next = StatusActive
		}
		res.EnabledChanged = next != p.Status
		changes["status"] = next
	}
	if len(changes) == 0 {
		return res, nil
	}
	changes["updated_at"] = s.now()
	tx := s.db.WithContext(ctx).Model(&Provider{}).Where("id = ? AND user_id = ?", p.ID, userID).Updates(changes)
	if tx.Error != nil {
		return nil, database.Translate(tx.Error)
	}
	if tx.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	fresh, err := s.find(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	res.Provider = fresh
	return res, nil
}

// ---- delete ---------------------------------------------------------------------------

// Delete hard-deletes the caller's provider. It works with the feature off.
func (s *Service) Delete(ctx context.Context, userID uint, publicID string) (*Provider, error) {
	p, err := s.find(ctx, userID, publicID)
	if err != nil {
		return nil, err
	}
	tx := s.db.WithContext(ctx).Where("id = ? AND user_id = ?", p.ID, userID).Delete(&Provider{})
	if tx.Error != nil {
		return nil, database.Translate(tx.Error)
	}
	if tx.RowsAffected == 0 {
		return nil, ErrNotFound
	}
	return p, nil
}

// ---- models of a saved provider ---------------------------------------------------------

// Models fetches the live catalog of a saved provider and records the outcome
// on the provider (a failure to record never fails the call).
func (s *Service) Models(ctx context.Context, userID uint, publicID string) ([]CatalogModel, *Provider, error) {
	st, err := s.requireEnabled(ctx)
	if err != nil {
		return nil, nil, err
	}
	p, err := s.find(ctx, userID, publicID)
	if err != nil {
		return nil, nil, err
	}
	names, err := s.checkAndFetch(ctx, p, st)
	s.recordCheck(ctx, p, err)
	if err != nil {
		return nil, p, err
	}
	return catalogViews(p.Protocol, names), p, nil
}

func (s *Service) checkAndFetch(ctx context.Context, p *Provider, st Settings) ([]string, error) {
	if _, err := s.checkURL(p.BaseURL, st); err != nil {
		return nil, err
	}
	key, err := s.decryptKey(p)
	if err != nil {
		return nil, ErrModelUnavailable.WithCause(err)
	}
	return s.fetchCatalog(ctx, p.Protocol, p.BaseURL, key)
}

func (s *Service) recordCheck(ctx context.Context, p *Provider, checkErr error) {
	code := ""
	if checkErr != nil {
		code = "internal.error"
		if te, ok := asTyped(checkErr); ok {
			code = te
		}
		if len(code) > maxLastErrorLen {
			code = code[:maxLastErrorLen]
		}
	}
	now := s.now()
	err := s.db.WithContext(ctx).Model(&Provider{}).Where("id = ? AND user_id = ?", p.ID, p.UserID).
		Updates(map[string]any{"last_error": code, "last_checked_at": now, "updated_at": now}).Error
	if err != nil {
		s.log.Warn("personal_provider_record_check_failed", logx.Err(err))
	}
}
