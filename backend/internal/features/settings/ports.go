package settings

import (
	"context"
	"time"
)

// The interfaces below are the seams to other areas. Every port is optional;
// the endpoints that need a missing port answer with the documented
// "unavailable" outcome instead of failing at startup.

// AuditEntry is one audit record request.
type AuditEntry struct {
	ActorUserID uint
	RequestID   string
	ClientIP    string
	UserAgent   string
	Action      string
	Resource    string
	ResourceID  string
	Detail      map[string]any
}

// AuditWriter records audit entries. It never fails the caller.
type AuditWriter interface {
	Record(ctx context.Context, e AuditEntry)
}

// SuperAdminChecker reports whether at least one active super administrator is
// bound to a third-party identity, so turning off username and email login
// cannot lock everybody out.
type SuperAdminChecker interface {
	HasBoundSuperAdmin(ctx context.Context) (bool, error)
}

// VectorStoreChecker reports whether the vector store can serve embeddings.
type VectorStoreChecker interface {
	VectorStoreAvailable(ctx context.Context) (bool, error)
}

// EmbeddingStatus summarises the state of the embedding index.
type EmbeddingStatus struct {
	ModelSignature   string `json:"modelSignature"`
	ReadyCount       int64  `json:"readyCount"`
	StaleCount       int64  `json:"staleCount"`
	PendingCount     int64  `json:"pendingCount"`
	StalledCount     int64  `json:"stalledCount"`
	ActiveCount      int64  `json:"activeCount"`
	FailedCount      int64  `json:"failedCount"`
	EmptyCount       int64  `json:"emptyCount"`
	UnsupportedCount int64  `json:"unsupportedCount"`
	NeedsReindex     bool   `json:"needsReindex"`
	ReindexRunning   bool   `json:"reindexRunning"`
}

// EmbeddingTask is one file row of the index administration listing.
type EmbeddingTask struct {
	FileID             string    `json:"fileID"`
	FileName           string    `json:"fileName"`
	MimeType           string    `json:"mimeType"`
	SizeBytes          int64     `json:"sizeBytes"`
	UserID             uint      `json:"userID"`
	UserLabel          string    `json:"userLabel"`
	EmbedStatus        string    `json:"embedStatus"`
	EmbedError         string    `json:"embedError"`
	Stalled            bool      `json:"stalled"`
	Retryable          bool      `json:"retryable"`
	RetryBlockedReason string    `json:"retryBlockedReason"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

// TaskQuery selects a page of index tasks.
type TaskQuery struct {
	Bucket   string
	Query    string
	Page     int
	PageSize int
}

// ReindexOutcome is the result of a rebuild request. Submitted counts embedding
// submissions and Reprocessed counts files sent back through extraction. When a
// rebuild is already running the port returns ErrReindexRunning together with
// the outcome of the reprocessing it still performed.
type ReindexOutcome struct {
	Submitted   int
	Reprocessed int
}

// SkippedFile names a file a retry did not submit and why.
type SkippedFile struct {
	FileID string `json:"fileID"`
	Reason string `json:"reason"`
}

// RetryOutcome is the result of a retry request.
type RetryOutcome struct {
	Submitted []string
	Skipped   []SkippedFile
}

// EmbeddingIndex is implemented by the embedding service. Ports report
// ErrPortUnavailable, ErrNothingConfigured and ErrReindexRunning (wrapped is
// fine) for the corresponding conditions.
type EmbeddingIndex interface {
	Status(ctx context.Context) (EmbeddingStatus, error)
	Reindex(ctx context.Context, includeEmpty bool) (ReindexOutcome, error)
	Tasks(ctx context.Context, q TaskQuery) ([]EmbeddingTask, int64, error)
	Retry(ctx context.Context, fileIDs []string) (RetryOutcome, error)
	// Reconcile marks files built under another embedding space as stale. It is
	// idempotent.
	Reconcile(ctx context.Context) error
	// Probe embeds a short text to check that the service answers.
	Probe(ctx context.Context, text string) error
}

// RuntimeStatus is the state of a managed helper service.
type RuntimeStatus struct {
	Source        string `json:"source"`
	BaseURL       string `json:"baseURL"`
	ContainerName string `json:"containerName"`
	Image         string `json:"image"`
	Network       string `json:"network"`
	Status        string `json:"status"`
	Reachable     bool   `json:"reachable"`
	Message       string `json:"message"`
}

// ManagedRuntime controls one managed helper (a container).
type ManagedRuntime interface {
	Status(ctx context.Context) (RuntimeStatus, error)
	Start(ctx context.Context) (RuntimeStatus, error)
	Stop(ctx context.Context) (RuntimeStatus, error)
	Restart(ctx context.Context) (RuntimeStatus, error)
}

// Names of the managed helpers.
const (
	RuntimeTika      = "tika"
	RuntimeDocling   = "docling"
	RuntimeTesseract = "tesseract"
	RuntimeRapidOCR  = "rapidocr"
	RuntimeMinerU    = "mineru"
)

// NativeToolCatalog supplies the native-tool catalog, including administrator
// overrides. When absent the built-in catalog is used.
type NativeToolCatalog interface {
	NativeTools(ctx context.Context) ([]NativeTool, error)
}

// UserSettingsHook is called after a user's settings were written, with the
// keys that changed, so per-user caches elsewhere see the committed values.
type UserSettingsHook func(ctx context.Context, userID uint, keys []string)

// Ports bundles the optional collaborators. Nil members stay unset.
type Ports struct {
	Audit        AuditWriter
	SuperAdmins  SuperAdminChecker
	VectorStore  VectorStoreChecker
	Embedding    EmbeddingIndex
	NativeTools  NativeToolCatalog
	UserSettings UserSettingsHook
	// Runtimes maps a helper name (see the Runtime* constants) to its control.
	Runtimes map[string]ManagedRuntime
}
