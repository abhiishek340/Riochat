package conversation

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode"
	"unicode/utf8"

	"gorm.io/gorm"

	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/platform/database"
	"github.com/riochat/riochat/backend/internal/platform/logx"
)

const (
	fileStatusActive  = "active"
	fileStatusDeleted = "deleted"
	previewRunes      = 600
	maxStatusBatch    = 100
	maxFileNameRunes  = 255
	// defaultFilePurpose is stored when an upload names no purpose.
	defaultFilePurpose = "conversation_input"
)

var blockedMIME = map[string]bool{
	"application/x-msdownload": true, "application/x-dosexec": true, "application/x-msi": true,
	"application/x-executable": true, "application/x-sharedlib": true, "application/x-mach-binary": true,
	"application/vnd.microsoft.portable-executable": true, "application/x-bat": true, "application/x-sh": true,
	"application/x-csh": true, "application/java-archive": true, "application/x-apple-diskimage": true,
}

var blockedExt = map[string]bool{
	".exe": true, ".dll": true, ".bat": true, ".cmd": true, ".com": true, ".scr": true, ".msi": true,
	".vbs": true, ".jar": true, ".app": true, ".dmg": true, ".pif": true,
}

var codeExt = map[string]bool{
	".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true, ".jsx": true, ".java": true, ".c": true,
	".h": true, ".cpp": true, ".hpp": true, ".cs": true, ".rs": true, ".rb": true, ".php": true, ".swift": true,
	".kt": true, ".sql": true, ".sh": true, ".css": true, ".html": true, ".json": true, ".yaml": true,
	".yml": true, ".toml": true, ".xml": true, ".md": true,
}

// defaultAllowedMIME is advertised when the administrator has not narrowed it.
var defaultAllowedMIME = []string{
	"application/json", "application/msword", "application/pdf", "application/vnd.ms-excel",
	"application/vnd.ms-powerpoint",
	"application/vnd.openxmlformats-officedocument.presentationml.presentation",
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document",
	"application/xml", "audio/mpeg", "audio/wav", "image/gif", "image/jpeg", "image/png", "image/webp",
	"text/csv", "text/html", "text/markdown", "text/plain", "video/mp4", "video/webm",
}

func baseMIME(m string) string {
	if i := strings.IndexByte(m, ';'); i >= 0 {
		m = m[:i]
	}
	return strings.ToLower(strings.TrimSpace(m))
}

// extMIME names text types the standard table does not know consistently.
var extMIME = map[string]string{
	".md": "text/markdown", ".markdown": "text/markdown", ".yaml": "text/yaml", ".yml": "text/yaml",
}

func detectMIME(name string, head []byte, declared string) string {
	sniffed := baseMIME(http.DetectContentType(head))
	ext := strings.ToLower(filepath.Ext(name))
	byExt := baseMIME(mime.TypeByExtension(ext))
	if m, ok := extMIME[ext]; ok {
		byExt = m
	}
	// Markup is stored and served as plain text, never as active content.
	if sniffed == "text/html" || sniffed == "text/xml" {
		sniffed = "text/plain"
	}
	switch {
	case sniffed == "" || sniffed == "application/octet-stream":
		if byExt != "" {
			return byExt
		}
		if d := baseMIME(declared); d != "" {
			return d
		}
		return "application/octet-stream"
	case sniffed == "text/plain" && byExt != "" && strings.HasPrefix(byExt, "text/") && byExt != "text/html" && byExt != "text/xml":
		return byExt
	case sniffed == "text/plain" && byExt == "application/json":
		return byExt
	case sniffed == "application/zip" && byExt != "":
		return byExt
	}
	return sniffed
}

// fileCategory is the coarse family stored with a file: image, pdf, audio,
// video, word, excel, presentation, text or unknown.
func fileCategory(mimeType, name string) string {
	ext := strings.ToLower(filepath.Ext(name))
	switch {
	case strings.HasPrefix(mimeType, "image/") && ext != ".svg":
		return "image"
	case mimeType == "application/pdf" || ext == ".pdf":
		return "pdf"
	case strings.HasPrefix(mimeType, "audio/"):
		return "audio"
	case strings.HasPrefix(mimeType, "video/"):
		return "video"
	case strings.Contains(mimeType, "presentation") || strings.Contains(mimeType, "ms-powerpoint") || ext == ".pptx" || ext == ".ppt":
		return "presentation"
	case mimeType == "text/csv" || ext == ".csv" || ext == ".xls" || strings.Contains(mimeType, "ms-excel"):
		return "excel"
	case strings.Contains(mimeType, "wordprocessingml") || strings.Contains(mimeType, "spreadsheetml") || strings.Contains(mimeType, "msword") ||
		ext == ".docx" || ext == ".doc" || ext == ".xlsx":
		return "word"
	case isTextual(mimeType, name):
		return "text"
	}
	return "unknown"
}

func isTextual(mimeType, name string) bool {
	if strings.HasPrefix(mimeType, "text/") {
		return true
	}
	switch mimeType {
	case "application/json", "application/xml", "application/x-yaml", "application/javascript":
		return true
	}
	return codeExt[strings.ToLower(filepath.Ext(name))]
}

// objectPrefix is the key folder of chat-file bytes inside the object store.
// It matches the folder used on the local storage root, so switching between
// the built-in local writer and a shared object store keeps the same layout.
const objectPrefix = "chat-files/"

// storageRoot returns the directory under which file bytes are kept on disk.
func (s *service) storageRoot() (string, error) {
	cfg := s.cfg.Load()
	if cfg.Storage.Backend != config.StorageLocal {
		return "", ErrServiceUnavailable.WithCause(errors.New("conversation files support only the local storage backend"))
	}
	root := cfg.Storage.Local.RootDir
	if root == "" {
		return "", ErrServiceUnavailable.WithCause(errors.New("storage root is not configured"))
	}
	return filepath.Join(root, "chat-files"), nil
}

// storageReady reports whether file bytes can be stored right now.
func (s *service) storageReady() error {
	if s.ports.Objects != nil {
		return nil
	}
	_, err := s.storageRoot()
	return err
}

func (s *service) absPath(rel string) (string, error) {
	root, err := s.storageRoot()
	if err != nil {
		return "", err
	}
	clean := filepath.Clean(filepath.FromSlash(rel))
	if strings.HasPrefix(clean, "..") || filepath.IsAbs(clean) {
		return "", ErrFileNotFound
	}
	return filepath.Join(root, clean), nil
}

// objectKey maps a stored relative path to its object-store key.
func objectKey(rel string) (string, error) {
	clean := path.Clean(strings.ReplaceAll(strings.TrimSpace(rel), "\\", "/"))
	if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") {
		return "", ErrFileNotFound
	}
	return objectPrefix + clean, nil
}

func (s *service) writeBytes(ctx context.Context, rel string, data []byte) error {
	if s.ports.Objects != nil {
		key, err := objectKey(rel)
		if err != nil {
			return err
		}
		return s.ports.Objects.Put(ctx, key, data, "")
	}
	abs, err := s.absPath(rel)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	return os.WriteFile(abs, data, 0o644)
}

func (s *service) readBytes(ctx context.Context, rel string) ([]byte, error) {
	if s.ports.Objects != nil {
		key, err := objectKey(rel)
		if err != nil {
			return nil, err
		}
		b, err := s.ports.Objects.Get(ctx, key)
		if errors.Is(err, ErrObjectNotFound) {
			return nil, ErrFileNotFound
		}
		return b, err
	}
	abs, err := s.absPath(rel)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return b, nil
}

// removeBytes deletes stored bytes on a budget of its own, so a cancelled
// request does not leave an orphan behind. Failures are only logged.
func (s *service) removeBytes(ctx context.Context, rel string) {
	if err := s.deleteBytes(ctx, rel); err != nil {
		s.log.Warn("conversation_file_bytes_remove_failed", logx.Err(err))
	}
}

func (s *service) deleteBytes(ctx context.Context, rel string) error {
	if s.ports.Objects != nil {
		key, err := objectKey(rel)
		if err != nil {
			return nil
		}
		dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
		defer cancel()
		return s.ports.Objects.Delete(dctx, key)
	}
	abs, err := s.absPath(rel)
	if err != nil {
		return nil
	}
	return removeIfExists(abs)
}

// ----- quota -----

// quotaFor loads (creating on first use) the user's quota record. A record
// whose allowance is 0 follows the configured default def (0 = unlimited), so
// records created before the default existed behave like new ones. The
// substitution is in memory only; usage updates never write the allowance.
func quotaFor(tx *gorm.DB, userID uint, def int64) (*StorageQuota, error) {
	q, err := loadQuota(tx, userID)
	if err != nil {
		return nil, err
	}
	if q.QuotaBytes <= 0 && def > 0 {
		q.QuotaBytes = def
	}
	return q, nil
}

func loadQuota(tx *gorm.DB, userID uint) (*StorageQuota, error) {
	var q StorageQuota
	err := lockRows(tx).Where("user_id = ?", userID).First(&q).Error
	if err == nil {
		return &q, nil
	}
	if !database.IsNotFound(err) {
		return nil, err
	}
	q = StorageQuota{UserID: userID}
	if err := tx.Create(&q).Error; err != nil {
		if database.IsDuplicate(database.Translate(err)) {
			if err := lockRows(tx).Where("user_id = ?", userID).First(&q).Error; err != nil {
				return nil, err
			}
			return &q, nil
		}
		return nil, err
	}
	return &q, nil
}

func quotaDTO(q *StorageQuota) *QuotaDTO {
	if q == nil {
		return nil
	}
	return &QuotaDTO{ID: q.ID, UserID: q.UserID, QuotaBytes: q.QuotaBytes, UsedBytes: q.UsedBytes, ReservedBytes: q.ReservedBytes, CreatedAt: q.CreatedAt, UpdatedAt: q.UpdatedAt}
}

func (s *service) userQuota(ctx context.Context, userID uint) (*QuotaDTO, error) {
	def := s.settings(ctx).UserStorageQuotaBytes
	var q *StorageQuota
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		q, err = quotaFor(tx, userID, def)
		return err
	})
	return quotaDTO(q), err
}

// consumeQuota adds size to the user's usage, failing when the allowance would be exceeded.
func consumeQuota(tx *gorm.DB, userID uint, size, def int64, failure error) error {
	q, err := quotaFor(tx, userID, def)
	if err != nil {
		return err
	}
	if q.QuotaBytes > 0 && q.UsedBytes+q.ReservedBytes+size > q.QuotaBytes {
		return failure
	}
	return tx.Model(&StorageQuota{}).Where("id = ?", q.ID).UpdateColumns(map[string]any{"used_bytes": gorm.Expr("used_bytes + ?", size), "updated_at": time.Now()}).Error
}

func releaseQuota(tx *gorm.DB, userID uint, size int64) error {
	q, err := loadQuota(tx, userID)
	if err != nil {
		return err
	}
	used := q.UsedBytes - size
	if used < 0 {
		used = 0
	}
	return tx.Model(&StorageQuota{}).Where("id = ?", q.ID).UpdateColumns(map[string]any{"used_bytes": used, "updated_at": time.Now()}).Error
}

// ----- DTOs -----

// FileDTO is the file object returned to clients.
type FileDTO struct {
	FileID                 string  `json:"fileID"`
	Purpose                string  `json:"purpose"`
	FileName               string  `json:"fileName"`
	MimeType               string  `json:"mimeType"`
	DetectedMIME           string  `json:"detectedMIME"`
	FileCategory           string  `json:"fileCategory"`
	SizeBytes              int64   `json:"sizeBytes"`
	SHA256                 string  `json:"sha256"`
	Status                 string  `json:"status"`
	ProcessingStatus       string  `json:"processingStatus"`
	ProcessingReady        bool    `json:"processingReady"`
	ProcessingErrorCode    string  `json:"processingErrorCode"`
	ProcessingErrorMessage string  `json:"processingErrorMessage"`
	ExtractStatus          string  `json:"extractStatus"`
	EmbedStatus            string  `json:"embedStatus"`
	EmbedError             string  `json:"embedError"`
	ChunkCount             int     `json:"chunkCount"`
	RAGOptOut              bool    `json:"ragOptOut"`
	CanVectorize           bool    `json:"canVectorize"`
	VectorizationReason    string  `json:"vectorizationReason"`
	LastAccessedAt         *string `json:"lastAccessedAt"`
	ExpiresAt              *string `json:"expiresAt"`
	CreatedAt              string  `json:"createdAt"`
	UpdatedAt              string  `json:"updatedAt"`
}

// FileProcessingDTO is the processing status object.
type FileProcessingDTO struct {
	FileID              string  `json:"fileID"`
	DetectedMIME        string  `json:"detectedMIME"`
	FileCategory        string  `json:"fileCategory"`
	ProcessingStatus    string  `json:"processingStatus"`
	ProcessingReady     bool    `json:"processingReady"`
	ExtractStatus       string  `json:"extractStatus"`
	EmbedStatus         string  `json:"embedStatus"`
	PreviewText         string  `json:"previewText"`
	OCRUsed             bool    `json:"ocrUsed"`
	RAGReady            bool    `json:"ragReady"`
	RAGReason           string  `json:"ragReason"`
	ErrorCode           string  `json:"errorCode"`
	ErrorMessage        string  `json:"errorMessage"`
	ExtractChars        int     `json:"extractChars"`
	ExtractPages        int     `json:"extractPages"`
	ChunkCount          int     `json:"chunkCount"`
	EmbedError          string  `json:"embedError"`
	CanVectorize        bool    `json:"canVectorize"`
	VectorizationReason string  `json:"vectorizationReason"`
	StartedAt           *string `json:"startedAt"`
	CompletedAt         *string `json:"completedAt"`
	UpdatedAt           string  `json:"updatedAt"`
}

func rfc(t *time.Time) *string {
	if t == nil {
		return nil
	}
	v := t.Local().Format("2006-01-02T15:04:05.999999999Z07:00")
	return &v
}

func (s *service) vectorization(ctx context.Context, rs RuntimeSettings, f FileObject) (bool, string) {
	if s.ports.Indexer != nil {
		return s.ports.Indexer.Vectorization(ctx, factsOf(f))
	}
	switch {
	case !rs.RAGAvailable:
		if rs.RAGAvailabilityReason != "" {
			return false, rs.RAGAvailabilityReason
		}
		return false, "embedding_not_configured"
	case f.RAGOptOut:
		return false, "rag_opt_out"
	case f.ExtractStatus != "ready":
		return false, "no_extracted_text"
	}
	return true, ""
}
