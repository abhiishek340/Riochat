package user

import (
	"context"

	"gorm.io/gorm"
)

const defaultImportDescription = "OpenWebUI import"

// ImportRecord is one user of a bulk import: the account, an already hashed
// credential and an opening balance in nano-USD (1e-9 USD).
type ImportRecord struct {
	Account        Account
	Credential     Credential
	BalanceNanoUSD int64
	RefNo          string
	Description    string
}

// ImportWithBalances creates all records atomically: any failure (a duplicate
// username, for instance) aborts the whole import and nothing is stored. The
// created accounts are returned with their ids and timestamps.
func (a *Accounts) ImportWithBalances(ctx context.Context, records []ImportRecord) ([]Account, error) {
	out := make([]Account, 0, len(records))
	if len(records) == 0 {
		return out, nil
	}
	now := a.now()
	err := a.conn(ctx).Transaction(func(tx *gorm.DB) error {
		for _, rec := range records {
			acct := rec.Account
			acct.ID = 0
			if acct.PublicID == "" {
				acct.PublicID = newPublicID()
			}
			if acct.Role == "" {
				acct.Role = RoleUser
			}
			if acct.Status == "" {
				acct.Status = StatusActive
			}
			if acct.Timezone == "" {
				acct.Timezone = DefaultTimezone
			}
			if acct.Locale == "" {
				acct.Locale = DefaultLocale
			}
			if err := tx.Create(&acct).Error; err != nil {
				return translateWrite(err, ErrUsernameExists)
			}

			cred := rec.Credential
			cred.ID = 0
			cred.UserID = acct.ID
			if cred.PasswordHash != "" {
				// An imported hash is a usable password; the flag defaults to true.
				cred.PasswordEnabled = true
			}
			if cred.PasswordAlgo == "" {
				cred.PasswordAlgo = "bcrypt"
			}
			if cred.PasswordOrigin == "" {
				cred.PasswordOrigin = PasswordOriginAdminCreated
			}
			if cred.PasswordUpdatedAt == nil {
				cred.PasswordUpdatedAt = &now
			}
			if cred.PasswordSetAt == nil {
				cred.PasswordSetAt = &now
			}
			if err := tx.Create(&cred).Error; err != nil {
				return err
			}

			balance := max(rec.BalanceNanoUSD, 0)
			billing := billingAccountRow{UserID: acct.ID, Currency: "USD", Status: "active", BalanceNanousd: balance}
			if err := tx.Create(&billing).Error; err != nil {
				return err
			}
			if balance > 0 {
				note := rec.Description
				if note == "" {
					note = defaultImportDescription
				}
				entry := balanceTxRow{
					AccountID: billing.ID, UserID: acct.ID, Type: "admin_set",
					AmountNanousd: balance, BalanceAfterNanousd: balance,
					RefType: "admin_import", RefNo: rec.RefNo, Description: note,
				}
				if err := tx.Create(&entry).Error; err != nil {
					return err
				}
			}
			out = append(out, acct)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
