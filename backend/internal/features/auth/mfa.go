package auth

import (
	"context"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

const (
	totpSetupTTL        = 30 * time.Minute
	recoveryCodeCount   = 10
	totpSetupExpiredMsg = "two factor setup expired"
)

type mfaStatus struct {
	Available     bool       `json:"available"`
	TOTPEnabled   bool       `json:"totpEnabled"`
	Required      bool       `json:"required"`
	RecoveryCount int        `json:"recoveryCount"`
	EnabledAt     *time.Time `json:"enabledAt"`
}

func statusOf(m *MFASettings) mfaStatus {
	st := mfaStatus{Available: true}
	if m != nil && totpActive(m) {
		st.TOTPEnabled = true
		st.RecoveryCount = recoveryCount(m)
		st.EnabledAt = m.EnabledAt
	}
	return st
}

type mfaSetup struct {
	Secret     string    `json:"secret"`
	OTPAuthURL string    `json:"otpauthURL"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

type recoveryResult struct {
	RecoveryCodes []string  `json:"recoveryCodes"`
	Status        mfaStatus `json:"status"`
}

// MFAStatus returns the user's two-factor status.
func (s *Service) MFAStatus(ctx context.Context, userID uint) (mfaStatus, error) {
	m, err := s.mfaOf(ctx, userID)
	if err != nil {
		return mfaStatus{}, err
	}
	return statusOf(m), nil
}

// SetupStart begins (or resumes) TOTP enrollment.
func (s *Service) SetupStart(ctx context.Context, u *User) (mfaSetup, error) {
	now := s.now()
	m, err := s.mfaOf(ctx, u.ID)
	if err != nil {
		return mfaSetup{}, err
	}
	if m != nil && totpActive(m) {
		return mfaSetup{}, ErrTwoFactorAlreadyEnabled
	}
	account := u.Email
	if account == "" {
		account = u.Username
	}
	if m != nil && m.TOTPSecretEncrypted != "" && isSet(m.TOTPSetupExpiresAt) && m.TOTPSetupExpiresAt.After(now) {
		if secret, _, derr := s.d.Secrets.Decrypt(m.TOTPSecretEncrypted); derr == nil && secret != "" {
			return mfaSetup{Secret: secret, OTPAuthURL: otpauthURL(account, secret), ExpiresAt: *m.TOTPSetupExpiresAt}, nil
		}
	}
	secret := newTOTPSecret()
	enc, err := s.d.Secrets.Encrypt(secret)
	if err != nil {
		return mfaSetup{}, err
	}
	exp := now.Add(totpSetupTTL)
	if m == nil {
		row := MFASettings{UserID: u.ID, TOTPSecretEncrypted: enc, TOTPSetupExpiresAt: &exp}
		if err := s.conn(ctx).Create(&row).Error; err != nil {
			if database.IsDuplicate(err) {
				return s.SetupStart(ctx, u) // lost a race with another start call
			}
			return mfaSetup{}, err
		}
	} else {
		err := s.conn(ctx).Model(&MFASettings{}).Where("id = ?", m.ID).Updates(map[string]any{
			"totp_secret_encrypted": enc, "totp_setup_expires_at": exp, "recovery_codes_hash": "",
			"totp_enabled": false,
		}).Error
		if err != nil {
			return mfaSetup{}, err
		}
	}
	return mfaSetup{Secret: secret, OTPAuthURL: otpauthURL(account, secret), ExpiresAt: exp}, nil
}

// SetupConfirm completes enrollment with a valid TOTP code.
func (s *Service) SetupConfirm(ctx context.Context, userID uint, code string) (recoveryResult, error) {
	now := s.now()
	m, err := s.mfaOf(ctx, userID)
	if err != nil {
		return recoveryResult{}, err
	}
	if m != nil && totpActive(m) {
		return recoveryResult{RecoveryCodes: nil, Status: statusOf(m)}, nil
	}
	if m == nil || m.TOTPSecretEncrypted == "" {
		return recoveryResult{}, ErrTwoFactorNotStarted
	}
	if !isSet(m.TOTPSetupExpiresAt) || !m.TOTPSetupExpiresAt.After(now) {
		_ = s.conn(ctx).Unscoped().Delete(&MFASettings{}, m.ID).Error
		return recoveryResult{}, ErrTwoFactorExpired.WithStatus(400).WithMessage(totpSetupExpiredMsg)
	}
	secret, _, err := s.d.Secrets.Decrypt(m.TOTPSecretEncrypted)
	if err != nil {
		return recoveryResult{}, err
	}
	norm := normalizeCode(code)
	if !isTOTPShape(norm) || !verifyTOTP(secret, norm, now) {
		return recoveryResult{}, ErrInvalidTwoFactorCode
	}
	codes, stored := s.newRecoverySet()
	err = s.conn(ctx).Model(&MFASettings{}).Where("id = ?", m.ID).Updates(map[string]any{
		"totp_enabled": true, "enabled_at": now, "last_verified_at": now,
		"totp_setup_expires_at": nil, "recovery_codes_hash": stored,
	}).Error
	if err != nil {
		return recoveryResult{}, err
	}
	fresh, err := s.mfaOf(ctx, userID)
	if err != nil {
		return recoveryResult{}, err
	}
	if !totpActive(fresh) {
		return recoveryResult{}, ErrInternal
	}
	return recoveryResult{RecoveryCodes: codes, Status: statusOf(fresh)}, nil
}

// CancelSetup removes a pending, unconfirmed enrollment.
func (s *Service) CancelSetup(ctx context.Context, userID uint) error {
	return s.conn(ctx).Unscoped().
		Where("user_id = ? AND totp_enabled = ?", userID, false).Delete(&MFASettings{}).Error
}

func (s *Service) newRecoverySet() (plain []string, stored string) {
	entries := make([]recoveryEntry, 0, recoveryCodeCount)
	for i := 0; i < recoveryCodeCount; i++ {
		c := newRecoveryCode()
		plain = append(plain, c)
		entries = append(entries, recoveryEntry{Hash: s.recoveryHash(c)})
	}
	return plain, encodeRecoveryHashes(entries)
}

// verifySecondFactor checks a TOTP code or consumes a recovery code for a user
// whose two-factor authentication is enabled.
func (s *Service) verifySecondFactor(ctx context.Context, m *MFASettings, code string) (bool, error) {
	if !totpActive(m) {
		return false, nil
	}
	now := s.now()
	norm := normalizeCode(code)
	if norm == "" {
		return false, nil
	}
	if isTOTPShape(norm) {
		secret, _, err := s.d.Secrets.Decrypt(m.TOTPSecretEncrypted)
		if err != nil {
			return false, err
		}
		if !verifyTOTP(secret, norm, now) {
			return false, nil
		}
		_ = s.conn(ctx).Model(&MFASettings{}).Where("id = ?", m.ID).Update("last_verified_at", now).Error
		return true, nil
	}
	want := s.recoveryHash(norm)
	list := decodeRecoveryHashes(m.RecoveryCodesHash)
	idx := -1
	for i, e := range list {
		if constEq(e.Hash, want) {
			idx = i
		}
	}
	if idx < 0 {
		return false, nil
	}
	rest := make([]recoveryEntry, 0, len(list)-1)
	rest = append(rest, list[:idx]...)
	rest = append(rest, list[idx+1:]...)
	// Compare-and-swap on the stored list: a concurrent use of the same code
	// finds the list already changed and loses.
	res := s.conn(ctx).Model(&MFASettings{}).
		Where("id = ? AND recovery_codes_hash = ?", m.ID, m.RecoveryCodesHash).
		Updates(map[string]any{"recovery_codes_hash": encodeRecoveryHashes(rest), "last_verified_at": now})
	if res.Error != nil {
		return false, res.Error
	}
	return res.RowsAffected == 1, nil
}

// VerifyUserSecondFactor loads the user's MFA record and verifies code.
func (s *Service) VerifyUserSecondFactor(ctx context.Context, userID uint, code string) (bool, error) {
	m, err := s.mfaOf(ctx, userID)
	if err != nil {
		return false, err
	}
	return s.verifySecondFactor(ctx, m, code)
}

// RegenerateRecovery replaces the recovery codes after verifying a code.
func (s *Service) RegenerateRecovery(ctx context.Context, userID uint, code string) (recoveryResult, error) {
	m, err := s.mfaOf(ctx, userID)
	if err != nil {
		return recoveryResult{}, err
	}
	if !totpActive(m) {
		return recoveryResult{}, ErrTwoFactorNotStarted
	}
	ok, err := s.verifySecondFactor(ctx, m, code)
	if err != nil {
		return recoveryResult{}, err
	}
	if !ok {
		return recoveryResult{}, ErrInvalidTwoFactorCode
	}
	codes, stored := s.newRecoverySet()
	if err := s.conn(ctx).Model(&MFASettings{}).Where("id = ?", m.ID).
		Update("recovery_codes_hash", stored).Error; err != nil {
		return recoveryResult{}, err
	}
	fresh, err := s.mfaOf(ctx, userID)
	if err != nil {
		return recoveryResult{}, err
	}
	return recoveryResult{RecoveryCodes: codes, Status: statusOf(fresh)}, nil
}

// DisableMFA removes the user's two-factor configuration after verifying a code.
func (s *Service) DisableMFA(ctx context.Context, userID uint, code string) error {
	m, err := s.mfaOf(ctx, userID)
	if err != nil {
		return err
	}
	if m == nil {
		return ErrTwoFactorNotStarted
	}
	if !totpActive(m) {
		// A setup is pending but two-factor is not on: there is nothing a code
		// could prove, so the code is simply rejected.
		return ErrInvalidTwoFactorCode
	}
	ok, err := s.verifySecondFactor(ctx, m, code)
	if err != nil {
		return err
	}
	if !ok {
		return ErrInvalidTwoFactorCode
	}
	return s.conn(ctx).Unscoped().Where("user_id = ?", userID).Delete(&MFASettings{}).Error
}

// ResetTwoFactor deletes a user's two-factor configuration without any check.
// The administrator-management area calls it; a missing record is not an error.
func ResetTwoFactor(ctx context.Context, db *gorm.DB, userID uint) error {
	return db.WithContext(ctx).Unscoped().Where("user_id = ?", userID).Delete(&MFASettings{}).Error
}
