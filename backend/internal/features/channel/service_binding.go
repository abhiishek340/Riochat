package channel

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
)

var errRouteSetDuplicate = errors.New("channel: duplicate route target")

func newBindingCode() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return "upm_" + hex.EncodeToString(b[:]), nil
}

// circuitInfo reads a circuit for display (only when the breaker is enabled).
func (s *Service) circuitInfo(ctx context.Context, enabled bool, prefix string) (bool, string) {
	if !enabled {
		return false, ""
	}
	open, until, err := s.circuits.peekOpen(ctx, prefix)
	if err != nil || !open {
		return false, ""
	}
	return true, strconv.FormatInt(until, 10)
}

// ---- upstream model listing --------------------------------------------------------

// UpstreamModelQuery are the filters of GET /admin/llm/upstreams/:id/models.
type UpstreamModelQuery struct {
	Q              string
	RouteStatus    string
	UpstreamStatus string
	Protocol       string
	Sort           string
	Page           int
	PageSize       int
}

type bindingRoutes struct {
	um     UpstreamModel
	pm     *PlatformModel
	routes []ModelRoute
}

// ListUpstreamModels pages over bindings and returns the flattened rows.
func (s *Service) ListUpstreamModels(ctx context.Context, upstreamID uint, q UpstreamModelQuery) ([]UpstreamModelRow, int64, error) {
	if _, err := s.loadUpstream(ctx, s.db, upstreamID); err != nil {
		return nil, 0, err
	}
	var ums []UpstreamModel
	if err := s.db.WithContext(ctx).Where("upstream_id = ?", upstreamID).Find(&ums).Error; err != nil {
		return nil, 0, err
	}
	umIDs := make([]uint, 0, len(ums))
	for _, um := range ums {
		umIDs = append(umIDs, um.ID)
	}
	var routes []ModelRoute
	if len(umIDs) > 0 {
		if err := s.db.WithContext(ctx).Where("upstream_model_id IN ?", umIDs).Order("id ASC").Find(&routes).Error; err != nil {
			return nil, 0, err
		}
	}
	pmIDs := map[uint]bool{}
	for _, r := range routes {
		pmIDs[r.PlatformModelID] = true
	}
	pms := map[uint]*PlatformModel{}
	if len(pmIDs) > 0 {
		ids := make([]uint, 0, len(pmIDs))
		for id := range pmIDs {
			ids = append(ids, id)
		}
		var list []PlatformModel
		if err := s.db.WithContext(ctx).Where("id IN ?", ids).Find(&list).Error; err != nil {
			return nil, 0, err
		}
		for i := range list {
			pms[list[i].ID] = &list[i]
		}
	}
	type key struct{ um, pm uint }
	byKey := map[key]*bindingRoutes{}
	var order []key
	for _, um := range ums {
		k := key{um.ID, 0}
		byKey[k] = &bindingRoutes{um: um}
		order = append(order, k)
	}
	umByID := map[uint]UpstreamModel{}
	for _, um := range ums {
		umByID[um.ID] = um
	}
	for _, r := range routes {
		k := key{r.UpstreamModelID, r.PlatformModelID}
		b := byKey[k]
		if b == nil {
			b = &bindingRoutes{um: umByID[r.UpstreamModelID], pm: pms[r.PlatformModelID]}
			byKey[k] = b
			order = append(order, k)
			// an upstream model with a binding is no longer "unbound"
			delete(byKey, key{r.UpstreamModelID, 0})
			for i, o := range order {
				if o == (key{r.UpstreamModelID, 0}) {
					order = append(order[:i], order[i+1:]...)
					break
				}
			}
		}
		b.routes = append(b.routes, r)
	}
	var bindings []*bindingRoutes
	for _, k := range order {
		if b := byKey[k]; b != nil {
			bindings = append(bindings, b)
		}
	}

	needle := strings.ToLower(strings.TrimSpace(q.Q))
	filtered := bindings[:0:0]
	for _, b := range bindings {
		if needle != "" {
			hay := []string{b.um.UpstreamModelName, b.um.BindingCode}
			if b.pm != nil {
				hay = append(hay, b.pm.Name)
			}
			for _, r := range b.routes {
				hay = append(hay, r.Protocol)
			}
			ok := false
			for _, h := range hay {
				if strings.Contains(strings.ToLower(h), needle) {
					ok = true
				}
			}
			if !ok {
				continue
			}
		}
		switch q.RouteStatus {
		case "bound":
			if len(b.routes) == 0 {
				continue
			}
		case StatusActive, StatusInactive:
			has := false
			for _, r := range b.routes {
				if r.Status == q.RouteStatus {
					has = true
				}
			}
			if !has {
				continue
			}
		}
		if (q.UpstreamStatus == StatusActive || q.UpstreamStatus == StatusInactive) && b.um.Status != q.UpstreamStatus {
			continue
		}
		if q.Protocol != "" {
			has := false
			for _, r := range b.routes {
				if r.Protocol == q.Protocol {
					has = true
				}
			}
			if !has {
				continue
			}
		}
		filtered = append(filtered, b)
	}
	pmName := func(b *bindingRoutes) string {
		if b.pm == nil {
			return ""
		}
		return b.pm.Name
	}
	statusRank := func(b *bindingRoutes) int {
		if len(b.routes) == 0 {
			return 2
		}
		for _, r := range b.routes {
			if r.Status == StatusActive {
				return 0
			}
		}
		return 1
	}
	minProto := func(b *bindingRoutes) string {
		p := ""
		for _, r := range b.routes {
			if p == "" || r.Protocol < p {
				p = r.Protocol
			}
		}
		return p
	}
	tie := func(a, b *bindingRoutes) bool {
		if a.um.UpstreamModelName != b.um.UpstreamModelName {
			return a.um.UpstreamModelName < b.um.UpstreamModelName
		}
		if a.um.ID != b.um.ID {
			return a.um.ID < b.um.ID
		}
		var ap, bp uint
		if a.pm != nil {
			ap = a.pm.ID
		}
		if b.pm != nil {
			bp = b.pm.ID
		}
		return ap < bp
	}
	sort.SliceStable(filtered, func(i, j int) bool {
		a, b := filtered[i], filtered[j]
		switch q.Sort {
		case "upstream_desc":
			if a.um.UpstreamModelName != b.um.UpstreamModelName {
				return a.um.UpstreamModelName > b.um.UpstreamModelName
			}
		case "platform_asc":
			if pmName(a) != pmName(b) {
				return pmName(a) < pmName(b)
			}
		case "platform_desc":
			if pmName(a) != pmName(b) {
				return pmName(a) > pmName(b)
			}
		case "status_asc":
			if statusRank(a) != statusRank(b) {
				return statusRank(a) < statusRank(b)
			}
		case "protocol_asc":
			if minProto(a) != minProto(b) {
				return minProto(a) < minProto(b)
			}
		}
		return tie(a, b)
	})
	total := int64(len(filtered))
	from, to := pageWindow(q.Page, q.PageSize, len(filtered))
	breaker := s.BreakerEnabled(ctx)
	rows := []UpstreamModelRow{}
	for _, b := range filtered[from:to] {
		open, until := s.circuitInfo(ctx, breaker, bindingPrefix(upstreamID, b.um.BindingCode))
		base := UpstreamModelRow{
			ID: b.um.ID, UpstreamID: upstreamID, BindingCode: b.um.BindingCode, UpstreamModelName: b.um.UpstreamModelName,
			UpstreamModelVendor: b.um.Vendor, UpstreamModelIcon: b.um.Icon, UpstreamModelKindsJSON: b.um.KindsJSON,
			SuggestedProtocol: b.um.SuggestedProtocol, UpstreamModelStatus: b.um.Status,
			CircuitOpen: open, CircuitUntil: until, CreatedAt: b.um.CreatedAt, UpdatedAt: b.um.UpdatedAt,
		}
		if b.pm != nil {
			base.PlatformModelID, base.PlatformModelName = b.pm.ID, b.pm.Name
			base.ModelVendor, base.ModelKindsJSON, base.ModelIcon = b.pm.Vendor, b.pm.KindsJSON, b.pm.Icon
		}
		if len(b.routes) == 0 {
			rows = append(rows, base)
			continue
		}
		for _, r := range b.routes {
			row := base
			row.RouteID, row.Protocol, row.RouteStatus = r.ID, r.Protocol, r.Status
			row.Priority, row.Weight, row.Source = r.Priority, r.Weight, r.Source
			row.CBFailureThreshold, row.CBDurationMin, row.CBWindowMin = r.CBFailureThreshold, r.CBDurationMin, r.CBWindowMin
			row.HeadersJSON = redactHeadersJSON(r.HeadersJSON)
			row.CreatedAt, row.UpdatedAt = r.CreatedAt, r.UpdatedAt
			rows = append(rows, row)
		}
	}
	return rows, total, nil
}

// ---- route set replacement ----------------------------------------------------------

// replaceRouteSet makes the routes of (pmID, umID) exactly the desired set,
// reusing the given existing routes. existingIDs must be exactly the routes of
// one pair.
func (s *Service) replaceRouteSet(tx *gorm.DB, pmID, umID uint, desired []ModelRoute, existingIDs []uint) error {
	var candidates []ModelRoute
	if len(existingIDs) > 0 {
		if err := tx.Where("id IN ?", existingIDs).Order("id ASC").Find(&candidates).Error; err != nil {
			return err
		}
		if len(candidates) != len(existingIDs) {
			return ErrBindingChanged
		}
		srcPM, srcUM := candidates[0].PlatformModelID, candidates[0].UpstreamModelID
		for _, c := range candidates {
			if c.PlatformModelID != srcPM || c.UpstreamModelID != srcUM {
				return ErrBindingChanged
			}
		}
		var pair []ModelRoute
		if err := tx.Where("platform_model_id = ? AND upstream_model_id = ?", srcPM, srcUM).Find(&pair).Error; err != nil {
			return err
		}
		if len(pair) != len(candidates) {
			return ErrBindingChanged
		}
		if srcPM != pmID || srcUM != umID {
			var n int64
			if err := tx.Model(&ModelRoute{}).Where("platform_model_id = ? AND upstream_model_id = ?", pmID, umID).Count(&n).Error; err != nil {
				return err
			}
			if n > 0 {
				return errRouteSetDuplicate
			}
		}
	} else {
		if err := tx.Where("platform_model_id = ? AND upstream_model_id = ?", pmID, umID).Order("id ASC").Find(&candidates).Error; err != nil {
			return err
		}
	}
	used := map[uint]bool{}
	type plan struct {
		route ModelRoute
		reuse *ModelRoute
	}
	var plans []plan
	pick := func(match func(c ModelRoute) bool) *ModelRoute {
		for i := range candidates {
			if !used[candidates[i].ID] && match(candidates[i]) {
				used[candidates[i].ID] = true
				return &candidates[i]
			}
		}
		return nil
	}
	// Pass 1 and 2 bind by protocol so protocol rewrites never collide.
	for _, d := range desired {
		d := d
		c := pick(func(c ModelRoute) bool {
			return c.PlatformModelID == pmID && c.UpstreamModelID == umID && c.Protocol == d.Protocol
		})
		if c == nil {
			c = pick(func(c ModelRoute) bool { return c.Protocol == d.Protocol })
		}
		plans = append(plans, plan{route: d, reuse: c})
	}
	for i := range plans {
		if plans[i].reuse == nil {
			plans[i].reuse = pick(func(ModelRoute) bool { return true })
		}
	}
	for _, c := range candidates {
		if !used[c.ID] {
			if err := tx.Delete(&ModelRoute{}, c.ID).Error; err != nil {
				return err
			}
		}
	}
	for _, p := range plans {
		r := p.route
		r.PlatformModelID, r.UpstreamModelID = pmID, umID
		var err error
		if p.reuse != nil {
			r.ID, r.CreatedAt = p.reuse.ID, p.reuse.CreatedAt
			err = tx.Save(&r).Error
		} else {
			r.ID = 0
			err = tx.Create(&r).Error
		}
		if err != nil {
			if database.IsDuplicate(err) {
				return errRouteSetDuplicate
			}
			return err
		}
	}
	return nil
}

// ---- upsert ------------------------------------------------------------------------------
