package knowledgebase

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/riochat/riochat/backend/internal/platform/pagination"
)

// ----- file views -----

func (s *Service) facts(f *fileObject) FileFacts {
	return FileFacts{
		FileID: f.FileID, OwnerUserID: f.UserID, FileName: f.FileName, MimeType: f.MimeType,
		DetectedMIME: f.DetectedMime, FileCategory: f.FileCategory, SizeBytes: f.SizeBytes,
		EmbedStatus: f.EmbedStatus, ChunkCount: f.ChunkCount, RAGOptOut: f.RagOptOut,
	}
}

// vectorization asks the embedding subsystem; without one every file answers
// false with no reason.
func (s *Service) vectorization(ctx context.Context, f *fileObject) (bool, string) {
	if s.ports.Vectorizer == nil {
		return false, ""
	}
	return s.ports.Vectorizer.CanVectorize(ctx, s.facts(f))
}

func (s *Service) fileView(ctx context.Context, f *fileObject) FileView {
	can, reason := s.vectorization(ctx, f)
	return FileView{
		FileID: f.FileID, FileName: f.FileName, MimeType: f.MimeType, DetectedMIME: f.DetectedMime,
		FileCategory: f.FileCategory, SizeBytes: f.SizeBytes, ProcessingStatus: f.ProcessingStatus,
		Processing: f.isProcessing(), ProcessingReady: f.ProcessingReady, ExtractStatus: f.ExtractStatus,
		EmbedStatus: f.EmbedStatus, EmbedError: f.EmbedError, ChunkCount: f.ChunkCount, RagOptOut: f.RagOptOut,
		CanVectorize: can, VectorizationReason: reason, CreatedAt: f.CreatedAt.Local(), UpdatedAt: f.UpdatedAt.Local(),
	}
}

func (s *Service) statusView(ctx context.Context, f *fileObject) StatusView {
	can, reason := s.vectorization(ctx, f)
	return StatusView{
		FileID: f.FileID, DetectedMIME: f.DetectedMime, FileCategory: f.FileCategory,
		ProcessingStatus: f.ProcessingStatus, Processing: f.isProcessing(), ProcessingReady: f.ProcessingReady,
		ExtractStatus: f.ExtractStatus, EmbedStatus: f.EmbedStatus, EmbedError: f.EmbedError,
		ChunkCount: f.ChunkCount, RagOptOut: f.RagOptOut, CanVectorize: can, VectorizationReason: reason,
		UpdatedAt: f.UpdatedAt.Local(),
	}
}

func (s *Service) fileViews(ctx context.Context, rows []fileObject) []FileView {
	out := make([]FileView, len(rows))
	for i := range rows {
		out[i] = s.fileView(ctx, &rows[i])
	}
	return out
}

// linked returns a query over the active, non-deleted files linked to a base.
func (s *Service) linked(ctx context.Context, db *gorm.DB, baseID uint) *gorm.DB {
	return db.WithContext(ctx).Model(&fileObject{}).
		Joins("JOIN knowledge_base_files kbf ON kbf.file_object_id = file_objects.id").
		Where("kbf.knowledge_base_id = ? AND file_objects.status = ?", baseID, fileStatusActive)
}

// ----- listing -----

func (s *Service) listFiles(ctx context.Context, a access, publicID string, page pagination.Page) ([]FileView, int64, error) {
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, 0, err
	}
	q := s.linked(ctx, s.db, kb.ID)
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []fileObject
	if err := q.Select("file_objects.*").
		Order("kbf.sort_order ASC").Order("kbf.created_at ASC").Order("file_objects.id ASC").
		Scopes(pagination.Scope(page)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return s.fileViews(ctx, rows), total, nil
}

// ownedFiles lists active, non-deleted files of one owner, optionally
// excluding those already in a base and filtering by a file-name keyword.
func (s *Service) ownedFiles(ctx context.Context, owner uint, excludeBase uint, keyword string, page pagination.Page) ([]FileView, int64, error) {
	q := s.db.WithContext(ctx).Model(&fileObject{}).
		Where("file_objects.user_id = ? AND file_objects.status = ?", owner, fileStatusActive)
	if excludeBase != 0 {
		q = q.Where("NOT EXISTS (SELECT 1 FROM knowledge_base_files kbf WHERE kbf.file_object_id = file_objects.id AND kbf.knowledge_base_id = ?)", excludeBase)
	}
	if kw := strings.ToLower(strings.TrimSpace(keyword)); kw != "" {
		q = q.Where(`LOWER(file_objects.file_name) LIKE ? ESCAPE '\'`, "%"+escapeLike(kw)+"%")
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []fileObject
	if err := q.Order("file_objects.created_at DESC").Order("file_objects.id DESC").
		Scopes(pagination.Scope(page)).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return s.fileViews(ctx, rows), total, nil
}

func (s *Service) availableFiles(ctx context.Context, a access, publicID, keyword string, page pagination.Page) ([]FileView, int64, error) {
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, 0, err
	}
	return s.ownedFiles(ctx, a.fileOwner(), kb.ID, keyword, page)
}

func (s *Service) platformFiles(ctx context.Context, keyword string, page pagination.Page) ([]FileView, int64, error) {
	return s.ownedFiles(ctx, 0, 0, keyword, page)
}

// ----- membership -----

// addFiles links files to a base. Every id must be an active file of the
// base's file owner; otherwise nothing is added.
func (s *Service) addFiles(ctx context.Context, a access, actor uint, publicID string, fileIDs []string) error {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 || len(ids) != len(fileIDs) || len(ids) > maxFileIDsPerCall {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		kb, err := s.find(ctx, tx, a, publicID, true)
		if err != nil {
			return err
		}
		var objs []fileObject
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("file_id IN ? AND user_id = ? AND status = ?", ids, a.fileOwner(), fileStatusActive).
			Find(&objs).Error; err != nil {
			return err
		}
		if len(objs) != len(ids) {
			return ErrNotFound
		}
		byFileID := make(map[string]uint, len(objs))
		objIDs := make([]uint, 0, len(objs))
		for _, o := range objs {
			byFileID[o.FileID] = o.ID
			objIDs = append(objIDs, o.ID)
		}

		var existing []uint
		if err := tx.Model(&KnowledgeBaseFile{}).
			Where("knowledge_base_id = ? AND file_object_id IN ?", kb.ID, objIDs).
			Pluck("file_object_id", &existing).Error; err != nil {
			return err
		}
		have := make(map[uint]bool, len(existing))
		for _, id := range existing {
			have[id] = true
		}

		var maxSort int
		if err := tx.Model(&KnowledgeBaseFile{}).Where("knowledge_base_id = ?", kb.ID).
			Select("COALESCE(MAX(sort_order), 0)").Scan(&maxSort).Error; err != nil {
			return err
		}

		now := s.clock.Now()
		inserted := 0
		for _, fid := range ids {
			objID := byFileID[fid]
			if have[objID] {
				continue
			}
			link := KnowledgeBaseFile{
				KnowledgeBaseID: kb.ID, FileObjectID: objID, SortOrder: maxSort + inserted + 1,
				AddedByUserID: actor, CreatedAt: now,
			}
			// A concurrent identical add loses quietly to the primary key.
			res := tx.Clauses(clause.OnConflict{DoNothing: true}).Create(&link)
			if res.Error != nil {
				return res.Error
			}
			inserted += int(res.RowsAffected)
		}
		if inserted == 0 {
			return nil
		}
		return bumpRevision(tx, kb.ID, now)
	})
}

func bumpRevision(tx *gorm.DB, baseID uint, now time.Time) error {
	return tx.Model(&KnowledgeBase{}).Where("id = ?", baseID).Updates(map[string]any{
		"revision":   gorm.Expr("revision + 1"),
		"updated_at": now,
	}).Error
}

// removeFile unlinks one file from a base; the file object is kept.
func (s *Service) removeFile(ctx context.Context, a access, publicID, fileID string) error {
	fid := strings.TrimSpace(fileID)
	if fid == "" {
		return ErrInvalid
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		kb, err := s.find(ctx, tx, a, publicID, true)
		if err != nil {
			return err
		}
		var obj fileObject
		if err := s.linked(ctx, tx, kb.ID).Select("file_objects.*").
			Where("file_objects.file_id = ?", fid).Take(&obj).Error; err != nil {
			return translate(err)
		}
		res := tx.Where("knowledge_base_id = ? AND file_object_id = ?", kb.ID, obj.ID).Delete(&KnowledgeBaseFile{})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return ErrNotFound
		}
		return bumpRevision(tx, kb.ID, s.clock.Now())
	})
}

// ----- processing status -----

func (s *Service) statusesFor(ctx context.Context, baseID uint, ids []string) ([]StatusView, error) {
	out := []StatusView{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []fileObject
	if err := s.linked(ctx, s.db, baseID).Select("file_objects.*").
		Where("file_objects.file_id IN ?", ids).
		Order("kbf.sort_order ASC").Order("file_objects.id ASC").Find(&rows).Error; err != nil {
		return nil, err
	}
	for i := range rows {
		out = append(out, s.statusView(ctx, &rows[i]))
	}
	return out, nil
}

func (s *Service) statuses(ctx context.Context, a access, publicID string, fileIDs []string) ([]StatusView, error) {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 || len(fileIDs) > maxFileIDsPerCall {
		return nil, ErrInvalid
	}
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, err
	}
	return s.statusesFor(ctx, kb.ID, ids)
}

type snapshot struct {
	KnowledgeBase KnowledgeBaseView `json:"knowledgeBase"`
	Statuses      []StatusView      `json:"statuses"`
}

func (s *Service) snapshot(ctx context.Context, a access, publicID string, fileIDs []string) (*snapshot, error) {
	ids := normalizeIDs(fileIDs)
	if len(fileIDs) > 0 && len(ids) == 0 || len(fileIDs) > maxFileIDsPerCall {
		return nil, ErrInvalid
	}
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, err
	}
	view, err := s.viewOf(ctx, kb)
	if err != nil {
		return nil, err
	}
	sts, err := s.statusesFor(ctx, kb.ID, ids)
	if err != nil {
		return nil, err
	}
	return &snapshot{KnowledgeBase: *view, Statuses: sts}, nil
}

// ----- content -----

// openContent streams a file that is linked to a base the caller may reach.
// The bytes are read on behalf of the file's real owner.
func (s *Service) openContent(ctx context.Context, a access, publicID, fileID string) (*FileContent, error) {
	fid := strings.TrimSpace(fileID)
	if fid == "" {
		return nil, ErrInvalid
	}
	kb, err := s.find(ctx, s.db, a, publicID, false)
	if err != nil {
		return nil, err
	}
	var obj fileObject
	if err := s.linked(ctx, s.db, kb.ID).Select("file_objects.*").
		Where("file_objects.file_id = ?", fid).Take(&obj).Error; err != nil {
		return nil, translate(err)
	}
	return s.read(ctx, &obj)
}

// openPoolContent streams a platform pool file.
func (s *Service) openPoolContent(ctx context.Context, fileID string) (*FileContent, error) {
	fid := strings.TrimSpace(fileID)
	if fid == "" {
		return nil, ErrInvalid
	}
	var obj fileObject
	if err := s.db.WithContext(ctx).
		Where("file_id = ? AND user_id = ? AND status = ?", fid, 0, fileStatusActive).
		Take(&obj).Error; err != nil {
		if errors.Is(translate(err), ErrNotFound) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return s.read(ctx, &obj)
}

func (s *Service) read(ctx context.Context, obj *fileObject) (*FileContent, error) {
	if s.ports.Reader == nil {
		return nil, ErrInternal.WithCause(errNoReader)
	}
	fc, err := s.ports.Reader.OpenFile(ctx, obj.UserID, obj.FileID)
	if err != nil {
		return nil, err
	}
	if fc.FileName == "" {
		fc.FileName = obj.FileName
	}
	if fc.MimeType == "" {
		fc.MimeType = obj.MimeType
	}
	return fc, nil
}

var errNoReader = errors.New("knowledgebase: no file reader configured")
