package user

import (
	"context"
	"encoding/json"
	"strings"
)

// TwoFactorState is what the administration views show about a user's TOTP
// configuration.
type TwoFactorState struct {
	Enabled       bool
	RecoveryCount int
}

// TwoFactorStates reads the two-factor state of the given users. A user without
// a settings row is absent from the result, and so is everybody when the
// security tables do not exist in this deployment.
func (a *Accounts) TwoFactorStates(ctx context.Context, userIDs []uint) (map[uint]TwoFactorState, error) {
	out := map[uint]TwoFactorState{}
	if len(userIDs) == 0 || !a.conn(ctx).Migrator().HasTable(&mfaRow{}) {
		return out, nil
	}
	var rows []mfaRow
	if err := a.conn(ctx).Where("user_id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		st := TwoFactorState{Enabled: r.TOTPEnabled}
		if raw := strings.TrimSpace(r.RecoveryCodesHash); raw != "" {
			var list []json.RawMessage
			if json.Unmarshal([]byte(raw), &list) == nil {
				st.RecoveryCount = len(list)
			}
		}
		out[r.UserID] = st
	}
	return out, nil
}

// CredentialsForUsers reads the credential rows of the given users, keyed by
// user id. A user without a row is absent from the result.
func (a *Accounts) CredentialsForUsers(ctx context.Context, userIDs []uint) (map[uint]Credential, error) {
	out := map[uint]Credential{}
	if len(userIDs) == 0 {
		return out, nil
	}
	var rows []Credential
	if err := a.conn(ctx).Where("user_id IN ?", userIDs).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.UserID] = r
	}
	return out, nil
}

// ResetTwoFactor permanently removes a user's two-factor settings. available is
// false when the security tables are not part of the deployment, in which case
// nothing was done. A user without settings is not an error.
func (a *Accounts) ResetTwoFactor(ctx context.Context, userID uint) (available bool, err error) {
	if !a.conn(ctx).Migrator().HasTable(&mfaRow{}) {
		return false, nil
	}
	err = a.conn(ctx).Unscoped().Where("user_id = ?", userID).Delete(&mfaRow{}).Error
	return err == nil, err
}

// ByIDs returns the live accounts with the given ids, keyed by id.
func (a *Accounts) ByIDs(ctx context.Context, ids []uint) (map[uint]Account, error) {
	out := map[uint]Account{}
	if len(ids) == 0 {
		return out, nil
	}
	var rows []Account
	if err := a.conn(ctx).Where("id IN ?", ids).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, r := range rows {
		out[r.ID] = r
	}
	return out, nil
}

// ApplyStatus applies the side effects of moving an account to a status: an
// active account has its failed-login counter and lock cleared, any other
// status ends all of its sessions. The status itself is written first.
func (a *Accounts) ApplyStatus(ctx context.Context, userID uint, status string) error {
	if err := a.SetStatus(ctx, userID, status); err != nil {
		return err
	}
	if status == StatusActive {
		return a.Unlock(ctx, userID)
	}
	return a.RevokeAllSessions(ctx, userID, "status set to "+status)
}

// ExistingUserIDs returns which of the ids belong to live accounts.
func (a *Accounts) ExistingUserIDs(ctx context.Context, ids []uint) ([]uint, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	var found []uint
	err := a.conn(ctx).Model(&Account{}).Where("id IN ?", ids).Pluck("id", &found).Error
	return found, err
}
