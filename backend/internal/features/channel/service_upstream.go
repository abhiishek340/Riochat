package channel

import (
	"context"
	"errors"
	"sort"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// ---- key documents ----------------------------------------------------------------

func (s *Service) keyHash(v string) string { return s.secrets.LookupHash(v) }

// decryptKeys decrypts and parses the key configuration of an upstream.
func (s *Service) decryptKeys(up *Upstream) (*keyDoc, error) {
	plain, _, err := s.secrets.Decrypt(up.APIKeysEnc)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(plain) == "" {
		return nil, errors.New("channel: empty key configuration")
	}
	return parseKeyDoc(plain)
}

func (s *Service) encryptKeys(doc *keyDoc) (string, error) {
	raw, err := doc.marshal()
	if err != nil {
		return "", err
	}
	return s.secrets.Encrypt(raw)
}

// parseAndValidateKeys parses a submitted key document.
func parseAndValidateKeys(raw string) (*keyDoc, error) {
	doc, err := parseKeyDoc(raw)
	if err != nil {
		return nil, err
	}
	if err := doc.validate(); err != nil {
		return nil, err
	}
	return doc, nil
}

// ---- views --------------------------------------------------------------------------

type chainRow struct {
	UpstreamID  uint
	UMID        uint
	BindingCode string
	RouteStatus string
	UMStatus    string
	PMStatus    string
}

func (s *Service) upstreamCounts(ctx context.Context, ids []uint, breaker bool, upstreams map[uint]*Upstream) (models, active map[uint]int64, err error) {
	models, active = map[uint]int64{}, map[uint]int64{}
	if len(ids) == 0 {
		return
	}
	var rows []chainRow
	err = s.db.WithContext(ctx).Table("llm_model_routes AS r").
		Select("um.upstream_id AS upstream_id, um.id AS um_id, um.binding_code AS binding_code, r.status AS route_status, um.status AS um_status, pm.status AS pm_status").
		Joins("JOIN llm_upstream_models um ON um.id = r.upstream_model_id").
		Joins("JOIN llm_platform_models pm ON pm.id = r.platform_model_id").
		Where("um.upstream_id IN ?", ids).Scan(&rows).Error
	if err != nil {
		return
	}
	seen, seenActive := map[[2]uint]bool{}, map[[2]uint]bool{}
	for _, r := range rows {
		k := [2]uint{r.UpstreamID, r.UMID}
		if !seen[k] {
			seen[k] = true
			models[r.UpstreamID]++
		}
		up := upstreams[r.UpstreamID]
		if seenActive[k] || up == nil || up.Status != StatusActive || r.RouteStatus != StatusActive || r.UMStatus != StatusActive || r.PMStatus != StatusActive {
			continue
		}
		if breaker {
			if open, _, perr := s.circuits.peekOpen(ctx, upstreamPrefix(r.UpstreamID)); perr == nil && open {
				continue
			}
			if open, _, perr := s.circuits.peekOpen(ctx, bindingPrefix(r.UpstreamID, r.BindingCode)); perr == nil && open {
				continue
			}
		}
		seenActive[k] = true
		active[r.UpstreamID]++
	}
	return
}

func (s *Service) upstreamViews(ctx context.Context, ups []Upstream) ([]UpstreamView, error) {
	ids := make([]uint, 0, len(ups))
	byID := map[uint]*Upstream{}
	for i := range ups {
		ids = append(ids, ups[i].ID)
		byID[ups[i].ID] = &ups[i]
	}
	breaker := s.BreakerEnabled(ctx)
	models, active, err := s.upstreamCounts(ctx, ids, breaker, byID)
	if err != nil {
		return nil, err
	}
	out := make([]UpstreamView, 0, len(ups))
	for i := range ups {
		up := &ups[i]
		v := UpstreamView{
			ID: up.ID, Name: up.Name, BaseURL: up.BaseURL, Compatible: up.Compatible,
			ProtocolDefaultsJSON: parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON).json(),
			APIKeyItems:          []APIKeyItem{},
			Status:               up.Status, ConnectTimeoutMS: up.ConnectTimeoutMS, ReadTimeoutMS: up.ReadTimeoutMS,
			StreamIdleTimeoutMS: up.StreamIdleTimeoutMS, CBFailureThreshold: up.CBFailureThreshold,
			CBModelThreshold: up.CBModelThreshold, CBThresholdLogic: up.CBThresholdLogic, CBDurationMin: up.CBDurationMin,
			CBWindowMin: up.CBWindowMin, HeadersJSON: redactHeadersJSON(up.HeadersJSON),
			ModelsCount: models[up.ID], ActiveModelsCount: active[up.ID],
			CreatedAt: up.CreatedAt, UpdatedAt: up.UpdatedAt,
		}
		if doc, err := s.decryptKeys(up); err == nil {
			v.APIKeysMasked = doc.masked()
			v.APIKeyItems = doc.items(s.keyHash)
		}
		if breaker {
			if open, until, err := s.circuits.peekOpen(ctx, upstreamPrefix(up.ID)); err == nil && open {
				v.CircuitOpen = true
				v.CircuitUntil = strconv.FormatInt(until, 10)
			}
		}
		if v.CircuitOpen || up.Status != StatusActive {
			v.ActiveModelsCount = 0
		}
		out = append(out, v)
	}
	return out, nil
}

// ---- list ---------------------------------------------------------------------------

// UpstreamQuery are the list filters of GET /admin/llm/upstreams.
type UpstreamQuery struct {
	Q          string
	Status     string
	Compatible string
	Sort       string
	Page       int
	PageSize   int
}

func escapeLike(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return r.Replace(strings.ToLower(s))
}

func (s *Service) upstreamFilter(ctx context.Context, q UpstreamQuery, withStatus bool) *gorm.DB {
	db := s.db.WithContext(ctx).Model(&Upstream{})
	if t := strings.TrimSpace(q.Q); t != "" {
		like := "%" + escapeLike(t) + "%"
		db = db.Where(`LOWER(name) LIKE ? ESCAPE '\' OR LOWER(base_url) LIKE ? ESCAPE '\'`, like, like)
	}
	if withStatus && (q.Status == StatusActive || q.Status == StatusInactive) {
		db = db.Where("status = ?", q.Status)
	}
	if c := strings.TrimSpace(q.Compatible); c != "" {
		db = db.Where("compatible = ?", c)
	}
	return db
}

func upstreamOrder(sort string) string {
	switch sort {
	case "id_asc":
		return "id ASC"
	case "name_asc":
		return "name ASC, id DESC"
	case "updated_desc":
		return "updated_at DESC, id DESC"
	}
	return "id DESC"
}

// ListUpstreams lists upstreams with filters and paging.
func (s *Service) ListUpstreams(ctx context.Context, q UpstreamQuery) ([]UpstreamView, int64, error) {
	if q.Status == "circuit" {
		return s.listCircuitUpstreams(ctx, q)
	}
	db := s.upstreamFilter(ctx, q, true)
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var ups []Upstream
	err := s.upstreamFilter(ctx, q, true).Order(upstreamOrder(q.Sort)).Offset(pageOffset(q.Page, q.PageSize)).Limit(q.PageSize).Find(&ups).Error
	if err != nil {
		return nil, 0, err
	}
	views, err := s.upstreamViews(ctx, ups)
	return views, total, err
}

func (s *Service) listCircuitUpstreams(ctx context.Context, q UpstreamQuery) ([]UpstreamView, int64, error) {
	if !s.BreakerEnabled(ctx) {
		return nil, 0, nil
	}
	var ups []Upstream
	if err := s.upstreamFilter(ctx, q, false).Order("id DESC").Limit(5000).Find(&ups).Error; err != nil {
		return nil, 0, err
	}
	var open []Upstream
	for _, up := range ups {
		if isOpen, _, err := s.circuits.peekOpen(ctx, upstreamPrefix(up.ID)); err == nil && isOpen {
			open = append(open, up)
		}
	}
	total := int64(len(open))
	from, to := pageWindow(q.Page, q.PageSize, len(open))
	views, err := s.upstreamViews(ctx, open[from:to])
	return views, total, err
}

// GetUpstream returns one upstream's view.
func (s *Service) GetUpstream(ctx context.Context, id uint) (*UpstreamView, error) {
	up, err := s.loadUpstream(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	views, err := s.upstreamViews(ctx, []Upstream{*up})
	if err != nil {
		return nil, err
	}
	return &views[0], nil
}

func (s *Service) loadUpstream(ctx context.Context, db *gorm.DB, id uint) (*Upstream, error) {
	var up Upstream
	if err := db.WithContext(ctx).First(&up, id).Error; err != nil {
		if isNotFound(err) {
			return nil, ErrUpstreamNotFound
		}
		return nil, err
	}
	return &up, nil
}

// ---- create / update -------------------------------------------------------------------

func normalizeCompatible(raw string) (string, error) {
	c := lower(raw)
	if c == "" {
		return StyleOpenAI, nil
	}
	if !validStyle(c) {
		return "", ErrInvalidCompatible
	}
	return c, nil
}

// CreateUpstream creates an upstream.
func (s *Service) CreateUpstream(ctx context.Context, req createUpstreamReq) (*UpstreamView, error) {
	baseURL, err := validateUpstreamBaseURL(req.BaseURL)
	if err != nil {
		return nil, err
	}
	style, err := normalizeCompatible(req.Compatible)
	if err != nil {
		return nil, err
	}
	var defaults string
	if strings.TrimSpace(req.ProtocolDefaultsJSON) == "" {
		defaults = fallbackDefaultsJSON(style)
	} else {
		d, err := parseProtocolDefaultsStrict(req.ProtocolDefaultsJSON)
		if err != nil {
			return nil, err
		}
		defaults = d.json()
	}
	doc, err := parseAndValidateKeys(req.APIKeys)
	if err != nil {
		return nil, err
	}
	if err := validateHeadersJSON(req.HeadersJSON); err != nil {
		return nil, err
	}
	enc, err := s.encryptKeys(doc)
	if err != nil {
		return nil, err
	}
	status := normalizeStatus(req.Status)
	if !validWritableStatus(status) {
		return nil, badRequest(apperr.ErrInvalidBody)
	}
	up := Upstream{
		Name: strings.TrimSpace(req.Name), BaseURL: baseURL, Compatible: style, ProtocolDefaultsJSON: defaults,
		Status: status, ConnectTimeoutMS: timeoutOrDefault(req.ConnectTimeoutMS, defaultConnectTimeoutMS),
		ReadTimeoutMS:       timeoutOrDefault(req.ReadTimeoutMS, defaultReadTimeoutMS),
		StreamIdleTimeoutMS: timeoutOrDefault(req.StreamIdleTimeoutMS, defaultStreamIdleMS),
		APIKeysEnc:          enc, CBFailureThreshold: req.CBFailureThreshold, CBModelThreshold: req.CBModelThreshold,
		CBThresholdLogic: normalizeThresholdLogic(req.CBThresholdLogic), CBDurationMin: req.CBDurationMin,
		CBWindowMin: req.CBWindowMin, HeadersJSON: req.HeadersJSON,
	}
	if err := s.db.WithContext(ctx).Create(&up).Error; err != nil {
		return nil, database.Translate(err)
	}
	s.invalidateCatalog()
	return s.GetUpstream(ctx, up.ID)
}

// UpdateUpstream applies a partial update.
func (s *Service) UpdateUpstream(ctx context.Context, id uint, req updateUpstreamReq) (*UpstreamView, error) {
	up, err := s.loadUpstream(ctx, s.db, id)
	if err != nil {
		return nil, err
	}
	statusChanged := false
	if req.Name != nil {
		n := strings.TrimSpace(*req.Name)
		if runeLen(n) < 2 || runeLen(n) > 128 {
			return nil, badRequest(apperr.ErrInvalidBody)
		}
		up.Name = n
	}
	if req.BaseURL != nil {
		u, err := validateUpstreamBaseURL(*req.BaseURL)
		if err != nil {
			return nil, err
		}
		up.BaseURL = u
	}
	styleChanged := false
	if req.Compatible != nil {
		style, err := normalizeCompatible(*req.Compatible)
		if err != nil {
			return nil, err
		}
		styleChanged = true
		up.Compatible = style
	}
	if req.ProtocolDefaultsJSON != nil {
		if strings.TrimSpace(*req.ProtocolDefaultsJSON) == "" {
			if styleChanged {
				up.ProtocolDefaultsJSON = fallbackDefaultsJSON(up.Compatible)
			} else {
				up.ProtocolDefaultsJSON = "{}"
			}
		} else {
			d, err := parseProtocolDefaultsStrict(*req.ProtocolDefaultsJSON)
			if err != nil {
				return nil, err
			}
			up.ProtocolDefaultsJSON = d.json()
		}
	}
	if req.APIKeys != nil {
		doc, err := parseAndValidateKeys(*req.APIKeys)
		if err != nil {
			return nil, err
		}
		if up.APIKeysEnc, err = s.encryptKeys(doc); err != nil {
			return nil, err
		}
	} else if req.AddAPIKeys != nil || len(req.DeleteAPIKeyIDs) > 0 {
		doc, err := s.decryptKeys(up)
		if err != nil {
			return nil, ErrInvalidAPIKeys
		}
		var added []keyEntry
		if req.AddAPIKeys != nil {
			addDoc, err := parseKeyDoc(*req.AddAPIKeys)
			if err != nil || len(addDoc.Keys) == 0 {
				return nil, ErrInvalidAPIKeys
			}
			added = addDoc.Keys
		}
		if err := doc.applyEdit(s.keyHash, req.DeleteAPIKeyIDs, added); err != nil {
			return nil, err
		}
		if up.APIKeysEnc, err = s.encryptKeys(doc); err != nil {
			return nil, err
		}
	}
	if req.Status != nil {
		st := normalizeStatus(*req.Status)
		if !validWritableStatus(st) {
			return nil, badRequest(apperr.ErrInvalidBody)
		}
		statusChanged = st != up.Status
		up.Status = st
	}
	if req.ConnectTimeoutMS != nil {
		up.ConnectTimeoutMS = timeoutOrDefault(*req.ConnectTimeoutMS, defaultConnectTimeoutMS)
	}
	if req.ReadTimeoutMS != nil {
		up.ReadTimeoutMS = timeoutOrDefault(*req.ReadTimeoutMS, defaultReadTimeoutMS)
	}
	if req.StreamIdleTimeoutMS != nil {
		up.StreamIdleTimeoutMS = timeoutOrDefault(*req.StreamIdleTimeoutMS, defaultStreamIdleMS)
	}
	if req.CBFailureThreshold != nil {
		up.CBFailureThreshold = *req.CBFailureThreshold
	}
	if req.CBModelThreshold != nil {
		up.CBModelThreshold = *req.CBModelThreshold
	}
	if req.CBThresholdLogic != nil {
		up.CBThresholdLogic = normalizeThresholdLogic(*req.CBThresholdLogic)
	}
	if req.CBDurationMin != nil {
		up.CBDurationMin = *req.CBDurationMin
	}
	if req.CBWindowMin != nil {
		up.CBWindowMin = *req.CBWindowMin
	}
	if req.HeadersJSON != nil {
		if err := validateHeadersJSON(*req.HeadersJSON); err != nil {
			return nil, err
		}
		up.HeadersJSON = *req.HeadersJSON
	}
	if err := s.db.WithContext(ctx).Save(up).Error; err != nil {
		return nil, database.Translate(err)
	}
	if statusChanged {
		s.invalidateCatalog()
	}
	return s.GetUpstream(ctx, id)
}

// ---- delete ------------------------------------------------------------------------------

// DeleteUpstream removes an upstream with its upstream models, their routes and
// the access rules that point at it. Platform models stay.
func (s *Service) DeleteUpstream(ctx context.Context, id uint) error {
	var codes []string
	err := s.tx(ctx, func(tx *gorm.DB) error {
		var up Upstream
		if err := tx.First(&up, id).Error; err != nil {
			if isNotFound(err) {
				return ErrUpstreamNotFound
			}
			return err
		}
		var ums []UpstreamModel
		if err := tx.Where("upstream_id = ?", id).Find(&ums).Error; err != nil {
			return err
		}
		umIDs := make([]uint, 0, len(ums))
		for _, um := range ums {
			umIDs = append(umIDs, um.ID)
			codes = append(codes, um.BindingCode)
		}
		if len(umIDs) > 0 {
			if err := tx.Where("upstream_model_id IN ?", umIDs).Delete(&ModelRoute{}).Error; err != nil {
				return err
			}
			if err := tx.Where("upstream_id = ?", id).Delete(&UpstreamModel{}).Error; err != nil {
				return err
			}
		}
		if err := tx.Where("rule_type = ? AND value = ?", RuleUpstream, strconv.FormatUint(uint64(id), 10)).Delete(&PermissionGroupModelRule{}).Error; err != nil {
			return err
		}
		return tx.Delete(&Upstream{}, id).Error
	})
	if err != nil {
		return err
	}
	s.circuits.forgetCounter(id)
	s.clearUpstreamState(ctx, id, codes)
	s.invalidateCatalog()
	return nil
}

func (s *Service) clearUpstreamState(ctx context.Context, id uint, codes []string) {
	if err := s.circuits.reset(ctx, upstreamPrefix(id), true); err != nil {
		s.log.Warn("llm_circuit_cleanup_failed", logx.Err(err))
	}
	for _, c := range codes {
		_ = s.circuits.reset(ctx, bindingPrefix(id, c), false)
	}
}

// BatchDeleteUpstreams deletes sequentially and independently.
func (s *Service) BatchDeleteUpstreams(ctx context.Context, ids []uint) BatchResult {
	return s.batchDelete(ids, func(id uint) error { return s.DeleteUpstream(ctx, id) }, ErrUpstreamNotFound)
}

func (s *Service) batchDelete(ids []uint, del func(uint) error, notFound error) BatchResult {
	res := BatchResult{Total: len(ids), Results: []BatchItem{}}
	for _, id := range ids {
		err := del(id)
		switch {
		case err == nil:
			res.SuccessCount++
			res.Results = append(res.Results, BatchItem{ID: id, Status: "deleted"})
		case errors.Is(err, notFound):
			res.NotFoundCount++
			res.Results = append(res.Results, BatchItem{ID: id, Status: "not_found"})
		default:
			res.FailedCount++
			msg := "batch delete failed"
			if te, ok := apperr.Find(err); ok && apperr.Registered(te.Code) && te.Message != "" {
				msg = te.Message
			}
			res.Results = append(res.Results, BatchItem{ID: id, Status: "failed", Error: msg})
		}
	}
	return res
}

// ---- manual circuit control ---------------------------------------------------------------

// OpenUpstreamCircuit opens an upstream circuit for 24 hours.
func (s *Service) OpenUpstreamCircuit(ctx context.Context, id uint) error {
	if _, err := s.loadUpstream(ctx, s.db, id); err != nil {
		return err
	}
	if !s.BreakerEnabled(ctx) {
		return ErrBreakerDisabled
	}
	return s.circuits.openManual(ctx, upstreamPrefix(id))
}

// ResetUpstreamCircuit clears an upstream's circuit and counters.
func (s *Service) ResetUpstreamCircuit(ctx context.Context, id uint) error {
	if _, err := s.loadUpstream(ctx, s.db, id); err != nil {
		return err
	}
	return s.circuits.reset(ctx, upstreamPrefix(id), true)
}

// sortedUpstreamIDs is a small helper for deterministic iteration.
func sortedUpstreamIDs(m map[uint]*Upstream) []uint {
	ids := make([]uint, 0, len(m))
	for id := range m {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}

var _ = time.Second
