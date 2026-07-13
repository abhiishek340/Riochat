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

type upsertOptions struct {
	routeSource   string // default source of created routes
	catalogSource string // source of a created upstream model
	forceStatus   string
}

func cleanSource(raw string, fallback string) (string, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return fallback, nil
	}
	if len(s) > 16 {
		return "", badRequest(apperr.ErrInvalidBody.WithMessage("invalid request body: source is too long"))
	}
	return s, nil
}

func umSource(s string) string {
	switch s {
	case SourceManual, SourceSync, SourceImport:
		return s
	}
	return SourceManual
}

// upsertResult describes what an upsert did.
type upsertResult struct {
	row             *UpstreamModelRow
	platformCreated bool
	protocols       []string
	createdRoutes   int
	existingRoutes  int
	bindingCode     string
	platformID      uint
}

// UpsertBinding creates or replaces a binding between a platform model and an
// upstream model on one upstream.
func (s *Service) UpsertBinding(ctx context.Context, upstreamID uint, req upsertBindingReq) (*UpstreamModelRow, error) {
	res, err := s.upsertBinding(ctx, upstreamID, req, upsertOptions{})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	return res.row, nil
}

func (s *Service) upsertBinding(ctx context.Context, upstreamID uint, req upsertBindingReq, opt upsertOptions) (*upsertResult, error) {
	pmName, err := normalizePlatformName(req.PlatformModelName)
	if err != nil {
		return nil, err
	}
	umName := strings.TrimSpace(req.UpstreamModelName)
	if umName == "" {
		return nil, badRequest(apperr.ErrInvalidBody.WithMessage("invalid request body: upstreamModelName is required"))
	}
	if runeLen(umName) > 256 {
		return nil, badRequest(apperr.ErrInvalidBody.WithMessage("invalid request body: upstreamModelName is too long"))
	}
	headers := ""
	if req.HeadersJSON != nil {
		if err := validateHeadersJSON(*req.HeadersJSON); err != nil {
			return nil, err
		}
		headers = *req.HeadersJSON
	}
	var explicitKinds []string
	if strings.TrimSpace(req.KindsJSON) != "" {
		if explicitKinds, err = normalizeKinds(req.KindsJSON); err != nil {
			return nil, err
		}
	}
	var out upsertResult
	var rowUM UpstreamModel
	var rowPM PlatformModel
	var rowRoute ModelRoute
	err = s.tx(ctx, func(tx *gorm.DB) error {
		up, err := s.loadUpstream(ctx, tx, upstreamID)
		if err != nil {
			return err
		}
		var pm PlatformModel
		pmErr := tx.Where("name = ?", pmName).First(&pm).Error
		if pmErr != nil && !isNotFound(pmErr) {
			return pmErr
		}
		pmExists := pmErr == nil
		kinds := explicitKinds
		if kinds == nil {
			if pmExists {
				if kinds = parseStoredKinds(pm.KindsJSON); len(kinds) == 0 {
					kinds = []string{KindChat}
				}
			} else {
				kinds = inferKinds(pmName)
			}
		}
		defs := parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON)
		protocols, err := resolveProtocolSet(up.Compatible, defs, kinds, req.Protocols)
		if err != nil {
			return err
		}
		if !pmExists {
			vendor := deriveVendor("", pmName)
			order, err := s.nextSortOrder(tx)
			if err != nil {
				return err
			}
			pm = PlatformModel{Name: pmName, Vendor: vendor, KindsJSON: kindsJSON(kinds), CapabilitiesJSON: "{}",
				AccessScope: ScopePublic, Icon: autoIcon("", pmName, vendor), CBPolicyMode: "default",
				Status: StatusActive, SortOrder: order}
			if err := tx.Create(&pm).Error; err != nil {
				if !database.IsDuplicate(err) {
					return err
				}
				if err := tx.Where("name = ?", pmName).First(&pm).Error; err != nil {
					return err
				}
			} else {
				out.platformCreated = true
			}
		} else if explicitKinds != nil && kindsJSON(explicitKinds) != kindsJSON(parseStoredKinds(pm.KindsJSON)) {
			pm.KindsJSON = kindsJSON(explicitKinds)
			if err := tx.Model(&PlatformModel{}).Where("id = ?", pm.ID).Update("kinds_json", pm.KindsJSON).Error; err != nil {
				return err
			}
		}
		var um UpstreamModel
		umErr := tx.Where("upstream_id = ? AND upstream_model_name = ?", upstreamID, umName).First(&um).Error
		if umErr != nil && !isNotFound(umErr) {
			return umErr
		}
		if umErr != nil {
			code, err := newBindingCode()
			if err != nil {
				return err
			}
			vendor := deriveVendor("", umName, up.Name, up.BaseURL)
			src := umSource(firstNonEmpty(opt.catalogSource, derefStr(req.Source)))
			um = UpstreamModel{UpstreamID: upstreamID, BindingCode: code, UpstreamModelName: umName, Vendor: vendor,
				Icon: autoIcon("", umName, vendor), SuggestedProtocol: protocols[0], KindsJSON: kindsJSON(kinds),
				Status: StatusActive, Source: src, RawJSON: "{}"}
			if err := tx.Create(&um).Error; err != nil {
				if !database.IsDuplicate(err) {
					return err
				}
				if err := tx.Where("upstream_id = ? AND upstream_model_name = ?", upstreamID, umName).First(&um).Error; err != nil {
					return err
				}
			}
		}
		// Existing routes of the binding.
		var existingIDs []uint
		var existing []ModelRoute
		if len(req.RouteIDs) > 0 {
			ids := uniqueUints(req.RouteIDs)
			sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
			if err := tx.Where("id IN ?", ids).Order("id ASC").Find(&existing).Error; err != nil {
				return err
			}
			if len(existing) != len(ids) {
				return ErrBindingChanged
			}
			for _, r := range existing {
				var owner UpstreamModel
				if err := tx.First(&owner, r.UpstreamModelID).Error; err != nil || owner.UpstreamID != upstreamID {
					return ErrUpstreamModelNotFound
				}
				existingIDs = append(existingIDs, r.ID)
			}
		} else if err := tx.Where("platform_model_id = ? AND upstream_model_id = ?", pm.ID, um.ID).Order("id ASC").Find(&existing).Error; err != nil {
			return err
		}
		byProto := map[string]ModelRoute{}
		for _, r := range existing {
			byProto[r.Protocol] = r
		}
		defaultSource := firstNonEmpty(opt.routeSource, SourceManual)
		desired := make([]ModelRoute, 0, len(protocols))
		for _, p := range protocols {
			tpl, ok := byProto[p]
			if !ok && len(existing) > 0 {
				tpl, ok = existing[0], true
			}
			r := ModelRoute{Protocol: p, Status: StatusActive, Priority: 1, Weight: 1, Source: defaultSource}
			if ok {
				r.Status, r.Priority, r.Weight, r.Source = tpl.Status, tpl.Priority, tpl.Weight, tpl.Source
				r.CBFailureThreshold, r.CBDurationMin, r.CBWindowMin, r.HeadersJSON = tpl.CBFailureThreshold, tpl.CBDurationMin, tpl.CBWindowMin, tpl.HeadersJSON
			}
			if opt.forceStatus != "" {
				r.Status = opt.forceStatus
			}
			if req.Status != nil {
				st := normalizeStatus(*req.Status)
				if !validWritableStatus(st) {
					return badRequest(apperr.ErrInvalidBody)
				}
				r.Status = st
			}
			if req.Priority != nil {
				r.Priority = atLeastOne(*req.Priority)
			}
			if req.Weight != nil {
				r.Weight = atLeastOne(*req.Weight)
			}
			if req.Source != nil {
				if r.Source, err = cleanSource(*req.Source, SourceManual); err != nil {
					return err
				}
			}
			if req.CBFailureThreshold != nil {
				r.CBFailureThreshold = nonNegative(*req.CBFailureThreshold)
			}
			if req.CBDurationMin != nil {
				r.CBDurationMin = nonNegative(*req.CBDurationMin)
			}
			if req.CBWindowMin != nil {
				r.CBWindowMin = nonNegative(*req.CBWindowMin)
			}
			if req.HeadersJSON != nil {
				r.HeadersJSON = headers
			}
			desired = append(desired, r)
		}
		before := map[string]bool{}
		for _, r := range existing {
			if r.PlatformModelID == pm.ID && r.UpstreamModelID == um.ID {
				before[r.Protocol] = true
			}
		}
		if err := s.replaceRouteSet(tx, pm.ID, um.ID, desired, existingIDs); err != nil {
			if errors.Is(err, errRouteSetDuplicate) {
				return ErrRouteConflict
			}
			return err
		}
		for _, p := range protocols {
			if before[p] {
				out.existingRoutes++
			} else {
				out.createdRoutes++
			}
		}
		out.protocols, out.bindingCode, out.platformID = protocols, um.BindingCode, pm.ID
		var first ModelRoute
		if err := tx.Where("platform_model_id = ? AND upstream_model_id = ? AND protocol = ?", pm.ID, um.ID, protocols[0]).First(&first).Error; err != nil {
			return err
		}
		rowUM, rowPM, rowRoute = um, pm, first
		return nil
	})
	if err != nil {
		return nil, err
	}
	// Built after the transaction: rowFor reads settings through the pool, and on
	// SQLite (one connection) doing that while the transaction holds the connection
	// would wait on itself.
	row := s.rowFor(ctx, upstreamID, &rowUM, &rowPM, &rowRoute)
	out.row = &row
	return &out, nil
}

func (s *Service) rowFor(ctx context.Context, upstreamID uint, um *UpstreamModel, pm *PlatformModel, r *ModelRoute) UpstreamModelRow {
	open, until := s.circuitInfo(ctx, s.BreakerEnabled(ctx), bindingPrefix(upstreamID, um.BindingCode))
	return UpstreamModelRow{
		ID: um.ID, RouteID: r.ID, UpstreamID: upstreamID, BindingCode: um.BindingCode, PlatformModelID: pm.ID,
		PlatformModelName: pm.Name, ModelVendor: pm.Vendor, ModelKindsJSON: pm.KindsJSON, ModelIcon: pm.Icon,
		UpstreamModelName: um.UpstreamModelName, UpstreamModelVendor: um.Vendor, UpstreamModelIcon: um.Icon,
		UpstreamModelKindsJSON: um.KindsJSON, SuggestedProtocol: um.SuggestedProtocol, Protocol: r.Protocol,
		UpstreamModelStatus: um.Status, RouteStatus: r.Status, Priority: r.Priority, Weight: r.Weight, Source: r.Source,
		CBFailureThreshold: r.CBFailureThreshold, CBDurationMin: r.CBDurationMin, CBWindowMin: r.CBWindowMin,
		HeadersJSON: redactHeadersJSON(r.HeadersJSON), CircuitOpen: open, CircuitUntil: until,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

func derefStr(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ---- route operations by upstream --------------------------------------------------------

// routeOfUpstream loads a route that belongs to the upstream.
func (s *Service) routeOfUpstream(ctx context.Context, db *gorm.DB, upstreamID, routeID uint) (*ModelRoute, *UpstreamModel, error) {
	var r ModelRoute
	if err := db.WithContext(ctx).First(&r, routeID).Error; err != nil {
		if isNotFound(err) {
			return nil, nil, ErrUpstreamModelNotFound
		}
		return nil, nil, err
	}
	var um UpstreamModel
	if err := db.WithContext(ctx).First(&um, r.UpstreamModelID).Error; err != nil || um.UpstreamID != upstreamID {
		if err != nil && !isNotFound(err) {
			return nil, nil, err
		}
		return nil, nil, ErrUpstreamModelNotFound
	}
	return &r, &um, nil
}

// DeleteRoute deletes one route of an upstream.
func (s *Service) DeleteRoute(ctx context.Context, upstreamID, routeID uint) error {
	if _, _, err := s.routeOfUpstream(ctx, s.db, upstreamID, routeID); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Delete(&ModelRoute{}, routeID).Error; err != nil {
		return err
	}
	s.invalidateCatalog()
	return nil
}

// BatchDeleteRoutes deletes routes of an upstream sequentially.
func (s *Service) BatchDeleteRoutes(ctx context.Context, upstreamID uint, ids []uint) BatchResult {
	return s.batchDelete(ids, func(id uint) error { return s.DeleteRoute(ctx, upstreamID, id) }, ErrUpstreamModelNotFound)
}

// SetRouteStatus enables or disables a route.
func (s *Service) SetRouteStatus(ctx context.Context, upstreamID, routeID uint, status string) error {
	if _, _, err := s.routeOfUpstream(ctx, s.db, upstreamID, routeID); err != nil {
		return err
	}
	if err := s.db.WithContext(ctx).Model(&ModelRoute{}).Where("id = ?", routeID).Update("status", status).Error; err != nil {
		return err
	}
	s.invalidateCatalog()
	return nil
}

// OpenBindingCircuit opens the circuit of the route's upstream model.
func (s *Service) OpenBindingCircuit(ctx context.Context, upstreamID, routeID uint) error {
	_, um, err := s.routeOfUpstream(ctx, s.db, upstreamID, routeID)
	if err != nil {
		return err
	}
	if !s.BreakerEnabled(ctx) {
		return ErrBreakerDisabled
	}
	return s.circuits.openManual(ctx, bindingPrefix(upstreamID, um.BindingCode))
}

// ResetBindingCircuit clears the circuit of the route's upstream model.
func (s *Service) ResetBindingCircuit(ctx context.Context, upstreamID, routeID uint) error {
	_, um, err := s.routeOfUpstream(ctx, s.db, upstreamID, routeID)
	if err != nil {
		return err
	}
	return s.circuits.reset(ctx, bindingPrefix(upstreamID, um.BindingCode), false)
}

// ---- model protocol set ------------------------------------------------------------------

// SetModelProtocols rewrites the protocol set of every binding of a model and
// the model's kinds, atomically.
func (s *Service) SetModelProtocols(ctx context.Context, modelID uint, req setProtocolsReq) (*ModelView, error) {
	kinds, err := normalizeKinds(req.KindsJSON)
	if err != nil {
		return nil, err
	}
	err = s.tx(ctx, func(tx *gorm.DB) error {
		pm, err := s.loadModel(ctx, tx, modelID)
		if err != nil {
			return err
		}
		var routes []ModelRoute
		if err := tx.Where("platform_model_id = ?", modelID).Order("id ASC").Find(&routes).Error; err != nil {
			return err
		}
		if len(routes) == 0 {
			return ErrSourcesNotFound
		}
		byUM := map[uint][]ModelRoute{}
		var umOrder []uint
		for _, r := range routes {
			if _, ok := byUM[r.UpstreamModelID]; !ok {
				umOrder = append(umOrder, r.UpstreamModelID)
			}
			byUM[r.UpstreamModelID] = append(byUM[r.UpstreamModelID], r)
		}
		if err := tx.Model(&PlatformModel{}).Where("id = ?", pm.ID).Update("kinds_json", kindsJSON(kinds)).Error; err != nil {
			return err
		}
		for _, umID := range umOrder {
			var um UpstreamModel
			if err := tx.First(&um, umID).Error; err != nil {
				return err
			}
			up, err := s.loadUpstream(ctx, tx, um.UpstreamID)
			if err != nil {
				return err
			}
			protocols, err := resolveProtocolSet(up.Compatible, parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON), kinds, req.Protocols)
			if err != nil {
				return err
			}
			existing := byUM[umID]
			ids := make([]uint, 0, len(existing))
			byProto := map[string]ModelRoute{}
			for _, r := range existing {
				ids = append(ids, r.ID)
				byProto[r.Protocol] = r
			}
			desired := make([]ModelRoute, 0, len(protocols))
			for _, p := range protocols {
				tpl, ok := byProto[p]
				if !ok {
					tpl = existing[0]
				}
				desired = append(desired, ModelRoute{Protocol: p, Status: tpl.Status, Priority: tpl.Priority, Weight: tpl.Weight,
					Source: tpl.Source, CBFailureThreshold: tpl.CBFailureThreshold, CBDurationMin: tpl.CBDurationMin,
					CBWindowMin: tpl.CBWindowMin, HeadersJSON: tpl.HeadersJSON})
			}
			if err := s.replaceRouteSet(tx, modelID, umID, desired, ids); err != nil {
				if errors.Is(err, errRouteSetDuplicate) {
					return ErrUpstreamModelConflict
				}
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	return s.GetModel(ctx, modelID)
}

// ---- sources (model-centric) -----------------------------------------------------------------

func (s *Service) sourceView(ctx context.Context, r routeRecord, breaker bool) SourceView {
	v := SourceView{
		ID: r.RouteID, UpstreamID: r.UpstreamID, UpstreamName: r.UpName, UpstreamStatus: r.UpStatus, BaseURL: r.UpBaseURL,
		BindingCode: r.BindingCode, UpstreamModelName: r.UpstreamModelName, UpstreamModelKindsJSON: r.UMKindsJSON,
		UpstreamModelVendor: deriveVendor(r.UMVendor, r.UpstreamModelName, r.UpName, r.UpBaseURL),
		SuggestedProtocol:   r.SuggestedProtocol, UpstreamModelStatus: r.UMStatus, Protocol: r.Protocol, Status: r.RouteStatus,
		Priority: r.Priority, Weight: r.Weight, Source: r.RouteSource, CBFailureThreshold: r.RouteCBFailure,
		CBDurationMin: r.RouteCBDuration, CBWindowMin: r.RouteCBWindow, HeadersJSON: redactHeadersJSON(r.RouteHeaders),
		CreatedAt: r.RouteCreatedAt, UpdatedAt: r.RouteUpdatedAt,
	}
	v.UpstreamModelIcon = autoIcon(r.UMIcon, r.UpstreamModelName, v.UpstreamModelVendor)
	if breaker {
		if open, until := s.circuitInfo(ctx, true, upstreamPrefix(r.UpstreamID)); open {
			v.CircuitOpen, v.CircuitUntil, v.CircuitScope = true, until, "upstream"
		} else if open, until := s.circuitInfo(ctx, true, bindingPrefix(r.UpstreamID, r.BindingCode)); open {
			v.CircuitOpen, v.CircuitUntil, v.CircuitScope = true, until, "source"
		}
	}
	return v
}

// ListSources lists the routes of a model ordered by priority then newest first.
func (s *Service) ListSources(ctx context.Context, modelID uint, page, size int) ([]SourceView, int64, error) {
	if _, err := s.loadModel(ctx, s.db, modelID); err != nil {
		return nil, 0, err
	}
	var total int64
	if err := s.db.WithContext(ctx).Model(&ModelRoute{}).Where("platform_model_id = ?", modelID).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var recs []routeRecord
	err := routeQuery(s.db.WithContext(ctx)).Where("r.platform_model_id = ?", modelID).
		Order("r.priority ASC, r.id DESC").Offset(pageOffset(page, size)).Limit(size).Scan(&recs).Error
	if err != nil {
		return nil, 0, err
	}
	breaker := s.BreakerEnabled(ctx)
	out := make([]SourceView, 0, len(recs))
	for _, r := range recs {
		out = append(out, s.sourceView(ctx, r, breaker))
	}
	return out, total, nil
}

func (s *Service) getSource(ctx context.Context, db *gorm.DB, routeID uint) (*SourceView, error) {
	var rec routeRecord
	if err := routeQuery(db.WithContext(ctx)).Where("r.id = ?", routeID).Scan(&rec).Error; err != nil {
		return nil, err
	}
	if rec.RouteID == 0 {
		return nil, ErrUpstreamModelNotFound
	}
	v := s.sourceView(ctx, rec, s.BreakerEnabled(ctx))
	return &v, nil
}

// BindSource binds an existing upstream model to a platform model.
func (s *Service) BindSource(ctx context.Context, modelID uint, req bindSourceReq) (*SourceView, error) {
	var routeID uint
	err := s.tx(ctx, func(tx *gorm.DB) error {
		pm, err := s.loadModel(ctx, tx, modelID)
		if err != nil {
			return err
		}
		up, err := s.loadUpstream(ctx, tx, req.UpstreamID)
		if err != nil {
			return err
		}
		if up.Status != StatusActive {
			return ErrSourceUnavailable
		}
		var um UpstreamModel
		if err := tx.First(&um, req.UpstreamModelID).Error; err != nil || um.UpstreamID != up.ID {
			if err != nil && !isNotFound(err) {
				return err
			}
			return ErrUpstreamModelNotFound
		}
		if um.Status != StatusActive {
			return ErrSourceUnavailable
		}
		kinds := parseStoredKinds(pm.KindsJSON)
		explicit := derefStr(req.Protocol)
		if strings.TrimSpace(explicit) == "" {
			explicit = um.SuggestedProtocol
		}
		proto, err := resolveProtocol(up.Compatible, parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON), kinds, explicit)
		if err != nil {
			return err
		}
		var existing []ModelRoute
		if err := tx.Where("platform_model_id = ? AND upstream_model_id = ?", pm.ID, um.ID).Find(&existing).Error; err != nil {
			return err
		}
		set := []string{proto}
		var current *ModelRoute
		for i, r := range existing {
			set = append(set, r.Protocol)
			if r.Protocol == proto {
				current = &existing[i]
			}
		}
		if !supportedCombination(set) {
			return ErrInvalidCombination
		}
		route := ModelRoute{PlatformModelID: pm.ID, UpstreamModelID: um.ID, Protocol: proto, Status: StatusActive, Priority: 1, Weight: 1, Source: SourceManual}
		if current != nil {
			route = *current
		}
		if req.Status != nil {
			route.Status = normalizeStatus(*req.Status)
		}
		if req.Priority != nil {
			route.Priority = atLeastOne(*req.Priority)
		}
		if req.Weight != nil {
			route.Weight = atLeastOne(*req.Weight)
		}
		if req.CBFailureThreshold != nil {
			route.CBFailureThreshold = nonNegative(*req.CBFailureThreshold)
		}
		if req.CBDurationMin != nil {
			route.CBDurationMin = nonNegative(*req.CBDurationMin)
		}
		if req.CBWindowMin != nil {
			route.CBWindowMin = nonNegative(*req.CBWindowMin)
		}
		if err := tx.Save(&route).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrRouteConflict
			}
			return err
		}
		routeID = route.ID
		return nil
	})
	if err != nil {
		return nil, err
	}
	s.invalidateCatalog()
	return s.getSource(ctx, s.db, routeID)
}

// UpdateSource updates one route of a model.
func (s *Service) UpdateSource(ctx context.Context, modelID, routeID uint, req updateSourceReq) (*SourceView, error) {
	changed := false
	err := s.tx(ctx, func(tx *gorm.DB) error {
		if _, err := s.loadModel(ctx, tx, modelID); err != nil {
			return err
		}
		var r ModelRoute
		if err := tx.First(&r, routeID).Error; err != nil {
			if !isNotFound(err) {
				return err
			}
			return ErrUpstreamModelNotFound
		}
		if r.PlatformModelID != modelID {
			return ErrModelNotFound
		}
		if req.Protocol != nil {
			p := lower(*req.Protocol)
			if p == "" {
				return ErrProtocolRequired
			}
			if !isKnownProtocol(p) {
				return ErrInvalidAdapter
			}
			if p != r.Protocol {
				var siblings []ModelRoute
				if err := tx.Where("platform_model_id = ? AND upstream_model_id = ? AND id <> ?", r.PlatformModelID, r.UpstreamModelID, r.ID).Find(&siblings).Error; err != nil {
					return err
				}
				set := []string{p}
				for _, sib := range siblings {
					set = append(set, sib.Protocol)
					if sib.Protocol == p {
						return ErrRouteConflict
					}
				}
				if !supportedCombination(set) {
					return ErrInvalidCombination
				}
				r.Protocol = p
				changed = true
			}
		}
		if req.Status != nil {
			r.Status = normalizeStatus(*req.Status)
			changed = true
		}
		if req.Priority != nil {
			r.Priority = atLeastOne(*req.Priority)
			changed = true
		}
		if req.Weight != nil {
			r.Weight = atLeastOne(*req.Weight)
			changed = true
		}
		if req.CBFailureThreshold != nil {
			r.CBFailureThreshold = nonNegative(*req.CBFailureThreshold)
			changed = true
		}
		if req.CBDurationMin != nil {
			r.CBDurationMin = nonNegative(*req.CBDurationMin)
			changed = true
		}
		if req.CBWindowMin != nil {
			r.CBWindowMin = nonNegative(*req.CBWindowMin)
			changed = true
		}
		if !changed {
			return nil
		}
		if err := tx.Save(&r).Error; err != nil {
			if database.IsDuplicate(err) {
				return ErrRouteConflict
			}
			return err
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if changed {
		s.invalidateCatalog()
	}
	return s.getSource(ctx, s.db, routeID)
}
