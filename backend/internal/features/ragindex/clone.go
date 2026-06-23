package ragindex

import (
	"context"
	"errors"

	"gorm.io/gorm"
)

// ErrCloneUnavailable means the source has no index to copy; the caller falls
// back to normal asynchronous indexing.
var ErrCloneUnavailable = errors.New("ragindex: source file has no ready index to clone")

// CloneIndex copies the chunks and vectors of a ready file to another file in
// one transaction; the target becomes ready under the same signature with the
// same chunk count. Any failure (including a vector count that does not match
// the chunk count) rolls everything back and is returned so the caller can fall
// back to indexing the target normally.
//
// With a non-nil tx the copy runs in a savepoint of the caller's transaction.
func (s *Service) CloneIndex(ctx context.Context, tx *gorm.DB, fromID, toID uint) error {
	db := s.db
	if tx != nil {
		db = tx
	}
	return db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var src, dst File
		if err := tx.Model(&File{}).Where("id = ? AND status = ?", fromID, fileActive).Take(&src).Error; err != nil {
			return ErrCloneUnavailable
		}
		if src.state() != StateReady || src.ChunkCount <= 0 || src.EmbedSignature == "" {
			return ErrCloneUnavailable
		}
		if err := tx.Model(&File{}).Where("id = ? AND status = ?", toID, fileActive).Take(&dst).Error; err != nil {
			return err
		}
		var chunks []Chunk
		if err := tx.Where("file_obj_id = ?", fromID).Order("chunk_index").Find(&chunks).Error; err != nil {
			return err
		}
		if len(chunks) == 0 {
			return ErrCloneUnavailable
		}
		var vecs []ChunkVector
		if err := tx.Where("file_obj_id = ?", fromID).Find(&vecs).Error; err != nil {
			return err
		}
		byChunk := make(map[uint]ChunkVector, len(vecs))
		for _, v := range vecs {
			byChunk[v.ChunkID] = v
		}
		if err := dropIndex(tx, toID); err != nil {
			return err
		}
		copies := make([]ChunkVector, 0, len(chunks))
		for _, c := range chunks {
			v, ok := byChunk[c.ID]
			if !ok {
				return errBatchMismatch
			}
			nc := c
			nc.ID, nc.FileObjID, nc.UserID = 0, toID, dst.UserID
			if err := tx.Create(&nc).Error; err != nil {
				return err
			}
			copies = append(copies, ChunkVector{
				ChunkID: nc.ID, UserID: dst.UserID, FileObjID: toID, EmbeddingSignature: v.EmbeddingSignature,
				Dims: v.Dims, Embedding: v.Embedding,
			})
		}
		if err := tx.CreateInBatches(copies, 100).Error; err != nil {
			return err
		}
		var n int64
		if err := tx.Model(&ChunkVector{}).Where("file_obj_id = ?", toID).Count(&n).Error; err != nil {
			return err
		}
		if int(n) != len(chunks) {
			return errBatchMismatch
		}
		return tx.Model(&File{}).Where("id = ?", toID).UpdateColumns(map[string]any{
			"embed_status": StateReady, "embed_signature": src.EmbedSignature, "embed_error": "",
			"chunk_count": len(chunks), "updated_at": s.now(),
		}).Error
	})
}
