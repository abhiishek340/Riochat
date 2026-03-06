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

// Run is one generation attempt.
type Run struct {
	database.Model
	RunID                string     `gorm:"column:run_id;size:64;uniqueIndex" json:"runId"`
	RequestID            string     `gorm:"column:request_id;size:64" json:"requestId"`
	UserID               uint       `gorm:"column:user_id;index" json:"userId"`
	ConversationID       uint       `gorm:"column:conversation_id;index" json:"conversationId"`
	TaskType             string     `gorm:"column:task_type;size:32;default:'chat';index" json:"taskType"`
	Endpoint             string     `gorm:"column:endpoint;size:128" json:"endpoint"`
	Provider             string     `gorm:"column:provider;size:32" json:"provider"`
	ProviderProtocol     string     `gorm:"column:provider_protocol;size:64" json:"providerProtocol"`
	UpstreamID           uint       `gorm:"column:upstream_id" json:"upstreamId"`
	UpstreamModelID      uint       `gorm:"column:upstream_model_id" json:"upstreamModelId"`
	UpstreamName         string     `gorm:"column:upstream_name;size:128" json:"upstreamName"`
	RequestedModelName   string     `gorm:"column:requested_model_name;size:128" json:"requestedModelName"`
	PlatformModelName    string     `gorm:"column:platform_model_name;size:128" json:"platformModelName"`
	RoutedBindingCode    string     `gorm:"column:routed_binding_code;size:128" json:"routedBindingCode"`
	ModelVendor          string     `gorm:"column:model_vendor;size:64" json:"modelVendor"`
	ModelIcon            string     `gorm:"column:model_icon;size:255" json:"modelIcon"`
	UpstreamModelName    string     `gorm:"column:upstream_model_name;size:128" json:"upstreamModelName"`
	InputTokens          int64      `gorm:"column:input_tokens;default:0" json:"inputTokens"`
	OutputTokens         int64      `gorm:"column:output_tokens;default:0" json:"outputTokens"`
	CacheReadTokens      int64      `gorm:"column:cache_read_tokens;default:0" json:"cacheReadTokens"`
	CacheWriteTokens     int64      `gorm:"column:cache_write_tokens;default:0" json:"cacheWriteTokens"`
	ReasoningTokens      int64      `gorm:"column:reasoning_tokens;default:0" json:"reasoningTokens"`
	ReasoningEffort      *string    `gorm:"column:reasoning_effort;size:16" json:"reasoningEffort"`
	ToolCallsCount       int        `gorm:"column:tool_calls_count;default:0" json:"toolCallsCount"`
	FirstTokenLatencyMS  int64      `gorm:"column:first_token_latency_ms;default:0" json:"firstTokenLatencyMs"`
	TotalLatencyMS       int64      `gorm:"column:total_latency_ms;default:0" json:"totalLatencyMs"`
	Status               string     `gorm:"column:status;size:32;index" json:"status"`
	ErrorCode            string     `gorm:"column:error_code;size:64" json:"errorCode"`
	ErrorMessage         string     `gorm:"column:error_message;size:255" json:"errorMessage"`
	ModerationState      string     `gorm:"column:moderation_state;size:32;default:'not_required'" json:"moderationState"`
	ModerationEventID    string     `gorm:"column:moderation_event_id;size:40" json:"moderationEventId"`
	ModerationCategories string     `gorm:"column:moderation_categories_json;type:text;default:'[]'" json:"moderationCategoriesJson"`
	StartedAt            time.Time  `gorm:"column:started_at;not null" json:"startedAt"`
	EndedAt              *time.Time `gorm:"column:ended_at" json:"endedAt"`
}

// TableName implements database.Tabler.
func (Run) TableName() string { return "chat_runs" }

// Run event scopes.
const (
	scopeTraceBlock = "trace_block"
	scopeTraceEvent = "trace_event"
	scopeToolCall   = "tool_call"
)

// RunEvent stores trace blocks, trace events and tool-call details of a run.
type RunEvent struct {
	database.Model
	RunID             string     `gorm:"column:run_id;size:64;uniqueIndex:ux_chat_run_events_scope_id;index" json:"runId"`
	EventScope        string     `gorm:"column:event_scope;size:32;uniqueIndex:ux_chat_run_events_scope_id" json:"eventScope"`
	EventID           string     `gorm:"column:event_id;size:255;uniqueIndex:ux_chat_run_events_scope_id" json:"eventId"`
	ParentEventID     string     `gorm:"column:parent_event_id;size:255" json:"parentEventId"`
	EventType         string     `gorm:"column:event_type;size:64" json:"eventType"`
	Phase             string     `gorm:"column:phase;size:64" json:"phase"`
	Stage             string     `gorm:"column:stage;size:64" json:"stage"`
	RoundID           string     `gorm:"column:round_id;size:64" json:"roundId"`
	Title             string     `gorm:"column:title;size:255" json:"title"`
	Summary           string     `gorm:"column:summary;type:text" json:"summary"`
	ContentMarkdown   string     `gorm:"column:content_markdown;type:text" json:"contentMarkdown"`
	Status            string     `gorm:"column:status;size:32" json:"status"`
	Seq               int        `gorm:"column:seq" json:"seq"`
	PayloadJSON       string     `gorm:"column:payload_json;type:text" json:"payloadJson"`
	ToolCallID        string     `gorm:"column:tool_call_id;size:255;index" json:"toolCallId"`
	ToolName          string     `gorm:"column:tool_name;size:128" json:"toolName"`
	MCPServerSnapshot string     `gorm:"column:mcp_server_snapshot;type:text" json:"mcpServerSnapshot"`
	InputJSON         string     `gorm:"column:input_json;type:text" json:"inputJson"`
	OutputJSON        string     `gorm:"column:output_json;type:text" json:"outputJson"`
	ErrorJSON         string     `gorm:"column:error_json;type:text" json:"errorJson"`
	LatencyMS         int64      `gorm:"column:latency_ms;default:0" json:"latencyMs"`
	StartedAt         *time.Time `gorm:"column:started_at" json:"startedAt"`
	EndedAt           *time.Time `gorm:"column:ended_at" json:"endedAt"`
}

// TableName implements database.Tabler.
func (RunEvent) TableName() string { return "chat_run_events" }

// Context record types.
const (
	recordSnapshot = "snapshot"
	recordArtifact = "artifact"
)

// ContextRecord holds rolling compaction snapshots and prompt-context artifacts.
type ContextRecord struct {
	database.Model
	UserID                uint       `gorm:"column:user_id;index" json:"userId"`
	ConversationID        uint       `gorm:"column:conversation_id;index" json:"conversationId"`
	MessageID             uint       `gorm:"column:message_id;index" json:"messageId"`
	RunID                 string     `gorm:"column:run_id;size:64;index" json:"runId"`
	RecordType            string     `gorm:"column:record_type;size:16;index" json:"recordType"`
	Kind                  string     `gorm:"column:kind;size:64" json:"kind"`
	SourceType            string     `gorm:"column:source_type;size:64" json:"sourceType"`
	SourceID              string     `gorm:"column:source_id;size:128" json:"sourceId"`
	SourceTitle           string     `gorm:"column:source_title;size:255" json:"sourceTitle"`
	Content               string     `gorm:"column:content;type:text" json:"content"`
	ContentHash           string     `gorm:"column:content_hash;size:64" json:"contentHash"`
	TokenEstimate         int        `gorm:"column:token_estimate" json:"tokenEstimate"`
	Score                 float64    `gorm:"column:score" json:"score"`
	MetadataJSON          string     `gorm:"column:metadata_json;type:text" json:"metadataJson"`
	ExpiresAt             *time.Time `gorm:"column:expires_at" json:"expiresAt"`
	CoveredUntilMessageID uint       `gorm:"column:covered_until_message_id;default:0;index" json:"coveredUntilMessageId"`
	CoveredUntilPublicID  string     `gorm:"column:covered_until_public_id;size:32;index" json:"coveredUntilPublicId"`
	CoveragePathHash      string     `gorm:"column:coverage_path_hash;size:64;index" json:"coveragePathHash"`
	CoveredMessageCount   int        `gorm:"column:covered_message_count;default:0" json:"coveredMessageCount"`
}

// TableName implements database.Tabler.
func (ContextRecord) TableName() string { return "chat_context_records" }

// MessageChunk is an embedding chunk of a message used for history recall.
type MessageChunk struct {
	database.Model
	ConversationID     uint   `gorm:"column:conversation_id;index" json:"conversationId"`
	MessageID          uint   `gorm:"column:message_id;index" json:"messageId"`
	UserID             uint   `gorm:"column:user_id;index" json:"userId"`
	Role               string `gorm:"column:role;size:16" json:"role"`
	ChunkIndex         int    `gorm:"column:chunk_index" json:"chunkIndex"`
	Content            string `gorm:"column:content;type:text" json:"content"`
	TokenCount         int    `gorm:"column:token_count" json:"tokenCount"`
	EmbeddingSignature string `gorm:"column:embedding_signature;size:128" json:"embeddingSignature"`
}

// TableName implements database.Tabler.
func (MessageChunk) TableName() string { return "chat_message_chunks" }

// UIState is the persisted state of one interactive block in a message. The
// table has no soft delete.
type UIState struct {
	ID             uint      `gorm:"primaryKey" json:"id"`
	UserID         uint      `gorm:"column:user_id;index" json:"userId"`
	ConversationID uint      `gorm:"column:conversation_id;index" json:"conversationId"`
	MessageID      uint      `gorm:"column:message_id;uniqueIndex:ux_chat_message_ui_states_block" json:"messageId"`
	BlockID        string    `gorm:"column:block_id;size:64;uniqueIndex:ux_chat_message_ui_states_block" json:"blockId"`
	StateJSON      string    `gorm:"column:state_json;type:text;default:'{}'" json:"stateJson"`
	CreatedAt      time.Time `json:"createdAt"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// TableName implements database.Tabler.
func (UIState) TableName() string { return "chat_message_ui_states" }

// FileObject is stored file metadata.
type FileObject struct {
	database.Model
	FileID            string     `gorm:"column:file_id;size:64;uniqueIndex" json:"fileId"`
	UserID            uint       `gorm:"column:user_id;index" json:"userId"`
	Purpose           string     `gorm:"column:purpose;size:32" json:"purpose"`
	FileName          string     `gorm:"column:file_name;size:255" json:"fileName"`
	MimeType          string     `gorm:"column:mime_type;size:128" json:"mimeType"`
	DetectedMIME      string     `gorm:"column:detected_mime;size:128" json:"detectedMime"`
	FileCategory      string     `gorm:"column:file_category;size:32" json:"fileCategory"`
	SizeBytes         int64      `gorm:"column:size_bytes" json:"sizeBytes"`
	SHA256            string     `gorm:"column:sha256;size:64;index" json:"sha256"`
	StoragePath       string     `gorm:"column:storage_path;size:512" json:"storagePath"`
	Status            string     `gorm:"column:status;size:32;default:'active';index" json:"status"`
	ProcessingStatus  string     `gorm:"column:processing_status;size:32" json:"processingStatus"`
	ProcessingReady   bool       `gorm:"column:processing_ready" json:"processingReady"`
	ProcessingErrCode string     `gorm:"column:processing_error_code;size:64" json:"processingErrorCode"`
	ProcessingErrMsg  string     `gorm:"column:processing_error_message;size:255" json:"processingErrorMessage"`
	ExtractStatus     string     `gorm:"column:extract_status;size:32" json:"extractStatus"`
	ExtractText       string     `gorm:"column:extract_text;type:text" json:"-"`
	PreviewText       string     `gorm:"column:preview_text;type:text" json:"previewText"`
	ExtractChars      int        `gorm:"column:extract_chars" json:"extractChars"`
	PageCount         int        `gorm:"column:page_count" json:"pageCount"`
	OCRUsed           bool       `gorm:"column:ocr_used" json:"ocrUsed"`
	EmbedStatus       string     `gorm:"column:embed_status;size:32" json:"embedStatus"`
	EmbedSignature    string     `gorm:"column:embed_signature;size:128" json:"embedSignature"`
	EmbedError        string     `gorm:"column:embed_error;size:255" json:"embedError"`
	RAGOptOut         bool       `gorm:"column:rag_opt_out" json:"ragOptOut"`
	ChunkCount        int        `gorm:"column:chunk_count" json:"chunkCount"`
	LastAccessedAt    *time.Time `gorm:"column:last_accessed_at" json:"lastAccessedAt"`
	ExpiresAt         *time.Time `gorm:"column:expires_at" json:"expiresAt"`
	ProcessingStarted *time.Time `gorm:"column:processing_started_at" json:"-"`
	ProcessingDone    *time.Time `gorm:"column:processing_completed_at" json:"-"`
}

// TableName implements database.Tabler.
func (FileObject) TableName() string { return "file_objects" }

// FileChunk is a retrievable chunk of a file. The columns are the ones the
// retrieval index writes (see the ragindex area, which owns their content);
// the model is declared here so the table exists whenever the file store does
// and so a file removal can delete its chunks.
type FileChunk struct {
	ID                 uint      `gorm:"primaryKey" json:"id"`
	FileObjectID       uint      `gorm:"column:file_obj_id;index" json:"fileObjectId"`
	UserID             uint      `gorm:"column:user_id;index" json:"userId"`
	ChunkIndex         int       `gorm:"column:chunk_index;default:0" json:"chunkIndex"`
	PageNum            int       `gorm:"column:page_num;default:0" json:"pageNum"`
	CharOffset         int       `gorm:"column:char_offset;default:0" json:"charOffset"`
	Modality           string    `gorm:"column:modality;size:16;default:text" json:"modality"`
	Content            string    `gorm:"column:content;type:text" json:"content"`
	TokenCount         int       `gorm:"column:token_count" json:"tokenCount"`
	EmbeddingSignature string    `gorm:"column:embedding_signature;size:64;index" json:"embeddingSignature"`
	CreatedAt          time.Time `gorm:"column:created_at" json:"createdAt"`
}

// TableName implements database.Tabler.
func (FileChunk) TableName() string { return "file_chunks" }

// StorageQuota is the per-user file storage allowance.
type StorageQuota struct {
	database.Model
	UserID        uint  `gorm:"column:user_id;uniqueIndex" json:"userId"`
	QuotaBytes    int64 `gorm:"column:quota_bytes;default:0" json:"quotaBytes"`
	UsedBytes     int64 `gorm:"column:used_bytes;default:0" json:"usedBytes"`
	ReservedBytes int64 `gorm:"column:reserved_bytes;default:0" json:"reservedBytes"`
}

// TableName implements database.Tabler.
func (StorageQuota) TableName() string { return "file_storage_quotas" }
