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
