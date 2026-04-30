package errorlog

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// filter is the parsed administrator query.
type filter struct {
	RequestID   string
	Keyword     string
	ErrorCode   string
	StatusClass string
	UserID      uint
	From, To    *time.Time
	Ascending   bool
}

// UserInfo is the identity data shown next to an entry.
type UserInfo struct {
	Username    string
	DisplayName string
}

// UserDirectory resolves user IDs to names. Missing users are simply absent
// from the result.
type UserDirectory interface {
	LookupUsers(ctx context.Context, ids []uint) (map[uint]UserInfo, error)
}

// dbUsers is the default directory: a narrow read of the identity users table.
type dbUsers struct{ db *gorm.DB }

func (d dbUsers) LookupUsers(ctx context.Context, ids []uint) (map[uint]UserInfo, error) {
	var rows []struct {
		ID          uint
		Username    string
		DisplayName string
	}
	err := d.db.WithContext(ctx).Table("identity_users").
		Select("id, username, display_name").
		Where("id IN ? AND deleted_at IS NULL", ids).
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	out := make(map[uint]UserInfo, len(rows))
	for _, r := range rows {
		out[r.ID] = UserInfo{Username: r.Username, DisplayName: r.DisplayName}
	}
	return out, nil
}

// service answers administrator queries.
type service struct {
	db    *gorm.DB
	users UserDirectory
}

// escapeLike makes s safe inside a LIKE pattern that uses backslash as escape.
func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(s)
}

func (f filter) apply(q *gorm.DB) *gorm.DB {
	if f.RequestID != "" {
		q = q.Where("request_id = ?", f.RequestID)
	}
	if kw := f.Keyword; kw != "" {
		low := strings.ToLower(kw)
		pat := "%" + escapeLike(low) + "%"
		cond := "request_id = ? OR trace_id = ?"
		args := []any{kw, low}
		for _, col := range []string{"route", "path", "error_code", "cause", "detail"} {
			cond += fmt.Sprintf(" OR LOWER(%s) LIKE ? ESCAPE '\\'", col)
			args = append(args, pat)
		}
		q = q.Where("("+cond+")", args...)
	}
	if f.ErrorCode != "" {
		q = q.Where("error_code = ?", f.ErrorCode)
	}
	switch f.StatusClass {
	case "5xx":
		q = q.Where("status_code >= ?", 500)
	case "stream":
		q = q.Where("status_code < ?", 400)
	}
	if f.UserID != 0 {
		q = q.Where("user_id = ?", f.UserID)
	}
	if f.From != nil {
		q = q.Where("created_at >= ?", f.From.UTC())
	}
	if f.To != nil {
		q = q.Where("created_at <= ?", f.To.UTC())
	}
	return q
}

// list returns one page of entries plus the total matching the filter.
func (s *service) list(ctx context.Context, f filter, p pagination.Page) ([]entryView, int64, error) {
	base := f.apply(s.db.WithContext(ctx).Model(&Entry{}))
	var total int64
	if err := base.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	order := "created_at DESC, id DESC"
	if f.Ascending {
		order = "created_at ASC, id ASC"
	}
	var rows []Entry
	if err := f.apply(s.db.WithContext(ctx).Model(&Entry{})).
		Order(order).Scopes(pagination.Scope(p)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	names := s.resolveUsers(ctx, rows)
	out := make([]entryView, 0, len(rows))
	for _, e := range rows {
		v := entryView{
			ID: e.ID, RequestID: e.RequestID, TraceID: e.TraceID, UserID: e.UserID,
			Method: e.Method, Route: e.Route, Path: e.Path, StatusCode: e.StatusCode,
			ErrorCode: e.ErrorCode, Cause: e.Cause, Detail: e.Detail, LatencyMS: e.LatencyMS,
			ClientIP: e.ClientIP, UserAgent: e.UserAgent, CreatedAt: e.CreatedAt,
		}
		if info, ok := names[e.UserID]; ok {
			v.UserUsername, v.UserDisplayName = info.Username, info.DisplayName
			v.UserLabel = labelFor(e.UserID, info)
		}
		out = append(out, v)
	}
	return out, total, nil
}

func labelFor(id uint, u UserInfo) string {
	if d := strings.TrimSpace(u.DisplayName); d != "" {
		return u.DisplayName
	}
	if n := strings.TrimSpace(u.Username); n != "" {
		return u.Username
	}
	return strconv.FormatUint(uint64(id), 10)
}

// resolveUsers looks up every distinct non-zero user in the page. A failed
// lookup degrades to empty names; the rows are still returned.
func (s *service) resolveUsers(ctx context.Context, rows []Entry) map[uint]UserInfo {
	if s.users == nil {
		return nil
	}
	seen := map[uint]bool{}
	var ids []uint
	for _, e := range rows {
		if e.UserID != 0 && !seen[e.UserID] {
			seen[e.UserID] = true
			ids = append(ids, e.UserID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	found, err := s.users.LookupUsers(ctx, ids)
	if err != nil {
		return nil
	}
	return found
}
