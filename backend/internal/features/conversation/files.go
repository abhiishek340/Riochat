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

// openFile returns the bytes of an owned file and refreshes its last-use time.
func (s *service) openFile(ctx context.Context, userID uint, fileID string) (*FileObject, []byte, error) {
	f, err := s.getFile(ctx, userID, fileID)
	if err != nil {
		return nil, nil, err
	}
	data, err := s.readBytes(ctx, f.StoragePath)
	if err != nil {
		return nil, nil, err
	}
	now := s.now()
	_ = s.db.WithContext(ctx).Model(&FileObject{}).Where("id = ?", f.ID).UpdateColumn("last_accessed_at", now).Error
	return f, data, nil
}

// ExtractDTO is the extracted text object.
type ExtractDTO struct {
	FileID       string `json:"fileID"`
	ExtractText  string `json:"extractText"`
	PreviewText  string `json:"previewText"`
	ExtractChars int    `json:"extractChars"`
	ExtractPages int    `json:"extractPages"`
	OCRUsed      bool   `json:"ocrUsed"`
}

func (s *service) extractFile(ctx context.Context, userID uint, fileID string) (*ExtractDTO, error) {
	f, err := s.getFile(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	if f.ExtractStatus != "ready" {
		return nil, ErrFileExtractPending
	}
	if f.ExtractChars == 0 && strings.TrimSpace(f.ExtractText) == "" {
		return nil, ErrFileExtractPending
	}
	return &ExtractDTO{FileID: f.FileID, ExtractText: f.ExtractText, PreviewText: f.PreviewText, ExtractChars: f.ExtractChars, ExtractPages: f.PageCount, OCRUsed: f.OCRUsed}, nil
}

func (s *service) processingStatus(ctx context.Context, userID uint, fileID string) (*FileProcessingDTO, error) {
	f, err := s.getFile(ctx, userID, fileID)
	if err != nil {
		if errors.Is(err, ErrFileNotFound) {
			// A status request for a file the caller cannot see is a server
			// fault, not a 404.
			return nil, errors.New("conversation: file status lookup failed: " + err.Error())
		}
		return nil, err
	}
	d := s.processingDTO(ctx, s.settings(ctx), *f)
	return &d, nil
}

func (s *service) processingStatuses(ctx context.Context, userID uint, ids []string) ([]FileProcessingDTO, error) {
	clean := make([]string, 0, len(ids))
	for _, id := range ids {
		if t := strings.TrimSpace(id); t != "" {
			clean = append(clean, t)
		}
	}
	clean = dedupeStrings(clean)
	out := []FileProcessingDTO{}
	if len(clean) == 0 {
		return out, nil
	}
	var rows []FileObject
	if err := s.db.WithContext(ctx).Where("user_id = ? AND status = ? AND file_id IN ?", userID, fileStatusActive, clean).Find(&rows).Error; err != nil {
		return nil, err
	}
	byID := map[string]FileObject{}
	for _, r := range rows {
		byID[r.FileID] = r
	}
	rs := s.settings(ctx)
	for _, id := range clean {
		if f, ok := byID[id]; ok {
			out = append(out, s.processingDTO(ctx, rs, f))
		}
	}
	return out, nil
}

// EmbeddingSubmission is the result of submitting files for embedding.
type EmbeddingSubmission struct {
	SubmittedFileIDs []string        `json:"submittedFileIDs"`
	Skipped          []EmbeddingSkip `json:"skipped"`
}

// EmbeddingSkip explains why a file was not submitted.
type EmbeddingSkip struct {
	FileID string `json:"fileID"`
	Reason string `json:"reason"`
}

func (s *service) submitEmbeddings(ctx context.Context, userID uint, ids []string) (*EmbeddingSubmission, error) {
	if s.ports.Indexer != nil {
		return s.ports.Indexer.Submit(ctx, userID, ids)
	}
	clean := dedupeStrings(trimAll(ids))
	if len(clean) > maxStatusBatch {
		return nil, ErrEmbedTooMany
	}
	rs := s.settings(ctx)
	if !rs.RAGAvailable {
		return nil, ErrEmbedNotConfigured
	}
	out := &EmbeddingSubmission{SubmittedFileIDs: []string{}, Skipped: []EmbeddingSkip{}}
	for _, id := range clean {
		if id == "" {
			continue
		}
		f, err := s.getFile(ctx, userID, id)
		if err != nil {
			out.Skipped = append(out.Skipped, EmbeddingSkip{FileID: id, Reason: "not_found"})
			continue
		}
		if ok, reason := s.vectorization(ctx, rs, *f); !ok {
			out.Skipped = append(out.Skipped, EmbeddingSkip{FileID: id, Reason: reason})
			continue
		}
		if err := s.db.WithContext(ctx).Model(&FileObject{}).Where("id = ?", f.ID).UpdateColumn("embed_status", "queued").Error; err != nil {
			return nil, ErrEmbedSubmitFailed.WithCause(err)
		}
		out.SubmittedFileIDs = append(out.SubmittedFileIDs, id)
	}
	return out, nil
}

func trimAll(in []string) []string {
	out := make([]string, 0, len(in))
	for _, v := range in {
		out = append(out, strings.TrimSpace(v))
	}
	return out
}

func (s *service) patchFile(ctx context.Context, userID uint, fileID string, name *string, ragOptOut *bool) (*FileDTO, error) {
	f, err := s.getFile(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	if name == nil && ragOptOut == nil {
		return nil, ErrFilePatchRequired
	}
	upd := map[string]any{}
	if name != nil {
		clean, ok := validFileName(path.Base(strings.ReplaceAll(strings.TrimSpace(*name), "\\", "/")))
		if !ok {
			return nil, ErrFileInvalidName
		}
		upd["file_name"] = clean
		f.FileName = clean
	}
	if ragOptOut != nil {
		upd["rag_opt_out"] = *ragOptOut
		f.RAGOptOut = *ragOptOut
	}
	if err := s.db.WithContext(ctx).Model(&FileObject{}).Where("id = ?", f.ID).Updates(upd).Error; err != nil {
		return nil, err
	}
	d := s.fileDTO(ctx, s.settings(ctx), *f)
	return &d, nil
}

// FileDeleteResult is returned when a file is deleted.
type FileDeleteResult struct {
	Deleted bool      `json:"deleted"`
	FileID  string    `json:"fileID"`
	Quota   *QuotaDTO `json:"quota"`
}

// removeFile deletes a file object, its bytes and releases its quota.
func (s *service) removeFile(ctx context.Context, f *FileObject) (*QuotaDTO, error) {
	def := s.settings(ctx).UserStorageQuotaBytes
	var quota *StorageQuota
	err := s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		// Attachment rows are history: a message keeps showing the file it
		// was sent with after the file itself is gone.
		if s.ports.Indexer != nil {
			if err := s.ports.Indexer.Forget(ctx, tx, f.ID); err != nil {
				return err
			}
		} else if err := tx.Where("file_obj_id = ?", f.ID).Delete(&FileChunk{}).Error; err != nil {
			return err
		}
		if err := tx.Model(&FileObject{}).Where("id = ?", f.ID).UpdateColumn("status", fileStatusDeleted).Error; err != nil {
			return err
		}
		if err := tx.Delete(&FileObject{}, f.ID).Error; err != nil {
			return err
		}
		if err := releaseQuota(tx, f.UserID, f.SizeBytes); err != nil {
			return err
		}
		var err error
		quota, err = quotaFor(tx, f.UserID, def)
		return err
	})
	if err != nil {
		return nil, err
	}
	s.removeBytes(ctx, f.StoragePath)
	return quotaDTO(quota), nil
}

func (s *service) deleteFile(ctx context.Context, userID uint, fileID string) (*FileDeleteResult, error) {
	f, err := s.getFile(ctx, userID, fileID)
	if err != nil {
		return nil, err
	}
	inUse, err := s.ports.Capabilities.FileInUse(ctx, userID, f.FileID)
	if err != nil {
		return nil, err
	}
	if inUse {
		return nil, ErrFileInUse
	}
	quota, err := s.removeFile(ctx, f)
	if err != nil {
		return nil, err
	}
	return &FileDeleteResult{Deleted: true, FileID: f.FileID, Quota: quota}, nil
}

// cleanupUnreferencedFiles removes files that were attached only to the given
// (already deleted) conversations. It returns the count and the new quota.
func (s *service) cleanupUnreferencedFiles(ctx context.Context, userID uint, convIDs []uint) (int, *QuotaDTO, error) {
	var fileIDs []string
	if len(convIDs) == 0 {
		return 0, nil, nil
	}
	if err := s.db.WithContext(ctx).Unscoped().Model(&Attachment{}).
		Where("user_id = ? AND conversation_id IN ? AND status <> ?", userID, convIDs, fileStatusDeleted).
		Distinct().Pluck("file_id", &fileIDs).Error; err != nil {
		return 0, nil, err
	}
	deleted := 0
	var last *QuotaDTO
	for _, id := range fileIDs {
		var refs int64
		if err := s.db.WithContext(ctx).Table("chat_attachments AS a").
			Joins("JOIN chat_conversations c ON c.id = a.conversation_id AND c.deleted_at IS NULL").
			Where("a.file_id = ? AND a.user_id = ? AND a.status <> ? AND a.deleted_at IS NULL", id, userID, fileStatusDeleted).
			Count(&refs).Error; err != nil {
			return deleted, last, err
		}
		if refs > 0 {
			continue
		}
		inUse, err := s.ports.Capabilities.FileInUse(ctx, userID, id)
		if err != nil || inUse {
			continue
		}
		f, err := s.getFile(ctx, userID, id)
		if err != nil {
			continue
		}
		q, err := s.removeFile(ctx, f)
		if err != nil {
			s.log.Warn("conversation_file_remove_failed", logx.Err(err))
			continue
		}
		deleted++
		last = q
	}
	if deleted == 0 {
		return 0, nil, nil
	}
	return deleted, last, nil
}

// copyFileFor duplicates a file object (and its bytes) for another user.
func (s *service) copyFileFor(ctx context.Context, tx *gorm.DB, src FileObject, userID uint, quotaErr error) (*FileObject, error) {
	data, err := s.readBytes(ctx, src.StoragePath)
	if err != nil {
		return nil, ErrSharedFileNotFound
	}
	// Identical content already owned by the target is reused without quota.
	var existing FileObject
	if err := tx.Where("user_id = ? AND sha256 = ? AND size_bytes = ? AND status = ?", userID, src.SHA256, src.SizeBytes, fileStatusActive).First(&existing).Error; err == nil && src.SHA256 != "" {
		return &existing, nil
	}
	if err := consumeQuota(tx, userID, src.SizeBytes, s.settings(ctx).UserStorageQuotaBytes, quotaErr); err != nil {
		return nil, err
	}
	fileID := newFileID()
	rel := path.Join(itoa(int(userID)), fileID)
	if err := s.writeBytes(ctx, rel, data); err != nil {
		return nil, err
	}
	dup := src
	dup.ID, dup.CreatedAt, dup.UpdatedAt, dup.DeletedAt = 0, src.CreatedAt, src.UpdatedAt, src.DeletedAt
	dup.FileID, dup.UserID, dup.StoragePath = fileID, userID, rel
	dup.LastAccessedAt = nil
	dup.ID = 0
	if err := tx.Create(&dup).Error; err != nil {
		s.removeBytes(ctx, rel)
		return nil, err
	}
	s.carryIndex(ctx, tx, src, &dup)
	return &dup, nil
}

// carryIndex gives a copied file the retrieval index of its source: a ready
// source with chunks is cloned (chunks and vectors) so the copy is ready at
// once. In every other case, and when cloning fails, the copy starts as a
// never-indexed file and is indexed normally later.
func (s *service) carryIndex(ctx context.Context, tx *gorm.DB, src FileObject, dup *FileObject) {
	reset := func() {
		dup.EmbedStatus, dup.EmbedSignature, dup.EmbedError, dup.ChunkCount = "none", "", "", 0
		tx.Model(&FileObject{}).Where("id = ?", dup.ID).UpdateColumns(map[string]any{
			"embed_status": "none", "embed_signature": "", "embed_error": "", "chunk_count": 0,
		})
	}
	if s.ports.Indexer == nil || src.EmbedStatus != "ready" || src.ChunkCount <= 0 {
		reset()
		return
	}
	if err := s.ports.Indexer.Clone(ctx, tx, src.ID, dup.ID); err != nil {
		s.log.Warn("conversation_index_clone_failed", logErr(err))
		reset()
		return
	}
	dup.EmbedStatus, dup.EmbedSignature, dup.ChunkCount, dup.EmbedError = "ready", src.EmbedSignature, src.ChunkCount, ""
}

// storeGenerated saves a generated media file for the user.
func (s *service) storeGenerated(ctx context.Context, tx *gorm.DB, userID uint, gf GeneratedFile) (*FileObject, error) {
	name, ok := validFileName(gf.FileName)
	if !ok {
		name = "generated-" + newPublicID()[:8]
	}
	mimeType := baseMIME(gf.MimeType)
	if mimeType == "" {
		mimeType = detectMIME(name, gf.Data, "")
	}
	sum := sha256.Sum256(gf.Data)
	if err := consumeQuota(tx, userID, int64(len(gf.Data)), s.settings(ctx).UserStorageQuotaBytes, ErrQuotaExceeded); err != nil {
		return nil, err
	}
	fileID := newFileID()
	rel := path.Join(itoa(int(userID)), fileID)
	if err := s.writeBytes(ctx, rel, gf.Data); err != nil {
		return nil, err
	}
	now := s.now()
	f := FileObject{
		FileID: fileID, UserID: userID, Purpose: "generated", FileName: name, MimeType: mimeType, DetectedMIME: mimeType,
		FileCategory: fileCategory(mimeType, name), SizeBytes: int64(len(gf.Data)), SHA256: hex.EncodeToString(sum[:]),
		StoragePath: rel, Status: fileStatusActive, ProcessingStatus: "ready", ProcessingReady: true,
		ExtractStatus: "none", EmbedStatus: "none", ProcessingStarted: &now, ProcessingDone: &now,
	}
	if err := tx.Create(&f).Error; err != nil {
		s.removeBytes(ctx, rel)
		if database.IsDuplicate(database.Translate(err)) {
			// Same bytes already stored for this user: reuse that object.
			var prev FileObject
			if e := tx.Where("user_id = ? AND sha256 = ? AND size_bytes = ? AND status = ?", userID, f.SHA256, f.SizeBytes, fileStatusActive).First(&prev).Error; e == nil {
				_ = releaseQuota(tx, userID, f.SizeBytes)
				return &prev, nil
			}
		}
		return nil, err
	}
	return &f, nil
}

// ----- policy -----

// FilePolicy is the chat file policy object.
type FilePolicy struct {
	MaxMessageFiles       int      `json:"maxMessageFiles"`
	MaxUploadFileBytes    int64    `json:"maxUploadFileBytes"`
	AllowedMIMETypes      []string `json:"allowedMIMETypes"`
	ImageMaxBytes         int64    `json:"imageMaxBytes"`
	DocMaxBytes           int64    `json:"docMaxBytes"`
	EffectiveImageMaxByte int64    `json:"effectiveImageMaxBytes"`
	EffectiveDocMaxBytes  int64    `json:"effectiveDocMaxBytes"`
	FullContextMaxBytes   int64    `json:"fullContextMaxBytes"`
	FullContextMaxTokens  int      `json:"fullContextMaxTokens"`
	FullContextPDFMaxPage int      `json:"fullContextPDFMaxPages"`
	RAGAvailable          bool     `json:"ragAvailable"`
	RAGAvailabilityReason string   `json:"ragAvailabilityReason"`
	CapabilityMode        string   `json:"capabilityMode"`
	FileMode              string   `json:"fileMode"`
}

func minPositive(a, b int64) int64 {
	switch {
	case a > 0 && b > 0:
		if a < b {
			return a
		}
		return b
	case a > 0:
		return a
	}
	return b
}

func (s *service) filePolicy(ctx context.Context, userID uint) FilePolicy {
	rs := s.settings(ctx)
	mimes := append([]string(nil), rs.AllowedMIMETypes...)
	if len(mimes) == 0 {
		mimes = append(mimes, defaultAllowedMIME...)
	}
	sort.Strings(mimes)
	mode := "full_context_only"
	if rs.RAGAvailable {
		mode = "full_context_and_rag"
	}
	fileMode := "auto"
	if v, ok, err := s.ports.Settings.UserSetting(ctx, userID, "chat.file_mode"); err == nil && ok && strings.TrimSpace(v) != "" {
		fileMode = strings.TrimSpace(v)
	}
	return FilePolicy{
		MaxMessageFiles: rs.MaxMessageFiles, MaxUploadFileBytes: rs.MaxUploadBytes, AllowedMIMETypes: mimes,
		ImageMaxBytes: rs.ImageMaxBytes, DocMaxBytes: rs.DocMaxBytes,
		EffectiveImageMaxByte: minPositive(rs.MaxUploadBytes, rs.ImageMaxBytes),
		EffectiveDocMaxBytes:  minPositive(rs.MaxUploadBytes, rs.DocMaxBytes),
		FullContextMaxBytes:   rs.FullContextMaxBytes, FullContextMaxTokens: rs.FullContextMaxTokens,
		FullContextPDFMaxPage: rs.FullContextPDFMaxPages, RAGAvailable: rs.RAGAvailable,
		RAGAvailabilityReason: rs.RAGAvailabilityReason, CapabilityMode: mode, FileMode: fileMode,
	}
}

var _ = bytes.MinRead
