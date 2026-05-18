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
