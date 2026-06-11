package personalprovider

import (
	"context"
	"errors"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/secretbox"

	"gorm.io/gorm"
)

// ActiveModel is one catalog entry contributed by a user's providers. The
// channel area turns it into a public model entry (source "personal", no
// pricing, capabilities inferred from UpstreamName).
type ActiveModel struct {
	Reference        string // personal:<providerPublicID>/<upstream model>
	UpstreamName     string
	Protocols        []string
	ProviderProtocol string
	ProviderName     string
	ProviderIcon     string
	ProviderHost     string
}

// ActiveModels lists the enabled models of the caller's active providers whose
// host is not deny-listed. With the feature off, or for user 0, it is empty.
func (s *Service) ActiveModels(ctx context.Context, userID uint) ([]ActiveModel, error) {
	if userID == 0 {
		return nil, nil
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Enabled {
		return nil, nil
	}
	var rows []Provider
	err = s.db.WithContext(ctx).Where("user_id = ? AND status = ?", userID, StatusActive).Order("id ASC").Find(&rows).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	deny := parseDenyList(st.BlockedHosts)
	var out []ActiveModel
	for _, r := range rows {
		if deny.matches(r.Host) {
			continue
		}
		for _, m := range r.Models() {
			out = append(out, ActiveModel{
				Reference: FormatReference(r.PublicID, m.Name), UpstreamName: m.Name, Protocols: m.Protocols,
				ProviderProtocol: r.Protocol, ProviderName: r.Name, ProviderIcon: r.Icon, ProviderHost: r.Host,
			})
		}
	}
	return out, nil
}

// ResolveRequest asks for the route of a personal model reference.
type ResolveRequest struct {
	UserID    uint
	Reference string
	// Task is one of the Task* constants; empty means chat.
	Task string
	// ExcludedRoutes is true when the caller already failed on a route and asks
	// for another one. A personal model has exactly one route.
	ExcludedRoutes bool
}

// Route is the single route of a personal model. The key is held in memory
// only: it is never serialized or printed.
type Route struct {
	Reference        string
	ProviderPublicID string
	UpstreamModel    string
	// Protocol is the model protocol chosen for the task.
	Protocol         string
	Protocols        []string
	ProviderProtocol string
	BaseURL          string
	Host             string
	ProviderName     string
	ProviderIcon     string
	// APIKey is the plaintext key.
	APIKey string `json:"-"`
	// UserSupplied marks the endpoint as user-chosen: outbound address
	// protection is mandatory and the circuit breaker is skipped.
	UserSupplied bool
}

// String never reveals the key.
func (r Route) String() string { return "personal route " + r.Reference }

// GoString never reveals the key.
func (r Route) GoString() string { return r.String() }

// Resolve re-checks everything on every call and returns the route, or
// ErrModelUnavailable. It never falls back to a platform upstream.
func (s *Service) Resolve(ctx context.Context, req ResolveRequest) (*Route, error) {
	if req.ExcludedRoutes {
		return nil, ErrAllRoutesUnavailable
	}
	publicID, model, ok := ParseReference(req.Reference)
	if !ok || req.UserID == 0 {
		return nil, ErrModelUnavailable
	}
	st, err := s.loadSettings(ctx)
	if err != nil {
		return nil, err
	}
	if !st.Enabled {
		return nil, ErrModelUnavailable
	}
	p, err := s.find(ctx, req.UserID, publicID)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return nil, ErrModelUnavailable
		}
		return nil, err
	}
	if p.Status != StatusActive {
		return nil, ErrModelUnavailable
	}
	var enabled *EnabledModel
	for _, m := range p.Models() {
		if m.Name == model {
			m := m
			enabled = &m
			break
		}
	}
	if enabled == nil {
		return nil, ErrModelUnavailable
	}
	if _, err := s.checkURL(p.BaseURL, st); err != nil {
		return nil, ErrModelUnavailable
	}
	key, err := s.decryptKey(p)
	if err != nil {
		s.log.Warn("personal_provider_key_unusable", logx.F("provider", p.PublicID))
		return nil, ErrModelUnavailable
	}
	proto, ok := ProtocolForTask(enabled.Protocols, req.Task)
	if !ok {
		return nil, ErrNoRouteForTask
	}
	return &Route{
		Reference: req.Reference, ProviderPublicID: p.PublicID, UpstreamModel: model, Protocol: proto,
		Protocols: enabled.Protocols, ProviderProtocol: p.Protocol, BaseURL: p.BaseURL, Host: p.Host,
		ProviderName: p.Name, ProviderIcon: p.Icon, APIKey: key, UserSupplied: true,
	}, nil
}

// reencryptBatch is the number of rows rewritten per query.
const reencryptBatch = 200

// ReencryptKeys moves every stored API key to the active key of the ring,
// keeping each row's binding (owner id + public id). The secret-rotation job
// calls it; it returns how many rows changed. A row that no key can open is
// skipped so that one damaged record cannot block the rotation of the others;
// a missing keyring or a database failure still aborts.
func ReencryptKeys(ctx context.Context, db *gorm.DB, ring *secretbox.KeyRing) (int, error) {
	changed, _, err := reencryptKeys(ctx, db, ring)
	return changed, err
}

func reencryptKeys(ctx context.Context, db *gorm.DB, ring *secretbox.KeyRing) (changed, skipped int, err error) {
	var after uint
	for {
		var batch []Provider
		err := db.WithContext(ctx).Where("id > ? AND api_key_encrypted <> ''", after).
			Order("id ASC").Limit(reencryptBatch).Find(&batch).Error
		if err != nil {
			return changed, skipped, database.Translate(err)
		}
		if len(batch) == 0 {
			return changed, skipped, nil
		}
		for _, p := range batch {
			after = p.ID
			next, did, err := ring.ReencryptBound(p.APIKeyEncrypted, keyBinding(p.UserID, p.PublicID))
			if err != nil {
				if errors.Is(err, secretbox.ErrNotConfigured) {
					return changed, skipped, err
				}
				skipped++
				continue
			}
			if !did {
				continue
			}
			if err := db.WithContext(ctx).Model(&Provider{}).Where("id = ?", p.ID).
				Update("api_key_encrypted", next).Error; err != nil {
				return changed, skipped, database.Translate(err)
			}
			changed++
		}
	}
}

// ReencryptKeys is the method form of the package function.
func (s *Service) ReencryptKeys(ctx context.Context) (int, error) {
	changed, skipped, err := reencryptKeys(ctx, s.db, s.secrets)
	if skipped > 0 {
		s.log.Warn("personal_provider_reencrypt_skipped_rows", logx.F("count", skipped))
	}
	return changed, err
}
