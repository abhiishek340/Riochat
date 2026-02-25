package llm

import (
	"encoding/json"
)

// Route is the connection configuration for one upstream model.
type Route struct {
	Protocol string
	BaseURL  string
	APIKey   string
	// HeadersJSON is a JSON object of custom request headers; values may use
	// ${RIOCHAT_CONVERSATION_ID}, ${RIOCHAT_SESSION_ID}, ${RIOCHAT_REQUEST_ID}
	// and ${RIOCHAT_UPSTREAM_REQUEST_ID}.
	HeadersJSON string
	// Timeouts in milliseconds; non-positive values use the defaults (connect
	// 10 s, read 120 s, stream idle 60 s).
	ConnectTimeoutMS    int
	ReadTimeoutMS       int
	StreamIdleTimeoutMS int
	// EndpointKind is carried for callers (for example "official"); the
	// transport does not branch on it.
	EndpointKind string
	// Model is the upstream model name.
	Model string
	// OpenRouter attribution, used only for openrouter.ai hosts.
	OpenRouterReferer string
	OpenRouterTitle   string
	// Untrusted marks a user-supplied endpoint: public-only policy, no
	// redirects.
	Untrusted bool
}

// Role is a message author.
type Role string

// Roles. Unknown roles are treated as user.
const (
	RoleSystem    Role = "system"
	RoleDeveloper Role = "developer"
	RoleUser      Role = "user"
	RoleAssistant Role = "assistant"
	RoleTool      Role = "tool"
	RoleFunction  Role = "function"
)

// PartKind classifies a content part.
type PartKind string

// Part kinds.
const (
	PartText     PartKind = "text"
	PartImage    PartKind = "image"
	PartVideo    PartKind = "video"
	PartAudio    PartKind = "audio"
	PartFile     PartKind = "file"     // extracted text of a file
	PartDocument PartKind = "document" // native PDF
)

// Part is one piece of multimodal content.
type Part struct {
	Kind     PartKind
	Text     string
	MIME     string
	Data     []byte
	FileName string
	Pages    int
	Duration float64 // seconds, for budgeting
	// Dynamic marks per-turn context that must never be cached.
	Dynamic bool
	// CacheHint asks for a cache breakpoint after this part.
	CacheHint bool
}

// ToolCall is a function call requested by the model.
type ToolCall struct {
	ID        string
	Name      string
	Arguments string // JSON text
	// ThoughtSignature is preserved for providers that require echoing it.
	ThoughtSignature string
}

// ToolResult is the outcome of a tool call.
type ToolResult struct {
	CallID  string
	Name    string
	Output  string // JSON or text; empty means {}
	IsError bool
	Status  string // error status text, for failures
	Error   string // error message, for failures
}

// Message is one conversation turn.
type Message struct {
	Role    Role
	Content string
	Parts   []Part
	// Reasoning is assistant thinking echoed back to thinking-mode providers.
	Reasoning   string
	ToolCalls   []ToolCall
	ToolResults []ToolResult
	// CacheHint asks for a cache breakpoint after the message.
	CacheHint bool
}

// ToolDef declares a function tool.
type ToolDef struct {
	Name        string
	Description string
	Schema      map[string]any // JSON Schema for the arguments
}

// Request is a provider-neutral generation request.
type Request struct {
	RequestID      string
	ConversationID string
	SessionKey     string
	// PromptCacheKey is server controlled; users cannot override it.
	PromptCacheKey string
	Messages       []Message
	Instructions   string
	Tools          []ToolDef
	DisableTools   bool
	// Options is the free-form sampling / reasoning / extension map.
	Options map[string]any
	// PreviousResponseID continues a stateful conversation (openai_responses).
	PreviousResponseID string
	// Background asks for background execution (official Responses only).
	Background bool
	// Ephemeral requests stateless inference.
	Ephemeral bool
	// Mask is the image-edit mask; VideoSource is the video-extension source.
	Mask        *Part
	VideoSource *Part
}

// Usage is normalized token accounting.
type Usage struct {
	// InputTokens counts non-cached input only.
	InputTokens        int64
	OutputTokens       int64
	CacheReadTokens    int64
	CacheWriteTokens   int64
	CacheWrite5mTokens int64
	CacheWrite1hTokens int64
	ReasoningTokens    int64
	Speed              string
	ServiceTier        string
	// Raw is the provider usage JSON (several documents merge into an array).
	Raw json.RawMessage
}

// InputObserved reports whether any input accounting was seen. A fully cached
// prompt legitimately has zero non-cached input.
func (u Usage) InputObserved() bool {
	return u.InputTokens > 0 || u.CacheReadTokens > 0 || u.CacheWriteTokens > 0
}

// OutputObserved reports whether any output accounting was seen.
func (u Usage) OutputObserved() bool { return u.OutputTokens > 0 || u.ReasoningTokens > 0 }

// IsZero reports whether nothing was recorded.
func (u Usage) IsZero() bool {
	return !u.InputObserved() && !u.OutputObserved() && len(u.Raw) == 0
}

// Citation is a source URL.
type Citation struct {
	URL   string
	Title string
}

// ServerToolCall records a provider-hosted tool invocation.
type ServerToolCall struct {
	ID     string
	Type   string // for example web_search, code_interpreter
	Status string
	// Queries and Sources describe searches.
	Queries []string
	Sources []Citation
	// Detail is the (sanitized) provider item.
	Detail json.RawMessage
}

// Image is a generated or returned image.
type Image struct {
	Index         int
	Data          []byte
	URL           string
	MIME          string
	RevisedPrompt string
	Partial       bool
}

// Video is a generated video.
type Video struct {
	URL         string
	Data        []byte
	MIME        string
	FileName    string
	DurationSec int
}

// Result is the provider-neutral outcome of a generation.
type Result struct {
	ID                 string
	Text               string
	Reasoning          string
	ReasoningSignature string
	ToolCalls          []ToolCall
	ServerToolCalls    []ServerToolCall
	ServerToolUsage    map[string]int
	Citations          []Citation
	Images             []Image
	Videos             []Video
	Usage              Usage
	ServiceTier        string
	FinishReason       string
	RevisedPrompt      string
	Debug              *Debug
}

// EventType classifies a streaming event.
type EventType string

// Streaming event types.
const (
	EventCreated    EventType = "created"
	EventText       EventType = "text"
	EventReasoning  EventType = "reasoning"
	EventToolCall   EventType = "tool_call"
	EventServerTool EventType = "server_tool"
	EventImage      EventType = "image"
	EventCitation   EventType = "citation"
	EventUsage      EventType = "usage"
)

// Event is one streaming increment. Only the fields relevant to Type are set.
type Event struct {
	Type        EventType
	Text        string
	Index       int
	ToolCall    *ToolCall // a fragment: only changed fields are set
	ServerTool  *ServerToolCall
	Image       *Image
	Citation    *Citation
	Usage       *Usage
	ResponseID  string
	ServiceTier string
}

// Sink receives streaming events; returning an error aborts the stream.
type Sink func(Event) error

// Model is one entry of a provider's model list.
type Model struct {
	ID      string
	OwnedBy string
}
