package conversation

import (
	"context"
	"errors"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// The conversation area depends on several services owned by other areas
// (model routing, billing, moderation, the model catalog, MCP, skills, knowledge
// bases, settings, auditing). It talks to them only through the small
// interfaces below. The integrator supplies real implementations through
// MountWith; Mount uses the permissive defaults at the bottom of this file so
// the area works on its own.

// Usage carries token counters of one generation.
type Usage struct {
	Input      int64 `json:"inputTokens"`
	Output     int64 `json:"outputTokens"`
	CacheRead  int64 `json:"cacheReadTokens"`
	CacheWrite int64 `json:"cacheWriteTokens"`
	Reasoning  int64 `json:"reasoningTokens"`
}

// Total is the total token count used for the message's tokenUsage field.
func (u Usage) Total() int64 { return u.Input + u.Output }

// RouteInfo describes the model route a generation used.
type RouteInfo struct {
	Provider           string
	ProviderProtocol   string
	Endpoint           string
	UpstreamID         uint
	UpstreamModelID    uint
	UpstreamName       string
	RequestedModelName string
	PlatformModelName  string
	RoutedBindingCode  string
	ModelVendor        string
	ModelIcon          string
	UpstreamModelName  string
}

// PromptFile is an attachment injected into the model context.
type PromptFile struct {
	FileID   string
	FileName string
	MimeType string
	Kind     string
	Size     int64
	// Text is the extracted text when available; Data holds raw bytes for
	// attachments that were not stored (temporary chat).
	Text string
	Data []byte
	// Retrieved marks an attachment whose content is searched per question: its
	// full text is not part of the turn (the dynamic context carries passages).
	Retrieved bool
}

// PromptMessage is one message of the model context.
type PromptMessage struct {
	Role      string
	Content   string
	Reasoning string
	Files     []PromptFile
}

// UIStateHint is an advisory interactive-block state offered to the model.
type UIStateHint struct {
	MessagePublicID string
	MessageIndex    int
	BlockID         string
	Title           string
	State           map[string]any
}

// MediaRequest carries the extra inputs of image and video tasks.
type MediaRequest struct {
	Kind              string
	Prompt            string
	InputFileIDs      []string
	MaskFileID        string
	SourceVideoFileID string
}

// Task names passed to the generator.
const (
	taskChat            = "chat"
	taskTitle           = "title"
	taskLabels          = "title_labels"
	taskImageGeneration = "image_generation"
	taskImageEdit       = "image_edit"
	taskVideoGeneration = "video_generation"
	taskVideoExtension  = "video_extension"
)

// GenerateRequest is what the conversation area asks the model layer to do.
type GenerateRequest struct {
	Task                 string
	UserID               uint
	ConversationPublicID string
	RunID                string
	RequestID            string
	Model                string
	System               string
	Messages             []PromptMessage
	Options              map[string]any
	Controls             map[string]any
	ReasoningEffort      string
	ToolIDs              []uint
	SkillIDs             []uint
	KnowledgeBaseIDs     []string
	HTMLVisualPrompt     bool
	UIComponentIDs       []uint
	UIBlockVersion       int
	UIStates             []UIStateHint
	Media                *MediaRequest
	Temporary            bool
	MaxLLMCalls          int
	PassbackReasoning    bool
	// DynamicContext is the per-question block (retrieved passages, full-text
	// fallbacks, notices) that belongs to the last user turn. It changes with
	// every question and is excluded from prompt-prefix fingerprints.
	DynamicContext string
}

// GenerateEvent is a progress event emitted while generating.
type GenerateEvent struct {
	// Type is one of delta, upstream_think_delta, usage, process_update,
	// rag_search, compact_done, file_proc.
	Type string
	// Delta is the text increment for delta and upstream_think_delta.
	Delta string
	// Fields are merged into the event object for the other types.
	Fields map[string]any
}

// GeneratedFile is a media artifact produced by a generation.
type GeneratedFile struct {
	FileName        string
	MimeType        string
	Kind            string
	Data            []byte
	DurationSeconds float64
}

// KnowledgeSource is a retrieved passage shown with an answer.
type KnowledgeSource struct {
	FileName   string  `json:"fileName"`
	FileID     string  `json:"fileId"`
	ChunkIndex int     `json:"chunkIndex"`
	Score      float64 `json:"score"`
	Preview    string  `json:"preview"`
	Modality   string  `json:"modality,omitempty"`
}

// GenerateResult is the outcome of one generation.
type GenerateResult struct {
	Content             string
	Reasoning           string
	Usage               Usage
	Route               RouteInfo
	FirstTokenLatencyMS int64
	TotalLatencyMS      int64
	KnowledgeSources    []KnowledgeSource
	Trace               *TraceData
	Files               []GeneratedFile
	ToolCalls           int
	// ResponseID is the upstream's id of the last response.
	ResponseID string
	// Billable is true when the upstream consumed billable usage even if the
	// call failed afterwards.
	Billable bool
}

// Generator runs a model call. It streams progress through emit and returns the
// final result. On failure it may return a partial result together with the
// error; the partial output is persisted.
type Generator interface {
	Generate(ctx context.Context, req GenerateRequest, emit func(GenerateEvent)) (GenerateResult, error)
}

// ReserveRequest asks billing to authorise a generation.
type ReserveRequest struct {
	UserID         uint
	ConversationID uint
	Model          string
	Task           string
	RunID          string
}

// Reservation is an opaque billing hold.
type Reservation struct {
	ID   string
	Mode string
}

// SettleRequest finalises a reservation.
type SettleRequest struct {
	UserID uint
	RunID  string
	Task   string
	Usage  Usage
	Route  RouteInfo
	Failed bool
	// LatencyMS is the measured duration of the run.
	LatencyMS int64
}

// Settlement is the billing outcome stored on the assistant message.
type Settlement struct {
	Mode                string
	Currency            string
	Nanousd             int64
	PricingSnapshotJSON string
}

// BasicServiceCharge bills an auxiliary call such as title generation.
type BasicServiceCharge struct {
	UserID         uint
	ConversationID uint
	Service        string
	Usage          Usage
	Route          RouteInfo
	RunID          string
	LatencyMS      int64
}
