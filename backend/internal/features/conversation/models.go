package conversation

import (
	"time"

	"github.com/riochat/riochat/backend/internal/platform/database"
)

// Conversation statuses.
const (
	statusActive   = "active"
	statusArchived = "archived"
)

// Message statuses.
const (
	msgPending     = "pending"
	msgSuccess     = "success"
	msgError       = "error"
	msgInterrupted = "interrupted"
	msgCanceled    = "canceled"
	msgBlocked     = "blocked"
)

// Message roles.
const (
	roleUser      = "user"
	roleAssistant = "assistant"
	roleSystem    = "system"
	roleTool      = "tool"
)

// Branch reasons.
const (
	branchDefault = "default"
	branchRetry   = "retry"
	branchEdit    = "edit"
)

// Conversation is one chat thread owned by a user.
type Conversation struct {
	database.Model
	UserID                uint       `gorm:"column:user_id;not null;index" json:"userId"`
	ProjectID             *uint      `gorm:"column:project_id;index" json:"projectId"`
	PublicID              string     `gorm:"column:public_id;size:32;index" json:"publicId"`
	Title                 string     `gorm:"column:title;size:255;default:''" json:"title"`
	LabelsJSON            string     `gorm:"column:labels_json;type:text;default:'[]'" json:"labelsJson"`
	LabelsManuallyManaged bool       `gorm:"column:labels_manually_managed;default:false" json:"labelsManuallyManaged"`
	ModelName             string     `gorm:"column:model;size:128" json:"model"`
	Provider              string     `gorm:"column:provider;size:32;index" json:"provider"`
	SessionKey            string     `gorm:"column:session_key;size:128;uniqueIndex" json:"sessionKey"`
	IsStarred             bool       `gorm:"column:is_starred;index" json:"isStarred"`
	StarredAt             *time.Time `gorm:"column:starred_at;index" json:"starredAt"`
	MessageCount          int        `gorm:"column:message_count;default:0" json:"messageCount"`
	Status                string     `gorm:"column:status;size:32;index" json:"status"`
	ContextPolicy         string     `gorm:"column:context_policy;type:text" json:"contextPolicy"`
	LastCompactedAt       *time.Time `gorm:"column:last_compacted_at" json:"lastCompactedAt"`
	LastResponseID        string     `gorm:"column:last_response_id;size:128;index" json:"lastResponseId"`
	LastPromptFingerprint string     `gorm:"column:last_prompt_fingerprint;size:64;index" json:"lastPromptFingerprint"`
}

// TableName implements database.Tabler.
func (Conversation) TableName() string { return "chat_conversations" }

// Project is a folder that groups conversations and carries defaults.
type Project struct {
	database.Model
	UserID         uint   `gorm:"column:user_id;index" json:"userId"`
	PublicID       string `gorm:"column:public_id;size:32;uniqueIndex" json:"publicId"`
	Name           string `gorm:"column:name;size:80" json:"name"`
	Description    string `gorm:"column:description;size:255" json:"description"`
	SystemPrompt   string `gorm:"column:system_prompt;type:text;not null;default:''" json:"systemPrompt"`
	DefaultModel   string `gorm:"column:default_model;size:128" json:"defaultModel"`
	MCPDefaultMode string `gorm:"column:mcp_default_mode;size:16;default:'inherit'" json:"mcpDefaultMode"`
	Color          string `gorm:"column:color;size:32" json:"color"`
	Icon           string `gorm:"column:icon;size:32" json:"icon"`
	SortOrder      int    `gorm:"column:sort_order;index" json:"sortOrder"`
	Status         string `gorm:"column:status;size:32;default:'active';index" json:"status"`
}

// TableName implements database.Tabler.
func (Project) TableName() string { return "chat_conversation_projects" }

// ProjectMCPTool links a project to a default MCP tool.
type ProjectMCPTool struct {
	ProjectID uint `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	ToolID    uint `gorm:"column:tool_id;primaryKey;autoIncrement:false"`
	SortOrder int  `gorm:"column:sort_order"`
}

// TableName implements database.Tabler.
func (ProjectMCPTool) TableName() string { return "chat_conversation_project_mcp_tools" }

// ProjectSkill links a project to a default skill.
type ProjectSkill struct {
	ProjectID uint `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	SkillID   uint `gorm:"column:skill_id;primaryKey;autoIncrement:false"`
	SortOrder int  `gorm:"column:sort_order"`
}

// TableName implements database.Tabler.
func (ProjectSkill) TableName() string { return "chat_conversation_project_skills" }

// ProjectKnowledgeBase links a project to a default knowledge base.
type ProjectKnowledgeBase struct {
	ProjectID       uint   `gorm:"column:project_id;primaryKey;autoIncrement:false"`
	KnowledgeBaseID string `gorm:"column:knowledge_base_id;size:64;primaryKey;autoIncrement:false"`
	SortOrder       int    `gorm:"column:sort_order"`
}

// TableName implements database.Tabler.
func (ProjectKnowledgeBase) TableName() string { return "chat_conversation_project_knowledge_bases" }

// Share statuses.
const (
	shareActive  = "active"
	shareRevoked = "revoked"
	shareExpired = "expired"
	shareNone    = "none"
)

// Share is an immutable snapshot of a conversation exposed through a link.
type Share struct {
	database.Model
	ShareID               string     `gorm:"column:share_id;size:32;uniqueIndex" json:"shareId"`
	ConversationID        uint       `gorm:"column:conversation_id;index" json:"conversationId"`
	UserID                uint       `gorm:"column:user_id;index" json:"userId"`
	Status                string     `gorm:"column:status;size:32;default:'active'" json:"status"`
	TitleSnapshot         string     `gorm:"column:title_snapshot;size:255" json:"titleSnapshot"`
	ModelSnapshot         string     `gorm:"column:model_snapshot;size:128" json:"modelSnapshot"`
	MessageIDsJSON        string     `gorm:"column:message_ids_json;type:text" json:"messageIdsJson"`
	DefaultMessageIDsJSON string     `gorm:"column:default_message_ids_json;type:text" json:"defaultMessageIdsJson"`
	RevokedAt             *time.Time `gorm:"column:revoked_at;index" json:"revokedAt"`
	RegeneratedAt         *time.Time `gorm:"column:regenerated_at" json:"regeneratedAt"`
	LastAccessedAt        *time.Time `gorm:"column:last_accessed_at;index" json:"lastAccessedAt"`
}

// TableName implements database.Tabler.
func (Share) TableName() string { return "chat_conversation_shares" }

// Message is one node in a conversation's message tree.
type Message struct {
	database.Model
	ConversationID           uint       `gorm:"column:conversation_id;index" json:"conversationId"`
	UserID                   uint       `gorm:"column:user_id;index" json:"userId"`
	PublicID                 string     `gorm:"column:public_id;size:32;uniqueIndex" json:"publicId"`
	ParentMessageID          *uint      `gorm:"column:parent_message_id;index" json:"parentMessageId"`
	RunID                    string     `gorm:"column:run_id;size:64;index" json:"runId"`
	Role                     string     `gorm:"column:role;size:16" json:"role"`
	ContentType              string     `gorm:"column:content_type;size:32" json:"contentType"`
	Content                  string     `gorm:"column:content;type:text" json:"content"`
	ReasoningContent         string     `gorm:"column:reasoning_content;type:text;not null;default:''" json:"reasoningContent"`
	BranchReason             string     `gorm:"column:branch_reason;size:32;default:'default'" json:"branchReason"`
	SourceMessageID          *uint      `gorm:"column:source_message_id" json:"sourceMessageId"`
	TokenUsage               int64      `gorm:"column:token_usage;default:0" json:"tokenUsage"`
	InputTokens              int64      `gorm:"column:input_tokens;default:0" json:"inputTokens"`
	OutputTokens             int64      `gorm:"column:output_tokens;default:0" json:"outputTokens"`
	CacheReadTokens          int64      `gorm:"column:cache_read_tokens;default:0" json:"cacheReadTokens"`
	CacheWriteTokens         int64      `gorm:"column:cache_write_tokens;default:0" json:"cacheWriteTokens"`
	ReasoningTokens          int64      `gorm:"column:reasoning_tokens;default:0" json:"reasoningTokens"`
	LatencyMS                int64      `gorm:"column:latency_ms;default:0" json:"latencyMs"`
	ReasoningEffort          *string    `gorm:"column:reasoning_effort;size:16" json:"reasoningEffort"`
	BilledCurrency           string     `gorm:"column:billed_currency;size:16;default:'USD'" json:"billedCurrency"`
	BilledNanoUSD            int64      `gorm:"column:billed_nanousd;default:0" json:"billedNanousd"`
	PricingSnapshot          string     `gorm:"column:pricing_snapshot;type:text" json:"pricingSnapshot"`
	Status                   string     `gorm:"column:status;size:32;index" json:"status"`
	ErrorCode                string     `gorm:"column:error_code;size:64" json:"errorCode"`
	ErrorMessage             string     `gorm:"column:error_message;size:255" json:"errorMessage"`
	ModerationEventID        string     `gorm:"column:moderation_event_id;size:40" json:"moderationEventId"`
	ModerationCategoriesJSON string     `gorm:"column:moderation_categories_json;type:text;default:'[]'" json:"moderationCategoriesJson"`
	KnowledgeSourcesJSON     string     `gorm:"column:knowledge_sources_json;type:text;default:'[]'" json:"knowledgeSourcesJson"`
	EditedAt                 *time.Time `gorm:"column:edited_at;index" json:"editedAt"`
}

// TableName implements database.Tabler.
func (Message) TableName() string { return "chat_messages" }

// Feedback is a thumbs up/down by one user on one assistant message.
type Feedback struct {
	database.Model
	UserID         uint   `gorm:"column:user_id;uniqueIndex:ux_chat_feedback_user_message" json:"userId"`
	ConversationID uint   `gorm:"column:conversation_id;index" json:"conversationId"`
	MessageID      uint   `gorm:"column:message_id;uniqueIndex:ux_chat_feedback_user_message;index" json:"messageId"`
	Feedback       string `gorm:"column:feedback;size:16" json:"feedback"`
}

// TableName implements database.Tabler.
func (Feedback) TableName() string { return "chat_feedback" }

// Attachment links a file to a message.
type Attachment struct {
	database.Model
	ConversationID uint       `gorm:"column:conversation_id;index" json:"conversationId"`
	MessageID      uint       `gorm:"column:message_id;index" json:"messageId"`
	UserID         uint       `gorm:"column:user_id;index" json:"userId"`
	FileID         string     `gorm:"column:file_id;size:64;index" json:"fileId"`
	Kind           string     `gorm:"column:kind;size:32" json:"kind"`
	FileName       string     `gorm:"column:file_name;size:255" json:"fileName"`
	MimeType       string     `gorm:"column:mime_type;size:128" json:"mimeType"`
	FileSize       int64      `gorm:"column:file_size" json:"fileSize"`
	SHA256         string     `gorm:"column:sha256;size:64" json:"sha256"`
	StoragePath    string     `gorm:"column:storage_path;size:512" json:"storagePath"`
	Status         string     `gorm:"column:status;size:32;default:'active'" json:"status"`
	MetaJSON       string     `gorm:"column:meta_json;type:text" json:"metaJson"`
	UploadedAt     *time.Time `gorm:"column:uploaded_at" json:"uploadedAt"`
}

// TableName implements database.Tabler.
func (Attachment) TableName() string { return "chat_attachments" }

// Run statuses.
const (
	runRunning     = "running"
	runSuccess     = "success"
	runError       = "error"
	runCanceled    = "canceled"
	runInterrupted = "interrupted"
	runBlocked     = "blocked"
)
