package knowledgebase

import (
	"context"
	"io"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/logx"
)

// The area talks to subsystems owned by other areas (the file store, the
// embedding queue, the settings store, auditing) only through the small
// interfaces below. The integrator supplies real implementations with
// MountWith. A nil member means "not available": the endpoints that depend on
// it answer with the documented unavailable error instead of failing at
// startup. The settings and audit members have working defaults.

// Settings supplies the two administrator settings this area reads.
type Settings interface {
	// Enabled is the feature switch (namespace knowledgebase, key enabled).
	Enabled(ctx context.Context) bool
	// MaxUploadBytes is the configured per-file upload cap; zero or negative
	// means "unset".
	MaxUploadBytes(ctx context.Context) int64
}

// AuditEntry is one audit record. Writing is best effort.
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

// Auditor records audit entries. Implementations must not fail the request.
type Auditor interface {
	Record(ctx context.Context, e AuditEntry)
}

// FileContent is an opened file.
type FileContent struct {
	FileName string
	MimeType string
	Size     int64
	Body     io.ReadCloser
}

// FileReader opens the stored bytes of a file on behalf of its real owner.
// It returns ErrFileNotFound when the file does not exist for that owner.
type FileReader interface {
	OpenFile(ctx context.Context, ownerUserID uint, fileID string) (*FileContent, error)
}

// FileCleaner deletes a file only when nothing else references it. It reports
// false (and no error) when the file was kept because it is still referenced
// or does not exist for that owner.
type FileCleaner interface {
	DeleteUnreferenced(ctx context.Context, ownerUserID uint, fileID string) (deleted bool, err error)
}

// UploadRequest asks the shared upload pipeline to store a file.
type UploadRequest struct {
	// OwnerUserID is the owning account; 0 is the platform pool.
	OwnerUserID uint
	// OperatorUserID is the administrator performing the upload. It is recorded
	// as operator only and never consumes quota.
	OperatorUserID uint
	Purpose        string
	FileName       string
	ContentType    string
	Size           int64
	Body           io.Reader
}

// FileUploader stores an uploaded file and returns its public file id. The
// errors it may return and that this area maps are ErrQuotaExceeded,
// ErrInvalidFile, ErrEmbeddingUnavailableForFile and ErrFileTooLarge.
type FileUploader interface {
	Upload(ctx context.Context, req UploadRequest) (fileID string, err error)
}

// SkippedFile is a file the embedding queue declined, with the reason.
type SkippedFile struct {
	FileID string `json:"fileID"`
	Reason string `json:"reason"`
}

// EmbedResult is the outcome of an embedding submission.
type EmbedResult struct {
	Submitted []string
	Skipped   []SkippedFile
}

// Embedder submits files to the shared vectorization queue. Errors it may
// return and that this area maps: ErrTooManyEmbeddingFiles,
// ErrEmbeddingNotConfigured and ErrEmbeddingUnavailable.
type Embedder interface {
	SubmitFiles(ctx context.Context, ownerUserID uint, fileIDs []string) (EmbedResult, error)
}

// FileFacts is what the vectorization assessment looks at.
type FileFacts struct {
	FileID       string
	OwnerUserID  uint
	FileName     string
	MimeType     string
	DetectedMIME string
	FileCategory string
	SizeBytes    int64
	EmbedStatus  string
	ChunkCount   int
	RAGOptOut    bool
}

// Vectorizer evaluates whether one file may be vectorized.
type Vectorizer interface {
	CanVectorize(ctx context.Context, f FileFacts) (ok bool, reason string)
}

// Ports groups the collaborators. Nil members get the defaults described on
// each field.
type Ports struct {
	// Settings defaults to reading system_settings (feature on, 20 MiB when
	// the table or the rows are absent).
	Settings Settings
	// Auditor defaults to a log line.
	Auditor Auditor
	// Reader nil: content endpoints answer 500 knowledge_base.internal.
	Reader FileReader
	// Cleaner nil: deletes with file removal answer 503.
	Cleaner FileCleaner
	// Uploader nil: platform uploads answer 400 knowledge_base.invalid.
	Uploader FileUploader
	// Embedder nil: submissions answer 503 embedding.service_not_configured.
	Embedder Embedder
	// Vectorizer nil: canVectorize is false for every file.
	Vectorizer Vectorizer
}

const defaultMaxUploadBytes int64 = 20 << 20

// ----- defaults -----

// tableSettings reads the two settings straight from system_settings. Any
// problem (no table, no row, unparsable value) falls back to the defaults.
type tableSettings struct{ db *gorm.DB }

func (s tableSettings) value(ctx context.Context, namespace, key string) (string, bool) {
	var row struct{ Value string }
	err := s.db.WithContext(ctx).Table("system_settings").Select("value").
		Where("namespace = ? AND "+quoteKey()+" = ?", namespace, key).Limit(1).Scan(&row).Error
	if err != nil || row.Value == "" {
		return "", false
	}
	return row.Value, true
}

// quoteKey quotes the column name "key", which is a reserved word on some
// databases; double quotes are accepted by both supported dialects.
func quoteKey() string { return `"key"` }

func (s tableSettings) Enabled(ctx context.Context) bool {
	v, ok := s.value(ctx, "knowledgebase", "enabled")
	if !ok {
		return true
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "false", "0", "off", "no":
		return false
	}
	return true
}

func (s tableSettings) MaxUploadBytes(ctx context.Context) int64 {
	v, ok := s.value(ctx, "storage", "max_upload_file_bytes")
	if !ok {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(v), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

type logAuditor struct{ log *logx.Logger }

func (a logAuditor) Record(_ context.Context, e AuditEntry) {
	if a.log == nil {
		return
	}
	a.log.Info("knowledge_base_audit",
		logx.F("action", e.Action), logx.F("resource", e.Resource),
		logx.F("resource_id", e.ResourceID), logx.F("user_id", e.ActorUserID),
		logx.F("request_id", e.RequestID))
}

func (p Ports) withDefaults(db *gorm.DB, log *logx.Logger) Ports {
	if p.Settings == nil {
		p.Settings = tableSettings{db: db}
	}
	if p.Auditor == nil {
		p.Auditor = logAuditor{log: log}
	}
	return p
}
