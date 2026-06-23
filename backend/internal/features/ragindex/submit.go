package ragindex

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"
)

// Skip reasons of an explicit submission (never HTTP errors).
const (
	SkipNotFound    = "not_found"
	SkipAlreadyDone = "already_ready"
	SkipProcessing  = "processing"
	SkipUnsupported = "unsupported"
	SkipNotReady    = "not_ready"
	SkipQueueBusy   = "queue_busy"
	SkipFailed      = "submit_failed"
)

// maxSubmitFiles bounds one submission.
const maxSubmitFiles = 100

// Skip names a file that was not submitted and why.
type Skip struct {
	FileID string `json:"fileID"`
	Reason string `json:"reason"`
}

// SubmitResult is the outcome of an explicit submission. Both lists are
// always present.
type SubmitResult struct {
	SubmittedFileIDs []string `json:"submittedFileIDs"`
	Skipped          []Skip   `json:"skipped"`
}

// normalizeIDs trims, drops blanks and removes duplicates, keeping order.
func normalizeIDs(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, id := range in {
		id = strings.TrimSpace(id)
		if id == "" || seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}

// requireAvailable returns the live configuration when indexing can run, else
// the documented error: not configured (disabled, model or host missing) or
// unavailable (anything else).
func (s *Service) requireAvailable(ctx context.Context) (Config, error) {
	cfg := s.config(ctx)
	av := s.availabilityFor(ctx, cfg)
	switch {
	case av.OK:
		return cfg, nil
	case av.NotConfigured:
		return cfg, ErrNotConfigured.WithCause(fmt.Errorf("%s", av.Reason))
	}
	return cfg, ErrUnavailable.WithCause(fmt.Errorf("%s", av.Reason))
}

// decision is the per-file verdict of an explicit submission.
type decision struct {
	// reason is the skip reason ("" when the file is accepted).
	reason string
	// reextract is set for administrators when the text must be extracted
	// again before the index is built.
	reextract bool
}

// reextractable reports whether an administrator may send the file back through
// text extraction: processing failed, extraction found nothing, or it has been
// waiting in uploaded/queued for 30 minutes.
func reextractable(f File, now time.Time, includeEmpty bool) bool {
	switch {
	case f.ProcessingStatus == procFailed:
		return true
	case f.ExtractStatus == extractEmpty:
		return includeEmpty
	}
	return (f.ProcessingStatus == procUploaded || f.ProcessingStatus == procQueued) && now.Sub(f.UpdatedAt) >= stalledAfter
}

// decide applies the submission rules in order: already indexed, already in
// flight, not indexable, text not ready.
func (s *Service) decide(f File, cfg Config, sig string, now time.Time, admin bool) decision {
	st := f.state()
	same := f.EmbedSignature == sig
	switch {
	case st == StateReady && same:
		return decision{reason: SkipAlreadyDone}
	case (st == StateQueued || st == StateProcessing) && same && !stalled(f, now):
		return decision{reason: SkipProcessing}
	case !s.indexable(f, cfg):
		return decision{reason: SkipUnsupported}
	case !f.ProcessingReady:
		if admin && reextractable(f, now, true) {
			return decision{reextract: true}
		}
		return decision{reason: SkipNotReady}
	}
	return decision{}
}

// Submit queues the caller's own active files for indexing.
func (s *Service) Submit(ctx context.Context, ownerID uint, ids []string) (SubmitResult, error) {
	return s.submit(ctx, ownerID, false, ids)
}

// SubmitAsAdmin queues files of any user by public id; files without usable
// text are sent back through extraction first. Tasks stay attributed to the
// file owner.
func (s *Service) SubmitAsAdmin(ctx context.Context, ids []string) (SubmitResult, error) {
	return s.submit(ctx, 0, true, ids)
}

func (s *Service) submit(ctx context.Context, ownerID uint, admin bool, ids []string) (SubmitResult, error) {
	res := SubmitResult{SubmittedFileIDs: []string{}, Skipped: []Skip{}}
	ids = normalizeIDs(ids)
	if len(ids) == 0 {
		return res, nil
	}
	if len(ids) > maxSubmitFiles {
		return res, ErrTooManyFiles
	}
	cfg, err := s.requireAvailable(ctx)
	if err != nil {
		return res, err
	}
	sig := cfg.Signature()
	q := s.files(ctx).Where("file_id IN ? AND status = ?", ids, fileActive)
	if !admin {
		q = q.Where("user_id = ?", ownerID)
	}
	var rows []File
	if err := q.Find(&rows).Error; err != nil {
		return res, ErrSubmitFailed.WithCause(err)
	}
	byID := make(map[string]File, len(rows))
	for _, r := range rows {
		byID[r.FileID] = r
	}
	now := s.now()
	for _, id := range ids {
		f, ok := byID[id]
		if !ok {
			res.Skipped = append(res.Skipped, Skip{FileID: id, Reason: SkipNotFound})
			continue
		}
		d := s.decide(f, cfg, sig, now, admin)
		if d.reason != "" {
			res.Skipped = append(res.Skipped, Skip{FileID: id, Reason: d.reason})
			continue
		}
		var reason string
		if d.reextract {
			reason = s.submitReextract(ctx, f, cfg, sig, now, true)
		} else {
			reason = s.submitIndex(ctx, f, cfg, sig, now)
		}
		if reason != "" {
			res.Skipped = append(res.Skipped, Skip{FileID: id, Reason: reason})
			continue
		}
		res.SubmittedFileIDs = append(res.SubmittedFileIDs, id)
	}
	return res, nil
}

// submitIndex atomically registers the file as queued under the signature and
// puts the message on the queue. It returns a skip reason, or "" on success.
func (s *Service) submitIndex(ctx context.Context, f File, cfg Config, sig string, now time.Time) string {
	r := s.files(ctx).Where("id = ? AND status = ?", f.ID, fileActive).
		Where("NOT (COALESCE(embed_signature,'') = ? AND (embed_status = ? OR (embed_status IN ? AND updated_at > ?)))",
			sig, StateReady, inFlight, now.Add(-stalledAfter)).
		UpdateColumns(map[string]any{"embed_status": StateQueued, "embed_signature": sig, "embed_error": "", "updated_at": now})
	if r.Error != nil {
		return SkipFailed
	}
	if r.RowsAffected == 0 {
		return SkipProcessing
	}
	if err := s.enqueue(ctx, jobIndex, f, sig, cfg.host()); err != nil {
		s.log.Warn("rag_enqueue_failed", errAttr(err))
		s.failFile(ctx, f.ID, sig, fmt.Errorf("%w: queue", errQueue))
		return SkipQueueBusy
	}
	return ""
}

// submitReextract atomically resets the file's text processing, leaves an index
// request on it and puts a message on the file-processing queue. It returns a
// skip reason, or "" on success.
func (s *Service) submitReextract(ctx context.Context, f File, cfg Config, sig string, now time.Time, includeEmpty bool) string {
	ok, err := s.resetForReextract(ctx, f.ID, sig, now, includeEmpty)
	if err != nil {
		return SkipFailed
	}
	if !ok {
		return SkipProcessing
	}
	if err := s.enqueue(ctx, jobExtract, f, sig, cfg.host()); err != nil {
		s.log.Warn("rag_enqueue_failed", errAttr(err))
		s.failProcessing(ctx, f, "queue_unavailable", "processing queue unavailable")
		return SkipQueueBusy
	}
	return ""
}

// resetForReextract moves a file to processing "queued" with an index request.
// It does nothing when another request already reset it or it is extracting.
func (s *Service) resetForReextract(ctx context.Context, id uint, sig string, now time.Time, includeEmpty bool) (bool, error) {
	cutoff := now.Add(-stalledAfter)
	eligible := "(processing_status = 'failed' OR (processing_status IN ('uploaded','queued') AND updated_at <= ?)"
	args := []any{cutoff}
	if includeEmpty {
		eligible += " OR extract_status = 'empty'"
	}
	eligible += ")"
	r := s.files(ctx).Where("id = ? AND status = ? AND processing_status <> ?", id, fileActive, procExtracting).
		Where(eligible, args...).
		UpdateColumns(map[string]any{
			"processing_status": procQueued, "processing_ready": false, "processing_error_code": "",
			"processing_error_message": "", "extract_status": extractNone,
			"embed_status": StateQueued, "embed_signature": sig, "embed_error": "", "updated_at": now,
		})
	return r.RowsAffected > 0, r.Error
}

// Vectorization tells whether an explicit submission of the file would be
// accepted now and, when not, why. Files whose index belongs to an older vector
// space report outdated_index.
func (s *Service) Vectorization(ctx context.Context, f File) (bool, string) {
	cfg := s.config(ctx)
	av := s.availabilityFor(ctx, cfg)
	if !av.OK {
		return false, av.Reason
	}
	sig := cfg.Signature()
	d := s.decide(f, cfg, sig, s.now(), false)
	if d.reason != "" {
		return false, d.reason
	}
	if st := f.state(); st == StateStale || (st == StateReady && f.EmbedSignature != sig) {
		return true, ReasonOutdatedIndex
	}
	return true, ""
}

// VectorizationOf is Vectorization for a file given by owner and public id;
// ok is false when the user has no such active file.
func (s *Service) VectorizationOf(ctx context.Context, ownerID uint, fileID string) (can bool, reason string, ok bool, err error) {
	var f File
	if err := s.files(ctx).Where("file_id = ? AND user_id = ? AND status = ?", fileID, ownerID, fileActive).Take(&f).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return false, SkipNotFound, false, nil
		}
		return false, "", false, err
	}
	can, reason = s.Vectorization(ctx, f)
	return can, reason, true, nil
}
