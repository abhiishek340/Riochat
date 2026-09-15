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
