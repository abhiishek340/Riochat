package user

import (
	"context"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Values that cannot be stored are refused up front instead of failing later in
// the database.
func TestOverlongOrUnstorableFieldsAreRejected(t *testing.T) {
	longLocal := strings.Repeat("a", 120)
	if _, err := NormalizeEmail(longLocal + "@example.com"); err == nil {
		t.Error("email over 128 characters must be invalid")
	}
	if _, err := NormalizeEmail(strings.Repeat("a", 100) + "@example.com"); err != nil {
		t.Errorf("email under the limit: %v", err)
	}
	if ValidAvatarRef("https://example.com/" + strings.Repeat("a", 2040)) {
		t.Error("avatar over 2048 characters must be invalid")
	}
	if !ValidAvatarRef("https://example.com/" + strings.Repeat("a", 2000)) {
		t.Error("avatar under the limit must be valid")
	}
	if ValidAvatarRef("https://example.com/a\x00.png") {
		t.Error("NUL in an avatar must be invalid")
	}
}

func TestDefaultModelRejectsNULAndInvalidUTF8(t *testing.T) {
	const k = "chat.default_model"
	// Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
	for _, v := range []string{"", "gpt", "  spaced  ", "\u6a21\u578b"} {
		if !ValidateSettingValue(k, v) {
			t.Errorf("%q should be valid", v)
		}
	}
	for _, v := range []string{"a\x00b", "\xff\xfe"} {
		if ValidateSettingValue(k, v) {
			t.Errorf("%q should be invalid", v)
		}
	}
}

func TestActivityDaysBeyondIntRangeClamps(t *testing.T) {
	for raw, want := range map[string]int{"99999999999999999999999": maxActivityDays, "+5": 5} {
		got, err := parseActivityDays(raw)
		if err != nil || got != want {
			t.Errorf("%q: %d %v", raw, got, err)
		}
	}
	for _, raw := range []string{"-99999999999999999999999", "9999999999999999999999x", "0"} {
		if _, err := parseActivityDays(raw); err == nil {
			t.Errorf("%q should be invalid", raw)
		}
	}
}

func TestImportedCredentialWithHashIsPasswordEnabled(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	rec := importRec("hashed", 0)
	rec.Credential.PasswordEnabled = false
	out, err := acc.ImportWithBalances(context.Background(), []ImportRecord{rec})
	if err != nil || len(out) != 1 {
		t.Fatalf("%v %v", out, err)
	}
	var cred Credential
	env.DB.Where("user_id = ?", out[0].ID).Take(&cred)
	if !cred.PasswordEnabled {
		t.Fatalf("an imported password hash must be usable: %+v", cred)
	}
}

func TestSettingsRefreshSurvivesRequestCancellationAfterCommit(t *testing.T) {
	var hookCtxErr error
	called := false
	env := newEnv(t, Ports{OnSettingsWritten: func(ctx context.Context, _ uint, _ []string) {
		called = true
		hookCtxErr = ctx.Err()
	}})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	// Cancel the request right after the upsert committed.
	if err := env.DB.Callback().Create().After("gorm:commit_or_rollback_transaction").
		Register("test:cancel_after_commit", func(*gorm.DB) { cancel() }); err != nil {
		t.Fatal(err)
	}
	store := NewSettingsStore(env.DB, env.Clock, nil, func(c context.Context, u uint, k []string) {
		called = true
		hookCtxErr = c.Err()
	}, nil)
	_, _ = store.Patch(ctx, 3, map[string]string{"chat.file_mode": "rag"})
	if !called {
		t.Fatal("hook did not run")
	}
	if hookCtxErr != nil {
		t.Fatalf("post-commit work must not be cancelled with the request: %v", hookCtxErr)
	}
}
