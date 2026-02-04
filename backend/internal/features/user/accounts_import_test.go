package user

import (
	"context"
	"errors"
	"testing"
	"time"
)

func importRec(username string, balance int64) ImportRecord {
	return ImportRecord{
		Account:        Account{Username: username, DisplayName: username, Email: username + "@x.io"},
		Credential:     Credential{PasswordHash: "$2a$10$hash", PasswordEnabled: true},
		BalanceNanoUSD: balance, RefNo: "ref-" + username,
	}
}

func TestImportCreatesAccountsCredentialsBalancesAndTransactions(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	ctx := context.Background()
	rich := importRec("rich", 5_000_000_000)
	rich.Description = "custom note"
	out, err := acc.ImportWithBalances(ctx, []ImportRecord{rich, importRec("poor", 0), importRec("owes", -10)})
	if err != nil || len(out) != 3 {
		t.Fatalf("%v %v", out, err)
	}
	for _, a := range out {
		if a.ID == 0 || a.CreatedAt.IsZero() || !hex32.MatchString(a.PublicID) || a.Role != RoleUser || a.Status != StatusActive ||
			a.Timezone != DefaultTimezone || a.Locale != DefaultLocale {
			t.Fatalf("account: %+v", a)
		}
	}
	var cred Credential
	env.DB.Where("user_id = ?", out[0].ID).Take(&cred)
	if cred.PasswordAlgo != "bcrypt" || cred.PasswordOrigin != PasswordOriginAdminCreated || cred.PasswordHash != "$2a$10$hash" ||
		cred.PasswordUpdatedAt == nil || cred.PasswordSetAt == nil || !cred.PasswordSetAt.Equal(env.Clock.Now()) {
		t.Fatalf("credential: %+v", cred)
	}

	var accounts []billingAccountRow
	env.DB.Order("user_id").Find(&accounts)
	if len(accounts) != 3 {
		t.Fatalf("billing accounts: %d", len(accounts))
	}
	for i, want := range []int64{5_000_000_000, 0, 0} {
		a := accounts[i]
		if a.Currency != "USD" || a.Status != "active" || a.BalanceNanousd != want {
			t.Errorf("account %d: %+v", i, a)
		}
	}
	var txs []balanceTxRow
	env.DB.Find(&txs)
	if len(txs) != 1 {
		t.Fatalf("only positive balances get a transaction: %d", len(txs))
	}
	tx := txs[0]
	if tx.Type != "admin_set" || tx.AmountNanousd != 5_000_000_000 || tx.BalanceAfterNanousd != 5_000_000_000 ||
		tx.RefType != "admin_import" || tx.RefNo != "ref-rich" || tx.Description != "custom note" ||
		tx.UserID != out[0].ID || tx.AccountID != accounts[0].ID {
		t.Fatalf("transaction: %+v", tx)
	}
}

func TestImportDefaultDescription(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	if _, err := acc.ImportWithBalances(context.Background(), []ImportRecord{importRec("rich", 1)}); err != nil {
		t.Fatal(err)
	}
	var tx balanceTxRow
	env.DB.Take(&tx)
	if tx.Description != "OpenWebUI import" {
		t.Fatalf("description %q", tx.Description)
	}
}

func TestImportKeepsSuppliedCredentialFields(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	stamp := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	rec := importRec("keeper", 0)
	rec.Credential.PasswordAlgo, rec.Credential.PasswordOrigin = "argon", PasswordOriginSSOPlaceholder
	rec.Credential.PasswordSetAt, rec.Credential.PasswordUpdatedAt = &stamp, &stamp
	out, err := acc.ImportWithBalances(context.Background(), []ImportRecord{rec})
	if err != nil {
		t.Fatal(err)
	}
	var cred Credential
	env.DB.Where("user_id = ?", out[0].ID).Take(&cred)
	if cred.PasswordAlgo != "argon" || cred.PasswordOrigin != PasswordOriginSSOPlaceholder || !cred.PasswordSetAt.Equal(stamp) || !cred.PasswordUpdatedAt.Equal(stamp) {
		t.Fatalf("%+v", cred)
	}
}

func TestImportEmptyInput(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	out, err := acc.ImportWithBalances(context.Background(), nil)
	if err != nil || out == nil || len(out) != 0 {
		t.Fatalf("%v %v", out, err)
	}
}

func TestImportIsAllOrNothingOnDuplicateUsername(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Exec("CREATE UNIQUE INDEX idx_identity_users_username ON identity_users(username)").Error; err != nil {
		t.Fatal(err)
	}
	acc := newAccounts(env, nil)
	seedAccount(t, env.DB, "existing")
	_, err := acc.ImportWithBalances(context.Background(), []ImportRecord{importRec("first", 10), importRec("second", 20), importRec("existing", 30)})
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("got %v", err)
	}
	var accounts, creds, billing, txs int64
	env.DB.Model(&Account{}).Count(&accounts)
	env.DB.Model(&Credential{}).Count(&creds)
	env.DB.Model(&billingAccountRow{}).Count(&billing)
	env.DB.Model(&balanceTxRow{}).Count(&txs)
	if accounts != 1 || creds != 0 || billing != 0 || txs != 0 {
		t.Fatalf("partial import: %d accounts %d creds %d billing %d txs", accounts, creds, billing, txs)
	}
}

func TestImportDuplicateInsideTheBatchAborts(t *testing.T) {
	env := newEnv(t, Ports{})
	if err := env.DB.Exec("CREATE UNIQUE INDEX idx_identity_users_username ON identity_users(username)").Error; err != nil {
		t.Fatal(err)
	}
	acc := newAccounts(env, nil)
	_, err := acc.ImportWithBalances(context.Background(), []ImportRecord{importRec("same", 10), importRec("same", 10)})
	if !errors.Is(err, ErrUsernameExists) {
		t.Fatalf("got %v", err)
	}
	var n int64
	env.DB.Model(&Account{}).Count(&n)
	if n != 0 {
		t.Fatalf("%d accounts persisted", n)
	}
}
