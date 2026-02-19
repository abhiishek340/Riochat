package auth

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	codeTTL         = 10 * time.Minute
	resendCooldown  = 60 * time.Second
	maxCodeAttempts = 5
)

func (s *Service) codeHash(token, code string) string {
	return s.hmacHex("verification", token, code)
}

// latestPending returns the newest pending, unexpired record of the triple.
func (s *Service) latestPending(ctx context.Context, userID uint, purpose, target string) (*ContactVerification, error) {
	var rec ContactVerification
	err := s.conn(ctx).
		Where("user_id = ? AND purpose = ? AND target = ? AND status = ? AND expires_at > ?",
			userID, purpose, target, verifPending, s.now()).
		Order("id DESC").First(&rec).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, nil
		}
		return nil, err
	}
	return &rec, nil
}

// recentlySent reports the pending record when it was sent within the resend
// cooldown.
func (s *Service) recentlySent(ctx context.Context, userID uint, purpose, target string) (*ContactVerification, error) {
	rec, err := s.latestPending(ctx, userID, purpose, target)
	if err != nil || rec == nil {
		return nil, err
	}
	if rec.SentAt != nil && s.now().Sub(*rec.SentAt) < resendCooldown {
		return rec, nil
	}
	return nil, nil
}

func (s *Service) cancelPending(ctx context.Context, tx *gorm.DB, userID uint, purpose, target string) error {
	now := s.now()
	return tx.WithContext(ctx).Model(&ContactVerification{}).
		Where("user_id = ? AND purpose = ? AND target = ? AND status = ?", userID, purpose, target, verifPending).
		Updates(map[string]any{"status": verifCanceled, "consumed_at": now}).Error
}

// sendCode issues a fresh code for (user, purpose, target) and mails it. The
// resend cooldown applies. When the mail cannot be sent the new record is
// canceled so the user can retry at once.
func (s *Service) sendCode(ctx context.Context, userID uint, purpose, target string) (time.Time, error) {
	if rec, err := s.recentlySent(ctx, userID, purpose, target); err != nil {
		return time.Time{}, err
	} else if rec != nil {
		return time.Time{}, ErrVerificationCodeRecent
	}
	return s.issueAndMail(ctx, userID, purpose, target)
}

// issueAndMail is sendCode without the cooldown check.
func (s *Service) issueAndMail(ctx context.Context, userID uint, purpose, target string) (time.Time, error) {
	now := s.now()
	code := randomDigits(6)
	token := randomHex(24)
	exp := now.Add(codeTTL)
	rec := ContactVerification{
		UserID: userID, Channel: "email", Purpose: purpose, Target: target, Token: token,
		CodeHash: s.codeHash(token, code), Status: verifPending, SentAt: &now, ExpiresAt: &exp,
	}
	err := s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.cancelPending(ctx, tx, userID, purpose, target); err != nil {
			return err
		}
		return tx.Create(&rec).Error
	})
	if err != nil {
		return time.Time{}, err
	}
	if err := s.mailer().Send(ctx, s.buildVerificationMail(target, purpose, code)); err != nil {
		_ = s.conn(context.WithoutCancel(ctx)).Model(&ContactVerification{}).Where("id = ?", rec.ID).
			Updates(map[string]any{"status": verifCanceled, "consumed_at": s.now()}).Error
		return time.Time{}, err
	}
	return exp, nil
}

// verifyCode checks a code against the pending record of (user, purpose,
// target). A correct code consumes the record.
func (s *Service) verifyCode(ctx context.Context, userID uint, purpose, target, code string) error {
	rec, err := s.latestPending(ctx, userID, purpose, target)
	if err != nil {
		return err
	}
	if rec == nil {
		return ErrVerificationCodeInvalid
	}
	if rec.AttemptCount >= maxCodeAttempts {
		return ErrVerificationAttemptsExceeded
	}
	if !constEq(rec.CodeHash, s.codeHash(rec.Token, code)) {
		if err := s.conn(ctx).Model(&ContactVerification{}).Where("id = ?", rec.ID).
			UpdateColumn("attempt_count", gorm.Expr("attempt_count + 1")).Error; err != nil {
			return err
		}
		return ErrVerificationCodeInvalid
	}
	now := s.now()
	res := s.conn(ctx).Model(&ContactVerification{}).
		Where("id = ? AND status = ?", rec.ID, verifPending).
		Updates(map[string]any{"status": verifVerified, "verified_at": now, "consumed_at": now})
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected != 1 {
		return ErrVerificationCodeInvalid
	}
	return nil
}
