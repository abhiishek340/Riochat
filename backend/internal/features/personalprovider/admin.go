package personalprovider

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// maxBulkIDs bounds the id lists of the bulk administrator operations.
const maxBulkIDs = 500

// AdminFilter narrows the administrator listing.
type AdminFilter struct {
	Query       string
	Status      string
	Host        string
	OwnerUserID *uint
}

// AdminView is one row of the administrator listing. It carries no key.
type AdminView struct {
	ID               string     `json:"id"`
	OwnerUserID      uint       `json:"ownerUserID"`
	OwnerLabel       string     `json:"ownerLabel"`
	OwnerPublicID    string     `json:"ownerPublicID"`
	OwnerUsername    string     `json:"ownerUsername"`
	OwnerDisplayName string     `json:"ownerDisplayName"`
	OwnerEmail       string     `json:"ownerEmail"`
	OwnerAvatarURL   string     `json:"ownerAvatarURL"`
	Name             string     `json:"name"`
	Icon             string     `json:"icon"`
	Protocol         string     `json:"protocol"`
	BaseURL          string     `json:"baseURL"`
	Host             string     `json:"host"`
	KeyHint          string     `json:"keyHint"`
	ModelCount       int        `json:"modelCount"`
	Status           string     `json:"status"`
	Source           string     `json:"source"`
	LastError        string     `json:"lastError"`
	LastCheckedAt    *time.Time `json:"lastCheckedAt"`
	CreatedAt        time.Time  `json:"createdAt"`
}

// AdminList returns providers of all users, newest first.
func (s *Service) AdminList(ctx context.Context, f AdminFilter, p pagination.Page) ([]AdminView, int64, error) {
	q := s.db.WithContext(ctx).Model(&Provider{})
	if text := strings.TrimSpace(f.Query); text != "" {
		pat := likePattern(text)
		var ownerIDs []uint
		if ids, err := s.owners.SearchIDs(ctx, text); err != nil {
			s.log.Warn("personal_provider_owner_search_failed", logx.Err(err))
		} else {
			ownerIDs = ids
		}
		if len(ownerIDs) > 0 {
			q = q.Where("(LOWER(name) LIKE ? ESCAPE '\\' OR LOWER(host) LIKE ? ESCAPE '\\' OR user_id IN ?)", pat, pat, ownerIDs)
		} else {
			q = q.Where("(LOWER(name) LIKE ? ESCAPE '\\' OR LOWER(host) LIKE ? ESCAPE '\\')", pat, pat)
		}
	}
	if st := strings.ToLower(strings.TrimSpace(f.Status)); st != "" {
		q = q.Where("status = ?", st)
	}
	if h := normalizeHostFilter(f.Host); h != "" {
		q = q.Where("host = ?", h)
	}
	if f.OwnerUserID != nil {
		q = q.Where("user_id = ?", *f.OwnerUserID)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, database.Translate(err)
	}
	var rows []Provider
	if err := q.Order("id DESC").Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, database.Translate(err)
	}

	owners := map[uint]Owner{}
	if len(rows) > 0 {
		seen := map[uint]bool{}
		var ids []uint
		for _, r := range rows {
			if !seen[r.UserID] {
				seen[r.UserID] = true
				ids = append(ids, r.UserID)
			}
		}
		if got, err := s.owners.Lookup(ctx, ids); err != nil {
			s.log.Warn("personal_provider_owner_lookup_failed", logx.Err(err))
		} else if got != nil {
			owners = got
		}
	}
	out := make([]AdminView, 0, len(rows))
	for _, r := range rows {
		o := owners[r.UserID]
		label := strings.TrimSpace(o.DisplayName)
		if label == "" {
			label = o.Username
		}
		v := AdminView{
			ID: r.PublicID, OwnerUserID: r.UserID, OwnerLabel: label, OwnerPublicID: o.PublicID,
			OwnerUsername: o.Username, OwnerDisplayName: o.DisplayName, OwnerEmail: o.Email, OwnerAvatarURL: o.AvatarURL,
			Name: r.Name, Icon: r.Icon, Protocol: r.Protocol, BaseURL: r.BaseURL, Host: r.Host, KeyHint: r.KeyHint,
			ModelCount: len(r.Models()), Status: r.Status, Source: r.Source, LastError: r.LastError,
			CreatedAt: r.CreatedAt.UTC(),
		}
		if r.LastCheckedAt != nil {
			t := r.LastCheckedAt.UTC()
			v.LastCheckedAt = &t
		}
		out = append(out, v)
	}
	return out, total, nil
}

func normalizeHostFilter(h string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(h), "."))
}

// normalizeIDs trims, drops blanks and removes duplicates, keeping the first
// occurrence. More than maxBulkIDs distinct ids is invalid input.
func normalizeIDs(in []string) ([]string, error) {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	if len(out) > maxBulkIDs {
		return nil, apperr.ErrInvalidRequest
	}
	return out, nil
}

// AdminSetSuspended suspends (or re-activates) the providers with the given
// public ids, whatever their previous status, and returns the affected count
// together with the normalized id list.
func (s *Service) AdminSetSuspended(ctx context.Context, ids []string, suspended bool) (int64, []string, error) {
	ids, err := normalizeIDs(ids)
	if err != nil {
		return 0, nil, err
	}
	if len(ids) == 0 {
		return 0, ids, nil
	}
	status := StatusActive
	if suspended {
		status = StatusSuspended
	}
	tx := s.db.WithContext(ctx).Model(&Provider{}).Where("public_id IN ?", ids).
		Updates(map[string]any{"status": status, "updated_at": s.now()})
	if tx.Error != nil {
		return 0, ids, database.Translate(tx.Error)
	}
	return tx.RowsAffected, ids, nil
}

// AdminSuspendHost suspends every provider stored with exactly this host. The
// returned host is the normalized spelling.
func (s *Service) AdminSuspendHost(ctx context.Context, host string) (int64, string, error) {
	host = normalizeHostFilter(host)
	if host == "" {
		return 0, "", ErrInvalidBaseURL
	}
	tx := s.db.WithContext(ctx).Model(&Provider{}).Where("host = ?", host).
		Updates(map[string]any{"status": StatusSuspended, "updated_at": s.now()})
	if tx.Error != nil {
		return 0, host, database.Translate(tx.Error)
	}
	return tx.RowsAffected, host, nil
}

// AdminDelete hard-deletes the providers with the given public ids.
func (s *Service) AdminDelete(ctx context.Context, ids []string) (int64, []string, error) {
	ids, err := normalizeIDs(ids)
	if err != nil {
		return 0, nil, err
	}
	if len(ids) == 0 {
		return 0, ids, nil
	}
	tx := s.db.WithContext(ctx).Where("public_id IN ?", ids).Delete(&Provider{})
	if tx.Error != nil {
		return 0, ids, database.Translate(tx.Error)
	}
	return tx.RowsAffected, ids, nil
}

// PurgeUserTx hard-deletes every provider of a user inside tx. The account
// deletion flow calls it.
func PurgeUserTx(tx *gorm.DB, userID uint) error {
	return database.Translate(tx.Where("user_id = ?", userID).Delete(&Provider{}).Error)
}
