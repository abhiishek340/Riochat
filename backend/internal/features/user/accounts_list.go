package user

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// ListFilter narrows a user listing. All members are optional and combine with
// AND.
type ListFilter struct {
	// Query is a case-insensitive substring over username, display name, email
	// and public id. %, _ and \ match literally.
	Query string
	// ProviderSlug keeps users with a linked identity of the provider with
	// exactly this slug.
	ProviderSlug string
	// SubscriptionStatus is "free", "active" or any other subscription status.
	SubscriptionStatus string
}

// likeEscaper escapes the LIKE wildcards and the escape character itself.
var likeEscaper = strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)

const currentPaidSubscriptions = `SELECT s.user_id FROM billing_subscriptions s
	JOIN billing_plans pl ON pl.id = s.plan_id AND pl.deleted_at IS NULL
	WHERE s.deleted_at IS NULL AND s.status = 'active' AND s.current_period_start_at <= ?
	  AND (s.current_period_end_at IS NULL OR s.current_period_end_at > ?) AND pl.code <> 'free'`

func (a *Accounts) filtered(ctx context.Context, f ListFilter) *gorm.DB {
	q := a.conn(ctx).Model(&Account{})
	if text := strings.ToLower(strings.TrimSpace(f.Query)); text != "" {
		pat := "%" + likeEscaper.Replace(text) + "%"
		q = q.Where(`(LOWER(username) LIKE ? ESCAPE '\' OR LOWER(display_name) LIKE ? ESCAPE '\'
			OR LOWER(email) LIKE ? ESCAPE '\' OR LOWER(public_id) LIKE ? ESCAPE '\')`, pat, pat, pat, pat)
	}
	if slug := strings.TrimSpace(f.ProviderSlug); slug != "" {
		q = q.Where(`id IN (SELECT l.user_id FROM identity_user_links l
			JOIN identity_providers p ON p.id = l.provider_id AND p.deleted_at IS NULL
			WHERE p.slug = ? AND l.deleted_at IS NULL)`, slug)
	}
	switch status := strings.TrimSpace(f.SubscriptionStatus); status {
	case "":
	case "free":
		now := a.now()
		q = q.Where("id NOT IN ("+currentPaidSubscriptions+")", now, now)
	case "active":
		now := a.now()
		q = q.Where("id IN ("+currentPaidSubscriptions+")", now, now)
	default:
		q = q.Where("id IN (SELECT user_id FROM billing_subscriptions WHERE status = ? AND deleted_at IS NULL)", status)
	}
	return q
}

// List returns one page of users, newest first, and the number of matches
// before pagination.
func (a *Accounts) List(ctx context.Context, f ListFilter, p pagination.Page) ([]Account, int64, error) {
	var total int64
	if err := a.filtered(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Account
	if err := a.filtered(ctx, f).Order("id DESC").Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// IdentityLink is a bound third-party identity with its provider summary.
type IdentityLink struct {
	LinkID           uint
	UserID           uint
	ProviderID       uint
	ProviderPublicID string
	ProviderType     string
	ProviderName     string
	ProviderSlug     string
	ProviderLogoURL  string
}

// IdentitiesForUsers returns the linked identities of the given users grouped by
// user id. Empty input gives an empty result.
func (a *Accounts) IdentitiesForUsers(ctx context.Context, userIDs []uint) (map[uint][]IdentityLink, error) {
	out := map[uint][]IdentityLink{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []IdentityLink
	err := a.conn(ctx).Raw(`SELECT l.id AS link_id, l.user_id AS user_id, p.id AS provider_id,
			p.public_id AS provider_public_id, p.type AS provider_type, p.name AS provider_name,
			p.slug AS provider_slug, p.logo_url AS provider_logo_url
		FROM identity_user_links l
		JOIN identity_providers p ON p.id = l.provider_id AND p.deleted_at IS NULL
		WHERE l.deleted_at IS NULL AND l.user_id IN ?
		ORDER BY p.sort_order ASC, p.id ASC, l.id ASC`, userIDs).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = append(out[r.UserID], r)
	}
	return out, nil
}

// AuthEventFilter narrows the authentication event listing.
type AuthEventFilter struct {
	UserID    uint // > 0 filters
	EventType string
	Result    string
}

// ListAuthEvents returns one page of events, newest first, with the total
// number of matches.
func (a *Accounts) ListAuthEvents(ctx context.Context, f AuthEventFilter, p pagination.Page) ([]AuthEvent, int64, error) {
	build := func() *gorm.DB {
		q := a.conn(ctx).Model(&AuthEvent{})
		if f.UserID > 0 {
			q = q.Where("user_id = ?", f.UserID)
		}
		if t := strings.TrimSpace(f.EventType); t != "" {
			q = q.Where("event_type = ?", t)
		}
		if r := strings.TrimSpace(f.Result); r != "" {
			q = q.Where("result = ?", r)
		}
		return q
	}
	var total int64
	if err := build().Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []AuthEvent
	if err := build().Order("occurred_at DESC, id DESC").Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}
