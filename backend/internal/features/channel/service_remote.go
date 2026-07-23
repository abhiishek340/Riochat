package channel

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// PreviewItem is one remote model in a preview.
type PreviewItem struct {
	UpstreamModelName          string   `json:"upstreamModelName"`
	SuggestedPlatformModelName string   `json:"suggestedPlatformModelName"`
	SuggestedKindsJSON         string   `json:"suggestedKindsJSON"`
	SuggestedProtocol          string   `json:"suggestedProtocol"`
	SuggestedProtocols         []string `json:"suggestedProtocols"`
	BindingCode                string   `json:"bindingCode"`
	BoundPlatformModels        []string `json:"boundPlatformModels"`
	UpstreamModelStatus        string   `json:"upstreamModelStatus"`
	AlreadySynced              bool     `json:"alreadySynced"`
	AlreadyBound               bool     `json:"alreadyBound"`
}

// SyncPlan groups remote models by what a sync would do.
type SyncPlan struct {
	AddedModels              []string `json:"addedModels"`
	UpdatedModels            []string `json:"updatedModels"`
	ReactivatedModels        []string `json:"reactivatedModels"`
	InactivatedModels        []string `json:"inactivatedModels"`
	UnchangedModels          []string `json:"unchangedModels"`
	ProtectedModels          []string `json:"protectedModels"`
	UnresolvedProtocolModels []string `json:"unresolvedProtocolModels"`
}

// RemotePreview is the response of GET .../models/remote.
type RemotePreview struct {
	Total      int           `json:"total"`
	Items      []PreviewItem `json:"items"`
	SnapshotID string        `json:"snapshotID"`
	SyncPlan   SyncPlan      `json:"syncPlan"`
}

// SyncedModel is one remote model in a sync result.
type SyncedModel struct {
	UpstreamModelName string `json:"upstreamModelName"`
	BindingCode       string `json:"bindingCode"`
	SuggestedProtocol string `json:"suggestedProtocol"`
	KindsJSON         string `json:"kindsJSON"`
	Status            string `json:"status"`
	Created           bool   `json:"created"`
	Updated           bool   `json:"updated"`
	Reactivated       bool   `json:"reactivated"`
	Protected         bool   `json:"protected"`
}

// SyncResult is the response of POST .../models/sync.
type SyncResult struct {
	SnapshotID               string        `json:"snapshotID"`
	TotalUpstream            int           `json:"totalUpstream"`
	CreatedUpstreamModels    int           `json:"createdUpstreamModels"`
	UpdatedUpstreamModels    int           `json:"updatedUpstreamModels"`
	UnchangedUpstreamModels  int           `json:"unchangedUpstreamModels"`
	ProtectedUpstreamModels  int           `json:"protectedUpstreamModels"`
	ExistingUpstreamModels   int           `json:"existingUpstreamModels"`
	SkippedUpstreamModels    int           `json:"skippedUpstreamModels"`
	InactivatedModels        int64         `json:"inactivatedModels"`
	ReactivatedModels        int           `json:"reactivatedModels"`
	SyncedModels             []SyncedModel `json:"syncedModels"`
	UnresolvedProtocolModels []string      `json:"unresolvedProtocolModels"`
}

type remoteEntry struct {
	RemoteModel
	kinds     []string
	protocols []string
	vendor    string
	icon      string
}

// normalizeRemote trims ids, drops blanks, keeps the first of duplicates and sorts by id.
func normalizeRemote(in []RemoteModel) []RemoteModel {
	seen := map[string]bool{}
	var out []RemoteModel
	for _, m := range in {
		id := strings.TrimSpace(m.ID)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, RemoteModel{ID: id, OwnedBy: strings.TrimSpace(m.OwnedBy)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

func snapshotID(list []RemoteModel) string {
	h := sha256.New()
	for _, m := range list {
		h.Write([]byte(m.ID))
		h.Write([]byte{0})
		h.Write([]byte(m.OwnedBy))
		h.Write([]byte{'\n'})
	}
	return hex.EncodeToString(h.Sum(nil))
}

// fetchRemote asks the upstream for its model list.
func (s *Service) fetchRemote(ctx context.Context, up *Upstream) ([]RemoteModel, error) {
	doc, err := s.decryptKeys(up)
	if err != nil || len(doc.activeKeys()) == 0 {
		return nil, ErrNoActiveKey
	}
	defs := parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON)
	protocol, err := resolveProtocol(up.Compatible, defs, []string{KindChat}, "")
	if err != nil {
		return nil, ErrProtocolRequired
	}
	if s.opts.Client == nil {
		return nil, ErrRemoteUnavailable
	}
	if _, err := validateUpstreamBaseURL(up.BaseURL); err != nil {
		return nil, err
	}
	key, _ := doc.pickKey(func() int64 { return s.circuits.nextKeyCounter(ctx, up.ID) })
	app, web := s.appAttribution()
	call := UpstreamCall{
		Protocol: protocol, BaseURL: strings.TrimSpace(up.BaseURL), APIKey: key, HeadersJSON: mergeHeaders(up.HeadersJSON, ""),
		ConnectTimeout: msDuration(up.ConnectTimeoutMS, defaultConnectTimeoutMS),
		ReadTimeout:    msDuration(up.ReadTimeoutMS, defaultReadTimeoutMS),
		AppName:        app, WebBaseURL: web,
	}
	list, err := s.opts.Client.ListModels(ctx, call)
	if err != nil {
		s.log.Warn("llm_remote_models_failed", logx.F("upstream", up.ID), logx.Err(err))
		return nil, ErrRemoteUnavailable.WithCause(err)
	}
	return normalizeRemote(list), nil
}

type remotePlan struct {
	entries    []remoteEntry
	local      map[string]UpstreamModel
	added      []remoteEntry
	updated    []remoteEntry
	react      []remoteEntry
	unchanged  []remoteEntry
	protected  []remoteEntry
	inact      []UpstreamModel
	unresolved []string
}

func (s *Service) planSync(ctx context.Context, up *Upstream, list []RemoteModel) (*remotePlan, error) {
	defs := parseProtocolDefaultsLenient(up.ProtocolDefaultsJSON)
	p := &remotePlan{local: map[string]UpstreamModel{}}
	var locals []UpstreamModel
	if err := s.db.WithContext(ctx).Where("upstream_id = ?", up.ID).Find(&locals).Error; err != nil {
		return nil, err
	}
	for _, l := range locals {
		p.local[l.UpstreamModelName] = l
	}
	remoteNames := map[string]bool{}
	for _, m := range list {
		kinds := inferKinds(m.ID)
		protos, err := suggestedProtocols(up.Compatible, defs, kinds)
		if err != nil {
			return nil, err
		}
		vendor := deriveVendor(m.OwnedBy, m.ID, up.Name, up.BaseURL)
		e := remoteEntry{RemoteModel: m, kinds: kinds, protocols: protos, vendor: vendor, icon: autoIcon("", m.ID, vendor)}
		p.entries = append(p.entries, e)
		remoteNames[m.ID] = true
		local, exists := p.local[m.ID]
		switch {
		case exists && local.Source != SourceSync && local.Source != SourceImport:
			p.protected = append(p.protected, e)
		case exists && local.Status != StatusActive:
			p.react = append(p.react, e)
			if len(protos) == 0 {
				p.unresolved = append(p.unresolved, m.ID)
			}
		case exists:
			first := ""
			if len(protos) > 0 {
				first = protos[0]
			}
			if local.Vendor != vendor || local.Icon != e.icon || local.SuggestedProtocol != first || kindsJSON(kinds) != local.KindsJSON {
				p.updated = append(p.updated, e)
			} else {
				p.unchanged = append(p.unchanged, e)
			}
			if len(protos) == 0 {
				p.unresolved = append(p.unresolved, m.ID)
			}
		default:
			p.added = append(p.added, e)
			if len(protos) == 0 {
				p.unresolved = append(p.unresolved, m.ID)
			}
		}
	}
	for _, l := range locals {
		if (l.Source == SourceSync || l.Source == SourceImport) && l.Status == StatusActive && !remoteNames[l.UpstreamModelName] {
			p.inact = append(p.inact, l)
		}
	}
	return p, nil
}

func names(es []remoteEntry) []string {
	out := []string{}
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func firstOr(list []string, def string) string {
	if len(list) > 0 {
		return list[0]
	}
	return def
}

// PreviewRemote asks the upstream for its models without writing anything.
func (s *Service) PreviewRemote(ctx context.Context, upstreamID uint) (*RemotePreview, error) {
	up, err := s.loadUpstream(ctx, s.db, upstreamID)
	if err != nil {
		return nil, err
	}
	list, err := s.fetchRemote(ctx, up)
	if err != nil {
		return nil, err
	}
	plan, err := s.planSync(ctx, up, list)
	if err != nil {
		return nil, err
	}
	// platform models bound to the local upstream models
	bound := map[uint][]string{}
	if len(plan.local) > 0 {
		ids := make([]uint, 0, len(plan.local))
		for _, l := range plan.local {
			ids = append(ids, l.ID)
		}
		var rows []struct {
			UMID uint   `gorm:"column:um_id"`
			Name string `gorm:"column:name"`
		}
		err := s.db.WithContext(ctx).Table("llm_model_routes AS r").
			Select("DISTINCT r.upstream_model_id AS um_id, pm.name AS name").
			Joins("JOIN llm_platform_models pm ON pm.id = r.platform_model_id").
			Where("r.upstream_model_id IN ?", ids).Order("pm.name ASC").Scan(&rows).Error
		if err != nil {
			return nil, err
		}
		for _, r := range rows {
			bound[r.UMID] = append(bound[r.UMID], r.Name)
		}
	}
	pv := &RemotePreview{Total: len(list), Items: []PreviewItem{}, SnapshotID: snapshotID(list)}
	for _, e := range plan.entries {
		local, exists := plan.local[e.ID]
		item := PreviewItem{
			UpstreamModelName: e.ID, SuggestedPlatformModelName: e.ID, SuggestedKindsJSON: kindsJSON(e.kinds),
			SuggestedProtocol: firstOr(e.protocols, ""), SuggestedProtocols: nonNilStrings(e.protocols),
			BoundPlatformModels: []string{}, AlreadySynced: exists,
		}
		if exists {
			item.BindingCode, item.UpstreamModelStatus = local.BindingCode, local.Status
			if b := bound[local.ID]; len(b) > 0 {
				item.BoundPlatformModels, item.AlreadyBound = b, true
			}
		}
		pv.Items = append(pv.Items, item)
	}
	inact := []string{}
	for _, l := range plan.inact {
		inact = append(inact, l.UpstreamModelName)
	}
	pv.SyncPlan = SyncPlan{
		AddedModels: names(plan.added), UpdatedModels: names(plan.updated), ReactivatedModels: names(plan.react),
		InactivatedModels: inact, UnchangedModels: names(plan.unchanged), ProtectedModels: names(plan.protected),
		UnresolvedProtocolModels: nonNilStrings(plan.unresolved),
	}
	return pv, nil
}

// SyncRemote reconciles the upstream's local model catalog with its remote list.
func (s *Service) SyncRemote(ctx context.Context, upstreamID uint, allowEmpty bool, expectedSnapshot string) (*SyncResult, error) {
	up, err := s.loadUpstream(ctx, s.db, upstreamID)
	if err != nil {
		return nil, err
	}
	list, err := s.fetchRemote(ctx, up)
	if err != nil {
		return nil, err
	}
	snap := snapshotID(list)
	if expectedSnapshot != "" && expectedSnapshot != snap {
		return nil, ErrRemoteSnapshotChanged
	}
	if len(list) == 0 && !allowEmpty {
		return nil, ErrRemoteEmpty
	}
	plan, err := s.planSync(ctx, up, list)
	if err != nil {
		return nil, err
	}
	now := s.now()
	res := &SyncResult{
		SnapshotID: snap, TotalUpstream: len(list), SyncedModels: []SyncedModel{},
		UnresolvedProtocolModels: nonNilStrings(plan.unresolved),
		UpdatedUpstreamModels:    len(plan.updated), UnchangedUpstreamModels: len(plan.unchanged),
		ProtectedUpstreamModels: len(plan.protected), ReactivatedModels: len(plan.react), CreatedUpstreamModels: len(plan.added),
		InactivatedModels: int64(len(plan.inact)),
	}
	created := map[string]string{} // name -> binding code
	err = s.tx(ctx, func(tx *gorm.DB) error {
		for _, e := range plan.added {
			code, err := newBindingCode()
			if err != nil {
				return err
			}
			row := UpstreamModel{UpstreamID: up.ID, BindingCode: code, UpstreamModelName: e.ID, Vendor: e.vendor, Icon: e.icon,
				SuggestedProtocol: firstOr(e.protocols, ""), KindsJSON: kindsJSON(e.kinds), Status: StatusActive,
				Source: SourceSync, LastSyncedAt: &now, RawJSON: rawMetadata(e.RemoteModel)}
			if err := tx.Create(&row).Error; err != nil {
				if database.IsDuplicate(err) {
					return ErrRemoteSnapshotChanged
				}
				return err
			}
			created[e.ID] = code
		}
		for _, group := range [][]remoteEntry{plan.updated, plan.react} {
			for _, e := range group {
				local := plan.local[e.ID]
				if err := tx.Model(&UpstreamModel{}).Where("id = ?", local.ID).Updates(map[string]any{
					"vendor": e.vendor, "icon": e.icon, "suggested_protocol": firstOr(e.protocols, ""),
					"kinds_json": kindsJSON(e.kinds), "status": StatusActive, "last_synced_at": now,
					"raw_json": rawMetadata(e.RemoteModel)}).Error; err != nil {
					return err
				}
			}
		}
		for _, l := range plan.inact {
			if err := tx.Model(&UpstreamModel{}).Where("id = ?", l.ID).Update("status", StatusInactive).Error; err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	res.ExistingUpstreamModels = res.TotalUpstream - res.CreatedUpstreamModels
	flag := func(group []remoteEntry) map[string]bool {
		m := map[string]bool{}
		for _, e := range group {
			m[e.ID] = true
		}
		return m
	}
	upd, rea, pro := flag(plan.updated), flag(plan.react), flag(plan.protected)
	for _, e := range plan.entries {
		sm := SyncedModel{UpstreamModelName: e.ID, SuggestedProtocol: firstOr(e.protocols, ""), KindsJSON: kindsJSON(e.kinds),
			Status: StatusActive, Updated: upd[e.ID], Reactivated: rea[e.ID], Protected: pro[e.ID]}
		if code, ok := created[e.ID]; ok {
			sm.Created, sm.BindingCode = true, code
		} else if l, ok := plan.local[e.ID]; ok {
			sm.BindingCode, sm.Status = l.BindingCode, l.Status
			if rea[e.ID] {
				sm.Status = StatusActive
			}
			if pro[e.ID] {
				sm.SuggestedProtocol, sm.KindsJSON = l.SuggestedProtocol, l.KindsJSON
			}
		}
		res.SyncedModels = append(res.SyncedModels, sm)
	}
	s.invalidateCatalog()
	return res, nil
}

func rawMetadata(m RemoteModel) string {
	return jsonObject(map[string]string{"id": m.ID, "owned_by": m.OwnedBy})
}

// ---- import ---------------------------------------------------------------------------------

// ImportItemResult is one item of an import.
type ImportItemResult struct {
	UpstreamModelName string   `json:"upstreamModelName"`
	PlatformModelName string   `json:"platformModelName"`
	BindingCode       string   `json:"bindingCode"`
	Status            string   `json:"status"`
	CreatedRoute      bool     `json:"createdRoute"`
	CreatedRoutes     int      `json:"createdRoutes"`
	ExistingRoutes    int      `json:"existingRoutes"`
	Protocols         []string `json:"protocols"`
	CreatedPlatform   bool     `json:"createdPlatform"`
	Error             string   `json:"error,omitempty"`
}

// ImportResult is the response of POST .../models/import.
type ImportResult struct {
	Total           int                `json:"total"`
	ImportedCount   int                `json:"importedCount"`
	FailedCount     int                `json:"failedCount"`
	CreatedRoutes   int                `json:"createdRoutes"`
	ExistingRoutes  int                `json:"existingRoutes"`
	CreatedPlatform int                `json:"createdPlatform"`
	Results         []ImportItemResult `json:"results"`
}

// ImportModels turns remote models into platform models and routes. Items are
// processed one by one; a failing item does not stop the others.
func (s *Service) ImportModels(ctx context.Context, upstreamID uint, req importReq) (*ImportResult, error) {
	if _, err := s.loadUpstream(ctx, s.db, upstreamID); err != nil {
		return nil, err
	}
	groupIDs := uniqueUints(req.PermissionGroupIDs)
	if len(groupIDs) > 0 {
		for _, id := range groupIDs {
			if id == 0 {
				return nil, ErrInvalidPermGroups
			}
		}
		var n int64
		if err := s.db.WithContext(ctx).Model(&PermissionGroup{}).Where("id IN ?", groupIDs).Count(&n).Error; err != nil {
			return nil, err
		}
		if int(n) != len(groupIDs) {
			return nil, ErrInvalidPermGroups
		}
	}
	res := &ImportResult{Total: len(req.Items), Results: []ImportItemResult{}}
	for _, it := range req.Items {
		item := ImportItemResult{UpstreamModelName: strings.TrimSpace(it.UpstreamModelName), PlatformModelName: strings.TrimSpace(it.PlatformModelName), Protocols: []string{}}
		out, err := s.importOne(ctx, upstreamID, it)
		if err != nil {
			item.Status, item.Error = "failed", publicMessage(err)
			res.FailedCount++
			res.Results = append(res.Results, item)
			continue
		}
		item.BindingCode, item.CreatedRoutes, item.ExistingRoutes = out.bindingCode, out.createdRoutes, out.existingRoutes
		item.Protocols, item.CreatedPlatform, item.CreatedRoute = out.protocols, out.platformCreated, out.createdRoutes > 0
		item.PlatformModelName = out.row.PlatformModelName
		item.Status = "existing"
		if out.createdRoutes > 0 {
			item.Status = "created"
		}
		if len(groupIDs) > 0 {
			if err := s.addManualGroups(ctx, out.platformID, groupIDs); err != nil {
				return nil, err
			}
		}
		res.ImportedCount++
		res.CreatedRoutes += out.createdRoutes
		res.ExistingRoutes += out.existingRoutes
		if out.platformCreated {
			res.CreatedPlatform++
		}
		res.Results = append(res.Results, item)
	}
	s.invalidateCatalog()
	return res, nil
}

func (s *Service) importOne(ctx context.Context, upstreamID uint, it importItemReq) (*upsertResult, error) {
	if strings.TrimSpace(it.UpstreamModelName) == "" {
		return nil, ErrUpstreamModelNotFound
	}
	protocols := it.Protocols
	if len(protocols) == 0 && strings.TrimSpace(it.Protocol) != "" {
		protocols = []string{it.Protocol}
	}
	if protocols == nil {
		protocols = []string{}
	}
	status := normalizeStatus(it.Status)
	req := upsertBindingReq{
		PlatformModelName: it.PlatformModelName, UpstreamModelName: it.UpstreamModelName, Protocols: protocols,
		KindsJSON: it.KindsJSON, Status: &status, Priority: ptr(atLeastOne(it.Priority)), Weight: ptr(1),
	}
	return s.upsertBinding(ctx, upstreamID, req, upsertOptions{routeSource: SourceImport, catalogSource: SourceSync})
}

// addManualGroups unions the requested groups into a model's manual groups.
func (s *Service) addManualGroups(ctx context.Context, modelID uint, groupIDs []uint) error {
	existing, err := s.ManualModelGroups(ctx, modelID)
	if err != nil {
		return err
	}
	union := append(append([]uint(nil), existing...), groupIDs...)
	return s.SetModelGroups(ctx, modelID, uniqueUints(union))
}

// publicMessage is the client-safe message of an error.
func publicMessage(err error) string {
	if te, ok := apperr.Find(err); ok && apperr.Registered(te.Code) && te.Message != "" {
		return te.Message
	}
	return "operation failed"
}
