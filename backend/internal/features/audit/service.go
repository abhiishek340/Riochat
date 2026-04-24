package audit

import (
	"context"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Sort keys accepted by the list endpoint.
const (
	SortIDAsc       = "id_asc"
	SortCreatedAsc  = "created_asc"
	SortCreatedDesc = "created_desc"
)

// Filter selects and orders audit records.
type Filter struct {
	Query       string
	Resource    string
	Action      string
	ActorUserID uint
	CreatedFrom *time.Time
	CreatedTo   *time.Time
	Sort        string
}

type service struct {
	db *gorm.DB
}

// orderClause maps a sort key to its ORDER BY; unknown keys use the default.
func orderClause(sort string) string {
	switch strings.TrimSpace(sort) {
	case SortIDAsc:
		return "id ASC"
	case SortCreatedAsc:
		return "created_at ASC, id ASC"
	case SortCreatedDesc:
		return "created_at DESC, id DESC"
	default:
		return "id DESC"
	}
}

func (s *service) filtered(ctx context.Context, f Filter) *gorm.DB {
	q := s.db.WithContext(ctx).Model(&Log{})
	if kw := strings.TrimSpace(f.Query); kw != "" {
		like := "%" + kw + "%"
		q = q.Where("(request_id LIKE ? OR action LIKE ? OR resource LIKE ? OR resource_id LIKE ? OR ip LIKE ? OR user_agent LIKE ? OR detail_json LIKE ?)",
			like, like, like, like, like, like, like)
	}
	if v := strings.TrimSpace(f.Resource); v != "" {
		q = q.Where("resource = ?", v)
	}
	if v := strings.TrimSpace(f.Action); v != "" {
		q = q.Where("action = ?", v)
	}
	if f.ActorUserID > 0 {
		q = q.Where("actor_user_id = ?", f.ActorUserID)
	}
	if f.CreatedFrom != nil {
		q = q.Where("created_at >= ?", f.CreatedFrom.UTC())
	}
	if f.CreatedTo != nil {
		q = q.Where("created_at <= ?", f.CreatedTo.UTC())
	}
	return q
}

// list returns one page of records and the total matching the filter.
func (s *service) list(ctx context.Context, f Filter, p pagination.Page) ([]Log, int64, error) {
	var total int64
	if err := s.filtered(ctx, f).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Log
	err := s.filtered(ctx, f).Order(orderClause(f.Sort)).Scopes(pagination.Scope(p)).Find(&rows).Error
	if err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// views converts rows to response elements, resolving each distinct actor once.
// A failed lookup leaves that actor's name fields empty and never fails the
// request.
func views(ctx context.Context, rows []Log) []LogView {
	res := currentResolver()
	cache := map[uint]Actor{}
	known := map[uint]bool{}
	out := make([]LogView, 0, len(rows))
	for _, r := range rows {
		v := LogView{
			ID: r.ID, RequestID: r.RequestID, ActorUserID: r.ActorUserID,
			Action: r.Action, Resource: r.Resource, ResourceID: r.ResourceID,
			IP: r.IP, UserAgent: r.UserAgent, DetailJSON: r.DetailJSON,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		}
		if r.ActorUserID != 0 && res != nil {
			a, seen := cache[r.ActorUserID]
			if !seen {
				actor, found, err := safeResolve(ctx, res, r.ActorUserID)
				if err == nil && found {
					a = actor
					known[r.ActorUserID] = true
				}
				cache[r.ActorUserID] = a
			}
			if known[r.ActorUserID] {
				v.ActorUsername = a.Username
				v.ActorDisplayName = a.DisplayName
				v.ActorLabel = a.label(r.ActorUserID)
			}
		}
		out = append(out, v)
	}
	return out
}

// safeResolve calls the resolver and turns a panic into a lookup failure, so
// one bad lookup can never fail the listing.
func safeResolve(ctx context.Context, res ActorResolver, id uint) (a Actor, found bool, err error) {
	defer func() {
		if p := recover(); p != nil {
			a, found, err = Actor{}, false, fmt.Errorf("actor lookup panicked: %v", p)
		}
	}()
	return res.ResolveActor(ctx, id)
}
