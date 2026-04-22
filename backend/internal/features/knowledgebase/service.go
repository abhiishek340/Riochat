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
