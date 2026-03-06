package conversation

import (
	"github.com/riochat/riochat/backend/internal/platform/migrate"
)

// Migrations returns the area's idempotent steps.
func Migrations() []migrate.Step {
	return []migrate.Step{
		migrate.AutoMigrate("tables",
			&Conversation{}, &Project{}, &ProjectMCPTool{}, &ProjectSkill{}, &ProjectKnowledgeBase{},
			&Share{}, &Message{}, &Feedback{}, &Attachment{}, &Run{}, &RunEvent{}, &ContextRecord{},
			&MessageChunk{}, &UIState{}, &FileObject{}, &FileChunk{}, &StorageQuota{},
		),
		migrate.Exec("conversation_list_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversations_list ON chat_conversations(user_id, status, is_starred, updated_at, id)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversations_list ON chat_conversations(user_id, status, is_starred, updated_at DESC, id DESC)",
		}),
		migrate.Exec("conversation_star_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversations_starred ON chat_conversations(user_id, status, is_starred, starred_at, id)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversations_starred ON chat_conversations(user_id, status, is_starred, starred_at DESC, id DESC)",
		}),
		migrate.Exec("conversation_project_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversations_project ON chat_conversations(user_id, project_id, status, updated_at, id) WHERE deleted_at IS NULL",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversations_project ON chat_conversations(user_id, project_id, status, updated_at DESC, id DESC) WHERE deleted_at IS NULL",
		}),
		migrate.Exec("project_order_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversation_projects_order ON chat_conversation_projects(user_id, status, sort_order, id) WHERE deleted_at IS NULL",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversation_projects_order ON chat_conversation_projects(user_id, status, sort_order, id) WHERE deleted_at IS NULL",
		}),
		migrate.Exec("share_active_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversation_shares_active ON chat_conversation_shares(conversation_id, updated_at, id) WHERE status = 'active'",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversation_shares_active ON chat_conversation_shares(conversation_id, updated_at, id) WHERE status = 'active'",
		}),
		migrate.Exec("share_owner_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_conversation_shares_owner ON chat_conversation_shares(user_id, status, updated_at, id)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_conversation_shares_owner ON chat_conversation_shares(user_id, status, updated_at, id)",
		}),
		migrate.Exec("message_conversation_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE INDEX IF NOT EXISTS idx_chat_messages_conversation_id ON chat_messages(conversation_id, id)",
			"postgres": "CREATE INDEX IF NOT EXISTS idx_chat_messages_conversation_id ON chat_messages(conversation_id, id)",
		}),
		migrate.Exec("file_content_unique_idx", migrate.PhaseSchema, map[string]string{
			"sqlite":   "CREATE UNIQUE INDEX IF NOT EXISTS ux_file_objects_user_sha ON file_objects(user_id, sha256, size_bytes) WHERE status = 'active' AND deleted_at IS NULL AND sha256 <> ''",
			"postgres": "CREATE UNIQUE INDEX IF NOT EXISTS ux_file_objects_user_sha ON file_objects(user_id, sha256, size_bytes) WHERE status = 'active' AND deleted_at IS NULL AND sha256 <> ''",
		}),
	}
}
