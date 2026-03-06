package auth

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

type deleteResult struct {
	Deleted bool `json:"deleted"`
}

// AccountDeleteStart resolves the verification method for deleting an account
// and mails a code when it is the email method.
func (s *Service) AccountDeleteStart(ctx context.Context, u *User, method string) (startResult, error) {
	if u.Role == RoleSuperAdmin {
		return startResult{}, ErrSuperadminDelete
	}
	avail, err := s.availableMethods(ctx, u)
	if err != nil {
		return startResult{}, err
	}
	picked, err := pickMethod(method, avail)
	if err != nil {
		return startResult{}, err
	}
	if picked == methodNone {
		return startResult{}, ErrDeleteVerificationNeeded
	}
	res, err := s.startVerification(ctx, u, PurposeAccountDelete, picked)
	if err != nil {
		s.event(ctx, eventInput{Type: "account_delete_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	if res.Sent {
		s.event(ctx, eventInput{Type: "account_delete_code", Result: resultSuccess, UserID: u.ID})
	}
	return res, nil
}

// AccountDelete permanently removes the account and everything it owns.
func (s *Service) AccountDelete(ctx context.Context, u *User, method, code string) error {
	if u.Role == RoleSuperAdmin {
		return ErrSuperadminDelete
	}
	if method == "" || method == methodNone {
		return ErrDeleteVerificationNeeded
	}
	avail, err := s.availableMethods(ctx, u)
	if err != nil {
		return err
	}
	if !containsString(avail, method) {
		return ErrVerificationMethodUnavailable
	}
	if err := s.checkMethod(ctx, u, PurposeAccountDelete, method, code); err != nil {
		s.event(ctx, eventInput{Type: "account_delete", Result: resultFailure, Reason: "verification failed", UserID: u.ID})
		if _, typed := asTyped(err); typed && !errors.Is(err, ErrInvalidEmail) && !errors.Is(err, ErrVerificationAttemptsExceeded) {
			return ErrVerificationCodeInvalid
		}
		return err
	}
	for _, p := range s.h.Purgers {
		if err := p.BeforeDelete(ctx, u.ID); err != nil {
			s.event(ctx, eventInput{Type: "account_delete", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
			return err
		}
	}
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		for _, p := range s.h.Purgers {
			if err := p.Purge(ctx, tx, u.ID); err != nil {
				return err
			}
		}
		for _, model := range []any{
			&Credential{}, &Session{}, &AuthEvent{}, &ContactVerification{}, &UserLink{}, &MFASettings{}, &TrustedDevice{},
		} {
			if err := tx.Unscoped().Where("user_id = ?", u.ID).Delete(model).Error; err != nil {
				return err
			}
		}
		return tx.Unscoped().Delete(&User{}, u.ID).Error
	})
	if err != nil {
		s.event(ctx, eventInput{Type: "account_delete", Result: resultFailure, Reason: "delete failed", UserID: u.ID})
		return err
	}
	total, failed := 0, 0
	var failedPaths []string
	for _, p := range s.h.Purgers {
		t, paths := p.AfterCommit(context.WithoutCancel(ctx), u.ID)
		total += t
		failed += len(paths)
		failedPaths = append(failedPaths, paths...)
	}
	// The user's own events were deleted with the account; the outcome is
	// recorded against the deleted id so operators can still see it.
	s.event(ctx, eventInput{Type: "account_delete", Result: resultSuccess, UserID: u.ID,
		Detail: map[string]any{"userID": u.ID, "publicID": u.PublicID, "storageFiles": total, "cleanupFailures": failed}})
	if failed > 0 {
		if len(failedPaths) > 10 {
			failedPaths = failedPaths[:10]
		}
		s.log.Warn("account_delete_storage_cleanup_failed", logx.F("user_id", u.ID), logx.F("failed", failed))
		s.event(ctx, eventInput{Type: "account_delete_storage_cleanup", Result: resultFailure, Reason: "storage cleanup partially failed",
			UserID: u.ID, Detail: map[string]any{"failedPaths": failedPaths, "failed": failed}})
	}
	return nil
}

// HasActiveSuperadminWithIdentity reports whether some active superadmin has a
// bound external identity. The settings area asks before allowing both
// password-style logins to be switched off.
func HasActiveSuperadminWithIdentity(ctx context.Context, db *gorm.DB) (bool, error) {
	var n int64
	err := db.WithContext(ctx).Table("identity_users AS u").
		Joins("JOIN identity_user_links AS l ON l.user_id = u.id AND l.deleted_at IS NULL").
		Where("u.deleted_at IS NULL AND u.role = ? AND u.status = ?", RoleSuperAdmin, StatusActive).
		Count(&n).Error
	return n > 0, err
}
