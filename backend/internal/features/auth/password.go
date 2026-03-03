package auth

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// smtpConfigured reports whether the SMTP settings are complete enough to send
// mail on behalf of password reset.
func smtpConfigured(st Settings) bool {
	return strings.TrimSpace(st.SMTPHost) != "" && st.SMTPPort >= 1 && st.SMTPPort <= 65535 &&
		strings.TrimSpace(st.SMTPUsername) != "" && st.SMTPPassword != ""
}

// passwordResetAvailable is true when users can actually be sent a reset code.
func passwordResetAvailable(st Settings) bool {
	return st.PasswordResetEnabled && st.EmailVerificationEnabled &&
		(st.UsernameLoginEnabled || st.EmailLoginEnabled) && smtpConfigured(st)
}

// storePassword replaces the user's password hash, clears every lock and flag,
// and reactivates a locked account. It runs inside tx.
func (s *Service) storePassword(tx *gorm.DB, u *User, cred *Credential, plain string, now time.Time) error {
	hash, err := hashPassword(plain)
	if err != nil {
		return err
	}
	fields := map[string]any{
		"password_hash": hash, "password_algo": "bcrypt", "password_enabled": true,
		"password_updated_at": now, "password_set_at": now, "password_origin": PasswordOriginUserSet,
		"must_reset_password": false, "failed_login_count": 0, "locked_until": nil,
	}
	if cred == nil {
		row := Credential{
			UserID: u.ID, PasswordHash: hash, PasswordAlgo: "bcrypt", PasswordEnabled: true,
			PasswordUpdatedAt: &now, PasswordSetAt: &now, PasswordOrigin: PasswordOriginUserSet,
		}
		if err := tx.Create(&row).Error; err != nil {
			return err
		}
	} else if err := tx.Model(&Credential{}).Where("id = ?", cred.ID).Updates(fields).Error; err != nil {
		return err
	}
	if u.Status == StatusLocked {
		if err := tx.Model(&User{}).Where("id = ?", u.ID).Update("status", StatusActive).Error; err != nil {
			return err
		}
		u.Status = StatusActive
	}
	return nil
}

// ---- change password ----------------------------------------------------------------

// PasswordChangeStart resolves the verification method and mails a code when it
// is the email method.
func (s *Service) PasswordChangeStart(ctx context.Context, u *User, method string) (startResult, error) {
	res, err := s.startVerification(ctx, u, PurposePasswordChange, method)
	if err != nil {
		s.event(ctx, eventInput{Type: "password_change_code", Result: resultFailure, Reason: reasonOf(err), UserID: u.ID})
		return startResult{}, err
	}
	if res.Sent {
		s.event(ctx, eventInput{Type: "password_change_code", Result: resultSuccess, UserID: u.ID})
	}
	return res, nil
}

type passwordChangeInput struct {
	CurrentPassword string
	NewPassword     string
	Method          string
	Code            string
}

// PasswordChangeComplete changes the password of a signed-in user and signs
// every session out.
func (s *Service) PasswordChangeComplete(ctx context.Context, u *User, in passwordChangeInput) error {
	newPw, err := checkPasswordPolicy(in.NewPassword)
	if err != nil {
		return err
	}
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return err
	}
	initial := mustReset(u, cred)
	if initial {
		if bootstrapPasswordActive(u, cred) && checkPassword(cred.PasswordHash, newPw) {
			return ErrPasswordReuse
		}
	} else {
		if cred != nil && cred.PasswordEnabled {
			if !checkPassword(cred.PasswordHash, in.CurrentPassword) &&
				!checkPassword(cred.PasswordHash, strings.TrimSpace(in.CurrentPassword)) {
				return ErrInvalidCurrentPassword
			}
		}
		avail, err := s.availableMethods(ctx, u)
		if err != nil {
			return err
		}
		method, err := pickMethod(in.Method, avail)
		if err != nil {
			return err
		}
		if err := s.checkMethod(ctx, u, PurposePasswordChange, method, in.Code); err != nil {
			s.event(ctx, eventInput{Type: "password_change", Result: resultFailure, Reason: "verification failed", UserID: u.ID})
			if errors.Is(err, ErrVerificationAttemptsExceeded) {
				return err
			}
			if _, typed := asTyped(err); typed {
				return ErrVerificationCodeInvalid
			}
			return err
		}
	}
	now := s.now()
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.storePassword(tx, u, cred, newPw, now); err != nil {
			return err
		}
		return tx.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", u.ID).
			Updates(map[string]any{"revoked_at": now, "revoke_reason": reasonPasswordSet}).Error
	})
	if err != nil {
		return err
	}
	s.event(ctx, eventInput{Type: "password_change", Result: resultSuccess, UserID: u.ID,
		Detail: map[string]any{"initial": initial}})
	return nil
}

// ---- reset password ----------------------------------------------------------------

type resetStartResult struct {
	Sent      bool      `json:"sent"`
	ExpiresAt time.Time `json:"expiresAt"`
}

// resetTarget finds the account a reset for email may act on. ok is false when
// there is none (disabled feature, unknown or unverified email, inactive user,
// or no password login).
func (s *Service) resetTarget(ctx context.Context, st Settings, email string) (*User, bool, error) {
	if !passwordResetAvailable(st) {
		return nil, false, nil
	}
	var u User
	if err := s.conn(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		if database.IsNotFound(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	if !isSet(u.EmailVerifiedAt) || (u.Status != StatusActive && u.Status != StatusLocked) {
		return nil, false, nil
	}
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return nil, false, err
	}
	if cred == nil || !cred.PasswordEnabled {
		return nil, false, nil
	}
	return &u, true, nil
}

// PasswordResetStart mails a reset code when the address belongs to an eligible
// account. The answer never reveals whether it did.
func (s *Service) PasswordResetStart(ctx context.Context, rawEmail string) (resetStartResult, error) {
	email, err := normalizeEmail(rawEmail)
	if err != nil {
		return resetStartResult{}, err
	}
	st := s.settings(ctx)
	digest := map[string]any{"emailHash": sha256Hex(email)}
	u, ok, err := s.resetTarget(ctx, st, email)
	if err != nil {
		return resetStartResult{}, ErrPasswordResetFailed.WithStatus(500).WithCause(err)
	}
	if !ok {
		s.event(ctx, eventInput{Type: "password_reset_code", Result: resultFailure, Reason: "target unavailable", Detail: digest})
		return resetStartResult{Sent: true, ExpiresAt: s.now().Add(codeTTL)}, nil
	}
	rec, err := s.recentlySent(ctx, u.ID, PurposePasswordReset, email)
	if err != nil {
		return resetStartResult{}, ErrPasswordResetFailed.WithStatus(500).WithCause(err)
	}
	if rec != nil {
		s.event(ctx, eventInput{Type: "password_reset_code", Result: resultFailure, Reason: "sent recently", UserID: u.ID, Detail: digest})
		return resetStartResult{Sent: true, ExpiresAt: *rec.ExpiresAt}, nil
	}
	exp, err := s.issueAndMail(ctx, u.ID, PurposePasswordReset, email)
	if err != nil {
		s.event(ctx, eventInput{Type: "password_reset_code", Result: resultFailure, Reason: "send failed", UserID: u.ID, Detail: digest})
		return resetStartResult{}, ErrPasswordResetFailed.WithStatus(500).WithCause(err)
	}
	s.event(ctx, eventInput{Type: "password_reset_code", Result: resultSuccess, UserID: u.ID, Detail: digest})
	return resetStartResult{Sent: true, ExpiresAt: exp}, nil
}

// PasswordResetComplete sets a new password from a mailed code and revokes
// every session of the account.
func (s *Service) PasswordResetComplete(ctx context.Context, rawEmail, code, newPassword string) error {
	newPw, err := checkPasswordPolicy(newPassword)
	if err != nil {
		return err
	}
	email, err := normalizeEmail(rawEmail)
	if err != nil {
		return err
	}
	st := s.settings(ctx)
	digest := map[string]any{"emailHash": sha256Hex(email)}
	u, ok, err := s.resetTarget(ctx, st, email)
	if err != nil {
		return ErrPasswordResetFailed.WithStatus(500).WithCause(err)
	}
	if !ok {
		s.event(ctx, eventInput{Type: "password_reset", Result: resultFailure, Reason: "target unavailable", Detail: digest})
		return ErrPasswordResetFailed
	}
	if err := s.verifyCode(ctx, u.ID, PurposePasswordReset, email, code); err != nil {
		s.event(ctx, eventInput{Type: "password_reset", Result: resultFailure, Reason: "invalid code", UserID: u.ID, Detail: digest})
		if _, typed := asTyped(err); typed {
			return ErrPasswordResetFailed
		}
		return err
	}
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return err
	}
	now := s.now()
	err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
		if err := s.storePassword(tx, u, cred, newPw, now); err != nil {
			return err
		}
		return tx.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", u.ID).
			Updates(map[string]any{"revoked_at": now, "revoke_reason": reasonPasswordReset}).Error
	})
	if err != nil {
		return err
	}
	s.event(ctx, eventInput{Type: "password_reset", Result: resultSuccess, UserID: u.ID, Detail: digest})
	return nil
}

// ---- onboarding ----------------------------------------------------------------------

// OnboardingComplete finishes first-login setup. It reports whether the password
// was replaced (which signs every session out).
func (s *Service) OnboardingComplete(ctx context.Context, u *User, newPassword string) (bool, error) {
	if s.initialUsernameRequired(u) {
		return false, ErrUsernameChangeRequired
	}
	cred, err := s.credentialOf(ctx, u.ID)
	if err != nil {
		return false, err
	}
	now := s.now()
	changed := false
	if mustReset(u, cred) {
		if strings.TrimSpace(newPassword) == "" {
			return false, ErrInvalidPassword
		}
		newPw, err := checkPasswordPolicy(newPassword)
		if err != nil {
			return false, err
		}
		if bootstrapPasswordActive(u, cred) && checkPassword(cred.PasswordHash, newPw) {
			return false, ErrPasswordReuse
		}
		err = s.conn(ctx).Transaction(func(tx *gorm.DB) error {
			if err := s.storePassword(tx, u, cred, newPw, now); err != nil {
				return err
			}
			return tx.Model(&Session{}).Where("user_id = ? AND revoked_at IS NULL", u.ID).
				Updates(map[string]any{"revoked_at": now, "revoke_reason": reasonPasswordSet}).Error
		})
		if err != nil {
			return false, err
		}
		changed = true
		s.event(ctx, eventInput{Type: "password_change", Result: resultSuccess, UserID: u.ID,
			Detail: map[string]any{"initialOnboarding": true}})
	}
	if err := s.conn(ctx).Model(&User{}).Where("id = ?", u.ID).Update("onboarding_completed_at", now).Error; err != nil {
		return false, err
	}
	u.OnboardingCompletedAt = &now
	return changed, nil
}
