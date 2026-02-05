package user

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"gorm.io/gorm"
)

// purgeSchema creates the tables other areas own, with just the columns the
// deletion touches.
var purgeSchema = []string{
	`CREATE TABLE chat_conversations (id INTEGER PRIMARY KEY, user_id INTEGER, deleted_at DATETIME)`,
	`CREATE TABLE chat_messages (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER, deleted_at DATETIME)`,
	`CREATE TABLE chat_feedback (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_attachments (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_message_chunks (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_message_ui_states (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_context_records (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_conversation_shares (id INTEGER PRIMARY KEY, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_runs (id INTEGER PRIMARY KEY, run_id TEXT, user_id INTEGER, conversation_id INTEGER)`,
	`CREATE TABLE chat_run_events (id INTEGER PRIMARY KEY, run_id TEXT)`,
	`CREATE TABLE chat_conversation_projects (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE chat_conversation_project_mcp_tools (project_id INTEGER, tool_id INTEGER)`,
	`CREATE TABLE chat_conversation_project_skills (project_id INTEGER, skill_id INTEGER)`,
	`CREATE TABLE chat_conversation_project_knowledge_bases (project_id INTEGER, knowledge_base_id TEXT)`,
	`CREATE TABLE skills (id INTEGER PRIMARY KEY, scope TEXT, owner_user_id INTEGER)`,
	`CREATE TABLE llm_user_providers (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE knowledge_bases (id INTEGER PRIMARY KEY, public_id TEXT, scope TEXT, owner_user_id INTEGER)`,
	`CREATE TABLE knowledge_base_files (knowledge_base_id INTEGER, file_object_id INTEGER)`,
	`CREATE TABLE file_objects (id INTEGER PRIMARY KEY, user_id INTEGER, status TEXT, storage_path TEXT, deleted_at DATETIME)`,
	`CREATE TABLE file_chunks (id INTEGER PRIMARY KEY, user_id INTEGER, file_object_id INTEGER)`,
	`CREATE TABLE file_storage_quotas (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE user_memories (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE user_memory_embeddings (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE permission_group_user_access (group_id INTEGER, user_id INTEGER)`,
	`CREATE TABLE identity_contact_verifications (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE identity_mfa_settings (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE identity_trusted_devices (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE billing_usage_ledgers (id INTEGER PRIMARY KEY, user_id INTEGER)`,
	`CREATE TABLE billing_payment_orders (id INTEGER PRIMARY KEY, user_id INTEGER)`,
}

func execAll(t *testing.T, db *gorm.DB, stmts ...string) {
	t.Helper()
	for _, s := range stmts {
		if err := db.Exec(s).Error; err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
}

func count(t *testing.T, db *gorm.DB, table, where string, args ...any) int64 {
	t.Helper()
	var n int64
	if err := db.Raw("SELECT COUNT(*) FROM "+table+" WHERE "+where, args...).Scan(&n).Error; err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

func seedUserData(t *testing.T, db *gorm.DB, u Account) {
	t.Helper()
	uid := u.ID
	now := time.Now()
	seedCredential(t, db, uid)
	db.Create(&sessionRow{SessionID: fmt.Sprintf("s%d", uid), UserID: uid, IssuedAt: now, ExpiresAt: now.Add(time.Hour)})
	db.Create(&AuthEvent{UserID: uid, EventType: "login", Result: "success", OccurredAt: now})
	db.Create(&linkRow{UserID: uid, ProviderID: 1, ProviderSubject: fmt.Sprintf("sub%d", uid), LinkedAt: now})
	db.Create(&subscriptionRow{UserID: uid, PlanID: 1, PriceID: 1, Status: "active", StartAt: now, CurrentPeriodStartAt: now})
	db.Create(&billingAccountRow{UserID: uid, Currency: "USD", Status: "active", BalanceNanousd: 5})
	db.Create(&Setting{UserID: uid, Key: "chat.file_mode", Value: "rag", UpdatedAt: now})
	execAll(t, db,
		fmt.Sprintf(`INSERT INTO identity_contact_verifications (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO identity_mfa_settings (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO identity_trusted_devices (user_id) VALUES (%d)`, uid),
		// conversations: one live, one soft-deleted
		fmt.Sprintf(`INSERT INTO chat_conversations (id, user_id) VALUES (%d, %d)`, uid*10+1, uid),
		fmt.Sprintf(`INSERT INTO chat_conversations (id, user_id, deleted_at) VALUES (%d, %d, '2020-01-01 00:00:00')`, uid*10+2, uid),
		// messages, one of them soft deleted, one authored under the conversation only
		fmt.Sprintf(`INSERT INTO chat_messages (user_id, conversation_id) VALUES (%d, %d)`, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_messages (user_id, conversation_id, deleted_at) VALUES (%d, %d, '2020-01-01 00:00:00')`, uid, uid*10+2),
		fmt.Sprintf(`INSERT INTO chat_messages (user_id, conversation_id) VALUES (0, %d)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_feedback (user_id, conversation_id) VALUES (%d, %d)`, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_attachments (user_id, conversation_id) VALUES (%d, %d)`, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_message_chunks (user_id, conversation_id) VALUES (0, %d)`, uid*10+2),
		fmt.Sprintf(`INSERT INTO chat_message_ui_states (user_id, conversation_id) VALUES (%d, %d)`, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_context_records (user_id, conversation_id) VALUES (0, %d)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_conversation_shares (user_id, conversation_id) VALUES (%d, %d)`, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_runs (run_id, user_id, conversation_id) VALUES ('run-%d', %d, %d)`, uid, uid, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_runs (run_id, user_id, conversation_id) VALUES ('run-conv-%d', 0, %d)`, uid, uid*10+2),
		fmt.Sprintf(`INSERT INTO chat_run_events (run_id) VALUES ('run-%d')`, uid),
		fmt.Sprintf(`INSERT INTO chat_run_events (run_id) VALUES ('run-conv-%d')`, uid),
		// projects and their links
		fmt.Sprintf(`INSERT INTO chat_conversation_projects (id, user_id) VALUES (%d, %d)`, uid*10+1, uid),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_mcp_tools VALUES (%d, 1)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_skills VALUES (%d, 1)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_knowledge_bases VALUES (%d, 'x')`, uid*10+1),
		// the user's private skill, linked from somebody else's project too
		fmt.Sprintf(`INSERT INTO skills (id, scope, owner_user_id) VALUES (%d, 'user', %d)`, uid*10+1, uid),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_skills VALUES (999, %d)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO llm_user_providers (user_id) VALUES (%d)`, uid),
		// private KB with a file link, linked from somebody else's project by public id and numeric id
		fmt.Sprintf(`INSERT INTO knowledge_bases (id, public_id, scope, owner_user_id) VALUES (%d, 'kbpub%d', 'user', %d)`, uid*10+1, uid, uid),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_knowledge_bases VALUES (999, 'kbpub%d')`, uid),
		fmt.Sprintf(`INSERT INTO chat_conversation_project_knowledge_bases VALUES (998, '%d')`, uid*10+1),
		// files: live and soft-deleted, with storage paths
		fmt.Sprintf(`INSERT INTO file_objects (id, user_id, status, storage_path) VALUES (%d, %d, 'active', 'u%d/a.bin')`, uid*10+1, uid, uid),
		fmt.Sprintf(`INSERT INTO file_objects (id, user_id, status, storage_path, deleted_at) VALUES (%d, %d, 'deleted', 'u%d/b.bin', '2020-01-01 00:00:00')`, uid*10+2, uid, uid),
		fmt.Sprintf(`INSERT INTO file_objects (id, user_id, status, storage_path) VALUES (%d, %d, 'active', 'u%d/a.bin')`, uid*10+3, uid, uid),
		fmt.Sprintf(`INSERT INTO file_chunks (user_id, file_object_id) VALUES (%d, %d)`, uid, uid*10+1),
		// the user's file inside a KB of somebody else, and a file link in the user's own KB
		fmt.Sprintf(`INSERT INTO knowledge_base_files VALUES (777, %d)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO knowledge_base_files VALUES (%d, 555)`, uid*10+1),
		fmt.Sprintf(`INSERT INTO file_storage_quotas (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO user_memories (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO user_memory_embeddings (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO permission_group_user_access VALUES (1, %d)`, uid),
		// retained for the financial audit
		fmt.Sprintf(`INSERT INTO billing_usage_ledgers (user_id) VALUES (%d)`, uid),
		fmt.Sprintf(`INSERT INTO billing_payment_orders (user_id) VALUES (%d)`, uid),
	)
	db.Create(&balanceTxRow{UserID: uid, AccountID: 1, Type: "topup", AmountNanousd: 1})
}

// footprint counts everything that belongs to the user across all families.
func footprint(t *testing.T, db *gorm.DB, uid uint) map[string]int64 {
	t.Helper()
	cu := fmt.Sprintf("conversation_id IN (%d, %d)", uid*10+1, uid*10+2)
	return map[string]int64{
		"account":       count(t, db.Unscoped(), "identity_users", "id = ?", uid),
		"credentials":   count(t, db, "identity_credentials", "user_id = ?", uid),
		"sessions":      count(t, db, "identity_sessions", "user_id = ?", uid),
		"auth_events":   count(t, db, "identity_auth_events", "user_id = ?", uid),
		"verifications": count(t, db, "identity_contact_verifications", "user_id = ?", uid),
		"links":         count(t, db, "identity_user_links", "user_id = ?", uid),
		"mfa":           count(t, db, "identity_mfa_settings", "user_id = ?", uid),
		"devices":       count(t, db, "identity_trusted_devices", "user_id = ?", uid),
		"conversations": count(t, db, "chat_conversations", "user_id = ?", uid),
		"messages":      count(t, db, "chat_messages", "user_id = ? OR "+cu, uid),
		"feedback":      count(t, db, "chat_feedback", "user_id = ? OR "+cu, uid),
		"attachments":   count(t, db, "chat_attachments", "user_id = ? OR "+cu, uid),
		"chunks":        count(t, db, "chat_message_chunks", "user_id = ? OR "+cu, uid),
		"ui_states":     count(t, db, "chat_message_ui_states", "user_id = ? OR "+cu, uid),
		"context":       count(t, db, "chat_context_records", "user_id = ? OR "+cu, uid),
		"shares":        count(t, db, "chat_conversation_shares", "user_id = ? OR "+cu, uid),
		"runs":          count(t, db, "chat_runs", "user_id = ? OR "+cu, uid),
		"run_events":    count(t, db, "chat_run_events", "run_id IN (?, ?)", fmt.Sprintf("run-%d", uid), fmt.Sprintf("run-conv-%d", uid)),
		"projects":      count(t, db, "chat_conversation_projects", "user_id = ?", uid),
		"project_mcp":   count(t, db, "chat_conversation_project_mcp_tools", "project_id = ?", uid*10+1),
		"project_skill": count(t, db, "chat_conversation_project_skills", "project_id = ? OR skill_id = ?", uid*10+1, uid*10+1),
		"project_kb":    count(t, db, "chat_conversation_project_knowledge_bases", "project_id = ? OR knowledge_base_id IN (?, ?)", uid*10+1, fmt.Sprintf("kbpub%d", uid), fmt.Sprint(uid*10+1)),
		"skills":        count(t, db, "skills", "owner_user_id = ?", uid),
		"providers":     count(t, db, "llm_user_providers", "user_id = ?", uid),
		"kb_files":      count(t, db, "knowledge_base_files", "knowledge_base_id = ? OR file_object_id IN (?, ?, ?)", uid*10+1, uid*10+1, uid*10+2, uid*10+3),
		"kbs":           count(t, db, "knowledge_bases", "owner_user_id = ?", uid),
		"file_chunks":   count(t, db, "file_chunks", "user_id = ?", uid),
		"files":         count(t, db, "file_objects", "user_id = ?", uid),
		"quotas":        count(t, db, "file_storage_quotas", "user_id = ?", uid),
		"memories":      count(t, db, "user_memories", "user_id = ?", uid),
		"embeddings":    count(t, db, "user_memory_embeddings", "user_id = ?", uid),
		"settings":      count(t, db, "user_settings", "user_id = ?", uid),
		"group_access":  count(t, db, "permission_group_user_access", "user_id = ?", uid),
		"subscriptions": count(t, db, "billing_subscriptions", "user_id = ?", uid),
		"billing_acct":  count(t, db, "billing_accounts", "user_id = ?", uid),
	}
}

func deletionFixture(t *testing.T) (*Accounts, *gorm.DB, Account, Account) {
	t.Helper()
	env := newEnv(t, Ports{})
	execAll(t, env.DB, purgeSchema...)
	a := seedAccount(t, env.DB, "victim")
	b := seedAccount(t, env.DB, "bystander")
	seedUserData(t, env.DB, a)
	seedUserData(t, env.DB, b)
	return newAccounts(env, nil), env.DB, a, b
}

func TestHardDeleteRemovesEveryFamilyIncludingSoftDeletedRows(t *testing.T) {
	acc, db, a, b := deletionFixture(t)
	before := footprint(t, db, a.ID)
	for name, n := range before {
		if n == 0 {
			t.Fatalf("test setup: nothing seeded for %s", name)
		}
	}
	bystanderBefore := footprint(t, db, b.ID)

	if err := acc.HardDelete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	for name, n := range footprint(t, db, a.ID) {
		if n != 0 {
			t.Errorf("%s: %d rows remain", name, n)
		}
	}
	after := footprint(t, db, b.ID)
	for name, n := range bystanderBefore {
		if after[name] != n {
			t.Errorf("bystander %s changed: %d -> %d", name, n, after[name])
		}
	}
	// Soft-deleted rows are gone for good, not merely hidden.
	if n := count(t, db, "chat_conversations", "1 = 1"); n != 2 {
		t.Errorf("conversation rows left: %d", n)
	}
	// Other users' links to the deleted user's skill and knowledge bases are gone,
	// but links that belong to unrelated things survive.
	if n := count(t, db, "chat_conversation_project_skills", "project_id = 999"); n != 1 {
		t.Errorf("the bystander's own project link to its own skill must stay: %d", n)
	}
}

func TestHardDeleteKeepsFinancialAuditData(t *testing.T) {
	acc, db, a, _ := deletionFixture(t)
	if err := acc.HardDelete(context.Background(), a.ID); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "billing_usage_ledgers", "user_id = ?", a.ID) != 1 {
		t.Error("usage ledger must be retained")
	}
	if count(t, db, "billing_payment_orders", "user_id = ?", a.ID) != 1 {
		t.Error("payment orders must be retained")
	}
	if count(t, db, "billing_balance_transactions", "user_id = ?", a.ID) != 1 {
		t.Error("balance transactions must be retained")
	}
}

func TestHardDeleteRefusedWhenBuiltinKnowledgeBaseReferencesOwnedFile(t *testing.T) {
	acc, db, a, _ := deletionFixture(t)
	execAll(t, db,
		`INSERT INTO knowledge_bases (id, public_id, scope, owner_user_id) VALUES (9000, 'builtin1', 'builtin', 0)`,
		fmt.Sprintf(`INSERT INTO knowledge_base_files VALUES (9000, %d)`, a.ID*10+1))
	before := footprint(t, db, a.ID)
	err := acc.HardDelete(context.Background(), a.ID)
	if !errors.Is(err, ErrOwnerFileReference) {
		t.Fatalf("got %v", err)
	}
	if ErrOwnerFileReference.Code != "knowledge_base.owner_file_reference" || ErrOwnerFileReference.Message != "user owns files referenced by builtin knowledge bases" {
		t.Fatalf("code/message: %s / %s", ErrOwnerFileReference.Code, ErrOwnerFileReference.Message)
	}
	after := footprint(t, db, a.ID)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s changed %d -> %d: nothing may be deleted", k, v, after[k])
		}
	}
}

func TestHardDeleteIgnoresBuiltinReferencesToInactiveOrOthersFiles(t *testing.T) {
	acc, db, a, b := deletionFixture(t)
	execAll(t, db,
		`INSERT INTO knowledge_bases (id, public_id, scope, owner_user_id) VALUES (9000, 'builtin1', 'builtin', 0)`,
		// the victim's soft-deleted file and the bystander's file are referenced, not the victim's active ones
		fmt.Sprintf(`INSERT INTO knowledge_base_files VALUES (9000, %d)`, a.ID*10+2),
		fmt.Sprintf(`INSERT INTO knowledge_base_files VALUES (9000, %d)`, b.ID*10+1))
	if err := acc.HardDelete(context.Background(), a.ID); err != nil {
		t.Fatalf("got %v", err)
	}
}

func TestHardDeleteIsAtomic(t *testing.T) {
	acc, db, a, _ := deletionFixture(t)
	before := footprint(t, db, a.ID)
	// A trigger makes the very last step (the account row) fail.
	execAll(t, db, `CREATE TRIGGER no_delete BEFORE DELETE ON identity_users BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	err := acc.HardDelete(context.Background(), a.ID)
	if err == nil || errors.Is(err, ErrOwnerFileReference) {
		t.Fatalf("expected a step failure, got %v", err)
	}
	if got := err.Error(); !contains(got, "account") {
		t.Fatalf("error should name the failing step: %q", got)
	}
	after := footprint(t, db, a.ID)
	for k, v := range before {
		if after[k] != v {
			t.Errorf("%s: %d -> %d, the transaction must roll back", k, v, after[k])
		}
	}
}

func TestHardDeleteNamesAnEarlierFailingStep(t *testing.T) {
	acc, db, a, _ := deletionFixture(t)
	execAll(t, db, `CREATE TRIGGER no_msg_delete BEFORE DELETE ON chat_messages BEGIN SELECT RAISE(ABORT, 'blocked'); END`)
	err := acc.HardDelete(context.Background(), a.ID)
	if err == nil || !contains(err.Error(), "messages") {
		t.Fatalf("got %v", err)
	}
	if count(t, db, "identity_credentials", "user_id = ?", a.ID) != 1 {
		t.Fatal("earlier steps must roll back")
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

func TestHardDeleteUnknownUserSucceeds(t *testing.T) {
	acc, db, _, b := deletionFixture(t)
	if err := acc.HardDelete(context.Background(), 12345); err != nil {
		t.Fatal(err)
	}
	if count(t, db, "identity_users", "id = ?", b.ID) != 1 {
		t.Fatal("bystander touched")
	}
}

func TestHardDeleteWorksWhenOtherAreasTablesAreAbsent(t *testing.T) {
	env := newEnv(t, Ports{}) // none of the chat/file/knowledge tables exist
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "lonely")
	seedCredential(t, env.DB, u.ID)
	env.DB.Create(&Setting{UserID: u.ID, Key: "chat.file_mode", Value: "rag"})
	if err := acc.HardDelete(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	if count(t, env.DB.Unscoped(), "identity_users", "id = ?", u.ID) != 0 || count(t, env.DB, "identity_credentials", "user_id = ?", u.ID) != 0 || count(t, env.DB, "user_settings", "user_id = ?", u.ID) != 0 {
		t.Fatal("rows remain")
	}
}

func TestHardDeleteRemovesAlreadySoftDeletedAccount(t *testing.T) {
	env := newEnv(t, Ports{})
	acc := newAccounts(env, nil)
	u := seedAccount(t, env.DB, "ghost")
	env.DB.Delete(&Account{}, u.ID)
	if err := acc.HardDelete(context.Background(), u.ID); err != nil {
		t.Fatal(err)
	}
	if count(t, env.DB.Unscoped(), "identity_users", "id = ?", u.ID) != 0 {
		t.Fatal("soft-deleted account row must be purged")
	}
}

func TestFileStoragePaths(t *testing.T) {
	acc, _, a, b := deletionFixture(t)
	got, err := acc.FileStoragePaths(context.Background(), a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{fmt.Sprintf("u%d/a.bin", a.ID), fmt.Sprintf("u%d/b.bin", a.ID)}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("got %v, want %v (distinct, soft-deleted files included)", got, want)
	}
	if none, _ := acc.FileStoragePaths(context.Background(), 12345); none == nil || len(none) != 0 {
		t.Fatalf("no files: %v", none)
	}
	_ = b
	env := newEnv(t, Ports{})
	if got, err := newAccounts(env, nil).FileStoragePaths(context.Background(), 1); err != nil || len(got) != 0 {
		t.Fatalf("without the file table: %v %v", got, err)
	}
}
