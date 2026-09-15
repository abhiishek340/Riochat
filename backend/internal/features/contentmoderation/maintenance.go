package contentmoderation

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/deps"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	fastLoopInterval    = 10 * time.Second
	cleanupInterval     = 6 * time.Hour
	staleRunAge         = 2 * time.Minute
	startupBudget       = 2 * time.Minute
	cleanupBatchSize    = 200
	cleanupMaxPasses    = 50
	blockedFileRetryMax = 200
	fastPassBudget      = 45 * time.Second
	fullPassBudget      = 5 * time.Minute
)

// contentHeld is the SQL condition for events that still hold flagged content.
const contentHeld = "(encrypted_text <> '' OR image_count > 0 OR (image_meta_json <> '[]' AND image_meta_json <> ''))"

// contentCleared is its complement.
const contentCleared = "(encrypted_text = '' AND image_count = 0 AND (image_meta_json = '[]' OR image_meta_json = ''))"

// startBackground wires the startup pass, the maintenance loops and shutdown.
func (s *Service) startBackground(d deps.Deps) {
	d.Lifecycle.OnStart("contentmoderation-startup", func(ctx context.Context) error {
		ctx, cancel := context.WithTimeout(ctx, startupBudget)
		defer cancel()
		s.RunMaintenance(ctx, true)
		return nil
	})
	d.Lifecycle.Go("contentmoderation-maintenance", func(ctx context.Context) {
		fast := time.NewTicker(fastLoopInterval)
		slow := time.NewTicker(cleanupInterval)
		defer fast.Stop()
		defer slow.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-fast.C:
				s.runBudgeted(ctx, false, fastPassBudget)
			case <-slow.C:
				s.runBudgeted(ctx, true, fullPassBudget)
			}
		}
	})
	d.Lifecycle.OnStop("contentmoderation-executor", func(context.Context) error {
		s.Close()
		return nil
	})
}

// Close stops accepting checks and waits for running ones.
func (s *Service) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.exec.stop()
}

func (s *Service) runBudgeted(ctx context.Context, full bool, budget time.Duration) {
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	s.RunMaintenance(ctx, full)
}

// RunMaintenance performs the periodic work. The fast pass retries unpersisted
// blocks, recovers stale runs and retries blocked-file deletes; the full pass
// adds content retention cleanup.
func (s *Service) RunMaintenance(ctx context.Context, full bool) {
	if full {
		if err := s.CleanupExpired(ctx); err != nil {
			s.log.Error("content_moderation_cleanup_failed", errField(err))
		}
	}
	s.RetryPendingBlocks(ctx)
	if err := s.RecoverStaleRuns(ctx); err != nil {
		s.log.Error("content_moderation_recover_failed", errField(err))
	}
	s.retryBlockedFiles(ctx)
}

// parseImageMeta reads an event's image metadata; ok is false when the stored
// text cannot be parsed.
func parseImageMeta(raw string) (metas []imageMeta, ok bool) {
	if strings.TrimSpace(raw) == "" {
		return nil, true
	}
	if err := json.Unmarshal([]byte(raw), &metas); err != nil {
		return nil, false
	}
	return metas, true
}

func (s *Service) retryBlockedFiles(ctx context.Context) {
	if s.files == nil {
		return
	}
	if err := s.files.RetryBlockedDeletes(ctx, blockedFileRetryMax); err != nil {
		s.log.Warn("content_moderation_blocked_file_retry_failed", errField(err))
	}
}

// RetryPendingBlocks reapplies blocks whose database write failed earlier.
func (s *Service) RetryPendingBlocks(ctx context.Context) {
	s.trackMu.Lock()
	specs := make([]BlockSpec, 0, len(s.pendingBlocks))
	for _, sp := range s.pendingBlocks {
		specs = append(specs, sp)
	}
	s.trackMu.Unlock()
	for _, sp := range specs {
		s.persistBlock(ctx, sp)
	}
}

// PendingBlocks reports how many blocks still wait for persistence.
func (s *Service) PendingBlocks() int {
	s.trackMu.Lock()
	defer s.trackMu.Unlock()
	return len(s.pendingBlocks)
}

// CleanupExpired enforces the retention rules: expired flagged content is
// deleted first, then event rows whose content is gone and whose metadata
// retention passed, then old statistics.
func (s *Service) CleanupExpired(ctx context.Context) error {
	failed := map[uint]bool{}
	for pass := 0; pass < cleanupMaxPasses; pass++ {
		q := s.db.WithContext(ctx).Where("content_expires_at <= ? AND "+contentHeld, s.clock.Now())
		if len(failed) > 0 {
			ids := make([]uint, 0, len(failed))
			for id := range failed {
				ids = append(ids, id)
			}
			q = q.Where("id NOT IN ?", ids)
		}
		var rows []Event
		if err := q.Order("id ASC").Limit(cleanupBatchSize).Find(&rows).Error; err != nil {
			return err
		}
		if len(rows) == 0 {
			break
		}
		cleared := 0
		for i := range rows {
			if s.clearEventContent(ctx, &rows[i]) {
				cleared++
			} else {
				failed[rows[i].ID] = true
			}
		}
		if cleared == 0 {
			break
		}
	}
	err := s.db.WithContext(ctx).Unscoped().
		Where("metadata_expires_at <= ? AND "+contentCleared, s.clock.Now()).
		Delete(&Event{}).Error
	if err != nil {
		return err
	}
	return s.pruneStats(ctx)
}

// clearEventContent deletes an event's isolated objects and, only when all of
// them are gone, wipes its content fields.
func (s *Service) clearEventContent(ctx context.Context, e *Event) bool {
	metas, parsed := parseImageMeta(e.ImageMetaJSON)
	if !parsed {
		s.log.Warn("content_moderation_image_meta_unreadable", logx.F("event", e.PublicID))
		return false
	}
	if len(metas) > 0 || e.ImageCount > 0 {
		if s.objects == nil {
			return false
		}
		for _, m := range metas {
			if err := s.objects.Delete(ctx, m.StoragePath); err != nil {
				s.log.Warn("content_moderation_isolated_delete_failed", logx.F("event", e.PublicID), errField(err))
				return false
			}
		}
	}
	err := s.db.WithContext(ctx).Unscoped().Model(&Event{}).Where("id = ?", e.ID).Updates(map[string]any{
		"encrypted_text": "", "image_count": 0, "image_meta_json": "[]", "content_summary": "",
	}).Error
	if err != nil {
		s.log.Error("content_moderation_clear_content_failed", errField(err))
		return false
	}
	return true
}

// DeleteEventsBefore removes events created before cutoff (the admin log
// cleanup of type "moderation"). Events whose isolated images cannot be deleted
// are kept. It returns the number of rows removed.
func (s *Service) DeleteEventsBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	var withImages []Event
	err := s.db.WithContext(ctx).Unscoped().
		Where("created_at < ? AND image_count > 0", cutoff).Find(&withImages).Error
	if err != nil {
		return 0, err
	}
	keep := []uint{}
	for i := range withImages {
		for _, m := range imagesFromJSON(withImages[i].ImageMetaJSON) {
			if s.objects == nil {
				keep = append(keep, withImages[i].ID)
				break
			}
			if derr := s.objects.Delete(ctx, m.StoragePath); derr != nil {
				keep = append(keep, withImages[i].ID)
				break
			}
		}
	}
	q := s.db.WithContext(ctx).Unscoped().Where("created_at < ?", cutoff)
	if len(keep) > 0 {
		q = q.Where("id NOT IN ?", keep)
	}
	res := q.Delete(&Event{})
	return res.RowsAffected, res.Error
}

// RecoverStaleRuns settles runs that were left moderating or pending by a
// process that no longer tracks them.
func (s *Service) RecoverStaleRuns(ctx context.Context) error {
	if s.runs == nil {
		return nil
	}
	refs, err := s.runs.StaleRuns(ctx, s.clock.Now().Add(-staleRunAge))
	if err != nil {
		return err
	}
	var firstErr error
	for _, ref := range refs {
		if err := s.RecoverRun(ctx, ref); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// RecoverRun settles one stuck run unless this process is still tracking it.
// Chat code also calls it when a client resumes a run in state moderating or
// pending.
func (s *Service) RecoverRun(ctx context.Context, ref RunRef) error {
	if s.isTracked(ref.RunID) {
		return nil
	}
	var hit Event
	err := s.db.WithContext(ctx).Where("run_id = ? AND result = ?", ref.RunID, ResultHit).
		Order("id DESC").First(&hit).Error
	switch {
	case err == nil:
		b := blockInfo{EventID: hit.PublicID, Direction: hit.Direction, Categories: categoriesFromJSON(hit.CategoriesJSON)}
		spec := BlockSpec{RunID: ref.RunID, EventID: b.EventID, Direction: b.Direction, Categories: b.Categories}
		// A block remembered by this process already told the client (or will,
		// when its retry lands): recovery must not emit a second terminal event.
		s.trackMu.Lock()
		_, hadPending := s.pendingBlocks[ref.RunID]
		already := s.notified[ref.RunID] || hadPending
		s.trackMu.Unlock()
		if !s.persistBlock(ctx, spec) {
			return errors.New("content moderation: block could not be persisted yet")
		}
		if !already {
			s.announceBlock(ctx, ref.RunID, b, "")
		}
		return nil
	case !database.IsNotFound(err):
		// A storage failure must never turn a possibly known hit into failed-open.
		return err
	}
	s.recordWorkerLost(ctx, ref)
	if s.runs != nil {
		if err := s.runs.SetRunState(ctx, ref.RunID, StateFailedOpen, "", ""); err != nil {
			return err
		}
	}
	return nil
}

// recordWorkerLost notes that the checks of a run were lost with their process.
// Only the run id is known to this path, so the event carries no user,
// conversation or message ids, and no daily statistic is counted.
func (s *Service) recordWorkerLost(ctx context.Context, ref RunRef) {
	cfg, cfgErr := s.currentConfig(ctx)
	now := s.clock.Now()
	e := &Event{
		PublicID: newPublicID(), RunID: ref.RunID,
		Direction: DirectionOutput, Modality: ModalityText, Result: ResultFailedOpen,
		CategoriesJSON: "[]", CategoryScoresJSON: "{}", ContentLocationJSON: "{}", ImageMetaJSON: "[]",
		ErrorCode: CodeWorkerLost, ErrorMessage: truncate(publicTextFor(CodeWorkerLost), 255),
		ContentExpiresAt: now.Add(contentRetention), MetadataExpiresAt: now.Add(metadataRetention),
	}
	if cfgErr == nil {
		e.ModelName, e.PolicyVersion = cfg.Model, cfg.Policy.Version
	}
	if err := s.createEvent(ctx, e); err != nil {
		s.log.Error("content_moderation_event_write_failed", errField(err))
	}
}
