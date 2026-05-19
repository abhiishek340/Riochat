package knowledgebase

import (
	"context"
	"errors"
	"strings"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// platformPurpose is the file purpose of pool files.
const platformPurpose = "knowledge_base"

// uploadLimit is the largest request body accepted for a platform upload: the
// configured per-file cap (20 MiB when unset) plus 1 MiB of multipart framing.
func (s *Service) uploadLimit(ctx context.Context) int64 {
	max := s.ports.Settings.MaxUploadBytes(ctx)
	if max <= 0 {
		max = defaultMaxUploadBytes
	}
	return max + (1 << 20)
}

// uploadPlatformFile stores a file in the platform pool and returns its view.
func (s *Service) uploadPlatformFile(ctx context.Context, adminID uint, req UploadRequest) (*FileView, error) {
	if s.ports.Uploader == nil {
		return nil, ErrInvalid
	}
	req.OwnerUserID = 0
	req.OperatorUserID = adminID
	req.Purpose = platformPurpose
	fileID, err := s.ports.Uploader.Upload(ctx, req)
	if err != nil {
		return nil, mapUploadError(err)
	}
	var obj fileObject
	if err := s.db.WithContext(ctx).Where("file_id = ?", fileID).Take(&obj).Error; err != nil {
		return nil, translate(err)
	}
	v := s.fileView(ctx, &obj)
	return &v, nil
}

// mapUploadError keeps the errors the upload contract names and hides the
// rest behind the generic area error.
func mapUploadError(err error) error {
	for _, known := range []error{ErrQuotaExceeded, ErrInvalidFile, ErrEmbeddingUnavailableForFile, ErrFileTooLarge, ErrFileNotFound} {
		if errors.Is(err, known) {
			return known
		}
	}
	// A file the store refuses on content grounds (blocked type, unreadable
	// stream) is the caller's problem, not a server fault.
	if te, ok := apperr.Find(err); ok && (te.Code == "file.type_blocked" || te.Code == "file.invalid_stream") {
		return ErrInvalidFile
	}
	return ErrInternal.WithCause(err)
}

// deletePlatformFile deletes an unreferenced pool file.
func (s *Service) deletePlatformFile(ctx context.Context, fileID string) error {
	fid := strings.TrimSpace(fileID)
	if fid == "" {
		return ErrInvalid
	}
	if s.ports.Cleaner == nil {
		return ErrCleanupUnavailable
	}
	// A link to any knowledge base is a reference, whatever the file
	// subsystem would say.
	var n int64
	err := s.db.WithContext(ctx).Table("knowledge_base_files AS kbf").
		Joins("JOIN file_objects fo ON fo.id = kbf.file_object_id").
		Where("fo.file_id = ? AND fo.user_id = ?", fid, 0).Count(&n).Error
	if err != nil {
		return err
	}
	if n > 0 {
		return ErrPlatformFileInUse
	}
	deleted, err := s.ports.Cleaner.DeleteUnreferenced(ctx, 0, fid)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			return s.undeletedPlatformFile(ctx, fid)
		}
		return err
	}
	if !deleted {
		return s.undeletedPlatformFile(ctx, fid)
	}
	return nil
}

// undeletedPlatformFile explains why a file was not deleted: an id that names
// no live file at all (never existed, or already deleted) is a plain 404; a
// file that exists but is still referenced, or is not a pool file, is in use.
func (s *Service) undeletedPlatformFile(ctx context.Context, fid string) error {
	var n int64
	if err := s.db.WithContext(ctx).Model(&fileObject{}).Where("file_id = ?", fid).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return ErrFileNotFound
	}
	return ErrPlatformFileInUse
}

// embedResult is the wire shape of an embedding submission.
type embedResult struct {
	SubmittedFileIDs []string      `json:"submittedFileIDs"`
	Skipped          []SkippedFile `json:"skipped"`
}

// submitEmbeddings queues pool files for vectorization. Ids are trimmed and
// de-duplicated; the queue is always addressed as owner 0.
func (s *Service) submitEmbeddings(ctx context.Context, fileIDs []string) (*embedResult, []string, error) {
	ids := normalizeIDs(fileIDs)
	if len(ids) == 0 || len(ids) > maxFileIDsPerCall {
		return nil, nil, ErrInvalid
	}
	if s.ports.Embedder == nil {
		return nil, ids, ErrEmbeddingNotConfigured
	}
	res, err := s.ports.Embedder.SubmitFiles(ctx, 0, ids)
	if err != nil {
		for _, known := range []error{ErrTooManyEmbeddingFiles, ErrEmbeddingNotConfigured, ErrEmbeddingUnavailable} {
			if errors.Is(err, known) {
				return nil, ids, known
			}
		}
		if mapped := translate(err); mapped != err {
			return nil, ids, mapped
		}
		s.log.Warn("knowledge_base_embedding_submit_failed", logx.Err(err))
		return nil, ids, ErrInternal.WithCause(err)
	}
	out := &embedResult{SubmittedFileIDs: res.Submitted, Skipped: res.Skipped}
	if out.SubmittedFileIDs == nil {
		out.SubmittedFileIDs = []string{}
	}
	if out.Skipped == nil {
		out.Skipped = []SkippedFile{}
	}
	return out, ids, nil
}
