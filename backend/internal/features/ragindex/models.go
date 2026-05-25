package ragindex

import (
	"time"

	"gorm.io/gorm"
)

// Index states stored in file_objects.embed_status. Any other stored value
// (an empty string, "none", or a legacy marker) means "never indexed".
const (
	StateNone       = "none"
	StateQueued     = "queued"
	StateProcessing = "processing"
	StateReady      = "ready"
	StateStale      = "stale"
	StateEmpty      = "empty"
	StateFailed     = "failed"
)

// Text-processing states (file_objects.processing_status) this area touches.
const (
	procUploaded   = "uploaded"
	procQueued     = "queued"
	procExtracting = "extracting"
	procEmbedding  = "embedding"
	procReady      = "ready"
	procFailed     = "failed"
)

// Extraction states (file_objects.extract_status) this area reads or writes.
const (
	extractNone  = "none"
	extractReady = "ready"
	extractEmpty = "empty"
)

const fileActive = "active"

// File is the part of a stored file the index reads and updates. The table
// belongs to the chat-file store; this area writes only the embed_* columns,
// chunk_count and, for administrator re-extraction, the processing and extract
// columns.
type File struct {
	ID               uint           `gorm:"primaryKey"`
	CreatedAt        time.Time      `gorm:"column:created_at"`
	UpdatedAt        time.Time      `gorm:"column:updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"column:deleted_at"`
	FileID           string         `gorm:"column:file_id"`
	UserID           uint           `gorm:"column:user_id"`
	FileName         string         `gorm:"column:file_name"`
	MimeType         string         `gorm:"column:mime_type"`
	DetectedMIME     string         `gorm:"column:detected_mime"`
	FileCategory     string         `gorm:"column:file_category"`
	SizeBytes        int64          `gorm:"column:size_bytes"`
	StoragePath      string         `gorm:"column:storage_path"`
	Status           string         `gorm:"column:status"`
	ProcessingStatus string         `gorm:"column:processing_status"`
	ProcessingReady  bool           `gorm:"column:processing_ready"`
	ProcessingErrMsg string         `gorm:"column:processing_error_message"`
	ProcessingErrCod string         `gorm:"column:processing_error_code"`
	ExtractStatus    string         `gorm:"column:extract_status"`
	ExtractText      string         `gorm:"column:extract_text"`
	EmbedStatus      string         `gorm:"column:embed_status"`
	EmbedSignature   string         `gorm:"column:embed_signature"`
	EmbedError       string         `gorm:"column:embed_error"`
	ChunkCount       int            `gorm:"column:chunk_count"`
	RAGOptOut        bool           `gorm:"column:rag_opt_out"`
}

// TableName implements database.Tabler.
func (File) TableName() string { return "file_objects" }

// MIME is the detected type, else the declared one.
func (f File) MIME() string {
	if f.DetectedMIME != "" {
		return f.DetectedMIME
	}
	return f.MimeType
}

// state is the normalised index state of the row.
func (f File) state() string { return normState(f.EmbedStatus) }

func normState(s string) string {
	switch s {
	case StateQueued, StateProcessing, StateReady, StateStale, StateEmpty, StateFailed:
		return s
	}
	return StateNone
}

// Chunk is one retrievable piece of a file (table file_chunks).
type Chunk struct {
	ID                 uint      `gorm:"primaryKey"`
	FileObjID          uint      `gorm:"column:file_obj_id;index"`
	UserID             uint      `gorm:"column:user_id;index"`
	ChunkIndex         int       `gorm:"column:chunk_index;default:0"`
	PageNum            int       `gorm:"column:page_num;default:0"`
	CharOffset         int       `gorm:"column:char_offset;default:0"`
	Modality           string    `gorm:"column:modality;size:16;default:text"`
	Content            string    `gorm:"column:content;type:text"`
	TokenCount         int       `gorm:"column:token_count"`
	EmbeddingSignature string    `gorm:"column:embedding_signature;size:64;index"`
	CreatedAt          time.Time `gorm:"column:created_at"`
}

// TableName implements database.Tabler.
func (Chunk) TableName() string { return "file_chunks" }

// Chunk modalities.
const (
	ModalityText  = "text"
	ModalityImage = "image"
)

// ChunkVector stores the embedding of one chunk as little-endian float32
// values (table file_chunk_embeddings, see GAPS.md for why it is not a native
// vector column).
type ChunkVector struct {
	ChunkID            uint   `gorm:"column:chunk_id;primaryKey;autoIncrement:false"`
	UserID             uint   `gorm:"column:user_id;index"`
	FileObjID          uint   `gorm:"column:file_obj_id;index"`
	EmbeddingSignature string `gorm:"column:embedding_signature;size:64;index"`
	Dims               int    `gorm:"column:dims"`
	Embedding          []byte `gorm:"column:embedding"`
}

// TableName implements database.Tabler.
func (ChunkVector) TableName() string { return "file_chunk_embeddings" }

// Job kinds.
const (
	jobIndex   = "index"
	jobExtract = "extract"
)

// Job is one durable queue message (table file_embed_jobs).
type Job struct {
	ID          uint       `gorm:"primaryKey"`
	CreatedAt   time.Time  `gorm:"column:created_at"`
	UpdatedAt   time.Time  `gorm:"column:updated_at"`
	Kind        string     `gorm:"column:kind;size:16;index"`
	UserID      uint       `gorm:"column:user_id"`
	FileObjID   uint       `gorm:"column:file_obj_id;index"`
	FileID      string     `gorm:"column:file_id;size:64"`
	Signature   string     `gorm:"column:signature;size:64"`
	Host        string     `gorm:"column:host;size:512"`
	Attempts    int        `gorm:"column:attempts"`
	Delivered   int        `gorm:"column:delivered"`
	AvailableAt time.Time  `gorm:"column:available_at;index"`
	LeaseUntil  *time.Time `gorm:"column:lease_until"`
}

// TableName implements database.Tabler.
func (Job) TableName() string { return "file_embed_jobs" }
