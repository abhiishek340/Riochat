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

func (s *service) fileDTO(ctx context.Context, rs RuntimeSettings, f FileObject) FileDTO {
	can, reason := s.vectorization(ctx, rs, f)
	return FileDTO{
		FileID: f.FileID, Purpose: f.Purpose, FileName: f.FileName, MimeType: f.MimeType, DetectedMIME: f.DetectedMIME,
		FileCategory: f.FileCategory, SizeBytes: f.SizeBytes, SHA256: f.SHA256, Status: f.Status,
		ProcessingStatus: f.ProcessingStatus, ProcessingReady: f.ProcessingReady, ProcessingErrorCode: f.ProcessingErrCode,
		ProcessingErrorMessage: f.ProcessingErrMsg, ExtractStatus: f.ExtractStatus, EmbedStatus: f.EmbedStatus,
		EmbedError: f.EmbedError, ChunkCount: f.ChunkCount, RAGOptOut: f.RAGOptOut, CanVectorize: can, VectorizationReason: reason,
		LastAccessedAt: rfc(f.LastAccessedAt), ExpiresAt: rfc(f.ExpiresAt),
		CreatedAt: *rfc(&f.CreatedAt), UpdatedAt: *rfc(&f.UpdatedAt),
	}
}

// ragReasonOf words why a file is not searchable in the processing status.
func ragReasonOf(f FileObject, can bool, reason string) string {
	switch {
	case f.ProcessingStatus == "failed":
		return "extract_failed"
	case can:
		return reason
	case f.FileCategory == "image" && (reason == "embedding_disabled" || reason == "rag_disabled"):
		return "image_not_applicable"
	case reason == "embedding_disabled":
		return "rag_disabled"
	}
	return reason
}

func (s *service) processingDTO(ctx context.Context, rs RuntimeSettings, f FileObject) FileProcessingDTO {
	can, reason := s.vectorization(ctx, rs, f)
	return FileProcessingDTO{
		FileID: f.FileID, DetectedMIME: f.DetectedMIME, FileCategory: f.FileCategory, ProcessingStatus: f.ProcessingStatus,
		ProcessingReady: f.ProcessingReady, ExtractStatus: f.ExtractStatus, EmbedStatus: f.EmbedStatus, PreviewText: f.PreviewText,
		OCRUsed: f.OCRUsed, RAGReady: f.EmbedStatus == "ready", RAGReason: ragReasonOf(f, can, reason), ErrorCode: f.ProcessingErrCode,
		ErrorMessage: f.ProcessingErrMsg, ExtractChars: f.ExtractChars, ExtractPages: f.PageCount, ChunkCount: f.ChunkCount,
		EmbedError: f.EmbedError, CanVectorize: can, VectorizationReason: reason,
		StartedAt: rfc(f.ProcessingStarted), CompletedAt: rfc(f.ProcessingDone), UpdatedAt: *rfc(&f.UpdatedAt),
	}
}

// ----- upload -----

type uploadResult struct {
	File   FileDTO   `json:"file"`
	Quota  *QuotaDTO `json:"quota"`
	Reused bool      `json:"reused"`
}

// maxUploadNameRunes is the longest stored file name a new upload may carry.
const maxUploadNameRunes = 255

// sanitizeName turns a client supplied name into the stored form: whitespace
// runs inside the name become one underscore each, the rest is kept.
func sanitizeName(n string) string {
	var b strings.Builder
	for _, r := range n {
		if unicode.IsSpace(r) {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func validFileName(name string) (string, bool) {
	n := strings.TrimSpace(name)
	if n == "" {
		return "", false
	}
	if strings.ContainsAny(n, "/\\") || n == "." || n == ".." {
		return "", false
	}
	for _, r := range n {
		if unicode.IsControl(r) {
			return "", false
		}
	}
	return sanitizeName(n), true
}

func (s *service) uploadFile(ctx context.Context, userID uint, name, declaredMIME, purpose string, r io.Reader) (*uploadResult, error) {
	rs := s.settings(ctx)
	clean, ok := validFileName(path.Base(strings.ReplaceAll(name, "\\", "/")))
	if !ok {
		return nil, ErrInvalidFile
	}
	if utf8.RuneCountInString(clean) > maxUploadNameRunes {
		return nil, errors.New("conversation: stored file name is too long")
	}
	if strings.TrimSpace(purpose) == "" {
		purpose = defaultFilePurpose
	}
	data, err := io.ReadAll(io.LimitReader(r, rs.MaxUploadBytes+1))
	if err != nil {
		return nil, ErrFileInvalidStream.WithCause(err)
	}
	if int64(len(data)) > rs.MaxUploadBytes {
		return nil, ErrFileRequired
	}
	head := data
	if len(head) > 512 {
		head = head[:512]
	}
	detected := detectMIME(clean, head, declaredMIME)
	if blockedMIME[detected] || blockedExt[strings.ToLower(filepath.Ext(clean))] || blockedMIME[baseMIME(declaredMIME)] {
		return nil, ErrFileTypeBlocked
	}
	if len(rs.AllowedMIMETypes) > 0 && !containsFold(rs.AllowedMIMETypes, detected) {
		return nil, ErrFileTypeBlocked
	}
	category := fileCategory(detected, clean)
	limit := rs.MaxUploadBytes
	if category == "image" && rs.ImageMaxBytes > 0 && rs.ImageMaxBytes < limit {
		limit = rs.ImageMaxBytes
	} else if category != "image" && rs.DocMaxBytes > 0 && rs.DocMaxBytes < limit {
		limit = rs.DocMaxBytes
	}
	if int64(len(data)) > limit {
		return nil, ErrFileTooLarge
	}
	sum := sha256.Sum256(data)
	hash := hex.EncodeToString(sum[:])

	// Identical uploads are serialized per user, hash and size, so two
	// concurrent copies cannot both miss each other's record.
	unlock := s.uploadGate.lock(itoa(int(userID)) + ":" + hash + ":" + itoa(len(data)))
	defer unlock()
	if reused, err := s.reuseVerified(ctx, rs, userID, hash, int64(len(data))); err != nil || reused != nil {
		return reused, err
	}

	fileID := newFileID()
	rel := path.Join(itoa(int(userID)), fileID)
	if err := s.writeBytes(ctx, rel, data); err != nil {
		return nil, err
	}
	f := FileObject{
		FileID: fileID, UserID: userID, Purpose: strings.TrimSpace(purpose), FileName: clean, MimeType: baseMIME(declaredMIME),
		DetectedMIME: detected, FileCategory: category, SizeBytes: int64(len(data)), SHA256: hash, StoragePath: rel,
		Status: fileStatusActive, ProcessingStatus: "queued", ProcessingReady: false, ExtractStatus: "none",
		EmbedStatus: "none",
	}
	if f.MimeType == "" {
		f.MimeType = detected
	}
	var quota *StorageQuota
	err = s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := consumeQuota(tx, userID, f.SizeBytes, rs.UserStorageQuotaBytes, ErrQuotaExceeded); err != nil {
			return err
		}
		if err := tx.Create(&f).Error; err != nil {
			return err
		}
		var err error
		quota, err = quotaFor(tx, userID, rs.UserStorageQuotaBytes)
		return err
	})
	if err != nil {
		s.removeBytes(ctx, rel)
		if database.IsDuplicate(database.Translate(err)) {
			if reused, rerr := s.reuseVerified(ctx, rs, userID, hash, int64(len(data))); rerr == nil && reused != nil {
				return reused, nil
			}
		}
		return nil, err
	}
	queued := f
	if inlineProcessing.Load() {
		s.processFile(ctx, f.ID, data)
		if err := s.db.WithContext(ctx).Where("id = ?", f.ID).First(&queued).Error; err != nil {
			return nil, err
		}
	} else {
		go s.processFile(context.WithoutCancel(ctx), f.ID, data)
	}
	return &uploadResult{File: s.fileDTO(ctx, rs, queued), Quota: quotaDTO(quota)}, nil
}

// inlineProcessing makes uploads finish their text processing before the
// request returns instead of in the background (used by tests).
var inlineProcessing atomic.Bool

// fileOutcome is the result of processing one uploaded file.
type fileOutcome struct {
	status, errCode, errMsg, extract string
	ready                            bool
	text, preview                    string
	chars                            int
}

const extractFailedMessage = "cannot extract text from this file"

// collapseSpace folds every run of whitespace, newlines included, into one
// space.
func collapseSpace(s string) string { return strings.Join(strings.Fields(s), " ") }

// evaluateFile works out what text processing makes of an uploaded file.
func evaluateFile(f FileObject, data []byte) fileOutcome {
	failed := fileOutcome{status: "failed", errCode: "extract_failed", errMsg: extractFailedMessage, extract: "failed"}
	ready := fileOutcome{status: "ready", ready: true, extract: "none"}
	ext := strings.ToLower(filepath.Ext(f.FileName))
	switch f.FileCategory {
	case "image", "audio", "video":
		return ready
	case "pdf":
		if bytes.HasPrefix(bytes.TrimLeft(data, "\x00\t\n\r "), []byte("%PDF-")) && bytes.Contains(data, []byte("%%EOF")) {
			return ready
		}
		return failed
	}
	if ext == ".xlsx" || ext == ".xls" {
		if bytes.HasPrefix(data, []byte("PK")) || bytes.HasPrefix(data, []byte{0xD0, 0xCF, 0x11, 0xE0}) {
			return ready
		}
		return failed
	}
	if isTextual(f.DetectedMIME, f.FileName) && f.FileCategory != "word" && f.FileCategory != "presentation" {
		if !utf8.Valid(data) {
			return failed
		}
		text := strings.TrimSpace(string(data))
		if text == "" {
			return failed
		}
		out := ready
		out.extract = "ready"
		out.text = text
		out.chars = utf8.RuneCountInString(text)
		out.preview = truncateRunes(collapseSpace(text), previewRunes)
		return out
	}
	return ready
}

// processFile finishes the text processing of a freshly stored file and then
// tells the indexer about it.
func (s *service) processFile(ctx context.Context, id uint, data []byte) {
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	var f FileObject
	if err := s.db.WithContext(ctx).Where("id = ? AND status = ?", id, fileStatusActive).First(&f).Error; err != nil {
		return
	}
	now := s.now()
	out := evaluateFile(f, data)
	upd := map[string]any{
		"processing_status": out.status, "processing_ready": out.ready, "processing_error_code": out.errCode,
		"processing_error_message": out.errMsg, "extract_status": out.extract, "extract_text": out.text,
		"preview_text": out.preview, "extract_chars": out.chars, "page_count": 0, "processing_completed_at": now,
		"updated_at": now,
	}
	if out.status != "failed" {
		upd["processing_started_at"] = now
	}
	if err := s.db.WithContext(ctx).Model(&FileObject{}).Where("id = ?", id).UpdateColumns(upd).Error; err != nil {
		s.log.Warn("conversation_file_processing_failed", logx.Err(err))
		return
	}
	if s.ports.Indexer != nil && out.ready {
		s.ports.Indexer.TextReady(ctx, id)
	}
}

// reuseVerified returns the user's existing upload of the same bytes after
// checking that its object is still in the store with the same size and hash.
// A record whose object is gone or differs is stale: it is deleted (quota
// released, object removed) and the search repeats. It returns nil when no
// usable record exists.
func (s *service) reuseVerified(ctx context.Context, rs RuntimeSettings, userID uint, hash string, size int64) (*uploadResult, error) {
	for attempt := 0; attempt < 4; attempt++ {
		var existing FileObject
		err := s.db.WithContext(ctx).Where("user_id = ? AND sha256 = ? AND size_bytes = ? AND status = ?", userID, hash, size, fileStatusActive).
			Order("id ASC").First(&existing).Error
		if err != nil {
			if database.IsNotFound(err) {
				return nil, nil
			}
			return nil, err
		}
		stored, rerr := s.readBytes(ctx, existing.StoragePath)
		switch {
		case rerr == nil && int64(len(stored)) == size && sha256Hex(stored) == hash:
			now := s.now()
			if err := s.db.WithContext(ctx).Model(&FileObject{}).Where("id = ?", existing.ID).UpdateColumn("last_accessed_at", now).Error; err != nil {
				s.log.Warn("conversation_file_touch_failed", logx.Err(err))
			}
			existing.LastAccessedAt = &now
			quota, qerr := s.userQuota(ctx, userID)
			if qerr != nil {
				return nil, qerr
			}
			return &uploadResult{File: s.fileDTO(ctx, rs, existing), Quota: quota, Reused: true}, nil
		case rerr == nil || errors.Is(rerr, ErrFileNotFound):
			if _, derr := s.removeFile(ctx, &existing); derr != nil {
				return nil, derr
			}
		default:
			return nil, rerr
		}
	}
	return nil, nil
}

func sha256Hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

// keyedGate serializes work per key within the process.
type keyedGate struct {
	mu   sync.Mutex
	held map[string]*gateEntry
}

type gateEntry struct {
	mu   sync.Mutex
	refs int
}

func (g *keyedGate) lock(key string) func() {
	g.mu.Lock()
	if g.held == nil {
		g.held = map[string]*gateEntry{}
	}
	e := g.held[key]
	if e == nil {
		e = &gateEntry{}
		g.held[key] = e
	}
	e.refs++
	g.mu.Unlock()
	e.mu.Lock()
	return func() {
		e.mu.Unlock()
		g.mu.Lock()
		if e.refs--; e.refs == 0 {
			delete(g.held, key)
		}
		g.mu.Unlock()
	}
}

func containsFold(list []string, v string) bool {
	for _, x := range list {
		if strings.EqualFold(strings.TrimSpace(x), v) {
			return true
		}
	}
	return false
}

// ----- lookups -----

func (s *service) getFile(ctx context.Context, userID uint, fileID string) (*FileObject, error) {
	fileID = strings.TrimSpace(fileID)
	if fileID == "" || len(fileID) > 64 {
		return nil, ErrFileInvalidID
	}
	var f FileObject
	err := s.db.WithContext(ctx).Where("file_id = ? AND user_id = ? AND status = ?", fileID, userID, fileStatusActive).First(&f).Error
	if err != nil {
		if database.IsNotFound(err) {
			return nil, ErrFileNotFound
		}
		return nil, err
	}
	return &f, nil
}

type fileListQuery struct {
	Q    string
	Kind string
	Sort string
	Page int
	Size int
}

// kindCategories maps a "kind" filter value to the stored categories it
// selects. "document" matches a category no upload produces.
var kindCategories = map[string][]string{
	"image": {"image"}, "document": {"document"}, "spreadsheet": {"excel"}, "presentation": {"presentation"},
	"code": {"text"}, "pdf": {"pdf"}, "audio": {"audio"}, "video": {"video"},
}

type fileListResult struct {
	Total   int64     `json:"total"`
	Results []FileDTO `json:"results"`
	Quota   *QuotaDTO `json:"quota"`
}

func (s *service) listFiles(ctx context.Context, userID uint, q fileListQuery, offset, limit int) (*fileListResult, error) {
	rs := s.settings(ctx)
	db := s.db.WithContext(ctx).Model(&FileObject{}).Where("user_id = ? AND status = ?", userID, fileStatusActive)
	if kw := strings.TrimSpace(q.Q); kw != "" {
		db = db.Where("LOWER(file_name) LIKE ? ESCAPE '!'", "%"+escapeLike(strings.ToLower(kw))+"%")
	}
	var kinds []string
	images := false
	for _, k := range strings.Split(q.Kind, ",") {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "all" {
			kinds, images = nil, false
			break
		}
		if cats, ok := kindCategories[k]; ok {
			kinds = append(kinds, cats...)
			images = images || k == "image"
		}
	}
	if len(kinds) > 0 {
		if images {
			// A picture is also whatever was declared as one.
			db = db.Where("(file_category IN ? OR mime_type LIKE 'image/%')", kinds)
		} else {
			db = db.Where("file_category IN ?", kinds)
		}
	}
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	order := "created_at DESC, id DESC"
	switch strings.ToLower(strings.TrimSpace(q.Sort)) {
	case "name":
		order = "LOWER(file_name) ASC, id ASC"
	case "size":
		order = "size_bytes DESC, id DESC"
	case "last_used":
		order = "CASE WHEN last_accessed_at IS NULL THEN 1 ELSE 0 END, last_accessed_at DESC, id DESC"
	}
	var rows []FileObject
	if err := db.Order(order).Offset(offset).Limit(limit).Find(&rows).Error; err != nil {
		return nil, err
	}
	res := &fileListResult{Total: total, Results: make([]FileDTO, 0, len(rows))}
	for _, f := range rows {
		res.Results = append(res.Results, s.fileDTO(ctx, rs, f))
	}
	quota, err := s.userQuota(ctx, userID)
	if err != nil {
		return nil, err
	}
	res.Quota = quota
	return res, nil
}
