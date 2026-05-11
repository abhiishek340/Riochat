package announcement

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// service holds the business rules. It owns no state besides its handles.
type service struct {
	db    *gorm.DB
	clock clock.Clock
	repo  repository
}

func newService(db *gorm.DB, clk clock.Clock) *service {
	return &service{db: db, clock: clk}
}

// createInput is a validated-by-the-service creation request.
type createInput struct {
	Title           string
	ContentMarkdown string
	Status          string
	Type            string
	Pinned          bool
	Priority        int
	StartsAt        *time.Time
	ExpiresAt       *time.Time
}

// timePatch is a three-state optional timestamp: untouched, cleared or set.
type timePatch struct {
	Set   bool
	Value *time.Time
}

// updateInput is a partial update. A nil pointer or an unset patch leaves the
// stored value alone.
type updateInput struct {
	Title           *string
	ContentMarkdown *string
	Status          *string
	Type            *string
	Pinned          *bool
	Priority        *int
	StartsAt        timePatch
	ExpiresAt       timePatch
}

func (in updateInput) touchesAnything() bool {
	return in.Title != nil || in.ContentMarkdown != nil || in.Status != nil || in.Type != nil ||
		in.Pinned != nil || in.Priority != nil || in.StartsAt.Set || in.ExpiresAt.Set
}

func invalid(reason string) error {
	return ErrInvalidAnnouncement.WithCause(errors.New(reason))
}

func cleanText(s string, name string, limit int) (string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", invalid(name + " is blank")
	}
	if utf8.RuneCountInString(s) > limit {
		return "", invalid(name + " is too long")
	}
	return s, nil
}

func cleanStatus(s string) (string, error) {
	switch s = strings.TrimSpace(s); s {
	case "", StatusActive:
		return StatusActive, nil
	case StatusInactive:
		return StatusInactive, nil
	}
	return "", invalid("unknown status")
}

func cleanType(s string) (string, error) {
	switch s = strings.TrimSpace(s); s {
	case "":
		return TypeGeneral, nil
	case TypeCritical, TypeWarning, TypeInfo, TypeNormal, TypeGeneral:
		return s, nil
	}
	return "", invalid("unknown type")
}

func checkWindow(start, end *time.Time) error {
	if start != nil && end != nil && !end.After(*start) {
		return invalid("expiry must be after start")
	}
	return nil
}

// Create stores a new announcement authored by adminID.
func (s *service) Create(ctx context.Context, adminID uint, in createInput) (*Announcement, error) {
	title, err := cleanText(in.Title, "title", maxTitleChars)
	if err != nil {
		return nil, err
	}
	content, err := cleanText(in.ContentMarkdown, "content", maxContentChars)
	if err != nil {
		return nil, err
	}
	status, err := cleanStatus(in.Status)
	if err != nil {
		return nil, err
	}
	typ, err := cleanType(in.Type)
	if err != nil {
		return nil, err
	}
	start, end := timePtr(in.StartsAt), timePtr(in.ExpiresAt)
	if err := checkWindow(start, end); err != nil {
		return nil, err
	}
	now := s.clock.Now().UTC().Truncate(versionPrecision)
	row := &Announcement{
		Title: title, ContentMarkdown: content, Status: status, Type: typ,
		Pinned: in.Pinned, Priority: in.Priority, StartsAt: start, ExpiresAt: end,
		CreatedByUserID: adminID,
	}
	row.CreatedAt, row.UpdatedAt = now, now
	if err := s.repo.create(ctx, s.db, row); err != nil {
		return nil, err
	}
	return row, nil
}

// Update applies a partial edit under a row lock so the window check and the
// write see one consistent stored state. Any edit that carries at least one
// field produces a new version.
func (s *service) Update(ctx context.Context, id uint, in updateInput) (*Announcement, error) {
	var out *Announcement
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := s.repo.lockForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		cur, err := s.repo.find(ctx, tx, id)
		if err != nil {
			return err
		}
		if !in.touchesAnything() {
			out = cur
			return nil
		}
		cols := map[string]any{}
		if in.Title != nil {
			v, err := cleanText(*in.Title, "title", maxTitleChars)
			if err != nil {
				return err
			}
			cols["title"] = v
		}
		if in.ContentMarkdown != nil {
			v, err := cleanText(*in.ContentMarkdown, "content", maxContentChars)
			if err != nil {
				return err
			}
			cols["content_markdown"] = v
		}
		if in.Status != nil {
			v, err := cleanStatus(*in.Status)
			if err != nil {
				return err
			}
			cols["status"] = v
		}
		if in.Type != nil {
			v, err := cleanType(*in.Type)
			if err != nil {
				return err
			}
			cols["type"] = v
		}
		if in.Pinned != nil {
			cols["pinned"] = *in.Pinned
		}
		if in.Priority != nil {
			cols["priority"] = *in.Priority
		}
		start, end := cur.StartsAt, cur.ExpiresAt
		if in.StartsAt.Set {
			start = timePtr(in.StartsAt.Value)
			cols["starts_at"] = timeColumn(start)
		}
		if in.ExpiresAt.Set {
			end = timePtr(in.ExpiresAt.Value)
			cols["expires_at"] = timeColumn(end)
		}
		if err := checkWindow(start, end); err != nil {
			return err
		}
		// The new version must differ from the old one even when the clock has
		// not moved (or moved by less than the storage precision).
		version := s.clock.Now().UTC().Truncate(versionPrecision)
		if prev := cur.UpdatedAt.UTC().Truncate(versionPrecision); !version.After(prev) {
			version = prev.Add(versionPrecision)
		}
		cols["updated_at"] = version
		if err := s.repo.update(ctx, tx, id, cols); err != nil {
			return err
		}
		out, err = s.repo.find(ctx, tx, id)
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// timeColumn converts an optional time to a value GORM writes as NULL or a
// timestamp inside an update map.
func timeColumn(t *time.Time) any {
	if t == nil {
		return nil
	}
	return *t
}

// Delete soft-deletes one announcement.
func (s *service) Delete(ctx context.Context, id uint) error {
	ok, err := s.repo.softDelete(ctx, s.db, id)
	if err != nil {
		return err
	}
	if !ok {
		return ErrNotFound
	}
	return nil
}

// AdminList returns one page of announcements plus the total match count.
func (s *service) AdminList(ctx context.Context, f adminFilter, p pagination.Page) ([]Announcement, int64, error) {
	f.Status = strings.TrimSpace(f.Status)
	f.Type = strings.TrimSpace(f.Type)
	return s.repo.adminList(ctx, s.db, f, p)
}

// listItem is an announcement as one user sees it.
type listItem struct {
	Announcement
	ClosedAt *time.Time
}

// ListForUser returns the announcements in effect now for the user, ordered with
// closed ones last.
func (s *service) ListForUser(ctx context.Context, userID uint, includeDismissed bool) ([]listItem, error) {
	now := s.clock.Now()
	rows, err := s.repo.listActive(ctx, s.db)
	if err != nil {
		return nil, err
	}
	live := make([]Announcement, 0, len(rows))
	ids := make([]uint, 0, len(rows))
	for _, a := range rows {
		if a.inEffect(now) {
			live = append(live, a)
			ids = append(ids, a.ID)
		}
	}
	states, err := s.repo.statesFor(ctx, s.db, userID, ids)
	if err != nil {
		return nil, err
	}
	byID := map[uint][]UserState{}
	for _, st := range states {
		byID[st.AnnouncementID] = append(byID[st.AnnouncementID], st)
	}
	items := make([]listItem, 0, len(live))
	for _, a := range live {
		item := listItem{Announcement: a}
		if st := matchVersion(byID[a.ID], a.UpdatedAt); st != nil {
			if !includeDismissed && st.DismissedUntil != nil && st.DismissedUntil.After(now) {
				continue
			}
			item.ClosedAt = st.ClosedAt
		}
		items = append(items, item)
	}
	sort.SliceStable(items, func(i, j int) bool { return listBefore(&items[i], &items[j]) })
	return items, nil
}

// listBefore is the user-facing order: not closed first, pinned first, higher
// priority first, newer update first, higher id first.
func listBefore(a, b *listItem) bool {
	if ac, bc := a.ClosedAt != nil, b.ClosedAt != nil; ac != bc {
		return !ac
	}
	if a.Pinned != b.Pinned {
		return a.Pinned
	}
	if a.Priority != b.Priority {
		return a.Priority > b.Priority
	}
	if !a.UpdatedAt.Equal(b.UpdatedAt) {
		return a.UpdatedAt.After(b.UpdatedAt)
	}
	return a.ID > b.ID
}

func matchVersion(states []UserState, version time.Time) *UserState {
	for i := range states {
		if states[i].AnnouncementUpdatedAt.Equal(version) {
			return &states[i]
		}
	}
	return nil
}

// DismissToday hides the given version for the user until the next local
// midnight.
func (s *service) DismissToday(ctx context.Context, userID, id uint, version time.Time) error {
	return s.recordChoice(ctx, userID, id, version, func(now time.Time, cols map[string]any) {
		cols["dismissed_until"] = nextLocalMidnight(now)
	})
}

// Close records that the user closed the given version.
func (s *service) Close(ctx context.Context, userID, id uint, version time.Time) error {
	return s.recordChoice(ctx, userID, id, version, func(now time.Time, cols map[string]any) {
		cols["closed_at"] = now.UTC().Truncate(versionPrecision)
	})
}

// nextLocalMidnight is 00:00 on the day after now in the server's local zone.
func nextLocalMidnight(now time.Time) time.Time {
	l := now.In(time.Local)
	return time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, time.Local).UTC()
}

// recordChoice checks eligibility and writes the user's state in one
// transaction, holding the announcement's row lock so a concurrent edit or
// delete cannot slip in between. set fills the columns the action owns; the
// other column of an existing row is left untouched.
func (s *service) recordChoice(ctx context.Context, userID, id uint, version time.Time, set func(now time.Time, cols map[string]any)) error {
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		ok, err := s.repo.lockForUpdate(ctx, tx, id)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		a, err := s.repo.find(ctx, tx, id)
		if err != nil {
			return err
		}
		now := s.clock.Now()
		if !a.inEffect(now) || !a.UpdatedAt.Equal(version) {
			return ErrNotFound
		}
		existing, err := s.repo.statesIncludingDeleted(ctx, tx, userID, id)
		if err != nil {
			return err
		}
		cols := map[string]any{"updated_at": now.UTC().Truncate(versionPrecision)}
		set(now, cols)
		if st := matchVersion(existing, a.UpdatedAt); st != nil {
			return s.repo.reviveState(ctx, tx, st.ID, cols)
		}
		st := &UserState{AnnouncementID: id, UserID: userID, AnnouncementUpdatedAt: a.UpdatedAt.UTC()}
		if v, ok := cols["dismissed_until"].(time.Time); ok {
			st.DismissedUntil = &v
		}
		if v, ok := cols["closed_at"].(time.Time); ok {
			st.ClosedAt = &v
		}
		st.CreatedAt = now.UTC().Truncate(versionPrecision)
		st.UpdatedAt = st.CreatedAt
		return s.repo.createState(ctx, tx, st)
	})
}
