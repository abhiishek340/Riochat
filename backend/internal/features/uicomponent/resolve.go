package uicomponent

import (
	"context"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// MaxTickedComponents bounds how many component ids one request may carry and
// how many components the prompt catalog lists.
const MaxTickedComponents = 32

// Resolve maps the component ids ticked for a turn to the components the user
// may use. Zero and repeated ids are dropped, first appearance fixes the order,
// and ids that are unknown, disabled or owned by someone else are skipped
// silently. A zero user, no ids or a switched-off feature yields nothing.
func (s *Service) Resolve(ctx context.Context, userID uint, ids []uint) ([]UIComponent, error) {
	if userID == 0 || len(ids) == 0 || !s.Enabled(ctx) {
		return nil, nil
	}
	seen := make(map[uint]bool, len(ids))
	order := make([]uint, 0, len(ids))
	for _, id := range ids {
		if id == 0 || seen[id] {
			continue
		}
		seen[id] = true
		order = append(order, id)
		if len(order) >= 4*MaxTickedComponents {
			break
		}
	}
	if len(order) == 0 {
		return nil, nil
	}
	var rows []UIComponent
	q := visibleScope(s.db.WithContext(ctx).Model(&UIComponent{}), userID).Where("id IN ?", order)
	if err := q.Find(&rows).Error; err != nil {
		return nil, database.Translate(err)
	}
	byID := make(map[uint]UIComponent, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}
	out := make([]UIComponent, 0, len(rows))
	for _, id := range order {
		if r, ok := byID[id]; ok {
			out = append(out, r)
		}
	}
	return out, nil
}
