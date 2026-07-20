package skill

import (
	"context"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// ProjectDefaults loads the default skill ids of many projects in one query,
// ordered by position then skill id. Every requested project has an entry
// (an empty, non-nil slice when it has none).
func (s *Service) ProjectDefaults(ctx context.Context, projectIDs []uint) (map[uint][]uint, error) {
	out := make(map[uint][]uint, len(projectIDs))
	for _, id := range projectIDs {
		out[id] = []uint{}
	}
	if len(projectIDs) == 0 {
		return out, nil
	}
	var links []ProjectLink
	err := s.repo.db.WithContext(ctx).Where("project_id IN ?", projectIDs).
		Order("project_id ASC, sort_order ASC, skill_id ASC").Find(&links).Error
	if err != nil {
		return nil, database.Translate(err)
	}
	for _, l := range links {
		out[l.ProjectID] = append(out[l.ProjectID], l.SkillID)
	}
	return out, nil
}

// ReplaceProjectDefaults replaces the whole default set of a project inside tx
// (pass the caller's transaction). Ids are normalized; positions start at 1.
func ReplaceProjectDefaults(tx *gorm.DB, projectID uint, ids []uint) error {
	if projectID == 0 {
		return ErrInvalidSkill
	}
	if err := tx.Where("project_id = ?", projectID).Delete(&ProjectLink{}).Error; err != nil {
		return database.Translate(err)
	}
	ids = NormalizeIDs(ids)
	links := make([]ProjectLink, len(ids))
	for i, id := range ids {
		links[i] = ProjectLink{ProjectID: projectID, SkillID: id, SortOrder: i + 1}
	}
	if len(links) == 0 {
		return nil
	}
	return database.Translate(tx.Create(&links).Error)
}

// DeleteProjectDefaults removes every default skill link of a project (call it
// when the project is deleted).
func DeleteProjectDefaults(tx *gorm.DB, projectID uint) error {
	return database.Translate(tx.Where("project_id = ?", projectID).Delete(&ProjectLink{}).Error)
}

// ValidateProjectDefaults checks a project's new default set against its
// previous one. An unchanged set is always valid. A changed set must not exceed
// the shared selection limit, and every newly added id must be usable by the
// user; previously saved ids are not re-checked. Failures are
// ErrInvalidProjectDefaults.
func (s *Service) ValidateProjectDefaults(ctx context.Context, user uint, previous, next []uint, maxSelected int) error {
	previous, next = NormalizeIDs(previous), NormalizeIDs(next)
	if sameList(previous, next) {
		return nil
	}
	if len(next) > SelectionLimit(maxSelected) {
		return ErrInvalidProjectDefaults
	}
	had := make(map[uint]struct{}, len(previous))
	for _, id := range previous {
		had[id] = struct{}{}
	}
	var added []uint
	for _, id := range next {
		if _, ok := had[id]; !ok {
			added = append(added, id)
		}
	}
	if len(added) == 0 {
		return nil
	}
	if user == 0 {
		return ErrInvalidProjectDefaults
	}
	n, err := s.repo.countUsable(ctx, user, added)
	if err != nil {
		return err
	}
	if n != int64(len(added)) {
		return ErrInvalidProjectDefaults
	}
	return nil
}

func sameList(a, b []uint) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// PurgeUser removes everything the skill area holds for a deleted account,
// inside tx: the user's personal skills, links pointing at them, and the links
// of the given projects (the user's projects, supplied by the project area).
// Built-in skills are kept.
func PurgeUser(tx *gorm.DB, userID uint, projectIDs []uint) error {
	if userID == 0 {
		return ErrInvalidSkill
	}
	mine := tx.Model(&Skill{}).Select("id").Where("scope = ? AND owner_user_id = ?", ScopeUser, userID)
	if err := tx.Where("skill_id IN (?)", mine).Delete(&ProjectLink{}).Error; err != nil {
		return database.Translate(err)
	}
	if len(projectIDs) > 0 {
		if err := tx.Where("project_id IN ?", projectIDs).Delete(&ProjectLink{}).Error; err != nil {
			return database.Translate(err)
		}
	}
	return database.Translate(tx.Where("scope = ? AND owner_user_id = ?", ScopeUser, userID).Delete(&Skill{}).Error)
}
