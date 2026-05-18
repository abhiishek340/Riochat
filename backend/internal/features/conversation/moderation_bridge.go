package conversation

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// ModerationBridge is the narrow door through which the content-moderation
// engine reaches runs, messages, files and the live run streams. The engine
// lives in another area and declares the interfaces it needs; the integrator
// adapts this bridge to them. The bridge speaks only in primitive values.
type ModerationBridge struct{ s *service }

// Moderation errors and values shared with the adapter.
var (
	// ErrModerationNotImage means a file is not an image and must be skipped.
	ErrModerationNotImage = errors.New("conversation: file is not an image")
	// ErrModerationFileTooLarge means the stored image exceeds the check limit.
	ErrModerationFileTooLarge = errors.New("conversation: image is larger than 20 MiB")
)

// Values written to blocked rows.
const (
	// BlockedErrorCode is the error code of a message withdrawn by moderation.
	BlockedErrorCode = "content_moderation.blocked"
	// BlockedErrorMessage is its error text.
	BlockedErrorMessage = "content blocked by moderation"

	fileStatusModerationBlocked = "moderation_blocked"

	moderationMaxImageBytes = 20 << 20
	// moderationStaleBatch bounds the stale runs listed per recovery pass.
	moderationStaleBatch = 100
)

// StaleRun is a run whose moderation never settled.
type StaleRun struct {
	RunID           string
	UserID          uint
	ConversationID  uint
	MessageID       uint
	MessagePublicID string
}

// SetRunState records the moderation state of a run. Empty eventID and
// categoriesJSON leave the stored values alone, so a recovery never clears
// what an earlier step saved. The state "blocked" also marks the run blocked.
func (b *ModerationBridge) SetRunState(ctx context.Context, runID, state, eventID, categoriesJSON string) error {
	upd := map[string]any{"moderation_state": state}
	if eventID != "" {
		upd["moderation_event_id"] = eventID
	}
	if strings.TrimSpace(categoriesJSON) != "" {
		upd["moderation_categories_json"] = categoriesJSON
	}
	if state == "blocked" {
		upd["status"] = runBlocked
	}
	return b.s.db.WithContext(ctx).Model(&Run{}).Where("run_id = ?", runID).Updates(upd).Error
}

// ApplyBlock withdraws a blocked turn in one transaction. Assistant messages
// are always blocked and emptied; user messages only when direction is input.
// It returns the ids of file rows whose stored objects must now be deleted. A
// missing run row fails the whole transaction.
func (b *ModerationBridge) ApplyBlock(ctx context.Context, runID, eventID, direction string, categories []string) ([]uint, error) {
	s := b.s
	cats := marshalNoEscape(orEmptyStrings(categories))
	var fileIDs []uint
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var run Run
		if err := tx.Where("run_id = ?", runID).First(&run).Error; err != nil {
			if database.IsNotFound(err) {
				return fmt.Errorf("moderation block: run %s not found", runID)
			}
			return err
		}
		var msgs []Message
		if err := tx.Where("run_id = ?", runID).Find(&msgs).Error; err != nil {
			return err
		}
		var asstIDs, userIDs []uint
		for _, m := range msgs {
			switch m.Role {
			case roleAssistant:
				asstIDs = append(asstIDs, m.ID)
			case roleUser:
				if direction == dirInput {
					userIDs = append(userIDs, m.ID)
				}
			}
		}
		if len(asstIDs) > 0 {
			var atts []Attachment
			if err := tx.Where("message_id IN ? AND status <> ?", asstIDs, fileStatusDeleted).Find(&atts).Error; err != nil {
				return err
			}
			var publicIDs []string
			for _, a := range atts {
				publicIDs = append(publicIDs, a.FileID)
			}
			if len(publicIDs) > 0 {
				if err := tx.Model(&Attachment{}).Where("message_id IN ?", asstIDs).UpdateColumn("status", fileStatusDeleted).Error; err != nil {
					return err
				}
				var files []FileObject
				if err := tx.Where("file_id IN ?", publicIDs).Find(&files).Error; err != nil {
					return err
				}
				for _, f := range files {
					if err := revokeFileRow(tx, f); err != nil {
						return err
					}
					if f.StoragePath != "" {
						fileIDs = append(fileIDs, f.ID)
					}
				}
			}
			upd := map[string]any{
				"status": msgBlocked, "content": "", "reasoning_content": "", "content_type": "text",
				"moderation_event_id": eventID, "moderation_categories_json": cats,
				"error_code": BlockedErrorCode, "error_message": BlockedErrorMessage,
			}
			if err := tx.Model(&Message{}).Where("id IN ?", asstIDs).Updates(upd).Error; err != nil {
				return err
			}
		}
		if len(userIDs) > 0 {
			upd := map[string]any{
				"status": msgBlocked, "moderation_event_id": eventID, "moderation_categories_json": cats,
				"error_code": BlockedErrorCode, "error_message": BlockedErrorMessage,
			}
			if err := tx.Model(&Message{}).Where("id IN ?", userIDs).Updates(upd).Error; err != nil {
				return err
			}
		}
		if err := tx.Unscoped().Where("run_id = ? AND event_scope IN ?", runID, []string{scopeTraceBlock, scopeTraceEvent}).
			Delete(&RunEvent{}).Error; err != nil {
			return err
		}
		now := s.now()
		res := tx.Model(&Run{}).Where("run_id = ?", runID).Updates(map[string]any{
			"moderation_state": "blocked", "moderation_event_id": eventID, "moderation_categories_json": cats,
			"status": runBlocked, "ended_at": gorm.Expr("COALESCE(ended_at, ?)", now),
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected == 0 {
			return fmt.Errorf("moderation block: run %s not found", runID)
		}
		return tx.Model(&Conversation{}).Where("id = ?", run.ConversationID).UpdateColumn("updated_at", now).Error
	})
	if err != nil {
		return nil, err
	}
	return fileIDs, nil
}

// revokeFileRow withdraws one file: status moderation_blocked, owner cleared.
// The owner's quota is released, as the file no longer counts as theirs. The
// stored path stays until the object is really gone.
func revokeFileRow(tx *gorm.DB, f FileObject) error {
	if f.Status == fileStatusModerationBlocked && f.UserID == 0 {
		return nil
	}
	if err := tx.Model(&Attachment{}).Where("file_id = ?", f.FileID).UpdateColumn("status", fileStatusDeleted).Error; err != nil {
		return err
	}
	if err := tx.Model(&FileObject{}).Where("id = ?", f.ID).
		Updates(map[string]any{"status": fileStatusModerationBlocked, "user_id": 0}).Error; err != nil {
		return err
	}
	if f.UserID != 0 && f.Status == fileStatusActive {
		return releaseQuota(tx, f.UserID, f.SizeBytes)
	}
	return nil
}

// StaleRuns lists up to a batch of runs still pending or moderating whose last
// update is older than cutoff.
func (b *ModerationBridge) StaleRuns(ctx context.Context, cutoff time.Time) ([]StaleRun, error) {
	var runs []Run
	err := b.s.db.WithContext(ctx).
		Where("moderation_state IN ? AND updated_at < ?", []string{"moderating", "pending"}, cutoff).
		Order("id ASC").Limit(moderationStaleBatch).Find(&runs).Error
	if err != nil {
		return nil, err
	}
	out := make([]StaleRun, 0, len(runs))
	for _, r := range runs {
		ref := StaleRun{RunID: r.RunID, UserID: r.UserID, ConversationID: r.ConversationID}
		var m Message
		if e := b.s.db.WithContext(ctx).Where("run_id = ? AND role = ?", r.RunID, roleAssistant).Order("id ASC").First(&m).Error; e == nil {
			ref.MessageID, ref.MessagePublicID = m.ID, m.PublicID
		}
		out = append(out, ref)
	}
	return out, nil
}

// LoadFile returns the stored bytes and detected MIME type of an active file
// row. Files that are not images answer ErrModerationNotImage without being
// read; a missing or withdrawn row answers ErrFileNotFound.
func (b *ModerationBridge) LoadFile(ctx context.Context, id uint) ([]byte, string, error) {
	var f FileObject
	if err := b.s.db.WithContext(ctx).Where("id = ? AND status = ?", id, fileStatusActive).First(&f).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, "", ErrFileNotFound
		}
		return nil, "", err
	}
	mime := strings.ToLower(strings.TrimSpace(f.DetectedMIME))
	if mime == "" {
		mime = strings.ToLower(strings.TrimSpace(f.MimeType))
	}
	if !strings.HasPrefix(mime, "image/") {
		return nil, mime, ErrModerationNotImage
	}
	if f.SizeBytes > moderationMaxImageBytes {
		return nil, mime, ErrModerationFileTooLarge
	}
	data, err := b.s.readBytes(ctx, f.StoragePath)
	if err != nil {
		return nil, mime, err
	}
	return data, mime, nil
}

// RevokeFile withdraws one file from its owner: status moderation_blocked,
// owner 0, attachments deleted.
func (b *ModerationBridge) RevokeFile(ctx context.Context, id uint) error {
	return b.s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var f FileObject
		if err := tx.Where("id = ?", id).First(&f).Error; err != nil {
			if database.IsNotFound(err) {
				return ErrFileNotFound
			}
			return err
		}
		return revokeFileRow(tx, f)
	})
}

// DeleteFileObjects removes the stored objects of withdrawn files and clears
// their stored path only after the delete succeeded. A failure on one file
// does not stop the others; the first error is returned.
func (b *ModerationBridge) DeleteFileObjects(ctx context.Context, ids []uint) error {
	var first error
	for _, id := range ids {
		var f FileObject
		err := b.s.db.WithContext(ctx).Unscoped().Where("id = ?", id).First(&f).Error
		if err != nil {
			if database.IsNotFound(err) {
				continue // the record is gone: nothing left to delete
			}
			if first == nil {
				first = err
			}
			continue
		}
		if err := b.purgeBlockedFile(ctx, f); err != nil {
			b.s.log.Warn("conversation_blocked_file_delete_failed", logx.F("file_id", f.ID), logx.Err(err))
			if first == nil {
				first = err
			}
		}
	}
	return first
}

// RetryBlockedDeletes retries, in ascending id order, the physical delete of
// moderation-blocked files that still carry a storage path.
func (b *ModerationBridge) RetryBlockedDeletes(ctx context.Context, limit int) error {
	if limit <= 0 {
		limit = 200
	}
	var files []FileObject
	err := b.s.db.WithContext(ctx).Unscoped().
		Where("status = ? AND storage_path <> ?", fileStatusModerationBlocked, "").
		Order("id ASC").Limit(limit).Find(&files).Error
	if err != nil {
		return err
	}
	for _, f := range files {
		if err := b.purgeBlockedFile(ctx, f); err != nil {
			b.s.log.Warn("conversation_blocked_file_retry_failed", logx.F("file_id", f.ID), logx.Err(err))
		}
	}
	return nil
}

// purgeBlockedFile deletes the object of a blocked file and then clears the
// stored path. The status stays moderation_blocked.
func (b *ModerationBridge) purgeBlockedFile(ctx context.Context, f FileObject) error {
	if f.StoragePath == "" {
		return nil
	}
	if err := b.s.db.WithContext(ctx).Model(&Attachment{}).Where("file_id = ? AND status <> ?", f.FileID, fileStatusDeleted).
		UpdateColumn("status", fileStatusDeleted).Error; err != nil {
		return err
	}
	if err := b.s.deleteBytes(ctx, f.StoragePath); err != nil {
		return err
	}
	return b.s.db.WithContext(ctx).Unscoped().Model(&FileObject{}).Where("id = ?", f.ID).UpdateColumn("storage_path", "").Error
}

// Emit publishes a moderation event to the run's resumable stream when this
// process owns the active generation; otherwise the event is dropped, as there
// is nobody to deliver to. A blocked event is held back while the run's own
// job is going to publish the terminal event itself (with the final result).
func (b *ModerationBridge) Emit(_ context.Context, runID, typ string, fields map[string]any) {
	lr := b.s.hub.get(runID)
	if lr == nil {
		return
	}
	lr.mu.Lock()
	closed := lr.done || lr.terminal
	hold := typ == evModerationBlocked && lr.holdBlocked
	lr.mu.Unlock()
	if closed || hold {
		return
	}
	b.s.hub.publish(lr, typ, fields)
}

// retainedTypes are the stream events that carry withdrawn content.
var retainedTypes = map[string]bool{
	"delta": true, "upstream_think_delta": true, "media": true, "media_delta": true, "thinking": true,
}

// DiscardRetained drops the buffered text deltas, thinking and media of a run
// so that a client reconnecting with an earlier cursor cannot replay withdrawn
// content. Other events (progress, usage) stay.
func (b *ModerationBridge) DiscardRetained(ctx context.Context, runID string) {
	lr := b.s.hub.get(runID)
	if lr == nil {
		return
	}
	lr.mu.Lock()
	kept := lr.events[:0:0]
	var dropped []int
	for _, ev := range lr.events {
		if retainedTypes[ev.Type] {
			dropped = append(dropped, ev.Seq)
			continue
		}
		kept = append(kept, ev)
	}
	lr.events = kept
	lr.content.Reset()
	lr.think.Reset()
	lr.mu.Unlock()
	for _, seq := range dropped {
		_ = b.s.cache.Delete(ctx, runKey(runID, "ev:"+strconv.Itoa(seq)))
	}
}
