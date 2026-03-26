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

// Billing authorises and settles generation usage.
type Billing interface {
	Reserve(ctx context.Context, req ReserveRequest) (Reservation, error)
	Settle(ctx context.Context, res Reservation, req SettleRequest) (Settlement, error)
	Release(ctx context.Context, res Reservation)
	ChargeBasicService(ctx context.Context, c BasicServiceCharge) error
}

// Moderation directions.
const (
	dirInput  = "input"
	dirOutput = "output"
)

// ModerationImage is an image carried inside the request itself (temporary
// chat); it has no stored file.
type ModerationImage struct {
	Name  string
	Bytes []byte
}

// ModerationRequest is one content check.
type ModerationRequest struct {
	UserID         uint
	ConversationID uint
	Direction      string
	Text           string
	Ephemeral      bool
	// Images are checked together with the text of an ephemeral input check.
	Images []ModerationImage
}

// ModerationVerdict is the outcome of a content check.
type ModerationVerdict struct {
	Blocked      bool
	EventID      string
	Direction    string
	Categories   []string
	BilledReason string
}

// Moderation checks content.
type Moderation interface {
	Check(ctx context.Context, req ModerationRequest) (ModerationVerdict, error)
}

// ModerationBegin identifies a persisted turn whose moderation is tracked
// across the whole run.
type ModerationBegin struct {
	UserID          uint
	ConversationID  uint
	RunID           string
	MessageID       uint
	MessagePublicID string
	// PaidReservation marks a turn whose upstream usage is billed.
	PaidReservation bool
	// CancelGeneration stops the upstream generation; the engine calls it at
	// most once, as soon as an input check hits.
	CancelGeneration func()
}

// ModerationFile names a stored file attached to the user turn.
type ModerationFile struct {
	// ObjectID is the internal file row id.
	ObjectID uint
	// SHA256 is the stored content hash used to check identical files once.
	SHA256 string
}

// ModerationOutput is what the model produced.
type ModerationOutput struct {
	Text      string
	Reasoning string
	// Images are the generated image files, with their bytes.
	Images []ModerationImage
}

// ModerationSession tracks the moderation of one persisted run. Input checks
// are queued while the model generates; Finish waits for every check and
// settles the turn. A session is closed with Close when abandoned.
type ModerationSession interface {
	CheckInputText(text string)
	CheckInputImages(files []ModerationFile)
	// ReportOutputImageFailure records a reply image whose bytes cannot be
	// loaded; it never counts as a pass.
	ReportOutputImageFailure(ctx context.Context)
	// Finish runs the output checks and returns the verdict. The engine has
	// already tried to persist a block; the caller still records billing.
	Finish(ctx context.Context, out ModerationOutput) ModerationVerdict
	Close()
}

// SessionModeration is implemented by a Moderation that can track a run: it
// checks stored images, withdraws a blocked turn by itself and recovers runs
// left half-moderated by a crash. Moderation implementations without it are
// used through Check, which only sees text.
type SessionModeration interface {
	Moderation
	Begin(ctx context.Context, b ModerationBegin) ModerationSession
}

// ObjectBytes keeps the bytes of chat files. The integrator hands in the
// shared object store (local disk or S3-compatible); without one the area
// writes below the local storage root. Keys never carry a leading slash.
type ObjectBytes interface {
	Put(ctx context.Context, key string, data []byte, contentType string) error
	// Get returns ErrObjectNotFound for a missing key.
	Get(ctx context.Context, key string) ([]byte, error)
	// Delete succeeds for a missing key.
	Delete(ctx context.Context, key string) error
}

// ErrObjectNotFound is what ObjectBytes.Get answers for a missing key.
var ErrObjectNotFound = errors.New("conversation: object not found")

// Catalog answers questions about the model catalog.
type Catalog interface {
	// ChatModelAvailable reports whether the model is active, supports chat and
	// may be used by the user.
	ChatModelAvailable(ctx context.Context, userID uint, model string) bool
}

// RuntimeSettings is the snapshot of administrator-controlled settings the
// area reads.
type RuntimeSettings struct {
	DefaultNewConversationModel string
	TaskModel                   string
	TitlePrompt                 string
	LabelsPrompt                string
	UIComponentsEnabled         bool
	KnowledgeBaseEnabled        bool
	MaxUploadBytes              int64
	// UserStorageQuotaBytes is the default per-user storage allowance (0 =
	// unlimited).
	UserStorageQuotaBytes       int64
	MaxMessageFiles             int
	MaxSelectedTools            int
	MaxLLMCalls                 int
	ContextMaxTurns             int
	ContextCompactionEnabled    bool
	ContextWindowFallbackTokens int
	ContextCompactTriggerPct    int
	ContextPreserveRecentTurns  int
	ImageMaxBytes               int64
	DocMaxBytes                 int64
	AllowedMIMETypes            []string
	FullContextMaxBytes         int64
	FullContextMaxTokens        int
	FullContextPDFMaxPages      int
	RAGAvailable                bool
	RAGAvailabilityReason       string
	// ContextTokenBudgetEnabled switches on the context-token budget rule of
	// the retrieval plan (large attachments are searched instead of injected).
	ContextTokenBudgetEnabled bool
	// ContextArtifactRetentionDays is how long evidence rows live (0 = never
	// expire).
	ContextArtifactRetentionDays int
}

// Settings supplies runtime and per-user settings.
type Settings interface {
	Runtime(ctx context.Context) RuntimeSettings
	// UserSetting returns a per-user setting; ok is false when it is not set.
	UserSetting(ctx context.Context, userID uint, key string) (value string, ok bool, err error)
}

// AuditEntry is one audit record.
type AuditEntry struct {
	UserID     uint
	RequestID  string
	ClientIP   string
	UserAgent  string
	Action     string
	Resource   string
	ResourceID string
	Detail     map[string]any
}

// Auditor records audit entries. Failures never fail a request.
type Auditor interface {
	Record(ctx context.Context, e AuditEntry) error
}

// Capabilities validates references to other areas' resources.
type Capabilities interface {
	// ValidateMCPTools checks that the tools exist, are active, belong to active
	// servers and include at most one image-attachment processor.
	ValidateMCPTools(ctx context.Context, userID uint, toolIDs []uint) error
	// ValidateSkills checks that every skill is visible to the user.
	ValidateSkills(ctx context.Context, userID uint, skillIDs []uint) error
	// ValidateKnowledgeBases checks visibility and that each has a ready file.
	ValidateKnowledgeBases(ctx context.Context, userID uint, ids []string) error
	// FileInUse reports whether another area (avatar, knowledge base) uses the file.
	FileInUse(ctx context.Context, userID uint, fileID string) (bool, error)
}

// Ports groups the collaborators. Nil members get the defaults.
type Ports struct {
	Generator    Generator
	Billing      Billing
	Moderation   Moderation
	Catalog      Catalog
	Settings     Settings
	Auditor      Auditor
	Capabilities Capabilities
	// OnFiles, when set, receives the chat-file store once the area is mounted
	// so the integrator can adapt it for other areas.
	OnFiles func(*Files)
	// Objects stores file bytes; nil keeps them on the local storage root.
	Objects ObjectBytes
	// OnModeration, when set, receives the bridge through which the moderation
	// engine reaches runs, messages, files and the live streams.
	OnModeration func(*ModerationBridge)
	// Indexer builds and tracks the retrieval index of files. Without it files
	// are never offered for vectorization.
	Indexer FileIndexer
	// Knowledge plans and runs retrieval for a chat turn. Without it
	// attachments are always injected in full and knowledge bases are ignored.
	Knowledge Knowledge
}

func (p Ports) withDefaults(log *logx.Logger) Ports {
	if p.Generator == nil {
		p.Generator = unconfiguredGenerator{}
	}
	if p.Billing == nil {
		p.Billing = noBilling{}
	}
	if p.Moderation == nil {
		p.Moderation = noModeration{}
	}
	if p.Catalog == nil {
		p.Catalog = openCatalog{}
	}
	if p.Settings == nil {
		p.Settings = defaultSettings{}
	}
	if p.Auditor == nil {
		p.Auditor = logAuditor{log: log}
	}
	if p.Capabilities == nil {
		p.Capabilities = permissiveCapabilities{}
	}
	return p
}

// DefaultRuntimeSettings returns the documented defaults.
func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		TaskModel:                   "follow",
		UIComponentsEnabled:         true,
		KnowledgeBaseEnabled:        true,
		MaxUploadBytes:              20 * 1024 * 1024,
		UserStorageQuotaBytes:       100 * 1024 * 1024,
		MaxMessageFiles:             10,
		MaxSelectedTools:            32,
		MaxLLMCalls:                 5,
		ContextMaxTurns:             40,
		ContextCompactionEnabled:    true,
		ContextWindowFallbackTokens: 128000,
		ContextCompactTriggerPct:    80,
		ContextPreserveRecentTurns:  6,
		ImageMaxBytes:               10 * 1024 * 1024,
		DocMaxBytes:                 20 * 1024 * 1024,
		FullContextMaxBytes:         2 * 1024 * 1024,
		FullContextMaxTokens:        100000,
		FullContextPDFMaxPages:      100,
		RAGAvailable:                false,
		RAGAvailabilityReason:       "embedding_not_configured",
	}
}

// ----- defaults -----

type unconfiguredGenerator struct{}

func (unconfiguredGenerator) Generate(context.Context, GenerateRequest, func(GenerateEvent)) (GenerateResult, error) {
	return GenerateResult{}, ErrModelRouteMissing
}

type noBilling struct{}

func (noBilling) Reserve(context.Context, ReserveRequest) (Reservation, error) {
	return Reservation{Mode: "self"}, nil
}

func (noBilling) Settle(context.Context, Reservation, SettleRequest) (Settlement, error) {
	return Settlement{Mode: "self", Currency: "USD"}, nil
}
func (noBilling) Release(context.Context, Reservation)                         {}
func (noBilling) ChargeBasicService(context.Context, BasicServiceCharge) error { return nil }

type noModeration struct{}

func (noModeration) Check(context.Context, ModerationRequest) (ModerationVerdict, error) {
	return ModerationVerdict{}, nil
}

type openCatalog struct{}

func (openCatalog) ChatModelAvailable(_ context.Context, _ uint, model string) bool {
	return strings.TrimSpace(model) != ""
}

type defaultSettings struct{}

func (defaultSettings) Runtime(context.Context) RuntimeSettings { return DefaultRuntimeSettings() }
func (defaultSettings) UserSetting(context.Context, uint, string) (string, bool, error) {
	return "", false, nil
}

type logAuditor struct{ log *logx.Logger }

func (a logAuditor) Record(_ context.Context, e AuditEntry) error {
	if a.log != nil {
		a.log.Info("conversation_audit", logx.F("action", e.Action), logx.F("resource", e.Resource), logx.F("resource_id", e.ResourceID), logx.F("user_id", e.UserID))
	}
	return nil
}

type permissiveCapabilities struct{}

func (permissiveCapabilities) ValidateMCPTools(context.Context, uint, []uint) error { return nil }
func (permissiveCapabilities) ValidateSkills(context.Context, uint, []uint) error   { return nil }
func (permissiveCapabilities) ValidateKnowledgeBases(context.Context, uint, []string) error {
	return nil
}
func (permissiveCapabilities) FileInUse(context.Context, uint, string) (bool, error) {
	return false, nil
}

// timeOrZero is a tiny helper shared by DTO builders.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// ----- retrieval index ports -----

// FileFacts are the stored facts of a file the indexer decides on.
type FileFacts struct {
	ObjectID         uint
	FileID           string
	UserID           uint
	FileName         string
	MimeType         string
	DetectedMIME     string
	FileCategory     string
	StoragePath      string
	Status           string
	ProcessingStatus string
	ProcessingReady  bool
	ExtractStatus    string
	EmbedStatus      string
	EmbedSignature   string
	ChunkCount       int
	RAGOptOut        bool
	UpdatedAt        time.Time
}

func factsOf(f FileObject) FileFacts {
	return FileFacts{
		ObjectID: f.ID, FileID: f.FileID, UserID: f.UserID, FileName: f.FileName, MimeType: f.MimeType, DetectedMIME: f.DetectedMIME,
		FileCategory: f.FileCategory, StoragePath: f.StoragePath, Status: f.Status, ProcessingStatus: f.ProcessingStatus,
		ProcessingReady: f.ProcessingReady, ExtractStatus: f.ExtractStatus, EmbedStatus: f.EmbedStatus,
		EmbedSignature: f.EmbedSignature, ChunkCount: f.ChunkCount, RAGOptOut: f.RAGOptOut, UpdatedAt: f.UpdatedAt,
	}
}

// FileIndexer is the retrieval index of files (owned by another area).
type FileIndexer interface {
	// Submit queues the caller's files for indexing. It returns typed errors
	// (embedding.service_not_configured, ...) that are passed through.
	Submit(ctx context.Context, userID uint, fileIDs []string) (*EmbeddingSubmission, error)
	// Vectorization says whether a submission of the file would be accepted
	// and, when not, why.
	Vectorization(ctx context.Context, f FileFacts) (bool, string)
	// TextReady is called when text processing of a stored file finished; the
	// indexer starts the background index when auto-indexing applies.
	TextReady(ctx context.Context, fileObjID uint)
	// Forget deletes the index data of a file inside the caller's transaction.
	Forget(ctx context.Context, tx *gorm.DB, fileObjID uint) error
	// Clone copies the index of one file to another inside the transaction.
	Clone(ctx context.Context, tx *gorm.DB, fromID, toID uint) error
}

// KnowledgeFile is one file offered to retrieval for a turn.
type KnowledgeFile struct {
	ObjectID        uint
	FileID          string
	Name            string
	Category        string
	SHA256          string
	RAGOptOut       bool
	EmbedStatus     string
	ExtractStatus   string
	ChunkCount      int
	UpdatedAt       time.Time
	ProcessingReady bool
	Text            string
	TextTokens      int
	FullContextOK   bool
	CurrentTurn     bool
	Generated       bool
	Attached        bool
}

// KnowledgeRequest describes a turn for the retrieval plan.
type KnowledgeRequest struct {
	UserID               uint
	Message              string
	PriorUserMessages    []string
	FileMode             string
	ContextBudgetFeature bool
	ContextBudgetTokens  int
	Files                []KnowledgeFile
	KnowledgeBaseIDs     []string
	Ephemeral            bool
	// Searching reports the "searching" progress event.
	Searching func()
}

// KnowledgeAttach names an image whose original must join the turn.
type KnowledgeAttach struct {
	FileID string
	Name   string
}

// KnowledgeTrace is the retrieval section of the message process trace.
type KnowledgeTrace struct {
	Title       string
	Summary     string
	Detail      string
	Status      string
	PayloadJSON string
}

// KnowledgeResult is what retrieval contributes to the turn.
type KnowledgeResult struct {
	Ran       bool
	Context   string
	Inline    map[string]bool
	Retrieved map[string]bool
	Attach    []KnowledgeAttach
	Sources   []KnowledgeSource
	Artifacts []ContextArtifactInput
	Trace     *KnowledgeTrace
	Status    string
	Reason    string
}

// Knowledge plans and runs retrieval for a turn. It returns the conversation
// area's own typed errors (knowledge_base.unavailable, ...), which abort the
// send.
type Knowledge interface {
	Prepare(ctx context.Context, req KnowledgeRequest) (KnowledgeResult, error)
}

// PricingCatalog is implemented by catalogs that can tell a model hidden for
// want of a price from one that is not offered at all.
type PricingCatalog interface {
	ModelUnpriced(ctx context.Context, userID uint, model string) bool
}

// RouteCatalog is implemented by catalogs that can tell a model that does not
// exist (or is switched off) from one the caller may not use.
type RouteCatalog interface {
	ModelRoutable(ctx context.Context, model string) bool
}
