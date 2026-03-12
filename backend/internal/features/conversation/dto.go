package conversation

import (
	"encoding/json"
	"strings"
	"time"
)

// ConversationDTO is the conversation object returned to clients.
type ConversationDTO struct {
	PublicID            string     `json:"publicID"`
	UserID              uint       `json:"userID"`
	ProjectID           string     `json:"projectID"`
	ProjectName         string     `json:"projectName"`
	Title               string     `json:"title"`
	LabelsJSON          string     `json:"labelsJSON"`
	Model               string     `json:"model"`
	Provider            string     `json:"provider"`
	SessionKey          string     `json:"sessionKey"`
	IsStarred           bool       `json:"isStarred"`
	StarredAt           *time.Time `json:"starredAt"`
	MessageCount        int        `json:"messageCount"`
	Status              string     `json:"status"`
	ContextPolicyJSON   string     `json:"contextPolicyJSON"`
	LastCompactedAt     *time.Time `json:"lastCompactedAt"`
	LastResponseID      string     `json:"lastResponseID"`
	ShareStatus         string     `json:"shareStatus"`
	ShareID             string     `json:"shareID"`
	SharedAt            *time.Time `json:"sharedAt"`
	LastShareAccessedAt *time.Time `json:"lastShareAccessedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

// ConversationSearchItem is one row of the search endpoint.
type ConversationSearchItem struct {
	PublicID     string    `json:"publicID"`
	ProjectID    string    `json:"projectID"`
	ProjectName  string    `json:"projectName"`
	Title        string    `json:"title"`
	LabelsJSON   string    `json:"labelsJSON"`
	IsStarred    bool      `json:"isStarred"`
	MessageCount int       `json:"messageCount"`
	Status       string    `json:"status"`
	UpdatedAt    time.Time `json:"updatedAt"`
}

// BillingCost is the cost summary of a billed message.
type BillingCost struct {
	BillingMode         string  `json:"billingMode"`
	BilledCurrency      string  `json:"billedCurrency"`
	BilledNanousd       int64   `json:"billedNanousd"`
	BilledUSD           float64 `json:"billedUSD"`
	PricingSnapshotJSON string  `json:"pricingSnapshotJSON"`
}

// ModerationInfo describes a moderation outcome attached to a message.
type ModerationInfo struct {
	State      string   `json:"state"`
	Direction  string   `json:"direction,omitempty"`
	EventID    string   `json:"eventID"`
	Categories []string `json:"categories"`
}

// MarshalJSON writes the bare state object for messages that were never
// moderated.
func (m ModerationInfo) MarshalJSON() ([]byte, error) {
	if m.State == "not_required" && m.EventID == "" && len(m.Categories) == 0 && m.Direction == "" {
		return []byte(`{"state":"not_required"}`), nil
	}
	type plain ModerationInfo
	return json.Marshal(plain(m))
}

// MessageDTO is the message object returned to clients.
type MessageDTO struct {
	ID                uint                       `json:"id"`
	ConversationID    uint                       `json:"conversationID"`
	UserID            uint                       `json:"userID"`
	PublicID          string                     `json:"publicID"`
	ParentMessageID   *uint                      `json:"parentMessageID"`
	RunID             string                     `json:"runID"`
	Role              string                     `json:"role"`
	ContentType       string                     `json:"contentType"`
	Content           string                     `json:"content"`
	BranchReason      string                     `json:"branchReason"`
	SourceMessageID   *uint                      `json:"sourceMessageID"`
	TokenUsage        int64                      `json:"tokenUsage"`
	InputTokens       int64                      `json:"inputTokens"`
	OutputTokens      int64                      `json:"outputTokens"`
	CacheReadTokens   int64                      `json:"cacheReadTokens"`
	CacheWriteTokens  int64                      `json:"cacheWriteTokens"`
	ReasoningTokens   int64                      `json:"reasoningTokens"`
	ReasoningEffort   *string                    `json:"reasoningEffort"`
	LatencyMS         int64                      `json:"latencyMS"`
	Status            string                     `json:"status"`
	ErrorCode         string                     `json:"errorCode"`
	ErrorMessage      string                     `json:"errorMessage"`
	ErrorRequestID    string                     `json:"errorRequestID"`
	Attachments       string                     `json:"attachments"`
	PlatformModelName string                     `json:"platformModelName"`
	UpstreamModelName string                     `json:"upstreamModelName"`
	ModelVendor       string                     `json:"modelVendor"`
	ModelIcon         string                     `json:"modelIcon"`
	ParentPublicID    string                     `json:"parentPublicID"`
	SourcePublicID    string                     `json:"sourcePublicID"`
	MyFeedback        string                     `json:"myFeedback"`
	ThumbsUpCount     int64                      `json:"thumbsUpCount"`
	ThumbsDownCount   int64                      `json:"thumbsDownCount"`
	UIState           map[string]json.RawMessage `json:"uiState,omitempty"`
	BillingCost       *BillingCost               `json:"billingCost,omitempty"`
	KnowledgeSources  []KnowledgeSource          `json:"knowledgeSources,omitempty"`
	ProcessTrace      *ProcessTrace              `json:"processTrace,omitempty"`
	Moderation        *ModerationInfo            `json:"moderation,omitempty"`
	EditedAt          *time.Time                 `json:"editedAt"`
	CreatedAt         time.Time                  `json:"createdAt"`
	UpdatedAt         time.Time                  `json:"updatedAt"`
}

// SendResult is returned by the send endpoints.
type SendResult struct {
	UserMessage         *MessageDTO `json:"userMessage"`
	AssistantMessage    *MessageDTO `json:"assistantMessage"`
	MetadataRefreshHint string      `json:"metadataRefreshHint,omitempty"`
}

// RunDTO is the run object returned to clients.
type RunDTO struct {
	ID                  uint       `json:"id"`
	RunID               string     `json:"runID"`
	RequestID           string     `json:"requestID"`
	UserID              uint       `json:"userID"`
	ConversationID      uint       `json:"conversationID"`
	TaskType            string     `json:"taskType"`
	Endpoint            string     `json:"endpoint"`
	Provider            string     `json:"provider"`
	ProviderProtocol    string     `json:"providerProtocol"`
	UpstreamID          uint       `json:"upstreamID"`
	UpstreamModelID     uint       `json:"upstreamModelID"`
	RequestedModelName  string     `json:"requestedModelName"`
	PlatformModelName   string     `json:"platformModelName"`
	RoutedBindingCode   string     `json:"routedBindingCode"`
	ModelVendor         string     `json:"modelVendor"`
	ModelIcon           string     `json:"modelIcon"`
	UpstreamModelName   string     `json:"upstreamModelName"`
	InputTokens         int64      `json:"inputTokens"`
	OutputTokens        int64      `json:"outputTokens"`
	CacheReadTokens     int64      `json:"cacheReadTokens"`
	CacheWriteTokens    int64      `json:"cacheWriteTokens"`
	ReasoningTokens     int64      `json:"reasoningTokens"`
	ReasoningEffort     *string    `json:"reasoningEffort"`
	ToolCallsCount      int        `json:"toolCallsCount"`
	FirstTokenLatencyMS int64      `json:"firstTokenLatencyMS"`
	TotalLatencyMS      int64      `json:"totalLatencyMS"`
	Status              string     `json:"status"`
	ErrorCode           string     `json:"errorCode"`
	ErrorMessage        string     `json:"errorMessage"`
	StartedAt           time.Time  `json:"startedAt"`
	EndedAt             *time.Time `json:"endedAt"`
	CreatedAt           time.Time  `json:"createdAt"`
	UpdatedAt           time.Time  `json:"updatedAt"`
}

func toRunDTO(r Run) RunDTO {
	return RunDTO{
		ID: r.ID, RunID: r.RunID, RequestID: r.RequestID, UserID: r.UserID, ConversationID: r.ConversationID,
		TaskType: r.TaskType, Endpoint: r.Endpoint, Provider: r.Provider, ProviderProtocol: r.ProviderProtocol,
		UpstreamID: r.UpstreamID, UpstreamModelID: r.UpstreamModelID, RequestedModelName: r.RequestedModelName,
		PlatformModelName: r.PlatformModelName, RoutedBindingCode: r.RoutedBindingCode, ModelVendor: r.ModelVendor,
		ModelIcon: r.ModelIcon, UpstreamModelName: r.UpstreamModelName, InputTokens: r.InputTokens,
		OutputTokens: r.OutputTokens, CacheReadTokens: r.CacheReadTokens, CacheWriteTokens: r.CacheWriteTokens,
		ReasoningTokens: r.ReasoningTokens, ReasoningEffort: r.ReasoningEffort, ToolCallsCount: r.ToolCallsCount,
		FirstTokenLatencyMS: r.FirstTokenLatencyMS, TotalLatencyMS: r.TotalLatencyMS, Status: r.Status,
		ErrorCode: r.ErrorCode, ErrorMessage: r.ErrorMessage, StartedAt: r.StartedAt, EndedAt: r.EndedAt,
		CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
	}
}

// ProjectDTO is the project object returned to clients.
type ProjectDTO struct {
	PublicID                string    `json:"publicID"`
	Name                    string    `json:"name"`
	Description             string    `json:"description"`
	SystemPrompt            string    `json:"systemPrompt"`
	DefaultModel            string    `json:"defaultModel"`
	MCPDefaultMode          string    `json:"mcpDefaultMode"`
	DefaultMCPToolIDs       []uint    `json:"defaultMCPToolIDs"`
	DefaultSkillIDs         []uint    `json:"defaultSkillIDs"`
	DefaultKnowledgeBaseIDs []string  `json:"defaultKnowledgeBaseIDs"`
	Color                   string    `json:"color"`
	Icon                    string    `json:"icon"`
	SortOrder               int       `json:"sortOrder"`
	Status                  string    `json:"status"`
	CreatedAt               time.Time `json:"createdAt"`
	UpdatedAt               time.Time `json:"updatedAt"`
}

// ShareDTO is the share object returned to the owner.
type ShareDTO struct {
	ShareID        string     `json:"shareID"`
	Status         string     `json:"status"`
	TitleSnapshot  string     `json:"titleSnapshot"`
	ModelSnapshot  string     `json:"modelSnapshot"`
	MessageCount   int        `json:"messageCount"`
	CreatedAt      time.Time  `json:"createdAt"`
	UpdatedAt      time.Time  `json:"updatedAt"`
	RevokedAt      *time.Time `json:"revokedAt"`
	LastAccessedAt *time.Time `json:"lastAccessedAt"`
}

// DeleteResult is the result of deleting a conversation or a project.
type DeleteResult struct {
	Deleted          bool      `json:"deleted"`
	DeletedFileCount int       `json:"deletedFileCount,omitempty"`
	Quota            *QuotaDTO `json:"quota,omitempty"`
}

// QuotaDTO is the storage quota object.
type QuotaDTO struct {
	ID            uint      `json:"id"`
	UserID        uint      `json:"userID"`
	QuotaBytes    int64     `json:"quotaBytes"`
	UsedBytes     int64     `json:"usedBytes"`
	ReservedBytes int64     `json:"reservedBytes"`
	CreatedAt     time.Time `json:"createdAt"`
	UpdatedAt     time.Time `json:"updatedAt"`
}

// ----- shared helpers -----

func ptrStr(s string) *string { return &s }

// collapseSpaces replaces every whitespace run by one space and trims.
func collapseSpaces(s string) string { return strings.Join(strings.Fields(s), " ") }

// trimQuotes removes surrounding quote characters and spaces.
func trimQuotes(s string) string {
	return strings.Trim(strings.TrimSpace(s), "\"'`“”‘’\u300c\u300d\u300e\u300f ")
}

func labelsOrEmpty(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" || t == "null" {
		return "[]"
	}
	return raw
}

func parseStringList(raw string) []string {
	var out []string
	if strings.TrimSpace(raw) == "" {
		return []string{}
	}
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func parseUintList(raw string) []uint {
	var out []uint
	if err := json.Unmarshal([]byte(raw), &out); err != nil || out == nil {
		return []uint{}
	}
	return out
}

func mustJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "null"
	}
	return string(b)
}

func uintPtr(v uint) *uint { return &v }
