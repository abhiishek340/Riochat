package skill

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Service is the skill area's public Go API. Other areas (chat, projects, user
// purge) construct one with NewService from their own database handle.
type Service struct {
	repo repository
	clk  clock.Clock
}

// NewService builds a service. A nil clock means the system clock.
func NewService(db *gorm.DB, clk clock.Clock) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	return &Service{repo: repository{db: db}, clk: clk}
}

// Discover lists the skills the user may pick: enabled built-ins plus the
// user's own enabled skills, as summaries.
func (s *Service) Discover(ctx context.Context, user uint, ids []uint, keyword string, p pagination.Page) ([]Summary, int64, error) {
	if user == 0 {
		return nil, 0, ErrInvalidSkill
	}
	rows, total, err := s.repo.discover(ctx, user, NormalizeIDs(ids), keyword, p)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Summary, len(rows))
	for i, r := range rows {
		out[i] = r.summary()
	}
	return out, total, nil
}

// Get returns one skill usable by the user. Everything else is ErrNotFound.
func (s *Service) Get(ctx context.Context, user, id uint) (Full, error) {
	if user == 0 || id == 0 {
		return Full{}, ErrInvalidSkill
	}
	rows, err := s.repo.usable(ctx, user, []uint{id})
	if err != nil {
		return Full{}, err
	}
	if len(rows) == 0 {
		return Full{}, ErrNotFound
	}
	return rows[0].full(), nil
}

// ListOwned lists the user's personal skills (scope user) or, with
// ScopeBuiltin, the built-in ones; enabled nil means no filter.
func (s *Service) ListOwned(ctx context.Context, scope string, owner uint, keyword string, enabled *bool, p pagination.Page) ([]Full, int64, error) {
	rows, total, err := s.repo.manage(ctx, scope, owner, keyword, enabled, p)
	if err != nil {
		return nil, 0, err
	}
	out := make([]Full, len(rows))
	for i, r := range rows {
		out[i] = r.full()
	}
	return out, total, nil
}

// Create stores a skill in the given scope. owner is the personal owner and
// must be 0 for built-ins; actor is the acting user.
func (s *Service) Create(ctx context.Context, scope string, owner, actor uint, in Input) (Full, error) {
	if actor == 0 || (scope != ScopeBuiltin && scope != ScopeUser) || (scope == ScopeUser && owner == 0) {
		return Full{}, ErrInvalidSkill
	}
	in, err := in.normalized()
	if err != nil {
		return Full{}, err
	}
	now := s.clk.Now()
	row := Skill{
		HardModel:   database.HardModel{CreatedAt: now, UpdatedAt: now},
		Scope:       scope,
		OwnerUserID: owner,
		Title:       in.Title, Trigger: in.Trigger, Description: in.Description, Markdown: in.Markdown,
		Enabled:         true,
		SortOrder:       in.SortOrder,
		CreatedByUserID: actor,
		UpdatedByUserID: actor,
	}
	if err := s.repo.insert(ctx, &row); err != nil {
		return Full{}, err
	}
	return row.full(), nil
}

// Update applies a partial update to a skill of the given scope and owner.
func (s *Service) Update(ctx context.Context, scope string, owner, actor, id uint, p Patch) (Full, error) {
	if actor == 0 || id == 0 {
		return Full{}, ErrInvalidSkill
	}
	changes, err := p.changes()
	if err != nil {
		return Full{}, err
	}
	changes["updated_by_user_id"] = actor
	changes["updated_at"] = s.clk.Now()
	row, err := s.repo.patch(ctx, scope, owner, id, changes)
	if err != nil {
		return Full{}, err
	}
	return row.full(), nil
}

// Delete removes a skill of the given scope and owner and its project links.
func (s *Service) Delete(ctx context.Context, scope string, owner, id uint) error {
	if id == 0 {
		return ErrInvalidSkill
	}
	return s.repo.remove(ctx, scope, owner, id)
}

// ValidateUsable reports ErrNotFound unless every id (after dropping zeros and
// duplicates) is usable by the user.
func (s *Service) ValidateUsable(ctx context.Context, user uint, ids []uint) error {
	if user == 0 {
		return ErrInvalidSkillUse
	}
	ids = NormalizeIDs(ids)
	if len(ids) == 0 {
		return nil
	}
	n, err := s.repo.countUsable(ctx, user, ids)
	if err != nil {
		return err
	}
	if n != int64(len(ids)) {
		return ErrNotFound
	}
	return nil
}

// Resolve turns the skill ids selected for one message into skills, in
// selection order. maxSelected is the configured shared selection limit (it is
// passed through SelectionLimit). Zero ids and duplicates are dropped; an empty
// selection yields nil.
func (s *Service) Resolve(ctx context.Context, user uint, ids []uint, maxSelected int) ([]Skill, error) {
	ids = NormalizeIDs(ids)
	if len(ids) == 0 {
		return nil, nil
	}
	if user == 0 {
		return nil, ErrInvalidSkillUse
	}
	if len(ids) > SelectionLimit(maxSelected) {
		return nil, ErrTooManySelected
	}
	rows, err := s.repo.usable(ctx, user, ids)
	if err != nil {
		return nil, err
	}
	byID := make(map[uint]Skill, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]Skill, 0, len(ids))
	for _, id := range ids {
		r, ok := byID[id]
		if !ok {
			return nil, ErrNotFound
		}
		out = append(out, r)
	}
	return out, nil
}
