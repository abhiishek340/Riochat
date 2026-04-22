package knowledgebase

import (
	"time"

	"gorm.io/gorm"
)

// Knowledge base scopes.
const (
	ScopeBuiltin = "builtin"
	ScopeUser    = "user"
)

// Field limits, counted in Unicode characters.
const (
	maxNameChars        = 80
	maxDescriptionChars = 255
	maxFileIDsPerCall   = 100
	maxFileIDChars      = 64
	maxReferencedBases  = 8
)

// KnowledgeBase is a named collection of files. Rows are hard deleted.
type KnowledgeBase struct {
	ID        uint      `gorm:"primaryKey"`
	CreatedAt time.Time `gorm:"column:created_at"`
	UpdatedAt time.Time `gorm:"column:updated_at"`

	PublicID    string `gorm:"column:public_id;size:32;not null;default:'';uniqueIndex"`
	Scope       string `gorm:"column:scope;size:32;not null;default:'user';index;uniqueIndex:ux_knowledge_bases_scope_owner_name,priority:1"`
	OwnerUserID uint   `gorm:"column:owner_user_id;not null;default:0;index;uniqueIndex:ux_knowledge_bases_scope_owner_name,priority:2"`
	Name        string `gorm:"column:name;size:80;not null;default:'';uniqueIndex:ux_knowledge_bases_scope_owner_name,priority:3"`
	Description string `gorm:"column:description;size:255;not null;default:''"`
	Enabled     bool   `gorm:"column:enabled;not null;default:true;index"`
	SortOrder   int    `gorm:"column:sort_order;not null;default:0;index"`
	Revision    uint64 `gorm:"column:revision;not null;default:1"`

	CreatedByUserID uint `gorm:"column:created_by_user_id;not null;default:0;index"`
	UpdatedByUserID uint `gorm:"column:updated_by_user_id;not null;default:0;index"`
}

// TableName implements database.Tabler.
func (KnowledgeBase) TableName() string { return "knowledge_bases" }

// KnowledgeBaseFile links a knowledge base to a file object.
type KnowledgeBaseFile struct {
	KnowledgeBaseID uint      `gorm:"column:knowledge_base_id;primaryKey;autoIncrement:false;index:idx_knowledge_base_files_base_sort,priority:1"`
	FileObjectID    uint      `gorm:"column:file_object_id;primaryKey;autoIncrement:false;index"`
	SortOrder       int       `gorm:"column:sort_order;not null;default:0;index:idx_knowledge_base_files_base_sort,priority:2"`
	AddedByUserID   uint      `gorm:"column:added_by_user_id;not null;default:0"`
	CreatedAt       time.Time `gorm:"column:created_at"`
}

// TableName implements database.Tabler.
func (KnowledgeBaseFile) TableName() string { return "knowledge_base_files" }

// fileObject is the part of the file subsystem's table this area reads. The
// file area owns and migrates the table; this struct is never migrated here
// and is only ever read.
type fileObject struct {
	ID               uint           `gorm:"column:id;primaryKey"`
	FileID           string         `gorm:"column:file_id"`
	UserID           uint           `gorm:"column:user_id"`
	Purpose          string         `gorm:"column:purpose"`
	FileName         string         `gorm:"column:file_name"`
	MimeType         string         `gorm:"column:mime_type"`
	DetectedMime     string         `gorm:"column:detected_mime"`
	FileCategory     string         `gorm:"column:file_category"`
	SizeBytes        int64          `gorm:"column:size_bytes"`
	Status           string         `gorm:"column:status"`
	ProcessingStatus string         `gorm:"column:processing_status"`
	ProcessingReady  bool           `gorm:"column:processing_ready"`
	ExtractStatus    string         `gorm:"column:extract_status"`
	EmbedStatus      string         `gorm:"column:embed_status"`
	EmbedError       string         `gorm:"column:embed_error"`
	RagOptOut        bool           `gorm:"column:rag_opt_out"`
	ChunkCount       int            `gorm:"column:chunk_count"`
	PageCount        int            `gorm:"column:page_count"`
	CreatedAt        time.Time      `gorm:"column:created_at"`
	UpdatedAt        time.Time      `gorm:"column:updated_at"`
	DeletedAt        gorm.DeletedAt `gorm:"column:deleted_at"`
}

// TableName implements database.Tabler.
func (fileObject) TableName() string { return "file_objects" }

const (
	fileStatusActive = "active"
	embedStatusReady = "ready"
)

// processingStatuses are the processing_status values that count as "in
// progress".
var processingStatuses = []string{"uploaded", "queued", "extracting", "embedding"}

// isProcessing reports whether the file is still being worked on.
func (f *fileObject) isProcessing() bool {
	for _, s := range processingStatuses {
		if f.ProcessingStatus == s {
			return true
		}
	}
	return f.ExtractStatus == "processing" || f.EmbedStatus == "processing"
}

// isRAGReady reports whether the file can serve retrieval.
func (f *fileObject) isRAGReady() bool {
	return f.ProcessingReady && f.EmbedStatus == embedStatusReady && !f.RagOptOut && f.ChunkCount > 0
}
