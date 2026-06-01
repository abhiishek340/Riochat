package ragindex

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// leaseFor is how long a consumer holds a job before another may take over.
const leaseFor = runLimit + time.Minute

// insertJob stores a queue message, replacing any pending message of the same
// kind for the file.
func (s *Service) insertJob(ctx context.Context, j *Job) error {
	now := s.now()
	j.CreatedAt, j.UpdatedAt = now, now
	if j.AvailableAt.IsZero() {
		j.AvailableAt = now
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("file_obj_id = ? AND kind = ?", j.FileObjID, j.Kind).Delete(&Job{}).Error; err != nil {
			return err
		}
		return tx.Create(j).Error
	})
}

func (s *Service) enqueue(ctx context.Context, kind string, f File, sig, host string) error {
	j := &Job{Kind: kind, UserID: f.UserID, FileObjID: f.ID, FileID: f.FileID, Signature: sig, Host: host}
	if err := s.enqueueFn(ctx, j); err != nil {
		return err
	}
	s.notify()
	return nil
}

// nextJob takes the oldest job that is due and not leased. A job whose earlier
// lease expired has Delivered > 0: the caller is taking over.
func (s *Service) nextJob(ctx context.Context) (*Job, bool) {
	now := s.now()
	for attempt := 0; attempt < 5; attempt++ {
		var j Job
		err := s.db.WithContext(ctx).
			Where("available_at <= ? AND (lease_until IS NULL OR lease_until <= ?)", now, now).
			Order("id").Take(&j).Error
		if err != nil {
			if !errors.Is(err, gorm.ErrRecordNotFound) && ctx.Err() == nil {
				s.log.Warn("rag_queue_poll_failed", logx.Err(err))
			}
			return nil, false
		}
		res := s.db.WithContext(ctx).Model(&Job{}).
			Where("id = ? AND delivered = ? AND (lease_until IS NULL OR lease_until <= ?)", j.ID, j.Delivered, now).
			UpdateColumns(map[string]any{"delivered": j.Delivered + 1, "lease_until": now.Add(leaseFor), "updated_at": now})
		if res.Error != nil {
			return nil, false
		}
		if res.RowsAffected == 1 {
			return &j, true
		}
	}
	return nil, false
}

func (s *Service) deleteJob(ctx context.Context, id uint) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	if err := s.db.WithContext(sc).Delete(&Job{}, id).Error; err != nil {
		s.log.Warn("rag_job_delete_failed", logx.Err(err))
	}
}

// releaseJob hands a job back to the queue untouched (shutdown, no slot).
func (s *Service) releaseJob(ctx context.Context, j *Job) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	s.db.WithContext(sc).Model(&Job{}).Where("id = ?", j.ID).
		UpdateColumns(map[string]any{"lease_until": nil, "delivered": j.Delivered, "updated_at": s.now()})
}

// processNext handles one due job and reports whether there was one.
func (s *Service) processNext(ctx context.Context) bool {
	j, ok := s.nextJob(ctx)
	if !ok {
		return false
	}
	switch j.Kind {
	case jobExtract:
		s.handleExtract(ctx, j)
	default:
		s.handleIndex(ctx, j)
	}
	return true
}

// DrainQueue processes due jobs until none is left. It is the synchronous
// equivalent of what the queue workers do and is used by tests and tools.
func (s *Service) DrainQueue(ctx context.Context) int {
	n := 0
	for ctx.Err() == nil && s.processNext(ctx) {
		n++
	}
	return n
}

// handleIndex runs one index message.
func (s *Service) handleIndex(ctx context.Context, j *Job) {
	takeover := j.Delivered > 0
	if !s.acquire(ctx) {
		s.releaseJob(ctx, j)
		return
	}
	defer s.release()
	rctx, cancel := context.WithTimeout(ctx, runLimit)
	_, err := s.runIndex(rctx, j.FileObjID, j.Signature, j.Host, runOpts{takeover: takeover})
	limitHit := errors.Is(rctx.Err(), context.DeadlineExceeded)
	cancel()
	if err == nil {
		s.deleteJob(ctx, j.ID)
		return
	}
	if ctx.Err() != nil {
		// Shutting down: give the file and the message back.
		sc, c := settleCtx(ctx)
		defer c()
		_, _ = s.transition(sc, nil, j.FileObjID, j.Signature, []string{StateProcessing}, map[string]any{"embed_status": StateQueued})
		s.releaseJob(ctx, j)
		return
	}
	if limitHit {
		err = context.DeadlineExceeded
	}
	s.retryOrFail(ctx, j, err)
}

// retryOrFail reschedules a failed message, showing the file as queued with the
// failure summary, or fails the file after the third attempt.
func (s *Service) retryOrFail(ctx context.Context, j *Job, err error) {
	s.log.Warn("rag_index_failed", logx.F("file_id", j.FileID), logx.F("attempt", j.Attempts+1), logx.Err(err))
	attempts := j.Attempts + 1
	sc, cancel := settleCtx(ctx)
	defer cancel()
	if retriable(err) && attempts < maxAttempts {
		if _, e := s.transition(sc, nil, j.FileObjID, j.Signature, []string{StateProcessing, StateQueued}, map[string]any{
			"embed_status": StateQueued, "embed_error": truncateRunes(summarize(err), 255),
		}); e != nil {
			s.log.Warn("rag_requeue_failed", logx.Err(e))
		}
		s.db.WithContext(sc).Model(&Job{}).Where("id = ?", j.ID).UpdateColumns(map[string]any{
			"attempts": attempts, "delivered": 0, "lease_until": nil,
			"available_at": s.now().Add(time.Duration(attempts) * retryBackoff), "updated_at": s.now(),
		})
		return
	}
	s.failFile(ctx, j.FileObjID, j.Signature, err)
	s.deleteJob(ctx, j.ID)
}

// handleExtract runs one re-extraction message: the text is extracted again and
// the pending index request on the file is then honoured.
func (s *Service) handleExtract(ctx context.Context, j *Job) {
	defer s.deleteJob(ctx, j.ID)
	var f File
	if err := s.files(ctx).Where("id = ? AND status = ?", j.FileObjID, fileActive).Take(&f).Error; err != nil {
		return
	}
	if s.content == nil {
		s.failProcessing(ctx, f, "extract_unavailable", "text extraction is unavailable")
		return
	}
	if err := s.files(ctx).Where("id = ?", f.ID).UpdateColumns(map[string]any{
		"processing_status": procExtracting, "updated_at": s.now(),
	}).Error; err != nil {
		return
	}
	text, err := s.content.Text(ctx, f)
	if err != nil {
		s.log.Warn("rag_reextract_failed", logx.F("file_id", f.FileID), logx.Err(err))
		s.failProcessing(ctx, f, "extract_failed", "text extraction failed")
		return
	}
	set := map[string]any{
		"processing_status": procReady, "processing_ready": true, "processing_error_code": "",
		"processing_error_message": "", "extract_status": extractReady, "extract_text": text, "updated_at": s.now(),
	}
	if strings.TrimSpace(text) == "" {
		set["extract_status"], set["processing_ready"], set["extract_text"] = extractEmpty, false, ""
	}
	if err := s.files(ctx).Where("id = ?", f.ID).UpdateColumns(set).Error; err != nil {
		return
	}
	s.afterProcessing(ctx, f.ID, true, true)
}

// failProcessing records a failed text extraction and reverts a pending index
// request to "never indexed".
func (s *Service) failProcessing(ctx context.Context, f File, code, msg string) {
	sc, cancel := settleCtx(ctx)
	defer cancel()
	s.files(sc).Where("id = ?", f.ID).UpdateColumns(map[string]any{
		"processing_status": procFailed, "processing_ready": false, "processing_error_code": code,
		"processing_error_message": msg, "updated_at": s.now(),
	})
	s.files(sc).Where("id = ? AND embed_status = ?", f.ID, StateQueued).
		UpdateColumns(map[string]any{"embed_status": StateNone, "embed_error": ""})
}

// AfterProcessing is called when text processing of a file finished (ok) or
// failed. It starts the background index when auto-indexing applies, or honours
// an explicit index request left on the file. It never blocks.
func (s *Service) AfterProcessing(ctx context.Context, fileObjID uint, ok bool) {
	s.afterProcessing(ctx, fileObjID, ok, false)
}

// afterProcessing is AfterProcessing; inline runs the index in the caller
// (a queue worker) instead of a background task.
func (s *Service) afterProcessing(ctx context.Context, fileObjID uint, ok, inline bool) {
	var f File
	if err := s.files(ctx).Where("id = ? AND status = ?", fileObjID, fileActive).Take(&f).Error; err != nil {
		return
	}
	requested := f.state() == StateQueued && f.EmbedSignature != ""
	if !ok {
		if requested {
			sc, cancel := settleCtx(ctx)
			defer cancel()
			s.files(sc).Where("id = ? AND embed_status = ?", f.ID, StateQueued).
				UpdateColumns(map[string]any{"embed_status": StateNone, "embed_error": ""})
		}
		return
	}
	cfg := s.config(ctx)
	sig := cfg.Signature()
	switch {
	case requested:
		if av := s.availabilityFor(ctx, cfg); !av.OK {
			msg := errServiceDownMsg
			if av.NotConfigured {
				msg = errServiceConf
			}
			s.files(ctx).Where("id = ? AND embed_status = ?", f.ID, StateQueued).
				UpdateColumns(map[string]any{"embed_status": StateFailed, "embed_error": msg, "updated_at": s.now()})
			return
		}
		if f.EmbedSignature != sig {
			s.files(ctx).Where("id = ? AND embed_status = ?", f.ID, StateQueued).
				UpdateColumns(map[string]any{"embed_status": StateStale, "embed_error": errConfigResubmit, "updated_at": s.now()})
			return
		}
	case cfg.TriggerOnUpload && f.state() == StateNone:
		if !s.availabilityFor(ctx, cfg).OK || !s.indexable(f, cfg) {
			return
		}
	default:
		return
	}
	run := func(rctx context.Context) { s.runAfterUpload(rctx, f.ID, sig, cfg.Host) }
	if inline {
		run(ctx)
		return
	}
	if s.life != nil {
		s.life.Detach(ctx, "rag-index-after-upload", runLimit+time.Minute, run)
		return
	}
	run(context.WithoutCancel(ctx))
}

// runAfterUpload indexes one file in the background after its text became
// available. A failure leaves the text processing ready with the retrieval
// flag off and the summary as the processing error.
func (s *Service) runAfterUpload(ctx context.Context, id uint, sig, host string) {
	if !s.acquire(ctx) {
		return
	}
	defer s.release()
	rctx, cancel := context.WithTimeout(ctx, runLimit)
	defer cancel()
	_, err := s.runIndex(rctx, id, sig, host, runOpts{fromUpload: true})
	if err != nil {
		s.failFile(ctx, id, sig, err)
		s.endUploadRun(ctx, id, summarize(err))
		return
	}
	s.endUploadRun(ctx, id, "")
}

// Sweep fails every file that has been processing for longer than the run
// limit plus a minute (its worker died). A file whose text processing was still
// "embedding" returns to ready with the embed_failed error code. It returns the
// number of files recovered.
func (s *Service) Sweep(ctx context.Context) (int, error) {
	cutoff := s.now().Add(-stallSweepAge)
	var rows []File
	if err := s.files(ctx).Select("id", "embed_signature", "processing_status").
		Where("status = ? AND embed_status = ? AND updated_at < ?", fileActive, StateProcessing, cutoff).
		Find(&rows).Error; err != nil {
		return 0, database.Translate(err)
	}
	n := 0
	for _, f := range rows {
		ok, err := s.transition(ctx, nil, f.ID, f.EmbedSignature, []string{StateProcessing}, map[string]any{
			"embed_status": StateFailed, "embed_error": errStalled,
		})
		if err != nil {
			return n, err
		}
		if !ok {
			continue
		}
		n++
		if f.ProcessingStatus == procEmbedding {
			s.endUploadRun(ctx, f.ID, errStalled)
		}
	}
	return n, nil
}

// Reconcile marks every active file whose index belongs to another vector
// space as stale: a queued, processing or ready file without a chunk carrying
// the current signature. Files queued or processing under the current
// signature are in-flight tasks of this space and stay. It is idempotent.
func (s *Service) Reconcile(ctx context.Context) error {
	sig := s.config(ctx).Signature()
	noChunk := "NOT EXISTS (SELECT 1 FROM file_chunks c WHERE c.file_obj_id = file_objects.id AND c.embedding_signature = ?)"
	return s.files(ctx).
		Where("status = ? AND embed_status IN ?", fileActive, []string{StateQueued, StateProcessing, StateReady}).
		Where(noChunk, sig).
		Where("(embed_status = ? OR COALESCE(embed_signature,'') <> ?)", StateReady, sig).
		UpdateColumns(map[string]any{"embed_status": StateStale, "embed_error": errReindexRequired, "updated_at": s.now()}).Error
}
