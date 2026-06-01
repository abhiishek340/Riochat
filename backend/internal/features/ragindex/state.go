package ragindex

import (
	"context"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// gormDB is the query handle type used by the listing helpers.
type gormDB = gorm.DB

// activeFiles is the file table restricted to active rows.
func (s *Service) activeFiles(ctx context.Context) *gorm.DB {
	return s.files(ctx).Where("status = ?", fileActive)
}

// noneClause matches files that were never indexed (the stored value is empty,
// "none" or a legacy marker).
const noneClause = "(embed_status IS NULL OR embed_status NOT IN ('queued','processing','ready','stale','empty','failed'))"

// embeddableClause selects files the administrator views list as embeddable:
// a storage path and a document category, or an image the configuration can
// take. The returned arguments belong to the clause.
func (s *Service) embeddableClause(cfg Config) (string, []any) {
	imageSQL := "1 = 0"
	var args []any
	switch {
	case cfg.ImageOCREnabled:
		imageSQL = "COALESCE(file_category,'') = 'image'"
	case s.imageProtocol(cfg):
		imageSQL = "(COALESCE(file_category,'') = 'image' AND LOWER(CASE WHEN COALESCE(detected_mime,'') <> '' THEN detected_mime ELSE mime_type END) IN ?)"
		mimes := make([]string, 0, len(pixelMIMEs))
		for m := range pixelMIMEs {
			mimes = append(mimes, m)
		}
		args = append(args, mimes)
	}
	sql := "(COALESCE(storage_path,'') <> '' AND (COALESCE(file_category,'') IN ? OR " + imageSQL + "))"
	return sql, append([]any{embeddableCategories}, args...)
}

// embeddable is the Go form of embeddableClause for one row.
func (s *Service) embeddable(f File, cfg Config) bool {
	if strings.TrimSpace(f.StoragePath) == "" {
		return false
	}
	if f.FileCategory == "image" {
		return imageIndexable(f.MIME(), cfg.ImageOCREnabled, s.imageProtocol(cfg))
	}
	for _, c := range embeddableCategories {
		if f.FileCategory == c {
			return true
		}
	}
	return false
}

// stalled reports whether a queued or processing task made no progress for 30
// minutes, or the text processing was stuck in uploaded/queued that long (an
// extraction message was lost).
func stalled(f File, now time.Time) bool {
	idle := now.Sub(f.UpdatedAt) >= stalledAfter
	switch f.state() {
	case StateQueued, StateProcessing:
		return idle
	}
	switch f.ProcessingStatus {
	case procUploaded, procQueued:
		return idle
	}
	return false
}

// claim registers the run: the file becomes processing under sig unless it is
// already processing or ready under the same signature (a takeover run may
// claim a processing file). fromUpload also moves the text-processing status
// to embedding.
func (s *Service) claim(ctx context.Context, id uint, sig string, takeover, fromUpload bool) (bool, error) {
	q := s.files(ctx).Where("id = ? AND status = ?", id, fileActive)
	if takeover {
		q = q.Where("NOT (COALESCE(embed_signature,'') = ? AND embed_status = ?)", sig, StateReady)
	} else {
		q = q.Where("NOT (COALESCE(embed_signature,'') = ? AND embed_status IN ?)", sig, []string{StateProcessing, StateReady})
	}
	upd := map[string]any{"embed_status": StateProcessing, "embed_signature": sig, "embed_error": "", "updated_at": s.now()}
	if fromUpload {
		upd["processing_status"] = procEmbedding
	}
	res := q.UpdateColumns(upd)
	return res.RowsAffected > 0, res.Error
}

// transition updates a file only while it still belongs to the task: its
// recorded signature equals sig and its state is one of from.
func (s *Service) transition(ctx context.Context, db *gorm.DB, id uint, sig string, from []string, set map[string]any) (bool, error) {
	if db == nil {
		db = s.db
	}
	set["updated_at"] = s.now()
	q := db.WithContext(ctx).Model(&File{}).Where("id = ? AND COALESCE(embed_signature,'') = ?", id, sig)
	if len(from) > 0 {
		q = q.Where("embed_status IN ?", from)
	}
	res := q.UpdateColumns(set)
	return res.RowsAffected > 0, res.Error
}

var inFlight = []string{StateQueued, StateProcessing}

// markStale records that the task's vector space no longer applies.
func (s *Service) markStale(ctx context.Context, id uint, sig, msg string) error {
	_, err := s.transition(ctx, nil, id, sig, []string{StateQueued, StateProcessing, StateReady}, map[string]any{
		"embed_status": StateStale, "embed_error": msg,
	})
	return err
}

// endUploadRun restores the text-processing status after an upload-triggered
// run; a failure leaves the file usable but not retrieval-ready.
func (s *Service) endUploadRun(ctx context.Context, id uint, failure string) {
	set := map[string]any{"processing_status": procReady, "processing_ready": true, "updated_at": s.now()}
	if failure != "" {
		set["processing_error_code"] = "embed_failed"
		set["processing_error_message"] = truncateRunes(failure, 255)
	}
	if err := s.files(ctx).Where("id = ? AND processing_status = ?", id, procEmbedding).UpdateColumns(set).Error; err != nil {
		s.log.Warn("rag_end_upload_run_failed", logx.Err(err))
	}
}
