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
