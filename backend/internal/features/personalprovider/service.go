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
