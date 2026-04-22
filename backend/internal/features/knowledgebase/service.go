package knowledgebase

import (
	"context"
	"errors"
	"strconv"
	"strings"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/clock"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// Service implements the knowledge base rules. It is safe for concurrent use.
// The exported methods at the bottom of integration.go are the contract other
// areas (conversation, account deletion, file deletion, retrieval) call.
type Service struct {
	db    *gorm.DB
	clock clock.Clock
	log   *logx.Logger
	ports Ports
}

// NewService builds a service. Nil ports members get their defaults.
func NewService(db *gorm.DB, clk clock.Clock, log *logx.Logger, ports Ports) *Service {
	if clk == nil {
		clk = clock.System{}
	}
	if log == nil {
		log = logx.Nop()
	}
	return &Service{db: db, clock: clk, log: log, ports: ports.withDefaults(db, log)}
}

// ----- access -----

type accessKind int

const (
	// accessVisible: enabled builtin bases plus the caller's own bases.
	accessVisible accessKind = iota
	// accessPersonal: only the caller's own user-scope bases.
	accessPersonal
	// accessAdmin: only builtin bases, enabled or not.
	accessAdmin
)

type access struct {
	kind   accessKind
	userID uint
}

func visibleTo(uid uint) access   { return access{accessVisible, uid} }
func personalOf(uid uint) access  { return access{accessPersonal, uid} }
func adminAccess(uid uint) access { return access{accessAdmin, uid} }

// fileOwner is the owner id of the files a base of this access kind may hold.
func (a access) fileOwner() uint {
	if a.kind == accessAdmin {
		return 0
	}
	return a.userID
}

func (a access) apply(q *gorm.DB) *gorm.DB {
	switch a.kind {
	case accessPersonal:
		return q.Where("scope = ? AND owner_user_id = ?", ScopeUser, a.userID)
	case accessAdmin:
		return q.Where("scope = ?", ScopeBuiltin)
	default:
		return q.Where("enabled = ? AND (scope = ? OR (scope = ? AND owner_user_id = ?))",
			true, ScopeBuiltin, ScopeUser, a.userID)
	}
}

// translate maps storage conditions onto this area's errors.
func translate(err error) error {
	switch {
	case err == nil:
		return nil
	case database.IsNotFound(err):
		return ErrNotFound.WithCause(err)
	case database.IsDuplicate(err):
		return ErrConflict.WithCause(err)
	case errors.Is(err, gorm.ErrInvalidData), errors.Is(err, gorm.ErrInvalidValue),
		errors.Is(err, gorm.ErrInvalidField):
		return ErrInvalid.WithCause(err)
	}
	return err
}

// mapStorage is translate for errors of unknown origin: an error that already
// carries one of this area's codes (including those handed back by the file
// and embedding collaborators) is left alone, so its code survives even when
// a storage error sits in its cause chain.
func mapStorage(err error) error {
	if te, ok := apperr.Find(err); ok {
		if _, known := statusByCode[te.Code]; known {
			return err
		}
	}
	return translate(err)
}

// find loads one base by public id under the access rule. A base the caller
// may not see looks exactly like a missing one.
func (s *Service) find(ctx context.Context, db *gorm.DB, a access, publicID string, lock bool) (*KnowledgeBase, error) {
	pid := strings.TrimSpace(publicID)
	if pid == "" {
		return nil, ErrInvalid
	}
	if a.kind != accessAdmin && a.userID == 0 {
		return nil, ErrInvalid
	}
	q := db.WithContext(ctx)
	if lock {
		q = q.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var kb KnowledgeBase
	if err := a.apply(q).Where("public_id = ?", pid).Take(&kb).Error; err != nil {
		return nil, translate(err)
	}
	return &kb, nil
}

// ----- counts and views -----

// countsFor aggregates the live counts of the given bases.
func (s *Service) countsFor(ctx context.Context, ids []uint) (map[uint]counts, error) {
	out := make(map[uint]counts, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	type row struct {
		KBID            uint
		FileCount       int64
		ReadyCount      int64
		ProcessingCount int64
	}
	var rows []row
	err := s.db.WithContext(ctx).Table("knowledge_base_files AS kbf").
		Select(`kbf.knowledge_base_id AS kb_id, COUNT(*) AS file_count,
			SUM(CASE WHEN fo.processing_ready = ? AND fo.embed_status = 'ready' AND fo.rag_opt_out = ? AND fo.chunk_count > 0 THEN 1 ELSE 0 END) AS ready_count,
			SUM(CASE WHEN fo.processing_status IN ('uploaded','queued','extracting','embedding') OR fo.extract_status = 'processing' OR fo.embed_status = 'processing' THEN 1 ELSE 0 END) AS processing_count`,
			true, false).
		Joins("JOIN file_objects fo ON fo.id = kbf.file_object_id").
		Where("fo.status = ? AND fo.deleted_at IS NULL", fileStatusActive).
		Where("kbf.knowledge_base_id IN ?", ids).
		Group("kbf.knowledge_base_id").
		Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.KBID] = counts{files: r.FileCount, ready: r.ReadyCount, processing: r.ProcessingCount}
	}
	return out, nil
}

func (s *Service) viewsFor(ctx context.Context, rows []KnowledgeBase) ([]KnowledgeBaseView, error) {
	ids := make([]uint, len(rows))
	for i := range rows {
		ids[i] = rows[i].ID
	}
	cs, err := s.countsFor(ctx, ids)
	if err != nil {
		return nil, err
	}
	out := make([]KnowledgeBaseView, len(rows))
	for i := range rows {
		out[i] = toView(&rows[i], cs[rows[i].ID])
	}
	return out, nil
}

func (s *Service) viewOf(ctx context.Context, kb *KnowledgeBase) (*KnowledgeBaseView, error) {
	vs, err := s.viewsFor(ctx, []KnowledgeBase{*kb})
	if err != nil {
		return nil, err
	}
	return &vs[0], nil
}

// ----- listing -----

type listMode int

const (
	listVisible listMode = iota
	listMine
	listBuiltin
)

type listQuery struct {
	mode    listMode
	userID  uint
	keyword string
	sort    string
	ids     []string
	enabled *bool
	page    pagination.Page
}

const activeFileCountExpr = `(SELECT COUNT(*) FROM knowledge_base_files kbf
	JOIN file_objects fo ON fo.id = kbf.file_object_id
	WHERE kbf.knowledge_base_id = knowledge_bases.id AND fo.status = 'active' AND fo.deleted_at IS NULL)`

func (s *Service) list(ctx context.Context, lq listQuery) ([]KnowledgeBaseView, int64, error) {
	empty := []KnowledgeBaseView{}
	if lq.mode != listBuiltin && lq.userID == 0 {
		return nil, 0, ErrInvalid
	}

	q := s.db.WithContext(ctx).Model(&KnowledgeBase{})
	switch lq.mode {
	case listVisible:
		q = visibleTo(lq.userID).apply(q)
	case listMine:
		q = personalOf(lq.userID).apply(q)
	case listBuiltin:
		q = adminAccess(0).apply(q)
	}
	if lq.mode != listVisible && lq.enabled != nil {
		q = q.Where("enabled = ?", *lq.enabled)
	}
	if kw := strings.ToLower(strings.TrimSpace(lq.keyword)); kw != "" {
		pat := "%" + escapeLike(kw) + "%"
		q = q.Where(`(LOWER(name) LIKE ? ESCAPE '\' OR LOWER(description) LIKE ? ESCAPE '\')`, pat, pat)
	}
	if len(lq.ids) > 0 {
		if len(lq.ids) > maxReferencedBases {
			return empty, 0, nil
		}
		q = q.Where("public_id IN ?", lq.ids)
	}

	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if total == 0 {
		return empty, 0, nil
	}

	switch strings.TrimSpace(lq.sort) {
	case "name":
		q = q.Order("LOWER(name) ASC").Order("id ASC")
	case "created":
		q = q.Order("created_at DESC").Order("id DESC")
	case "updated":
		q = q.Order("updated_at DESC").Order("id DESC")
	case "files":
		q = q.Order(activeFileCountExpr + " DESC").Order("id DESC")
	default:
		if lq.mode == listVisible {
			q = q.Order("CASE WHEN scope = 'builtin' THEN 0 ELSE 1 END ASC")
		} else {
			q = q.Order("enabled DESC")
		}
		q = q.Order("sort_order ASC").Order("updated_at DESC").Order("id DESC")
	}

	var rows []KnowledgeBase
	if err := q.Scopes(pagination.Scope(lq.page)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	views, err := s.viewsFor(ctx, rows)
	if err != nil {
		return nil, 0, err
	}
	return views, total, nil
}

// normalizeIDFilter prepares the repeated id query values: ok is false when
// the selection is invalid and must produce an empty page.
func normalizeIDFilter(raw []string) (ids []string, ok bool) {
	if len(raw) == 0 {
		return nil, true
	}
	ids = normalizeIDs(raw)
	if len(ids) == 0 || len(ids) > maxReferencedBases {
		return nil, false
	}
	return ids, true
}

// get returns one base with live counts.
func (s *Service) get(ctx context.Context, a access, publicID string) (*KnowledgeBaseView, error) {
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, err
	}
	return s.viewOf(ctx, kb)
}

// ----- create -----

type createInput struct {
	Name        string
	Description string
	Enabled     bool
	SortOrder   int
}

func (s *Service) createPersonal(ctx context.Context, userID uint, name, description string) (*KnowledgeBaseView, error) {
	if userID == 0 {
		return nil, ErrInvalid
	}
	return s.create(ctx, ScopeUser, userID, userID, createInput{Name: name, Description: description, Enabled: true})
}

func (s *Service) createBuiltin(ctx context.Context, adminID uint, in createInput) (*KnowledgeBaseView, error) {
	return s.create(ctx, ScopeBuiltin, 0, adminID, in)
}

func (s *Service) create(ctx context.Context, scope string, owner, actor uint, in createInput) (*KnowledgeBaseView, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return nil, err
	}
	desc, err := cleanDescription(in.Description)
	if err != nil {
		return nil, err
	}
	now := s.clock.Now()
	kb := &KnowledgeBase{
		PublicID: newPublicID(), Scope: scope, OwnerUserID: owner, Name: name, Description: desc,
		Enabled: true, SortOrder: in.SortOrder, Revision: 1,
		CreatedByUserID: actor, UpdatedByUserID: actor, CreatedAt: now, UpdatedAt: now,
	}
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if kb.SortOrder <= 0 {
			var max int
			if err := tx.Model(&KnowledgeBase{}).Where("scope = ? AND owner_user_id = ?", scope, owner).
				Select("COALESCE(MAX(sort_order), 0)").Scan(&max).Error; err != nil {
				return err
			}
			kb.SortOrder = max + 1
		}
		if err := tx.Create(kb).Error; err != nil {
			return translate(err)
		}
		if !in.Enabled {
			// The column default is true, which a plain insert of false would
			// silently take; write the explicit value.
			if err := tx.Model(kb).UpdateColumn("enabled", false).Error; err != nil {
				return err
			}
			kb.Enabled = false
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	v := toView(kb, counts{})
	return &v, nil
}

// ----- update -----

type patchInput struct {
	Name        *string
	Description *string
	Enabled     *bool
	SortOrder   *int
}

func (p patchInput) empty() bool {
	return p.Name == nil && p.Description == nil && p.Enabled == nil && p.SortOrder == nil
}

func (s *Service) updatePersonal(ctx context.Context, userID uint, publicID string, in patchInput) (*KnowledgeBaseView, error) {
	// Personal bases cannot be disabled or reordered.
	if in.Enabled != nil || in.SortOrder != nil {
		return nil, ErrInvalid
	}
	return s.update(ctx, personalOf(userID), userID, publicID, in)
}

func (s *Service) updateBuiltin(ctx context.Context, adminID uint, publicID string, in patchInput) (*KnowledgeBaseView, error) {
	return s.update(ctx, adminAccess(adminID), adminID, publicID, in)
}

func (s *Service) update(ctx context.Context, a access, actor uint, publicID string, in patchInput) (*KnowledgeBaseView, error) {
	if in.empty() {
		return nil, ErrInvalid
	}
	changes := map[string]any{}
	if in.Name != nil {
		n, err := cleanName(*in.Name)
		if err != nil {
			return nil, err
		}
		changes["name"] = n
	}
	if in.Description != nil {
		d, err := cleanDescription(*in.Description)
		if err != nil {
			return nil, err
		}
		changes["description"] = d
	}
	if in.Enabled != nil {
		changes["enabled"] = *in.Enabled
	}
	if in.SortOrder != nil {
		changes["sort_order"] = *in.SortOrder
	}
	changes["updated_by_user_id"] = actor
	changes["updated_at"] = s.clock.Now()

	var out *KnowledgeBase
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		kb, err := s.find(ctx, tx, a, publicID, true)
		if err != nil {
			return err
		}
		if err := tx.Model(&KnowledgeBase{}).Where("id = ?", kb.ID).Updates(changes).Error; err != nil {
			return translate(err)
		}
		var fresh KnowledgeBase
		if err := tx.Where("id = ?", kb.ID).Take(&fresh).Error; err != nil {
			return err
		}
		out = &fresh
		return nil
	})
	if err != nil {
		return nil, err
	}
	return s.viewOf(ctx, out)
}

// ----- delete -----

const projectLinksTable = "chat_conversation_project_knowledge_bases"

// deleteProjectLinks removes projects' default-base links to the given bases.
// The conversation area owns that table; it keys links by the base's public
// id, and the numeric id is matched too in case an older row used it.
func deleteProjectLinks(tx *gorm.DB, bases []KnowledgeBase) error {
	if len(bases) == 0 || !tx.Migrator().HasTable(projectLinksTable) {
		return nil
	}
	keys := make([]string, 0, len(bases)*2)
	for _, b := range bases {
		keys = append(keys, b.PublicID, strconv.FormatUint(uint64(b.ID), 10))
	}
	return tx.Exec("DELETE FROM "+projectLinksTable+" WHERE knowledge_base_id IN ?", keys).Error
}

type linkedFile struct {
	fileID string
	owner  uint
}

// deleteBase removes a base, its links and its project defaults. It returns
// the number of files deleted when deleteFiles is set.
func (s *Service) deleteBase(ctx context.Context, a access, publicID string, deleteFiles bool) (int, error) {
	if deleteFiles && s.ports.Cleaner == nil {
		// Checked before anything is touched so a refusal leaves no change.
		if _, err := s.find(ctx, s.db, a, publicID, false); err != nil {
			return 0, err
		}
		return 0, ErrCleanupUnavailable
	}

	var files []linkedFile
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		kb, err := s.find(ctx, tx, a, publicID, true)
		if err != nil {
			return err
		}
		if deleteFiles {
			var objs []fileObject
			if err := tx.Model(&fileObject{}).Select("file_objects.*").
				Joins("JOIN knowledge_base_files kbf ON kbf.file_object_id = file_objects.id").
				Where("kbf.knowledge_base_id = ? AND file_objects.status = ?", kb.ID, fileStatusActive).
				Order("file_objects.id ASC").Find(&objs).Error; err != nil {
				return err
			}
			for _, o := range objs {
				files = append(files, linkedFile{fileID: o.FileID, owner: o.UserID})
			}
		}
		if err := deleteProjectLinks(tx, []KnowledgeBase{*kb}); err != nil {
			return err
		}
		if err := tx.Where("knowledge_base_id = ?", kb.ID).Delete(&KnowledgeBaseFile{}).Error; err != nil {
			return err
		}
		return tx.Delete(&KnowledgeBase{}, kb.ID).Error
	})
	if err != nil {
		return 0, err
	}

	deleted := 0
	for _, f := range files {
		ok, err := s.ports.Cleaner.DeleteUnreferenced(ctx, f.owner, f.fileID)
		if err != nil {
			s.log.Warn("knowledge_base_file_cleanup_failed", logx.F("file_id", f.fileID), logx.Err(err))
			continue
		}
		if ok {
			deleted++
		}
	}
	return deleted, nil
}
