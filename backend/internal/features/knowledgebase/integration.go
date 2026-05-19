package knowledgebase

import (
	"context"

	"gorm.io/gorm"
)

// This file is the contract with the rest of the application. Other areas
// call these methods on the Service returned by NewService.

// Enabled reports the feature switch.
func (s *Service) Enabled(ctx context.Context) bool { return s.ports.Settings.Enabled(ctx) }

// ResolvedFile is a RAG-usable file reached through the requested bases.
type ResolvedFile struct {
	FileObjectID     uint
	FileID           string
	OwnerUserID      uint
	FileName         string
	MimeType         string
	ChunkCount       int
	KnowledgeBaseIDs []string
}

// Resolution is the result of Resolve.
type Resolution struct {
	Bases []KnowledgeBaseView
	Files []ResolvedFile
}

// Resolve turns the public ids a conversation references into bases (with
// live counts) and the de-duplicated list of retrieval-ready files. Any
// problem with the references (count out of 1..8, blanks, duplicates, a base
// that is missing, disabled or foreign) yields ErrReferenceUnavailable.
func (s *Service) Resolve(ctx context.Context, userID uint, ids []string) (*Resolution, error) {
	norm := normalizeIDs(ids)
	if userID == 0 || len(norm) == 0 || len(norm) != len(ids) || len(norm) > maxReferencedBases {
		return nil, ErrReferenceUnavailable
	}
	var rows []KnowledgeBase
	if err := visibleTo(userID).apply(s.db.WithContext(ctx).Model(&KnowledgeBase{})).
		Where("public_id IN ?", norm).Find(&rows).Error; err != nil {
		return nil, translateRef(err)
	}
	if len(rows) != len(norm) {
		return nil, ErrReferenceUnavailable
	}
	byPublic := make(map[string]KnowledgeBase, len(rows))
	for _, r := range rows {
		byPublic[r.PublicID] = r
	}
	ordered := make([]KnowledgeBase, 0, len(norm))
	baseIDs := make([]uint, 0, len(norm))
	publicOf := make(map[uint]string, len(norm))
	for _, id := range norm {
		kb := byPublic[id]
		ordered = append(ordered, kb)
		baseIDs = append(baseIDs, kb.ID)
		publicOf[kb.ID] = kb.PublicID
	}
	views, err := s.viewsFor(ctx, ordered)
	if err != nil {
		return nil, err
	}

	type link struct {
		ID         uint   `gorm:"column:id"`
		FileID     string `gorm:"column:file_id"`
		UserID     uint   `gorm:"column:user_id"`
		FileName   string `gorm:"column:file_name"`
		MimeType   string `gorm:"column:mime_type"`
		ChunkCount int    `gorm:"column:chunk_count"`
		BaseID     uint   `gorm:"column:kb_base_id"`
	}
	var links []link
	err = s.db.WithContext(ctx).Model(&fileObject{}).
		Select("file_objects.id, file_objects.file_id, file_objects.user_id, file_objects.file_name, file_objects.mime_type, file_objects.chunk_count, kbf.knowledge_base_id AS kb_base_id").
		Joins("JOIN knowledge_base_files kbf ON kbf.file_object_id = file_objects.id").
		Where("kbf.knowledge_base_id IN ? AND file_objects.status = ?", baseIDs, fileStatusActive).
		Where("file_objects.processing_ready = ? AND file_objects.embed_status = ? AND file_objects.rag_opt_out = ? AND file_objects.chunk_count > 0",
			true, embedStatusReady, false).
		Order("file_objects.id ASC").Order("kbf.knowledge_base_id ASC").
		Find(&links).Error
	if err != nil {
		return nil, err
	}
	res := &Resolution{Bases: views, Files: []ResolvedFile{}}
	index := map[uint]int{}
	for _, l := range links {
		i, seen := index[l.ID]
		if !seen {
			index[l.ID] = len(res.Files)
			res.Files = append(res.Files, ResolvedFile{
				FileObjectID: l.ID, FileID: l.FileID, OwnerUserID: l.UserID, FileName: l.FileName,
				MimeType: l.MimeType, ChunkCount: l.ChunkCount,
			})
			i = len(res.Files) - 1
		}
		res.Files[i].KnowledgeBaseIDs = append(res.Files[i].KnowledgeBaseIDs, publicOf[l.BaseID])
	}
	return res, nil
}

// translateRef treats storage "not found" and "invalid" the same as an
// unusable reference; anything else passes through.
func translateRef(err error) error {
	if e := translate(err); e != err {
		return ErrReferenceUnavailable
	}
	return err
}

// ValidateKnowledgeBases is Resolve plus the readiness rules the conversation
// area applies: every requested base needs at least one ready file.
func (s *Service) ValidateKnowledgeBases(ctx context.Context, userID uint, ids []string) error {
	res, err := s.Resolve(ctx, userID, ids)
	if err != nil {
		return err
	}
	if len(res.Files) == 0 {
		return ErrNotReady
	}
	for _, b := range res.Bases {
		if b.ReadyFileCount == 0 {
			return ErrNotReady
		}
	}
	return nil
}

// FilterProjectBases returns, in the order given, those public ids that are
// still enabled and either builtin or owned by ownerUserID. Projects use it
// when they report their default bases.
func (s *Service) FilterProjectBases(ctx context.Context, ownerUserID uint, ids []string) ([]string, error) {
	ids = normalizeIDs(ids)
	if len(ids) == 0 {
		return []string{}, nil
	}
	var rows []KnowledgeBase
	if err := visibleTo(ownerUserID).apply(s.db.WithContext(ctx).Model(&KnowledgeBase{})).
		Where("public_id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	ok := make(map[string]bool, len(rows))
	for _, r := range rows {
		ok[r.PublicID] = true
	}
	out := make([]string, 0, len(rows))
	for _, id := range ids {
		if ok[id] {
			out = append(out, id)
		}
	}
	return out, nil
}

// IsFileLinked reports whether a file object belongs to any knowledge base.
// The generic file deletion path calls it and refuses (as a conflict) while it
// is true.
func (s *Service) IsFileLinked(ctx context.Context, fileObjectID uint) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Model(&KnowledgeBaseFile{}).Where("file_object_id = ?", fileObjectID).Count(&n).Error
	return n > 0, err
}

// FileInEnabledBuiltinBase reports whether a file belongs to an enabled
// builtin base, which makes its chunks retrievable by every user.
func (s *Service) FileInEnabledBuiltinBase(ctx context.Context, fileObjectID uint) (bool, error) {
	var n int64
	err := s.db.WithContext(ctx).Table("knowledge_base_files AS kbf").
		Joins("JOIN knowledge_bases kb ON kb.id = kbf.knowledge_base_id").
		Where("kbf.file_object_id = ? AND kb.scope = ? AND kb.enabled = ?", fileObjectID, ScopeBuiltin, true).
		Count(&n).Error
	return n > 0, err
}

// CheckAccountDeletion refuses (409 owner_file_reference) when the user owns an
// active file that a builtin base contains.
func (s *Service) CheckAccountDeletion(ctx context.Context, userID uint) error {
	var n int64
	err := s.db.WithContext(ctx).Table("knowledge_base_files AS kbf").
		Joins("JOIN knowledge_bases kb ON kb.id = kbf.knowledge_base_id").
		Joins("JOIN file_objects fo ON fo.id = kbf.file_object_id").
		Where("kb.scope = ? AND fo.user_id = ? AND fo.status = ? AND fo.deleted_at IS NULL", ScopeBuiltin, userID, fileStatusActive).
		Count(&n).Error
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrOwnerFileReference
	}
	return nil
}

// PurgeUserData removes everything knowledge-base related that belongs to a
// user being hard deleted: personal bases, their project links, and every
// membership row that references those bases or the user's files. It runs in
// the caller's transaction.
func (s *Service) PurgeUserData(ctx context.Context, tx *gorm.DB, userID uint) error {
	tx = tx.WithContext(ctx)
	var bases []KnowledgeBase
	if err := tx.Where("scope = ? AND owner_user_id = ?", ScopeUser, userID).Find(&bases).Error; err != nil {
		return err
	}
	if err := deleteProjectLinks(tx, bases); err != nil {
		return err
	}
	if len(bases) > 0 {
		ids := make([]uint, len(bases))
		for i, b := range bases {
			ids[i] = b.ID
		}
		if err := tx.Where("knowledge_base_id IN ?", ids).Delete(&KnowledgeBaseFile{}).Error; err != nil {
			return err
		}
		if err := tx.Where("id IN ?", ids).Delete(&KnowledgeBase{}).Error; err != nil {
			return err
		}
	}
	// Memberships of the user's own files (soft-deleted ones included).
	return tx.Exec("DELETE FROM knowledge_base_files WHERE file_object_id IN (SELECT id FROM file_objects WHERE user_id = ?)", userID).Error
}
