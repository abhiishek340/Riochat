package contentmoderation

import (
	"context"
	"time"

	"github.com/riochat/riochat/backend/internal/platform/logx"
	"github.com/riochat/riochat/backend/internal/platform/reqctx"
)

const (
	blockPersistTimeout = 15 * time.Second
	fileDeleteTimeout   = 5 * time.Second
)

// setRunState records a run's moderation state; ephemeral runs have no record.
func (s *Service) setRunState(ctx context.Context, r *Run, state, eventID, categoriesJSON string) {
	if r.info.Ephemeral || s.runs == nil {
		return
	}
	pctx, cancel := s.persistCtx(ctx)
	defer cancel()
	if err := s.runs.SetRunState(pctx, r.info.RunID, state, eventID, categoriesJSON); err != nil {
		s.log.Warn("content_moderation_run_state_failed", logx.F("state", state), errField(err))
	}
}

func (s *Service) emit(ctx context.Context, runID string, ev StreamEvent) {
	if s.notifier != nil {
		s.notifier.Emit(ctx, runID, ev)
	}
}

// applyBlock withdraws the turn and tells the client. It is idempotent: a block
// already applied is repeated only when a late input hit upgrades an earlier
// output-only block.
func (s *Service) applyBlock(ctx context.Context, r *Run, b blockInfo) {
	r.applyMu.Lock()
	defer r.applyMu.Unlock()
	if r.applied != nil && !(r.applied.Direction == DirectionOutput && b.Direction == DirectionInput) {
		return
	}
	cp := b
	r.applied = &cp
	if !r.info.Ephemeral {
		spec := BlockSpec{RunID: r.info.RunID, EventID: b.EventID, Direction: b.Direction, Categories: b.Categories}
		if !s.persistBlock(ctx, spec) && b.EventID != "" {
			// Keep the run findable by stale-run recovery, which reapplies the
			// block from the stored hit event: back to moderating, with the
			// event id and categories saved.
			s.setRunState(ctx, r, StateModerating, b.EventID, categoriesToJSON(b.Categories))
		}
	}
	billed := ""
	if b.Direction == DirectionOutput && r.info.PaidReservation {
		billed = BilledReasonUpstreamUsage
	}
	s.announceBlock(reqctx.Detach(ctx), r.info.RunID, b, billed)
}

// persistBlock applies the block in the database. When that fails the block is
// remembered and retried by the background loop.
func (s *Service) persistBlock(ctx context.Context, spec BlockSpec) bool {
	if s.runs == nil {
		return true
	}
	pctx, cancel := detachedCtx(ctx, blockPersistTimeout)
	defer cancel()
	ids, err := s.runs.ApplyBlock(pctx, spec)
	if err != nil {
		s.log.Error("content_moderation_block_persist_failed", logx.F("run_id", spec.RunID), errField(err))
		s.trackMu.Lock()
		s.pendingBlocks[spec.RunID] = spec
		s.trackMu.Unlock()
		return false
	}
	s.trackMu.Lock()
	delete(s.pendingBlocks, spec.RunID)
	s.trackMu.Unlock()
	s.deleteBlockedFiles(ctx, ids)
	return true
}

// announceBlock discards replayable stream content and emits the terminal
// moderation_blocked event.
func (s *Service) announceBlock(ctx context.Context, runID string, b blockInfo, billedReason string) {
	if s.notifier == nil {
		return
	}
	s.notifier.DiscardRetained(ctx, runID)
	s.notifier.Emit(ctx, runID, StreamEvent{
		Type: EventTypeBlocked, EventID: b.EventID, Direction: b.Direction,
		Categories: nonNilStrings(b.Categories), BilledReason: billedReason,
	})
	s.trackMu.Lock()
	s.notified[runID] = true
	s.trackMu.Unlock()
}

func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// deleteBlockedFiles removes the physical objects of withdrawn files on a short
// budget; whatever is left is retried by the maintenance loop.
func (s *Service) deleteBlockedFiles(ctx context.Context, ids []uint) {
	if s.files == nil || len(ids) == 0 {
		return
	}
	dctx, cancel := detachedCtx(ctx, fileDeleteTimeout)
	defer cancel()
	if err := s.files.DeleteObjects(dctx, ids); err != nil {
		s.log.Warn("content_moderation_blocked_file_delete_failed", errField(err))
	}
}
